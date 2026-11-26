package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/store"
	"github.com/Sinkear/flowloop/internal/zipx"
)

// TestParseSkillMarkdown: the skill name/title/description are read from the
// SKILL.md YAML frontmatter. A file without frontmatter, or without a name,
// is "not a skill" — returns ("", "", ""). Title is optional: the UI falls
// back to the machine name.
func TestParseSkillMarkdown(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want [3]string // name, title, description
	}{
		{
			"full frontmatter",
			"---\nname: code-review-checklist\ndescription: 一句话说明\n---\n正文\n",
			[3]string{"code-review-checklist", "", "一句话说明"},
		},
		{
			"quoted values",
			"---\nname: \"my skill\"\ndescription: 'has quotes'\n---\n",
			[3]string{"my skill", "", "has quotes"},
		},
		{
			"name only, no description",
			"---\nname: bare\n---\n",
			[3]string{"bare", "", ""},
		},
		{
			"no frontmatter",
			"# Just a markdown file\nno frontmatter here",
			[3]string{"", "", ""},
		},
		{
			"frontmatter without name",
			"---\ndescription: missing name\n---\n",
			[3]string{"", "", "missing name"},
		},
		{
			"comment lines in frontmatter",
			"---\n# a comment\nname: with-comment\ndescription: x\n---\n",
			[3]string{"with-comment", "", "x"},
		},
		{
			"extra frontmatter fields ignored",
			"---\nname: has-extra\ndescription: desc\nversion: 1.0\nallowed-tools:\n  - Read\n  - Bash\n---\n",
			[3]string{"has-extra", "", "desc"},
		},
		{
			"CRLF line endings",
			"---\r\nname: crlf-skill\r\ndescription: windows\r\n---\r\nbody\r\n",
			[3]string{"crlf-skill", "", "windows"},
		},
		{
			"title present",
			"---\nname: flowloop-chat-create\ntitle: 对话式创建\ndescription: x\n---\n",
			[3]string{"flowloop-chat-create", "对话式创建", "x"},
		},
		{
			"closing delimiter at EOF (no trailing newline)",
			"---\nname: eof\n---",
			[3]string{"eof", "", ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotTitle, gotDesc := parseSkillMarkdown(c.md)
			if gotName != c.want[0] || gotTitle != c.want[1] || gotDesc != c.want[2] {
				t.Fatalf("parseSkillMarkdown(%q) = (%q, %q, %q), want (%q, %q, %q)",
					c.md, gotName, gotTitle, gotDesc, c.want[0], c.want[1], c.want[2])
			}
		})
	}
}

