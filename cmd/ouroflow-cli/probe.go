package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sinkear/ouroflow/internal/link"
	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// probeTimeout bounds a single probe command (a hung version check must
// not stall registration). Derived from the caller's ctx so Ctrl+C also
// aborts probing.
const probeTimeout = 5 * time.Second

// modelsTimeout bounds one model detection (models_cmd run or the ACP
// initialize+session/new introspection). Longer than probeTimeout because
// spawning a real ACP server takes seconds, but still short enough that a
// hung CLI never stalls the probe report (detections run concurrently).
const modelsTimeout = 15 * time.Second

// modelsCacheTTL is how long a CLI's detected model list is reused. Probe
// ticks run about once a minute; re-probing every models_cmd / spawning an
// ACP server for every CLI each tick would be pure waste.
const modelsCacheTTL = 30 * time.Minute

// probeEntry is one agent CLI probe-table entry (builtin or user-configured
// via ~/.ouroflow/agents.yaml). Each entry maps a CLI to: the probe command
// (runs fast and prints a version), how to start it as an ACP stdio server,
// and where its skills / profile files live (config push lands the skills).
// ProjectSkillsDir is the relative directory inside a run workdir where the
// CLI loads PROJECT-level skills — the executor stages the agent's skills
// there and the commit excludes every probed dir.
type probeEntry struct {
	Name             string   `yaml:"name"`
	ProbeCmd         string   `yaml:"probe_cmd"`          // run with sh -c; success = installed
	ModelsCmd        string   `yaml:"models_cmd"`         // run with sh -c; prints available models (best effort)
	ACPSpawn         []string `yaml:"acp_spawn"`          // how to start it as an ACP stdio server
	SkillsDir        string   `yaml:"skills_dir"`         // ~ expanded (global skills)
	ProjectSkillsDir string   `yaml:"project_skills_dir"` // relative, inside a run workdir
	ProfileFiles     []string `yaml:"profile_files"`
}

// agentsConfig is the top-level shape of the ~/.ouroflow/agents.yaml file.
type agentsConfig struct {
	Agents []probeEntry `yaml:"agents"`
}

// builtinProbeTable is the hardcoded registry of agent CLIs the CLI knows
// how to detect (CLI 分支 Phase 1). A user's agents.yaml can ADD entries
// (new CLIs) or OVERRIDE these by Name — see mergeProbeTables.
var builtinProbeTable = []probeEntry{
	{
		Name:             "claude",
		ProbeCmd:         "claude --version",
		ModelsCmd:        "claude models", // best effort; ACP fallback covers a missing subcommand
		ACPSpawn:         []string{"claude", "--acp"},
		SkillsDir:        "~/.claude/skills",
		ProjectSkillsDir: ".claude/skills",
		ProfileFiles:     []string{"CLAUDE.md", "AGENTS.md"},
	},
	{
		Name:      "opencode",
		ProbeCmd:  "opencode --version",
		ModelsCmd: "opencode models", // prints one model id per line
		// v2.2.5 rejects the unknown --pure flag (prints usage, exits → ACP
		// "transport closed"). Plain `opencode acp` is the correct spawn.
		ACPSpawn:         []string{"opencode", "acp"},
		SkillsDir:        "~/.config/opencode/skills",
		ProjectSkillsDir: ".opencode/skill",
		ProfileFiles:     []string{"AGENTS.md"},
	},
	{
		Name:             "hwcloud",
		ProbeCmd:         "hwcloud --version",
		ACPSpawn:         []string{"hwcloud", "serve", "--acp"},
		SkillsDir:        "~/.agents/skills",
		ProjectSkillsDir: ".agents/skills", // the AgentSkills standard (npx skills add)
		ProfileFiles:     []string{"SOUL.md", "SYSTEM.md", "AGENTS.md"},
	},
	{
		Name:             "kimi",
		ProbeCmd:         "kimi --version",
		ACPSpawn:         []string{"kimi", "acp"},
		SkillsDir:        "~/.kimi/skills",
		ProjectSkillsDir: ".kimi/skills",
		ProfileFiles:     []string{"AGENTS.md"},
	},
	{
		Name:             "hermes",
		ProbeCmd:         "hermes-acp --version",
		ACPSpawn:         []string{"hermes-acp"},
		SkillsDir:        "~/.hermes/skills",
		ProjectSkillsDir: "",
		ProfileFiles:     []string{"AGENTS.md"},
	},
	{
		Name:             "openclaw",
		ProbeCmd:         "/root/runtime/openclaw/openclaw.mjs --version",
		ACPSpawn:         []string{"/root/runtime/openclaw/node/bin/node", "/root/runtime/openclaw/openclaw.mjs", "acp"},
		SkillsDir:        "~/.openclaw/skills",
		ProjectSkillsDir: ".agents/skills",
		ProfileFiles:     []string{"AGENTS.md"},
	},
	{
		Name:             "jiuwenswarm",
		ProbeCmd:         "jiuwenswarm-acp --help",
		ACPSpawn:         []string{"jiuwenswarm-acp"},
		SkillsDir:        "~/.jiuwenswarm/service_default/agent_default/jiuwenswarm_workspace/skills",
		ProjectSkillsDir: "",
		ProfileFiles:     []string{"AGENTS.md"},
	},
	{
		Name:             "codearts",
		ProbeCmd:         "codearts --version",
		ACPSpawn:         []string{"codearts", "acp"},
		SkillsDir:        "~/.codeartsdoer/skills",
		ProjectSkillsDir: ".codeartsdoer/skills",
		ProfileFiles:     []string{"AGENTS.md"},
	},
}

