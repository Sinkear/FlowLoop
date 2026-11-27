package service

import (
	"context"
	"testing"
)

// TestCascadeFailedDependentRevivesWhenDependencyVerifies (P6, issue #31):
// a cascade-failed dependent (dependency exhausted its retry chain while the
// child was gated, so the child NEVER ran) stays failed forever today —
// wakeDependentsTx only scans status='running'. When the dependency is
// later restored and verifies (human retry + successful run), the revive
// path must bring the cascade casualty back: running/attempt=0 with a fresh
// run, so the 11-26 openviking stall (gate approved 19:34, downstream sat
// failed 35+ minutes) self-heals instead of waiting for a human.
func TestCascadeFailedDependentRevivesWhenDependencyVerifies(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "A", "dep", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "B", "gated", assignee, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create sgB: %v", err)
	}
	// Exhaust A's retry chain → B cascade-fails (no run, marker comment).
	for i := 0; i < maxExecutionAttempts; i++ {
		failSubGoalRun(t, rs, st, sgA.ID)
	}
	b, _ := gs.GetSubGoal(ctx, sgB.ID)
	if b.Status != "failed" {
		t.Fatalf("setup: B must be cascade-failed, got %q", b.Status)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("setup: cascade casualty must be run-less, got %d", n)
	}

	// Restore A: human retry + completed run → A verifies.
	if _, err := gs.RetrySubGoal(ctx, sgA.ID); err != nil {
		t.Fatalf("retry dependency: %v", err)
	}
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, sgA.ID), "completed", "recovered"); err != nil {
		t.Fatalf("finish dependency: %v", err)
	}
	a, _ := gs.GetSubGoal(ctx, sgA.ID)
	if a.Status != "verified" {
		t.Fatalf("dependency must be verified, got %q", a.Status)
	}

	// The revive: B must be running again with a fresh run and attempt=0.
	b, _ = gs.GetSubGoal(ctx, sgB.ID)
	if b.Status != "running" || b.ExecutionAttempt != 0 {
		t.Fatalf("cascade casualty must auto-revive (running/0), got %s/%d", b.Status, b.ExecutionAttempt)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 1 {
		t.Fatalf("revived dependent must get exactly 1 run, got %d", n)
	}
	var reviveNote int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM comment WHERE goal_id=? AND content LIKE '%自动复位并重新派发%'`, g.ID).Scan(&reviveNote); err != nil {
		t.Fatalf("count revive comments: %v", err)
	}
	if reviveNote == 0 {
		t.Fatal("the revive must record a system comment")
	}
	// And the revived run finishes normally.
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, sgB.ID), "completed", "done"); err != nil {
		t.Fatalf("finish revived dependent: %v", err)
	}
	b, _ = gs.GetSubGoal(ctx, sgB.ID)
	if b.Status != "verified" {
		t.Fatalf("revived dependent must verify, got %q", b.Status)
	}
}
