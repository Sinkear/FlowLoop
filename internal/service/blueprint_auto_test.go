package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/store"
)

// autoGateJSON is the B-3 gate fixture WITH a runtime auto rule (and no
// timeout): the platform may decide without a human, but only when the named
// upstream node's sealed output satisfies the rule.
func autoGateJSON(workerID, autoJSON string) string {
	s := `{
  "name": "auto-bp",
  "description": "gate auto fixture",
  "nodes": [
    {"id": "n_seed", "type": "task", "title": "prepare",
     "task": {"assignee": "agent:WORKER", "brief": "prepare the proposal"}},
    {"id": "n_gate", "type": "gate", "title": "sign-off",
     "gate": {"prompt": "放行还是驳回？",
       "actions": [
         {"id": "approve", "label": "放行"},
         {"id": "reject", "label": "驳回"}
       ]AUTO}},
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
	extra := ""
	if autoJSON != "" {
		extra = ",\n       \"auto\": " + autoJSON
	}
	s = strings.ReplaceAll(s, "AUTO", extra)
	return strings.ReplaceAll(s, "agent:WORKER", "agent:"+workerID)
}

// autoGateScenario compiles a gate spec with the given auto rule and finishes
// the seed task with the given output summary, leaving the gate held.
func autoGateScenario(t *testing.T, autoJSON, seedSummary string) (*GoalService, *RunService, *store.Store, string, string) {
	t.Helper()
	gs, rs, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "auto-bp", autoGateJSON(worker, autoJSON))
	g, err := gs.Create(ctx, Goal{
		Title: "auto goal", Description: "d", DomainID: domID,
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
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, seedSG.ID), "completed", seedSummary); err != nil {
		t.Fatalf("finish seed run: %v", err)
	}
	return gs, rs, st, g.ID, seedSG.ID
}

// TestGateAutoMatchesUpstreamOutput: the rule reads the sealed upstream output
// and, on a match, resolves the gate as decided_by=auto — no human, no timeout.
func TestGateAutoMatchesUpstreamOutput(t *testing.T) {
	gs, _, st, goalID, _ := autoGateScenario(t,
		`{"when": "node.n_seed.output contains \"ready to ship\"", "action": "approve"}`,
		"ready to ship — all checks pass")
	ctx := context.Background()

	held, err := gs.ListHeldGates(ctx, goalID)
	if err != nil || len(held) != 1 {
		t.Fatalf("want one held gate before the sweep, got %d (%v)", len(held), err)
	}
	if held[0].AutoWhen == "" || held[0].AutoAction != "approve" {
		t.Fatalf("decision card must expose the auto rule, got %+v", held[0])
	}
	if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 1 {
		t.Fatalf("auto match must resolve one gate, got %d (%v)", n, err)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if !strings.Contains(cfg, `"decided_by":"auto"`) || !strings.Contains(cfg, `"action":"approve"`) {
		t.Fatalf("ledger must record the automatic decision, got %s", cfg)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_gate"] == "running" {
		t.Fatalf("resolved gate must leave the held state, got %v", states)
	}
	// The approved branch woke: the ship task has a run.
	okSG := subGoalByTitle(t, gs, goalID, "ship it")
	if latestSubGoalRunID(t, st, okSG.ID) == "" {
		t.Fatalf("approved branch must be dispatched after the auto decision")
	}
}

// TestGateAutoMissKeepsHolding: a non-matching output changes nothing — the
// gate stays held for the human, and no decision is fabricated.
func TestGateAutoMissKeepsHolding(t *testing.T) {
	gs, _, st, goalID, _ := autoGateScenario(t,
		`{"when": "node.n_seed.output contains \"ready to ship\"", "action": "approve"}`,
		"not ready yet")
	ctx := context.Background()

	if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 0 {
		t.Fatalf("auto miss must not resolve, got %d (%v)", n, err)
	}
	cfg := resolvedConfigOf(t, st, goalID, "n_gate")
	if strings.Contains(cfg, `"decided_by"`) {
		t.Fatalf("auto miss must not write a decision, got %s", cfg)
	}
	if states := blueprintNodeStates(t, st, goalID); states["n_gate"] != "running" {
		t.Fatalf("gate must stay held on a miss, got %v", states)
	}
	// Evidence exists but does not match; the rule stays visible to a human.
	held, _ := gs.ListHeldGates(ctx, goalID)
	if len(held) != 1 || held[0].AutoAction != "approve" {
		t.Fatalf("held card must keep the auto rule, got %+v", held)
	}
}

// TestGateAutoRuleValidation: publication rejects rules that could never be
// evaluated or could never take effect.
func TestGateAutoRuleValidation(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	cases := []struct {
		name, auto, want string
	}{
		{"undeclared action",
			`{"when": "node.n_seed.output contains \"ready\"", "action": "ship"}`,
			"must be a declared action id"},
		{"bad test syntax",
			`{"when": "n_seed is good", "action": "approve"}`,
			"not a runtime node test"},
		{"not upstream",
			`{"when": "node.n_no.output contains \"ready\"", "action": "approve"}`,
			"not a direct upstream node"},
		{"missing when",
			`{"action": "approve"}`,
			"gate.auto.when is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := bs.Create(context.Background(), "v-"+c.name, "", autoGateJSON(worker, c.auto), "human", "u1"); err == nil {
				t.Fatalf("want validation error containing %q", c.want)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
	_ = gs
}
