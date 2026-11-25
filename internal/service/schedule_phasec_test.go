package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/store"
)

// Phase C tests: a schedule can pin one published workflow version and carry
// the analysis axis, so a scheduled run stops being the "no workflow, no
// review" special case. The pin is validated at SAVE time (the daemon copies
// it verbatim onto fired goals) — these tests pin that contract.

func phaseCScheduleFixture(t *testing.T) (st *store.Store, bs *BlueprintService, sqA, sqB, leaderA, dom, vA, vDraft string) {
	t.Helper()
	_, _, bs, st = bpCluster(t)
	ctx := context.Background()
	dom = seedDomain(t, st)
	leaderA = seedAgent(t, st, "sched-a-"+newID()[:6])
	leaderB := seedAgent(t, st, "sched-b-"+newID()[:6])
	sqA = insertTestSquad(t, st, leaderA, "")
	sqB = insertTestSquad(t, st, leaderB, "")

	content := strings.ReplaceAll(plainTaskEndJSON, "agent:WORKER", "agent:"+leaderA)
	bp, err := bs.CreateForSquad(ctx, sqA, "sched-flow-"+newID()[:6], "", content, "human", "")
	if err != nil {
		t.Fatalf("create squad workflow: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %v", err)
	}
	published, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// A draft version of the same squad's workflow, for the "only published"
	// refusal (drafts are the editor's working copy, never a runnable pin).
	draft, err := bs.NewDraft(ctx, bp.ID, "tweak")
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	return st, bs, sqA, sqB, leaderA, dom, published.ID, draft.ID
}

