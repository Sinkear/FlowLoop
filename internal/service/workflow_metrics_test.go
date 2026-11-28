package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const wfTestBase int64 = 1_700_000_000_000

func wfAgent(key string, startMS, durMS int64) WorkflowNode {
	return WorkflowNode{Key: key, Kind: "agent", Label: key, Status: "verified",
		StartedAtMS: startMS, EndedAtMS: startMS + durMS, DurationMS: durMS, Attempt: 1}
}

func wfGraph(nodes []WorkflowNode, edges []WorkflowEdge) *WorkflowGraph {
	return &WorkflowGraph{GoalID: "g", Nodes: nodes, Edges: edges, Metrics: map[string]any{}}
}

// Regression: a fan-in node's chain must add only its own duration. The old
// code added the accumulated d.length again, so A||B -> C (30/30/10 min)
// produced 70 min instead of the hand-computed 40 min.
func TestWorkflowCriticalPathFanIn(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase, 30*m),
			wfAgent("sg:c", wfTestBase+30*m, 10*m),
		},
		[]WorkflowEdge{
			{From: "sg:a", To: "sg:c", Kind: "depends_on", Source: "explicit"},
			{From: "sg:b", To: "sg:c", Kind: "depends_on", Source: "explicit"},
		})
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "critical_path_ms"); got != 40*m {
		t.Fatalf("critical_path_ms = %d, want %d", got, 40*m)
	}
	if got := mi(g.Metrics, "work_sum_ms"); got != 70*m {
		t.Fatalf("work_sum_ms = %d, want %d", got, 70*m)
	}
	if got := mi(g.Metrics, "serialization_loss_ms"); got != 0 {
		t.Fatalf("serialization_loss_ms = %d, want 0", got)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.75 {
		t.Fatalf("parallel_speedup = %v, want 1.75", got)
	}
	if len(g.Critical) != 2 {
		t.Fatalf("critical path = %v, want 2 nodes", g.Critical)
	}
}

// Inferred seq edges must not constrain the critical path. Two independent
// 30-min tasks that merely ran one after another are a serialization loss,
// not a dependency chain.
func TestWorkflowInferredSeqExcludedFromCriticalPath(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		},
		[]WorkflowEdge{{From: "sg:a", To: "sg:b", Kind: "seq", Source: "inferred"}})
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "critical_path_ms"); got != 30*m {
		t.Fatalf("critical_path_ms = %d, want %d (seq must be ignored)", got, 30*m)
	}
	if got := mi(g.Metrics, "wall_ms"); got != 60*m {
		t.Fatalf("wall_ms = %d, want %d", got, 60*m)
	}
	if got := mi(g.Metrics, "serialization_loss_ms"); got != 30*m {
		t.Fatalf("serialization_loss_ms = %d, want %d", got, 30*m)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.0 {
		t.Fatalf("parallel_speedup = %v, want 1.0", got)
	}
}

// Review runs have no attribution edge: report their busy time separately
// instead of letting them inflate work_sum/speedup or become their own CP.
func TestWorkflowReviewIsolatedFromWorkAndSpeedup(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			{Key: "review:r1", Kind: "review", Label: "审查", Status: "completed",
				StartedAtMS: wfTestBase + 30*m, EndedAtMS: wfTestBase + 150*m, DurationMS: 120 * m},
		}, nil)
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "work_sum_ms"); got != 30*m {
		t.Fatalf("work_sum_ms = %d, want %d (review excluded)", got, 30*m)
	}
	if got := mi(g.Metrics, "wall_ms"); got != 30*m {
		t.Fatalf("wall_ms = %d, want %d (review excluded)", got, 30*m)
	}
	if got := mi(g.Metrics, "review_busy_ms"); got != 120*m {
		t.Fatalf("review_busy_ms = %d, want %d", got, 120*m)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.0 {
		t.Fatalf("parallel_speedup = %v, want 1.0", got)
	}
	if got := mi(g.Metrics, "critical_path_ms"); got != 30*m {
		t.Fatalf("critical_path_ms = %d, want %d", got, 30*m)
	}
}

// Equal-length tails must resolve deterministically; the old map iteration
// picked sg-a 361 / sg-b 39 times out of 400.
func TestWorkflowCriticalPathDeterministicTie(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	mk := func() *WorkflowGraph {
		return wfGraph([]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase, 30*m),
		}, nil)
	}
	first := mk()
	computeWorkflowMetrics(first)
	want := append([]string(nil), first.Critical...)
	for i := 0; i < 500; i++ {
		g := mk()
		computeWorkflowMetrics(g)
		if len(g.Critical) != len(want) || (len(want) > 0 && g.Critical[0] != want[0]) {
			t.Fatalf("iteration %d: critical path = %v, want stable %v", i, g.Critical, want)
		}
	}
}

