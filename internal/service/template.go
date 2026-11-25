package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sinkear/flowloop/internal/gitutil"
	"github.com/Sinkear/flowloop/internal/logging"
	"github.com/Sinkear/flowloop/internal/store"
	"gopkg.in/yaml.v3"
)

// Template settings keys (app_settings). platform.template_repo is the
// user-overridable source ({git_url, branch}); platform.template_cache holds
// the fetched templates JSON so GET /templates answers from local state even
// when GitCode is unreachable (fetched_at drives staleness).
const (
	templateRepoKey  = "platform.template_repo"
	templateCacheKey = "platform.template_cache"
	// There is deliberately NO built-in remote repository (m01058 #4): the
	// practice library must not fail or hit the network when the deployer has
	// not configured a source. An empty config means "no source configured" —
	// List returns empty, not a fetch error.
)

// TemplateRepoConfig is the platform.template_repo value.
type TemplateRepoConfig struct {
	GitURL string `json:"git_url"`
	Branch string `json:"branch"`
}

// TemplateMeta is the common front matter of every template (api_version +
// metadata) and the registry index entry shape.
type TemplateMeta struct {
	APIVersion  string   `yaml:"api_version" json:"api_version"`
	Kind        string   `yaml:"kind"        json:"kind"` // squad-template | project-template
	ID          string   `yaml:"id"          json:"id"`
	Name        string   `yaml:"name"        json:"name"`
	Description string   `yaml:"description" json:"description"`
	Version     string   `yaml:"version"     json:"version"`
	Tags        []string `yaml:"tags"        json:"tags,omitempty"`
	Icon        string   `yaml:"icon"        json:"icon,omitempty"`
}

// TemplateSummary is the list-item shape (metadata only, no spec).
type TemplateSummary struct {
	TemplateMeta
	Path string `json:"path"`
}

// TemplateDetail is the get-one shape: metadata + raw YAML + decoded spec.
type TemplateDetail struct {
	TemplateMeta
	Path     string          `json:"path"`
	SpecYAML string          `json:"spec_yaml"`
	Spec     json.RawMessage `json:"spec"` // the inner spec node only (repo/domain/team/…), re-encoded as JSON
}

// templateRegistry is the registry.yaml contract.
type templateRegistry struct {
	Templates []struct {
		ID          string   `yaml:"id"`
		Kind        string   `yaml:"kind"`
		Path        string   `yaml:"path"`
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Version     string   `yaml:"version"`
		Tags        []string `yaml:"tags"`
	} `yaml:"templates"`
}

// templateCache is the platform.template_cache value (the fetched snapshot).
type templateCache struct {
	FetchedAt string             `json:"fetched_at"`
	Repo      TemplateRepoConfig `json:"repo"`
	Templates []cachedTemplate   `json:"templates"`
	Errors    []string           `json:"errors,omitempty"`
	// SkillFiles are the embedded skill packages (skills/<name>/<file>:
	// content) captured at fetch time — ApplySquad hands them to
	// SkillService.Create without a second GitCode round-trip.
	SkillFiles map[string]string `json:"skill_files,omitempty"`
}

// cachedTemplate is one fetched template file (raw YAML kept for preview).
type cachedTemplate struct {
	TemplateMeta
	Path     string `json:"path"`
	SpecYAML string `json:"spec_yaml"`
}

// TemplateFetchResult reports one refresh.
type TemplateFetchResult struct {
	Fetched   int      `json:"fetched"`
	Errors    []string `json:"errors,omitempty"`
	FetchedAt string   `json:"fetched_at"`
}

// templateIDRe constrains metadata.id — it doubles as a URL path segment.
var templateIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

// Template kinds.
const (
	TemplateKindSquad   = "squad-template"
	TemplateKindProject = "project-template"
	TemplateKindAgent   = "agent-template"
)

// TemplateService owns the template source lifecycle: fetching the GitCode
// template repository (a shallow clone — the platform already depends on git,
// avoiding a bet on the largely undocumented contents API), validating each
// template's YAML, and caching the snapshot in app_settings.
type TemplateService struct {
	st       *store.Store
	settings *SettingsService
}

func NewTemplateService(st *store.Store, settings *SettingsService) *TemplateService {
	return &TemplateService{st: st, settings: settings}
}

// RepoConfig returns the configured template repo. Empty means no source is
// configured; the practice library then renders its empty state instead of
// attempting a network fetch.
func (s *TemplateService) RepoConfig(ctx context.Context) (TemplateRepoConfig, error) {
	var cfg TemplateRepoConfig
	raw, err := s.settings.Get(ctx, templateRepoKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return TemplateRepoConfig{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return TemplateRepoConfig{}, NewValidationError("platform.template_repo is not valid JSON: " + err.Error())
	}
	if strings.TrimSpace(cfg.GitURL) == "" {
		return TemplateRepoConfig{}, NewValidationError("platform.template_repo.git_url is required")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = "main"
	}
	return cfg, nil
}

// SetRepoConfig stores the template repo override (the settings PUT path).
func (s *TemplateService) SetRepoConfig(ctx context.Context, cfg TemplateRepoConfig) error {
	if strings.TrimSpace(cfg.GitURL) == "" {
		return NewFieldRequiredError("git_url")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		cfg.Branch = "main"
	}
	raw, _ := json.Marshal(cfg)
	return s.settings.Set(ctx, templateRepoKey, string(raw))
}

// Refresh re-fetches the template repository: shallow-clone to a temp dir,
// parse registry.yaml, load every listed template file, validate, and store
// the snapshot in app_settings. On fetch failure the error is returned AND
// the previous cache is kept (the list endpoint degrades gracefully).
func (s *TemplateService) Refresh(ctx context.Context, token string) (*TemplateFetchResult, error) {
	cfg, err := s.RepoConfig(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.GitURL) == "" {
		return nil, NewCodedError(CodeTemplateFetchFailed, "实践库源尚未接入——配置 platform.template_repo 后再刷新")
	}
	// Bound the clone independently of the request ctx — a dead GitCode host
	// must not wedge the caller. refreshTimeout was previously declared but
	// never applied, so a hanging clone blocked until the HTTP client timed
	// out (and List's auto-refresh path inherits the same gate).
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	files, err := fetchTemplateFiles(ctx, cfg, token)
	if err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateFetchFailed,
			fmt.Sprintf("拉取模板仓库失败：%v", err),
			map[string]any{"repo": gitutil.SanitizeURL(cfg.GitURL)})
	}
	cache, ferr := parseTemplateFiles(files)
	if ferr != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, ferr.Error(), nil)
	}
	cache.FetchedAt = now()
	cache.Repo = cfg
	// Keep embedded skill packages on the snapshot (skills/<name>/<file>).
	// SkillFiles is nil unless initialized — assigning into a nil map panics.
	if cache.SkillFiles == nil {
		cache.SkillFiles = map[string]string{}
	}
	for path, content := range files {
		if strings.HasPrefix(path, "skills/") {
			cache.SkillFiles[path] = content
		}
	}
	raw, _ := json.Marshal(cache)
	if err := s.settings.Set(ctx, templateCacheKey, string(raw)); err != nil {
		return nil, fmt.Errorf("store template cache: %w", err)
	}
	logging.Infof("templates: refreshed from %s — %d template(s), %d error(s)",
		gitutil.SanitizeURL(cfg.GitURL), len(cache.Templates), len(cache.Errors))
	return &TemplateFetchResult{Fetched: len(cache.Templates), Errors: cache.Errors, FetchedAt: cache.FetchedAt}, nil
}