// mergedProbeTable holds the builtin table + agents.yaml overrides, loaded
// once at connect startup by setMergedProbeTable. nil = no config loaded →
// activeProbeTable falls back to the builtin table (so tests and a missing
// config file still probe the builtins).
var (
	probeTableMu     sync.RWMutex
	mergedProbeTable []probeEntry
)

// activeProbeTable returns the merged probe table when a config has been
// loaded, else the builtin table. Both probeCLIs and projectSkillsDirs
// consume this so a custom entry's ProjectSkillsDir lands in the git-exclude
// set and its CLI gets probed. Returns a copy: the probe tick hot-reloads the
// table while an RPC handler may be reading it (the refresh path), so callers
// must not observe a half-swapped slice.
func activeProbeTable() []probeEntry {
	probeTableMu.RLock()
	defer probeTableMu.RUnlock()
	if mergedProbeTable != nil {
		return append([]probeEntry(nil), mergedProbeTable...)
	}
	return append([]probeEntry(nil), builtinProbeTable...)
}

// mergeProbeTables merges custom (agents.yaml) entries into the builtin
// table. Entries sharing a builtin's Name OVERRIDE it (customize a known
// CLI's spawn command / dirs); new Names are appended in config-file order.
// Builtins not mentioned in custom are kept unchanged. Result order:
// builtins first (original order, with overrides applied), then new custom
// entries (config-file order).
func mergeProbeTables(builtin, custom []probeEntry) []probeEntry {
	byName := map[string]probeEntry{}
	for _, e := range builtin {
		byName[e.Name] = e
	}
	for _, e := range custom {
		byName[e.Name] = e // override-by-name: custom wins
	}
	merged := make([]probeEntry, 0, len(builtin)+len(custom))
	// Builtins first, with any override applied.
	for _, e := range builtin {
		merged = append(merged, byName[e.Name])
		delete(byName, e.Name)
	}
	// Remaining are new custom entries — preserve config-file order.
	for _, e := range custom {
		if _, ok := byName[e.Name]; ok {
			merged = append(merged, e)
			delete(byName, e.Name)
		}
	}
	return merged
}

// loadAgentsConfig reads and validates the agents YAML config. Each entry
// needs at least name + probe_cmd + acp_spawn; an entry missing any of
// those is warned and skipped (never fatal — the remaining valid entries
// are still returned). Returns (nil, nil) for an empty/no-valid-entry file.
// A missing file returns the os error so the caller can distinguish "no
// config" from "bad config".
func loadAgentsConfig(path string) ([]probeEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg agentsConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	valid := make([]probeEntry, 0, len(cfg.Agents))
	for i, e := range cfg.Agents {
		if e.Name == "" {
			cliLogf("agents config: entry %d skipped: name is required", i+1)
			continue
		}
		if e.ProbeCmd == "" {
			cliLogf("agents config: entry %q skipped: probe_cmd is required", e.Name)
			continue
		}
		if len(e.ACPSpawn) == 0 {
			cliLogf("agents config: entry %q skipped: acp_spawn is required", e.Name)
			continue
		}
		valid = append(valid, e)
	}
	return valid, nil
}

