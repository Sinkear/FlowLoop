package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/store"
)

// The F1/F2/F3 fix's regression tests (2026-11-14 kimicode incident — see
// docs/use-cases/2026-11-14-kimicode-hitl-gate/). The authorize primitive:
//
//	park semantics   approve = release back to active + wake owner to CONTINUE
//	                 reject  = back active with the reason (human_iterations++)
//	staging          RequestAuthorization stages review_request="authorize: …"
//	                 on the ACTIVE goal; the end-of-run reconcile parks it
//	                 BEFORE the finalization guard (pending sub-goals are the
//	                 point of a mid-pipeline gate, not a blocker)
//	reviewers        enqueueSquadReviewTx skips authorize parks — there is no
//	                 diff to review, and a review run's "nothing was
//	                 implemented" opinion was the incident's misleading REJECT

// stageAuthorize drives RequestAuthorization the way the daemon does for a
// live owner run: enqueue (queued), flip to running, call, return.
func stageAuthorize(t *testing.T, gs *GoalService, rs *RunService, st *store.Store, g *Goal, agentID, statement string) {
	t.Helper()
	ctx := context.Background()
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatalf("mark run running: %v", err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentID, r.ID, statement); err != nil {
		t.Fatalf("request authorization: %v", err)
	}
}

// TestAuthorizeApproveReleasesGoalToContinue: the F1 core. An authorize park
// approved does NOT deliver/close the goal (the old semantics — the incident:
// 批准 = 接受未完成的现状并关单). It releases the goal back to active AND
// wakes the owner with a continue note. A successor run must exist (P0-2:
// enqueued in the same tx as the status flip).
func TestAuthorizeApproveReleasesGoalToContinue(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	domID := seedDomain(t, st) // frozen, NO gates: completion would auto-done

	g, err := gs.Create(ctx, Goal{Title: "升级 kimicode", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID, Category: "hitl"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatalf("mark run running: %v", err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "升级 0.39.1→2.6.2 属跨大版本（risk=high），请授权后我再派发 Upgrader 执行升级。"); err != nil {
		t.Fatalf("request authorization: %v", err)
	}
	// The run ends — the staged request parks the goal (NOT the hootl
	// auto-complete, NOT the finalization guard, NOT done).
	if err := rs.Finish(ctx, r.ID, "completed", "已到人工闸门"); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	parked, _ := gs.Get(ctx, g.ID)
	if parked.Status != "review" {
		t.Fatalf("authorize: goal must park in review at the gate, got %q", parked.Status)
	}
	if !strings.HasPrefix(parked.ReviewRequest, "authorize: ") {
		t.Fatalf("authorize: review_request must keep the authorize prefix + statement, got %q", parked.ReviewRequest)
	}

	// The human approves → CONTINUE: back to active, successor enqueued.
	resolved, err := gs.ResolveReview(ctx, g.ID, r.ID, "approve", "", "human")
	if err != nil {
		t.Fatalf("approve authorization: %v", err)
	}
	if resolved.Status != "active" {
		t.Fatalf("authorize approve must RELEASE the goal to active (continue), got %q", resolved.Status)
	}
	if resolved.ReviewRequest != "" {
		t.Fatalf("authorize approve must clear review_request, got %q", resolved.ReviewRequest)
	}
	// P0-2: the successor owner run was enqueued in the resolve tx.
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='owner' AND status='queued'`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("authorize approve must wake the owner (successor run queued), found %d", n)
	}
	// The wake note tells the owner to continue, not to re-verify.
	var wake string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(wake_note,'') FROM run WHERE goal_id=? AND role='owner' AND status='queued' ORDER BY created_at DESC LIMIT 1`, g.ID).Scan(&wake); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wake, "approved") || !strings.Contains(wake, "continue") {
		t.Fatalf("wake note must say the human approved + continue, got %q", wake)
	}
	// human_iterations untouched by an authorize approve — the human did
	// not send the work back.
	if resolved.HumanIterations != 0 {
		t.Fatalf("authorize approve must not bump human_iterations, got %d", resolved.HumanIterations)
	}
}

