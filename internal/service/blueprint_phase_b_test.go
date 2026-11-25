package service

// Phase B acceptance tests (docs/BLUEPRINT-PROPOSAL.md §11):
// B1 capability:<role-slug> resolves against the goal's own squad vocabulary
//    (least-loaded eligible member, reviewers excluded) — fail-closed when
//    the vocabulary has no match;
// B2 spawn compiles to a planner sub_goal whose completion gate enforces the
//    fenced-JSON child plan: materialized children are real sub_goal rows
//    gated on the spawn, direct successors re-gate on ALL children, and a
//    contract violation walks the bounded retry chain;
// B3 node-level briefing: a compiled sub_goal carries team_squad_id so
//    cross-squad node runs are briefed with THAT squad's roster.

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// bpGoalSquad seeds a squad (leader+members with roles) and a squad-assigned
// backlog goal pinned to the given published version.
func bpGoalSquad(t *testing.T, st *store.Store, gs *GoalService, versionID string, withMembers bool) (squadID string, memberIDs [3]string) {
	t.Helper()
	ctx := context.Background()
	leader := seedAgent(t, st, "sq-leader")
	memberIDs[0] = seedAgent(t, st, "impl-a")
	memberIDs[1] = seedAgent(t, st, "impl-b")
	memberIDs[2] = seedAgent(t, st, "rev-c")
	sqSvc := NewSquadService(st, events.NewBus())
	sq, err := sqSvc.Create(ctx, Squad{Name: "bp-squad", Description: "bp", LeaderID: leader})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	if withMembers {
		if _, err := sqSvc.AddMember(ctx, sq.ID, "agent", memberIDs[0], "implementer"); err != nil {
			t.Fatalf("add impl-a: %v", err)
		}
		if _, err := sqSvc.AddMember(ctx, sq.ID, "agent", memberIDs[1], " Implementer "); err != nil {
			t.Fatalf("add impl-b: %v", err)
		}
		if _, err := sqSvc.AddMember(ctx, sq.ID, "agent", memberIDs[2], "reviewer"); err != nil {
			t.Fatalf("add rev-c: %v", err)
		}
	}
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{
		Title: "cap goal", AssigneeType: "squad", AssigneeID: sq.ID,
		Status: "backlog", DomainID: domID, BlueprintVersionID: versionID,
	})
	if err != nil {
		t.Fatalf("create squad goal: %v", err)
	}
	return g.ID, memberIDs
}

const capJSONTemplate = `{
  "name": "cap-bp",
  "description": "capability blueprint",
  "nodes": [
    {"id": "n1", "type": "task", "title": "do the thing",
     "task": {"assignee": "capability:implementer", "brief": "do it"}},
    {"id": "n9", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n1", "source_handle": "done", "target": "n9"}
  ]
}`

