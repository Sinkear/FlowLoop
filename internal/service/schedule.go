package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// Schedule is a cron-triggered goal template. Each firing clones a fresh goal
// from TitleTemplate/Description, assigns it to AssigneeID, and enqueues a
// run. assignee may be an agent or a squad. v2 (M1): DomainID is carried into
// every fired goal — unattended runs need the domain's worktree and execution
// config, exactly like human-created goals.
type Schedule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TitleTemplate string `json:"title_template"`
	Description   string `json:"description"`
	AssigneeType  string `json:"assignee_type"` // agent | squad
	AssigneeID    string `json:"assignee_id"`
	DomainID      string `json:"domain_id"` // v2: fired goals belong to this domain
	// Category is the fired goals' human-in-the-loop mode (v3): "hitl"
	// (default — checkpoint gates park completed runs in review) | "hootl"
	// (unattended completion, gates skipped). Every firing clones it onto the
	// goal it creates.
	Category       string `json:"category"`
	CronExpression string `json:"cron_expression"`
	Timezone       string `json:"timezone"`
	Enabled        bool   `json:"enabled"`
	// Phase C (WEB-IA-REDESIGN D3): the workflow version pinned when the
	// schedule is SAVED — never re-resolved at fire time, so consecutive runs
	// share one topology and their reviews stay comparable across versions.
	// '' = the fired goal auto-decomposes (no workflow). A non-empty pin must
	// be a published version owned by the schedule's squad; a single-agent
	// schedule cannot pin a workflow at all (D5).
	BlueprintVersionID string `json:"blueprint_version_id"`
	// ExecutionMode mirrors goal.execution_mode on the fired goal:
	// "infer" (default — no review) | "train" (review on). The web form renders
	// it as the "执行后迭代工作流" switch; it is NOT a separate concept.
	ExecutionMode  string  `json:"execution_mode"`
	ContentCapture string  `json:"content_capture"` // none|redacted|full
	SampleRate     float64 `json:"sample_rate"`
	BudgetUSD      float64 `json:"budget_usd"`
	// Read-only projections of the pin (joined at list/get): the workflow id,
	// its display name and the pinned version NUMBER, so the schedules list can
	// label "工作流 W · v3" and the edit dialog can prefill the picker without a
	// second round trip. All empty/0 when the schedule auto-decomposes, or when
	// the pinned version was deleted (history must still render).
	BlueprintID      string `json:"blueprint_id"`
	BlueprintName    string `json:"blueprint_name"`
	BlueprintVersion int    `json:"blueprint_version"`
	// BuiltIn marks the system-seeded schedule (每日AI知识精选): the API
	// guards reject delete and identity edits on it, but its execution time
	// (cron_expression + timezone) stays user-editable. Computed per read
	// from the app_settings marker — not a column (no migration tooling).
	BuiltIn   bool   `json:"built_in"`
	NextRunAt string `json:"next_run_at"`
	LastRunAt string `json:"last_run_at"`
	CreatedAt string `json:"created_at"`
}

type ScheduleService struct {
	st  *store.Store
	bus *events.Bus
}

func NewScheduleService(st *store.Store, bus *events.Bus) *ScheduleService {
	return &ScheduleService{st: st, bus: bus}
}