// Regression (issue #13, Bug 1): a work item that never ran has no window, so
// its own chain length is 0. The DP used to drop such a predecessor outright
// (`p.length == 0`), which cut the chain in two: the first工序 fell off the
// path (45764ms on the reference goal) and depth under-reported the工序 count.
// Chaining through it must keep the upstream time without inventing a window,
// and the windowless node itself is not a工序 (it contributes 0ms).
func TestWorkflowCriticalPathChainsThroughWindowlessNode(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	windowless := WorkflowNode{Key: "sg:mid", Kind: "agent", Label: "确认审计范围", Status: "pending"}
	g := wfGraph(
		[]WorkflowNode{
			{Key: "goal", Kind: "goal", Label: "沙箱资产审计", Status: "active"},
			wfAgent("sg:a", wfTestBase, 30*m),
			windowless,
			wfAgent("sg:c", wfTestBase+30*m, 10*m),
		},
		[]WorkflowEdge{
			{From: "goal", To: "sg:a", Kind: "created", Source: "explicit"},
			{From: "goal", To: "sg:mid", Kind: "created", Source: "explicit"},
			{From: "goal", To: "sg:c", Kind: "created", Source: "explicit"},
			{From: "sg:a", To: "sg:mid", Kind: "depends_on", Source: "explicit"},
			{From: "sg:mid", To: "sg:c", Kind: "depends_on", Source: "explicit"},
		})
	computeWorkflowMetrics(g)
	// 30min + 0 + 10min: the upstream 30min must survive the gap.
	if got := mi(g.Metrics, "critical_path_ms"); got != 40*m {
		t.Fatalf("critical_path_ms = %d, want %d (chain must pass through the windowless node)", got, 40*m)
	}
	if got := mi(g.Metrics, "work_sum_ms"); got != 40*m {
		t.Fatalf("work_sum_ms = %d, want %d", got, 40*m)
	}
	if got := mi(g.Metrics, "wall_ms"); got != 40*m {
		t.Fatalf("wall_ms = %d, want %d", got, 40*m)
	}
	// depth counts工序 (busy work items): the windowless node and the goal root
	// are traversed but must not be listed.
	if got := mi(g.Metrics, "depth"); got != 2 {
		t.Fatalf("depth = %d, want 2", got)
	}
	if len(g.Critical) != 2 || g.Critical[0] != "sg:a" || g.Critical[1] != "sg:c" {
		t.Fatalf("critical path = %v, want [sg:a sg:c]", g.Critical)
	}
}

// Issue #13 (D1): the metric that carries wall - cp is an IDLE GAP, not a
// serialization loss. It is published as idle_gap_ms and the old key stays as a
// same-value deprecated alias so RSI round ledgers and AB tables keep reading a
// number; the schema version marks the口径 break for trend comparisons.
func TestWorkflowIdleGapAliasSameValue(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			// Independent task 30 min after: wall 60, cp 30 → 30 min of idle gap.
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		}, nil)
	computeWorkflowMetrics(g)
	want := 30 * m
	if got := mi(g.Metrics, "idle_gap_ms"); got != want {
		t.Fatalf("idle_gap_ms = %d, want %d", got, want)
	}
	if got := mi(g.Metrics, "serialization_loss_ms"); got != want {
		t.Fatalf("serialization_loss_ms = %d, want %d (deprecated alias must keep the same value)", got, want)
	}
	if got := mi(g.Metrics, "metrics_schema_version"); got != 3 {
		t.Fatalf("metrics_schema_version = %d, want 3", got)
	}
	// Pure linear chain → no gap at all (the alias must not fabricate one).
	linear := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		},
		[]WorkflowEdge{{From: "sg:a", To: "sg:b", Kind: "depends_on", Source: "explicit"}})
	computeWorkflowMetrics(linear)
	if got := mi(linear.Metrics, "idle_gap_ms"); got != 0 {
		t.Fatalf("idle_gap_ms = %d, want 0 on a linear chain", got)
	}
	if got := mi(linear.Metrics, "critical_path_ms"); got != mi(linear.Metrics, "work_sum_ms") {
		t.Fatalf("linear chain: cp %d != work_sum %d", got, mi(linear.Metrics, "work_sum_ms"))
	}
}

