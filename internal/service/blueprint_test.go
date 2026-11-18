package service

// Phase A acceptance tests (docs/BLUEPRINT-PROPOSAL.md §7 Phase A):
// A1 spec-driven goal cannot finalize done while compiled nodes are still
//    pending/running (the completeness checker — a skipped compile beat or a
//    leaked node structurally blocks finalization);
// A2 Activate is exactly-once: a blueprint compiles its full topology in one
//    transaction (ungated tasks run immediately, gated tasks wait for wake);
//    reactivation after terminal states never re-compiles;
// A3 CreateSubGoal is structurally refused on a blueprint-driven goal;
// A4 the beat-2 release path (wakeDependentsTx) resolves {{node.*}} refs and
//    marks the node running when the gated task's first run is born;
// A5 condition choices prune the graph at beat 1 (skipped tasks never run);
// A6 hash/integrity gate: a drifted pinned version refuses to compile;
// A7 published versions are immutable (draft-editable, published not);
// A8 infer goals (blueprint_version_id='') are untouched by every hook —
//    the four terminal hooks and the guard count are all no-ops.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// bpCluster wires the blueprint service into the standard test cluster.
func bpCluster(t *testing.T) (*GoalService, *RunService, *BlueprintService, *store.Store) {
	t.Helper()
	gs, rs, _, st := newTestCluster(t)
	bs := NewBlueprintService(st, events.NewBus())
	return gs, rs, bs, st
}

// bpJSON builds a small but complete spec: n1 → n2 → end (task,task,end).
// Edges carry explicit source_handle="done" (the task node's port) and every
// node needs a title — V1 rules.
func bpJSON(title, brief2 string) string {
	return `{
  "name": "bp",
  "description": "test blueprint",
  "nodes": [
    {"id": "n1", "type": "task", "title": "step one",
     "task": {"assignee": "agent:WORKER", "brief": "do {{goal.title}} first"}},
    {"id": "n2", "type": "task", "title": "step two",
     "task": {"assignee": "agent:WORKER", "brief": "` + brief2 + `"}},
    {"id": "n9", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n1", "source_handle": "done", "target": "n2"},
    {"source": "n2", "source_handle": "done", "target": "n9"}
  ]
}`
}

// seedPublishedBlueprint registers + publishes a blueprint whose task
// assignees reference the given worker agent id (substituted for WORKER).
func seedPublishedBlueprint(t *testing.T, bs *BlueprintService, workerID, title, brief2 string) *BlueprintVersion {
	t.Helper()
	ctx := context.Background()
	content := strings.ReplaceAll(bpJSON(title, brief2), "agent:WORKER", "agent:"+workerID)
	bp, err := bs.Create(ctx, "bp-"+workerID, "", content, "human", "")
	if err != nil {
		t.Fatalf("create blueprint: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("blueprint versions missing: %v", err)
	}
	v, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish blueprint: %v", err)
	}
	return v
}