// Create validates the cron expression, computes the first next_run_at, and
// inserts the schedule.
// validateSchedule runs the shared field checks (Create and Update both use
// it): required fields, assignee_type + existence, domain existence, cron
// legality. It normalizes defaults (assignee_type, timezone) and recomputes
// next_run_at into sch — the caller persists the result. Returns ErrNotFound
// only indirectly via mustExist for assignee/domain; the schedule's own
// existence is checked by Update's caller path.
func (s *ScheduleService) validateSchedule(ctx context.Context, sch *Schedule) error {
	if sch.Name == "" {
		return NewFieldRequiredError("name")
	}
	if sch.TitleTemplate == "" {
		return NewFieldRequiredError("title_template")
	}
	if sch.AssigneeID == "" {
		return NewFieldRequiredError("assignee_id")
	}
	if sch.AssigneeType == "" {
		sch.AssigneeType = "agent"
	}
	switch sch.AssigneeType {
	case "agent":
		if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, sch.AssigneeID, "assignee agent"); err != nil {
			return err
		}
	case "squad":
		if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, sch.AssigneeID, "assignee squad"); err != nil {
			return err
		}
	default:
		return NewValidationError("assignee_type must be agent or squad")
	}
	// v3 HITL/HOOTL: each fired goal inherits this mode. Empty means the
	// default human-in-the-loop; anything else is a client bug.
	switch sch.Category {
	case "":
		sch.Category = "hitl"
	case "hitl", "hootl":
	default:
		return NewValidationError("category must be hitl or hootl")
	}
	// Phase C (WEB-IA-REDESIGN D3/D5): the workflow pin is validated ONCE, when
	// the schedule is saved — the daemon copies it onto every fired goal
	// verbatim, so an unpublishable pin must be rejected here. '' means
	// auto-decomposition and is always allowed. A pin with an owner squad must
	// match the schedule's squad; a single-agent schedule can never pin an
	// owned workflow (a scheduled run must not borrow another crew's roster).
	if sch.BlueprintVersionID != "" {
		var status, ownerSquad string
		err := s.st.DB().QueryRowContext(ctx,
			`SELECT bv.status, b.squad_id FROM blueprint_version bv
			 JOIN blueprint b ON b.id=bv.blueprint_id WHERE bv.id=?`,
			sch.BlueprintVersionID).Scan(&status, &ownerSquad)
		if errors.Is(err, sql.ErrNoRows) {
			return NewValidationError(fmt.Sprintf("blueprint_version_id %q not found", sch.BlueprintVersionID))
		}
		if err != nil {
			return fmt.Errorf("check workflow version: %w", err)
		}
		if status != "published" {
			return NewValidationError(fmt.Sprintf("blueprint_version_id %q is %s — only a published version can be pinned", sch.BlueprintVersionID, status))
		}
		if sch.AssigneeType != "squad" {
			return NewValidationError("single-agent schedules cannot pin a workflow — assign the schedule to a squad")
		}
		if ownerSquad != "" && sch.AssigneeID != ownerSquad {
			return NewValidationError("workflow belongs to a different squad — pick one of this squad's workflows or use auto-decomposition")
		}
	}
	// The analysis axis mirrors goal validation exactly (same accepted values,
	// same defaults), so a schedule-created goal is indistinguishable from a
	// hand-created one downstream.
	switch sch.ExecutionMode {
	case "":
		sch.ExecutionMode = "infer"
	case "infer", "train":
	default:
		return NewValidationError("execution_mode must be infer or train")
	}
	switch sch.ContentCapture {
	case "":
		sch.ContentCapture = "redacted"
	case "none", "redacted", "full":
	default:
		return NewValidationError("content_capture must be none, redacted, or full")
	}
	if sch.BudgetUSD <= 0 {
		sch.BudgetUSD = 2
	}
	if sch.BudgetUSD > 100 {
		return NewValidationError("budget_usd must be <= 100")
	}
	if sch.SampleRate <= 0 {
		sch.SampleRate = 1
	}
	if sch.SampleRate > 1 {
		return NewValidationError("sample_rate must be in (0,1]")
	}
	// v2 (M1): unattended goals need a domain (worktree + execution config +
	// deliver). Require it for agent/squad assignees.
	if sch.DomainID == "" {
		return NewFieldRequiredError("domain_id")
	}
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM domain WHERE id=?`, sch.DomainID, "domain"); err != nil {
		return err
	}
	if sch.CronExpression == "" {
		return NewFieldRequiredError("cron_expression")
	}
	if sch.Timezone == "" {
		sch.Timezone = "UTC"
	}
	if err := ValidateCron(sch.CronExpression, sch.Timezone); err != nil {
		return err
	}
	next, err := ComputeNextRun(sch.CronExpression, sch.Timezone, time.Now())
	if err != nil {
		return err
	}
	sch.NextRunAt = next.Format(time.RFC3339Nano)
	return nil
}

func (s *ScheduleService) Create(ctx context.Context, sch Schedule) (*Schedule, error) {
	if err := s.validateSchedule(ctx, &sch); err != nil {
		return nil, err
	}
	sch.ID = newID()
	sch.CreatedAt = now()
	sch.Enabled = true

	enabled := 0
	if sch.Enabled {
		enabled = 1
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO schedule (id,name,title_template,description,assignee_type,assignee_id,domain_id,category,blueprint_version_id,execution_mode,content_capture,sample_rate,budget_usd,cron_expression,timezone,enabled,next_run_at,last_run_at,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'',?)`,
		sch.ID, sch.Name, sch.TitleTemplate, sch.Description, sch.AssigneeType, sch.AssigneeID, sch.DomainID, sch.Category, sch.BlueprintVersionID, sch.ExecutionMode, sch.ContentCapture, sch.SampleRate, sch.BudgetUSD, sch.CronExpression, sch.Timezone, enabled, sch.NextRunAt, sch.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert schedule: %w", err)
	}

	s.projectPin(ctx, &sch)
	s.bus.Publish(ctx, events.Event{Topic: "schedule:created", Payload: sch})
	return &sch, nil
}

