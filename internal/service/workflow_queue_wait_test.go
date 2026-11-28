package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// queueWaitHarness builds a goal with sub-goals sharing one created_at, so
// every test can reason about dispatch delay (start - ready) in isolation.
type queueWaitHarness struct {
	t     *testing.T
	st    *store.Store
	goal  string
	base  time.Time
	rsi   *RSIService
	subAt time.Time
}

func newQueueWaitHarness(t *testing.T) *queueWaitHarness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "queue-wait.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	nowTime := time.Now().UTC()
	base := nowTime.Add(-2 * time.Hour)
	// Sub-goals and the goal share the chain's start instant: any dispatch
	// delay then comes purely from a run starting after its ready time.
	created := base
	cts := created.Format(time.RFC3339Nano)
	runDT := created.Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(`INSERT INTO runtime (id,name,status,created_at) VALUES ('rt-qw','qw@local','active',?)`, runDT); err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO agent (id,name,runtime_id,created_at) VALUES ('ag-x','Agent X','rt-qw',?)`, runDT); err != nil {
		t.Fatalf("agent: %v", err)
	}
	goalID := newID()
	if _, err := st.DB().Exec(`INSERT INTO goal (id,title,status,created_at) VALUES (?,?,?,?)`,
		goalID, "queue wait fixture", "active", cts); err != nil {
		t.Fatalf("goal: %v", err)
	}
	return &queueWaitHarness{
		t: t, st: st, goal: goalID, base: base, subAt: created,
		rsi: NewRSIService(st, events.NewBus()),
	}
}

func (h *queueWaitHarness) sub(id, deps string) {
	h.t.Helper()
	h.subStatus(id, deps, "verified")
}

func (h *queueWaitHarness) subStatus(id, deps, status string) {
	h.t.Helper()
	cts := h.subAt.UTC().Format(time.RFC3339Nano)
	if _, err := h.st.DB().Exec(`INSERT INTO sub_goal (id,goal_id,title,assignee_id,status,depends_on,created_at)
		VALUES (?,?,?,?,?,?,?)`, id, h.goal, id, "ag-x", status, deps, cts); err != nil {
		h.t.Fatalf("sub_goal %s: %v", id, err)
	}
}

// gateNode links a sub-goal to a blueprint gate holder: gates are settled by
// a human/auto decision and by construction never get a run of their own.
func (h *queueWaitHarness) gateNode(nodeKey, sgID string) {
	h.t.Helper()
	if _, err := h.st.DB().Exec(`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,sub_goal_id,state,created_at)
		VALUES (?,?,?,'gate',?,'running',?)`,
		newID(), h.goal, nodeKey, sgID, h.subAt.UTC().Format(time.RFC3339Nano)); err != nil {
		h.t.Fatalf("blueprint_node %s: %v", nodeKey, err)
	}
}

func (h *queueWaitHarness) run(id, sgid string, start, finish time.Time) {
	h.t.Helper()
	sts, fis := start.UTC().Format(time.RFC3339Nano), finish.UTC().Format(time.RFC3339Nano)
	// queued_at == started_at: the run was ready and dispatched immediately,
	// so only the dependency gate (if any) can delay it.
	if _, err := h.st.DB().Exec(`INSERT INTO run (id,goal_id,sub_goal_id,agent_id,role,status,attempt,queued_at,started_at,finished_at,created_at)
		VALUES (?,?,?,?,?,?,1,?,?,?,?)`, id, h.goal, sgid, "ag-x", "subgoal", "completed", sts, sts, fis, sts); err != nil {
		h.t.Fatalf("run %s: %v", id, err)
	}
}

// runOpen inserts a run that started but has not finished (empty end
// timestamp), i.e. a sub-goal that is still executing when the graph is read.
func (h *queueWaitHarness) runOpen(id, sgid string, start time.Time) {
	h.t.Helper()
	sts := start.UTC().Format(time.RFC3339Nano)
	if _, err := h.st.DB().Exec(`INSERT INTO run (id,goal_id,sub_goal_id,agent_id,role,status,attempt,queued_at,started_at,finished_at,created_at)
		VALUES (?,?,?,?,?,'running',1,?,?,'',?)`, id, h.goal, sgid, "ag-x", "subgoal", sts, sts, sts); err != nil {
		h.t.Fatalf("run %s: %v", id, err)
	}
}

func (h *queueWaitHarness) graph() *WorkflowGraph {
	h.t.Helper()
	g, err := h.rsi.BuildGoalWorkflow(context.Background(), h.goal)
	if err != nil {
		h.t.Fatalf("BuildGoalWorkflow: %v", err)
	}
	return g
}

// gateSettled inserts a blueprint gate holder whose human decision has
// landed: resolved_config carries held_since/decided_at (written by
// markGateHeldTx / ResolveBlueprintGate). A gate has NO run by
// construction — the decision is its execution.
func (h *queueWaitHarness) gateSettled(nodeKey, sgID string, held, decided time.Time) {
	h.t.Helper()
	cfg, err := json.Marshal(map[string]string{
		"held_since": held.UTC().Format(time.RFC3339Nano),
		"decided_at": decided.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		h.t.Fatalf("marshal resolved_config: %v", err)
	}
	if _, err := h.st.DB().Exec(`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,sub_goal_id,state,resolved_config,created_at)
		VALUES (?,?,?,'gate',?,'settled',?,?)`,
		newID(), h.goal, nodeKey, sgID, string(cfg), h.subAt.UTC().Format(time.RFC3339Nano)); err != nil {
		h.t.Fatalf("blueprint_node %s: %v", nodeKey, err)
	}
}

// gateVerdict inserts the verification_result row ResolveBlueprintGate
// writes for every settled gate (passed, untimed, no verifier run).
func (h *queueWaitHarness) gateVerdict(sgID string, decided time.Time) {
	h.t.Helper()
	if _, err := h.st.DB().Exec(`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,verify_ms,status,summary,evidence,created_at)
		VALUES (?,?,?,?,0,'passed','','gate decision',?)`,
		newID(), h.goal, sgID, "", decided.UTC().Format(time.RFC3339Nano)); err != nil {
		h.t.Fatalf("gate verdict %s: %v", sgID, err)
	}
}

func TestQueueWaitLinearChain(t *testing.T) {
	h := newQueueWaitHarness(t)
	const n = 12
	prev := ""
	for i := 0; i < n; i++ {
		id := "sg-" + string(rune('a'+i))
		deps := "[]"
		if prev != "" {
			deps = `["` + prev + `"]`
		}
		h.sub(id, deps)
		start := h.base.Add(time.Duration(i) * 10 * time.Minute)
		h.run("run-"+id, id, start, start.Add(10*time.Minute))
		prev = id
	}

	g := h.graph()
	if got := mi(g.Metrics, "dispatch_wait_ms"); got != 0 {
		t.Fatalf("dispatch_wait_ms = %d, want 0 (queued_at == started_at, no dispatch delay)", got)
	}
	if got := mi(g.Metrics, "queue_wait_ms"); got != 0 {
		t.Fatalf("queue_wait_ms = %d, want 0 (dependency gating must not be counted)", got)
	}
	if got := mi(g.Metrics, "dependency_wait_ms"); got <= 0 {
		t.Fatalf("dependency_wait_ms = %d, want > 0 (chain is gated)", got)
	}
	if q, w := mi(g.Metrics, "queue_wait_ms"), mi(g.Metrics, "wall_ms"); q > w {
		t.Fatalf("queue_wait_ms = %d exceeds wall_ms = %d", q, w)
	}
}

func TestQueueWaitParallelDependencies(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	h.sub("sg-b", "[]")
	h.sub("sg-c", `["sg-a","sg-b"]`)

	h.run("run-a", "sg-a", h.base, h.base.Add(10*time.Minute))
	h.run("run-b", "sg-b", h.base, h.base.Add(10*time.Minute))
	// c is ready at max(a.end, b.end) = base+10; dispatched at base+15.
	h.run("run-c", "sg-c", h.base.Add(15*time.Minute), h.base.Add(25*time.Minute))

	g := h.graph()
	if got := mi(g.Metrics, "queue_wait_ms"); got != int64(5*time.Minute/time.Millisecond) {
		t.Fatalf("queue_wait_ms = %d, want only c's 5min dispatch delay", got)
	}
	if got := mi(g.Metrics, "queue_wait_ms"); got != mi(g.Metrics, "dispatch_wait_ms") {
		t.Fatalf("queue_wait_ms = %d, dispatch_wait_ms = %d, want equal", got, mi(g.Metrics, "dispatch_wait_ms"))
	}
	if q, w := mi(g.Metrics, "queue_wait_ms"), mi(g.Metrics, "wall_ms"); q > w {
		t.Fatalf("queue_wait_ms = %d exceeds wall_ms = %d", q, w)
	}
}

func TestQueueWaitNoDependencies(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	h.run("run-a", "sg-a", h.base.Add(10*time.Minute), h.base.Add(20*time.Minute))

	g := h.graph()
	if got := mi(g.Metrics, "queue_wait_ms"); got != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("queue_wait_ms = %d, want 10min dispatch delay", got)
	}
	if got := mi(g.Metrics, "dependency_wait_ms"); got != 0 {
		t.Fatalf("dependency_wait_ms = %d, want 0 without dependencies", got)
	}
	if v, ok := g.Metrics["queue_wait_capped"]; ok {
		t.Fatalf("queue_wait_capped = %v, want unset without overflow", v)
	}
}

func TestQueueWaitUnrunSubGoal(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	// sg-b was created but never dispatched: still running, no run row.
	h.subStatus("sg-b", "[]", "running")
	h.run("run-a", "sg-a", h.base, h.base.Add(10*time.Minute))

	g := h.graph()
	if got := mi(g.Metrics, "unrun_sub_goals"); got != 1 {
		t.Fatalf("unrun_sub_goals = %d, want 1", got)
	}
	if got := mi(g.Metrics, "queue_wait_ms"); got != 0 {
		t.Fatalf("queue_wait_ms = %d, want 0 (unrun sub-goal must not contribute)", got)
	}
}

func TestQueueWaitVerifiedWithoutRunIsNotUnrun(t *testing.T) {
	h := newQueueWaitHarness(t)
	// Settled without a run of its own (router/gate style settle): the node
	// is done, not "pending dispatch".
	h.sub("sg-a", "[]")

	g := h.graph()
	if got := mi(g.Metrics, "unrun_sub_goals"); got != 0 {
		t.Fatalf("unrun_sub_goals = %d, want 0 (verified sub-goal must not count as unrun)", got)
	}
}

func TestQueueWaitGateHolderIsNotUnrun(t *testing.T) {
	h := newQueueWaitHarness(t)
	// A blueprint gate holder never gets a run at all — the human/auto
	// decision settles it. It must not be reported as an unexecuted sub-goal.
	h.subStatus("sg-gate", "[]", "running")
	h.gateNode("gate-1", "sg-gate")
	h.subStatus("sg-wait", `["sg-gate"]`, "running")

	g := h.graph()
	if got := mi(g.Metrics, "unrun_sub_goals"); got != 1 {
		t.Fatalf("unrun_sub_goals = %d, want 1 (only the gated sg-wait, not the gate holder)", got)
	}
}

func TestQueueWaitInFlightSubGoalWithinSpan(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	// The only run is still in flight (finished_at=''): it contributes no
	// busy window, so wall_ms stays 0. The dispatch delay must still be
	// bounded by the orchestration span (ready → now), never by wall_ms
	// (#11 symptom: queue_wait > wall).
	h.runOpen("run-a", "sg-a", h.base.Add(10*time.Minute))

	g := h.graph()
	wall := mi(g.Metrics, "wall_ms")
	span := mi(g.Metrics, "wall_span_ms")
	q := mi(g.Metrics, "queue_wait_ms")
	if wall != 0 {
		t.Fatalf("wall_ms = %d, want 0 (in-flight run has no busy window)", wall)
	}
	if q != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("queue_wait_ms = %d, want 10min dispatch delay", q)
	}
	if span < q {
		t.Fatalf("wall_span_ms = %d is below queue_wait_ms = %d (span must cover ready→now)", span, q)
	}
	if g.Metrics["queue_wait_capped"] == true {
		t.Fatalf("queue_wait_capped = true, want unset (10min delay fits in the span)")
	}
}

func TestQueueWaitCappedToSpan(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	h.sub("sg-b", "[]")
	// Two independent runs both delayed 20min and overlapping for 10min:
	// summed dispatch wait (40min) exceeds the 30min orchestration span
	// (ready at base → last finish at base+30) and must converge to it.
	h.run("run-a", "sg-a", h.base.Add(20*time.Minute), h.base.Add(30*time.Minute))
	h.run("run-b", "sg-b", h.base.Add(20*time.Minute), h.base.Add(30*time.Minute))

	g := h.graph()
	wall := mi(g.Metrics, "wall_ms")
	span := mi(g.Metrics, "wall_span_ms")
	q := mi(g.Metrics, "queue_wait_ms")
	if span != int64(30*time.Minute/time.Millisecond) {
		t.Fatalf("wall_span_ms = %d, want 30min (ready base → last finish base+30)", span)
	}
	if wall != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("wall_ms = %d, want 10min busy wall", wall)
	}
	if q > span {
		t.Fatalf("queue_wait_ms = %d exceeds wall_span_ms = %d", q, span)
	}
	if q != span {
		t.Fatalf("queue_wait_ms = %d, want converged to wall_span_ms = %d", q, span)
	}
	if g.Metrics["queue_wait_capped"] != true {
		t.Fatalf("queue_wait_capped = %v, want true", g.Metrics["queue_wait_capped"])
	}
	if got := mi(g.Metrics, "queue_wait_overlap_ms"); got != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("queue_wait_overlap_ms = %d, want 10min (40min summed - 30min span)", got)
	}
	if got := mi(g.Metrics, "dispatch_wait_ms"); got != span {
		t.Fatalf("dispatch_wait_ms = %d, want capped to wall_span_ms %d", got, span)
	}
}

// Issue #38: a sub-goal whose dependency is a settled blueprint gate (no run
// of its own) must anchor ready_at to the gate's decided_at — NOT collapse
// back to its own created_at, which billed the whole "upstream serial chain +
// human gate hold" to dispatch_wait_ms (74% of wall on a real goal, with the
// finding advising MORE lanes for what was actually a serial-chain + human
// gate structure). The wait belongs to dependency_wait_ms; the gate is a
// graph node; its verdict counts in verify_rounds.
func TestQueueWaitGateDependencyAnchorsReadyToDecision(t *testing.T) {
	h := newQueueWaitHarness(t)
	m := int64(time.Minute / time.Millisecond)
	// Upstream worker: t0 → t0+10m.
	h.sub("sg-work", "[]")
	h.run("run-work", "sg-work", h.base, h.base.Add(10*time.Minute))
	// Gate: depends on the worker, settled by a human at t0+40m after a
	// 30m hold (held at worker finish t0+10m). No run — by construction.
	h.subStatus("sg-gate", `["sg-work"]`, "verified")
	h.gateSettled("gate-1", "sg-gate", h.base.Add(10*time.Minute), h.base.Add(40*time.Minute))
	h.gateVerdict("sg-gate", h.base.Add(40*time.Minute))
	// Packer: depends on the gate, dispatched the moment it became ready.
	h.sub("sg-pack", `["sg-gate"]`)
	h.run("run-pack", "sg-pack", h.base.Add(40*time.Minute), h.base.Add(50*time.Minute))

	g := h.graph()
	// The 40m upstream wait (10m chain + 30m human hold) must land in
	// dependency_wait_ms, leaving dispatch_wait_ms empty: pack started
	// exactly at ready.
	if got := mi(g.Metrics, "dispatch_wait_ms"); got != 0 {
		t.Fatalf("dispatch_wait_ms = %d, want 0 (ready anchored to gate decided_at; the old code billed the 40m upstream wait here)", got)
	}
	if got := mi(g.Metrics, "dependency_wait_ms"); got != 40*m {
		t.Fatalf("dependency_wait_ms = %d, want 40min (created t0 → gate decision t0+40m)", got)
	}
	// The human hold window is the gate's own metric.
	if got := mi(g.Metrics, "gate_wait_ms"); got != 30*m {
		t.Fatalf("gate_wait_ms = %d, want 30min (held_since → decided_at)", got)
	}
	if got := mi(g.Metrics, "human_wait_ms"); got != 30*m {
		t.Fatalf("human_wait_ms = %d, want 30min (no delivery review rows, gate hold only)", got)
	}
	// Gate is a graph node with a zero-width decision window.
	var gateNode *WorkflowNode
	for i := range g.Nodes {
		if g.Nodes[i].SubGoalID == "sg-gate" && g.Nodes[i].Kind == "gate" {
			gateNode = &g.Nodes[i]
		}
	}
	if gateNode == nil {
		t.Fatalf("gate node (Kind=gate) missing; nodes: %+v", g.Nodes)
	}
	if gateNode.StartedAtMS == 0 || gateNode.StartedAtMS != gateNode.EndedAtMS {
		t.Fatalf("gate node window = [%d,%d], want zero-width at decided_at", gateNode.StartedAtMS, gateNode.EndedAtMS)
	}
	// The explicit gate → dependent edge has both endpoints now (the old
	// graph dropped it, severing the critical path at every human gate).
	var gated bool
	for _, e := range g.Edges {
		if e.From == "sg:sg-gate" && e.To == "sg:sg-pack" && e.Kind == "depends_on" {
			gated = true
		}
	}
	if !gated {
		t.Fatalf("explicit edge sg:sg-gate → sg:sg-pack missing (critical path breaks at the gate)")
	}
	// Critical path chains THROUGH the gate: work 10m + pack 10m = 20m.
	// The pre-fix graph lost the gate segment (cp broke at the gate, the
	// packer was left hanging on its own created edge).
	if got := mi(g.Metrics, "critical_path_ms"); got != 20*m {
		t.Fatalf("critical_path_ms = %d, want 20min (work + pack chained through the zero-work gate)", got)
	}
	if got := mi(g.Metrics, "idle_gap_ms"); got != 30*m {
		t.Fatalf("idle_gap_ms = %d, want 30min (wall 50m − cp 20m; the pre-fix idle gap was inflated by the missing gate chain)", got)
	}
	// The gate verdict counts as a verification round.
	if got := mi(g.Metrics, "verify_rounds"); got != 1 {
		t.Fatalf("verify_rounds = %d, want 1 (gate verdict must count)", got)
	}
	// Gate structure signal for the shape-aware dispatch finding.
	if got := mi(g.Metrics, "gate_node_count"); got != 1 {
		t.Fatalf("gate_node_count = %d, want 1", got)
	}
	// Peak width stays 1: the zero-width gate never counts as parallelism.
	if got := mi(g.Metrics, "parallel_width_peak"); got != 1 {
		t.Fatalf("parallel_width_peak = %d, want 1", got)
	}
}
