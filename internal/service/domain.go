package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// Domain is an asset/evolution domain: a shared repo (or a scratch project
// directory) plus its routing/acceptance configuration. type=repo is the M0
// default; type=scratch is a repo-less project directory.
type Domain struct {
	ID            string `json:"id"`
	Type          string `json:"type"` // repo (M0); others deferred
	Name          string `json:"name"`
	GitURL        string `json:"git_url"`
	DefaultBranch string `json:"default_branch"`
	GitIdentity   string `json:"git_identity"` // "name <email>" for commits
	// GitCredentials is the platform's REMOTE-OPERATION identity (decision
	// 3-5): the token used for issue comments/close AND git push — every
	// remote action appears under this account. Configure a dedicated
	// agentwork-bot account's token (GitHub or GitCode — both platforms
	// render comments under the authenticating account) so the human's own
	// identity stays clean; commits are separately authored by git_identity.
	GitCredentials    string `json:"git_credentials"`
	MaxRunDuration    int    `json:"max_run_duration"`    // seconds per run
	IssueRepo         string `json:"issue_repo"`          // M4-B: "owner/repo" whose open issues auto-become goals ('' = none)
	IssueAssignee     string `json:"issue_assignee"`      // M4-B: agent|squad id handling this repo's issues ('' = don't auto-create)
	IssueAssigneeType string `json:"issue_assignee_type"` // M4-B: agent | squad (default agent)
	IssueProvider     string `json:"issue_provider"`      // M4-B: github | gitcode (default github)
	PullAssignee      string `json:"pull_assignee"`       // M4-B PR pull: agent|squad id handling this repo's pull requests ('' = don't pull PRs)
	PullAssigneeType  string `json:"pull_assignee_type"`  // M4-B PR pull: agent | squad (default agent)
	// AcceptanceLevel is the project-level acceptance mode (验收级别):
	// "" = 待定/未设置 (unset), "hitl" = human-in-the-loop (park completed
	// goals in review), "hootl" = human-out-of-the-loop (auto approve).
	// Repo webhook/poller-created goals inherit it (see issue.Poller).
	AcceptanceLevel string `json:"acceptance_level"`
	// WebhookSecret is the repo-level webhook signing secret. '' = fall back
	// to the platform secret (app_settings platform.webhook_secret).
	WebhookSecret string `json:"webhook_secret"`
	CreatedAt     string `json:"created_at"`
	// ScratchDir is the scratch domain's persistent project root on disk
	// ('' for repo domains) — the UI shows it so the human can find the
	// artifacts.
	ScratchDir string `json:"scratch_dir,omitempty"`
}

type DomainService struct {
	st  *store.Store
	bus *events.Bus
}

func NewDomainService(st *store.Store, bus *events.Bus) *DomainService {
	return &DomainService{st: st, bus: bus}
}

// validateAcceptanceLevel validates the project-level acceptance mode
// (验收级别): "" = 待定/未设置, "hitl" = 人工验收, "hootl" = 自动验收.
// Stored verbatim; an empty level means "unset" — goals created from the
// domain then fall back to the goal service's default (hitl).
func validateAcceptanceLevel(level string) error {
	switch level {
	case "", "hitl", "hootl":
		return nil
	default:
		return NewValidationError(`acceptance_level must be "" (待定/未设置), hitl, or hootl`)
	}
}