// B1a: capability resolves to the least-loaded eligible member; the reviewer
// role is structurally excluded even though it exists in the vocabulary.
func TestBlueprintCapabilityResolvesLeastLoaded(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	bp, err := bs.Create(ctx, "cap-bp", "", capJSONTemplate, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(ctx, bp.ID)
	if _, err := bs.Publish(ctx, versions[0].ID, "init"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	goalID, members := bpGoalSquad(t, st, gs, versions[0].ID, true)

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, err := gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs) != 1 {
		t.Fatalf("compiled sub-goals = %d (err %v), want 1", len(sgs), err)
	}
	sg := sgs[0]
	if sg.AssigneeID != members[0] && sg.AssigneeID != members[1] {
		t.Fatalf("capability resolved to %q, want one of the implementers %q/%q", sg.AssigneeID, members[0], members[1])
	}
	if sg.AssigneeID == members[2] {
		t.Fatalf("capability resolved to the REVIEWER — reviewers never execute")
	}
	if sg.TeamSquadID == "" {
		t.Fatalf("capability node must carry team_squad_id (the briefing squad, §11.5)")
	}
	// Deterministic tie-break: both implementers idle → earliest member.
	if sg.AssigneeID != members[0] {
		t.Fatalf("tie-break must pick the earliest-created member: got %q, want %q", sg.AssigneeID, members[0])
	}
	// The node run actually runs and settles.
	finishSubGoalRunCompleted(t, rs, st, sg.ID)
	states := blueprintNodeStates(t, st, goalID)
	if states["n1"] != "settled" {
		t.Fatalf("n1 state = %q, want settled", states["n1"])
	}
}

// B1b: zero matches fail-closed — the goal stays in backlog with the
// diagnostic, no partial compile.
func TestBlueprintCapabilityZeroMatchFailsClosed(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	bp, err := bs.Create(ctx, "cap-bp-zero", "", capJSONTemplate, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(ctx, bp.ID)
	if _, err := bs.Publish(ctx, versions[0].ID, "init"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Squad exists but NO member carries the implementer role. Two members
	// carry a DIFFERENT word so the §11.8 alignment surface is assertable:
	// the zero-hit error must list the vocabulary the squad actually has.
	goalID, memberIDs := bpGoalSquad(t, st, gs, versions[0].ID, false)
	var sqID string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT assignee_id FROM goal WHERE id=?`, goalID).Scan(&sqID); err != nil {
		t.Fatalf("find goal squad: %v", err)
	}
	sqSvc := NewSquadService(st, events.NewBus())
	if _, err := sqSvc.AddMember(ctx, sqID, "agent", memberIDs[0], "design-implementer"); err != nil {
		t.Fatalf("seed design member: %v", err)
	}
	if _, err := sqSvc.AddMember(ctx, sqID, "agent", memberIDs[1], "proprietary-implementer"); err != nil {
		t.Fatalf("seed proprietary member: %v", err)
	}

	_, err = gs.Activate(ctx, goalID)
	if err == nil || !strings.Contains(err.Error(), "implementer") {
		t.Fatalf("Activate must fail-closed with the capability diagnostic, got: %v", err)
	}
	// §11.8 alignment surface: the diagnostic lists the squad's real words.
	for _, word := range []string{"design-implementer", "proprietary-implementer", "roles present in this squad"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("zero-hit error must list vocabulary %q, got: %v", word, err)
		}
	}
	g := mustGoal(t, gs, goalID)
	if g.Status != "backlog" {
		t.Fatalf("goal must stay backlog after a failed compile, got %q", g.Status)
	}
	var n int
	st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&n)
	if n != 0 {
		t.Fatalf("failed compile must leave zero blueprint_node rows, got %d", n)
	}
	var sgCount int
	st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sub_goal WHERE goal_id=?`, goalID).Scan(&sgCount)
	if sgCount != 0 {
		t.Fatalf("failed compile must leave zero sub_goal rows, got %d", sgCount)
	}
}

// B1c: capability on an agent-assigned goal cannot resolve (no vocabulary)
// — refused at Activate, goal intact.
func TestBlueprintCapabilityOnAgentGoalRefused(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	bp, err := bs.Create(ctx, "cap-agent-goal", "", capJSONTemplate, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(ctx, bp.ID)
	if _, err := bs.Publish(ctx, versions[0].ID, "init"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{
		Title: "agent goal", AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", DomainID: domID, Category: "hootl", BlueprintVersionID: versions[0].ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err == nil || !strings.Contains(err.Error(), "squad-assigned") {
		t.Fatalf("Activate must refuse capability without a squad, got: %v", err)
	}
	if gg := mustGoal(t, gs, g.ID); gg.Status != "backlog" {
		t.Fatalf("goal must stay backlog, got %q", gg.Status)
	}
}

const spawnJSONTemplate = `{
  "name": "spawn-bp",
  "description": "spawn blueprint",
  "nodes": [
    {"id": "n_plan", "type": "spawn", "title": "拆解规划",
     "spawn": {"assignee": "agent:PLANNER", "brief": "把 {{goal.title}} 拆成子项", "max_children": 3}},
    {"id": "n_after", "type": "task", "title": "整合收尾",
     "task": {"assignee": "agent:PLANNER", "brief": "整合全部子项结果"}},
    {"id": "n9", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n_plan", "source_handle": "done", "target": "n_after"},
    {"source": "n_after", "source_handle": "done", "target": "n9"}
  ]
}`

// bpSpawnFixture: published spawn blueprint + backlog goal pinned to it.
// Returns (goalID, plannerID, version).
func bpSpawnFixture(t *testing.T, gs *GoalService, bs *BlueprintService, st *store.Store) (goalID, plannerID string) {
	t.Helper()
	ctx := context.Background()
	planner := seedAgent(t, st, "planner")
	owner := seedAgent(t, st, "owner")
	content := strings.ReplaceAll(spawnJSONTemplate, "agent:PLANNER", "agent:"+planner)
	bp, err := bs.Create(ctx, "spawn-bp", "", content, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(ctx, bp.ID)
	if _, err := bs.Publish(ctx, versions[0].ID, "init"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{
		Title: "spawn goal", AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", DomainID: domID, Category: "hootl", BlueprintVersionID: versions[0].ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g.ID, planner
}

// plannerSummary is what the planner's run reports.
func plannerSummary(children string) string {
	return "规划如下：\n两项子工作。\n```json\n" + children + "\n```"
}

// B2 full chain: planner completes with a valid plan → children materialize
// (gated on the spawn) → successor re-gates on ALL children → children
// verify → spawn's node settles only after the plan gate passes → successor
// releases and runs → goal finalizes.
func TestBlueprintSpawnMaterializesChildren(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, planner := bpSpawnFixture(t, gs, bs, st)

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	// The spawn compiles to ONE planner sub_goal (ungated, runs immediately);
	// the successor is gated on it.
	sgs, err := gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs) != 2 {
		t.Fatalf("after compile: sub-goals = %d (err %v), want 2 (spawn + gated successor)", len(sgs), err)
	}
	var spawnSG, afterSG string
	for _, sg := range sgs {
		if len(sg.DependsOn) == 0 {
			spawnSG = sg.ID
		} else {
			afterSG = sg.ID
		}
	}
	if spawnSG == "" || afterSG == "" {
		t.Fatalf("expected one ungated spawn + one gated successor, got %+v", sgs)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_plan"] != "running" || states["n_after"] != "pending" {
		t.Fatalf("n_plan=%q n_after=%q, want running/pending", states["n_plan"], states["n_after"])
	}

	// Planner completes with a 2-child plan (both children assigned to the
	// planner itself — a squadless spawn may only re-dispatch to itself).
	kids := `[{"title":"child-a","brief":"做甲","assignee":"` + planner + `"},{"title":"child-b","brief":"做乙","assignee":"` + planner + `"}]`
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, spawnSG), "completed", plannerSummary(kids)); err != nil {
		t.Fatalf("finish planner run: %v", err)
	}
	// Children materialized: spawn(1) + successor(1) + 2 children = 4.
	sgs, err = gs.ListSubGoals(ctx, goalID)
	if err != nil || len(sgs) != 4 {
		t.Fatalf("after materialize: sub-goals = %d (err %v), want 4", len(sgs), err)
	}
	var childA, childB string
	for _, sg := range sgs {
		if sg.ID == spawnSG || sg.ID == afterSG {
			continue
		}
		if sg.Title == "child-a" {
			childA = sg.ID
		}
		if sg.Title == "child-b" {
			childB = sg.ID
		}
	}
	if childA == "" || childB == "" {
		t.Fatalf("children not materialized: %+v", sgs)
	}
	// Successor re-gated on the spawn AND both children.
	after := mustSubGoal(t, gs, afterSG)
	if !containsString(after.DependsOn, spawnSG) || !containsString(after.DependsOn, childA) || !containsString(after.DependsOn, childB) {
		t.Fatalf("successor must re-gate on spawn+all children, got deps %v", after.DependsOn)
	}
	// Spawn sub_goal itself is verified (its own work — the plan — passed).
	if sg := mustSubGoal(t, gs, spawnSG); sg.Status != "verified" {
		t.Fatalf("spawn sub_goal = %q, want verified after a valid plan", sg.Status)
	}
	// Children run (ungated on the spawn — depends_on=[spawn] is satisfied).
	for _, c := range []string{childA, childB} {
		finishSubGoalRunCompleted(t, rs, st, c)
	}
	// Successor now releases (all deps verified), runs, verifies.
	finishSubGoalRunCompleted(t, rs, st, afterSG)

	// Guard: with everything terminal the goal finalizes through the owner
	// run; end nodes settle.
	r := enqueueFirst(t, rs, mustGoal(t, gs, goalID))
	if err := rs.Finish(ctx, r.ID, "completed", "all integrated"); err != nil {
		t.Fatalf("finish owner run: %v", err)
	}
	g := mustGoal(t, gs, goalID)
	if g.Status != "done" {
		t.Fatalf("goal = %q, want done (children are sub_goal rows, the guard counts them)", g.Status)
	}
	states = blueprintNodeStates(t, st, goalID)
	if states["n_plan"] != "settled" || states["n_after"] != "settled" || states["n9"] != "settled" {
		t.Fatalf("final node states = %v, want all settled", states)
	}
}

// B2b: contract violations walk the bounded retry chain — a plan without the
// fenced JSON block keeps the spawn running with a fresh run, and exhausting
// the retries fails the spawn + cascades the gated successor.
func TestBlueprintSpawnContractViolationRetriesThenFails(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, _ := bpSpawnFixture(t, gs, bs, st)

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, _ := gs.ListSubGoals(ctx, goalID)
	var spawnSG, afterSG string
	for _, sg := range sgs {
		if len(sg.DependsOn) == 0 {
			spawnSG = sg.ID
		} else {
			afterSG = sg.ID
		}
	}
	// Violation 1..3: no fenced block each time → retry each time, then fail.
	for i := 1; i <= 3; i++ {
		if err := rs.Finish(ctx, latestSubGoalRunID(t, st, spawnSG), "completed", "我打算直接自己干完"); err != nil {
			t.Fatalf("finish planner run %d: %v", i, err)
		}
		sg := mustSubGoal(t, gs, spawnSG)
		if i < 3 {
			if sg.Status != "running" || sg.ExecutionAttempt != i {
				t.Fatalf("attempt %d: spawn = %q attempt=%d, want running attempt=%d (bounded retry)", i, sg.Status, sg.ExecutionAttempt, i)
			}
		}
	}
	// After the third violation the chain is exhausted: spawn failed, the
	// gated successor cascaded, and the node ledger marks the failure.
	if sg := mustSubGoal(t, gs, spawnSG); sg.Status != "failed" {
		t.Fatalf("spawn = %q, want failed after exhausted retries", sg.Status)
	}
	if sg := mustSubGoal(t, gs, afterSG); sg.Status != "failed" {
		t.Fatalf("gated successor = %q, want failed (cascade)", sg.Status)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_plan"] != "failed" {
		t.Fatalf("n_plan = %q, want failed", states["n_plan"])
	}
	// No children were ever created.
	sgs, _ = gs.ListSubGoals(ctx, goalID)
	if len(sgs) != 2 {
		t.Fatalf("sub-goals = %d, want 2 (no materialization on violation)", len(sgs))
	}
}

// B2c: the fan-out cap is enforced at materialization — a 4-child plan
// against max_children=3 is a contract violation (retry), not a silent trim.
func TestBlueprintSpawnOverCapRefused(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, planner := bpSpawnFixture(t, gs, bs, st)

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, _ := gs.ListSubGoals(ctx, goalID)
	var spawnSG string
	for _, sg := range sgs {
		if len(sg.DependsOn) == 0 {
			spawnSG = sg.ID
		}
	}
	kids := `[` +
		`{"title":"c1","brief":"b","assignee":"` + planner + `"},` +
		`{"title":"c2","brief":"b","assignee":"` + planner + `"},` +
		`{"title":"c3","brief":"b","assignee":"` + planner + `"},` +
		`{"title":"c4","brief":"b","assignee":"` + planner + `"}]`
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, spawnSG), "completed", plannerSummary(kids)); err != nil {
		t.Fatalf("finish planner run: %v", err)
	}
	sg := mustSubGoal(t, gs, spawnSG)
	if sg.Status != "running" || sg.ExecutionAttempt != 1 {
		t.Fatalf("over-cap plan must be a bounded retry, got %q attempt=%d", sg.Status, sg.ExecutionAttempt)
	}
	// No children materialized.
	sgs, _ = gs.ListSubGoals(ctx, goalID)
	if len(sgs) != 2 {
		t.Fatalf("sub-goals = %d, want 2 (over-cap materialization refused)", len(sgs))
	}
}

// B2d: empty plan is legal — zero children, the spawn verifies, the
// successor releases directly.
func TestBlueprintSpawnEmptyPlanLegal(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, _ := bpSpawnFixture(t, gs, bs, st)

	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, _ := gs.ListSubGoals(ctx, goalID)
	var spawnSG, afterSG string
	for _, sg := range sgs {
		if len(sg.DependsOn) == 0 {
			spawnSG = sg.ID
		} else {
			afterSG = sg.ID
		}
	}
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, spawnSG), "completed", plannerSummary(`[]`)); err != nil {
		t.Fatalf("finish planner run: %v", err)
	}
	if sg := mustSubGoal(t, gs, spawnSG); sg.Status != "verified" {
		t.Fatalf("empty plan is legal: spawn = %q, want verified", sg.Status)
	}
	// Successor released (no children to wait for).
	finishSubGoalRunCompleted(t, rs, st, afterSG)
	r := enqueueFirst(t, rs, mustGoal(t, gs, goalID))
	if err := rs.Finish(ctx, r.ID, "completed", "done"); err != nil {
		t.Fatalf("finish owner: %v", err)
	}
	if g := mustGoal(t, gs, goalID); g.Status != "done" {
		t.Fatalf("goal = %q, want done after an empty-plan spawn chain", g.Status)
	}
}

func mustSubGoal(t *testing.T, gs *GoalService, id string) *SubGoal {
	t.Helper()
	sg, err := gs.GetSubGoal(context.Background(), id)
	if err != nil {
		t.Fatalf("get sub-goal %s: %v", id, err)
	}
	return sg
}
