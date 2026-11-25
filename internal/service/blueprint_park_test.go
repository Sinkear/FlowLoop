package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// parkGateJSON is the B-3 gate fixture without a timeout edge: the gate's
// deadline (when declared) can only PARK — there is no automatic route to
// take, which is exactly the "default park" policy.
func parkGateJSON(workerID, timeoutJSON string) string {
	s := `{
  "name": "park-bp",
  "description": "gate park fixture",
  "nodes": [
    {"id": "n_seed", "type": "task", "title": "prepare",
     "task": {"assignee": "agent:WORKER", "brief": "prepare the proposal"}},
    {"id": "n_gate", "type": "gate", "title": "human sign-off",
     "gate": {"prompt": "放行还是驳回？",
       "actions": [
         {"id": "approve", "label": "放行"},
         {"id": "reject", "label": "驳回"}
       ]TIMEOUT}},
    {"id": "n_ok", "type": "task", "title": "ship it",
     "task": {"assignee": "agent:WORKER", "brief": "ship the approved work"}},
    {"id": "n_no", "type": "task", "title": "rework",
     "task": {"assignee": "agent:WORKER", "brief": "rework per rejection"}},
    {"id": "n_end_ok", "type": "end", "title": "fin-ok", "end": {"outcome": "success"}},
    {"id": "n_end_no", "type": "end", "title": "fin-no", "end": {"outcome": "failure"}}
  ],
  "edges": [
    {"source": "n_seed", "source_handle": "done", "target": "n_gate"},
    {"source": "n_gate", "source_handle": "approve", "target": "n_ok"},
    {"source": "n_gate", "source_handle": "reject", "target": "n_no"},
    {"source": "n_ok", "source_handle": "done", "target": "n_end_ok"},
    {"source": "n_no", "source_handle": "done", "target": "n_end_no"}
  ]
}`
	if timeoutJSON == "" {
		s = strings.ReplaceAll(s, "TIMEOUT", "")
	} else {
		s = strings.ReplaceAll(s, "TIMEOUT", ",\n       \"timeout\": "+timeoutJSON)
	}
	return strings.ReplaceAll(s, "agent:WORKER", "agent:"+workerID)
}

