package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/store"
)

// ── B-3 fixtures ──────────────────────────────────────────────────────────

// routerJSON builds a routing blueprint: n_seed (task) → n_route (router,
// classes quick|long, fallback long) → one task per branch → one end per
// branch. Two DISTINCT ends let the dead-branch test assert that the dead
// path's end is skipped while the live one settles.
func routerJSON(workerID string) string {
	return strings.ReplaceAll(`{
  "name": "router-bp",
  "description": "B-3 router fixture",
  "nodes": [
    {"id": "n_seed", "type": "task", "title": "prep",
     "task": {"assignee": "agent:WORKER", "brief": "prepare the artifact"}},
    {"id": "n_route", "type": "router", "title": "route by size",
     "router": {"classes": [
        {"id": "quick", "title": "快速路径", "description": "small diff, patch directly"},
        {"id": "long", "title": "长路径", "description": "big diff, spec first"}
      ], "fallback": "long"}},
    {"id": "n_quick", "type": "task", "title": "quick fix",
     "task": {"assignee": "agent:WORKER", "brief": "apply the quick fix"}},
    {"id": "n_long", "type": "task", "title": "long fix",
     "task": {"assignee": "agent:WORKER", "brief": "spec then fix"}},
    {"id": "n_end_q", "type": "end", "title": "fin-q", "end": {"outcome": "success"}},
    {"id": "n_end_l", "type": "end", "title": "fin-l", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n_seed", "source_handle": "done", "target": "n_route"},
    {"source": "n_route", "source_handle": "quick", "target": "n_quick"},
    {"source": "n_route", "source_handle": "long", "target": "n_long"},
    {"source": "n_quick", "source_handle": "done", "target": "n_end_q"},
    {"source": "n_long", "source_handle": "done", "target": "n_end_l"}
  ]
}`, "agent:WORKER", "agent:"+workerID)
}

// gateJSON builds a human-gate blueprint: n_seed (task) → n_gate (approve|
// reject, timeout 24h → reject) → one task per action → one end per action.
func gateJSON(workerID string) string {
	return strings.ReplaceAll(`{
  "name": "gate-bp",
  "description": "B-3 gate fixture",
  "nodes": [
    {"id": "n_seed", "type": "task", "title": "prepare",
     "task": {"assignee": "agent:WORKER", "brief": "prepare the proposal, upstream said {{node.n_seed.output}}"}},
    {"id": "n_gate", "type": "gate", "title": "human sign-off",
     "gate": {"prompt": "检查 {{node.n_seed.output}} 后决定是否放行。",
       "actions": [
         {"id": "approve", "label": "放行"},
         {"id": "reject", "label": "驳回"}
       ],
       "timeout": {"after": "24h", "action": "reject"}}},
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
    {"source": "n_gate", "source_handle": "timeout", "target": "n_no"},
    {"source": "n_ok", "source_handle": "done", "target": "n_end_ok"},
    {"source": "n_no", "source_handle": "done", "target": "n_end_no"}
  ]
}`, "agent:WORKER", "agent:"+workerID)
}

