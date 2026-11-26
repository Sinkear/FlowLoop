package service

import (
	"context"
	"testing"
)

// A parked gate must only show while its goal can still act on the decision.
// Cancelled/failed goals keep stale running nodes (nothing reconciles them), so
// the global feed has to filter terminal goals itself — otherwise the badge is
// pinned forever. Cancelling also marks the leftover nodes skipped.
func TestPendingGatesSkipsTerminalGoalsAndCancelClearsGate(t *testing.T) {
	ctx := context.Background()
	gs, _, _, st := newTestCluster(t)
	domainID := seedDomain(t, st)

	content := gateJSON("a1")
	mustExec(t, st, `INSERT INTO blueprint (id,name,created_at) VALUES (?,?,?)`,
		"bp-pending", "pending-bp", now())
	mustExec(t, st, `INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,created_at) VALUES (?,?,?,?,?,?,?)`,
		"bv-pending", "bp-pending", 1, content, "hash", "published", now())

	addGoal := func(id, status, held string) {
		t.Helper()
		mustExec(t, st, `INSERT INTO goal (id,title,domain_id,status,assignee_type,created_at,blueprint_version_id) VALUES (?,?,?,?,'agent',?,?)`,
			id, "goal-"+id, domainID, status, now(), "bv-pending")
		mustExec(t, st, `INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?)`,
			"bn-"+id, id, "n_gate", "gate", "running",
			`{"held_since":"`+held+`","timeout_after":"24h"}`, now())
	}
	addGoal("g-active", "active", "2026-11-26T10:00:00+08:00")
	addGoal("g-review", "review", "2026-11-26T11:00:00+08:00")
	addGoal("g-cancelled", "cancelled", "2026-11-26T09:00:00+08:00")
	addGoal("g-failed", "failed", "2026-11-26T09:30:00+08:00")

	gates, err := gs.ListPendingGates(ctx)
	if err != nil {
		t.Fatalf("list pending gates: %v", err)
	}
	if len(gates) != 2 {
		t.Fatalf("want 2 open gates (active/review), got %d: %+v", len(gates), gates)
	}
	// Oldest held_since first.
	if gates[0].GoalID != "g-active" || gates[1].GoalID != "g-review" {
		t.Fatalf("unexpected order: %s then %s", gates[0].GoalID, gates[1].GoalID)
	}
	if gates[0].Title != "human sign-off" || len(gates[0].Actions) != 2 {
		t.Fatalf("spec gate not rendered: %+v", gates[0])
	}
	if gates[0].HeldSince == "" {
		t.Fatalf("gate held_since missing: %+v", gates[0])
	}

	if _, err := gs.Cancel(ctx, "g-active"); err != nil {
		t.Fatalf("cancel goal: %v", err)
	}
	var state string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT state FROM blueprint_node WHERE goal_id=?`, "g-active").Scan(&state); err != nil {
		t.Fatalf("read node state: %v", err)
	}
	if state != "skipped" {
		t.Fatalf("cancel must skip parked nodes, got %q", state)
	}
	gates, err = gs.ListPendingGates(ctx)
	if err != nil {
		t.Fatalf("list pending gates after cancel: %v", err)
	}
	if len(gates) != 1 || gates[0].GoalID != "g-review" {
		t.Fatalf("cancelled goal still in feed: %+v", gates)
	}
}