// fetchTemplateFiles is a var so tests can stub the git clone without network.
var fetchTemplateFiles = fetchTemplateFilesFromGit

// fetchTemplateFilesFromGit shallow-clones the repo and returns {relative path: content}
// for registry.yaml and every template/skill file it references. A token,
// when given, is embedded via gitCloneURL and never logged.
func fetchTemplateFilesFromGit(ctx context.Context, cfg TemplateRepoConfig, token string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "flowloop-templates-*")
	if err != nil {
		return nil, fmt.Errorf("mkdir temp: %w", err)
	}
	defer os.RemoveAll(dir)
	url := gitutil.CloneURL(cfg.GitURL, token)
	args := []string{"clone", "--depth", "1"}
	if cfg.Branch != "" {
		args = append(args, "--branch", cfg.Branch, "--single-branch")
	}
	args = append(args, url, dir)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %v: %s", err, strings.TrimSpace(gitutil.SanitizeURL(strings.TrimSpace(string(out)))))
	}
	files := map[string]string{}
	read := func(rel string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	regRaw, err := read("registry.yaml")
	if err != nil {
		return nil, fmt.Errorf("registry.yaml missing at repo root: %w", err)
	}
	files["registry.yaml"] = regRaw
	var reg templateRegistry
	if err := yaml.Unmarshal([]byte(regRaw), &reg); err != nil {
		return nil, fmt.Errorf("parse registry.yaml: %w", err)
	}
	if len(reg.Templates) == 0 {
		return nil, fmt.Errorf("registry.yaml lists no templates")
	}
	for _, entry := range reg.Templates {
		content, err := read(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("registry entry %q: read %s: %w", entry.ID, entry.Path, err)
		}
		files[entry.Path] = content
	}
	// skills/ embedded packages: every file under skills/ travels with the
	// snapshot so ApplySquad can hand whole directories to SkillService.
	skillsRoot := filepath.Join(dir, "skills")
	if entries, err := os.ReadDir(skillsRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			_ = filepath.WalkDir(filepath.Join(skillsRoot, e.Name()), func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil // ignore (best effort — a broken skill dir fails at apply time)
				}
				rel, rerr := filepath.Rel(dir, p)
				if rerr != nil {
					return nil
				}
				if c, rerr := os.ReadFile(p); rerr == nil {
					files[rel] = string(c)
				}
				return nil
			})
		}
	}
	return files, nil
}

// parseTemplateFiles validates the fetched files into a cache snapshot.
// Every registry entry must parse and carry a consistent api_version/kind/id;
// a bad template is reported in Errors and skipped (the rest stay usable).
func parseTemplateFiles(files map[string]string) (*templateCache, error) {
	var reg templateRegistry
	if err := yaml.Unmarshal([]byte(files["registry.yaml"]), &reg); err != nil {
		return nil, fmt.Errorf("parse registry.yaml: %w", err)
	}
	cache := &templateCache{SkillFiles: map[string]string{}}
	for _, entry := range reg.Templates {
		content := files[entry.Path]
		var meta TemplateMeta
		if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: parse metadata: %v", entry.Path, err))
			continue
		}
		// "not: a template" parses as an empty TemplateMeta — a template file
		// must at least carry api_version + kind to be considered one.
		if meta.APIVersion == "" && meta.Kind == "" && meta.ID == "" {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: not a template file (missing api_version/kind)", entry.Path))
			continue
		}
		if !templateIDRe.MatchString(entry.ID) {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: registry id %q is not kebab-case", entry.Path, entry.ID))
			continue
		}
		switch {
		case meta.APIVersion == "":
			meta.APIVersion = "flowloop/v1"
		case meta.APIVersion != "flowloop/v1":
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: unsupported api_version %q", entry.Path, meta.APIVersion))
			continue
		}
		// Registry is authoritative for identity; the file must not disagree.
		if meta.Kind == "" {
			meta.Kind = entry.Kind
		}
		if meta.Kind != entry.Kind {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: kind %q disagrees with registry %q", entry.Path, meta.Kind, entry.Kind))
			continue
		}
		if meta.ID == "" {
			meta.ID = entry.ID
		}
		if meta.ID != entry.ID {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: id %q disagrees with registry %q", entry.Path, meta.ID, entry.ID))
			continue
		}
		if meta.Name == "" {
			meta.Name = entry.Name
		}
		if meta.Description == "" {
			meta.Description = entry.Description
		}
		if meta.Version == "" {
			meta.Version = entry.Version
		}
		if len(meta.Tags) == 0 {
			meta.Tags = entry.Tags
		}
		if meta.Kind != TemplateKindSquad && meta.Kind != TemplateKindProject && meta.Kind != TemplateKindAgent {
			cache.Errors = append(cache.Errors, fmt.Sprintf("template %s: kind must be squad-template, project-template or agent-template, got %q", entry.Path, meta.Kind))
			continue
		}
		cache.Templates = append(cache.Templates, cachedTemplate{TemplateMeta: meta, Path: entry.Path, SpecYAML: content})
	}
	return cache, nil
}

// cached reads the snapshot from app_settings; not-found maps to a coded
// error (never fetched, or wiped).
func (s *TemplateService) cached(ctx context.Context) (*templateCache, error) {
	raw, err := s.settings.Get(ctx, templateCacheKey)
	if err != nil || raw == "" {
		return nil, NewCodedError(CodeTemplateFetchFailed, "模板尚未拉取——请先刷新模板列表")
	}
	var c templateCache
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("corrupt template cache: %w", err)
	}
	return &c, nil
}

// List returns the cached templates' metadata (optionally filtered by kind:
// "squad" | "project" | full kind | ” = all). An empty cache triggers a
// best-effort fetch so the first page load works with zero clicks.
func (s *TemplateService) List(ctx context.Context, kind string) ([]TemplateSummary, error) {
	cfg, cerr := s.RepoConfig(ctx)
	if cerr != nil {
		return nil, cerr
	}
	if strings.TrimSpace(cfg.GitURL) == "" {
		// No source configured: the practice library is a valid empty state,
		// never a 400 from an unreachable GitCode repo.
		return []TemplateSummary{}, nil
	}
	c, err := s.cached(ctx)
	if err != nil {
		if _, rerr := s.Refresh(ctx, ""); rerr != nil {
			return nil, err // surface the original (coded) cache error
		}
		if c, err = s.cached(ctx); err != nil {
			return nil, err
		}
	}
	filter := normalizeKindFilter(kind)
	out := []TemplateSummary{}
	for _, t := range c.Templates {
		if filter != "" && t.Kind != filter {
			continue
		}
		out = append(out, TemplateSummary{TemplateMeta: t.TemplateMeta, Path: t.Path})
	}
	return out, nil
}

// Get returns one cached template in full (raw YAML + JSON spec).
func (s *TemplateService) Get(ctx context.Context, kind, id string) (*TemplateDetail, error) {
	c, err := s.cached(ctx)
	if err != nil {
		return nil, err
	}
	filter := normalizeKindFilter(kind)
	for _, t := range c.Templates {
		if t.ID != id || (filter != "" && t.Kind != filter) {
			continue
		}
		specJSON, err := templateSpecJSON(t.SpecYAML)
		if err != nil {
			return nil, NewCodedErrorDetail(CodeTemplateInvalid,
				fmt.Sprintf("模板 %s 的 spec 无法解析：%v", id, err), nil)
		}
		return &TemplateDetail{TemplateMeta: t.TemplateMeta, Path: t.Path, SpecYAML: t.SpecYAML, Spec: specJSON}, nil
	}
	return nil, NewCodedErrorDetail(CodeTemplateNotFound,
		fmt.Sprintf("模板 %q 不存在（kind=%s）——刷新模板列表后重试", id, kind),
		map[string]any{"id": id, "kind": kind})
}

