package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
)

// seedNextRoundFixture builds the incident's RSI shape:
// origin training goal → session → report → finding → approved proposal whose
// applied version is still a draft (ApproveProposal opens a draft, never
// publishes). The next-round entry is the only place that closes the loop.
type nextRoundFixture struct {
	gs      *GoalService
	bs      *BlueprintService
	origin  *Goal
	draftID string
	propID  string
	agentID string
	domain  string
}

func seedNextRoundFixture(t *testing.T) *nextRoundFixture {
	t.Helper()
	gs, _, bs, st := bpCluster(t)
	owner := seedAgent(t, st, "owner")
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	ctx := context.Background()

	v1 := seedPublishedContent(t, bs, "loop-bp", strings.ReplaceAll(bpJSON("loop", "second"), "agent:WORKER", "agent:"+worker))
	origin, err := gs.Create(ctx, Goal{
		Title: "原始训练目标", Description: "基线", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog", Category: "hitl",
		ExecutionMode: "train", ContentCapture: "full", BudgetUSD: 5, SampleRate: 0.5,
		BlueprintVersionID: v1.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := now()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mustExec(`INSERT INTO training_session (id,goal_id,content_capture,sample_rate,budget_usd,status,started_at)
	          VALUES ('ts1',?,'full',0.5,5,'ready',?)`, origin.ID, ts)
	mustExec(`INSERT INTO rsi_report (id,session_id,status,created_at) VALUES ('rp1','ts1','ready',?)`, ts)
	mustExec(`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,status,created_at)
	          VALUES ('f1','rp1','plan_deviation','high','L1','boom','open',?)`, ts)

	// ApproveProposal needs a pending proposal + valid patches; a direct row is
	// enough here because the next-round entry only reads applied_version_id.
	draft, err := bs.NewDraft(ctx, v1.BlueprintID, "proposal patch")
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	mustExec(`INSERT INTO blueprint_proposal
	          (id,blueprint_id,base_version_id,title,hypothesis,expected_metric,expected_direction,patches_json,finding_id,status,applied_version_id,decided_at,decided_by_type,decided_by_id,created_by_type,created_by_id,created_at)
	          VALUES ('p1',?,?,?,?,?,?,'[]','f1','approved',?,?,'human','','rsi-translator','',?)`,
		v1.BlueprintID, v1.ID, "提案标题", "假设", "wall_clock_ms", "down", draft.ID, ts, ts)

	return &nextRoundFixture{gs: gs, bs: bs, origin: origin, draftID: draft.ID, propID: "p1", agentID: owner, domain: domID}
}

func TestNextRoundFromProposalPublishesAndStarts(t *testing.T) {
	fx := seedNextRoundFixture(t)
	ctx := context.Background()

	// The applied version starts as a draft: approval alone must not run it.
	if v, err := fx.bs.GetVersion(ctx, fx.draftID); err != nil || v.Status != "draft" {
		t.Fatalf("fixture precondition: version must be draft, got %v (%v)", v, err)
	}

	g, created, err := fx.gs.StartNextRoundFromProposal(ctx, fx.propID, "human", "", true)
	if err != nil {
		t.Fatalf("next round: %v", err)
	}
	if !created || g == nil {
		t.Fatalf("first call must create")
	}
	if g.Status != "active" {
		t.Fatalf("start=true must create an active round, got %q", g.Status)
	}
	if g.ExecutionMode != "train" || g.BlueprintVersionID != fx.draftID {
		t.Fatalf("round must be train + pinned to the applied version, got %+v", g)
	}
	if g.DomainID != fx.domain || g.AssigneeID != fx.agentID || g.AssigneeType != "agent" {
		t.Fatalf("round must inherit the origin execution context, got %+v", g)
	}
	if g.SourceRef != "rsi-proposal:"+fx.propID {
		t.Fatalf("round must carry the proposal source_ref, got %q", g.SourceRef)
	}
	if !strings.HasPrefix(g.Title, "下一轮：") {
		t.Fatalf("round title must mark the round, got %q", g.Title)
	}
	if !strings.Contains(g.Description, fx.propID) || !strings.Contains(g.Description, "wall_clock_ms") {
		t.Fatalf("description must cite proposal + expected metric, got %q", g.Description)
	}

	// The loop requires the applied version to become authority.
	if v, err := fx.bs.GetVersion(ctx, fx.draftID); err != nil || v.Status != "published" {
		t.Fatalf("applied version must be published by the entry, got %v (%v)", v, err)
	}
	// Create(active)+pin compiles the plan (BUG-3 invariant), so the round is
	// actually spec-bound — the whole point of the next round.
	var nodes int
	if err := fx.gs.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, g.ID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes == 0 {
		t.Fatalf("active pinned round must materialize its plan")
	}
	// And its first owner run exists (active creation is run-bearing).
	var runs int
	if err := fx.gs.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM run WHERE goal_id=?`, g.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs == 0 {
		t.Fatalf("started round must enqueue its first run")
	}

	// Idempotent: a second click returns the same goal and does not create a
	// duplicate round.
	again, created, err := fx.gs.StartNextRoundFromProposal(ctx, fx.propID, "human", "", true)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created || again.ID != g.ID {
		t.Fatalf("second call must be a no-op, got created=%v id=%s want %s", created, again.ID, g.ID)
	}
	var count int
	if err := fx.gs.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal WHERE source_ref=?`, "rsi-proposal:"+fx.propID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("exactly one next-round goal, got %d", count)
	}
}

func TestNextRoundFromProposalCanStayBacklog(t *testing.T) {
	fx := seedNextRoundFixture(t)
	g, created, err := fx.gs.StartNextRoundFromProposal(context.Background(), fx.propID, "human", "", false)
	if err != nil {
		t.Fatalf("next round: %v", err)
	}
	if !created || g.Status != "backlog" {
		t.Fatalf("start=false must create a backlog round, got %+v", g)
	}
	var nodes int
	if err := fx.gs.st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, g.ID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes != 0 {
		t.Fatalf("backlog rounds stay uncompiled until Activate, got %d nodes", nodes)
	}
}

func TestNextRoundRejectsPendingProposal(t *testing.T) {
	fx := seedNextRoundFixture(t)
	if _, err := fx.gs.st.DB().ExecContext(context.Background(),
		`UPDATE blueprint_proposal SET status='pending', applied_version_id='' WHERE id=?`, fx.propID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.gs.StartNextRoundFromProposal(context.Background(), fx.propID, "human", "", true); err == nil {
		t.Fatalf("pending proposals have no applied version to run")
	}
}

// TestNextRoundFindinglessProposal explains the required refusal: a proposal
// without a finding cannot infer the origin domain/assignee, so the entry must
// fail loudly instead of creating an orphan round.
func TestNextRoundFindinglessProposal(t *testing.T) {
	fx := seedNextRoundFixture(t)
	if _, err := fx.gs.st.DB().ExecContext(context.Background(),
		`UPDATE blueprint_proposal SET finding_id='' WHERE id=?`, fx.propID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fx.gs.StartNextRoundFromProposal(context.Background(), fx.propID, "human", "", true); err == nil {
		t.Fatalf("proposal without finding must be refused")
	}
}

var _ = events.NewBus

func TestNextRoundInFlightRejectsConcurrentCall(t *testing.T) {
	fx := seedNextRoundFixture(t)
	ctx := context.Background()
	sourceRef := "rsi-proposal:" + fx.propID

	// Pretend another request is mid-flight (between publish and Create).
	nextRoundInFlight.Store(sourceRef, struct{}{})
	defer nextRoundInFlight.Delete(sourceRef)

	if _, _, err := fx.gs.StartNextRoundFromProposal(ctx, fx.propID, "human", "", true); err == nil {
		t.Fatal("concurrent next-round call must be refused while another is in flight")
	} else if !strings.Contains(err.Error(), "already being created") {
		t.Fatalf("want in-flight conflict, got %v", err)
	}
	if existing, err := fx.gs.getBySourceRef(ctx, sourceRef); err != nil || existing != nil {
		t.Fatalf("refused call must not create a goal (got %v, %v)", existing, err)
	}
}
