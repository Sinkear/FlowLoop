package service

// Gate/retry integrity regressions for the 2026-11-28 fix round
// (docs/use-cases/2026-11-28-blueprint-conditional-edge-semantics/):
//
//   - G6-R3 mirror drift: RetrySubGoal flipped sub_goal failed→running but
//     left blueprint_node 'failed' (markBlueprintNodeTx is monotonic), so
//     the later verify's settle mirror was dropped and the ledger lied —
//     the 11-26 openviking run left four nodes stuck failed under verified
//     sub_goals.
//   - G5 gate bypass: RetrySubGoal (and the P6 revive path) enqueued a
//     WORKER RUN for a retried gate holder — the machine verdict then
//     settled the human checkpoint. A retried/revived gate must re-HOLD.
//   - G5 pre-hold decision: ResolveBlueprintGate accepted decisions on a
//     gate whose upstream had not all verified (no held_since). Now refused.
//   - G4: specGraphToWorkflow keeps the branch port (source_handle) so the
//     canvas can render conditional edges as conditional.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ── Bug A: a retried compiled TASK settles its node again ────────────────

// The R3 drift, end to end: task terminal-failed with its node failed →
// human RetrySubGoal → run → machine verify. The node must settle; before
// the fix it stayed 'failed' forever under a verified sub_goal.
func TestRetrySubGoalMirrorSettlesAfterRetry(t *testing.T) {
	gs, rs, st, goalID, _ := gateScenario(t)
	ctx := context.Background()

	// Release the approved branch so n_ok is a live compiled task.
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}
	okSG := subGoalByTitle(t, gs, goalID, "ship it")

	// Exhaust n_ok's retry chain → terminal failed + node mirror failed
	// (the same state the cascade/exhaustion paths left on 11-26).
	for i := 0; i < maxExecutionAttempts; i++ {
		failSubGoalRun(t, rs, st, okSG.ID)
	}
	okFailed, err := gs.GetSubGoal(ctx, okSG.ID)
	if err != nil {
		t.Fatal(err)
	}
	if okFailed.Status != "failed" {
		t.Fatalf("fixture: task must be terminal-failed, got %q", okFailed.Status)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_ok"] != "failed" {
		t.Fatalf("fixture: node must start failed, got %v", states)
	}

	// Human retry: sub_goal re-runs, node mirror flips.
	if _, err := gs.RetrySubGoal(ctx, okSG.ID); err != nil {
		t.Fatalf("retry failed task: %v", err)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_ok"] != "running" {
		t.Fatalf("retried task node must flip failed→running, got %v", states)
	}

	// The retried run verifies → the node must SETTLE (previously the
	// monotonic mirror dropped the settle write and the node stayed failed).
	finishSubGoalRunCompleted(t, rs, st, okSG.ID)
	if states := blueprintNodeStates(t, st, goalID); states["n_ok"] != "settled" {
		t.Fatalf("node must settle after the retried verify (R3 drift), got %v", states)
	}
}

// ── Bug B (manual path): RetrySubGoal on a gate re-holds, never runs ─────

func TestRetrySubGoalGateReholdsInsteadOfRun(t *testing.T) {
	gs, _, st, goalID, gateSGID := gateScenario(t)
	ctx := context.Background()

	forceSubGoalStatus(t, st, gateSGID, "failed", maxExecutionAttempts)
	mustExec(t, st,
		`UPDATE blueprint_node SET state='failed' WHERE goal_id=? AND node_key=?`,
		goalID, "n_gate")

	if _, err := gs.RetrySubGoal(ctx, gateSGID); err != nil {
		t.Fatalf("retry gate holder: %v", err)
	}
	if n := subGoalRunCount(t, st, gateSGID); n != 0 {
		t.Fatalf("retried gate must NEVER enqueue a worker run, got %d", n)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_gate"] != "running" {
		t.Fatalf("retried gate node must be running (held), got %v", states)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"held_since"`) {
		t.Fatalf("retried gate must stamp held_since (sweeper + held guard), got %s", cfg)
	}
	// The re-hold is narrated in the feed (system comment).
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM comment WHERE goal_id=? AND author_type='system' AND content LIKE '%重新等待人工决策%'`,
		goalID).Scan(&n); err != nil || n == 0 {
		t.Fatalf("gate re-hold comment missing (%v)", err)
	}

	// The human decision still settles it — the re-hold is not a dead end.
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
		t.Fatalf("resolve re-held gate: %v", err)
	}
	gateSG, err := gs.GetSubGoal(ctx, gateSGID)
	if err != nil {
		t.Fatal(err)
	}
	if gateSG.Status != "verified" {
		t.Fatalf("decided gate holder must verify, got %q", gateSG.Status)
	}
	if states = blueprintNodeStates(t, st, goalID); states["n_gate"] != "settled" {
		t.Fatalf("decided gate node must settle, got %v", states)
	}
}

