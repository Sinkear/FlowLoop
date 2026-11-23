package service

import (
	"context"
	"embed"
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
//go:embed assets/ouroflow-chat-create
var builtinSkillsFS embed.FS

// SeedBuiltinSkills installs or refreshes every skill the platform ships with
// the daemon. Called once at boot, before the steward is seeded (the steward's
// agent.skills must reference an existing row). Idempotent: an existing row is
// updated in place and its files are rewritten, so upgrading the binary
// upgrades the skill.
//
// Returns the chat-create skill so the caller can log its id.
func SeedBuiltinSkills(ctx context.Context, skillSvc *SkillService) (*Skill, error) {
	files, err := readBuiltinSkillFiles("assets/ouroflow-chat-create")
	if err != nil {
		return nil, err
	}
	md, ok := files["SKILL.md"]
	if !ok {
		return nil, fmt.Errorf("builtin skill asset is missing SKILL.md")
	}
	_, title, description := parseSkillMarkdown(md)
	if title == "" {
		title = BuiltinChatCreateTitle
	}
	return skillSvc.EnsureBuiltin(ctx, BuiltinChatCreateID, BuiltinChatCreateName, title, description, files)
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