// Issue #13 (D2): a machine verification round has no separable timing window
// (the gate runs inside the daemon on the owner run's window), so the graph used
// to print verify_overhead 0% with nothing to distinguish it from "verification
// is free". Rounds are now split into measured/unmeasured with a coverage ratio
// and a scope marker, and the verify node carrying unmeasured rounds is flagged.
func TestWorkflowVerifyCoverageUnmeasured(t *testing.T) {
	st, gsvc, rsi := newRSITestEnv(t)
	g := mkRSIGoal(t, gsvc, "machine-verify", "infer")
	base := time.Now().UTC().Add(-2 * time.Hour)
	created := base.Format(time.RFC3339Nano)
	const rounds = 12
	for i := 0; i < rounds; i++ {
		sg := fmt.Sprintf("sg-%02d", i)
		if _, err := st.DB().Exec(`INSERT INTO sub_goal (id,goal_id,title,assignee_id,verifier_id,status,depends_on,created_at)
		 VALUES (?,?,'task','ag-1','','verified','[]',?)`, sg, g.ID, created); err != nil {
			t.Fatalf("sub_goal: %v", err)
		}
		insertRun(t, st, "r-"+sg, g.ID, sg, "subgoal", base, base.Add(10*time.Minute))
		// A machine round: empty verifier_run_id (the schema marker for machine
		// verification), machine_run_id names the judged run, verify_ms 0 = the
		// daemon did not time the gate.
		if _, err := st.DB().Exec(`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,verify_ms,machine_run_id,status,summary,created_at)
		 VALUES (?,?,?,'',0,?,'passed','machine gate',?)`, "vr-"+sg, g.ID, sg, "r-"+sg, created); err != nil {
			t.Fatalf("verification: %v", err)
		}
	}
	gf, err := rsi.BuildGoalWorkflow(context.Background(), g.ID)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := mi(gf.Metrics, "verify_rounds"); got != rounds {
		t.Fatalf("verify_rounds = %d, want %d", got, rounds)
	}
	if got := mi(gf.Metrics, "verify_measured_rounds"); got != 0 {
		t.Fatalf("verify_measured_rounds = %d, want 0", got)
	}
	if got := mi(gf.Metrics, "verify_unmeasured_rounds"); got != rounds {
		t.Fatalf("verify_unmeasured_rounds = %d, want %d", got, rounds)
	}
	if got := mf(gf.Metrics, "verify_coverage_ratio"); got != 0 {
		t.Fatalf("verify_coverage_ratio = %v, want 0", got)
	}
	if got, _ := gf.Metrics["verify_overhead_scope"].(string); got != "unmeasured" {
		t.Fatalf("verify_overhead_scope = %q, want unmeasured", got)
	}
	if got := mi(gf.Metrics, "verify_busy_measured_ms"); got != 0 {
		t.Fatalf("verify_busy_measured_ms = %d, want 0", got)
	}
	// The 0% is a FLOOR accompanied by the scope marker, never a bare 0%.
	if got := mf(gf.Metrics, "verify_overhead_ratio"); got != 0 {
		t.Fatalf("verify_overhead_ratio = %v, want 0 (measured busy only)", got)
	}
	for _, n := range gf.Nodes {
		if n.Kind != "verify" {
			continue
		}
		if !n.Unmeasured || n.UnmeasuredRounds != 1 || n.MeasuredRounds != 0 || n.Attempt != 1 {
			t.Fatalf("verify node %s = %+v, want unmeasured 1 round", n.Key, n)
		}
	}

	// The other two states, without a DB fixture: partial and complete.
	m := int64(time.Minute / time.Millisecond)
	partial := wfGraph([]WorkflowNode{{
		Key: "v:a", Kind: "verify", Status: "passed", Attempt: 4,
		MeasuredRounds: 1, UnmeasuredRounds: 3, Unmeasured: true,
		StartedAtMS: wfTestBase, EndedAtMS: wfTestBase + m, DurationMS: m,
	}}, nil)
	computeWorkflowMetrics(partial)
	if got := mf(partial.Metrics, "verify_coverage_ratio"); got != 0.25 {
		t.Fatalf("partial coverage = %v, want 0.25", got)
	}
	if got, _ := partial.Metrics["verify_overhead_scope"].(string); got != "partial" {
		t.Fatalf("partial scope = %q, want partial", got)
	}
	if got := mi(partial.Metrics, "verify_unmeasured_rounds"); got != 3 {
		t.Fatalf("partial unmeasured = %d, want 3", got)
	}

	complete := wfGraph([]WorkflowNode{{
		Key: "v:b", Kind: "verify", Status: "passed", Attempt: 2,
		MeasuredRounds: 2,
		StartedAtMS:    wfTestBase, EndedAtMS: wfTestBase + 2*m, DurationMS: 2 * m,
	}}, nil)
	computeWorkflowMetrics(complete)
	if got := mf(complete.Metrics, "verify_coverage_ratio"); got != 1 {
		t.Fatalf("complete coverage = %v, want 1", got)
	}
	if got, _ := complete.Metrics["verify_overhead_scope"].(string); got != "complete" {
		t.Fatalf("complete scope = %q, want complete", got)
	}
	if got := mf(complete.Metrics, "verify_overhead_ratio"); got != 1 {
		t.Fatalf("complete verify_overhead_ratio = %v, want 1 (2min verify / 2min work)", got)
	}
}

