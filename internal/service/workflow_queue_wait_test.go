package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/store"
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