// ── Bug B (revive path): a revived cascade-failed gate re-holds ──────────

// The R3 sequence, end to end through the REAL cascade path: upstream task
// exhausts its retry chain → failGatedDependentsTx cascade-fails the
// gate holder (marker comment + node failed, exactly what 11-26 left).
// When the upstream is retried and verifies, the P6 revive sweep picks the
// gate casualty up — and a revived GATE holder must re-hold, never enqueue.
func TestReviveFailedGateReholdsInsteadOfRun(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedContent(t, bs, "gate-bp", gateJSON(worker))
	g, err := gs.Create(ctx, Goal{
		Title: "gate goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	goalID := g.ID
	seedSG := subGoalByTitle(t, gs, goalID, "prepare")
	gateSGID := subGoalByTitle(t, gs, goalID, "human sign-off").ID

	// Kill the upstream: exhaust the seed's retry chain → terminal failed.
	for i := 0; i < maxExecutionAttempts; i++ {
		failSubGoalRun(t, rs, st, seedSG.ID)
	}
	seedFailed, err := gs.GetSubGoal(ctx, seedSG.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seedFailed.Status != "failed" {
		t.Fatalf("fixture: seed must be terminal-failed, got %q", seedFailed.Status)
	}
	// The gate holder cascade-failed — run-less, marker comment, node failed.
	gateFailed, err := gs.GetSubGoal(ctx, gateSGID)
	if err != nil {
		t.Fatal(err)
	}
	if gateFailed.Status != "failed" {
		t.Fatalf("fixture: gate holder must be cascade-failed, got %q", gateFailed.Status)
	}
	if n := subGoalRunCount(t, st, gateSGID); n != 0 {
		t.Fatalf("fixture: cascade-failed gate must be run-less, got %d", n)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_gate"] != "failed" {
		t.Fatalf("fixture: gate node must be failed, got %v", states)
	}

	// Restore the upstream: human retry + completed run → seed verifies.
	if _, err := gs.RetrySubGoal(ctx, seedSG.ID); err != nil {
		t.Fatalf("retry seed: %v", err)
	}
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, seedSG.ID), "completed", "recovered"); err != nil {
		t.Fatalf("finish retried seed: %v", err)
	}
	seedVerified, _ := gs.GetSubGoal(ctx, seedSG.ID)
	if seedVerified.Status != "verified" {
		t.Fatalf("seed must verify, got %q", seedVerified.Status)
	}

	// The revive: the gate casualty re-HOLDS — running sub_goal, no run,
	// node running, held_since stamped — and the human still decides.
	gateSG, err := gs.GetSubGoal(ctx, gateSGID)
	if err != nil {
		t.Fatal(err)
	}
	if gateSG.Status != "running" {
		t.Fatalf("revived gate holder must be running (held), got %q", gateSG.Status)
	}
	if n := subGoalRunCount(t, st, gateSGID); n != 0 {
		t.Fatalf("revived gate must NEVER enqueue a worker run, got %d", n)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_gate"] != "running" {
		t.Fatalf("revived gate node must be running (held), got %v", states)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"held_since"`) {
		t.Fatalf("revived gate must stamp held_since (sweeper + held guard), got %s", cfg)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM comment WHERE goal_id=? AND author_type='system' AND content LIKE '%重新等待人工决策%'`,
		goalID).Scan(&n); err != nil || n == 0 {
		t.Fatalf("gate revive comment missing (%v)", err)
	}
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
		t.Fatalf("resolve revived gate: %v", err)
	}
	if gateSG, _ = gs.GetSubGoal(ctx, gateSGID); gateSG.Status != "verified" {
		t.Fatalf("revived gate must still be decidable, got %q", gateSG.Status)
	}
}

// ── Bug C: a decision before hold is refused ────────────────────────────