// projectPin fills the read-only workflow projections (pin → blueprint id,
// display name, pinned version NUMBER) on a row that was just written: the
// create/update responses must already carry the label the list will show, so
// the web form can confirm what it saved without a second request. A pin whose
// version was deleted after the write resolves to ”, ”, 0 (same as unpinned).
func (s *ScheduleService) projectPin(ctx context.Context, sch *Schedule) {
	sch.BlueprintID, sch.BlueprintName, sch.BlueprintVersion = "", "", 0
	if sch.BlueprintVersionID == "" {
		return
	}
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT b.id,b.name,bv.version FROM blueprint_version bv
		 JOIN blueprint b ON b.id=bv.blueprint_id WHERE bv.id=?`,
		sch.BlueprintVersionID).Scan(&sch.BlueprintID, &sch.BlueprintName, &sch.BlueprintVersion)
}

// Update applies partial edits to a schedule's mutable fields (name,
// title_template, description, assignee, domain, cron, timezone). enabled,
// last_run_at, created_at, and the id are preserved. Changing cron/timezone
// recomputes next_run_at; changing assignee/domain only affects FUTURE
// firings — already-derived goals keep their original assignment. The
// caller-supplied sch carries the new field values; fields left zero
// (empty string) are still subject to the required-field checks, so the
// caller must send the full intended value set, not a partial patch.
func (s *ScheduleService) Update(ctx context.Context, id string, sch Schedule) (*Schedule, error) {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err // ErrNotFound propagates
	}
	// Built-in guard: the seeded digest schedule's identity (name, prompt,
	// assignee, domain) is fixed, but the execution time stays editable —
	// the one field users legitimately tune (e.g. 每天9点 → 每晚11点). Empty
	// identity fields carry forward (a partial body with just
	// cron_expression edits the time); a NON-empty field that differs from
	// the row is an identity edit → rejected. Timezone carries forward too:
	// the seeded row is Asia/Shanghai and validate's UTC default must not
	// silently move the 9点 firing.
	if s.builtInMarkerID(ctx) == id {
		if sch.Name == "" {
			sch.Name = existing.Name
		}
		if sch.TitleTemplate == "" {
			sch.TitleTemplate = existing.TitleTemplate
		}
		if sch.Description == "" {
			sch.Description = existing.Description
		}
		if sch.AssigneeType == "" {
			sch.AssigneeType = existing.AssigneeType
		}
		if sch.AssigneeID == "" {
			sch.AssigneeID = existing.AssigneeID
		}
		if sch.DomainID == "" {
			sch.DomainID = existing.DomainID
		}
		if sch.Timezone == "" {
			sch.Timezone = existing.Timezone
		}
		// Category is not identity, but a time-only edit must not silently
		// flip the built-in schedule's mode: empty carries forward (same rule
		// as timezone), a non-empty value is an explicit mode change.
		if sch.Category == "" {
			sch.Category = existing.Category
		}
		// Phase C fields are not identity either: a time-only edit must not
		// silently drop the built-in schedule's workflow pin or review mode.
		if sch.BlueprintVersionID == "" {
			sch.BlueprintVersionID = existing.BlueprintVersionID
		}
		if sch.ExecutionMode == "" {
			sch.ExecutionMode = existing.ExecutionMode
		}
		if sch.ContentCapture == "" {
			sch.ContentCapture = existing.ContentCapture
		}
		if sch.SampleRate == 0 {
			sch.SampleRate = existing.SampleRate
		}
		if sch.BudgetUSD == 0 {
			sch.BudgetUSD = existing.BudgetUSD
		}
		if sch.Name != existing.Name || sch.TitleTemplate != existing.TitleTemplate ||
			sch.Description != existing.Description ||
			sch.AssigneeType != existing.AssigneeType || sch.AssigneeID != existing.AssigneeID ||
			sch.DomainID != existing.DomainID {
			return nil, NewCodedError(CodeScheduleBuiltIn, "内置自动化任务仅可修改执行时间")
		}
	}
	// Carry the immutable fields forward from the existing row.
	sch.ID = existing.ID
	sch.CreatedAt = existing.CreatedAt
	sch.Enabled = existing.Enabled
	sch.LastRunAt = existing.LastRunAt
	if err := s.validateSchedule(ctx, &sch); err != nil {
		return nil, err
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE schedule SET name=?,title_template=?,description=?,assignee_type=?,assignee_id=?,domain_id=?,category=?,blueprint_version_id=?,execution_mode=?,content_capture=?,sample_rate=?,budget_usd=?,cron_expression=?,timezone=?,next_run_at=? WHERE id=?`,
		sch.Name, sch.TitleTemplate, sch.Description, sch.AssigneeType, sch.AssigneeID, sch.DomainID, sch.Category, sch.BlueprintVersionID, sch.ExecutionMode, sch.ContentCapture, sch.SampleRate, sch.BudgetUSD, sch.CronExpression, sch.Timezone, sch.NextRunAt, sch.ID); err != nil {
		return nil, fmt.Errorf("update schedule: %w", err)
	}
	s.projectPin(ctx, &sch)
	s.bus.Publish(ctx, events.Event{Topic: "schedule:updated", Payload: sch})
	return &sch, nil
}

