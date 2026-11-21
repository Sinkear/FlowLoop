package service

// BUG-3 regression tests: a goal created DIRECTLY as active while pinned to a
// published blueprint used to bypass Activate and therefore never compile its
// spec graph — blueprint_node stayed at 0 rows, which silently starved the RSI
// auto-proposal translator. Create now compiles in the same transaction, so:
//
//	active  + pinned → nodes compiled + compiled beat-1 run events published;
//	backlog + pinned → nothing compiled until Activate (compile stays exactly-once
//	                   on the backlog→active edge);
//	active  unpinned → no blueprint_node rows (infer mode unchanged).

import (
	"context"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// blueprintNodeCount counts the compiled spec ledger rows for a goal.
func blueprintNodeCount(t *testing.T, st *store.Store, goalID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&n); err != nil {
		t.Fatalf("count blueprint nodes: %v", err)
	}
	return n
}

// compiledSubGoalRuns counts the beat-1 runs enqueued for compiled sub-goals.
// Create/Activate publish run:enqueued on the async bus; the durable contract
// is the queued run row, which is what the machine poll (the executor pickup
// path) actually consumes.
func compiledSubGoalRuns(t *testing.T, st *store.Store, goalID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='subgoal'`, goalID).Scan(&n); err != nil {
		t.Fatalf("count compiled sub-goal runs: %v", err)
	}
	return n
}

// subscribeRunEnqueued captures run:enqueued payloads published by the shared
// test cluster's bus. The bus fans out asynchronously, so callers wait on the
// channel with a deadline instead of assuming synchronous delivery.
func subscribeRunEnqueued(gs *GoalService) <-chan events.Event {
	ch := make(chan events.Event, 8)
	gs.bus.Subscribe("run:enqueued", func(_ context.Context, e events.Event) { ch <- e })
	return ch
}

// waitCompiledRunEvent blocks until a run:enqueued event whose payload is a
// compiled sub-goal run (SubGoalID non-empty) arrives, or the deadline fires.
func waitCompiledRunEvent(ch <-chan events.Event, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if r, ok := e.Payload.(Run); ok && r.SubGoalID != "" {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// TestCreateActivePinnedBlueprintCompiles is the BUG-3 regression: creating a
// goal as active with a published blueprint pin must compile the spec graph in
// the creation transaction, exactly like Activate does, and must publish the
// compiled beat-1 run events only after that commit.
func TestCreateActivePinnedBlueprintCompiles(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedBlueprint(t, bs, worker, "demo", "second step")
	runCh := subscribeRunEnqueued(gs)

	g, err := gs.Create(ctx, Goal{
		Title: "bp active", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "active",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create active pinned goal: %v", err)
	}

	// The spec graph committed with the goal: n1 ungated/running, n2 + end pending.
	if n := blueprintNodeCount(t, st, g.ID); n != 3 {
		t.Fatalf("expected 3 compiled blueprint nodes for an active pinned goal, got %d", n)
	}
	states := blueprintNodeStates(t, st, g.ID)
	if states["n1"] != "running" || states["n2"] != "pending" || states["n9"] != "pending" {
		t.Fatalf("compiled node states wrong: %v", states)
	}

	// The compiled beat-1 run is enqueued in the same transaction: one queued
	// sub-goal run exists for the ungated root (n2 stays gated on n1).
	if n := compiledSubGoalRuns(t, st, g.ID); n != 1 {
		t.Fatalf("expected 1 compiled beat-1 sub-goal run for an active pinned goal, got %d", n)
	}
	// The compiled run's event is published after the commit (Activate order),
	// and exactly once: the second-pass enqueue coalesces and emits nothing.
	if !waitCompiledRunEvent(runCh, 3*time.Second) {
		t.Fatalf("compiled beat-1 run event was not published for the active pinned goal")
	}
}

// TestCreateBacklogPinnedBlueprintCompilesOnActivate guards the semantic
// split: backlog creation must NOT compile (no spec runs for an inactive
// goal); Activate owns the compile and does it exactly once.
func TestCreateBacklogPinnedBlueprintCompilesOnActivate(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedBlueprint(t, bs, worker, "demo", "second step")
	runCh := subscribeRunEnqueued(gs)

	g, err := gs.Create(ctx, Goal{
		Title: "bp backlog", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create backlog pinned goal: %v", err)
	}
	if n := blueprintNodeCount(t, st, g.ID); n != 0 {
		t.Fatalf("backlog creation must not compile: got %d blueprint nodes", n)
	}
	if n := compiledSubGoalRuns(t, st, g.ID); n != 0 {
		t.Fatalf("backlog creation must not enqueue compiled runs, got %d", n)
	}

	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if n := blueprintNodeCount(t, st, g.ID); n != 3 {
		t.Fatalf("expected 3 compiled blueprint nodes after Activate, got %d", n)
	}
	if n := compiledSubGoalRuns(t, st, g.ID); n != 1 {
		t.Fatalf("expected 1 compiled beat-1 sub-goal run after Activate, got %d", n)
	}
	// Activate takes the same compile path; the blueprint.go event-capture fix
	// must publish the compiled run there too (not just on Create).
	if !waitCompiledRunEvent(runCh, 3*time.Second) {
		t.Fatalf("compiled beat-1 run event was not published on Activate")
	}
}

// TestCreateActiveInferGoalHasNoBlueprintNodes is the no-regression check:
// an unpinned active goal hits compileBlueprintTx's internal no-op.
func TestCreateActiveInferGoalHasNoBlueprintNodes(t *testing.T) {
	gs, _, _, st := bpCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)

	g, err := gs.Create(ctx, Goal{
		Title: "infer active", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "active",
		Category: "hootl",
	})
	if err != nil {
		t.Fatalf("create active infer goal: %v", err)
	}
	if n := blueprintNodeCount(t, st, g.ID); n != 0 {
		t.Fatalf("infer goal must have no blueprint nodes, got %d", n)
	}
}
