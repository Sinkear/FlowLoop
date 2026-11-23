package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Sinkear/ouroflow/internal/link"
)

// TestProbeCLIsDetectsFakeCLI plants a fake `claude` executable at the front
// of PATH and verifies the probe table picks it up (real machines resolve
// the same table against their actual PATH).
func TestProbeCLIsDetectsFakeCLI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 2.1.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	clis := probeCLIs(context.Background(), nil)
	var found bool
	for _, c := range clis {
		if c.Name == "claude" {
			found = true
			if c.Version != "2.1.0" {
				t.Fatalf("expected version 2.1.0, got %q", c.Version)
			}
			if len(c.ACPSpawn) == 0 {
				t.Fatalf("expected acp spawn args")
			}
		}
	}
	if !found {
		t.Fatalf("fake claude not detected, got %+v", clis)
	}
}

// TestProbeCLIsDetectsCLIInScanDir plants a fake `claude` OUTSIDE PATH and
// verifies --scan resolves it — including the glob form (/opt/*/), and that
// the reported ACPSpawn carries the absolute executable.
func TestProbeCLIsDetectsCLIInScanDir(t *testing.T) {
	// A restricted PATH (only a shell) forces the PATH probe to miss every
	// CLI — the scan dirs must resolve them.
	bin := t.TempDir()
	if err := os.Symlink("/bin/sh", filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	base := t.TempDir()
	for _, sub := range []string{"alpha", "beta"} {
		dir := filepath.Join(base, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho 9.9.9\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	clis := probeCLIs(context.Background(), []string{filepath.Join(base, "*")})
	var found bool
	for _, c := range clis {
		if c.Name == "claude" {
			found = true
			if c.ACPSpawn[0] != filepath.Join(base, "alpha", "claude") && c.ACPSpawn[0] != filepath.Join(base, "beta", "claude") {
				t.Fatalf("expected absolute spawn path from a scan dir, got %q", c.ACPSpawn[0])
			}
		}
	}
	if !found {
		t.Fatalf("fake claude in a --scan glob not detected, got %+v", clis)
	}
}

// TestScanDirsFor covers the --scan expansion rules: plain paths pass
// through, globs resolve to their DIRECTORY matches only.
func TestScanDirsFor(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	fileB := filepath.Join(base, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := scanDirsFor(filepath.Join(base, "*"))
	if len(got) != 1 || got[0] != dirA {
		t.Fatalf("glob must keep only directories, got %v", got)
	}
	if plain := scanDirsFor(dirA); len(plain) != 1 || plain[0] != dirA {
		t.Fatalf("plain path must pass through, got %v", plain)
	}
	// ** recurses: a nested directory two levels down still matches.
	deep := filepath.Join(base, "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	deepGot := scanDirsFor(filepath.Join(base, "**"))
	found := false
	for _, d := range deepGot {
		if d == deep {
			found = true
		}
	}
	if !found {
		t.Fatalf("** must recurse into nested directories, got %v", deepGot)
	}
}

// TestConnectWSURL covers the server-flag → /connect WebSocket URL mapping.
func TestConnectWSURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:7373", "ws://127.0.0.1:7373/connect"},
		{"https://ouroflow.example.com", "wss://ouroflow.example.com/connect"},
		{"192.168.1.5:7373", "ws://192.168.1.5:7373/connect"},
	}
	for _, c := range cases {
		got := connectWSURL(c.in, "")
		if got != c.want {
			t.Fatalf("connectWSURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := connectWSURL("http://x", "tok"); got != "ws://x/connect?token=tok" {
		t.Fatalf("token must ride the query, got %q", got)
	}
}

// resetMergedProbeTable clears the package-level merged table after a test
// that set it, so later tests fall back to the builtin table.
func resetMergedProbeTable(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		probeTableMu.Lock()
		mergedProbeTable = nil
		probeTableMu.Unlock()
	})
}

// TestMergeProbeTablesOverride: a custom entry with a builtin's Name
// overrides it (spawn/dirs change); the builtin's other fields are replaced
// wholesale by the custom entry.
func TestMergeProbeTablesOverride(t *testing.T) {
	custom := []probeEntry{{
		Name: "claude", ProbeCmd: "claude --version",
		ACPSpawn:         []string{"/opt/claude/claude", "--acp"},
		SkillsDir:        "~/.claude/skills",
		ProjectSkillsDir: ".claude/skills",
		ProfileFiles:     []string{"AGENTS.md"},
	}}
	merged := mergeProbeTables(builtinProbeTable, custom)
	var claude probeEntry
	for _, e := range merged {
		if e.Name == "claude" {
			claude = e
		}
	}
	if claude.ACPSpawn[0] != "/opt/claude/claude" {
		t.Fatalf("override must replace ACPSpawn, got %v", claude.ACPSpawn)
	}
	if !slices.Equal(claude.ProfileFiles, []string{"AGENTS.md"}) {
		t.Fatalf("override must replace ProfileFiles, got %v", claude.ProfileFiles)
	}
}

// TestMergeProbeTablesAdd: a custom entry with a new Name is appended.
func TestMergeProbeTablesAdd(t *testing.T) {
	custom := []probeEntry{{
		Name: "myagent", ProbeCmd: "myagent --version",
		ACPSpawn: []string{"myagent", "acp"},
	}}
	merged := mergeProbeTables(builtinProbeTable, custom)
	if !slices.ContainsFunc(merged, func(e probeEntry) bool { return e.Name == "myagent" }) {
		t.Fatalf("new custom entry must be appended, got %v", merged)
	}
}

// TestMergeProbeTablesBuiltinUnchanged: a builtin not mentioned in custom
// keeps its original values. Uses the FIRST builtin (whatever it is) so the
// test does not break when the builtin table changes.
func TestMergeProbeTablesBuiltinUnchanged(t *testing.T) {
	if len(builtinProbeTable) == 0 {
		t.Fatal("builtinProbeTable must not be empty")
	}
	first := builtinProbeTable[0]
	custom := []probeEntry{{Name: "myagent", ProbeCmd: "x", ACPSpawn: []string{"x"}}}
	merged := mergeProbeTables(builtinProbeTable, custom)
	var got probeEntry
	for _, e := range merged {
		if e.Name == first.Name {
			got = e
		}
	}
	if !slices.Equal(got.ACPSpawn, first.ACPSpawn) {
		t.Fatalf("unmentioned builtin %s must keep its spawn %v, got %v", first.Name, first.ACPSpawn, got.ACPSpawn)
	}
}

// TestMergeProbeTablesOrdering: builtins first (original order), then new
// custom entries (config-file order). Derives expectations from
// builtinProbeTable so it survives table changes.
func TestMergeProbeTablesOrdering(t *testing.T) {
	custom := []probeEntry{
		{Name: "zagent", ProbeCmd: "z", ACPSpawn: []string{"z"}},
		{Name: "aagent", ProbeCmd: "a", ACPSpawn: []string{"a"}},
	}
	merged := mergeProbeTables(builtinProbeTable, custom)
	// Builtins come first in their original order.
	for i, want := range builtinProbeTable {
		if merged[i].Name != want.Name {
			t.Fatalf("builtin %d must be %s, got %s (merged=%v)", i, want.Name, merged[i].Name, merged)
		}
	}
	// New entries follow in config-file order (zagent before aagent).
	offset := len(builtinProbeTable)
	if len(merged) != offset+2 || merged[offset].Name != "zagent" || merged[offset+1].Name != "aagent" {
		t.Fatalf("new entries must follow in config order, got %v", merged)
	}
}

// TestLoadAgentsConfigValid: a valid YAML produces the right entries.
func TestLoadAgentsConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: myagent
    probe_cmd: "myagent --version"
    acp_spawn: ["myagent", "acp"]
    skills_dir: "~/.myagent/skills"
    project_skills_dir: ".myagent/skills"
    profile_files: ["AGENTS.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := loadAgentsConfig(path)
	if err != nil {
		t.Fatalf("valid config must load, got %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "myagent" {
		t.Fatalf("expected one myagent entry, got %v", entries)
	}
	if entries[0].ProjectSkillsDir != ".myagent/skills" {
		t.Fatalf("project_skills_dir must parse, got %q", entries[0].ProjectSkillsDir)
	}
}

// TestLoadAgentsConfigMalformedEntry: entries missing required fields are
// skipped; valid entries in the same file are still returned.
func TestLoadAgentsConfigMalformedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: no-probe
    acp_spawn: ["x"]
  - name: no-spawn
    probe_cmd: "x --version"
  - probe_cmd: "x"
    acp_spawn: ["x"]
  - name: good
    probe_cmd: "good --version"
    acp_spawn: ["good", "acp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := loadAgentsConfig(path)
	if err != nil {
		t.Fatalf("load must not fail on malformed entries, got %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "good" {
		t.Fatalf("only the valid entry must remain, got %v", entries)
	}
}

// TestLoadAgentsConfigMissing: a missing file returns an error the caller
// can distinguish (os.IsNotExist).
func TestLoadAgentsConfigMissing(t *testing.T) {
	_, err := loadAgentsConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("missing file must return os.IsNotExist error, got %v", err)
	}
}

// TestActiveProbeTableFallback: nil mergedProbeTable → builtin; set → merged.
func TestActiveProbeTableFallback(t *testing.T) {
	resetMergedProbeTable(t)
	got := activeProbeTable()
	if len(got) != len(builtinProbeTable) {
		t.Fatalf("nil merged must fall back to builtin (len %d), got %v", len(builtinProbeTable), got)
	}
	for i, e := range builtinProbeTable {
		if got[i].Name != e.Name {
			t.Fatalf("nil merged must return builtin[%d]=%s, got %s", i, e.Name, got[i].Name)
		}
	}
	mergedProbeTable = []probeEntry{{Name: "only-custom", ProbeCmd: "x", ACPSpawn: []string{"x"}}}
	if got := activeProbeTable(); len(got) != 1 || got[0].Name != "only-custom" {
		t.Fatalf("set merged must return it, got %v", got)
	}
}

// TestProjectSkillsDirsWithCustom: after setMergedProbeTable adds a custom
// entry with a new ProjectSkillsDir, projectSkillsDirs() includes it (the
// git-exclude set must cover custom-CLI skills).
func TestProjectSkillsDirsWithCustom(t *testing.T) {
	resetMergedProbeTable(t)
	custom := []probeEntry{{
		Name: "myagent", ProbeCmd: "myagent --version",
		ACPSpawn:         []string{"myagent", "acp"},
		ProjectSkillsDir: ".myagent/skills",
	}}
	mergedProbeTable = mergeProbeTables(builtinProbeTable, custom)
	dirs := projectSkillsDirs()
	if !slices.Contains(dirs, ".myagent/skills") {
		t.Fatalf("custom ProjectSkillsDir must be in the exclude set, got %v", dirs)
	}
	// Builtins' dirs still present.
	if !slices.Contains(dirs, ".claude/skills") {
		t.Fatalf("builtin ProjectSkillsDir must still be present, got %v", dirs)
	}
}

// TestProbeCLIsWithCustomEntry: a custom entry whose ProbeCmd points at a
// fake script on PATH is probed and returns its custom ACPSpawn.
func TestProbeCLIsWithCustomEntry(t *testing.T) {
	resetMergedProbeTable(t)
	// A fake 'myagent' on PATH (prepend so sh stays resolvable for sh -c).
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "myagent"), []byte("#!/bin/sh\necho 1.2.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	mergedProbeTable = []probeEntry{{
		Name: "myagent", ProbeCmd: "myagent --version",
		ACPSpawn:         []string{"myagent", "acp"},
		ProjectSkillsDir: ".myagent/skills",
	}}
	clis := probeCLIs(context.Background(), nil)
	var found bool
	for _, c := range clis {
		if c.Name == "myagent" {
			found = true
			if c.Version != "1.2.3" {
				t.Fatalf("version must be probed, got %q", c.Version)
			}
			if !slices.Equal(c.ACPSpawn, []string{"myagent", "acp"}) {
				t.Fatalf("custom ACPSpawn must be carried, got %v", c.ACPSpawn)
			}
		}
	}
	if !found {
		t.Fatalf("custom entry must be probed, got %+v", clis)
	}
}

// TestSetMergedProbeTableMissingFile: a missing config path leaves the
// merged table nil (activeProbeTable falls back to builtins), no error.
func TestSetMergedProbeTableMissingFile(t *testing.T) {
	resetMergedProbeTable(t)
	if err := setMergedProbeTable(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Fatalf("missing config must not error, got %v", err)
	}
	if mergedProbeTable != nil {
		t.Fatalf("missing config must leave merged table nil, got %v", mergedProbeTable)
	}
}

// TestSetMergedProbeTableHotReloadClearsOnDelete: the hot-reload contract.
// After a valid config sets mergedProbeTable, DELETING (or emptying) the file
// on a later reload must clear mergedProbeTable back to nil — so the builtin
// table is restored without restarting connect. A stale custom table must NOT
// survive the config's removal.
func TestSetMergedProbeTableHotReloadClearsOnDelete(t *testing.T) {
	resetMergedProbeTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: myagent
    probe_cmd: "myagent --version"
    acp_spawn: ["myagent", "acp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if mergedProbeTable == nil {
		t.Fatal("merged table must be set after a valid config")
	}
	// Delete the file → next reload must clear the cached table.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("reload after delete must not error, got %v", err)
	}
	if mergedProbeTable != nil {
		t.Fatalf("deleting config must clear merged table (restore builtins), got %v", mergedProbeTable)
	}
}

// TestSetMergedProbeTableHotReloadClearsOnEmpty: same contract, but the file
// is EMPTIED (zero valid entries) rather than deleted — also must clear.
func TestSetMergedProbeTableHotReloadClearsOnEmpty(t *testing.T) {
	resetMergedProbeTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: myagent
    probe_cmd: "myagent --version"
    acp_spawn: ["myagent", "acp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	// Rewrite to an empty config → reload must clear.
	if err := os.WriteFile(path, []byte("agents: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("reload after empty must not error, got %v", err)
	}
	if mergedProbeTable != nil {
		t.Fatalf("emptying config must clear merged table, got %v", mergedProbeTable)
	}
}

// TestSetMergedProbeTableHotReloadKeepsPreviousOnParseError: a corrupted
// (unparseable) file on reload must NOT clear a previously loaded table — a
// half-edited config shouldn't drop a working one. The error is returned so
// the caller can warn, but mergedProbeTable stays as it was.
func TestSetMergedProbeTableHotReloadKeepsPreviousOnParseError(t *testing.T) {
	resetMergedProbeTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: myagent
    probe_cmd: "myagent --version"
    acp_spawn: ["myagent", "acp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("initial load: %v", err)
	}
	prev := mergedProbeTable
	if prev == nil {
		t.Fatal("merged table must be set after a valid config")
	}
	// Corrupt the file with invalid YAML → reload must error but NOT clear.
	if err := os.WriteFile(path, []byte("agents: [this is not: valid: yaml: ]:\n  - oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err == nil {
		t.Fatal("corrupted config must return an error")
	}
	// Same identity (not cleared, not rebuilt) — the previous table survives.
	if len(mergedProbeTable) != len(prev) {
		t.Fatalf("parse error must keep the previous table (len %d), got %v", len(prev), mergedProbeTable)
	}
	hasMyAgent := false
	for _, e := range mergedProbeTable {
		if e.Name == "myagent" {
			hasMyAgent = true
		}
	}
	if !hasMyAgent {
		t.Fatalf("previous custom entry must survive a parse error, got %v", mergedProbeTable)
	}
}

// TestSetMergedProbeTableLoadsAndMerges: a real config file is loaded and
// merged into mergedProbeTable.
func TestSetMergedProbeTableLoadsAndMerges(t *testing.T) {
	resetMergedProbeTable(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.yaml")
	if err := os.WriteFile(path, []byte(`agents:
  - name: myagent
    probe_cmd: "myagent --version"
    acp_spawn: ["myagent", "acp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setMergedProbeTable(path); err != nil {
		t.Fatalf("load must succeed, got %v", err)
	}
	if mergedProbeTable == nil {
		t.Fatal("merged table must be set after a valid config")
	}
	if !slices.ContainsFunc(mergedProbeTable, func(e probeEntry) bool { return e.Name == "myagent" }) {
		t.Fatalf("custom entry must be in merged table, got %v", mergedProbeTable)
	}
	if !slices.ContainsFunc(mergedProbeTable, func(e probeEntry) bool { return e.Name == "claude" }) {
		t.Fatalf("builtins must still be present, got %v", mergedProbeTable)
	}
}

// ── model detection ──

// TestParseModelsOutputLines: one model id per line, trimmed, comments and
// headers skipped, duplicates dropped.
func TestParseModelsOutputLines(t *testing.T) {
	raw := "Available models:\n# comment\nclaude-sonnet-4\n  claude-opus-4  \n\nclaude-sonnet-4\n"
	got := parseModelsOutput(raw)
	want := []string{"claude-sonnet-4", "claude-opus-4"}
	if len(got) != len(want) {
		t.Fatalf("expected %d models, got %+v", len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("model %d: expected %q, got %q", i, id, got[i].ID)
		}
	}
}

// TestParseModelsOutputJSONArray: a JSON array is accepted — both string
// entries and {id|modelId,name,description} objects.
func TestParseModelsOutputJSONArray(t *testing.T) {
	got := parseModelsOutput(`[
	  {"id":"m1","name":"Model One","description":"first"},
	  {"modelId":"m2","name":"Model Two"},
	  "m3",
	  {"id":"m1","name":"dup"}
	]`)
	if len(got) != 3 {
		t.Fatalf("expected 3 models, got %+v", got)
	}
	if got[0].ID != "m1" || got[0].Name != "Model One" || got[0].Description != "first" {
		t.Fatalf("object model mismatch: %+v", got[0])
	}
	if got[1].ID != "m2" || got[1].Name != "Model Two" {
		t.Fatalf("modelId fallback mismatch: %+v", got[1])
	}
	if got[2].ID != "m3" {
		t.Fatalf("string entry mismatch: %+v", got[2])
	}

	// A malformed JSON array falls back to line parsing (the whole text is
	// then one model id) rather than dropping the detection.
	if models := parseModelsOutput("[\nnot-json\n"); len(models) == 0 {
		t.Fatalf("malformed JSON must fall back to line parsing, got %+v", models)
	}
	if models := parseModelsOutput("   "); len(models) != 0 {
		t.Fatalf("blank output must yield no models, got %+v", models)
	}
}

// TestParseACPModels covers the three accepted session/new result shapes:
// result.models.availableModels, result.models array, and top-level
// availableModels.
func TestParseACPModels(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   []string
	}{
		{
			name:   "models.availableModels",
			result: `{"sessionId":"s1","models":{"availableModels":[{"modelId":"a","name":"A","description":"da"}]}}`,
			want:   []string{"a"},
		},
		{
			name:   "models array",
			result: `{"sessionId":"s1","models":[{"id":"b","name":"B"}]}`,
			want:   []string{"b"},
		},
		{
			name:   "top-level availableModels",
			result: `{"sessionId":"s1","availableModels":[{"modelId":"c"},{"modelId":"d"}]}`,
			want:   []string{"c", "d"},
		},
		{
			// Two explicit shapes at once: the first present (result.models)
			// wins; the parser must not union them.
			name:   "models wins over top-level availableModels",
			result: `{"models":{"availableModels":[{"modelId":"a"}]},"availableModels":[{"modelId":"b"}]}`,
			want:   []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseACPModels([]byte(tc.result))
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d models, got %+v", len(tc.want), got)
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Fatalf("model %d: expected %q, got %q", i, id, got[i].ID)
				}
			}
		})
	}
	if got := parseACPModels([]byte(`{"sessionId":"s1"}`)); len(got) != 0 {
		t.Fatalf("a result without models must yield none, got %+v", got)
	}
	if got := parseACPModels(nil); len(got) != 0 {
		t.Fatalf("nil result must yield none, got %+v", got)
	}
}

// TestParseACPModelsConfigOptions covers the configOptions shape `opencode
// acp` (verified 2.2.5) returns when it does not provide an explicit model
// list: [{id:"model",category:"model",type:"select",
// options:[{value,name,description}]}].
func TestParseACPModelsConfigOptions(t *testing.T) {
	// id=="model" selector: value/name/description become the ModelInfo.
	got := parseACPModels([]byte(`{
	  "sessionId":"s1",
	  "configOptions":[
	    {"id":"model","category":"model","type":"select","currentValue":"opencode/sonnet",
	     "options":[
	       {"value":"opencode/sonnet","name":"Sonnet"},
	       {"value":"opencode/opus","name":"Opus","description":"deep"}
	     ]},
	    {"id":"mode","category":"mode","type":"select","options":[{"value":"plan","name":"Plan"}]},
	    {"id":"model_toggle","category":"model","type":"boolean"}
	  ]}`))
	if len(got) != 2 {
		t.Fatalf("expected 2 model options (non-model configs ignored), got %+v", got)
	}
	if got[0].ID != "opencode/sonnet" || got[0].Name != "Sonnet" || got[0].Description != "" {
		t.Fatalf("first option mismatch: %+v", got[0])
	}
	if got[1].ID != "opencode/opus" || got[1].Name != "Opus" || got[1].Description != "deep" {
		t.Fatalf("second option mismatch: %+v", got[1])
	}

	// category=="model" alone is enough (the id is implementation-defined).
	got = parseACPModels([]byte(`{"configOptions":[{"id":"primary","category":"model","type":"select","options":[{"value":"h3","name":"Hermes 3"}]}]}`))
	if len(got) != 1 || got[0].ID != "h3" || got[0].Name != "Hermes 3" {
		t.Fatalf("category-only model option mismatch: %+v", got)
	}

	// Both shapes present: an explicit model list is the CLI's actual
	// selector and wins outright — configOptions (its whole registry) is
	// NOT merged in, so models the CLI cannot currently run do not leak in.
	got = parseACPModels([]byte(`{
	  "models":{"availableModels":[{"modelId":"m1","name":"from models"}]},
	  "configOptions":[{"id":"model","type":"select","options":[
	    {"value":"m1","name":"from configOptions"},{"value":"m2","name":"only config"}]}]}`))
	if len(got) != 1 {
		t.Fatalf("explicit models must win without configOptions union, got %+v", got)
	}
	if got[0].ID != "m1" || got[0].Name != "from models" {
		t.Fatalf("explicit model list should win: %+v", got[0])
	}
}

// TestFilterModelsByAuth covers the mixed-provider defense: a CLI that
// advertises a catalog for several provider namespaces while only one
// provider has a credential must report only the credentialed namespace.
// When auth ids don't describe the catalog (opencode's slash ids) or no
// non-terminal auth exists, the list is returned untouched.
func TestFilterModelsByAuth(t *testing.T) {
	init := []byte(`{"authMethods":[{"id":"opencode-zen"},{"id":"hermes-setup","type":"terminal"}]}`)
	models := []link.ModelInfo{
		{ID: "opencode-zen:claude-sonnet-4"},
		{ID: "openrouter:some-model"},
		{ID: "opencode-zen:gpt-5"},
	}
	got := filterModelsByAuth(models, init)
	if len(got) != 2 || got[0].ID != "opencode-zen:claude-sonnet-4" || got[1].ID != "opencode-zen:gpt-5" {
		t.Fatalf("only credentialed provider namespace must survive, got %+v", got)
	}

	// Slash-namespaced ids (opencode) never match auth ids → untouched.
	slash := []link.ModelInfo{{ID: "deepseek/deepseek-v4"}, {ID: "magpie/opencode/glm"}}
	if got := filterModelsByAuth(slash, init); len(got) != 2 {
		t.Fatalf("slash-namespaced catalog must be untouched, got %+v", got)
	}
	// No colon at all → untouched.
	if got := filterModelsByAuth([]link.ModelInfo{{ID: "plain-model"}}, init); len(got) != 1 {
		t.Fatalf("un-namespaced catalog must be untouched, got %+v", got)
	}
	// Mixed catalog: a plain id has no provider evidence and must survive
	// even though the colon-namespaced filtering is active.
	mixed := []link.ModelInfo{{ID: "opencode-zen:good"}, {ID: "plain-model"}, {ID: "openrouter:nokey"}}
	if got := filterModelsByAuth(mixed, init); len(got) != 2 || got[0].ID != "opencode-zen:good" || got[1].ID != "plain-model" {
		t.Fatalf("mixed catalog must keep the authed namespace and the plain id, got %+v", got)
	}
	// Terminal-only or absent auth → no provider ids to filter by → untouched.
	if got := filterModelsByAuth(models, []byte(`{"authMethods":[{"id":"setup","type":"terminal"}]}`)); len(got) != 3 {
		t.Fatalf("terminal-only auth must not reach the filter, got %+v", got)
	}
	if got := filterModelsByAuth(models, nil); len(got) != 3 {
		t.Fatalf("nil initialize must be untouched, got %+v", got)
	}
}

// TestProbeModelsViaACPFilterUncredentialedProviders: end-to-end through the
// ACP handshake — initialize advertises one provider credential, session/new
// returns models for it plus another provider → only the credentialed ones
// are reported.
func TestProbeModelsViaACPFilterUncredentialedProviders(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mixed-acp")
	body := `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"opencode-zen"},{"id":"setup","type":"terminal"}]}}'
read line
echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1","models":{"availableModels":[{"modelId":"opencode-zen:good"},{"modelId":"openrouter:nokey"}]}}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := probeModelsViaACP(context.Background(), []string{script})
	if !ok {
		t.Fatal("mixed-provider probe must complete its handshake")
	}
	if len(got) != 1 || got[0].ID != "opencode-zen:good" {
		t.Fatalf("no-key provider models must be dropped, got %+v", got)
	}
}

// TestProbeModelsViaACPTerminalOnlyAuthStillProbesSession: a terminal-only
// authMethods advertisement must NOT short-circuit detection. CLIs can
// authenticate from env/config while advertising only terminal setup; the
// session/new outcome is the authoritative answer.
func TestProbeModelsViaACPTerminalOnlyAuthStillProbesSession(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "session-new-called")
	script := filepath.Join(dir, "env-authed-acp")
	body := `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"setup","type":"terminal"}]}}'
read line
echo "$line" > "` + marker + `"
echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1","models":{"availableModels":[{"modelId":"env-authed-model","name":"Env Authed"}]}}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := probeModelsViaACP(context.Background(), []string{script})
	if !ok {
		t.Fatal("a successful session/new must report ok even with terminal-only authMethods")
	}
	if len(got) != 1 || got[0].ID != "env-authed-model" {
		t.Fatalf("env-authenticated CLI models must be reported, got %+v", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("session/new must be called regardless of authMethods shape")
	}
}

// TestProbeModelsViaACPSessionErrorNotOK: a JSON-RPC error from session/new
// is an infrastructure/probe failure (ok=false), not an authoritative empty
// catalog — refresh must not clear the persisted model list because of it.
func TestProbeModelsViaACPSessionErrorNotOK(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "failing-acp")
	body := `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
read line
echo '{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"Internal error","data":{"details":"No LLM provider configured"}}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	models, ok := probeModelsViaACP(context.Background(), []string{script})
	if ok {
		t.Fatalf("session/new error must report ok=false, got models %+v", models)
	}
	if len(models) != 0 {
		t.Fatalf("failed handshake must not yield models, got %+v", models)
	}
}

// TestDetectCLIModelsUsesModelsCmd: a fake CLI whose `models` subcommand
// prints ids → detection parses them (and the cache serves the second call).
func TestDetectCLIModelsUsesModelsCmd(t *testing.T) {
	resetModelsCache()
	dir := t.TempDir()
	script := filepath.Join(dir, "fakecli")
	body := "#!/bin/sh\nif [ \"$1\" = \"models\" ]; then echo one; echo two; else echo 1.0.0; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeEntry{Name: "fakecli", ProbeCmd: "fakecli --version", ModelsCmd: "fakecli models", ACPSpawn: []string{script, "acp"}}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := detectCLIModels(context.Background(), e, e.ACPSpawn, "")
	if len(got) != 2 || got[0].ID != "one" || got[1].ID != "two" {
		t.Fatalf("models_cmd detection mismatch: %+v", got)
	}
	// Second call is served from the cache (remove the script → same result).
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if cached, ok := cachedModels("fakecli"); !ok || len(cached) != 2 {
		t.Fatalf("detection must be cached, got %+v (ok=%v)", cached, ok)
	}
}

// TestRefreshCLIModelsBypassesCache: the manual refresh path must re-run the
// probe even when a fresh (non-expired) value sits in the 30-minute cache —
// otherwise clicking 「刷新检测」 right after detection would change nothing.
func TestRefreshCLIModelsBypassesCache(t *testing.T) {
	resetModelsCache()
	dir := t.TempDir()
	counter := filepath.Join(dir, "n")
	script := filepath.Join(dir, "fakecli")
	body := "#!/bin/sh\nn=$(cat '" + counter + "' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '" + counter + "'\necho model-$n\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeEntry{Name: "refreshcli", ModelsCmd: script}
	ctx := context.Background()
	if got := detectCLIModels(ctx, e, nil, ""); len(got) != 1 || got[0].ID != "model-1" {
		t.Fatalf("first detection mismatch: %+v", got)
	}
	// Cached: the script must not run a second time.
	if got := detectCLIModels(ctx, e, nil, ""); len(got) != 1 || got[0].ID != "model-1" {
		t.Fatalf("second detection must be cached, got %+v", got)
	}
	if b, _ := os.ReadFile(counter); string(b) != "1\n" {
		t.Fatalf("cached call must not re-probe, counter=%q", b)
	}
	if got, err := refreshCLIModels(ctx, e, nil, ""); err != nil || len(got) != 1 || got[0].ID != "model-2" {
		t.Fatalf("refresh must bypass the cache, got %+v (err=%v)", got, err)
	}
	// The refreshed value replaces the cached one for subsequent reads.
	if got, ok := cachedModels("refreshcli"); !ok || len(got) != 1 || got[0].ID != "model-2" {
		t.Fatalf("refresh must store its result, got %+v (ok=%v)", got, ok)
	}
}

// TestDetectCLIModelsACPFallback: no models_cmd → the CLI is spawned as an
// ACP stdio server; initialize + session/new replies carry the models.
func TestDetectCLIModelsACPFallback(t *testing.T) {
	resetModelsCache()
	dir := t.TempDir()
	script := filepath.Join(dir, "acpcli")
	body := `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
read line
echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1","models":{"availableModels":[{"modelId":"acp-model","name":"ACP Model"}]}}}'
read line
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeEntry{Name: "acpcli", ProbeCmd: "acpcli --version", ACPSpawn: []string{script}}
	got := detectCLIModels(context.Background(), e, e.ACPSpawn, "")
	if len(got) != 1 || got[0].ID != "acp-model" || got[0].Name != "ACP Model" {
		t.Fatalf("ACP fallback detection mismatch: %+v", got)
	}
}

// TestDetectCLIModelsACPFallbackConfigOptions: an ACP-only CLI (hermes-acp,
// which has no models_cmd) advertises its model selector as session/new
// result.configOptions — the opencode shape. Detection must cover it too,
// including option names/descriptions, and cache the result.
func TestDetectCLIModelsACPFallbackConfigOptions(t *testing.T) {
	resetModelsCache()
	dir := t.TempDir()
	script := filepath.Join(dir, "hermes-acp")
	body := `#!/bin/sh
read line
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
read line
echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1","configOptions":[{"id":"model","category":"model","type":"select","currentValue":"hermes-3","options":[{"value":"hermes-3","name":"Hermes 3"},{"value":"hermes-4","name":"Hermes 4","description":"newer"}]}]}}'
read line
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeEntry{Name: "hermes", ProbeCmd: "hermes-acp --version", ACPSpawn: []string{script}}
	got := detectCLIModels(context.Background(), e, e.ACPSpawn, "")
	if len(got) != 2 {
		t.Fatalf("configOptions ACP detection mismatch: %+v", got)
	}
	if got[0].ID != "hermes-3" || got[0].Name != "Hermes 3" {
		t.Fatalf("first configOption model mismatch: %+v", got[0])
	}
	if got[1].ID != "hermes-4" || got[1].Name != "Hermes 4" || got[1].Description != "newer" {
		t.Fatalf("second configOption model mismatch: %+v", got[1])
	}
	// Cached like every other detection.
	if cached, ok := cachedModels("hermes"); !ok || len(cached) != 2 {
		t.Fatalf("configOptions detection must be cached, got %+v (ok=%v)", cached, ok)
	}
}

// TestProbeModelsViaACPRealOpencode exercises the introspection against the
// real `opencode acp` when the binary is installed (skipped otherwise — no
// network requirement). It asserts only that advertised ids are well-formed;
// an unauthenticated install may advertise none, which is not a failure.
func TestProbeModelsViaACPRealOpencode(t *testing.T) {
	path, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, ok := probeModelsViaACP(ctx, []string{path, "acp"})
	if !ok {
		t.Log("opencode acp handshake failed (unauthenticated/offline install?) — parse shapes covered by the fake-agent tests")
		return
	}
	for _, m := range models {
		if m.ID == "" {
			t.Fatalf("advertised model has an empty id: %+v", m)
		}
	}
	if len(models) == 0 {
		t.Log("opencode acp advertised no models (unauthenticated install?) — parse shapes covered by the fake-agent tests")
	}
}

// TestDetectCLIModelsFailureIsEmpty: a CLI that yields nothing (bad models
// subcommand, dead ACP spawn) reports an empty list and does not error — but
// the failure must NOT be cached as a real "no models" answer.
func TestDetectCLIModelsFailureIsEmpty(t *testing.T) {
	resetModelsCache()
	e := probeEntry{Name: "missing", ProbeCmd: "missing --version", ModelsCmd: "missing models", ACPSpawn: []string{"missing", "acp"}}
	got := detectCLIModels(context.Background(), e, e.ACPSpawn, "")
	if got == nil || len(got) != 0 {
		t.Fatalf("failed detection must return an empty slice, got %+v", got)
	}
	if _, ok := cachedModels("missing"); ok {
		t.Fatal("a failed detection must not be cached")
	}
}

// TestDetectCLIModelsRetriesAfterFailure: a failing models_cmd must be re-run
// on the next probe tick instead of being pinned for modelsCacheTTL.
func TestDetectCLIModelsRetriesAfterFailure(t *testing.T) {
	resetModelsCache()
	dir := t.TempDir()
	counter := filepath.Join(dir, "n")
	script := filepath.Join(dir, "badcli")
	body := "#!/bin/sh\nn=$(cat '" + counter + "' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '" + counter + "'\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	e := probeEntry{Name: "badcli", ModelsCmd: script}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if got := detectCLIModels(ctx, e, nil, ""); len(got) != 0 {
			t.Fatalf("call %d: expected no models, got %+v", i, got)
		}
	}
	if b, _ := os.ReadFile(counter); string(b) != "2\n" {
		t.Fatalf("failed detection must be retried, counter=%q", b)
	}
	if _, ok := cachedModels("badcli"); ok {
		t.Fatal("a failed detection must not be cached")
	}
}

// TestRefreshCLIModelsFailureReturnsError: the manual refresh path must
// surface probe failure rather than store/return an empty authoritative list
// (M1: a transient failure must never clear the persisted catalog).
func TestRefreshCLIModelsFailureReturnsError(t *testing.T) {
	resetModelsCache()
	e := probeEntry{Name: "failing", ModelsCmd: "exit 7"}
	got, err := refreshCLIModels(context.Background(), e, nil, "")
	if err == nil {
		t.Fatalf("refresh must return an error on probe failure, got models %+v", got)
	}
	if _, ok := cachedModels("failing"); ok {
		t.Fatal("a failed refresh must not populate the cache")
	}
}

// TestProbeCLIsReportsModels: probeCLIs attaches detected models to the
// reported ProbeCLI (the wire field the daemon persists).
func TestProbeCLIsReportsModels(t *testing.T) {
	resetModelsCache()
	resetMergedProbeTable(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nif [ \"$1\" = \"models\" ]; then echo claude-sonnet-4; else echo 2.1.0; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	clis := probeCLIs(context.Background(), nil)
	for _, c := range clis {
		if c.Name != "claude" {
			continue
		}
		if len(c.Models) != 1 || c.Models[0].ID != "claude-sonnet-4" {
			t.Fatalf("probe report must carry detected models, got %+v", c.Models)
		}
		return
	}
	t.Fatalf("fake claude not detected, got %+v", clis)
}