// LoadRaw returns one cached template's raw YAML (the apply path's input).
func (s *TemplateService) LoadRaw(ctx context.Context, kind, id string) (string, error) {
	c, err := s.cached(ctx)
	if err != nil {
		return "", err
	}
	filter := normalizeKindFilter(kind)
	for _, t := range c.Templates {
		if t.ID == id && (filter == "" || t.Kind == filter) {
			return t.SpecYAML, nil
		}
	}
	return "", NewCodedErrorDetail(CodeTemplateNotFound,
		fmt.Sprintf("模板 %q 不存在（kind=%s）", id, kind), map[string]any{"id": id, "kind": kind})
}

// SkillPackage returns the file map of an embedded skill package (skills/<name>/…)
// from the cached snapshot. ApplySquad uses it to hand whole packages to
// SkillService. ok=false when the package does not exist in the snapshot.
func (s *TemplateService) SkillPackage(ctx context.Context, name string) (map[string]string, bool, error) {
	c, err := s.cached(ctx)
	if err != nil {
		return nil, false, err
	}
	prefix := "skills/" + name + "/"
	files := map[string]string{}
	for path, content := range c.SkillFiles {
		if after, ok := strings.CutPrefix(path, prefix); ok && after != "" && after != "package.zip" {
			files[after] = content
		}
	}
	if len(files) == 0 {
		return nil, false, nil
	}
	return files, true, nil
}