// TestCreateFromZipParsesNameFromSkillMD: the platform extracts the skill
// name/description from the archive's SKILL.md, NOT from a caller-supplied
// name. A zip without SKILL.md (or whose SKILL.md has no name) is rejected
// as "not a skill package".
func TestCreateFromZipParsesNameFromSkillMD(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)

	// A valid skill zip: SKILL.md with frontmatter name + a resource file.
	zipData, err := zipx.Build(map[string]string{
		"SKILL.md":        "---\nname: code-review-checklist\ndescription: PR 审查清单\n---\n审查步骤…\n",
		"scripts/lint.sh": "echo lint\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sk, err := svc.CreateFromZip(ctx, zipData)
	if err != nil {
		t.Fatalf("valid skill zip must create, got %v", err)
	}
	if sk.Name != "code-review-checklist" {
		t.Fatalf("name must be parsed from SKILL.md, got %q", sk.Name)
	}
	if sk.Description != "PR 审查清单" {
		t.Fatalf("description must be parsed from SKILL.md, got %q", sk.Description)
	}

	// A zip nested under a single top-level folder must still find SKILL.md.
	nestedZip, err := zipx.Build(map[string]string{
		"myskill/SKILL.md": "---\nname: nested\n---\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := svc.CreateFromZip(ctx, nestedZip)
	if err != nil {
		t.Fatalf("nested-folder zip must create, got %v", err)
	}
	if sk2.Name != "nested" {
		t.Fatalf("nested skill name must be parsed, got %q", sk2.Name)
	}

	// A zip WITHOUT SKILL.md → rejected as "not a skill package".
	noMD, err := zipx.Build(map[string]string{"readme.txt": "no skill here"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFromZip(ctx, noMD); err == nil {
		t.Fatal("zip without SKILL.md must be rejected")
	}

	// A zip whose SKILL.md has NO name in frontmatter → rejected.
	noName, err := zipx.Build(map[string]string{
		"SKILL.md": "---\ndescription: no name\n---\nbody\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFromZip(ctx, noName); err == nil {
		t.Fatal("SKILL.md without name must be rejected")
	}

	// A duplicate name is rejected (the parsed name already exists).
	if _, err := svc.CreateFromZip(ctx, zipData); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate skill name must be rejected, got %v", err)
	}
}

// TestCreateFromZipRejectsMultiSkillBundle: a zip containing MORE than one
// skill (several subdirectories each with their own SKILL.md) is rejected
// with a clear message — one zip = one skill. The user uploads skills one
// at a time.
func TestCreateFromZipRejectsMultiSkillBundle(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)

	bundle, err := zipx.Build(map[string]string{
		"skillA/SKILL.md": "---\nname: skill-a\n---\n",
		"skillB/SKILL.md": "---\nname: skill-b\n---\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateFromZip(ctx, bundle)
	if err == nil {
		t.Fatal("multi-skill bundle must be rejected")
	}
	if !strings.Contains(err.Error(), "一个 zip 只能包含一个 skill") {
		t.Fatalf("multi-skill bundle must be rejected with the one-skill message, got %v", err)
	}
	// Neither skill was persisted.
	var n int
	_ = st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM skill`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected bundle must not persist any skill, got %d rows", n)
	}
}

// TestCreateFromTextBlockParsesNameFromSkillMD: the legacy text-block
// (JSON) upload path ALSO parses name/description from SKILL.md.
func TestCreateFromTextBlockParsesNameFromSkillMD(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)

	sk, err := svc.Create(ctx, map[string]string{
		"SKILL.md": "---\nname: textblock-skill\ndescription: via json\n---\n",
	})
	if err != nil {
		t.Fatalf("text-block create must succeed, got %v", err)
	}
	if sk.Name != "textblock-skill" {
		t.Fatalf("name must be parsed from SKILL.md, got %q", sk.Name)
	}

	// Text block without SKILL.md → rejected.
	if _, err := svc.Create(ctx, map[string]string{"x.txt": "no skill"}); err == nil {
		t.Fatal("text block without SKILL.md must be rejected")
	}
}

// TestCreateFromZipNormalizesToFlatArchive: regardless of whether the
// uploaded zip had a top-level skill/ wrapper folder, the STORED
// package.zip is FLAT (SKILL.md + files at the archive root, no wrapper).
// This is the platform's normalization contract — every consumer
// (config.push → CLI Extract) sees one format and never re-detects a
// wrapper directory.
func TestCreateFromZipNormalizesToFlatArchive(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)

	// A NESTED zip: top-level myskill/ folder wrapping SKILL.md + a script.
	nested, err := zipx.Build(map[string]string{
		"myskill/SKILL.md":        "---\nname: nested-flat\ndescription: x\n---\n",
		"myskill/scripts/lint.sh": "echo lint\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sk, err := svc.CreateFromZip(ctx, nested)
	if err != nil {
		t.Fatalf("nested zip must create, got %v", err)
	}

	// The stored package.zip must be FLAT: SKILL.md at the root, no
	// myskill/ wrapper. Read it back and check its entry names.
	archive, err := svc.PackageZip(ctx, sk.ID)
	if err != nil {
		t.Fatalf("read back package.zip: %v", err)
	}
	entries, err := zipxEntries(archive)
	if err != nil {
		t.Fatalf("read archive entries: %v", err)
	}
	hasSkillMD := false
	for _, e := range entries {
		if e == "SKILL.md" {
			hasSkillMD = true
		}
		if strings.HasPrefix(e, "myskill/") {
			t.Fatalf("stored archive must be flat (no wrapper), found %q", e)
		}
	}
	if !hasSkillMD {
		t.Fatalf("stored archive must have SKILL.md at root, got %v", entries)
	}

	// A FLAT zip (no wrapper) must also stay flat.
	flat, err := zipx.Build(map[string]string{
		"SKILL.md":     "---\nname: already-flat\n---\n",
		"scripts/x.sh": "echo x\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := svc.CreateFromZip(ctx, flat)
	if err != nil {
		t.Fatalf("flat zip must create, got %v", err)
	}
	archive2, _ := svc.PackageZip(ctx, sk2.ID)
	entries2, _ := zipxEntries(archive2)
	hasSkillMD = false
	for _, e := range entries2 {
		if e == "SKILL.md" {
			hasSkillMD = true
		}
	}
	if !hasSkillMD {
		t.Fatalf("flat zip must stay flat with SKILL.md at root, got %v", entries2)
	}
}

// zipxEntries reads a zip's entry names (for the normalization test).
func zipxEntries(data []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out, nil
}

// TestSeedBuiltinSkills: every embedded skill is installed with its fixed id,
// gets its title from the SKILL.md frontmatter, is flagged builtin, writes its
// files to the runs skill dir, and re-seeding updates in place (idempotent).
func TestSeedBuiltinSkills(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)

	skills, err := SeedBuiltinSkills(ctx, svc)
	if err != nil {
		t.Fatalf("seed builtin: %v", err)
	}
	if len(skills) != len(builtinSkills) {
		t.Fatalf("seeded %d skills, want %d", len(skills), len(builtinSkills))
	}
	byID := make(map[string]*Skill, len(skills))
	for _, sk := range skills {
		byID[sk.ID] = sk
	}

	for _, b := range builtinSkills {
		t.Run(b.Name, func(t *testing.T) {
			sk, ok := byID[b.ID]
			if !ok {
				t.Fatalf("builtin %q (%s) was not seeded", b.Name, b.ID)
			}
			if sk.Name != b.Name {
				t.Fatalf("builtin name must stay stable, got %q", sk.Name)
			}
			if sk.Title != b.Title {
				t.Fatalf("title must come from frontmatter, got %q", sk.Title)
			}
			if !sk.Builtin {
				t.Fatal("seeded skill must be flagged builtin")
			}
			if strings.TrimSpace(sk.Description) == "" {
				t.Fatal("description must come from frontmatter")
			}
			md, err := builtinSkillsFS.ReadFile(b.Asset + "/SKILL.md")
			if err != nil {
				t.Fatalf("read embedded SKILL.md: %v", err)
			}
			fmName, fmTitle, _ := parseSkillMarkdown(string(md))
			if fmName != b.Name || fmTitle != b.Title {
				t.Fatalf("frontmatter name/title = %q/%q, want %q/%q",
					fmName, fmTitle, b.Name, b.Title)
			}

			files, err := svc.Files(ctx, sk.ID)
			if err != nil {
				t.Fatalf("builtin files: %v", err)
			}
			embedded, err := readBuiltinSkillFiles(b.Asset)
			if err != nil {
				t.Fatalf("read embedded %s: %v", b.Asset, err)
			}
			// Files() adds the generated archive next to the source files.
			delete(files, "package.zip")
			if len(files) != len(embedded) {
				t.Fatalf("skill dir has %d files, embedded tree has %d: %v vs %v",
					len(files), len(embedded), keysOf(files), keysOf(embedded))
			}
			for name, want := range embedded {
				if got, ok := files[name]; !ok || got != want {
					t.Fatalf("%s must be written verbatim to the skill dir", name)
				}
			}
		})
	}

	// Re-seed: same ids, no duplicate rows, still builtin.
	// (Files are rewritten from the embedded tree on every boot.)
	again, err := SeedBuiltinSkills(ctx, svc)
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if len(again) != len(skills) {
		t.Fatalf("re-seed returned %d skills, want %d", len(again), len(skills))
	}
	for i, sk := range again {
		if sk.ID != skills[i].ID || !sk.Builtin || sk.Title != builtinSkills[i].Title {
			t.Fatalf("re-seed must be idempotent, got %+v", sk)
		}
	}
	for _, b := range builtinSkills {
		var n int
		if err := st.DB().QueryRow("SELECT COUNT(*) FROM skill WHERE id = ?", b.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("re-seed must not duplicate %s, got %d rows", b.Name, n)
		}
	}
}

// TestBuiltinVODContent: the shipped VOD skill must keep the three-section
// contract the issue asks for (问题 / 背景 / 诉求) and the write-back command,
// or the skill stops doing the one thing it exists for.
func TestBuiltinVODContent(t *testing.T) {
	skillMD, err := builtinSkillsFS.ReadFile("assets/flowloop-vod/SKILL.md")
	if err != nil {
		t.Fatalf("read VOD SKILL.md: %v", err)
	}
	for _, want := range []string{
		"name: flowloop-vod",
		"title: 开发者声音",
		"## 问题",
		"## 背景",
		"## 诉求",
		"flowloop issue comment",
		"待确认",
	} {
		if !strings.Contains(string(skillMD), want) {
			t.Fatalf("VOD SKILL.md must contain %q", want)
		}
	}

	tpl, err := builtinSkillsFS.ReadFile("assets/flowloop-vod/references/template.md")
	if err != nil {
		t.Fatalf("read VOD template: %v", err)
	}
	for _, want := range []string{"## 问题", "## 背景", "## 诉求", "## 反例", "发布前自检"} {
		if !strings.Contains(string(tpl), want) {
			t.Fatalf("VOD template must contain %q", want)
		}
	}
}

// TestEnsureBuiltinRejectsForeignName: every builtin's machine name is
// reserved; a different skill already holding one makes the seed fail loudly
// instead of silently stealing the name.
func TestEnsureBuiltinRejectsForeignName(t *testing.T) {
	for _, b := range builtinSkills {
		t.Run(b.Name, func(t *testing.T) {
			st, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			svc := NewSkillService(st)
			if _, err := svc.Create(ctx, map[string]string{
				"SKILL.md": "---\nname: " + b.Name + "\ndescription: impostor\n---\n",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := SeedBuiltinSkills(ctx, svc); err == nil {
				t.Fatalf("seeding over a foreign skill named %s must fail", b.Name)
			} else if !errors.Is(err, ErrConflict) {
				t.Fatalf("want conflict error, got %v", err)
			}
		})
	}
}

// TestStewardBuiltinSkillIDs: only chat-create is auto-attached to the
// steward. VOD needs a goal source, which chat runs do not have, so adding it
// here would ship a skill that can never post.
func TestStewardBuiltinSkillIDs(t *testing.T) {
	got := stewardBuiltinSkillIDs()
	if len(got) != 1 || got[0] != BuiltinChatCreateID {
		t.Fatalf("steward builtins = %v, want only %s", got, BuiltinChatCreateID)
	}
}

// TestDeleteBuiltinRejected: builtin skills ride the binary; deleting one would
// diverge the platform from its own release.
func TestDeleteBuiltinRejected(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc := NewSkillService(st)
	if _, err := SeedBuiltinSkills(ctx, svc); err != nil {
		t.Fatal(err)
	}

	err = svc.Delete(ctx, BuiltinChatCreateID)
	if err == nil {
		t.Fatal("deleting a builtin skill must be rejected")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation error, got %v", err)
	}
	for _, b := range builtinSkills {
		if _, err := svc.Get(ctx, b.ID); err != nil {
			t.Fatalf("rejected delete must leave %s intact: %v", b.Name, err)
		}
		if err := svc.Delete(ctx, b.ID); err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("deleting builtin %s must be rejected with a validation error, got %v", b.Name, err)
		}
	}
}

// TestBuiltinSkillMirror: the repo's skills/ tree is the human-facing mirror of
// the embedded tree. They must stay byte-identical — editing one without the
// other means the shipped skill and the reviewed skill differ.
func TestBuiltinSkillMirror(t *testing.T) {
	for _, b := range builtinSkills {
		t.Run(b.Name, func(t *testing.T) {
			embedded, err := readBuiltinSkillFiles(b.Asset)
			if err != nil {
				t.Fatal(err)
			}
			mirrorRoot := filepath.Join("..", "..", "skills", b.Name)
			mirror := make(map[string]string)
			err = filepath.WalkDir(mirrorRoot, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, err := filepath.Rel(mirrorRoot, p)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				mirror[filepath.ToSlash(rel)] = string(data)
				return nil
			})
			if err != nil {
				t.Fatalf("read mirror %s: %v", mirrorRoot, err)
			}
			if len(mirror) != len(embedded) {
				t.Fatalf("mirror has %d files, embedded tree has %d: %v vs %v",
					len(mirror), len(embedded), keysOf(mirror), keysOf(embedded))
			}
			for name, want := range embedded {
				if got, ok := mirror[name]; !ok || got != want {
					t.Fatalf("%s differs between skills/ and the embedded asset (edit the asset, then copy it to skills/)", name)
				}
			}
		})
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