// setMergedProbeTable loads the agents config from path and merges it with
// the builtin table, caching the result in mergedProbeTable. A missing file
// or a config with no valid entries clears mergedProbeTable (activeProbeTable
// then falls back to builtins) — so deleting or emptying the config restores
// the builtin table on the next reload, no restart needed. A YAML parse
// error returns the error WITHOUT touching mergedProbeTable: the caller keeps
// the previously loaded table (a transiently-corrupted edit shouldn't drop a
// working custom table). Called at connect startup and on every probe tick
// (hot-reload: edits to ~/.ouroflow/agents.yaml take effect within a minute).
func setMergedProbeTable(path string) error {
	custom, err := loadAgentsConfig(path)
	probeTableMu.Lock()
	defer probeTableMu.Unlock()
	if os.IsNotExist(err) {
		mergedProbeTable = nil // file removed → restore builtin fallback
		return nil
	}
	if err != nil {
		return err // parse error: caller warns, KEEPS the previous table
	}
	if len(custom) == 0 {
		mergedProbeTable = nil // empty / no valid entries → builtin fallback
		return nil
	}
	mergedProbeTable = mergeProbeTables(builtinProbeTable, custom)
	return nil
}

// projectSkillsDirs returns every project-level skills directory the probe
// table knows — the git exclusion set (staged skills are platform
// infrastructure, never the agent's commit) and the executor's fallback.
func projectSkillsDirs() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range activeProbeTable() {
		if e.ProjectSkillsDir != "" && !seen[e.ProjectSkillsDir] {
			seen[e.ProjectSkillsDir] = true
			out = append(out, e.ProjectSkillsDir)
		}
	}
	return out
}

// probeCLISpawn resolves one probe entry to a runnable ACP spawn: PATH
// first, then each --scan directory. Returns ok=false when the CLI is not
// installed. When a scan dir resolved it, the spawn carries the ABSOLUTE
// executable and extraPath is that directory (prepended to PATH for
// models_cmd / version probes).
func probeCLISpawn(ctx context.Context, e probeEntry, scanDirs []string) (spawn []string, extraPath string, ok bool) {
	if probeRuns(ctx, e.ProbeCmd, "") {
		return e.ACPSpawn, "", true
	}
	for _, pattern := range scanDirs {
		for _, d := range scanDirsFor(pattern) {
			if abs, err := filepath.Abs(d); err == nil {
				d = abs
			}
			if !probeRuns(ctx, e.ProbeCmd, d) {
				continue
			}
			return append([]string{filepath.Join(d, e.Name)}, e.ACPSpawn[1:]...), d, true
		}
	}
	return nil, "", false
}

// probeCLIs scans the environment for the probe-table CLIs: PATH first,
// then each --scan directory (a CLI installed outside PATH — the common
// case for absolute-path installs). When a scan dir resolves the CLI, the
// reported ACPSpawn carries the ABSOLUTE executable so the runtime row
// spawns correctly on a machine whose PATH never sees it. Probe commands
// get a hard timeout derived from ctx (a hung version check must not stall
// registration; Ctrl+C aborts probing too).
func probeCLIs(ctx context.Context, scanDirs []string) []link.ProbeCLI {
	type foundCLI struct {
		probe link.ProbeCLI
		entry probeEntry
		path  string // scan dir that resolved the CLI ('' = found on PATH)
	}
	found := []foundCLI{}
	for _, e := range activeProbeTable() {
		spawn, extraPath, ok := probeCLISpawn(ctx, e, scanDirs)
		if !ok {
			continue
		}
		found = append(found, foundCLI{
			probe: link.ProbeCLI{
				Name:             e.Name,
				Version:          probeVersion(ctx, e.ProbeCmd, extraPath),
				ACPSpawn:         spawn,
				SkillsDir:        expandHome(e.SkillsDir),
				ProjectSkillsDir: e.ProjectSkillsDir,
				ProfileFiles:     e.ProfileFiles,
			},
			entry: e,
			path:  extraPath,
		})
	}
	// Model detection runs CONCURRENTLY: registration must not serialize
	// eight CLIs' models_cmd runs / ACP intros behind one slow CLI.
	out := make([]link.ProbeCLI, len(found))
	var wg sync.WaitGroup
	for i, f := range found {
		wg.Add(1)
		go func(i int, f foundCLI) {
			defer wg.Done()
			f.probe.Models = detectCLIModels(ctx, f.entry, f.probe.ACPSpawn, f.path)
			out[i] = f.probe
		}(i, f)
	}
	wg.Wait()
	return out
}