// TestAuthorizeRejectSendsItBack: reject at an authorization gate is the
// "don't proceed" answer — the ordinary reject path (back to active with
// the reason, human_iterations++, successor woken with it).
func TestAuthorizeRejectSendsItBack(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	domID := seedDomain(t, st)

	g, err := gs.Create(ctx, Goal{Title: "gated work", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID, Category: "hitl"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "升级前请授权"); err != nil {
		t.Fatalf("request authorization: %v", err)
	}
	if err := rs.Finish(ctx, r.ID, "completed", "at gate"); err != nil {
		t.Fatal(err)
	}
	parked, _ := gs.Get(ctx, g.ID)
	if parked.Status != "review" {
		t.Fatalf("expected review park, got %q", parked.Status)
	}
	resolved, err := gs.ResolveReview(ctx, g.ID, r.ID, "reject", "暂不升级，保持 0.39.1", "human")
	if err != nil {
		t.Fatalf("reject authorization: %v", err)
	}
	if resolved.Status != "active" {
		t.Fatalf("authorize reject must send the goal back active, got %q", resolved.Status)
	}
	if resolved.HumanIterations != 1 {
		t.Fatalf("authorize reject bumps human_iterations, got %d", resolved.HumanIterations)
	}
	var n int
	_ = st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='owner' AND status='queued'`, g.ID).Scan(&n)
	if n < 1 {
		t.Fatalf("authorize reject must wake the owner with the reason, found %d queued", n)
	}
}

// TestAuthorizeParkSkipsReviewerRuns: R3. An authorize park must NOT pull
// reviewer runs — there is no diff to review; the incident's review run
// produced a misleading REJECT ("升级未执行") precisely because the
// acceptance frame was applied to an authorization gate.
func TestAuthorizeParkSkipsReviewerRuns(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")

	domID := seedDomain(t, st)
	// A squad goal: the park path that normally enqueues reviewers.
	squadSvc := NewSquadService(st, events.NewBus())
	sq, err := squadSvc.Create(ctx, Squad{Name: "sq", LeaderID: agentA})
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	if _, err := squadSvc.AddMember(ctx, sq.ID, "agent", agentA, "reviewer"); err != nil {
		t.Fatalf("add reviewer: %v", err)
	}
	g, err := gs.Create(ctx, Goal{Title: "squad gated", AssigneeType: "squad", AssigneeID: sq.ID, Status: "active", DomainID: domID, Category: "hitl"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running', is_leader_run=1 WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "请授权继续下一阶段"); err != nil {
		t.Fatalf("request authorization: %v", err)
	}
	if err := rs.Finish(ctx, r.ID, "completed", "gate"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='review'`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("authorize park must not enqueue reviewer runs (R3), found %d", n)
	}
}

// TestAuthorizeSurvivesPendingSubGoals: F2. A mid-pipeline gate freezes
// around PENDING work — the authorize park must fire even though the
// finalization guard would hold (the incident: only the Checker sub-goal
// was verified, Upgrader/Verifier/Packager never dispatched; the goal
// drifted to an empty acceptance review instead of an explicit gate).
func TestAuthorizeSurvivesPendingSubGoals(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	agentB := seedAgent(t, st, "B")
	domID := seedDomain(t, st)

	g, err := gs.Create(ctx, Goal{Title: "pipeline", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID, Category: "hitl"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// A pending sub-goal — the finalization guard would HOLD the goal
	// active on run end; the authorize park must preempt it.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO sub_goal (id,goal_id,title,description,status,assignee_id,created_at) VALUES (?,?,?,?,'pending',?,?)`,
		"sg_pipeline_1", g.ID, "执行升级", "", agentB, now()); err != nil {
		t.Fatal(err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "检查完成，升级高风险，请授权执行"); err != nil {
		t.Fatalf("request authorization: %v", err)
	}
	if err := rs.Finish(ctx, r.ID, "completed", "checker done"); err != nil {
		t.Fatal(err)
	}
	after, _ := gs.Get(ctx, g.ID)
	if after.Status != "review" {
		t.Fatalf("authorize park must preempt the finalization guard with pending sub-goals (F2), got %q", after.Status)
	}
	// The pending sub-goal survives the park untouched.
	var sgStatus string
	if err := st.DB().QueryRowContext(ctx, `SELECT status FROM sub_goal WHERE id='sg_pipeline_1'`).Scan(&sgStatus); err != nil {
		t.Fatal(err)
	}
	if sgStatus != "pending" {
		t.Fatalf("pending sub-goal must survive the authorize park, got %q", sgStatus)
	}
}

// TestAuthorizeFromDeadRunRejected: only a LIVE run may stage the ask — a
// queued/terminal run (or a replayed token) must not.
func TestAuthorizeFromDeadRunRejected(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatal(err)
	}
	r := enqueueFirst(t, rs, g) // still queued
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "x"); err == nil {
		t.Fatal("authorize must be rejected from a non-running run")
	}
}

