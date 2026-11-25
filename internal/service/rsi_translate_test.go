package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// Phase D expansion tests: every analyzer finding category has a
// deterministic translator, each draft is legal per V1–V6 (proved by
// approving it), and the shared rails (effect dedup, human gate) hold.

// translateFixture creates+publishes a blueprint from content, wires a pinned
// train-mode goal and activates it (which compiles blueprint_node rows), then
// returns everything a translator test needs. "agent:WORKER" is substituted
// with a freshly seeded agent (also "agent:WORKER2" when content uses it).
func translateFixture(t *testing.T, content string) (gs *GoalService, rs *RunService, bs *BlueprintService, rsi *RSIService, st *store.Store, goalID, versionID, bpID, workerID string) {
	t.Helper()
	ctx := context.Background()
	gs, rs, bs, rsi, st = propCluster(t)
	dom := seedDomain(t, st)
	workerID = seedAgent(t, st, "tr-"+newID()[:8])
	worker2 := seedAgent(t, st, "tr2-"+newID()[:8])
	// A squad lets a split fixture exercise the spawn fan-out path: compile
	// only attaches a team squad (and therefore allows non-planner children)
	// for squad:/capability: assignees.
	sq, err := NewSquadService(st, events.NewBus()).Create(ctx, Squad{
		Name: "tr-squad-" + newID()[:8], Description: "translator fixture", LeaderID: workerID,
	})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	content = strings.ReplaceAll(content, "squad:WORKER", "squad:"+sq.ID)
	content = strings.ReplaceAll(content, "agent:WORKER2", "agent:"+worker2)
	content = strings.ReplaceAll(content, "agent:WORKER", "agent:"+workerID)
	bp, err := bs.Create(ctx, "translate-"+newID()[:8], "desc", content, "human", "")
	if err != nil {
		t.Fatalf("create blueprint: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("blueprint versions: %v (n=%d)", err, len(versions))
	}
	v, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish blueprint: %v", err)
	}
	g, err := gs.Create(ctx, Goal{
		Title: "translate goal " + newID()[:6], AssigneeType: "agent", AssigneeID: workerID,
		Status: "backlog", DomainID: dom, Category: "hootl", ExecutionMode: "train",
		BlueprintVersionID: v.ID, SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate goal (compile): %v", err)
	}
	return gs, rs, bs, rsi, st, g.ID, v.ID, bp.ID, workerID
}

// sgOf maps spec node key → compiled sub_goal id for a goal.
func sgOf(t *testing.T, st *store.Store, goalID string) map[string]string {
	t.Helper()
	rows, err := st.DB().QueryContext(context.Background(),
		`SELECT node_key, sub_goal_id FROM blueprint_node WHERE goal_id=? AND COALESCE(sub_goal_id,'')!=''`, goalID)
	if err != nil {
		t.Fatalf("blueprint_node: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, sg string
		if err := rows.Scan(&key, &sg); err != nil {
			t.Fatalf("scan blueprint_node: %v", err)
		}
		out[key] = sg
	}
	return out
}

// graphFor builds a synthetic execution graph: one agent node per spec key,
// explicit depends_on edges in the given chain order, critical path = keys.
func graphFor(t *testing.T, st *store.Store, goalID string, keys []string, durations map[string]int64) *WorkflowGraph {
	t.Helper()
	sg := sgOf(t, st, goalID)
	g := &WorkflowGraph{GoalID: goalID, Metrics: map[string]any{}}
	for _, key := range keys {
		id := sg[key]
		if id == "" {
			t.Fatalf("spec node %q not compiled", key)
		}
		g.Nodes = append(g.Nodes, WorkflowNode{Key: "sg:" + id, Kind: "agent", SubGoalID: id, DurationMS: durations[key], EndedAtMS: 1})
		g.Critical = append(g.Critical, "sg:"+id)
	}
	for i := 0; i+1 < len(keys); i++ {
		g.Edges = append(g.Edges, WorkflowEdge{From: "sg:" + sg[keys[i]], To: "sg:" + sg[keys[i+1]], Kind: "depends_on", Source: "explicit"})
	}
	var total int64
	for _, d := range durations {
		total += d
	}
	g.Metrics["work_sum_ms"] = total
	g.Metrics["wall_ms"] = total
	g.Metrics["critical_path_ms"] = total
	g.Metrics["agent_busy_ms"] = total
	g.Metrics["agent_busy_share"] = 1.0
	g.Metrics["idle_gap_share"] = 0.0
	return g
}

func draftFor(t *testing.T, rsi *RSIService, bs *BlueprintService, goalID, bpID string, g *WorkflowGraph, categories ...string) []BlueprintProposal {
	t.Helper()
	findings := make([]RsiFinding, 0, len(categories))
	for _, c := range categories {
		findings = append(findings, RsiFinding{ID: newID(), Category: c, Title: c})
	}
	rsi.draftAutoProposals(context.Background(), goalID, g, findings)
	props, err := bs.ListProposals(context.Background(), bpID)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	return props
}

// approveLegal proves the drafted patch set passes the full V1–V6 validator
// (ApproveProposal re-validates and opens a new DRAFT, never publishes).
func approveLegal(t *testing.T, bs *BlueprintService, p *BlueprintProposal) {
	t.Helper()
	decided, version, err := bs.ApproveProposal(context.Background(), p.ID, "human", "tester")
	if err != nil {
		t.Fatalf("approve %q (patch set must be V1–V6 legal): %v", p.Title, err)
	}
	if decided.Status != "approved" || version == nil || version.Status != "draft" {
		t.Fatalf("approve %q: status=%s version=%v (want approved + new draft)", p.Title, decided.Status, version)
	}
}

const plainTaskEndJSON = `{
  "name": "one-task",
  "description": "single task",
  "nodes": [
    {"id": "t1", "type": "task", "title": "only step",
     "task": {"assignee": "agent:WORKER", "brief": "do the thing"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [{"source": "t1", "source_handle": "done", "target": "end_ok"}]
}`

// splitFixtureJSON is the split_candidate fixture: heavy n1/n2 carry a team
// (squad:) assignee because only team-assigned nodes can fan out.
const twoTaskEndJSON = `{
  "name": "two-tasks",
  "description": "two tasks with verification",
  "nodes": [
    {"id": "t1", "type": "task", "title": "first step",
     "task": {"assignee": "agent:WORKER", "brief": "one"}},
    {"id": "t2", "type": "task", "title": "second step",
     "task": {"assignee": "agent:WORKER", "brief": "two"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "t2"},
    {"source": "t2", "source_handle": "done", "target": "end_ok"}
  ]
}`

const splitFixtureJSON = `{
  "name": "split",
  "description": "split fixture",
  "nodes": [
    {"id": "n1", "type": "task", "title": "heavy one", "task": {"assignee": "squad:WORKER", "brief": "one"}},
    {"id": "n2", "type": "task", "title": "heavy two", "task": {"assignee": "squad:WORKER", "brief": "two"}},
    {"id": "n3", "type": "task", "title": "light", "task": {"assignee": "agent:WORKER", "brief": "three"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n1", "source_handle": "done", "target": "n2"},
    {"source": "n2", "source_handle": "done", "target": "n3"},
    {"source": "n3", "source_handle": "done", "target": "end_ok"}
  ]
}`

// Test translators: split, merge, human_wait, wrong_assignee, brief hardening,
// consult, ping_pong, plan_deviation — each drafts one proposal that is legal.
func TestFindingTranslatorsProduceLegalProposals(t *testing.T) {
	// split_candidate: two heavy critical-path tasks → spawn conversion.
	t.Run("split_candidate", func(t *testing.T) {
		content := splitFixtureJSON
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
		g := graphFor(t, st, goalID, []string{"n1", "n2", "n3"}, map[string]int64{"n1": 60000, "n2": 50000})
		g.Metrics["critical_path_ms"] = int64(110000)
		props := draftFor(t, rsi, bs, goalID, bpID, g, "split_candidate")
		if len(props) != 1 {
			t.Fatalf("split proposals=%d, want 1", len(props))
		}
		p := props[0]
		if !strings.Contains(p.Title, "拆成并行子任务") || !strings.Contains(p.Title, "n1") {
			t.Fatalf("split title %q", p.Title)
		}
		ops := map[string]int{}
		for _, patch := range p.Patches {
			ops[patch.Op]++
			if patch.Op == "add_node" && patch.Node.Type != "spawn" {
				t.Fatalf("split add_node %s type=%s, want spawn", patch.Node.ID, patch.Node.Type)
			}
		}
		if ops["delete_node"] != 1 || ops["add_node"] != 1 || ops["add_edge"] < 1 {
			t.Fatalf("split patch ops=%v", ops)
		}
		approveLegal(t, bs, &p)
	})

	// An agent:-assigned heavy node is skipped: its spawn children can only be
	// claimed by that same agent, and per-agent single-flight serializes them.
	t.Run("split_candidate_agent_assignee_skipped", func(t *testing.T) {
		content := strings.ReplaceAll(splitFixtureJSON, "squad:WORKER", "agent:WORKER")
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
		g := graphFor(t, st, goalID, []string{"n1", "n2", "n3"}, map[string]int64{"n1": 60000, "n2": 50000})
		g.Metrics["critical_path_ms"] = int64(110000)
		if props := draftFor(t, rsi, bs, goalID, bpID, g, "split_candidate"); len(props) != 0 {
			t.Fatalf("agent:-assigned split drafted %d proposals, want 0", len(props))
		}
	})

	// merge_candidate: three parallel leaves → merge the two smallest into the keeper.
	t.Run("merge_candidate", func(t *testing.T) {
		content := `{
  "name": "merge",
  "description": "merge fixture",
  "nodes": [
    {"id": "r1", "type": "task", "title": "root one", "task": {"assignee": "agent:WORKER", "brief": "r1"}},
    {"id": "l1", "type": "task", "title": "leaf one", "task": {"assignee": "agent:WORKER", "brief": "l1"}},
    {"id": "r2", "type": "task", "title": "root two", "task": {"assignee": "agent:WORKER", "brief": "r2"}},
    {"id": "l2", "type": "task", "title": "leaf two", "task": {"assignee": "agent:WORKER", "brief": "l2"}},
    {"id": "r3", "type": "task", "title": "root three", "task": {"assignee": "agent:WORKER", "brief": "r3"}},
    {"id": "l3", "type": "task", "title": "leaf three", "task": {"assignee": "agent:WORKER", "brief": "l3"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "r1", "source_handle": "done", "target": "l1"},
    {"source": "l1", "source_handle": "done", "target": "end_ok"},
    {"source": "r2", "source_handle": "done", "target": "l2"},
    {"source": "l2", "source_handle": "done", "target": "end_ok"},
    {"source": "r3", "source_handle": "done", "target": "l3"},
    {"source": "l3", "source_handle": "done", "target": "end_ok"}
  ]
}`
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
		g := &WorkflowGraph{GoalID: goalID, Metrics: map[string]any{}}
		sg := sgOf(t, st, goalID)
		for _, key := range []string{"r1", "l1", "r2", "l2", "r3", "l3"} {
			g.Nodes = append(g.Nodes, WorkflowNode{Key: "sg:" + sg[key], Kind: "agent", SubGoalID: sg[key], EndedAtMS: 1})
		}
		g.Nodes[1].DurationMS = 30000 // l1 keeper
		g.Nodes[3].DurationMS = 5000  // l2 victim
		g.Nodes[5].DurationMS = 3000  // l3 victim
		g.Edges = []WorkflowEdge{
			{From: "sg:" + sg["r1"], To: "sg:" + sg["l1"], Kind: "depends_on", Source: "explicit"},
			{From: "sg:" + sg["r2"], To: "sg:" + sg["l2"], Kind: "depends_on", Source: "explicit"},
			{From: "sg:" + sg["r3"], To: "sg:" + sg["l3"], Kind: "depends_on", Source: "explicit"},
		}
		g.Metrics["wall_ms"] = int64(1000000)
		props := draftFor(t, rsi, bs, goalID, bpID, g, "merge_candidate")
		if len(props) != 1 {
			t.Fatalf("merge proposals=%d, want 1", len(props))
		}
		p := props[0]
		if !strings.Contains(p.Title, "合并") || !strings.Contains(p.Title, "l1") {
			t.Fatalf("merge title %q", p.Title)
		}
		ops := map[string]int{}
		for _, patch := range p.Patches {
			ops[patch.Op]++
		}
		if ops["delete_node"] != 2 || ops["update_node"] != 1 || ops["add_edge"] != 2 {
			t.Fatalf("merge patch ops=%v", ops)
		}
		approveLegal(t, bs, &p)
	})

	// merge_candidate must not move another assignee's work into the keeper:
	// the victims' briefs would be executed by the keeper's agent.
	t.Run("merge_candidate_cross_assignee_skipped", func(t *testing.T) {
		content := `{
  "name": "merge-mixed",
  "description": "merge fixture with different assignees",
  "nodes": [
    {"id": "r1", "type": "task", "title": "root one", "task": {"assignee": "agent:WORKER", "brief": "r1"}},
    {"id": "l1", "type": "task", "title": "leaf one", "task": {"assignee": "agent:WORKER", "brief": "l1"}},
    {"id": "r2", "type": "task", "title": "root two", "task": {"assignee": "agent:WORKER", "brief": "r2"}},
    {"id": "l2", "type": "task", "title": "leaf two", "task": {"assignee": "agent:WORKER2", "brief": "l2"}},
    {"id": "r3", "type": "task", "title": "root three", "task": {"assignee": "agent:WORKER", "brief": "r3"}},
    {"id": "l3", "type": "task", "title": "leaf three", "task": {"assignee": "agent:WORKER2", "brief": "l3"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "r1", "source_handle": "done", "target": "l1"},
    {"source": "l1", "source_handle": "done", "target": "end_ok"},
    {"source": "r2", "source_handle": "done", "target": "l2"},
    {"source": "l2", "source_handle": "done", "target": "end_ok"},
    {"source": "r3", "source_handle": "done", "target": "l3"},
    {"source": "l3", "source_handle": "done", "target": "end_ok"}
  ]
}`
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
		g := &WorkflowGraph{GoalID: goalID, Metrics: map[string]any{}}
		sg := sgOf(t, st, goalID)
		for _, key := range []string{"r1", "l1", "r2", "l2", "r3", "l3"} {
			g.Nodes = append(g.Nodes, WorkflowNode{Key: "sg:" + sg[key], Kind: "agent", SubGoalID: sg[key], EndedAtMS: 1})
		}
		g.Nodes[1].DurationMS = 30000 // l1 keeper
		g.Nodes[3].DurationMS = 5000
		g.Nodes[5].DurationMS = 3000
		g.Edges = []WorkflowEdge{
			{From: "sg:" + sg["r1"], To: "sg:" + sg["l1"], Kind: "depends_on", Source: "explicit"},
			{From: "sg:" + sg["r2"], To: "sg:" + sg["l2"], Kind: "depends_on", Source: "explicit"},
			{From: "sg:" + sg["r3"], To: "sg:" + sg["l3"], Kind: "depends_on", Source: "explicit"},
		}
		g.Metrics["wall_ms"] = int64(1000000)
		if props := draftFor(t, rsi, bs, goalID, bpID, g, "merge_candidate"); len(props) != 0 {
			t.Fatalf("merge across assignees drafted %d proposals, want 0", len(props))
		}
	})

	// human_wait: evidence pack inserted before the success end.
	t.Run("human_wait", func(t *testing.T) {
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, plainTaskEndJSON)
		g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 30000})
		g.Metrics["human_wait_share"] = 0.5
		g.Metrics["human_wait_ms"] = int64(600000)
		props := draftFor(t, rsi, bs, goalID, bpID, g, "human_wait")
		if len(props) != 1 {
			t.Fatalf("human_wait proposals=%d, want 1", len(props))
		}
		p := props[0]
		if !strings.Contains(p.Title, "人工审核证据包") || p.ExpectedMetric != "human_wait_ms" {
			t.Fatalf("human_wait proposal %q metric=%s", p.Title, p.ExpectedMetric)
		}
		var packID string
		for _, patch := range p.Patches {
			if patch.Op == "add_node" && patch.Node.Type == "task" {
				packID = patch.Node.ID
			}
		}
		if packID == "" {
			t.Fatalf("human_wait: no pack task added")
		}
		approveLegal(t, bs, &p)
	})

	// human_wait must not stack a second evidence pack across analyses.
	t.Run("human_wait_idempotent", func(t *testing.T) {
		content := `{
  "name": "packed",
  "description": "already packed",
  "nodes": [
    {"id": "t1", "type": "task", "title": "work", "task": {"assignee": "agent:WORKER", "brief": "do"}},
    {"id": "rsi_review_pack", "type": "task", "title": "人工审核证据包", "task": {"assignee": "agent:WORKER", "brief": "evidence"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "rsi_review_pack"},
    {"source": "rsi_review_pack", "source_handle": "done", "target": "end_ok"}
  ]
}`
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
		g := graphFor(t, st, goalID, []string{"t1", "rsi_review_pack"}, map[string]int64{"t1": 30000})
		g.Metrics["human_wait_share"] = 0.5
		g.Metrics["human_wait_ms"] = int64(600000)
		if props := draftFor(t, rsi, bs, goalID, bpID, g, "human_wait"); len(props) != 0 {
			t.Fatalf("human_wait drafted %d proposals against an already-packed spec, want 0", len(props))
		}
	})

	// wrong_assignee: move work from the busiest lane to the idlest.
	t.Run("wrong_assignee", func(t *testing.T) {
		content := `{
  "name": "lanes",
  "description": "lane fixture",
  "nodes": [
    {"id": "a", "type": "task", "title": "busy work", "task": {"assignee": "agent:WORKER", "brief": "a"}},
    {"id": "b", "type": "task", "title": "idle work", "task": {"assignee": "agent:WORKER2", "brief": "b"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "a", "source_handle": "done", "target": "b"},
    {"source": "b", "source_handle": "done", "target": "end_ok"}
  ]
}`
		_, _, bs, rsi, st, goalID, _, bpID, worker := translateFixture(t, content)
		var worker2 string
		_ = st.DB().QueryRowContext(context.Background(),
			`SELECT assignee_id FROM sub_goal WHERE goal_id=? AND title='idle work'`, goalID).Scan(&worker2)
		worker2 = strings.TrimPrefix(worker2, "agent:")
		g := graphFor(t, st, goalID, []string{"a", "b"}, map[string]int64{"a": 90000, "b": 10000})
		g.Metrics["lanes"] = map[string]int64{worker: 900000, worker2: 10000}
		props := draftFor(t, rsi, bs, goalID, bpID, g, "wrong_assignee")
		if len(props) != 1 {
			t.Fatalf("wrong_assignee proposals=%d, want 1", len(props))
		}
		p := props[0]
		if !strings.Contains(p.Title, "改派") {
			t.Fatalf("wrong_assignee title %q", p.Title)
		}
		approveLegal(t, bs, &p)
	})

	// brief hardening categories: update_node only, idempotent marker.
	for _, cat := range []string{"brief_quality", "retry_amplification"} {
		t.Run(cat, func(t *testing.T) {
			content := plainTaskEndJSON
			if cat == "brief_quality" {
				content = twoTaskEndJSON
			}
			_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, content)
			sg := sgOf(t, st, goalID)
			keys := []string{"t1"}
			if cat == "brief_quality" {
				keys = []string{"t1", "t2"}
			}
			g := graphFor(t, st, goalID, keys, map[string]int64{"t1": 30000, "t2": 30000})
			if cat == "brief_quality" {
				// >=2 affected sub_goals and >=3 rejected verification rounds.
				g.Nodes = append(g.Nodes, WorkflowNode{
					Key: "v:t1", Kind: "verify", SubGoalID: sg["t1"], Reworks: 2, Attempt: 3,
				}, WorkflowNode{
					Key: "v:t2", Kind: "verify", SubGoalID: sg["t2"], Reworks: 1, Attempt: 2,
				})
			} else {
				g.Nodes[0].Attempt = 3
			}
			props := draftFor(t, rsi, bs, goalID, bpID, g, cat)
			if len(props) != 1 {
				t.Fatalf("%s proposals=%d, want 1", cat, len(props))
			}
			p := props[0]
			if len(p.Patches) == 0 {
				t.Fatalf("%s: no patches", cat)
			}
			for _, patch := range p.Patches {
				if patch.Op != "update_node" {
					t.Fatalf("%s patch ops=%v, want update_node only", cat, patch.Op)
				}
			}
			approveLegal(t, bs, &p)
		})
	}

	// consult_repeat: derive the task from a consult_request joined to its run.
	t.Run("consult_repeat", func(t *testing.T) {
		_, _, bs, rsi, st, goalID, _, bpID, worker := translateFixture(t, plainTaskEndJSON)
		sg := sgOf(t, st, goalID)
		runID := newID()
		if _, err := st.DB().ExecContext(context.Background(),
			`INSERT INTO run (id,goal_id,agent_id,queued_at,created_at,sub_goal_id,role)
			 VALUES (?,?,?,?,?,?, 'subgoal')`, runID, goalID, worker, now(), now(), sg["t1"]); err != nil {
			t.Fatalf("insert run: %v", err)
		}
		for i := 0; i < ruleConsultRepeat; i++ {
			if _, err := st.DB().ExecContext(context.Background(),
				`INSERT INTO consult_request (id,goal_id,requester_agent_id,requester_run_id,target_agent_id,created_at)
				 VALUES (?,?,?,?,?,?)`, newID(), goalID, worker, runID, "agent:someone", now()); err != nil {
				t.Fatalf("insert consult_request: %v", err)
			}
		}
		g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 30000})
		props := draftFor(t, rsi, bs, goalID, bpID, g, "consult_repeat")
		if len(props) != 1 || !strings.Contains(props[0].Title, "咨询前移") {
			t.Fatalf("consult_repeat proposals=%d", len(props))
		}
		approveLegal(t, bs, &props[0])
	})

	// ping_pong: 3 consult hops between the same pair → one-shot question list.
	t.Run("ping_pong", func(t *testing.T) {
		_, _, bs, rsi, st, goalID, _, bpID, worker := translateFixture(t, plainTaskEndJSON)
		for i := 0; i < rulePingPongPairs; i++ {
			for _, pair := range [][2]string{{worker, "agent:peer"}, {"agent:peer", worker}} {
				if _, err := st.DB().ExecContext(context.Background(),
					`INSERT INTO consult_request (id,goal_id,requester_agent_id,target_agent_id,created_at)
					 VALUES (?,?,?,?,?)`, newID(), goalID, pair[0], pair[1], now()); err != nil {
					t.Fatalf("insert consult_request: %v", err)
				}
			}
		}
		g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 30000})
		props := draftFor(t, rsi, bs, goalID, bpID, g, "ping_pong")
		if len(props) != 1 || !strings.Contains(props[0].Title, "一次性问题清单") {
			t.Fatalf("ping_pong proposals=%d", len(props))
		}
		approveLegal(t, bs, &props[0])
	})

	// plan_deviation: discipline block on the entry task.
	t.Run("plan_deviation", func(t *testing.T) {
		_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, plainTaskEndJSON)
		g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 30000})
		props := draftFor(t, rsi, bs, goalID, bpID, g, "plan_deviation")
		if len(props) != 1 || !strings.Contains(props[0].Title, "计划节点纪律") {
			t.Fatalf("plan_deviation proposals=%d", len(props))
		}
		approveLegal(t, bs, &props[0])
	})
}