// templateSpecJSON extracts the inner spec node of a template YAML document
// and re-encodes it as JSON so the frontend can consume it directly —
// TemplateDetail.Spec is the spec section only (repo/domain/team/goals/
// schedules), never the whole document (metadata siblings would force the
// frontend into spec.spec paths and broke repo_token detection).
func templateSpecJSON(doc string) (json.RawMessage, error) {
	var v any
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(false)
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return json.RawMessage("null"), nil // empty document = no spec section
		}
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return json.RawMessage("null"), nil
	}
	spec := m["spec"]
	if spec == nil {
		return json.RawMessage("null"), nil
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// normalizeKindFilter maps the query filter ("squad" | "project" | "agent" |
// full kind) to a full kind; "" = unfiltered.
func normalizeKindFilter(kind string) string {
	switch strings.TrimSpace(kind) {
	case "squad":
		return TemplateKindSquad
	case "project":
		return TemplateKindProject
	case "agent":
		return TemplateKindAgent
	case TemplateKindSquad, TemplateKindProject, TemplateKindAgent:
		return strings.TrimSpace(kind)
	default:
		return ""
	}
}

// refreshTimeout bounds one clone — a dead host must not wedge the request.
const refreshTimeout = 2 * time.Minute

// ---------------------------------------------------------------------------
// Practice package import
//
// A practice package is a self-contained JSON document. Unlike the GitCode
// template library it needs no configured source: the user supplies the JSON
// and import creates private copies of every entity, so a package can never
// overwrite existing work. Project, squad and workflow packages are supported.
// ---------------------------------------------------------------------------

var practiceCapabilityRe = regexp.MustCompile(`^[a-z0-9_.-]+$`)

const (
	practiceAPIVersion   = "flowloop/v1"
	practiceKindSquad    = "squad"
	practiceKindProject  = "project"
	practiceKindWorkflow = "workflow"
)

// PracticeImportRequest is the body of POST /templates/import/preview and
// POST /templates/import. Package is the raw practice-package JSON; the other
// fields are the user's choices made in the import wizard.
type PracticeImportRequest struct {
	Package        json.RawMessage `json:"package"`
	Name           string          `json:"name"`
	SquadID        string          `json:"squad_id"`
	GitURL         string          `json:"git_url"`
	GitCredentials string          `json:"git_credentials"`
	ProjectType    string          `json:"project_type"`
}

// PracticeImportAgent describes one agent the import will create.
type PracticeImportAgent struct {
	Name      string `json:"name"`
	FinalName string `json:"final_name"`
	Renamed   bool   `json:"renamed,omitempty"`
}

// PracticeImportPreview is the non-mutating result of planning an import.
type PracticeImportPreview struct {
	Kind              string                `json:"kind"`
	PackageID         string                `json:"package_id,omitempty"`
	Name              string                `json:"name"`
	OriginalName      string                `json:"original_name,omitempty"`
	Description       string                `json:"description,omitempty"`
	Version           string                `json:"version,omitempty"`
	Source            string                `json:"source"`
	SourceLabel       string                `json:"source_label"`
	SourceNote        string                `json:"source_note,omitempty"`
	ProjectType       string                `json:"project_type,omitempty"`
	GitURL            string                `json:"git_url,omitempty"`
	NeedsGitURL       bool                  `json:"needs_git_url,omitempty"`
	OwnerSquad        string                `json:"owner_squad,omitempty"`
	OwnerSquadID      string                `json:"owner_squad_id,omitempty"`
	NeedsSquad        bool                  `json:"needs_squad,omitempty"`
	Agents            []PracticeImportAgent `json:"agents,omitempty"`
	TeamSquad         string                `json:"team_squad,omitempty"`
	Goals             []string              `json:"goals,omitempty"`
	Schedules         []string              `json:"schedules,omitempty"`
	WorkflowNodeCount int                   `json:"workflow_node_count,omitempty"`
	NameSuffix        bool                  `json:"name_suffix,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
}

// PracticeImportRef points at one created entity. URL is a UI-relative link.
type PracticeImportRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PracticeImportResult is returned by POST /templates/import. It is built from
// what actually happened — Items records per-entity failures, never fake ones.
type PracticeImportResult struct {
	Preview   PracticeImportPreview `json:"preview"`
	Project   *PracticeImportRef    `json:"project,omitempty"`
	Squad     *PracticeImportRef    `json:"squad,omitempty"`
	Workflow  *PracticeImportRef    `json:"workflow,omitempty"`
	Agents    []PracticeImportRef   `json:"agents,omitempty"`
	Goals     []PracticeImportRef   `json:"goals,omitempty"`
	Schedules []PracticeImportRef   `json:"schedules,omitempty"`
	Items     []ApplyItem           `json:"items,omitempty"`
	Warnings  []string              `json:"warnings,omitempty"`
}

// PracticeImportService validates practice packages and imports copies of
// their contents. It reuses TemplateApplyService (same package) so agent,
// squad, domain, goal and schedule creation stay in one place.
type PracticeImportService struct {
	apply      *TemplateApplyService
	blueprints *BlueprintService
}

// NewPracticeImportService builds an import service. blueprints may be nil:
// only workflow packages then report that workflow import is unavailable.
func NewPracticeImportService(apply *TemplateApplyService, blueprints *BlueprintService) *PracticeImportService {
	return &PracticeImportService{apply: apply, blueprints: blueprints}
}

// Preview validates a package and reports exactly what an import would create,
// without writing anything.
func (s *PracticeImportService) Preview(ctx context.Context, req PracticeImportRequest) (*PracticeImportPreview, error) {
	plan, err := s.plan(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plan.preview, nil
}

type practicePackage struct {
	APIVersion string           `json:"api_version"`
	Kind       string           `json:"kind"`
	Metadata   practiceMetadata `json:"metadata"`
	Source     string           `json:"source"`
	Spec       json.RawMessage  `json:"spec"`
}

type practiceMetadata struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Tags        []string `json:"tags"`
}

type practiceWorkflowSpec struct {
	Squad string    `json:"squad"`
	Graph SpecGraph `json:"graph"`
}

// practicePlan carries everything preview showed and import needs, so both
// paths agree on final names and validation.
type practicePlan struct {
	preview             PracticeImportPreview
	squadSpec           *squadSpec
	projectSpec         *projectSpec
	workflowSpec        *practiceWorkflowSpec
	workflowContent     string
	workflowName        string
	workflowDescription string
	projectType         string
	gitURL              string
	branch              string
	projectName         string
	teamSquadName       string
	teamSquadOriginal   string
	owner               *Squad
	renameMap           map[string]string
}

func (s *PracticeImportService) plan(ctx context.Context, req PracticeImportRequest) (*practicePlan, error) {
	if s.apply == nil {
		return nil, errors.New("实践库导入服务未配置")
	}
	if len(bytes.TrimSpace(req.Package)) == 0 || string(bytes.TrimSpace(req.Package)) == "null" {
		return nil, NewFieldRequiredError("package")
	}
	var pkg practicePackage
	if err := decodeStrictJSON(req.Package, &pkg); err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包不是合法的 JSON："+err.Error(), map[string]any{"reason": "json"})
	}
	kind, err := practicePackageKind(pkg.Kind)
	if err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, err.Error(), map[string]any{"reason": "kind"})
	}
	if strings.TrimSpace(pkg.Metadata.Name) == "" {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包缺少 metadata.name", map[string]any{"reason": "metadata"})
	}
	if v := strings.TrimSpace(pkg.APIVersion); v != "" && v != practiceAPIVersion {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, fmt.Sprintf("不支持的 api_version %q：当前只支持 %q", pkg.APIVersion, practiceAPIVersion), map[string]any{"reason": "api_version"})
	}

	source, sourceWarnings, sourceNote := practiceSourceInfo(pkg.Source)
	plan := &practicePlan{
		renameMap: map[string]string{},
	}
	plan.preview = PracticeImportPreview{
		Kind:        kind,
		PackageID:   strings.TrimSpace(pkg.Metadata.ID),
		Description: strings.TrimSpace(pkg.Metadata.Description),
		Version:     strings.TrimSpace(pkg.Metadata.Version),
		Source:      source,
		SourceLabel: practiceSourceLabel(source),
		SourceNote:  sourceNote,
		Warnings:    append([]string{}, sourceWarnings...),
	}

	switch kind {
	case practiceKindSquad:
		spec, err := s.parseSquadSpec(pkg)
		if err != nil {
			return nil, err
		}
		if err := validatePracticeSquad(spec); err != nil {
			return nil, err
		}
		if strings.TrimSpace(spec.Strategy) == conflictUpsert {
			plan.preview.Warnings = append(plan.preview.Warnings, "实践包声明了“存在则更新”；导入永远创建新副本，包内的 strategy 设置不生效。")
		}
		baseName := strings.TrimSpace(req.Name)
		if baseName == "" {
			baseName = strings.TrimSpace(spec.Squad.Name)
		}
		finalName, suffixed, err := copyName(baseName, func(name string) (bool, error) {
			return s.squadNameTaken(ctx, name)
		})
		if err != nil {
			return nil, err
		}
		if suffixed {
			plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名小队「%s」，将创建为「%s」。", baseName, finalName))
		}
		agents, rename, err := s.planAgents(ctx, spec.Agents)
		if err != nil {
			return nil, err
		}
		for original, final := range rename {
			if original != final {
				plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名 Agent「%s」，将创建为「%s」。", original, final))
			}
		}
		if taken, err := s.domainNameTaken(ctx, finalName); err != nil {
			return nil, err
		} else if taken {
			plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名项目「%s」，小队会复用它作为默认工作区，不会修改该项目。", finalName))
		}
		plan.renameMap = rename
		plan.squadSpec = spec
		plan.preview.Name = finalName
		plan.preview.OriginalName = baseName
		plan.preview.NameSuffix = suffixed
		plan.preview.Agents = agents
		if plan.preview.Description == "" {
			plan.preview.Description = strings.TrimSpace(spec.Squad.Description)
		}
		return plan, nil

	case practiceKindProject:
		spec, err := s.parseProjectSpec(pkg)
		if err != nil {
			return nil, err
		}
		if err := validatePracticeProject(spec); err != nil {
			return nil, err
		}
		if spec.Team != nil && strings.TrimSpace(spec.Team.Strategy) == conflictUpsert {
			plan.preview.Warnings = append(plan.preview.Warnings, "实践包声明了“存在则更新”；导入永远创建新副本，包内的 strategy 设置不生效。")
		}
		projectType, gitURL, branch, needsGitURL, err := resolvePracticeProjectTarget(spec, req)
		if err != nil {
			return nil, err
		}
		baseName := strings.TrimSpace(req.Name)
		if baseName == "" {
			baseName = strings.TrimSpace(pkg.Metadata.Name)
		}
		finalName, suffixed, err := copyName(baseName, func(name string) (bool, error) {
			return s.domainNameTaken(ctx, name)
		})
		if err != nil {
			return nil, err
		}
		if suffixed {
			plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名项目「%s」，将创建为「%s」。", baseName, finalName))
		}
		plan.projectType = projectType
		plan.gitURL = gitURL
		plan.branch = branch
		plan.projectName = finalName
		plan.projectSpec = spec
		plan.preview.Name = finalName
		plan.preview.OriginalName = baseName
		plan.preview.NameSuffix = suffixed
		plan.preview.ProjectType = projectType
		plan.preview.GitURL = gitURL
		plan.preview.NeedsGitURL = needsGitURL
		if needsGitURL {
			plan.preview.Warnings = append(plan.preview.Warnings, "这是一个仓库项目，需要填写 Git 仓库地址后才能导入；未填写不会创建任何内容。")
		}
		if spec.Team != nil {
			if err := validatePracticeSquad(spec.Team); err != nil {
				return nil, err
			}
			agents, rename, err := s.planAgents(ctx, spec.Team.Agents)
			if err != nil {
				return nil, err
			}
			for original, final := range rename {
				if original != final {
					plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名 Agent「%s」，将创建为「%s」。", original, final))
				}
			}
			teamBase := strings.TrimSpace(spec.Team.Squad.Name)
			teamFinal, teamSuffixed, err := copyName(teamBase, func(name string) (bool, error) {
				return s.squadNameTaken(ctx, name)
			})
			if err != nil {
				return nil, err
			}
			if teamSuffixed {
				plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("已有同名小队「%s」，将创建为「%s」。", teamBase, teamFinal))
			}
			plan.renameMap = rename
			plan.teamSquadName = teamFinal
			plan.teamSquadOriginal = teamBase
			plan.preview.Agents = agents
			plan.preview.TeamSquad = teamFinal
		}
		for _, g := range spec.Goals {
			title := strings.TrimSpace(g.Title)
			if title == "" {
				title = "（未命名目标）"
			}
			plan.preview.Goals = append(plan.preview.Goals, title)
		}
		for _, sc := range spec.Schedules {
			name := strings.TrimSpace(sc.Name)
			if name == "" {
				name = "（未命名定时任务）"
			}
			plan.preview.Schedules = append(plan.preview.Schedules, name)
		}
		return plan, nil

	case practiceKindWorkflow:
		spec, err := s.parseWorkflowSpec(pkg)
		if err != nil {
			return nil, err
		}
		owner, err := s.resolveOwnerSquad(ctx, req.SquadID, spec.Squad)
		if err != nil {
			return nil, err
		}
		baseName := strings.TrimSpace(req.Name)
		if baseName == "" {
			baseName = strings.TrimSpace(spec.Graph.Name)
		}
		if baseName == "" {
			baseName = strings.TrimSpace(pkg.Metadata.Name)
		}
		finalName := baseName
		suffixed := false
		if owner != nil {
			finalName, suffixed, err = copyName(baseName, func(name string) (bool, error) {
				return s.workflowNameTaken(ctx, owner.ID, name)
			})
			if err != nil {
				return nil, err
			}
			if suffixed {
				plan.preview.Warnings = append(plan.preview.Warnings, fmt.Sprintf("同名小队下已有工作流「%s」，将创建为「%s」。", baseName, finalName))
			}
		}
		content, err := s.prepareWorkflowGraph(ctx, spec.Graph, owner, finalName)
		if err != nil {
			return nil, err
		}
		plan.workflowSpec = spec
		plan.workflowContent = content
		plan.workflowName = finalName
		plan.workflowDescription = strings.TrimSpace(spec.Graph.Description)
		if plan.workflowDescription == "" {
			plan.workflowDescription = plan.preview.Description
		}
		plan.owner = owner
		plan.preview.Name = finalName
		plan.preview.OriginalName = baseName
		plan.preview.NameSuffix = suffixed
		plan.preview.WorkflowNodeCount = len(spec.Graph.Nodes)
		if owner != nil {
			plan.preview.OwnerSquad = owner.Name
			plan.preview.OwnerSquadID = owner.ID
		} else {
			plan.preview.NeedsSquad = true
			plan.preview.Warnings = append(plan.preview.Warnings, "工作流必须归属一支小队：请选择归属小队后再导入。")
		}
		if plan.preview.Description == "" {
			plan.preview.Description = plan.workflowDescription
		}
		return plan, nil
	}
	return nil, NewCodedErrorDetail(CodeTemplateInvalid, "不支持的实践包类型", map[string]any{"reason": "kind"})
}

// decodeStrictJSON rejects unknown fields and trailing garbage, so a typo in a
// package cannot be silently ignored.
func decodeStrictJSON(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("JSON 结尾存在多余内容")
		}
		return err
	}
	return nil
}

func practicePackageKind(kind string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case practiceKindSquad, "squad-template", "team", "team-template":
		return practiceKindSquad, nil
	case practiceKindProject, "project-template":
		return practiceKindProject, nil
	case practiceKindWorkflow, "workflow-template", "blueprint", "blueprint-template":
		return practiceKindWorkflow, nil
	}
	return "", fmt.Errorf("不支持的实践包类型 %q：当前支持 squad、project、workflow", kind)
}

func practiceSourceInfo(source string) (string, []string, string) {
	s := strings.TrimSpace(strings.ToLower(source))
	switch s {
	case "", "official", "community", "unverified":
	default:
		return "unverified", []string{fmt.Sprintf("忽略了无法识别的来源标记「%s」。", source)},
			"包内来源声明不采信：本地导入一律视为未验证。"
	}
	if s == "" || s == "unverified" {
		return "unverified", nil, "本地导入的实践包一律视为未验证；请检查其中的指令与脚本后再使用。"
	}
	return "unverified", []string{fmt.Sprintf("包内声称来源为「%s」，但本地导入不采信来源声明，一律标为未验证，请自行检查内容。", practiceSourceLabel(s))},
		"包内来源声明不采信：本地导入一律视为未验证。"
}

func practiceSourceLabel(source string) string {
	switch source {
	case "official":
		return "官方验证"
	case "community":
		return "社区分享"
	default:
		return "未验证"
	}
}

func (s *PracticeImportService) parseSquadSpec(pkg practicePackage) (*squadSpec, error) {
	doc, err := practiceTemplateDoc("squad-template", pkg)
	if err != nil {
		return nil, err
	}
	_, parsed, err := parseTemplate(doc)
	if err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包内容有误："+err.Error(), map[string]any{"reason": "spec"})
	}
	spec, ok := parsed.(*squadSpec)
	if !ok {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包内容不是一个小队包", map[string]any{"reason": "spec"})
	}
	return spec, nil
}

func (s *PracticeImportService) parseProjectSpec(pkg practicePackage) (*projectSpec, error) {
	doc, err := practiceTemplateDoc("project-template", pkg)
	if err != nil {
		return nil, err
	}
	_, parsed, err := parseTemplate(doc)
	if err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包内容有误："+err.Error(), map[string]any{"reason": "spec"})
	}
	spec, ok := parsed.(*projectSpec)
	if !ok {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包内容不是一个项目包", map[string]any{"reason": "spec"})
	}
	return spec, nil
}

func (s *PracticeImportService) parseWorkflowSpec(pkg practicePackage) (*practiceWorkflowSpec, error) {
	if len(bytes.TrimSpace(pkg.Spec)) == 0 || string(bytes.TrimSpace(pkg.Spec)) == "null" {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "实践包缺少 spec 内容", map[string]any{"reason": "spec"})
	}
	var spec practiceWorkflowSpec
	if err := decodeStrictJSON(pkg.Spec, &spec); err != nil {
		return nil, NewCodedErrorDetail(CodeTemplateInvalid, "工作流实践包内容有误："+err.Error(), map[string]any{"reason": "spec"})
	}
	return &spec, nil
}

// practiceTemplateDoc wraps the package's generic spec in the YAML template
// shape parseTemplate already validates strictly. The synthetic id is never
// persisted — it only satisfies the template parser.
func practiceTemplateDoc(kindFull string, pkg practicePackage) (string, error) {
	if len(bytes.TrimSpace(pkg.Spec)) == 0 || string(bytes.TrimSpace(pkg.Spec)) == "null" {
		return "", NewCodedErrorDetail(CodeTemplateInvalid, "实践包缺少 spec 内容", map[string]any{"reason": "spec"})
	}
	var spec any
	if err := json.Unmarshal(pkg.Spec, &spec); err != nil {
		return "", NewCodedErrorDetail(CodeTemplateInvalid, "实践包 spec 不是合法的 JSON："+err.Error(), map[string]any{"reason": "spec"})
	}
	meta := map[string]any{"id": "practice-package", "name": pkg.Metadata.Name}
	if id := templateSlug(pkg.Metadata.Name); id != "" {
		meta["id"] = id
	}
	if pkg.Metadata.Description != "" {
		meta["description"] = pkg.Metadata.Description
	}
	if pkg.Metadata.Version != "" {
		meta["version"] = pkg.Metadata.Version
	}
	doc := map[string]any{
		"api_version": practiceAPIVersion,
		"kind":        kindFull,
		"metadata":    meta,
		"spec":        spec,
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", NewCodedErrorDetail(CodeTemplateInvalid, "实践包内容无法序列化："+err.Error(), map[string]any{"reason": "spec"})
	}
	return string(out), nil
}

func validatePracticeSquad(spec *squadSpec) error {
	if spec == nil {
		return NewValidationError("实践包缺少小队内容")
	}
	if len(spec.Agents) == 0 {
		return NewValidationError("实践包的 spec.agents 不能为空：至少需要一个 Agent")
	}
	seen := map[string]bool{}
	for i := range spec.Agents {
		name := strings.TrimSpace(spec.Agents[i].Name)
		if name == "" {
			return NewValidationError(fmt.Sprintf("第 %d 个 Agent 缺少 name", i+1))
		}
		if seen[name] {
			return NewValidationError(fmt.Sprintf("实践包内 Agent 名称重复：%q", name))
		}
		seen[name] = true
		spec.Agents[i].Name = name
	}
	squadName := strings.TrimSpace(spec.Squad.Name)
	if squadName == "" {
		return NewValidationError("小队包缺少 spec.squad.name")
	}
	leader := strings.TrimSpace(spec.Squad.Leader)
	if leader == "" {
		return NewValidationError("小队包缺少 spec.squad.leader")
	}
	if !seen[leader] {
		return NewValidationError(fmt.Sprintf("spec.squad.leader %q 不在 spec.agents 中", leader))
	}
	spec.Squad.Name = squadName
	spec.Squad.Leader = leader
	for _, m := range spec.Squad.Members {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			return NewValidationError("spec.squad.members 里有成员缺少 name")
		}
		if !seen[name] {
			return NewValidationError(fmt.Sprintf("spec.squad.members 引用了不存在的 Agent %q", name))
		}
	}
	return nil
}

func validatePracticeProject(spec *projectSpec) error {
	if spec == nil {
		return NewValidationError("实践包缺少项目内容")
	}
	if spec.Repo != nil && spec.Repo.Create {
		return NewValidationError("项目包要求自动创建 Git 仓库；本地导入不会代建仓库，请改用 scratch 项目或手动填写仓库地址")
	}
	switch tp := strings.TrimSpace(spec.Domain.Type); tp {
	case "", "repo", "scratch":
	default:
		return NewValidationError(fmt.Sprintf("项目类型 %q 不受支持：只支持 repo 或 scratch", spec.Domain.Type))
	}
	if spec.Team != nil {
		if err := validatePracticeSquad(spec.Team); err != nil {
			return err
		}
	}
	for i, g := range spec.Goals {
		if strings.TrimSpace(g.Title) == "" {
			return NewValidationError(fmt.Sprintf("第 %d 个目标缺少 title", i+1))
		}
		if strings.TrimSpace(g.Assignee) == "" {
			return NewValidationError(fmt.Sprintf("目标「%s」缺少 assignee", g.Title))
		}
		typ := strings.TrimSpace(g.AssigneeType)
		if typ == "" {
			typ = "agent"
		}
		if typ != "agent" && typ != "squad" {
			return NewValidationError(fmt.Sprintf("目标「%s」的 assignee_type %q 不受支持：只支持 agent 或 squad", g.Title, g.AssigneeType))
		}
	}
	for i, sc := range spec.Schedules {
		if strings.TrimSpace(sc.Name) == "" {
			return NewValidationError(fmt.Sprintf("第 %d 个定时任务缺少 name", i+1))
		}
		if strings.TrimSpace(sc.Cron) == "" {
			return NewValidationError(fmt.Sprintf("定时任务「%s」缺少 cron", sc.Name))
		}
		if strings.TrimSpace(sc.Title) == "" {
			return NewValidationError(fmt.Sprintf("定时任务「%s」缺少 title", sc.Name))
		}
		if strings.TrimSpace(sc.Assignee) == "" {
			return NewValidationError(fmt.Sprintf("定时任务「%s」缺少 assignee", sc.Name))
		}
		typ := strings.TrimSpace(sc.AssigneeType)
		if typ == "" {
			typ = "agent"
		}
		if typ != "agent" && typ != "squad" {
			return NewValidationError(fmt.Sprintf("定时任务「%s」的 assignee_type %q 不受支持：只支持 agent 或 squad", sc.Name, sc.AssigneeType))
		}
	}
	return nil
}

func resolvePracticeProjectTarget(spec *projectSpec, req PracticeImportRequest) (string, string, string, bool, error) {
	projectType := strings.TrimSpace(req.ProjectType)
	if projectType == "" {
		projectType = strings.TrimSpace(spec.Domain.Type)
	}
	if projectType == "" {
		projectType = "repo"
	}
	if projectType != "repo" && projectType != "scratch" {
		return "", "", "", false, NewValidationError(fmt.Sprintf("项目类型 %q 不受支持：只支持 repo 或 scratch", projectType))
	}
	gitURL := strings.TrimSpace(req.GitURL)
	if gitURL == "" {
		gitURL = strings.TrimSpace(spec.Domain.GitURL)
	}
	branch := strings.TrimSpace(spec.Domain.DefaultBranch)
	if branch == "" {
		branch = "main"
	}
	if projectType == "scratch" {
		return projectType, "", branch, false, nil
	}
	return projectType, gitURL, branch, gitURL == "", nil
}

func (s *PracticeImportService) planAgents(ctx context.Context, agents []tplAgent) ([]PracticeImportAgent, map[string]string, error) {
	rename := map[string]string{}
	out := make([]PracticeImportAgent, 0, len(agents))
	for _, a := range agents {
		base := strings.TrimSpace(a.Name)
		final, renamed, err := copyName(base, func(name string) (bool, error) {
			return s.agentNameTaken(ctx, name)
		})
		if err != nil {
			return nil, nil, err
		}
		rename[base] = final
		out = append(out, PracticeImportAgent{Name: base, FinalName: final, Renamed: renamed})
	}
	return out, rename, nil
}

// copyName finds a free name by appending 副本 markers; 50 attempts is far
// beyond any human workflow and stops an unbounded loop.
func copyName(base string, taken func(string) (bool, error)) (string, bool, error) {
	base = strings.TrimSpace(base)
	busy, err := taken(base)
	if err != nil {
		return "", false, err
	}
	if !busy {
		return base, false, nil
	}
	for i := 1; i <= 50; i++ {
		candidate := base + "（副本）"
		if i > 1 {
			candidate = fmt.Sprintf("%s（副本 %d）", base, i)
		}
		busy, err := taken(candidate)
		if err != nil {
			return "", false, err
		}
		if !busy {
			return candidate, true, nil
		}
	}
	return "", false, NewConflictError(fmt.Sprintf("同名条目过多，无法为「%s」找到可用的副本名称", base))
}

func (s *PracticeImportService) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var n int
	if err := s.apply.st.DB().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *PracticeImportService) agentNameTaken(ctx context.Context, name string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM agent WHERE name = ?`, name)
}