func TestScheduleWorkflowPinPersistsAndProjects(t *testing.T) {
	ctx := context.Background()
	st, _, sqA, _, _, dom, versionID, _ := phaseCScheduleFixture(t)
	svc := NewScheduleService(st, events.NewBus())

	sch, err := svc.Create(ctx, Schedule{
		Name: "weekly", TitleTemplate: "weekly {{date}}", Description: "d",
		AssigneeType: "squad", AssigneeID: sqA, DomainID: dom,
		CronExpression: "0 9 * * 1", Timezone: "UTC", Category: "hitl",
		BlueprintVersionID: versionID,
		ExecutionMode:      "train",
		ContentCapture:     "full",
		SampleRate:         0.5,
		BudgetUSD:          7,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	// The projection is filled on the way out (Create projects too), so a
	// client can render the row without a second round-trip.
	if sch.BlueprintName == "" || sch.BlueprintVersion == 0 || sch.BlueprintID == "" {
		t.Fatalf("created projection empty: id=%q name=%q v=%d", sch.BlueprintID, sch.BlueprintName, sch.BlueprintVersion)
	}

	got, err := svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BlueprintVersionID != versionID || got.ExecutionMode != "train" ||
		got.ContentCapture != "full" || got.SampleRate != 0.5 || got.BudgetUSD != 7 {
		t.Fatalf("Phase C fields not persisted: %+v", got)
	}
	if got.BlueprintName == "" || got.BlueprintVersion != 1 {
		t.Fatalf("pin projection: name=%q v=%d", got.BlueprintName, got.BlueprintVersion)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].BlueprintVersionID != versionID || list[0].BlueprintName == "" {
		t.Fatalf("list pin mismatch: %+v", list)
	}

	// Raw column check: the daemon reads the row, not the struct.
	var pin, mode, capture string
	var rate, budget float64
	if err := st.DB().QueryRowContext(ctx,
		`SELECT blueprint_version_id, execution_mode, content_capture, sample_rate, budget_usd FROM schedule WHERE id=?`,
		sch.ID).Scan(&pin, &mode, &capture, &rate, &budget); err != nil {
		t.Fatalf("raw columns: %v", err)
	}
	if pin != versionID || mode != "train" || capture != "full" || rate != 0.5 || budget != 7 {
		t.Fatalf("raw Phase C columns = %q/%q/%q/%v/%v", pin, mode, capture, rate, budget)
	}
}

func TestSchedulePhaseCDefaultsNormalize(t *testing.T) {
	ctx := context.Background()
	st, _, sqA, _, _, dom, _, _ := phaseCScheduleFixture(t)
	svc := NewScheduleService(st, events.NewBus())

	// An old client that knows none of the Phase C fields must still produce a
	// readable row: infer / redacted / 1 / 2 (the same defaults GoalService uses).
	sch, err := svc.Create(ctx, Schedule{
		Name: "legacy-shape", TitleTemplate: "t", AssigneeType: "squad", AssigneeID: sqA,
		DomainID: dom, CronExpression: "0 9 * * *", Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BlueprintVersionID != "" || got.ExecutionMode != "infer" ||
		got.ContentCapture != "redacted" || got.SampleRate != 1 || got.BudgetUSD != 2 {
		t.Fatalf("defaults = pin=%q mode=%q capture=%q rate=%v budget=%v",
			got.BlueprintVersionID, got.ExecutionMode, got.ContentCapture, got.SampleRate, got.BudgetUSD)
	}
	if got.BlueprintName != "" || got.BlueprintVersion != 0 {
		t.Fatalf("auto-decompose must not project a workflow: %+v", got)
	}
}

func TestSchedulePinRefusalsAreRealErrors(t *testing.T) {
	ctx := context.Background()
	st, _, sqA, sqB, leaderA, dom, versionID, draftID := phaseCScheduleFixture(t)
	svc := NewScheduleService(st, events.NewBus())
	base := Schedule{
		Name: "n", TitleTemplate: "t", DomainID: dom,
		CronExpression: "0 9 * * *", Timezone: "UTC",
	}

	cases := []struct {
		name string
		sch  Schedule
		want string
	}{
		{
			name: "unknown version",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "squad", sqA
				s.BlueprintVersionID = "nope"
				return s
			}(),
			want: "not found",
		},
		{
			name: "draft version",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "squad", sqA
				s.BlueprintVersionID = draftID
				return s
			}(),
			want: "only a published version can be pinned",
		},
		{
			name: "foreign squad",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "squad", sqB
				s.BlueprintVersionID = versionID
				return s
			}(),
			want: "workflow belongs to a different squad",
		},
		{
			name: "agent assignee cannot pin",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "agent", leaderA
				s.BlueprintVersionID = versionID
				return s
			}(),
			want: "single-agent schedules cannot pin a workflow",
		},
		{
			name: "budget too high",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "squad", sqA
				s.BudgetUSD = 500
				return s
			}(),
			want: "budget_usd must be <= 100",
		},
		{
			name: "bad mode",
			sch: func() Schedule {
				s := base
				s.AssigneeType, s.AssigneeID = "squad", sqA
				s.ExecutionMode = "analysis"
				return s
			}(),
			want: "execution_mode must be infer or train",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, tc.sch)
			if err == nil {
				t.Fatalf("create succeeded, want error containing %q", tc.want)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("error %q is not ErrValidation", err.Error())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestScheduleUpdateCarriesAndClearsPin(t *testing.T) {
	ctx := context.Background()
	st, _, sqA, _, _, dom, versionID, _ := phaseCScheduleFixture(t)
	svc := NewScheduleService(st, events.NewBus())

	sch, err := svc.Create(ctx, Schedule{
		Name: "auto", TitleTemplate: "t", AssigneeType: "squad", AssigneeID: sqA,
		DomainID: dom, CronExpression: "0 9 * * *", Timezone: "UTC", Category: "hitl",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sch.BlueprintVersionID = versionID
	sch.ExecutionMode = "train"
	sch.BudgetUSD = 3
	if _, err := svc.Update(ctx, sch.ID, *sch); err != nil {
		t.Fatalf("update to pinned: %v", err)
	}
	got, err := svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BlueprintVersionID != versionID || got.ExecutionMode != "train" || got.BlueprintName == "" {
		t.Fatalf("update did not carry pin: %+v", got)
	}

	// Clearing the pin is explicit ('' = auto-decompose) and must not leave a
	// stale projection behind.
	got.BlueprintVersionID = ""
	got.ExecutionMode = "infer"
	if _, err := svc.Update(ctx, got.ID, *got); err != nil {
		t.Fatalf("update to auto: %v", err)
	}
	back, err := svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if back.BlueprintVersionID != "" || back.BlueprintID != "" || back.BlueprintName != "" {
		t.Fatalf("cleared pin still projects: %+v", back)
	}
}

// --- review ownership snapshot + manual proposal -------------------------

// prepReviewSession wires a session + frozen report for an existing goal so the
// manual-proposal path can be exercised without running a real analysis.
func prepReviewSession(t *testing.T, st *store.Store, rsi *RSIService, goalID string, g *WorkflowGraph) (sessionID string) {
	t.Helper()
	ctx := context.Background()
	// Activating a train-mode goal normally creates the session; make it
	// explicit so the fixture doesn't depend on activation timing.
	var mode string
	if err := st.DB().QueryRowContext(ctx, `SELECT execution_mode FROM goal WHERE id=?`, goalID).Scan(&mode); err != nil {
		t.Fatalf("goal mode: %v", err)
	}
	if err := rsi.EnsureSession(ctx, &Goal{ID: goalID, ExecutionMode: "train"}); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	err := st.DB().QueryRowContext(ctx, `SELECT id FROM training_session WHERE goal_id=?`, goalID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no session for goal %s", goalID)
	}
	if err != nil {
		t.Fatalf("session id: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='ready', finding_count=1 WHERE id=?`, sessionID); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	wf, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal graph: %v", err)
	}
	reportID := newID()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO rsi_report (id,session_id,status,summary,metrics_json,baseline_json,workflow_json,narrative,created_at)
		 VALUES (?,?,'ready','','{}','{}',?,'',?)`,
		reportID, sessionID, string(wf), now()); err != nil {
		t.Fatalf("insert report: %v", err)
	}
	return sessionID
}

func insertFinding(t *testing.T, st *store.Store, sessionID, category string) string {
	t.Helper()
	var reportID string
	if err := st.DB().QueryRowContext(context.Background(), `SELECT id FROM rsi_report WHERE session_id=?`, sessionID).Scan(&reportID); err != nil {
		t.Fatalf("report: %v", err)
	}
	id := newID()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at)
		 VALUES (?,?,?,'warn','L2','finding under test','','','[]',1.0,'open',?)`,
		id, reportID, category, now()); err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	return id
}

func TestEnsureSessionSnapshotsWorkflowOwnership(t *testing.T) {
	ctx := context.Background()
	_, _, bs, rsi, st, goalID, versionID, bpID, _ := translateFixture(t, plainTaskEndJSON)
	_ = bs
	_ = bpID
	g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 1000})
	prepReviewSession(t, st, rsi, goalID, g)

	sessions, err := rsi.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var got *RsiSession
	for i := range sessions {
		if sessions[i].GoalID == goalID {
			got = &sessions[i]
		}
	}
	if got == nil {
		t.Fatalf("session for goal %s not listed", goalID)
	}
	if got.WorkflowVersionID != versionID || got.BlueprintID == "" || got.BlueprintName == "" {
		t.Fatalf("session workflow snapshot = %+v (want version %s)", got, versionID)
	}
	// The fixture's blueprint is the historical global bucket (squad ''), so the
	// squad snapshot is legitimately empty; the workflow snapshot is the point.
	sess, _, _, _, err := rsi.GetSession(ctx, got.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.BlueprintName == "" || sess.BlueprintVersion == 0 {
		t.Fatalf("detail lost workflow projection: %+v", sess)
	}
}

func TestDraftProposalFromFindingManualPath(t *testing.T) {
	ctx := context.Background()
	_, _, _, rsi, st, goalID, _, bpID, _ := translateFixture(t, splitFixtureJSON)
	g := graphFor(t, st, goalID, []string{"n1", "n2", "n3"}, map[string]int64{"n1": 60000, "n2": 50000})
	g.Metrics["critical_path_ms"] = int64(110000)
	sessionID := prepReviewSession(t, st, rsi, goalID, g)
	findingID := insertFinding(t, st, sessionID, "split_candidate")

	first, err := rsi.DraftProposalFromFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("draft proposal: %v", err)
	}
	if first == nil || first.BlueprintID != bpID || first.FindingID != findingID {
		t.Fatalf("proposal not linked: %+v", first)
	}
	if first.CreatedByID != translatorCreatedByID || first.Status != "pending" {
		t.Fatalf("proposal provenance: by=%q status=%q", first.CreatedByID, first.Status)
	}

	// Clicking twice returns the same proposal instead of a duplicate.
	again, err := rsi.DraftProposalFromFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("second draft: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("second call made a new proposal %s (want %s)", again.ID, first.ID)
	}

	// The finding now reports its proposal, so the UI shows 已生成提案 and not a
	// second 生成提案 button.
	_, _, findings, _, err := rsi.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	var linked string
	for _, f := range findings {
		if f.ID == findingID {
			linked = f.ProposalID
		}
	}
	if linked != first.ID {
		t.Fatalf("finding proposal_id = %q, want %q", linked, first.ID)
	}
}

func TestDraftProposalFromFindingRefusesWithoutWorkflow(t *testing.T) {
	ctx := context.Background()
	_, _, _, rsi, st, goalID, _, _, _ := translateFixture(t, plainTaskEndJSON)
	// A goal with no pin: analysis can still run (emergent drafting), but a
	// finding must not become a proposal — there is no workflow to edit.
	agent := seedAgent(t, st, "auto-"+newID()[:6])
	// The translator fixture already seeded "test-domain"; reuse that project
	// instead of creating a second one with the same name.
	var dom string
	if err := st.DB().QueryRowContext(ctx, `SELECT domain_id FROM goal WHERE id=?`, goalID).Scan(&dom); err != nil {
		t.Fatalf("fixture domain: %v", err)
	}
	gID := newID()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO goal (id,title,description,domain_id,assignee_type,assignee_id,status,category,handoff_note,created_by_type,created_by_id,created_at,execution_mode,content_capture,budget_usd,sample_rate,blueprint_version_id)
		 VALUES (?,?,?,?,'agent',?,'active','hitl','','human','',?,'train','redacted',2,1,'')`,
		gID, "auto", "", dom, agent, now()); err != nil {
		t.Fatalf("insert goal: %v", err)
	}
	g := &WorkflowGraph{GoalID: gID, Metrics: map[string]any{}}
	sessionID := prepReviewSession(t, st, rsi, gID, g)
	findingID := insertFinding(t, st, sessionID, "split_candidate")

	_, err := rsi.DraftProposalFromFinding(ctx, findingID)
	if err == nil {
		t.Fatal("draft succeeded without a pinned workflow")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error %q is not ErrValidation", err.Error())
	}
	if !strings.Contains(err.Error(), "没有固定工作流") {
		t.Fatalf("error %q should explain the missing workflow", err.Error())
	}
}