// scheduleSelect is the canonical read shape: the schedule's own columns plus
// the read-only projection of its workflow pin — the pinned blueprint's id and
// name, and the version NUMBER (not the version row id). LEFT JOINs on
// purpose: an unpinned schedule (” = auto-decompose) and a schedule whose
// pinned version was deleted after a publish cycle must both still list.
const scheduleSelect = `SELECT s.id,s.name,s.title_template,s.description,s.assignee_type,s.assignee_id,s.domain_id,s.category,
	s.blueprint_version_id,s.execution_mode,s.content_capture,s.sample_rate,s.budget_usd,
	s.cron_expression,s.timezone,s.enabled,s.next_run_at,s.last_run_at,s.created_at,
	COALESCE(b.id,''),COALESCE(b.name,''),COALESCE(bv.version,0)
	FROM schedule s
	LEFT JOIN blueprint_version bv ON bv.id = s.blueprint_version_id
	LEFT JOIN blueprint b ON b.id = bv.blueprint_id`

// scanSchedule reads one scheduleSelect row. Shared by List and Get so the
// column order can never drift between the list and the detail response.
func scanSchedule(row interface{ Scan(dest ...any) error }) (Schedule, error) {
	var sch Schedule
	var enabled int
	if err := row.Scan(&sch.ID, &sch.Name, &sch.TitleTemplate, &sch.Description, &sch.AssigneeType, &sch.AssigneeID, &sch.DomainID, &sch.Category,
		&sch.BlueprintVersionID, &sch.ExecutionMode, &sch.ContentCapture, &sch.SampleRate, &sch.BudgetUSD,
		&sch.CronExpression, &sch.Timezone, &enabled, &sch.NextRunAt, &sch.LastRunAt, &sch.CreatedAt,
		&sch.BlueprintID, &sch.BlueprintName, &sch.BlueprintVersion); err != nil {
		return sch, err
	}
	sch.Enabled = enabled != 0
	return sch, nil
}

