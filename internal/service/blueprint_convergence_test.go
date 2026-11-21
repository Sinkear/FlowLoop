package service

// §14 convergence tests: layered signatures (L0 skeleton / L1 exact) and the
// auto-iteration round cap (rsi.max_auto_rounds). These extend the Phase C
// §12.4 suite — the emergent-draft behavior these tests cover is defined in
// docs/BLUEPRINT-PROPOSAL.md §14.1/§14.2.

import (
	"context"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/store"
)

// noisyShapeGoal: a done infer goal whose WORK SKELETON matches
// runInferGoalToEnd's single-sub_goal shape, but whose L1 exact signature
// differs because it carries a consult run (consult node + consult edges).
// This is the §14.1 scenario: same real process, run-to-run support-loop
// noise. It reuses the C-suite helpers via a minimal local copy to stay
// self-contained (see differentShapeGoal for the pattern).
var noiseRound int

func noisyShapeGoal(t *testing.T, gs *GoalService, rs *RunService, st *store.Store, domainID string) string {
	t.Helper()
	worker := seedAgent(t, st, "w3-"+newID()[:8])
	g, err := gs.Create(context.Background(), Goal{
		Title: "tn", AssigneeType: "agent", AssigneeID: worker,
		Status: "active", DomainID: domainID, Category: "hootl",
	})
	if err != nil {
		t.Fatal(err)
	}
	sg, err := gs.CreateSubGoal(context.Background(), g.ID, "s", "d", worker, "", nil, "human", "")
	if err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY created_at DESC LIMIT 1`, sg.ID).
		Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := rs.Finish(context.Background(), runID, "completed", "ok"); err != nil {
		t.Fatal(err)
	}
	// The noise: a finished consult run against the same goal (consult node
	// enters the L1 kind multiset, never the L0 skeleton). The number of
	// noise runs varies per call so two noisy goals' L1 signatures differ
	// from EACH OTHER while their skeletons stay equal — the true near-miss
	// scenario (L1 only hashes kind/edge MULTISETS, so a single consult node
	// in both goals would leave L1 equal too).
	noiseRuns := 1 + noiseRound
	noiseRound++
	for i := 0; i < noiseRuns; i++ {
		if _, err := st.DB().ExecContext(context.Background(),
			`INSERT INTO run (id,goal_id,agent_id,status,role,trigger_comment_id,queued_at,started_at,finished_at,created_at,result_summary)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			newID(), g.ID, worker, "completed", "consult", "", now(), now(), now(), now(), "advice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	goalObj, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(context.Background(), *goalObj); err != nil {
		t.Fatal(err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatal(err)
	}
	if err := rs.Finish(context.Background(), ownerRun, "completed", "done"); err != nil {
		t.Fatal(err)
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	got, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status=%s", got.Status)
	}
	return g.ID
}

// TestSkeletonSignatureIgnoresSupportLoops: the L0 skeleton of a consult-
// noisy goal equals the L0 skeleton of a plain goal, while their L1 exact
// signatures differ. This is the mechanism §14.1 relies on.
func TestSkeletonSignatureIgnoresSupportLoops(t *testing.T) {
	gs, rs, _, rsi, st := propCluster(t)
	dom := seedDomain(t, st)

	plainID := runInferGoalToEnd(t, gs, rs, st, dom)
	plainWF, err := rsi.BuildGoalWorkflow(context.Background(), plainID)
	if err != nil {
		t.Fatal(err)
	}
	noisyID := noisyShapeGoal(t, gs, rs, st, dom)
	noisyWF, err := rsi.BuildGoalWorkflow(context.Background(), noisyID)
	if err != nil {
		t.Fatal(err)
	}
	if executionShapeSignature(plainWF) == executionShapeSignature(noisyWF) {
		t.Fatal("L1 exact signatures unexpectedly EQUAL — noise must perturb L1")
	}
	if skeletonShapeSignature(plainWF) != skeletonShapeSignature(noisyWF) {
		t.Fatal("L0 skeletons differ — support-loop noise must be stripped in L0")
	}
}