func blueprintNodeStates(t *testing.T, st *store.Store, goalID string) map[string]string {
	t.Helper()
	rows, err := st.DB().QueryContext(context.Background(),
		`SELECT node_key, state FROM blueprint_node WHERE goal_id=?`, goalID)
	if err != nil {
		t.Fatalf("scan blueprint nodes: %v", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, s string
		if err := rows.Scan(&k, &s); err != nil {
			t.Fatalf("scan node state: %v", err)
		}
		m[k] = s
	}
	return m
}

// finishSubGoalRunAndAdvance finishes the sub-goal's current run as completed.
// The machine-verify path (no verifier) flips the sub_goal to verified inside
// the same Finish call, which also marks the blueprint node settled.
func finishSubGoalRunCompleted(t *testing.T, rs *RunService, st *store.Store, sgID string) {
	t.Helper()
	if err := rs.Finish(context.Background(), latestSubGoalRunID(t, st, sgID), "completed", "all good"); err != nil {
		t.Fatalf("finish sub-goal run: %v", err)
	}
}

// compileFixture: a published blueprint (worker → worker chain) + a BACKLOG
// goal pinned to it, ready to Activate.
func compileFixture(t *testing.T, gs *GoalService, bs *BlueprintService, st *store.Store, brief2 string) (goalID, versionID, workerID string) {
	t.Helper()
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedBlueprint(t, bs, worker, "demo", brief2)
	g, err := gs.Create(ctx, Goal{
		Title: "bp goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create pinned goal: %v", err)
	}
	return g.ID, v.ID, worker
}

// A1 + part of A2: after Activate the goal compiles; while the second task
// is still gated (pending), finishing the owner's run CANNOT finalize the
// goal — the guard holds on blueprint pending nodes. Completing both tasks
// settles every node and lets the goal finalize.
func TestBlueprintGuardBlocksFinalizationUntilNodesSettle(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, _, _ := compileFixture(t, gs, bs, st, "second step after {{node.n1.output}}")

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n1"] != "running" || states["n2"] != "pending" || states["n9"] != "pending" {
		t.Fatalf("post-compile states wrong: %v", states)
	}

	// The owner run finishes "successfully" — but n2 is still gated/pending.
	ownerRun, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, goalID))
	if err != nil {
		t.Fatalf("enqueue owner run: %v", err)
	}
	if err := rs.Finish(ctx, ownerRun.ID, "completed", "owner done"); err != nil {
		t.Fatalf("finish owner run: %v", err)
	}
	g, err := gs.Get(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status == "done" {
		t.Fatalf("goal finalized while blueprint node n2 still pending — the completeness check is broken")
	}

	// Worker finishes task 1 → its node settles, task 2 releases with its
	// brief resolved (beat 2), then finishes too. Now the goal CAN finalize.
	sgs, err := gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs) != 2 {
		t.Fatalf("expected 2 compiled sub-goals, got %d (%v)", len(sgs), err)
	}
	// Find by depends_on shape: the ungated one first.
	var first, second *SubGoal
	for i := range sgs {
		if len(sgs[i].DependsOn) == 0 {
			first = &sgs[i]
		} else {
			second = &sgs[i]
		}
	}
	if first == nil || second == nil {
		t.Fatalf("compiled topology lost its gating shape: %+v", sgs)
	}
	finishSubGoalRunCompleted(t, rs, st, first.ID)

	secondGot, err := gs.GetSubGoal(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secondGot.Description, "{{node.") {
		t.Fatalf("gated task brief still has unresolved node refs after release (beat 2): %q", secondGot.Description)
	}
	if !strings.Contains(secondGot.Description, "all good") {
		t.Fatalf("beat-2 must substitute the upstream run summary, got %q", secondGot.Description)
	}
	if n := subGoalRunCount(t, st, second.ID); n != 1 {
		t.Fatalf("released gated task must have exactly 1 run, got %d", n)
	}
	finishSubGoalRunCompleted(t, rs, st, second.ID)

	states = blueprintNodeStates(t, st, goalID)
	if states["n1"] != "settled" || states["n2"] != "settled" {
		t.Fatalf("task nodes must be settled after their runs verify, got %v", states)
	}
	// n9 (end) flips LAZILY inside the next guard evaluation — by design
	// (the guard is the single place that observes "all tasks terminal").

	// Final owner reconcile: the guard now passes — the end node flips and
	// the goal finalizes.
	finalRun, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, goalID))
	if err != nil {
		t.Fatalf("enqueue final owner run: %v", err)
	}
	if err := rs.Finish(ctx, finalRun.ID, "completed", "all verified"); err != nil {
		t.Fatalf("finish final owner run: %v", err)
	}
	g, err = gs.Get(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "done" {
		t.Fatalf("goal with all-settled blueprint nodes must finalize to done, got %q", g.Status)
	}
	states = blueprintNodeStates(t, st, goalID)
	if states["n9"] != "settled" {
		t.Fatalf("end node must flip settled at finalization, got %q", states["n9"])
	}
}

func mustGoal(t *testing.T, gs *GoalService, id string) *Goal {
	t.Helper()
	g, err := gs.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("load goal %s: %v", id, err)
	}
	return g
}