// detectCLIModels returns the CLI's model list, cached per CLI name for
// modelsCacheTTL. Strategy: introspect over ACP when the CLI is spawnable;
// when the handshake fails or advertises no models, fall back to ModelsCmd
// and parse its one-model-per-line (or JSON array) output. Only successful
// detections are cached: a failed spawn/timeout must be retried on the next
// probe tick rather than pinning "0 models" for 30 minutes.
func detectCLIModels(ctx context.Context, e probeEntry, spawn []string, extraPath string) []link.ModelInfo {
	if models, ok := cachedModels(e.Name); ok {
		return models
	}
	models, ok := detectCLIModelsUncached(ctx, e, spawn, extraPath)
	if !ok {
		return models
	}
	storeModels(e.Name, models)
	return models
}

// refreshCLIModels performs an uncached detection for the manual refresh
// (machine.models_refresh). Unlike the periodic path it reports failure, so
// the daemon can refuse to overwrite a working catalog with the empty result
// of a transient probe failure. A successful handshake that yields zero models
// is still a valid empty answer and IS cached.
func refreshCLIModels(ctx context.Context, e probeEntry, spawn []string, extraPath string) ([]link.ModelInfo, error) {
	models, ok := detectCLIModelsUncached(ctx, e, spawn, extraPath)
	if !ok {
		return nil, fmt.Errorf("model detection failed for CLI %q — model list left unchanged", e.Name)
	}
	storeModels(e.Name, models)
	return models, nil
}

// detectCLIModelsUncached is the detection strategy itself, without the
// read-side cache. ok reports that at least one source completed a handshake /
// command successfully; an empty list with ok=true is an authoritative "this
// CLI currently exposes no usable models". ok=false means infrastructure
// failure (spawn error, timeout, JSON-RPC error, failed models_cmd) and the
// returned list never represents the CLI's actual catalog.
func detectCLIModelsUncached(ctx context.Context, e probeEntry, spawn []string, extraPath string) ([]link.ModelInfo, bool) {
	// Prefer the ACP handshake when the CLI is spawnable: its session/new
	// model selector lists the models the CLI will actually accept (opencode
	// advertises configOptions and rejects registry-only ids). The `models`
	// subcommand is the fallback for CLIs whose ACP server is unavailable or
	// advertises none.
	var models []link.ModelInfo
	ok := false
	if len(spawn) > 0 {
		models, ok = probeModelsViaACP(ctx, spawn)
	}
	if !ok || len(models) == 0 {
		if e.ModelsCmd != "" {
			raw, cmdOK := runModelsCmd(ctx, e.ModelsCmd, extraPath)
			if cmdOK {
				parsed := parseModelsOutput(raw)
				// A successful handshake that advertised zero models stays
				// authoritative; the command only fills a gap, it cannot
				// resurrect a failed ACP probe into a "failure".
				if len(parsed) > 0 || !ok {
					models = parsed
				}
				ok = true
			}
		}
	}
	if models == nil {
		models = []link.ModelInfo{}
	}
	return models, ok
}

// ── model detection cache ──

type modelsCacheEntry struct {
	models []link.ModelInfo
	at     time.Time
}

var (
	modelsCacheMu sync.Mutex
	modelsCache   = map[string]modelsCacheEntry{}
)