func (s *DomainService) Create(ctx context.Context, d Domain) (*Domain, error) {
	if d.Name == "" {
		return nil, NewFieldRequiredError("name")
	}
	if err := validateAcceptanceLevel(d.AcceptanceLevel); err != nil {
		return nil, err
	}
	if d.Type == "" {
		d.Type = "repo"
	}
	switch d.Type {
	case "repo":
		if d.GitURL == "" {
			return nil, NewFieldRequiredError("git_url")
		}
	case "scratch":
		// A scratch domain has NO shared repository — its persistent home is
		// runs/scratch/<name>/ (the human-maintained shared root + per-goal
		// directories). git_url is meaningless here.
	default:
		return nil, NewValidationError("domain type must be repo or scratch")
	}
	if d.DefaultBranch == "" {
		d.DefaultBranch = "main"
	}
	// Issue tracking identity is DERIVED from the repo URL — the human
	// already gave the URL; asking for owner/repo + platform again is
	// double entry. Explicit values (edit-form corrections) win.
	if d.IssueRepo == "" && d.GitURL != "" {
		d.IssueRepo, d.IssueProvider = deriveIssueSource(d.GitURL)
	}
	if d.MaxRunDuration == 0 {
		d.MaxRunDuration = 7200 // 2h (DESIGN.md §4)
	}
	// M4-B: issue tracking needs a repo AND an assignee (git_credentials is
	// independently valid — it also serves commit identity). Reject
	// half-configured issue tracking loudly instead of silently never
	// polling. The assignee may be an agent or a squad (issue_assignee_type).
	if err := s.validateIssueTracking(ctx, &d); err != nil {
		return nil, err
	}
	d.ID = newID()
	d.CreatedAt = now()
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO domain (id,type,name,git_url,default_branch,git_identity,git_credentials,max_run_duration,issue_repo,issue_assignee,issue_assignee_type,issue_provider,pull_assignee,pull_assignee_type,webhook_secret,acceptance_level,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Type, d.Name, d.GitURL, d.DefaultBranch, d.GitIdentity, d.GitCredentials, d.MaxRunDuration, d.IssueRepo, d.IssueAssignee, d.IssueAssigneeType, d.IssueProvider, d.PullAssignee, d.PullAssigneeType, d.WebhookSecret, d.AcceptanceLevel, d.CreatedAt)
	if err != nil {
		if ve := dupNameCodedError(err, CodeDomainNameExists, "domain", d.Name); ve != nil {
			return nil, ve
		}
		return nil, fmt.Errorf("insert domain: %w", err)
	}
	s.bus.Publish(ctx, events.Event{Topic: "domain:created", Payload: d})
	return &d, nil
}

// fillScratchDir computes the on-disk project root for a scratch domain.
func fillScratchDir(d *Domain) {
	if d.Type == "scratch" {
		d.ScratchDir = ScratchDomainRoot(d.Name)
	}
}

func (s *DomainService) List(ctx context.Context) ([]Domain, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,type,name,git_url,default_branch,git_identity,git_credentials,max_run_duration,issue_repo,issue_assignee,issue_assignee_type,issue_provider,pull_assignee,pull_assignee_type,webhook_secret,acceptance_level,created_at
		 FROM domain ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Type, &d.Name, &d.GitURL, &d.DefaultBranch, &d.GitIdentity, &d.GitCredentials, &d.MaxRunDuration, &d.IssueRepo, &d.IssueAssignee, &d.IssueAssigneeType, &d.IssueProvider, &d.PullAssignee, &d.PullAssigneeType, &d.WebhookSecret, &d.AcceptanceLevel, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	for i := range out {
		fillScratchDir(&out[i])
	}
	return out, rows.Err()
}