// TestAuthorizeNonOwnerRejected: a guest agent cannot park someone else's
// goal (defense in depth past the run-token binding).
func TestAuthorizeNonOwnerRejected(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	agentB := seedAgent(t, st, "B")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatal(err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running', agent_id=?, role='consult' WHERE id=?`, agentB, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentB, r.ID, "x"); err == nil {
		t.Fatal("authorize must be rejected for a non-owner run")
	}
}

// TestSubmitReviewWithPendingSubGoalsRejected: C1 — submit-review's approve
// closes the goal via MarkDelivered, whose invariant asserts "Pending
// sub-goals cannot exist here". Submitting with pending sub-goals used to
// sail through the live-run check and left dangling rows on approve; now
// it is refused and the owner is pointed at authorize for mid-pipeline
// gates instead.
func TestSubmitReviewWithPendingSubGoalsRejected(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	agentB := seedAgent(t, st, "B")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatal(err)
	}
	// Create enqueued the first owner run; make it terminal so the goal is
	// submittable (no live run) — the pending-SG check is what's under test.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='completed', finished_at=? WHERE goal_id=?`, now(), g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO sub_goal (id,goal_id,title,description,status,assignee_id,created_at) VALUES (?,?,?,?,'pending',?,?)`,
		"sg_submit_1", g.ID, "wip", "", agentB, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.SubmitForReview(ctx, g.ID, ""); err == nil {
		t.Fatal("submit-review must be rejected while sub-goals are pending")
	}
	after, _ := gs.Get(ctx, g.ID)
	if after.Status != "active" {
		t.Fatalf("rejected submit must leave the goal active, got %q", after.Status)
	}
	// All terminal → submit goes through.
	if _, err := st.DB().ExecContext(ctx, `UPDATE sub_goal SET status='verified' WHERE id='sg_submit_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.SubmitForReview(ctx, g.ID, ""); err != nil {
		t.Fatalf("submit-review must pass once sub-goals are terminal: %v", err)
	}
}

// TestAuthorizeDuplicateDecisionIsIdempotent: the duplicate-decision guard
// (built for the async deliver window) does not apply to authorize parks —
// there is no deliver step; a second approve after release lands on an
// active goal and fails with the plain "not in review" shape.
func TestAuthorizeDuplicateDecisionIsIdempotent(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedAgent(t, st, "A")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID, Category: "hitl"})
	if err != nil {
		t.Fatal(err)
	}
	r := enqueueFirst(t, rs, g)
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.RequestAuthorization(ctx, g.ID, agentA, r.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	if err := rs.Finish(ctx, r.ID, "completed", "gate"); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.ResolveReview(ctx, g.ID, r.ID, "approve", "", "human"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	// A second click on the stale card: goal is active now — the approve
	// must fail (no double release, no phantom successor).
	if _, err := gs.ResolveReview(ctx, g.ID, r.ID, "approve", "", "human"); err == nil {
		t.Fatal("second approve on a released goal must fail")
	}
	var queued int
	_ = st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='owner' AND status='queued'`, g.ID).Scan(&queued)
	if queued != 1 {
		t.Fatalf("double approve must not double-queue successors, found %d", queued)
	}
}