// cachedModels returns a cached detection younger than modelsCacheTTL.
func cachedModels(name string) ([]link.ModelInfo, bool) {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	ent, ok := modelsCache[name]
	if !ok || time.Since(ent.at) > modelsCacheTTL {
		return nil, false
	}
	return append([]link.ModelInfo(nil), ent.models...), true
}

// storeModels caches one CLI's detection result.
func storeModels(name string, models []link.ModelInfo) {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	modelsCache[name] = modelsCacheEntry{models: append([]link.ModelInfo(nil), models...), at: time.Now()}
}

// resetModelsCache clears the cache (tests; a config change may also want a
// fresh detection).
func resetModelsCache() {
	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	modelsCache = map[string]modelsCacheEntry{}
}

// runModelsCmd runs a models_cmd with a hard timeout and returns its
// combined output. ok is false when the command could not start, exited
// non-zero, or timed out — callers must treat that as a detection failure, not
// as "the CLI has no models". extraPath (a --scan dir) is prepended to PATH so
// a CLI resolved outside PATH still runs.
func runModelsCmd(ctx context.Context, cmd, extraPath string) (string, bool) {
	pctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	c := exec.CommandContext(pctx, "sh", "-c", cmd)
	if extraPath != "" {
		c.Env = append(os.Environ(), "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	b, err := c.CombinedOutput()
	if err != nil {
		return "", false
	}
	return string(b), true
}

// parseModelsOutput parses a models_cmd's output into model infos. Two
// shapes are accepted: a JSON array (of strings or {id|modelId,name,
// description} objects) and one model id per line. Lines that are empty,
// comment-prefixed (#) or a trailing-colon header ("Available models:") are
// skipped; duplicates are dropped (first wins).
func parseModelsOutput(raw string) []link.ModelInfo {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return []link.ModelInfo{}
	}
	if strings.HasPrefix(raw, "[") {
		if models, ok := parseModelJSONArray([]byte(raw)); ok {
			return models
		}
	}
	models := []link.ModelInfo{}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		id := strings.TrimSpace(line)
		if id == "" || strings.HasPrefix(id, "#") || strings.HasSuffix(id, ":") {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, link.ModelInfo{ID: id})
	}
	return models
}

func parseModelJSONArray(b []byte) ([]link.ModelInfo, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, false
	}
	models := []link.ModelInfo{}
	seen := map[string]bool{}
	add := func(m link.ModelInfo) {
		if m.ID == "" || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		models = append(models, m)
	}
	for _, item := range items {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			add(link.ModelInfo{ID: strings.TrimSpace(s)})
			continue
		}
		var m acpModel
		if err := json.Unmarshal(item, &m); err == nil {
			add(m.info())
		}
	}
	return models, true
}

