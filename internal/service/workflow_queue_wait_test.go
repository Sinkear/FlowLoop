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
	cts := h.subAt.UTC().Format(time.RFC3339Nano)
	if _, err := h.st.DB().Exec(`INSERT INTO sub_goal (id,goal_id,title,assignee_id,status,depends_on,created_at)
		VALUES (?,?,?,?,?,?,?)`, id, h.goal, id, "ag-x", "verified", deps, cts); err != nil {
		h.t.Fatalf("sub_goal %s: %v", id, err)
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
	h.sub("sg-b", "[]")
	h.run("run-a", "sg-a", h.base, h.base.Add(10*time.Minute))

	g := h.graph()
	if got := mi(g.Metrics, "unrun_sub_goals"); got != 1 {
		t.Fatalf("unrun_sub_goals = %d, want 1", got)
	}
	if got := mi(g.Metrics, "queue_wait_ms"); got != 0 {
		t.Fatalf("queue_wait_ms = %d, want 0 (unrun sub-goal must not contribute)", got)
	}
}

func TestQueueWaitCappedToWall(t *testing.T) {
	h := newQueueWaitHarness(t)
	h.sub("sg-a", "[]")
	h.sub("sg-b", "[]")
	// Two independent runs both delayed 20min but overlapping for 10min:
	// summed dispatch wait (40min) exceeds the 10min wall and must converge.
	h.run("run-a", "sg-a", h.base.Add(20*time.Minute), h.base.Add(30*time.Minute))
	h.run("run-b", "sg-b", h.base.Add(20*time.Minute), h.base.Add(30*time.Minute))

	g := h.graph()
	wall := mi(g.Metrics, "wall_ms")
	q := mi(g.Metrics, "queue_wait_ms")
	if q > wall {
		t.Fatalf("queue_wait_ms = %d exceeds wall_ms = %d", q, wall)
	}
	if q != wall {
		t.Fatalf("queue_wait_ms = %d, want converged to wall_ms = %d", q, wall)
	}
	if g.Metrics["queue_wait_capped"] != true {
		t.Fatalf("queue_wait_capped = %v, want true", g.Metrics["queue_wait_capped"])
	}
	if got := mi(g.Metrics, "queue_wait_overlap_ms"); got != int64(30*time.Minute/time.Millisecond) {
		t.Fatalf("queue_wait_overlap_ms = %d, want 30min (40min summed - 10min wall)", got)
	}
	if got := mi(g.Metrics, "dispatch_wait_ms"); got != wall {
		t.Fatalf("dispatch_wait_ms = %d, want capped to wall %d", got, wall)
	}
}