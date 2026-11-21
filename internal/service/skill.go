package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eushing/agentwork/internal/store"
	"github.com/eushing/agentwork/internal/zipx"
)

// Skill is a platform-managed skill package (SKILL.md + resources) — the
// skills library agents get their skills from (CLI 分支 Phase 4). Files
// live on disk under <RunsRoot>/skills/<id>/; the machine receives them
// via config.push and installs them under agentwork-<name>/.
type Skill struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Title is the human-facing display name (SKILL.md frontmatter `title`).
	// Name stays the machine identity (dir / archive / agent.skills), so one
	// skill can be "agentwork-chat-create" on disk and 「对话式创建」 in the UI.
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	// Builtin marks a skill the platform ships with the daemon binary: it is
	// re-seeded on every boot (files refreshed, row updated), auto-attached
	// to the steward, and cannot be deleted by hand.
	Builtin bool `json:"builtin,omitempty"`
	// Source is the optional remote git repository binding (feature: remote
	// skill source). nil = the skill is local-only. The repo token lives on
	// Source but is never serialized (json:"-").
	Source *SkillSource `json:"source,omitempty"`
}

// skillSelectColumns is the canonical skill-row projection. Every skill read
// must use it so the remote-source columns are never forgotten.
const skillSelectColumns = `id,name,title,description,created_at,repo_url,repo_provider,repo_branch,repo_path,repo_token,last_synced_at,last_sync_action,last_remote_sha,last_sync_digest,builtin`

// skillRowScanner is satisfied by *sql.Row and *sql.Rows.
type skillRowScanner interface {
	Scan(dest ...any) error
}

// scanSkill reads one canonical skill row (skillSelectColumns order).
func scanSkill(row skillRowScanner) (*Skill, error) {
	var (
		sk                                          Skill
		repoURL, provider, branch, path, token      string
		lastSyncedAt, lastSyncAction, lastRemoteSHA string
		lastSyncDigest                              string
		builtin                                     int
	)
	if err := row.Scan(&sk.ID, &sk.Name, &sk.Title, &sk.Description, &sk.CreatedAt,
		&repoURL, &provider, &branch, &path, &token,
		&lastSyncedAt, &lastSyncAction, &lastRemoteSHA, &lastSyncDigest, &builtin); err != nil {
		return nil, err
	}
	sk.Builtin = builtin != 0
	sk.Source = newSkillSource(repoURL, provider, branch, path, token, lastSyncedAt, lastSyncAction, lastRemoteSHA, lastSyncDigest)
	return &sk, nil
}

// SkillService owns the skills library.
type SkillService struct {
	st *store.Store
	// Remote-source support (skill_remote.go). Both are injectable so tests
	// never touch the network or a real git binary.
	pullProviderFactory func(provider, token string) (PullProvider, error)
	git                 remoteGit
}

// NewSkillService wires the library.
func NewSkillService(st *store.Store) *SkillService {
	return &SkillService{st: st, git: &gitCLI{}}
}