// acpModel is one model descriptor as ACP implementations shape it
// (modelId per spec; id tolerated — some servers use it). Also reused for
// JSON-array models_cmd output.
type acpModel struct {
	ModelID     string `json:"modelId"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (m acpModel) info() link.ModelInfo {
	id := strings.TrimSpace(m.ModelID)
	if id == "" {
		id = strings.TrimSpace(m.ID)
	}
	return link.ModelInfo{ID: id, Name: m.Name, Description: m.Description}
}

func acpModelsToInfos(in []acpModel) []link.ModelInfo {
	models := []link.ModelInfo{}
	seen := map[string]bool{}
	for _, m := range in {
		info := m.info()
		if info.ID == "" || seen[info.ID] {
			continue
		}
		seen[info.ID] = true
		models = append(models, info)
	}
	return models
}

// acpConfigOption is one entry of a session/new result.configOptions array.
// opencode returns its model selector this way (verified with opencode
// 2.2.5):
//
//	{id:"model", category:"model", type:"select", currentValue:"...",
//	 options:[{value:"...", name:"...", description:"..."}]}
//
// Unknown fields (currentValue, _meta) are ignored.
type acpConfigOption struct {
	ID       string              `json:"id"`
	Category string              `json:"category"`
	Type     string              `json:"type"`
	Options  []acpConfigOptValue `json:"options"`
}

// acpConfigOptValue is one selectable value of a config option.
type acpConfigOptValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// acpConfigOptionModels maps the model selector's options to ModelInfo:
// entries whose id OR category is "model" (and that are not boolean
// toggles). A boolean "model" option has no options list, so it contributes
// nothing anyway.
func acpConfigOptionModels(opts []acpConfigOption) []link.ModelInfo {
	const model = "model"
	out := []link.ModelInfo{}
	for _, o := range opts {
		if o.ID != model && o.Category != model {
			continue
		}
		if o.Type == "boolean" {
			continue
		}
		for _, v := range o.Options {
			id := strings.TrimSpace(v.Value)
			if id == "" {
				continue
			}
			out = append(out, link.ModelInfo{ID: id, Name: v.Name, Description: v.Description})
		}
	}
	return out
}

// parseACPModels extracts the model list from an ACP session/new RESULT
// payload. The sources are NOT unioned: the first explicit model list present
// (result.models as an array, result.models.availableModels, or top-level
// availableModels) is the CLI's authoritative selector; only when none is
// present does the parser fall back to configOptions' model selector.
//
// Unioning the sources inflates the list with a registry catalog the CLI does
// not currently offer (the refresh path used to report every model a provider
// knows about, including ones the user has no key/entitlement for). One
// source, first present, wins; ids are deduplicated within that source.
func parseACPModels(result json.RawMessage) []link.ModelInfo {
	out := []link.ModelInfo{}
	if len(result) == 0 {
		return out
	}
	var top struct {
		Models          json.RawMessage   `json:"models"`
		AvailableModels []acpModel        `json:"availableModels"`
		ConfigOptions   []acpConfigOption `json:"configOptions"`
	}
	if err := json.Unmarshal(result, &top); err != nil {
		return out
	}
	models := strings.TrimSpace(string(top.Models))
	if models != "" && models != "null" {
		if strings.HasPrefix(models, "[") {
			var arr []acpModel
			if err := json.Unmarshal([]byte(models), &arr); err == nil {
				out = acpModelsToInfos(arr)
			}
		} else {
			var obj struct {
				AvailableModels []acpModel `json:"availableModels"`
			}
			if err := json.Unmarshal([]byte(models), &obj); err == nil {
				out = acpModelsToInfos(obj.AvailableModels)
			}
		}
	}
	if len(out) == 0 {
		out = acpModelsToInfos(top.AvailableModels)
	}
	if len(out) > 0 {
		// Explicit selector wins outright — configOptions is the agent's
		// full registry and may contain providers/models this CLI cannot
		// actually run right now.
		return out
	}
	return acpConfigOptionModels(top.ConfigOptions)
}

// filterModelsByAuth keeps only models whose provider-namespaced id
// ("<provider>:<model>", the shape hermes-acp advertises) belongs to a
// provider whose auth method initialize actually advertises — i.e. one the
// CLI holds a credential for. Models a configured CLI lists for providers the
// user never set up are exactly the "no key, but still shown" entries.
//
// Conservative on purpose: it filters ONLY when at least one model prefix
// matches an advertised auth id. CLIs whose auth ids don't line up with their
// model namespaces (or model ids without a colon, e.g. opencode's
// "deepseek/deepseek-v4") are returned untouched — this can never empty a
// valid catalog.
func filterModelsByAuth(models []link.ModelInfo, initResult json.RawMessage) []link.ModelInfo {
	if len(models) == 0 || len(initResult) == 0 {
		return models
	}
	var v struct {
		AuthMethods []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"authMethods"`
	}
	if json.Unmarshal(initResult, &v) != nil {
		return models
	}
	authed := map[string]bool{}
	for _, m := range v.AuthMethods {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id != "" && !strings.EqualFold(strings.TrimSpace(m.Type), "terminal") {
			authed[id] = true
		}
	}
	if len(authed) == 0 {
		return models
	}
	prefix := func(id string) string {
		i := strings.IndexByte(id, ':')
		if i <= 0 {
			return ""
		}
		return strings.ToLower(id[:i])
	}
	matches := 0
	for _, m := range models {
		if authed[prefix(m.ID)] {
			matches++
		}
	}
	if matches == 0 {
		return models // auth ids don't describe this catalog's namespaces
	}
	out := make([]link.ModelInfo, 0, len(models))
	for _, m := range models {
		// Keep un-namespaced ids: a plain id carries no provider evidence, and
		// dropping it in a partly-namespaced catalog would lose a valid model.
		if p := prefix(m.ID); p == "" || authed[p] {
			out = append(out, m)
		}
	}
	return out
}