// Post-compile, pre-release: the gate node is pending with NO held_since
// (upstream unverified). A decision now would settle the holder and release
// downstream past work that may still fail — refused.
func TestResolveBlueprintGateRefusesPreHoldDecision(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedContent(t, bs, "gate-bp", gateJSON(worker))
	g, err := gs.Create(ctx, Goal{
		Title: "pre-hold goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if states := blueprintNodeStates(t, st, g.ID); states["n_gate"] != "pending" {
		t.Fatalf("fixture: gate must still be pending pre-release, got %v", states)
	}

	err = gs.ResolveBlueprintGate(ctx, g.ID, "n_gate", "approve", "too early", "human")
	if err == nil {
		t.Fatal("pre-hold decision must be refused")
	}
	if !strings.Contains(err.Error(), "not held yet") {
		t.Fatalf("refusal must say the gate is not held yet, got %q", err.Error())
	}
	if states := blueprintNodeStates(t, st, g.ID); states["n_gate"] != "pending" {
		t.Fatalf("refused decision must leave the gate pending, got %v", states)
	}
}

// The held path still decides (the guard does not over-block).
func TestResolveBlueprintGateHeldStillDecides(t *testing.T) {
	gs, _, st, goalID, gateSGID := gateScenario(t)
	ctx := context.Background()

	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"held_since"`) {
		t.Fatalf("fixture: gate must be held before deciding, got %s", cfg)
	}
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
		t.Fatalf("held gate decision must pass: %v", err)
	}
	gateSG, err := gs.GetSubGoal(ctx, gateSGID)
	if err != nil {
		t.Fatal(err)
	}
	if gateSG.Status != "verified" {
		t.Fatalf("decided holder must verify, got %q", gateSG.Status)
	}
}

// ── G4: specGraphToWorkflow keeps the branch port ────────────────────────

// The workflow graph must carry each spec edge's source_handle so the
// canvas can render a conditional edge as conditional (done/failed/
// approve/reject/timeout…), not as an unconditional arrow.
func TestSpecGraphToWorkflowKeepsHandle(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	v := seedPublishedContent(t, bs, "gate-bp", gateJSON(worker))

	wf, err := bs.SpecWorkflowGraph(ctx, v.ID)
	if err != nil {
		t.Fatalf("spec workflow graph: %v", err)
	}
	var approve, reject, timeout, seedDone bool
	for _, e := range wf.Edges {
		if e.From == "n_gate" {
			switch e.Handle {
			case "approve":
				approve = true
			case "reject":
				reject = true
			case "timeout":
				timeout = true
			}
		}
		if e.From == "n_seed" && e.Handle == "done" {
			seedDone = true
		}
	}
	if !approve || !reject || !timeout || !seedDone {
		t.Fatalf("handles lost: approve=%v reject=%v timeout=%v seedDone=%v (edges %+v)",
			approve, reject, timeout, seedDone, wf.Edges)
	}
	// The JSON the frontend receives carries the tag.
	b, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	if !strings.Contains(string(b), `"handle":"approve"`) {
		t.Fatalf("JSON must carry handle tags, got %s", b)
	}
}

// ── Sweeper clock: markGateHeldTx re-entry must not restart it ──────────

// The held_since stamp is the sweeper's clock (SweepBlueprintGates reads it
// against the gate's timeout policy) and ResolveBlueprintGate's held guard
// keys on it. wakeDependentsTx re-scans a still-held gate on EVERY later
// settle in the goal (gatedSubGoalsTx has no held exemption), so
// markGateHeldTx must be a NO-OP once held_since exists — its pre-existing
// early return is the idempotency the function comment calls "refreshes
// nothing". A regression here defers gate timeouts indefinitely on goals
// with concurrent branch activity. Lock the contract with a sentinel.
func TestMarkGateHeldTxKeepsFirstStamp(t *testing.T) {
	_, _, st, _, gateSGID := gateScenario(t) // scenario ends with the gate held
	ctx := context.Background()

	// Plant a clearly-old sentinel stamp — the gate is already held, so
	// rewriting held_since directly models "held long ago".
	sentinel := "2020-01-01T00:00:00Z"
	var cfg string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`, gateSGID).
		Scan(&cfg); err != nil {
		t.Fatalf("load gate ledger: %v", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		t.Fatalf("parse gate ledger: %v", err)
	}
	m["held_since"] = sentinel
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal ledger: %v", err)
	}
	mustExec(t, st, `UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`, string(b), gateSGID)

	// Re-entry — the shape wakeDependentsTx produces on every later settle.
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := markGateHeldTx(ctx, tx, gateSGID); err != nil {
		t.Fatalf("markGateHeldTx re-entry: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The sentinel must survive: a second call never restarts the clock.
	var cfg2 string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`, gateSGID).
		Scan(&cfg2); err != nil {
		t.Fatalf("reload gate ledger: %v", err)
	}
	m2 := map[string]any{}
	if err := json.Unmarshal([]byte(cfg2), &m2); err != nil {
		t.Fatalf("parse reloaded ledger: %v", err)
	}
	if got, _ := m2["held_since"].(string); got != sentinel {
		t.Fatalf("held_since was re-stamped (sweeper clock reset): got %q want %q", got, sentinel)
	}
}