// Get returns one skill (with its remote source, token excluded from JSON).
func (s *SkillService) Get(ctx context.Context, id string) (*Skill, error) {
	sk, err := scanSkill(s.st.DB().QueryRowContext(ctx,
		`SELECT `+skillSelectColumns+` FROM skill WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill: %w", err)
	}
	return sk, nil
}

// skillDir returns the on-disk root of one skill's files.
func skillDir(skillID string) string {
	return filepath.Join(RunsRoot(), "skills", skillID)
}

// Create stores a skill package from a text-block file map (the legacy JSON
// upload mode). The skill's name/description are PARSED from the files'
// SKILL.md — the caller does not supply them. SKILL.md is required; a map
// without it (or without a name in its frontmatter) is not a skill package.
func (s *SkillService) Create(ctx context.Context, files map[string]string) (*Skill, error) {
	// Text-block mode builds the SAME archive shape a zip upload
	// produces — the push layer sends the original archive either way.
	zipData, err := zipx.Build(files)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	return s.createFromArchive(ctx, files, zipData)
}

// CreateFromZip uploads a skill as a ZIP archive. The skill's name and
// description are PARSED from the archive's SKILL.md frontmatter — the
// caller does NOT supply them. A zip without a valid SKILL.md (or whose
// SKILL.md has no name in its frontmatter) is not a skill package and is
// rejected. The archive is validated, extracted into the skill's library
// dir, and KEPT for push (the machine receives the original bytes).
func (s *SkillService) CreateFromZip(ctx context.Context, zipData []byte) (*Skill, error) {
	// One zip = one skill. A multi-skill bundle (several subdirectories each
	// with their own SKILL.md) is rejected with a clear message — the user
	// should upload skills one at a time.
	skillMDs := zipx.FindSkillMDs(zipData)
	if len(skillMDs) == 0 {
		return nil, NewValidationError("这不是一个 skill 包：缺少 SKILL.md")
	}
	if len(skillMDs) > 1 {
		return nil, NewValidationError("一个 zip 只能包含一个 skill——检测到多个 SKILL.md，请每个 skill 单独打包上传")
	}
	content, ok := zipx.ReadFile(zipData, "SKILL.md")
	if !ok {
		return nil, NewValidationError("这不是一个 skill 包：缺少 SKILL.md")
	}
	name, title, description := parseSkillMarkdown(content)
	if strings.TrimSpace(name) == "" {
		return nil, NewValidationError("这不是一个 skill 包：SKILL.md 缺少 name（frontmatter 里需有 `name: <skill 名>`）")
	}
	return s.createSkillFromArchive(ctx, name, title, description, zipData, nil)
}

// createFromArchive is the legacy text-block upload path: the caller supplies
// files (path→content) which the platform archives into a zip. The skill's
// name/title/description are STILL parsed from the files' SKILL.md — the
// caller does not supply them either.
func (s *SkillService) createFromArchive(ctx context.Context, files map[string]string, zipData []byte) (*Skill, error) {
	content, ok := files["SKILL.md"]
	if !ok || strings.TrimSpace(content) == "" {
		return nil, NewValidationError("这不是一个 skill 包：缺少 SKILL.md")
	}
	name, title, description := parseSkillMarkdown(content)
	if strings.TrimSpace(name) == "" {
		return nil, NewValidationError("这不是一个 skill 包：SKILL.md 缺少 name（frontmatter 里需有 `name: <skill 名>`）")
	}
	return s.createSkillFromArchive(ctx, name, title, description, zipData, files)
}

// createSkillFromArchive is the shared DB insert + storage path. It receives
// the PARSED name/title/description (from SKILL.md). When files is non-nil the
// files are written from the map (text-block mode); otherwise the zip is
// extracted (zip-upload mode).
func (s *SkillService) createSkillFromArchive(ctx context.Context, name, title, description string, zipData []byte, files map[string]string) (*Skill, error) {
	name = strings.TrimSpace(name)
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM skill WHERE name=?`, name).Scan(&n); err != nil {
		return nil, fmt.Errorf("check skill name: %w", err)
	}
	if n > 0 {
		return nil, NewCodedErrorDetail(CodeSkillNameExists, fmt.Sprintf("skill %q already exists", name), map[string]any{"name": name})
	}
	sk := &Skill{ID: newID(), Name: name, Title: title, Description: description, CreatedAt: now()}
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO skill (id,name,title,description,created_at) VALUES (?,?,?,?,?)`,
		sk.ID, sk.Name, sk.Title, sk.Description, sk.CreatedAt); err != nil {
		// The pre-flight COUNT(*) + INSERT is not atomic — two concurrent
		// creates with the same name can both pass COUNT(n=0) and the second
		// hits UNIQUE. dupNameCodedError catches the driver conflict and
		// surfaces a 400 coded error instead of a bare 500.
		if ve := dupNameCodedError(err, CodeSkillNameExists, "skill", name); ve != nil {
			return nil, ve
		}
		return nil, fmt.Errorf("insert skill: %w", err)
	}
	dir := skillDir(sk.ID)
	if files != nil {
		if err := s.writeSkillFilesAndArchive(sk.ID, files); err != nil {
			_, _ = s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, sk.ID)
			return nil, err
		}
	} else {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			_, _ = s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, sk.ID)
			return nil, fmt.Errorf("mkdir skill dir: %w", err)
		}
		if err := zipx.Extract(zipData, dir); err != nil {
			_, _ = s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, sk.ID)
			return nil, NewValidationError(err.Error())
		}
		archive, err := zipx.BuildFromDir(dir, "package.zip")
		if err != nil {
			_, _ = s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, sk.ID)
			return nil, fmt.Errorf("normalize skill archive: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.zip"), archive, 0o644); err != nil {
			_, _ = s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, sk.ID)
			return nil, fmt.Errorf("store skill archive: %w", err)
		}
	}
	return sk, nil
}

// Platform built-in skills. These ship inside the daemon binary (//go:embed
// under internal/service/assets) and are re-seeded on every boot, so they
// exist without network access and cannot drift from the binary's UI/docs.
// The id is a fixed constant (NOT newID) so the steward's agent.skills can
// reference it as a stable string across restarts and machines.
const (
	// BuiltinChatCreateID is the stable skill id of the conversation-first
	// creation skill (创建任务 / 团队 / 智能体 / 蓝本 by talking to the steward).
	BuiltinChatCreateID    = "7c1f3d5a9b2e4c8d8a6f0b3e5c7d9a21"
	BuiltinChatCreateName  = "agentwork-chat-create"
	BuiltinChatCreateTitle = "对话式创建"
)

// EnsureBuiltin installs or refreshes one platform-shipped skill: the row is
// upserted by its fixed id (title/description updated in place, created_at
// preserved) and the on-disk files are rewritten from the embedded copy every
// boot. Idempotent by design — callers run it at daemon startup and again when
// a machine registers, so a fresh install and an upgraded binary converge on
// the same skill.
//
// It refuses to proceed when another (user-created) skill already owns the
// name: a name collision would make the skill ambiguous in `agentwork skill
// list` and in agent pickers, so the caller should log and continue rather
// than hijack the user's row.
func (s *SkillService) EnsureBuiltin(ctx context.Context, id, name, title, description string, files map[string]string) (*Skill, error) {
	if strings.TrimSpace(files["SKILL.md"]) == "" {
		return nil, NewValidationError("内置技能缺少 SKILL.md")
	}
	var otherID string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT id FROM skill WHERE name=?`, name).Scan(&otherID)
	if otherID != "" && otherID != id {
		return nil, NewConflictError(fmt.Sprintf("skill name %q is already taken by %s", name, otherID))
	}
	var existingID string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT id FROM skill WHERE id=?`, id).Scan(&existingID)
	if existingID != "" {
		if _, err := s.st.DB().ExecContext(ctx,
			`UPDATE skill SET name=?, title=?, description=?, builtin=1 WHERE id=?`,
			name, title, description, id); err != nil {
			return nil, fmt.Errorf("refresh builtin skill %q: %w", name, err)
		}
	} else if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO skill (id,name,title,description,created_at,builtin) VALUES (?,?,?,?,?,1)`,
		id, name, title, description, now()); err != nil {
		return nil, fmt.Errorf("seed builtin skill %q: %w", name, err)
	}
	// Rewrite files from the binary every boot: the embedded copy is the
	// source of truth, and a half-written dir from a crashed boot must heal.
	_ = os.RemoveAll(skillDir(id))
	if err := s.writeSkillFilesAndArchive(id, files); err != nil {
		return nil, fmt.Errorf("write builtin skill files %q: %w", name, err)
	}
	return s.Get(ctx, id)
}

// parseSkillMarkdown extracts the skill name/title/description from a
// SKILL.md's YAML frontmatter (the `---`-delimited block at the top). The
// frontmatter is the Anthropic skill convention:
//
//	---
//	name: code-review-checklist
//	title: 代码审查清单
//	description: One-line summary
//	---
//	<instructions body>
//
// `title` is optional — it is the human-facing display name; a skill without
// one falls back to name in the UI. The frontmatter may carry OTHER fields
// beyond these three — those are part of the skill's contract but not stored
// on the platform row; they ride along in SKILL.md (pushed verbatim to the
// machine). Uses gopkg.in/yaml.v3 for robust parsing (quoted values,
// multi-line, lists, comments) rather than a fragile line-scanner. Mirrors
// openagent-go/skill/fs/loader.go's parseFrontmatter handling: CRLF
// normalization, EOF without a trailing newline. Returns ("", "", "") when
// there is no frontmatter or no name — the caller rejects that as
// "not a skill".
func parseSkillMarkdown(content string) (name, title, description string) {
	// Normalize CRLF → LF so Windows-authored SKILL.md files parse the same.
	text := strings.ReplaceAll(content, "\r\n", "\n")
	// A leading BOM (some editors add it) would break the "---\n" prefix
	// check; strip it.
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---\n") && text != "---" {
		return "", "", ""
	}
	// Opening delimiter is "---\n" (4 bytes) — find the closing "\n---\n".
	rest := text[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		// Closing separator at EOF without a trailing newline ("...\n---"):
		// the body after it is empty.
		if strings.HasSuffix(rest, "\n---") {
			idx = len(rest) - 4
		} else {
			return "", "", "" // unclosed frontmatter
		}
	}
	yamlBlock := rest[:idx]
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return "", "", ""
	}
	if n, ok := fm["name"].(string); ok {
		name = n
	}
	if t, ok := fm["title"].(string); ok {
		title = t
	}
	if d, ok := fm["description"].(string); ok {
		description = d
	}
	return name, title, description
}

// PackageZip returns the skill's NORMALIZED (flat) archive for config.push
// — the machine extracts it into the agent's staging dir with zipx.Extract.
// The stored package.zip is always flat (no top-level wrapper directory):
// createSkillFromArchive rebuilds it from the extracted dir, so every
// consumer sees one format. Skills created before this normalization have
// no stored archive: rebuild one from the extracted files once (flat) and
// persist it, so every skill pushes the same way.
func (s *SkillService) PackageZip(ctx context.Context, id string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(skillDir(id), "package.zip"))
	if err == nil {
		return b, nil
	}
	files, err := s.Files(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load skill files: %w", err)
	}
	zipData, err := zipx.Build(files)
	if err != nil {
		return nil, fmt.Errorf("rebuild skill archive: %w", err)
	}
	_ = os.WriteFile(filepath.Join(skillDir(id), "package.zip"), zipData, 0o644)
	return zipData, nil
}

// writeFiles materializes the skill's files under its directory. Paths are
// sanitized: no absolute paths, no .. — a skill must not write outside its
// own directory.
func (s *SkillService) writeFiles(skillID string, files map[string]string) error {
	dir := skillDir(skillID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir skill dir: %w", err)
	}
	for path, content := range files {
		clean := filepath.Clean(path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid skill file path %q", path)
		}
		target := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// UpsertByName creates or updates a skill by name (the team-import path).
// When the skill exists, its description is updated and its files are rebuilt
// from the supplied map (the old files are wiped first). When it does not
// exist, a new skill row is created the same way CreateFromZip creates one —
// name/description are supplied by the caller (already parsed from the team
// repo's SKILL.md by the import agent). Returns the skill.
func (s *SkillService) UpsertByName(ctx context.Context, name, description string, files map[string]string) (*Skill, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, NewValidationError("skill name is required")
	}
	if files == nil {
		files = map[string]string{}
	}
	title := ""
	if content := files["SKILL.md"]; strings.TrimSpace(content) != "" {
		_, title, _ = parseSkillMarkdown(content)
	}
	var existingID string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT id FROM skill WHERE name=?`, name).Scan(&existingID)
	if existingID != "" {
		if _, err := s.st.DB().ExecContext(ctx,
			`UPDATE skill SET description=?, title=? WHERE id=?`, description, title, existingID); err != nil {
			return nil, fmt.Errorf("update skill %q: %w", name, err)
		}
		_ = os.RemoveAll(skillDir(existingID))
		if err := s.writeSkillFilesAndArchive(existingID, files); err != nil {
			return nil, err
		}
		return &Skill{ID: existingID, Name: name, Title: title, Description: description}, nil
	}
	zipData, err := zipx.Build(files)
	if err != nil {
		return nil, NewValidationError(err.Error())
	}
	return s.createSkillFromArchive(ctx, name, title, description, zipData, files)
}