// A2: Activate compiles exactly once. A terminal goal re-activated (admin
// path) hits the reopen guard — existing rows mean no re-compile, so the
// sub-goal count does not double.
func TestBlueprintCompileIsExactlyOnce(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, _, _ := compileFixture(t, gs, bs, st, "second step")

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, err := gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs) != 2 {
		t.Fatalf("first compile must produce 2 sub-goals, got %d (%v)", len(sgs), err)
	}

	// Cancel the ungated task: the cascade (failGatedDependentsTx) fails the
	// gated one in the same transaction — so ONE cancel closes both. The
	// blueprint mirror must record BOTH as failed (not pending), otherwise
	// the guard would hold the reopen forever.
	var ungatedID string
	for i := range sgs {
		if len(sgs[i].DependsOn) == 0 {
			ungatedID = sgs[i].ID
		}
	}
	if _, err := gs.CancelSubGoal(ctx, ungatedID); err != nil {
		t.Fatalf("cancel ungated sub-goal: %v", err)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n1"] != "failed" || states["n2"] != "failed" {
		t.Fatalf("cascade must fail both spec nodes (n1/n2), got %v", states)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE goal SET status='backlog' WHERE id=?`, goalID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	sgs2, err := gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs2) != 2 {
		t.Fatalf("reopen must not recompile the blueprint — sub-goal count must stay 2, got %d (%v)", len(sgs2), err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("reopen must not duplicate blueprint_node rows (want 3, got %d)", n)
	}
	_ = rs
}

// A3: ad-hoc sub-goal creation is refused on a blueprint-driven goal.
func TestBlueprintGoalRefusesAdHocSubGoals(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, _, worker := compileFixture(t, gs, bs, st, "second step")
	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	_, err := gs.CreateSubGoal(ctx, goalID, "sneaky", "ad hoc", worker, "", nil, "human", "")
	if err == nil {
		t.Fatalf("CreateSubGoal must be structurally refused on a blueprint-driven goal")
	}
	if !strings.Contains(err.Error(), "blueprint-driven") {
		t.Fatalf("refusal must name the blueprint contract, got: %v", err)
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("refusal must be a validation error, got %T %v", err, err)
	}
	_ = rs
}

// A5: conditions prune at beat 1 — the not-chosen branch's tasks are born
// 'skipped', never get sub_goal rows, and the chosen branch gates correctly.
func TestBlueprintConditionPrunesAtCompile(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	content := `{
  "name": "cond", "description": "",
  "nodes": [
    {"id": "t_root", "type": "task", "title": "root",
     "task": {"assignee": "agent:` + worker + `", "brief": "always runs"}},
    {"id": "c1", "type": "condition", "title": "branch?",
     "condition": {"expr": "goal.category == \"hitl\"", "cases": [{"id": "hot", "test": "goal.category == \"hitl\""}]}},
    {"id": "t_hot", "type": "task", "title": "hot path",
     "task": {"assignee": "agent:` + worker + `", "brief": "only for hitl"}},
    {"id": "t_cold", "type": "task", "title": "cold path",
     "task": {"assignee": "agent:` + worker + `", "brief": "only for hootl"}},
    {"id": "n9", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t_root", "source_handle": "done", "target": "c1"},
    {"source": "c1", "source_handle": "hot", "target": "t_hot"},
    {"source": "c1", "source_handle": "else", "target": "t_cold"},
    {"source": "t_hot", "source_handle": "done", "target": "n9"},
    {"source": "t_cold", "source_handle": "done", "target": "n9"}
  ]
}`
	bp, err := bs.Create(ctx, "cond-bp", "", content, "human", "")
	if err != nil {
		t.Fatalf("create cond blueprint: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %v", err)
	}
	v, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Category hitl → the 'hot' case matches → t_cold must be pruned.
	g, err := gs.Create(ctx, Goal{
		Title: "hot goal", DomainID: domID, Category: "hitl",
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	states := blueprintNodeStates(t, st, g.ID)
	if states["t_cold"] != "skipped" {
		t.Fatalf("cold-branch task must be pruned to skipped, got %q", states["t_cold"])
	}
	if states["t_hot"] != "pending" {
		t.Fatalf("hot-branch task must survive (gated), got %q", states["t_hot"])
	}
	var sgCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sub_goal WHERE goal_id=?`, g.ID).Scan(&sgCount); err != nil {
		t.Fatal(err)
	}
	if sgCount != 2 {
		t.Fatalf("pruned task must never get a sub_goal (want 2: root+hot, got %d)", sgCount)
	}
}