func (s *DomainService) Get(ctx context.Context, id string) (*Domain, error) {
	var d Domain
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,type,name,git_url,default_branch,git_identity,git_credentials,max_run_duration,issue_repo,issue_assignee,issue_assignee_type,issue_provider,pull_assignee,pull_assignee_type,webhook_secret,acceptance_level,created_at
		 FROM domain WHERE id=?`, id).
		Scan(&d.ID, &d.Type, &d.Name, &d.GitURL, &d.DefaultBranch, &d.GitIdentity, &d.GitCredentials, &d.MaxRunDuration, &d.IssueRepo, &d.IssueAssignee, &d.IssueAssigneeType, &d.IssueProvider, &d.PullAssignee, &d.PullAssigneeType, &d.WebhookSecret, &d.AcceptanceLevel, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	fillScratchDir(&d)
	return &d, nil
}

func (s *DomainService) Delete(ctx context.Context, id string) error {
	// Refuse if goals reference this domain — deleting the domain would
	// silently orphan their worktrees and acceptance policies.
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE domain_id=?`, id).Scan(&n); err != nil {
		return fmt.Errorf("check goals: %w", err)
	}
	if n > 0 {
		// Resolve the name for a human-facing message — the id is opaque to
		// the operator. The query is best-effort (row may be gone in a race);
		// fall back to id so we never fail to report the conflict.
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM domain WHERE id=?`, id).Scan(&name)
		if name == "" {
			name = id
		}
		return NewCodedErrorDetail(CodeDomainHasGoals,
			fmt.Sprintf("domain %q has %d goal(s); delete its goals first", name, n),
			map[string]any{"name": name, "count": n})
	}
	// Refuse if schedules reference this domain — a schedule.domain_id is a
	// required execution precondition (fired goals need the domain's worktree
	// and execution config), so deleting the domain would turn every
	// dependent schedule into a zombie that fails its next firing.
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schedule WHERE domain_id=?`, id).Scan(&n); err != nil {
		return fmt.Errorf("check schedules: %w", err)
	}
	if n > 0 {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM domain WHERE id=?`, id).Scan(&name)
		if name == "" {
			name = id
		}
		return NewCodedErrorDetail(CodeDomainHasSchedules,
			fmt.Sprintf("domain %q has %d schedule(s); delete or reassign them first", name, n),
			map[string]any{"name": name, "count": n})
	}
	// The name travels in the payload: the daemon removes a scratch
	// domain's project root after the row is gone.
	var name string
	_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM domain WHERE id=?`, id).Scan(&name)
	if _, err := s.st.DB().ExecContext(ctx, `DELETE FROM domain WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	s.bus.Publish(ctx, events.Event{Topic: "domain:deleted", Payload: map[string]string{"id": id, "name": name}})
	return nil
}

// validateIssueTracking validates the M4-B issue-tracking configuration
// (shared by Create and Update): a repo AND an assignee must come together,
// the platform token is required, the provider is github|gitcode, and the
// assignee may be an agent or a squad.
// deriveIssueSource infers the issue tracker identity (owner/repo +
// provider) from the repo URL: https://gitcode.com/eushing/test-repo.git →
// ("eushing/test-repo", "gitcode"). Handles https/http and ssh shapes.
func deriveIssueSource(gitURL string) (ownerRepo, provider string) {
	u := strings.TrimSpace(gitURL)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.Index(u, "@"); i >= 0 { // git@host:owner/repo.git
		u = strings.Replace(u[i+1:], ":", "/", 1)
	}
	u = strings.TrimSuffix(u, ".git")
	parts := strings.Split(strings.Trim(u, "/"), "/")
	if len(parts) < 3 {
		return "", ""
	}
	host, owner, repo := parts[0], parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || repo == "" {
		return "", ""
	}
	switch {
	case strings.Contains(host, "gitcode"):
		provider = "gitcode"
	case strings.Contains(host, "github"):
		provider = "github"
	}
	return owner + "/" + repo, provider
}

func (s *DomainService) validateIssueTracking(ctx context.Context, d *Domain) error {
	if d.Type == "scratch" && (d.IssueRepo != "" || d.IssueAssignee != "" || d.PullAssignee != "") {
		return NewValidationError("issue tracking needs a repository — not available on a scratch domain")
	}
	if d.IssueAssignee != "" {
		// The ASSIGNEE is the opt-in switch: issue_repo is derived from the
		// repo URL (or corrected explicitly), so a repo domain always has
		// one — only a missing assignee-side pair is an error.
		if d.IssueRepo == "" {
			return NewValidationError("issue tracking needs an issue_repo (derivable from git_url)")
		}
		if d.GitCredentials == "" {
			return NewValidationError("issue tracking needs git_credentials (the platform token)")
		}
		if d.IssueProvider == "" {
			d.IssueProvider = "github"
		}
		if d.IssueProvider != "github" && d.IssueProvider != "gitcode" {
			return NewValidationError("issue_provider must be github or gitcode")
		}
		if d.IssueAssigneeType == "" {
			d.IssueAssigneeType = "agent"
		}
		switch d.IssueAssigneeType {
		case "agent":
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, d.IssueAssignee, "issue assignee"); err != nil {
				return err
			}
		case "squad":
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, d.IssueAssignee, "issue assignee squad"); err != nil {
				return err
			}
		default:
			return NewValidationError("issue_assignee_type must be agent or squad")
		}
	}
	// M4-B PR pull: mirrors the issue-tracking validation — the PR handler
	// reuses the issue tracking's repo/provider (one repo, one tracking
	// config), only the assignee side is new ('' = don't pull PRs).
	if d.PullAssignee != "" {
		if d.IssueRepo == "" {
			return NewValidationError("PR pulling needs a repository (derivable from git_url)")
		}
		if d.GitCredentials == "" {
			return NewValidationError("PR pulling needs git_credentials (the platform token)")
		}
		if d.IssueProvider == "" {
			d.IssueProvider = "github"
		}
		if d.IssueProvider != "github" && d.IssueProvider != "gitcode" {
			return NewValidationError("issue_provider must be github or gitcode")
		}
		if d.PullAssigneeType == "" {
			d.PullAssigneeType = "agent"
		}
		switch d.PullAssigneeType {
		case "agent":
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, d.PullAssignee, "PR assignee"); err != nil {
				return err
			}
		case "squad":
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, d.PullAssignee, "PR assignee squad"); err != nil {
				return err
			}
		default:
			return NewValidationError("pull_assignee_type must be agent or squad")
		}
	}
	return nil
}

// Update edits a domain's mutable configuration — the issue handler can be
// changed after creation (e.g. a single agent → a squad, the whole point of
// the edit path).
func (s *DomainService) Update(ctx context.Context, id string, d Domain) (*Domain, error) {
	// The edit path never changes type — repo/scratch is fixed at creation.
	// Fetching the old domain both checks existence and yields the DB's real
	// type for validation. The frontend edit form omits type, so the decoded
	// d.Type is "" and the scratch guard in validateIssueTracking would never
	// fire against that empty value — letting a scratch domain silently absorb
	// issue tracking config (a type ↔ capability contradiction). Overlaying the
	// real type makes the guard evaluate against the true domain type.
	old, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Type != "" && d.Type != old.Type {
		return nil, NewValidationError("domain type cannot be changed after creation (repo ↔ scratch is not supported)")
	}
	d.Type = old.Type
	// Name: the edit form may rename a domain. An EMPTY incoming name means
	// "unchanged" (partial Domains built elsewhere never blank the row); a
	// non-empty name must stay unique — the UNIQUE constraint is enforced on
	// UPDATE and surfaces Create's coded duplicate-name error.
	if d.Name == "" {
		d.Name = old.Name
	}
	// Acceptance level round-trips like webhook_secret: an EMPTY incoming
	// value means "unchanged", never "clear" — an unrelated edit must not
	// silently reset the project's 验收级别. Clearing back to 待定 is the
	// explicit SetAcceptanceLevel endpoint's job.
	if d.AcceptanceLevel == "" {
		d.AcceptanceLevel = old.AcceptanceLevel
	}
	if err := validateAcceptanceLevel(d.AcceptanceLevel); err != nil {
		return nil, err
	}
	// Webhook secret round-trips through Update, but an EMPTY incoming value
	// means "unchanged", never "clear": the edit form and the template apply
	// path build partial Domains that do not carry webhook_secret, so treating
	// '' as clear would silently drop a repo-level secret on every unrelated
	// edit (the domain would fall back to the platform secret without anyone
	// asking). Setting a non-empty value works normally.
	if d.WebhookSecret == "" {
		d.WebhookSecret = old.WebhookSecret
	}
	if err := s.validateIssueTracking(ctx, &d); err != nil {
		return nil, err
	}
	_, err = s.st.DB().ExecContext(ctx,
		`UPDATE domain SET name=?, git_url=?, default_branch=?, git_identity=?, git_credentials=?, issue_repo=?, issue_assignee=?, issue_assignee_type=?, issue_provider=?, pull_assignee=?, pull_assignee_type=?, webhook_secret=?, acceptance_level=? WHERE id=?`,
		d.Name, d.GitURL, d.DefaultBranch, d.GitIdentity, d.GitCredentials, d.IssueRepo, d.IssueAssignee, d.IssueAssigneeType, d.IssueProvider, d.PullAssignee, d.PullAssigneeType, d.WebhookSecret, d.AcceptanceLevel, id)
	if err != nil {
		if ve := dupNameCodedError(err, CodeDomainNameExists, "domain", d.Name); ve != nil {
			return nil, ve
		}
		return nil, fmt.Errorf("update domain: %w", err)
	}
	return s.Get(ctx, id)
}

// SetAcceptanceLevel is the explicit endpoint for the project-level acceptance
// mode (验收级别). Unlike Update — where an empty value means "unchanged" —
// this path can clear the level back to "" (待定/未设置); it is the ONLY way
// to unset it. A missing domain reports ErrNotFound (the UPDATE's
// RowsAffected==0). Values are validated before the write.
func (s *DomainService) SetAcceptanceLevel(ctx context.Context, id, level string) (*Domain, error) {
	if err := validateAcceptanceLevel(level); err != nil {
		return nil, err
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE domain SET acceptance_level=? WHERE id=?`, level, id)
	if err != nil {
		return nil, fmt.Errorf("set domain acceptance level: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set domain acceptance level: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

// WebhookDelivery is one row of the inbound-delivery log (webhook_delivery) —
// the ground truth for the setup panel's "did the host actually deliver?".
type WebhookDelivery struct {
	Event      string `json:"event"`
	Action     string `json:"action"`
	Repo       string `json:"repo"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	ReceivedAt string `json:"received_at"`
}

// ListWebhookDeliveries returns a domain's most recent deliveries, newest
// first (the panel shows the last 20). Always a non-nil slice.
func (s *DomainService) ListWebhookDeliveries(ctx context.Context, domainID string, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT event, action, repo, ok, COALESCE(error,''), received_at
		   FROM webhook_delivery WHERE domain_id=?
		  ORDER BY received_at DESC LIMIT ?`, domainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	defer rows.Close()
	out := []WebhookDelivery{}
	for rows.Next() {
		var d WebhookDelivery
		var ok int
		if err := rows.Scan(&d.Event, &d.Action, &d.Repo, &ok, &d.Error, &d.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan webhook delivery: %w", err)
		}
		d.OK = ok != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetAssignees edits ONLY the four routing fields (issue/PR assignee + type)
// and deliberately skips the git-connectivity gate that updateDomain enforces:
// the squad page rebinds repos directly, and a temporarily unreachable repo
// must not block that. The assignee existence/shape validation still runs
// (validateIssueTracking: agent/squad must exist, type must be agent|squad).
func (s *DomainService) SetAssignees(ctx context.Context, id, issueAssignee, issueAssigneeType, pullAssignee, pullAssigneeType string) (*Domain, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Connections own issue/PR routing for this repo: the poller skips any
	// domain that has one, and legacy webhook dispatch no-ops. Reject a
	// non-empty legacy assignee instead of silently storing a value that
	// nothing will ever act on.
	var connCount int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM connection WHERE domain_id=?`, id).Scan(&connCount); err != nil {
		return nil, fmt.Errorf("count domain connections: %w", err)
	}
	if connCount > 0 && (issueAssignee != "" || pullAssignee != "") {
		return nil, NewValidationError("该仓库已有小队连接接管 issue/PR 路由；请到小队详情页调整连接，或先删除连接再使用 domain 处理方")
	}
	d.IssueAssignee = issueAssignee
	d.IssueAssigneeType = issueAssigneeType
	d.PullAssignee = pullAssignee
	d.PullAssigneeType = pullAssigneeType
	if d.IssueAssigneeType == "" {
		d.IssueAssigneeType = "agent"
	}
	if d.PullAssigneeType == "" {
		d.PullAssigneeType = "agent"
	}
	// Types are checked even when the assignee is empty: a bogus type with no
	// assignee must still be a 400, not a silently stored value.
	if d.IssueAssigneeType != "agent" && d.IssueAssigneeType != "squad" {
		return nil, NewValidationError("issue_assignee_type must be agent or squad")
	}
	if d.PullAssigneeType != "agent" && d.PullAssigneeType != "squad" {
		return nil, NewValidationError("pull_assignee_type must be agent or squad")
	}
	if err := s.validateIssueTracking(ctx, d); err != nil {
		return nil, err
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE domain SET issue_assignee=?, issue_assignee_type=?, pull_assignee=?, pull_assignee_type=? WHERE id=?`,
		d.IssueAssignee, d.IssueAssigneeType, d.PullAssignee, d.PullAssigneeType, id); err != nil {
		return nil, fmt.Errorf("set domain assignees: %w", err)
	}
	return s.Get(ctx, id)
}

// SetWebhookSecret sets or clears the repo-level webhook secret. Values are
// only trimmed; an empty (or whitespace-only) secret clears it so verification
// falls back to the platform-level platform.webhook_secret. A missing domain
// reports ErrNotFound.
func (s *DomainService) SetWebhookSecret(ctx context.Context, id, secret string) (*Domain, error) {
	secret = strings.TrimSpace(secret)
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE domain SET webhook_secret=? WHERE id=?`, secret, id)
	if err != nil {
		return nil, fmt.Errorf("set webhook secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set webhook secret: %w", err)
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}