// TestNearMissSkeletonTripleDraftsApproximate (§14.1): three runs with the
// same work skeleton but differing details → exactly one 近似固化 draft
// (named by the skeleton signature, still unpublished, still dedup'd on a
// fourth near-miss run).
func TestNearMissSkeletonTripleDraftsApproximate(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)

	// 1st shape: plain single-sub_goal run.
	runInferGoalToEnd(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	// 2nd + 3rd: consult-noisy variants — same skeleton, different L1.
	for i := 0; i < 2; i++ {
		noisyShapeGoal(t, gs, rs, st, dom)
		if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
			t.Fatal(err)
		}
		bps, _ := bs.List(context.Background())
		if i == 0 && len(bps) != 0 {
			t.Fatalf("2 shapes must not draft, got %d", len(bps))
		}
	}
	bps, _ := bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("near-miss triple must draft exactly once, got %d", len(bps))
	}
	if !strings.HasPrefix(bps[0].Name, "近似固化：") {
		t.Fatalf("draft name = %q", bps[0].Name)
	}
	if bps[0].LatestVersionID != "" {
		t.Fatal("approximate draft must NOT be published")
	}

	// 4th near-miss run: dedup into the SAME draft (skeleton-keyed name).
	noisyShapeGoal(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("skeleton-keyed dedup failed: %d blueprints", len(bps))
	}

	// drafted ledger: the shape row of the goal that produced the draft is
	// marked (cap counting relies on it).
	var drafted int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM infer_shape WHERE domain_id=? AND drafted=1`, dom).Scan(&drafted); err != nil {
		t.Fatal(err)
	}
	if drafted != 1 {
		t.Fatalf("drafted rows = %d, want 1", drafted)
	}
}

// TestAutoRoundsCapZeroDisablesEmergentDraft (§14.2): rsi.max_auto_rounds=0
// turns auto-drafting off entirely; shape recording itself continues
// (visibility is never capped).
func TestAutoRoundsCapZeroDisablesEmergentDraft(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO app_settings (key,value,updated_at) VALUES ('rsi.max_auto_rounds','0',?)`, now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		runInferGoalToEnd(t, gs, rs, st, dom)
		if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
			t.Fatal(err)
		}
	}
	bps, _ := bs.List(context.Background())
	if len(bps) != 0 {
		t.Fatalf("cap=0 must disable auto-drafting, got %d", len(bps))
	}
	// Recording still happened — the cap never regresses visibility.
	var shapes int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM infer_shape WHERE domain_id=?`, dom).Scan(&shapes); err != nil {
		t.Fatal(err)
	}
	if shapes != 3 {
		t.Fatalf("shape rows = %d, want 3", shapes)
	}
}

// TestAutoRoundsCapReachedAfterFirstDraft (§14.2): with cap=1 the first
// triple drafts, the NEXT streak is capped (drafted counter already 1).
func TestAutoRoundsCapReachedAfterFirstDraft(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO app_settings (key,value,updated_at) VALUES ('rsi.max_auto_rounds','1',?)`, now()); err != nil {
		t.Fatal(err)
	}
	// First streak of 3 → 1 draft (cap not yet reached).
	for i := 0; i < 3; i++ {
		runInferGoalToEnd(t, gs, rs, st, dom)
		if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
			t.Fatal(err)
		}
	}
	bps, _ := bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("first streak should draft under cap=1, got %d", len(bps))
	}
	// Second streak: a DIFFERENT skeleton shape (two sub_goals) drafts
	// nothing — cap reached (drafted=1 >= 1).
	differentShapeGoal(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	differentShapeGoal(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	differentShapeGoal(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("cap=1 must block the second draft, got %d", len(bps))
	}
}