func (s *ScheduleService) List(ctx context.Context) ([]Schedule, error) {
	// The marker read MUST come before the main query: the returned rows
	// hold their pooled connection until Close, and an in-memory test store
	// pools exactly one connection — a second query inside the open rows
	// deadlocks. The marker is set at daemon startup (static for the call).
	marker := s.builtInMarkerID(ctx)
	rows, err := s.st.DB().QueryContext(ctx, scheduleSelect+` ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// BuiltIn is per-row computed from the marker read above.
	out := []Schedule{}
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		sch.BuiltIn = marker != "" && sch.ID == marker
		out = append(out, sch)
	}
	return out, rows.Err()
}

func (s *ScheduleService) Get(ctx context.Context, id string) (*Schedule, error) {
	sch, err := scanSchedule(s.st.DB().QueryRowContext(ctx, scheduleSelect+` WHERE s.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sch.BuiltIn = s.builtInMarkerID(ctx) == sch.ID
	return &sch, nil
}

// ScheduleRun is one firing of a schedule — the fired goal's identity and
// current status (the web schedule detail's firing history).
type ScheduleRun struct {
	ID         string `json:"id"`
	ScheduleID string `json:"schedule_id"`
	GoalID     string `json:"goal_id"`
	GoalTitle  string `json:"goal_title"`
	GoalStatus string `json:"goal_status"`
	PlannedAt  string `json:"planned_at"`
	Status     string `json:"status"` // dispatched|failed
	CreatedAt  string `json:"created_at"`
}

// ListRuns returns a schedule's firing history (newest first) with each
// firing's goal title/status joined in.
func (s *ScheduleService) ListRuns(ctx context.Context, scheduleID string) ([]ScheduleRun, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT sr.id, sr.schedule_id, sr.goal_id, COALESCE(g.title,''), COALESCE(g.status,''), sr.planned_at, sr.status, sr.created_at
		 FROM schedule_run sr LEFT JOIN goal g ON g.id = sr.goal_id
		 WHERE sr.schedule_id=? ORDER BY sr.created_at DESC`, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("list schedule runs: %w", err)
	}
	defer rows.Close()
	out := []ScheduleRun{}
	for rows.Next() {
		var r ScheduleRun
		if err := rows.Scan(&r.ID, &r.ScheduleID, &r.GoalID, &r.GoalTitle, &r.GoalStatus, &r.PlannedAt, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEnabled toggles a schedule without deleting it (the IM intake
// "停掉定时任务" flow — the schedule row and its firing history stay, the
// daemon's dispatchSchedules only fires enabled=1 rows).
func (s *ScheduleService) SetEnabled(ctx context.Context, id string, enabled bool) (*Schedule, error) {
	v := 0
	if enabled {
		v = 1
	}
	res, err := s.st.DB().ExecContext(ctx, `UPDATE schedule SET enabled=? WHERE id=?`, v, id)
	if err != nil {
		return nil, fmt.Errorf("set schedule enabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

// FireNow stamps a schedule's next_run_at to the current instant so the
// daemon's schedule tick fires it within seconds. This is a FIRST-RUN
// convenience ("创建即先跑一次"), not a general reschedule: the daemon's
// fireSchedule re-derives next_run_at from the cron after firing, so the
// normal cadence is untouched. The dispatchSchedules miss-grace (1 min)
// covers a daemon down between this stamp and its restart; the
// uq_schedule_run_planned unique index dedupes a stamp that raced a firing.
func (s *ScheduleService) FireNow(ctx context.Context, id string) error {
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE schedule SET next_run_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("fire-now schedule %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *ScheduleService) Delete(ctx context.Context, id string) error {
	// Built-in guard: the seeded digest schedule is not user-deletable.
	if s.builtInMarkerID(ctx) == id {
		return NewCodedError(CodeScheduleBuiltIn, "内置自动化任务不可删除")
	}
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Drop run history first (FK), then the schedule. Same effect as ON DELETE
	// CASCADE; we keep the FK non-cascading to match the rest of the schema.
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_run WHERE schedule_id=?`, id); err != nil {
		return fmt.Errorf("delete schedule_run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return tx.Commit()
}

// builtInMarkerID returns the seeded built-in schedule's id (” = none).
// The marker lives in app_settings (key builtin.digest.schedule_id, written
// by SeedDigestSchedule) — bare SQL here, matching the daemon's settings
// reads. Update/Delete consult it for the built-in guard; List/Get for the
// computed built_in flag.
func (s *ScheduleService) builtInMarkerID(ctx context.Context) string {
	return digestMarkerValue(ctx, s.st, digestKeySchedule)
}