// parkGateScenario compiles a gate spec (no timeout edge) and holds the gate.
func parkGateScenario(t *testing.T, timeoutJSON string) (*GoalService, *RunService, *store.Store, string, string) {
	t.Helper()
	gs, rs, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "park-bp", parkGateJSON(worker, timeoutJSON))
	g, err := gs.Create(ctx, Goal{
		Title: "park goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	seedSG := subGoalByTitle(t, gs, g.ID, "prepare")
	finishSubGoalRunCompleted(t, rs, st, seedSG.ID)
	return gs, rs, st, g.ID, seedSG.ID
}

func backdateHeldSince(t *testing.T, st *store.Store, goalID, nodeKey string, by time.Duration) {
	t.Helper()
	ctx := context.Background()
	old := time.Now().Add(-by).UTC().Format(time.RFC3339Nano)
	cfg := resolvedConfigOf(t, st, goalID, nodeKey)
	patched := strings.Replace(cfg, `"held_since":"`+heldSinceOf(t, st, goalID, nodeKey)+`"`, `"held_since":"`+old+`"`, 1)
	if patched == cfg {
		t.Fatalf("held_since backdate failed: %s", cfg)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE goal_id=? AND node_key=?`,
		patched, goalID, nodeKey); err != nil {
		t.Fatalf("backdate held_since: %v", err)
	}
}

// TestBlueprintGateWithoutTimeoutHolds: a gate with no timeout policy is
// valid (timeout optional), holds forever, and the sweeper leaves it alone.
func TestBlueprintGateWithoutTimeoutHolds(t *testing.T) {
	gs, _, st, goalID, _ := parkGateScenario(t, "")
	ctx := context.Background()

	states := blueprintNodeStates(t, st, goalID)
	if states["n_gate"] != "running" {
		t.Fatalf("gate must hold, got %v", states)
	}
	held, err := gs.ListHeldGates(ctx, goalID)
	if err != nil || len(held) != 1 {
		t.Fatalf("want one open card, got %d (%v)", len(held), err)
	}
	if held[0].TimeoutAfter != "" || held[0].TimeoutAction != "" {
		t.Fatalf("gate without timeout must expose no deadline, got %+v", held[0])
	}
	if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 0 {
		t.Fatalf("sweep without timeout want 0, got %d (%v)", n, err)
	}
	if cfg := resolvedConfigOf(t, st, goalID, "n_gate"); strings.Contains(cfg, "parked_at") {
		t.Fatalf("no deadline must not park, got %s", cfg)
	}
}

// TestBlueprintGateTimeoutWithoutActionParks: the default timeout behavior is
// PARK — the deadline stamps parked_at once, never writes a decision, and the
// gate stays open for the human.
func TestBlueprintGateTimeoutWithoutActionParks(t *testing.T) {
	gs, _, st, goalID, _ := parkGateScenario(t, `{"after":"24h"}`)
	ctx := context.Background()

	backdateHeldSince(t, st, goalID, "n_gate", 25*time.Hour)
	if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 0 {
		t.Fatalf("park sweep must not resolve, got %d (%v)", n, err)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"parked_at"`) {
		t.Fatalf("timed-out gate must stamp parked_at, got %s", cfg)
	}
	if strings.Contains(cfg, `"decided_by"`) || strings.Contains(cfg, `"action":"reject"`) {
		t.Fatalf("park must never decide, got %s", cfg)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_gate"] != "running" {
		t.Fatalf("parked gate must stay held, got %v", states)
	}
	var decisions int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gate_decision WHERE goal_id=?`, goalID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Fatalf("park must not write gate_decision, got %d", decisions)
	}
	// Idempotent: a second sweep leaves parked_at untouched.
	first := cfg
	time.Sleep(2 * time.Millisecond)
	if _, err := gs.SweepBlueprintGates(ctx); err != nil {
		t.Fatal(err)
	}
	if again := resolvedConfigOf(t, st, goalID, "n_gate"); again != first {
		t.Fatalf("re-sweep must not re-park: %s vs %s", again, first)
	}
	if held, _ := gs.ListHeldGates(ctx, goalID); len(held) != 1 || held[0].TimeoutAction != "" {
		t.Fatalf("parked card must remain open with no action, got %+v", held)
	}
}

// TestAssertPlanMaterializedTx: the invariant fails for a pinned goal with an
// empty ledger and passes once compileBlueprintTx materializes the spec.
func TestAssertPlanMaterializedTx(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "inv-bp", parkGateJSON(worker, ""))
	g, err := gs.Create(ctx, Goal{
		Title: "inv goal", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := gs.assertPlanMaterializedTx(ctx, tx, g.ID); err == nil {
		_ = tx.Rollback()
		t.Fatalf("pinned goal with blueprint_node=0 must fail the invariant")
	}
	var evs []events.Event
	if err := gs.compileBlueprintTx(ctx, tx, g.ID, &evs); err != nil {
		_ = tx.Rollback()
		t.Fatalf("compile: %v", err)
	}
	if err := gs.assertPlanMaterializedTx(ctx, tx, g.ID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("materialized plan must pass the invariant: %v", err)
	}
	// Release the write lock before opening another transaction (the test
	// store uses a single connection).
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// Infer mode is exempt.
	plain, err := gs.Create(ctx, Goal{Title: "plain", DomainID: domID, AssigneeType: "agent", AssigneeID: owner})
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	tx2, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()
	if err := gs.assertPlanMaterializedTx(ctx, tx2, plain.ID); err != nil {
		t.Fatalf("infer-mode goal must be exempt: %v", err)
	}
}

// TestFinalizationGuardParksUnmaterializedPlan: a pinned goal that reached a
// finished run with an empty ledger must be parked for a human instead of
// being promoted to done (the incident's silent-deviation backstop).
func TestFinalizationGuardParksUnmaterializedPlan(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "guard-bp", parkGateJSON(worker, ""))
	g, err := gs.Create(ctx, Goal{
		Title: "guard goal", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate the incident state: active, pinned, but nothing compiled.
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='active' WHERE id=?`, g.ID); err != nil {
		t.Fatal(err)
	}
	run, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, g.ID))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := rs.Finish(ctx, run.ID, "completed", "done"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := gs.ReconcileGoal(ctx, g.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "review" {
		t.Fatalf("unmaterialized pinned goal must park in review, got %q", got.Status)
	}
	if !strings.Contains(got.ReviewRequest, "蓝图计划未物化") {
		t.Fatalf("park reason missing: %q", got.ReviewRequest)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_log WHERE goal_id=? AND action='plan_unmaterialized'`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want one plan_unmaterialized activity, got %d", n)
	}
}