// TestLostParallelismDefersToSerialChain: both findings describe the same
// chain→fork fix, so only the more precise serial_chain draft is produced.
func TestLostParallelismDefersToSerialChain(t *testing.T) {
	_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, chain4JSON(false))
	g := graphFor(t, st, goalID, []string{"t1", "t2", "t3", "t4"}, map[string]int64{"t1": 30000, "t2": 30000, "t3": 30000, "t4": 30000})
	g.Metrics["critical_path_ms"] = int64(120000)
	g.Metrics["parallel_speedup"] = 1.0
	props := draftFor(t, rsi, bs, goalID, bpID, g, "lost_parallelism", "serial_chain")
	if len(props) != 1 {
		t.Fatalf("proposals=%d, want 1 (lost_parallelism must defer to serial_chain)", len(props))
	}
	if !strings.Contains(props[0].Title, "并行化串行链") {
		t.Fatalf("title=%q, want the serial_chain title", props[0].Title)
	}
}

// TestTranslatorRegistryCoversEveryCategory guards the original gap: the
// analyzer had eleven categories while only serial_chain was translated.
func TestTranslatorRegistryCoversEveryCategory(t *testing.T) {
	analyzerCategories := []string{
		"serial_chain", "split_candidate", "lost_parallelism", "merge_candidate",
		"wrong_assignee", "ping_pong", "brief_quality", "consult_repeat",
		"retry_amplification", "human_wait", "plan_deviation",
	}
	if len(findingTranslators) != len(analyzerCategories) {
		t.Fatalf("translators=%d, analyzer categories=%d", len(findingTranslators), len(analyzerCategories))
	}
	for _, cat := range analyzerCategories {
		if findingTranslators[cat] == nil {
			t.Fatalf("category %q has no translator", cat)
		}
	}
}

// TestTranslatorCollisionGuard: two categories that would rewrite the same
// task node (brief_quality + retry_amplification both patch t1's brief) must
// not both draft — the second is skipped by the touched-set guard so the
// approved proposal cannot silently drop the other's block.
func TestTranslatorCollisionGuard(t *testing.T) {
	_, _, bs, rsi, st, goalID, _, bpID, _ := translateFixture(t, plainTaskEndJSON)
	sg := sgOf(t, st, goalID)
	g := graphFor(t, st, goalID, []string{"t1"}, map[string]int64{"t1": 30000})
	g.Nodes[0].Attempt = 3 // retry_amplification signal
	g.Nodes = append(g.Nodes, WorkflowNode{
		Key: "v:t1", Kind: "verify", SubGoalID: sg["t1"], Reworks: 2, Attempt: 3,
	})
	props := draftFor(t, rsi, bs, goalID, bpID, g, "brief_quality", "retry_amplification")
	if len(props) != 1 {
		t.Fatalf("colliding categories drafted %d proposals, want 1 (touched-set guard)", len(props))
	}
	approveLegal(t, bs, &props[0])
}