// A6: a drifted pinned version (content_hash mismatch) refuses to compile —
// the activation fails and the goal stays backlog (no partial compile).
func TestBlueprintDriftedVersionRefusesCompile(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, versionID, _ := compileFixture(t, gs, bs, st, "second step")
	// Corrupt the stored content without fixing the hash.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE blueprint_version SET content='{\"name\":\"x\"}' WHERE id=?`, versionID); err != nil {
		t.Fatal(err)
	}
	_, err := gs.Activate(ctx, goalID)
	if err == nil {
		t.Fatalf("activation must refuse to compile a drifted pinned version")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("refusal must name the hash mismatch, got: %v", err)
	}
	g := mustGoal(t, gs, goalID)
	if g.Status != "backlog" {
		t.Fatalf("a refused compile must leave the goal in backlog, got %q", g.Status)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a refused compile must leave zero blueprint_node rows, got %d", n)
	}
}

// A7: draft versions are editable; published versions are immutable.
func TestBlueprintPublishedVersionsAreImmutable(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "imm-worker")
	bp, err := bs.Create(ctx, "imm", "", strings.ReplaceAll(bpJSON("t", "b"), "agent:WORKER", "agent:"+worker), "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %v", err)
	}
	wj := func(brief string) string { return strings.ReplaceAll(bpJSON("t", brief), "agent:WORKER", "agent:"+worker) }
	v := versions[0]
	if _, err := bs.UpdateDraft(ctx, v.ID, wj("b2")); err != nil {
		t.Fatalf("draft must be editable: %v", err)
	}
	pub, err := bs.Publish(ctx, v.ID, "v1")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := bs.UpdateDraft(ctx, pub.ID, wj("b3")); err == nil {
		t.Fatalf("published version must refuse content edits")
	}
	// NewDraft carries the content forward as an editable v2.
	d2, err := bs.NewDraft(ctx, bp.ID, "iterate")
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	if d2.Version != 2 || d2.Status != "draft" {
		t.Fatalf("new draft must be v2/draft, got v%d/%s", d2.Version, d2.Status)
	}
	if !strings.Contains(d2.Content, "b2") {
		t.Fatalf("new draft must carry the published content forward, got %s", d2.Content)
	}
}

// A8: every hook is a no-op for infer goals. A plain infer goal with an
// ad-hoc sub-goal runs the full existing lifecycle unchanged, and no
// blueprint_node rows appear.
func TestInferGoalUnaffectedByBlueprintHooks(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "infer", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.BlueprintVersionID != "" {
		t.Fatalf("infer goal must pin no blueprint, got %q", g.BlueprintVersionID)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "step", "work", b, "", nil, "human", "")
	if err != nil {
		t.Fatalf("infer goal must still allow ad-hoc sub-goals: %v", err)
	}
	finishSubGoalRunCompleted(t, rs, st, sg.ID)
	sgGot, err := gs.GetSubGoal(ctx, sg.ID)
	if err != nil || sgGot.Status != "verified" {
		t.Fatalf("infer sub-goal must verify through the existing path, got %q (%v)", sgGot.Status, err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("infer goal must produce no blueprint_node rows, got %d", n)
	}
	// The guard's pending count is 0 — finalization is driven by the
	// existing pendingSG/pendingChanges conditions only.
	r := enqueueFirst(t, rs, g)
	if err := rs.Finish(ctx, r.ID, "completed", "done"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	gg := mustGoal(t, gs, g.ID)
	if gg.Status != "done" {
		t.Fatalf("infer goal must finalize through the unchanged path, got %q", gg.Status)
	}
}