// Splitting a long serial gap into two independent tasks is exactly the
// "lost parallelism" case: work 60, cp 30, actual speedup 1.0.
func TestWorkflowLostParallelismInputs(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		}, nil)
	computeWorkflowMetrics(g)
	gain := float64(mi(g.Metrics, "work_sum_ms")) / float64(mi(g.Metrics, "critical_path_ms"))
	actual := mf(g.Metrics, "parallel_speedup")
	if gain != 2 || actual != 1 {
		t.Fatalf("gain=%v actual=%v, want 2 and 1", gain, actual)
	}
	if !(actual < gain*0.7) {
		t.Fatalf("lost_parallelism guard should fire: actual=%v gain=%v", actual, gain)
	}
}

// The lost_parallelism rule must actually fire on the inputs it was written
// for. Previously speedup was work/cp, so "speedup < gain*0.7" reduced to
// 1 < 0.7 and no finding could ever be produced.
func TestLostParallelismRuleFires(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		}, nil)
	computeWorkflowMetrics(g)
	svc := &RSIService{}
	findings := svc.structureFindings(context.Background(), "goal-1", g)
	for _, f := range findings {
		if f.Category == "lost_parallelism" {
			return
		}
	}
	t.Fatalf("lost_parallelism did not fire; metrics=%v", g.Metrics)
}

// Issue #38: the dispatch_wait finding's ATTRIBUTION must follow the
// workflow's shape. On a serial-through-gate graph (peak parallel width ≤ 1
// with blueprint gates present) there is no queue or lane contention to
// fix — advising "add lanes / pre-dispatch beats editing the graph" pointed
// exactly away from the real bottleneck (serial chain + human gate). The
// share is still reported; the advice switches.
func TestDispatchWaitFindingAttributionIsShapeAware(t *testing.T) {
	svc := &RSIService{}
	g := wfGraph(nil, nil)
	const dw, wall = int64(40 * time.Minute / time.Millisecond), int64(100 * time.Minute / time.Millisecond)

	// Serial-through-gate shape: gate present, peak width 1 → the advice
	// must point at the graph structure (serial chain + human gate), not
	// at lanes or pre-dispatch.
	serial := map[string]any{
		"dispatch_wait_ms": dw, "wall_ms": wall, "lane_count": int64(5),
		"gate_node_count": int64(1), "parallel_width_peak": int64(1),
	}
	var f *RsiFinding
	fs := svc.nodeFindings(context.Background(), "goal-1", g, serial, nil)
	for i := range fs {
		if fs[i].Category == "dispatch_wait" {
			f = &fs[i]
		}
	}
	if f == nil {
		t.Fatalf("dispatch_wait finding missing on serial shape; metrics=%v", serial)
	}
	if !strings.Contains(f.Suggestion, "图结构") {
		t.Fatalf("serial-through-gate advice must point at graph structure, got: %s", f.Suggestion)
	}
	if strings.Contains(f.Suggestion, "比改图更有效") || strings.Contains(f.Suggestion, "提高并行认领") {
		t.Fatalf("serial-through-gate advice must not recommend lanes/pre-dispatch as the fix, got: %s", f.Suggestion)
	}
	if !strings.Contains(f.Suggestion, "不会缩短这段等待") {
		t.Fatalf("serial-through-gate advice must explicitly rule out lanes/pre-dispatch, got: %s", f.Suggestion)
	}
	if !strings.Contains(f.Explanation, "不是并发队列或 lane 争用") {
		t.Fatalf("serial-through-gate explanation must name the real cause, got: %s", f.Explanation)
	}

	// Queue shape (no gates, real parallelism): the classic lane guidance
	// is correct and must be preserved verbatim.
	queue := map[string]any{
		"dispatch_wait_ms": dw, "wall_ms": wall, "lane_count": int64(5),
		"gate_node_count": int64(0), "parallel_width_peak": int64(3),
	}
	f = nil
	fs = svc.nodeFindings(context.Background(), "goal-1", g, queue, nil)
	for i := range fs {
		if fs[i].Category == "dispatch_wait" {
			f = &fs[i]
		}
	}
	if f == nil {
		t.Fatalf("dispatch_wait finding missing on queue shape; metrics=%v", queue)
	}
	if !strings.Contains(f.Suggestion, "预派发") {
		t.Fatalf("queue-shape advice must keep lane/pre-dispatch guidance, got: %s", f.Suggestion)
	}
	if !strings.Contains(f.Explanation, "而不是图结构问题") {
		t.Fatalf("queue-shape explanation must keep the classic attribution, got: %s", f.Explanation)
	}
}