// probeModelsViaACP introspects a CLI over ACP stdio: spawn ACPSpawn, read
// its initialize reply, then session/new (empty mcpServers, temp cwd) and
// parse the model list off the reply — from result.models shapes or the
// result.configOptions model selector (opencode/hermes). The process is
// always killed and the temp cwd removed.
//
// ok=false means the handshake itself failed (spawn error, timeout, JSON-RPC
// error); the caller must not treat that as "no models". A successful
// session/new that advertises zero usable models returns (empty, true) — an
// authoritative empty answer. initialize's authMethods are not used as a
// precondition: a CLI may authenticate from env/config while advertising only
// a terminal setup method, and hermes already fails session/new itself when
// no provider is configured.
func probeModelsViaACP(ctx context.Context, spawn []string) ([]link.ModelInfo, bool) {
	if len(spawn) == 0 {
		return nil, false
	}
	cwd, err := os.MkdirTemp("", "ouroflow-models-")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(cwd)

	pctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, spawn[0], spawn[1:]...)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, false
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	defer func() {
		cancel() // CommandContext kills the process on ctx cancel
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	lines := make(chan []byte, 32)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-pctx.Done():
				return
			}
		}
	}()

	send := func(id int, method string, params any) bool {
		frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			return false
		}
		_, err = stdin.Write(append(frame, '\n'))
		return err == nil
	}
	// call writes one request and waits for the reply with the matching id.
	call := func(id int, method string, params any) (json.RawMessage, bool) {
		if !send(id, method, params) {
			return nil, false
		}
		for {
			select {
			case <-pctx.Done():
				return nil, false
			case line, ok := <-lines:
				if !ok {
					return nil, false
				}
				var env struct {
					ID     json.RawMessage `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  json.RawMessage `json:"error"`
				}
				if json.Unmarshal(line, &env) != nil {
					continue
				}
				if strings.TrimSpace(string(env.ID)) != strconv.Itoa(id) {
					continue
				}
				if e := strings.TrimSpace(string(env.Error)); e != "" && e != "null" {
					return nil, false
				}
				return env.Result, true
			}
		}
	}
	initResult, ok := call(1, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
	})
	if !ok {
		return nil, false
	}
	result, ok := call(2, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if !ok {
		return nil, false
	}
	// The handshake succeeded: whatever it advertises (possibly nothing after
	// the provider-namespace filter) is the CLI's real answer.
	return filterModelsByAuth(parseACPModels(result), initResult), true
}

// scanDirsFor expands one --scan argument into concrete directories: ~ is
// expanded, glob patterns are resolved via doublestar (`*` one segment,
// `**` recursive — /opt/**/ = any depth under /opt), and non-directory
// matches are dropped. Plain paths pass through unchanged.
func scanDirsFor(pattern string) []string {
	pattern = expandHome(pattern)
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			out = append(out, m)
		}
	}
	return out
}

// probeRuns reports whether the probe command executes successfully, with
// extraPath (a --scan dir) prepended to PATH when non-empty.
func probeRuns(ctx context.Context, cmd, extraPath string) bool {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := exec.CommandContext(pctx, "sh", "-c", cmd)
	if extraPath != "" {
		c.Env = append(os.Environ(), "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return c.Run() == nil
}

// probeVersion runs the probe command and returns its first output line,
// trimmed (” when the CLI prints nothing).
func probeVersion(ctx context.Context, cmd, extraPath string) string {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := exec.CommandContext(pctx, "sh", "-c", cmd)
	if extraPath != "" {
		c.Env = append(os.Environ(), "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	b, err := c.CombinedOutput()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line
}

// expandHome resolves a leading ~ (” input passes through).
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
