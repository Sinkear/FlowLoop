package service

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// builtinSkillsFS carries the skills the platform ships with the daemon. They
// live in the binary so a fresh install has them with no network and they can
// never drift from the release that ships them; the same tree is mirrored
// under the repository's skills/ directory for local use, and a test keeps the
// two byte-identical.
//
//go:embed assets/flowloop-chat-create assets/flowloop-vod
var builtinSkillsFS embed.FS

// builtinSkill is one shipped skill: its stable identity (the constants in
// skill.go) plus the embedded asset directory it is read from.
type builtinSkill struct {
	ID    string
	Name  string
	Title string
	Asset string
}

// builtinSkills is the platform's shipped skill set, in seeding order. Ids are
// fixed constants — never regenerate them; agent configs reference them.
var builtinSkills = []builtinSkill{
	{
		ID:    BuiltinChatCreateID,
		Name:  BuiltinChatCreateName,
		Title: BuiltinChatCreateTitle,
		Asset: "assets/flowloop-chat-create",
	},
	{
		ID:    BuiltinVODID,
		Name:  BuiltinVODName,
		Title: BuiltinVODTitle,
		Asset: "assets/flowloop-vod",
	},
}

// SeedBuiltinSkills installs or refreshes every skill the platform ships with
// the daemon. Called once at boot, before the steward is seeded (the steward's
// agent.skills must reference an existing row). Idempotent: an existing row is
// updated in place and its files are rewritten, so upgrading the binary
// upgrades the skills.
//
// Returns the seeded rows in builtinSkills order so the caller can log them.
// A failing skill does not stop the ones after it: each is independent, and
// the caller still gets the rows that did seed plus a joined error.
func SeedBuiltinSkills(ctx context.Context, skillSvc *SkillService) ([]*Skill, error) {
	seeded := make([]*Skill, 0, len(builtinSkills))
	var errs []error
	for _, b := range builtinSkills {
		files, err := readBuiltinSkillFiles(b.Asset)
		if err != nil {
			errs = append(errs, fmt.Errorf("read builtin skill %q: %w", b.Name, err))
			continue
		}
		md, ok := files["SKILL.md"]
		if !ok {
			errs = append(errs, fmt.Errorf("builtin skill %q is missing SKILL.md", b.Name))
			continue
		}
		_, title, description := parseSkillMarkdown(md)
		if title == "" {
			title = b.Title
		}
		sk, err := skillSvc.EnsureBuiltin(ctx, b.ID, b.Name, title, description, files)
		if err != nil {
			errs = append(errs, fmt.Errorf("seed builtin skill %q: %w", b.Name, err))
			continue
		}
		seeded = append(seeded, sk)
	}
	return seeded, errors.Join(errs...)
}

// readBuiltinSkillFiles flattens an embedded skill directory into the
// relative-path → content map the skill service stores and archives.
func readBuiltinSkillFiles(root string) (map[string]string, error) {
	files := make(map[string]string)
	err := fs.WalkDir(builtinSkillsFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := builtinSkillsFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := relPath(root, p)
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// relPath is path.Rel for slash paths (embed uses forward slashes on every OS).
func relPath(root, p string) (string, error) {
	if len(p) <= len(root)+1 || p[:len(root)] != root || p[len(root)] != '/' {
		return "", fmt.Errorf("%q is not under %q", p, root)
	}
	return path.Clean(p[len(root)+1:]), nil
}