// seedPublishedContent registers + publishes a blueprint from raw content.
func seedPublishedContent(t *testing.T, bs *BlueprintService, name, content string) *BlueprintVersion {
	t.Helper()
	ctx := context.Background()
	bp, err := bs.Create(ctx, name, "", content, "human", "")
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

// subGoalByTitle finds a compiled sub-goal by its (unique in these
// fixtures) title.
func subGoalByTitle(t *testing.T, gs *GoalService, goalID, title string) *SubGoal {
	t.Helper()
	sgs, err := gs.ListSubGoals(context.Background(), goalID)
	if err != nil {
		t.Fatalf("list sub-goals: %v", err)
	}
	for i := range sgs {
		if sgs[i].Title == title {
			return &sgs[i]
		}
	}
	t.Fatalf("sub-goal %q not found among %d compiled", title, len(sgs))
	return nil
}

// resolvedConfigOf reads a blueprint node's resolved_config.
func resolvedConfigOf(t *testing.T, st *store.Store, goalID, nodeKey string) string {
	t.Helper()
	var cfg string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COALESCE(resolved_config,'') FROM blueprint_node WHERE goal_id=? AND node_key=?`,
		goalID, nodeKey).Scan(&cfg); err != nil {
		t.Fatalf("load resolved_config %s: %v", nodeKey, err)
	}
	return cfg
}

// routerSummary is a legal router run report: reasoning + fenced choice.
func routerSummary(classID string) string {
	return "分析了改动规模。\n```json\n{\"class\":\"" + classID + "\"}\n```\n"
}

// ── B-3a: router legal choice prunes the dead branch ─────────────────────

// The router scenarios share one driver: routerScenario drives
// seed → release → decide(summary) and asserts the pruning bookkeeping for
// the chosen class, then finalizes the goal down the live path.
func routerScenario(t *testing.T, summary string, wantChosen string, wantFallback bool) {
	gs, rs, _, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	bs := NewBlueprintService(st, events.NewBus())
	v := seedPublishedContent(t, bs, "router-bp", routerJSON(worker))
	g, err := gs.Create(ctx, Goal{
		Title: "router goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Compile shape: seed running (enqueued), router+branches pending, ends pending.
	states := blueprintNodeStates(t, st, g.ID)
	if states["n_seed"] != "running" || states["n_route"] != "pending" {
		t.Fatalf("post-compile states wrong: %v", states)
	}
	seedSG := subGoalByTitle(t, gs, g.ID, "prep")
	if n := subGoalRunCount(t, st, seedSG.ID); n != 1 {
		t.Fatalf("root task must be enqueued, got %d runs", n)
	}
	// The router holder must exist and be gated on the seed.
	routeSG := subGoalByTitle(t, gs, g.ID, "route by size")
	if len(routeSG.DependsOn) != 1 || routeSG.DependsOn[0] != seedSG.ID {
		t.Fatalf("router holder deps wrong: %+v", routeSG.DependsOn)
	}

	// Seed verifies → the router run releases (a router IS enqueued; only
	// gates hold).
	finishSubGoalRunCompleted(t, rs, st, seedSG.ID)
	if n := subGoalRunCount(t, st, routeSG.ID); n != 1 {
		t.Fatalf("router holder must release into a run, got %d", n)
	}
	states = blueprintNodeStates(t, st, g.ID)
	if states["n_route"] != "running" {
		t.Fatalf("router node must be running after release, got %v", states)
	}

	// The router decides.
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, routeSG.ID), "completed", summary); err != nil {
		t.Fatalf("finish router run: %v", err)
	}

	// Decision recorded + auditable.
	cfg := resolvedConfigOf(t, st, g.ID, "n_route")
	if !strings.Contains(cfg, `"chosen":"`+wantChosen+`"`) {
		t.Fatalf("router decision not snapshotted (want %s): %s", wantChosen, cfg)
	}
	if !strings.Contains(cfg, `"fallback_used":"`+boolJSON(wantFallback)+`"`) {
		t.Fatalf("fallback_used flag wrong in %s", cfg)
	}
	states = blueprintNodeStates(t, st, g.ID)
	if states["n_route"] != "settled" {
		t.Fatalf("router node must settle after the decision, got %v", states)
	}

	// Live branch released, dead branch cancelled + skipped (its end too).
	var deadTitle, liveTitle, deadEnd, liveEnd string
	if wantChosen == "quick" {
		liveTitle, deadTitle, liveEnd, deadEnd = "quick fix", "long fix", "n_end_q", "n_end_l"
	} else {
		liveTitle, deadTitle, liveEnd, deadEnd = "long fix", "quick fix", "n_end_l", "n_end_q"
	}
	deadSG := subGoalByTitle(t, gs, g.ID, deadTitle)
	if deadSG.Status != "cancelled" {
		t.Fatalf("dead branch sub_goal must be cancelled, got %q", deadSG.Status)
	}
	if states[mapKeyForTask(deadTitle)] != "skipped" {
		t.Fatalf("dead branch node must be skipped, got %v", states)
	}
	if states[deadEnd] != "skipped" {
		t.Fatalf("dead branch END must be skipped (only live ends settle), got %v", states)
	}
	liveSG := subGoalByTitle(t, gs, g.ID, liveTitle)
	if liveSG.Status != "running" || subGoalRunCount(t, st, liveSG.ID) != 1 {
		t.Fatalf("live branch must be released into a run, status=%q runs=%d", liveSG.Status, subGoalRunCount(t, st, liveSG.ID))
	}
	if states[liveEnd] != "pending" {
		t.Fatalf("live END must stay pending until the live branch verifies, got %v", states)
	}

	// Live branch completes → guard (blueprint count) reaches zero → goal
	// can finalize.
	finishSubGoalRunCompleted(t, rs, st, liveSG.ID)
	finalRun, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, g.ID))
	if err != nil {
		t.Fatalf("enqueue owner run: %v", err)
	}
	if err := rs.Finish(ctx, finalRun.ID, "completed", "done"); err != nil {
		t.Fatalf("finish owner run: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("goal must finalize once the live branch settles, got %q", got.Status)
	}
	states = blueprintNodeStates(t, st, g.ID)
	if states[liveEnd] != "settled" || states[deadEnd] != "skipped" {
		t.Fatalf("end bookkeeping wrong: live=%q dead=%q", states[liveEnd], states[deadEnd])
	}
}

func TestBlueprintRouterChosesRegisteredClass(t *testing.T) {
	routerScenario(t, routerSummary("quick"), "quick", false)
}

func TestBlueprintRouterFallsBackOnGarbage(t *testing.T) {
	// No fenced block at all → fallback, flagged.
	routerScenario(t, "我觉得应该走长路径，改动太大了。", "long", true)
}

func TestBlueprintRouterFallsBackOnUnregisteredClass(t *testing.T) {
	// A fenced but UNREGISTERED id → fallback, flagged (§4.4: the model
	// can only ever land on the registered set ∪ {fallback}).
	routerScenario(t, routerSummary("middle"), "long", true)
}

// ── B-3b: gate holds, human decides, timeout decides ─────────────────────

// gateScenario drives seed → hold, then hands control to the caller with
// the holder verified=false.
func gateScenario(t *testing.T) (*GoalService, *RunService, *store.Store, string, string) {
	gs, rs, _, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	bs := NewBlueprintService(st, events.NewBus())
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
	seedSG := subGoalByTitle(t, gs, g.ID, "prepare")
	finishSubGoalRunCompleted(t, rs, st, seedSG.ID)

	// The gate must now be HELD: node running, NO run enqueued for its
	// holder, held_since stamped, and the prompt's {{node.*}} resolved.
	gateSG := subGoalByTitle(t, gs, g.ID, "human sign-off")
	if n := subGoalRunCount(t, st, gateSG.ID); n != 0 {
		t.Fatalf("gate holder must NEVER be enqueued, got %d runs", n)
	}
	states := blueprintNodeStates(t, st, g.ID)
	if states["n_gate"] != "running" {
		t.Fatalf("held gate node must be running, got %v", states)
	}
	cfg := resolvedConfigOf(t, st, g.ID, "n_gate")
	if !strings.Contains(cfg, `"held_since"`) {
		t.Fatalf("gate must stamp held_since, got %s", cfg)
	}
	gateSGDesc, err := gs.GetSubGoal(ctx, gateSG.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gateSGDesc.Description, "{{node.") {
		t.Fatalf("gate prompt must resolve {{node.*}} at hold time: %q", gateSGDesc.Description)
	}

	// The guard holds: an owner run cannot finalize past an open gate.
	ownerRun, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, g.ID))
	if err != nil {
		t.Fatalf("enqueue owner run: %v", err)
	}
	if err := rs.Finish(ctx, ownerRun.ID, "completed", "done?"); err != nil {
		t.Fatalf("finish owner run: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == "done" {
		t.Fatalf("goal finalized past an OPEN human gate — guard broken")
	}

	// The decision card is visible.
	held, err := gs.ListHeldGates(ctx, g.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("ListHeldGates want 1 open card, got %d (%v)", len(held), err)
	}
	if held[0].NodeKey != "n_gate" || len(held[0].Actions) != 2 {
		t.Fatalf("held gate card wrong: %+v", held[0])
	}
	return gs, rs, st, g.ID, gateSG.ID
}