func (s *PracticeImportService) squadNameTaken(ctx context.Context, name string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM squad WHERE name = ?`, name)
}

func (s *PracticeImportService) domainNameTaken(ctx context.Context, name string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM domain WHERE name = ?`, name)
}

func (s *PracticeImportService) workflowNameTaken(ctx context.Context, squadID, name string) (bool, error) {
	return s.exists(ctx, `SELECT COUNT(1) FROM blueprint WHERE squad_id = ? AND name = ?`, squadID, name)
}

func (s *PracticeImportService) resolveOwnerSquad(ctx context.Context, squadID, ref string) (*Squad, error) {
	ref = strings.TrimSpace(ref)
	if strings.TrimSpace(squadID) != "" {
		ref = strings.TrimSpace(squadID)
	}
	if ref == "" {
		return nil, nil
	}
	sq, err := s.lookupSquadRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	if sq == nil {
		return nil, NewValidationError(fmt.Sprintf("找不到归属小队 %q：请先创建小队，或选择已有小队", ref))
	}
	return sq, nil
}

func (s *PracticeImportService) lookupSquadRef(ctx context.Context, ref string) (*Squad, error) {
	if sq, err := s.apply.squadSvc.Get(ctx, ref); err == nil {
		return sq, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.apply.squadByName(ctx, ref)
}

func practicePlaceholderSquad() *Squad {
	return &Squad{ID: strings.Repeat("0", 32), Name: "（导入时选择）"}
}

// prepareWorkflowGraph rewrites portable assignees to real platform ids and
// validates the graph. With no owner selected it uses a placeholder squad so
// the wizard can still preview node structure and agent references.
func (s *PracticeImportService) prepareWorkflowGraph(ctx context.Context, graph SpecGraph, owner *Squad, finalName string) (string, error) {
	if len(graph.Nodes) > maxSpecNodes {
		return "", NewValidationError(fmt.Sprintf("工作流节点过多：最多 %d 个，包内有 %d 个", maxSpecNodes, len(graph.Nodes)))
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	var copy SpecGraph
	if err := json.Unmarshal(raw, &copy); err != nil {
		return "", err
	}
	if strings.TrimSpace(copy.Name) == "" {
		copy.Name = finalName
	}
	effectiveOwner := owner
	if effectiveOwner == nil {
		effectiveOwner = practicePlaceholderSquad()
	}
	for i := range copy.Nodes {
		n := &copy.Nodes[i]
		var assignee *string
		switch {
		case n.Task != nil:
			assignee = &n.Task.Assignee
		case n.Spawn != nil:
			assignee = &n.Spawn.Assignee
		}
		if assignee == nil || strings.TrimSpace(*assignee) == "" {
			continue
		}
		rewritten, err := s.rewriteAssignee(ctx, *assignee, owner, effectiveOwner)
		if err != nil {
			label := strings.TrimSpace(n.Title)
			if label == "" {
				label = n.ID
			}
			return "", NewValidationError(fmt.Sprintf("节点「%s」：%v", label, err))
		}
		*assignee = rewritten
	}
	if problems := ValidateSpec(&copy); len(problems) > 0 {
		return "", NewValidationError("工作流定义有误：" + strings.Join(problems, "；"))
	}
	out, err := json.Marshal(&copy)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// rewriteAssignee accepts portable references:
//   - agent:<agent name or id>  → agent:<id>
//   - squad:owner              → squad:<selected owner id>
//   - squad:<squad name or id> → same owner only (no cross-squad assignment)
//   - capability:<role>        → unchanged (resolved at run time)
func (s *PracticeImportService) rewriteAssignee(ctx context.Context, assignee string, owner, effectiveOwner *Squad) (string, error) {
	assignee = strings.TrimSpace(assignee)
	parts := strings.SplitN(assignee, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("指派格式应为 agent:<名称>、squad:owner 或 capability:<角色>（收到 %q）", assignee)
	}
	prefix := strings.TrimSpace(parts[0])
	ref := strings.TrimSpace(parts[1])
	switch prefix {
	case "capability":
		if !practiceCapabilityRe.MatchString(ref) {
			return "", fmt.Errorf("capability 名称 %q 只能包含小写字母、数字、下划线、点和连字符", ref)
		}
		return "capability:" + ref, nil
	case "agent":
		id, err := s.resolveAgentRef(ctx, ref)
		if err != nil {
			return "", err
		}
		return "agent:" + id, nil
	case "squad":
		if owner == nil {
			if ref == "owner" {
				return "squad:" + effectiveOwner.ID, nil
			}
			return "squad:" + effectiveOwner.ID, nil
		}
		if ref != "owner" {
			target, err := s.lookupSquadRef(ctx, ref)
			if err != nil {
				return "", err
			}
			if target == nil {
				return "", fmt.Errorf("引用的 Squad「%s」不存在", ref)
			}
			if target.ID != owner.ID {
				return "", fmt.Errorf("不能跨队分配：节点指派给小队「%s」，而本次导入的归属小队是「%s」；工作流只能把工作派给归属小队自己的成员，或用 agent:<名称> 指派给具体 Agent", target.Name, owner.Name)
			}
		}
		return "squad:" + owner.ID, nil
	default:
		return "", fmt.Errorf("不支持的前缀 %q：只支持 agent:<名称>、squad:owner 或 capability:<角色>", prefix)
	}
}

func (s *PracticeImportService) resolveAgentRef(ctx context.Context, ref string) (string, error) {
	if id := s.apply.agentIDByName(ctx, ref); id != "" {
		return id, nil
	}
	var id string
	if err := s.apply.st.DB().QueryRowContext(ctx, `SELECT id FROM agent WHERE id = ?`, ref).Scan(&id); err == nil && id != "" {
		return id, nil
	}
	return "", fmt.Errorf("引用的 Agent「%s」不存在：请先导入或创建该 Agent，再重新导入工作流", ref)
}

// Import validates the package again (preview must never be trusted as a
// write permit) and creates copies of everything it describes.
func (s *PracticeImportService) Import(ctx context.Context, req PracticeImportRequest) (*PracticeImportResult, error) {
	plan, err := s.plan(ctx, req)
	if err != nil {
		return nil, err
	}
	switch plan.preview.Kind {
	case practiceKindSquad:
		return s.importSquad(ctx, plan)
	case practiceKindProject:
		return s.importProject(ctx, plan, req)
	case practiceKindWorkflow:
		return s.importWorkflow(ctx, plan)
	}
	return nil, NewCodedErrorDetail(CodeTemplateInvalid, "不支持的实践包类型", map[string]any{"reason": "kind"})
}

func (s *PracticeImportService) importSquad(ctx context.Context, plan *practicePlan) (*PracticeImportResult, error) {
	spec := plan.squadSpec
	res, err := s.apply.applySquadSpec(ctx, spec, conflictCreate, plan.renameMap, plan.preview.Name)
	out := newPracticeImportResult(plan)
	if res != nil {
		out.Items = append(out.Items, res.Items...)
		out.Agents = practiceAgentRefs(res.Agents)
		if res.Squad != nil {
			out.Squad = practiceEntityRef("squad", res.Squad.ID, res.Squad.Name, "/squads/"+res.Squad.ID)
			dom, derr := s.apply.ensureScratchDomain(ctx, res.Squad.Name)
			if derr != nil {
				out.Warnings = append(out.Warnings, "小队已创建，但默认工作区创建失败："+derr.Error())
				out.Items = append(out.Items, ApplyItem{Kind: "domain", Name: res.Squad.Name, Action: "failed", Error: derr.Error()})
			} else if dom != nil {
				out.Items = append(out.Items, ApplyItem{Kind: "domain", Name: dom.Name, ID: dom.ID, Action: "created"})
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PracticeImportService) importProject(ctx context.Context, plan *practicePlan, req PracticeImportRequest) (*PracticeImportResult, error) {
	spec := plan.projectSpec
	out := newPracticeImportResult(plan)

	if plan.projectType == "repo" {
		if strings.TrimSpace(plan.gitURL) == "" {
			return nil, NewValidationError("repo 类型项目需要 Git 仓库地址：请填写 git_url 或改用 scratch 项目")
		}
		if err := s.apply.probeGit(ctx, plan.gitURL, plan.branch, req.GitCredentials); err != nil {
			return nil, err
		}
	}
	dom, err := s.apply.domainSvc.Create(ctx, Domain{
		Type:           plan.projectType,
		Name:           plan.projectName,
		GitURL:         plan.gitURL,
		DefaultBranch:  plan.branch,
		GitIdentity:    spec.Domain.GitIdentity,
		GitCredentials: req.GitCredentials,
	})
	if err != nil {
		return nil, err
	}
	out.Project = practiceEntityRef("project", dom.ID, dom.Name, "/projects/"+dom.ID)
	out.Items = append(out.Items, ApplyItem{Kind: "domain", Name: dom.Name, ID: dom.ID, Action: "created"})

	var teamRes *ApplySquadResult
	if spec.Team != nil {
		teamRes, err = s.apply.applySquadSpec(ctx, spec.Team, conflictCreate, plan.renameMap, plan.teamSquadName)
		if teamRes != nil {
			out.Items = append(out.Items, teamRes.Items...)
			out.Agents = practiceAgentRefs(teamRes.Agents)
			if teamRes.Squad != nil {
				out.Squad = practiceEntityRef("squad", teamRes.Squad.ID, teamRes.Squad.Name, "/squads/"+teamRes.Squad.ID)
			}
		}
		if err != nil {
			out.Warnings = append(out.Warnings, "项目已创建，但小队没有建全："+err.Error())
			out.Items = append(out.Items, ApplyItem{Kind: "squad", Name: plan.teamSquadName, Action: "failed", Error: err.Error()})
		}
	}

	if ref := strings.TrimSpace(spec.Domain.IssueAssignee); ref != "" {
		typ := strings.TrimSpace(spec.Domain.IssueAssigneeType)
		if typ == "" {
			typ = "agent"
		}
		name := plan.mapAssignee(typ, ref)
		id := s.apply.resolveIssueAssignee(ctx, name, typ)
		if id == "" {
			out.Items = append(out.Items, ApplyItem{Kind: "issue_assignee", Name: name, Action: "failed", Error: "无法解析 Issue 负责人"})
		} else {
			updated, uerr := s.apply.domainSvc.Update(ctx, dom.ID, Domain{
				GitURL:            dom.GitURL,
				DefaultBranch:     dom.DefaultBranch,
				GitIdentity:       dom.GitIdentity,
				GitCredentials:    dom.GitCredentials,
				IssueRepo:         dom.IssueRepo,
				IssueAssignee:     id,
				IssueAssigneeType: typ,
				IssueProvider:     dom.IssueProvider,
			})
			if uerr != nil {
				out.Items = append(out.Items, ApplyItem{Kind: "issue_assignee", Name: name, Action: "failed", Error: uerr.Error()})
			} else {
				dom = updated
				out.Items = append(out.Items, ApplyItem{Kind: "issue_assignee", Name: name, ID: id, Action: "updated"})
			}
		}
	}

	for _, g := range spec.Goals {
		typ := strings.TrimSpace(g.AssigneeType)
		if typ == "" {
			typ = "agent"
		}
		name := plan.mapAssignee(typ, g.Assignee)
		assigneeID := s.apply.resolveAssigneeName(ctx, typ, name, nil)
		status := "backlog"
		if g.Start {
			status = "active"
		}
		goal, gerr := s.apply.goalSvc.Create(ctx, Goal{
			Title:         g.Title,
			Description:   g.Description,
			DomainID:      dom.ID,
			AssigneeType:  typ,
			AssigneeID:    assigneeID,
			Status:        status,
			CreatedByType: "human",
		})
		if gerr != nil {
			out.Items = append(out.Items, ApplyItem{Kind: "goal", Name: g.Title, Action: "failed", Error: gerr.Error()})
			continue
		}
		out.Goals = append(out.Goals, PracticeImportRef{Kind: "goal", ID: goal.ID, Name: goal.Title})
		out.Items = append(out.Items, ApplyItem{Kind: "goal", Name: goal.Title, ID: goal.ID, Action: "created"})
	}

	for _, sc := range spec.Schedules {
		typ := strings.TrimSpace(sc.AssigneeType)
		if typ == "" {
			typ = "agent"
		}
		name := plan.mapAssignee(typ, sc.Assignee)
		assigneeID := s.apply.resolveAssigneeName(ctx, typ, name, nil)
		sched, serr := s.apply.schedSvc.Create(ctx, Schedule{
			Name:           sc.Name,
			TitleTemplate:  sc.Title,
			Description:    sc.Description,
			AssigneeType:   typ,
			AssigneeID:     assigneeID,
			DomainID:       dom.ID,
			CronExpression: sc.Cron,
			Timezone:       localTimezone(),
			Enabled:        true,
		})
		if serr != nil {
			out.Items = append(out.Items, ApplyItem{Kind: "schedule", Name: sc.Name, Action: "failed", Error: serr.Error()})
			continue
		}
		out.Schedules = append(out.Schedules, PracticeImportRef{Kind: "schedule", ID: sched.ID, Name: sched.Name})
		out.Items = append(out.Items, ApplyItem{Kind: "schedule", Name: sched.Name, ID: sched.ID, Action: "created"})
	}

	if failed := countFailedItems(out.Items); failed > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("有 %d 项内容没有创建成功，详见结果列表；其余内容已正常创建。", failed))
	}
	return out, nil
}

func (s *PracticeImportService) importWorkflow(ctx context.Context, plan *practicePlan) (*PracticeImportResult, error) {
	if s.blueprints == nil {
		return nil, errors.New("工作流服务未配置，暂时不能导入工作流包")
	}
	if plan.owner == nil {
		return nil, NewValidationError("工作流包必须指定归属小队：请在导入时选择一支小队")
	}
	out := newPracticeImportResult(plan)
	bp, err := s.blueprints.CreateForSquad(ctx, plan.owner.ID, plan.workflowName, plan.workflowDescription, plan.workflowContent, "human", "")
	if err != nil {
		return nil, err
	}
	out.Workflow = practiceEntityRef("workflow", bp.ID, bp.Name, "/workflows/"+bp.ID)
	out.Items = append(out.Items, ApplyItem{Kind: "workflow", Name: bp.Name, ID: bp.ID, Action: "created"})
	if versions, verr := s.blueprints.ListVersions(ctx, bp.ID); verr == nil && len(versions) > 0 {
		out.Items = append(out.Items, ApplyItem{Kind: "workflow_version", Name: bp.Name, ID: versions[0].ID, Action: "draft"})
	}
	out.Warnings = append(out.Warnings, "工作流已导入为草稿 v1；在工作流页检查并发布后，才能派活执行。")
	return out, nil
}

func newPracticeImportResult(plan *practicePlan) *PracticeImportResult {
	preview := plan.preview
	preview.Warnings = append([]string{}, plan.preview.Warnings...)
	return &PracticeImportResult{
		Preview:  preview,
		Warnings: append([]string{}, plan.preview.Warnings...),
	}
}

func practiceEntityRef(kind, id, name, url string) *PracticeImportRef {
	return &PracticeImportRef{Kind: kind, ID: id, Name: name, URL: url}
}

func practiceAgentRefs(agents []*Agent) []PracticeImportRef {
	out := make([]PracticeImportRef, 0, len(agents))
	for _, a := range agents {
		if a == nil {
			continue
		}
		out = append(out, PracticeImportRef{Kind: "agent", ID: a.ID, Name: a.Name, URL: "/agents/" + a.ID})
	}
	return out
}

func (p *practicePlan) mapAssignee(assigneeType, name string) string {
	if assigneeType == "squad" && p.teamSquadOriginal != "" && name == p.teamSquadOriginal {
		return p.teamSquadName
	}
	return rename(p.renameMap, name)
}

func countFailedItems(items []ApplyItem) int {
	n := 0
	for _, item := range items {
		if strings.EqualFold(item.Action, "failed") || item.Error != "" {
			n++
		}
	}
	return n
}