// writeSkillFilesAndArchive writes the file map to the skill's directory and
// stores the normalized flat package.zip. Shared by create and update paths.
func (s *SkillService) writeSkillFilesAndArchive(skillID string, files map[string]string) error {
	dir := skillDir(skillID)
	if err := s.writeFiles(skillID, files); err != nil {
		return err
	}
	archive, err := zipx.BuildFromDir(dir, "package.zip")
	if err != nil {
		return fmt.Errorf("normalize skill archive: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.zip"), archive, 0o644); err != nil {
		return fmt.Errorf("store skill archive: %w", err)
	}
	return nil
}

// List returns the library (oldest first).
func (s *SkillService) List(ctx context.Context) ([]Skill, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT `+skillSelectColumns+` FROM skill ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sk)
	}
	return out, rows.Err()
}

// Delete removes a skill from the library and its files.
func (s *SkillService) Delete(ctx context.Context, id string) error {
	// Built-in skills are part of the binary: they are re-seeded on the next
	// boot anyway, so deleting one would only make the library look wrong
	// until a restart. Refuse with a message that says what to do instead.
	var builtin int
	_ = s.st.DB().QueryRowContext(ctx, `SELECT builtin FROM skill WHERE id=?`, id).Scan(&builtin)
	if builtin != 0 {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM skill WHERE id=?`, id).Scan(&name)
		return NewValidationError(fmt.Sprintf("技能 %q 是平台内置技能，随版本发布，不能删除", name))
	}
	var n int
	// INSTR + a quoted id for the usage check: a pure substring test with no
	// wildcard semantics (LIKE also works on the pinned modernc.org/sqlite,
	// but a skill id ever containing % or _ would silently change LIKE's
	// meaning), and the surrounding quotes anchor the id so id "ab" cannot
	// match "abc". Daemon-side SkillAgents uses the same predicate.
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent WHERE INSTR(skills, ?) > 0`, `"`+id+`"`).Scan(&n); err != nil {
		return fmt.Errorf("check skill usage: %w", err)
	}
	if n > 0 {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM skill WHERE id=?`, id).Scan(&name)
		if name == "" {
			name = id
		}
		return NewCodedErrorDetail(CodeSkillSelected,
			fmt.Sprintf("skill %q is selected by %d agent(s); unselect it in those agents first", name, n),
			map[string]any{"name": name, "count": n})
	}
	if _, err := s.st.DB().ExecContext(ctx, `DELETE FROM skill WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	_ = os.RemoveAll(skillDir(id))
	return nil
}

// Files returns the skill's file map (path → content) for config.push.
func (s *SkillService) Files(ctx context.Context, id string) (map[string]string, error) {
	dir := skillDir(id)
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// SortedFilePaths returns the skill's file paths in a stable order.
func SortedFilePaths(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