func TestBlueprintGateHumanDecisionReleasesChosenBranch(t *testing.T) {
	gs, rs, st, goalID, gateSGID := gateScenario(t)
	ctx := context.Background()

	// An UNREGISTERED action is rejected outright.
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "maybe", "hmm", "human"); err == nil {
		t.Fatalf("unregistered action must be rejected")
	}
	// The decision itself.
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
		t.Fatalf("resolve gate: %v", err)
	}

	// Holder verified + node settled + decision snapshotted.
	gateSG, err := gs.GetSubGoal(ctx, gateSGID)
	if err != nil {
		t.Fatal(err)
	}
	if gateSG.Status != "verified" {
		t.Fatalf("gate holder must verify on decision, got %q", gateSG.Status)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_gate"] != "settled" {
		t.Fatalf("gate node must settle, got %v", states)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"action":"approve"`) || !strings.Contains(cfg, `"decided_by":"human"`) {
		t.Fatalf("gate decision not snapshotted: %s", cfg)
	}

	// Chosen branch released; rejected branch cancelled + skipped (its end too).
	okSG := subGoalByTitle(t, gs, goalID, "ship it")
	if okSG.Status != "running" || subGoalRunCount(t, st, okSG.ID) != 1 {
		t.Fatalf("approved branch must run, status=%q", okSG.Status)
	}
	noSG := subGoalByTitle(t, gs, goalID, "rework")
	if noSG.Status != "cancelled" {
		t.Fatalf("rejected branch must cancel, got %q", noSG.Status)
	}
	if states["n_no"] != "skipped" || states["n_end_no"] != "skipped" {
		t.Fatalf("dead-side bookkeeping wrong: %v", states)
	}

	// Re-deciding is a conflict.
	if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "reject", "changed my mind", "human"); err == nil {
		t.Fatalf("re-deciding a settled gate must fail")
	}

	// Complete the live branch → goal finalizes.
	finishSubGoalRunCompleted(t, rs, st, okSG.ID)
	finalRun, err := rs.EnqueueForGoal(ctx, *mustGoal(t, gs, goalID))
	if err != nil {
		t.Fatalf("enqueue owner run: %v", err)
	}
	if err := rs.Finish(ctx, finalRun.ID, "completed", "done"); err != nil {
		t.Fatalf("finish owner run: %v", err)
	}
	got, err := gs.Get(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("goal must finalize after gate decision + branch completion, got %q", got.Status)
	}
}

func TestBlueprintGateTimeoutSweepDecides(t *testing.T) {
	gs, _, st, goalID, _ := gateScenario(t)
	ctx := context.Background()

	// Backdate the hold past the 24h timeout, then run ONE sweep pass.
	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	// Rewrite held_since in the JSON (field order safe: decode→encode).
	patched := strings.Replace(cfg, `"held_since":"`+heldSinceOf(t, st, goalID, "n_gate")+`"`, `"held_since":"`+old+`"`, 1)
	if patched == cfg {
		t.Fatalf("held_since backdate failed to patch: %s", cfg)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE goal_id=? AND node_key=?`,
		patched, goalID, "n_gate"); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 1 {
		t.Fatalf("sweep want 1 resolution, got %d (%v)", n, err)
	}
	// The timeout action landed with decided_by=system.
	cfg = resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"action":"reject"`) || !strings.Contains(cfg, `"decided_by":"system"`) {
		t.Fatalf("timeout decision not recorded: %s", cfg)
	}
	// Timeout took the REJECT branch: rework runs, shipping is cancelled.
	okSG := subGoalByTitle(t, gs, goalID, "ship it")
	noSG := subGoalByTitle(t, gs, goalID, "rework")
	if okSG.Status != "cancelled" {
		t.Fatalf("timeout must cancel the approve branch, got %q", okSG.Status)
	}
	if noSG.Status != "running" || subGoalRunCount(t, st, noSG.ID) != 1 {
		t.Fatalf("timeout must release the reject branch, status=%q", noSG.Status)
	}
	states := blueprintNodeStates(t, st, goalID)
	if states["n_end_no"] != "pending" || states["n_end_ok"] != "skipped" {
		t.Fatalf("timeout end bookkeeping wrong: %v", states)
	}
}

// heldSinceOf extracts the stamped held_since for the backdate patch.
func heldSinceOf(t *testing.T, st *store.Store, goalID, nodeKey string) string {
	cfg := resolvedConfigOf(t, st, goalID, nodeKey)
	i := strings.Index(cfg, `"held_since":"`)
	if i < 0 {
		t.Fatalf("no held_since in %s", cfg)
	}
	rest := cfg[i+len(`"held_since":"`):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed held_since in %s", cfg)
	}
	return rest[:j]
}

// mapKeyForTask maps a fixture task title to its node key (used only where
// states are asserted by title-derived keys).
func mapKeyForTask(title string) string {
	switch title {
	case "quick fix":
		return "n_quick"
	case "long fix":
		return "n_long"
	case "ship it":
		return "n_ok"
	case "rework":
		return "n_no"
	}
	return title
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
