package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// Phase D (proposal §13) tests:
//   D1  serial_chain finding → deterministic chain→fork auto-proposal drafted
//       (pending, never auto-approved), duplicate analysis does not double-draft.
//   D2  a chain whose middle task references an upstream output (data flow!)
//       must NOT be translated.
//   D3  infer-mode goal (no pinned version) never drafts.
//   D4  verify_pass_rate present in workflow metrics + ABMetricKeys.

// chain4JSON builds a pure 4-task serial chain: t1→t2→t3→t4→end_ok.
// Four tasks make the execution-graph critical path 4 nodes long, which is
// exactly the serial_chain finding threshold (ruleSerialChainThreshold=4).
// briefs carry NO cross-references (pure chain, fork is safe) unless
// withDataFlow is set: then t2's brief references t1's output.
func chain4JSON(withDataFlow bool) string {
	brief2 := "second step, no refs"
	if withDataFlow {
		brief2 = "build on {{node.t1.output}} directly"
	}
	return `{
  "name": "chain4",
  "description": "phase d chain fixture",
  "nodes": [
    {"id": "t1", "type": "task", "title": "step one",
     "task": {"assignee": "agent:WORKER", "brief": "first independent step"}},
    {"id": "t2", "type": "task", "title": "step two",
     "task": {"assignee": "agent:WORKER", "brief": "` + brief2 + `"}},
    {"id": "t3", "type": "task", "title": "step three",
     "task": {"assignee": "agent:WORKER", "brief": "third independent step"}},
    {"id": "t4", "type": "task", "title": "step four",
     "task": {"assignee": "agent:WORKER", "brief": "fourth independent step"}},
    {"id": "end_ok", "type": "end", "title": "fin", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "t2"},
    {"source": "t2", "source_handle": "done", "target": "t3"},
    {"source": "t3", "source_handle": "done", "target": "t4"},
    {"source": "t4", "source_handle": "done", "target": "end_ok"}
  ]
}`
}

// runPinnedTrainGoalToDone compiles a published blueprint into a train-mode
// goal and drives every compiled sub-goal run to completion, then the owner
// run, leaving the goal done with its execution graph materialized.
func runPinnedTrainGoalToDone(t *testing.T, gs *GoalService, rs *RunService, st *store.Store, versionID, workerID, domainID, title string) string {
	t.Helper()
	ctx := context.Background()
	// backlog → Activate compiles the pinned spec (Create with status active
	// would skip the compile step entirely — that is the engine's guarantee).
	g, err := gs.Create(ctx, Goal{
		Title: title, AssigneeType: "agent", AssigneeID: workerID,
		Status: "backlog", DomainID: domainID, Category: "hootl", ExecutionMode: "train",
		BlueprintVersionID: versionID, SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate (compile): %v", err)
	}
	// finish every sub-goal run in created order; each verified release wakes
	// the next one, so loop until no queued/running sub-goal run remains.
	// Real runs carry wall-clock windows (busy>0) — same-instant fixtures
	// would zero every DurationMS and the critical-path DP refuses to chain
	// through length-0 predecessors. Backdate each run as we finish it.
	step := 0
	for {
		var runID string
		err := st.DB().QueryRowContext(ctx,
			`SELECT r.id FROM run r JOIN sub_goal sg ON sg.id=r.sub_goal_id
			 WHERE r.goal_id=? AND r.role='subgoal' AND r.status IN ('queued','running')
			 ORDER BY r.created_at LIMIT 1`, g.ID).Scan(&runID)
		if err != nil || runID == "" {
			break
		}
		if err := rs.Finish(ctx, runID, "completed", "done"); err != nil {
			t.Fatalf("finish subgoal run %s: %v", runID, err)
		}
		step++
		backdateRunWindow(t, st, runID, step)
	}
	// owner run: finalize through the guard.
	goalObj, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(ctx, *goalObj); err != nil {
		t.Fatalf("owner enqueue: %v", err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatalf("no owner run: %v", err)
	}
	if err := rs.Finish(ctx, ownerRun, "completed", "done"); err != nil {
		t.Fatalf("owner finish: %v", err)
	}
	if err := gs.ReconcileGoal(ctx, g.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("goal status=%s, want done", got.Status)
	}
	return g.ID
}

// backdateRunWindow rewrites a finished run's started_at/finished_at so the
// execution graph sees a realistic busy window (30s per step, staggered).
func backdateRunWindow(t *testing.T, st *store.Store, runID string, step int) {
	t.Helper()
	start := time.Date(2026, 11, 18, 12, 0, 30*(step-1), 0, time.UTC)
	end := start.Add(30 * time.Second)
	_, err := st.DB().Exec(`UPDATE run SET started_at=?, finished_at=? WHERE id=?`,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), runID)
	if err != nil {
		t.Fatalf("backdate %s: %v", runID, err)
	}
}

// waitForSessionReady polls until the goal's async analysis has landed.
// Production path: the reconcile afterCommit fires rsi.OnGoalTerminal, which
// seals the session ('waiting'→'analyzing') and spawns `go runAnalysis` —
// every production entry (OnGoalTerminal/Retry) serializes through
// analyzeMu. A test calling rsi.Analyze directly while that goroutine is in
// flight runs TWO concurrent analyses, and both in-flight draft passes
// TOCTOU the translator's title dedup (COUNT reads 0 before either INSERT).
func waitForSessionReady(t *testing.T, rsi *RSIService, goalID string) string {
	t.Helper()
	sid := sessionIDFor(t, rsi, goalID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		if err := rsi.st.DB().QueryRowContext(context.Background(),
			`SELECT status FROM training_session WHERE id=?`, sid).Scan(&status); err != nil {
			t.Fatalf("session status: %v", err)
		}
		if status == "ready" {
			return sid
		}
		if status == "failed" || status == "skipped" {
			t.Fatalf("analysis for %s ended status=%s", goalID, status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("analysis for %s never became ready (status=%s)", goalID, status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sessionIDFor(t *testing.T, rsi *RSIService, goalID string) string {
	t.Helper()
	var sid string
	if err := rsi.st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM training_session WHERE goal_id=?`, goalID).Scan(&sid); err != nil {
		t.Fatalf("no training session for %s: %v", goalID, err)
	}
	return sid
}

func proposalsOf(t *testing.T, bs *BlueprintService, bpID string) []BlueprintProposal {
	t.Helper()
	props, err := bs.ListProposals(context.Background(), bpID)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	return props
}

// TestPhaseDAutoProposalChainToFork (D1): a train goal pinned to a pure
// 3-task chain runs to done; analysis sees serial_chain on the critical
// path and the deterministic translator drafts a pending chain→fork
// proposal. Re-analyzing must not double-draft (title dedup).
func TestPhaseDAutoProposalChainToFork(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	worker := seedAgent(t, st, "d1w-"+newID()[:8])

	content := strings.ReplaceAll(chain4JSON(false), "agent:WORKER", "agent:"+worker)
	bp, err := bs.Create(context.Background(), "chain4-bp", "desc", content, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(context.Background(), bp.ID)
	v, err := bs.Publish(context.Background(), versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	goalID := runPinnedTrainGoalToDone(t, gs, rs, st, v.ID, worker, dom, "D1 chain goal")

	// The reconcile afterCommit already sealed the session and spawned the
	// async analysis — wait for it (production path) instead of racing a
	// second concurrent Analyze.
	sid := waitForSessionReady(t, rsi, goalID)

	props := proposalsOf(t, bs, bp.ID)
	if len(props) != 1 {
		t.Fatalf("proposals=%d, want 1 (auto-proposal drafted)", len(props))
	}
	p := props[0]
	if p.Status != "pending" {
		t.Fatalf("auto-proposal status=%s, want pending — translator must never auto-approve", p.Status)
	}
	if !strings.Contains(p.Title, "并行化串行链") {
		t.Fatalf("title %q missing chain→fork marker", p.Title)
	}
	if p.CreatedByID != "rsi-translator" || p.CreatedByType != "agent" {
		t.Fatalf("provenance %s/%s, want agent/rsi-translator", p.CreatedByType, p.CreatedByID)
	}
	if p.ExpectedMetric != "wall_ms" || p.ExpectedDirection != "down" {
		t.Fatalf("expected %s/%s, want wall_ms/down", p.ExpectedMetric, p.ExpectedDirection)
	}
	if p.FindingID == "" {
		t.Fatalf("finding_id empty — auto-proposal must trace its finding")
	}
	// The patch set: delete t1→t2, t2→t3, t3→t4; add t1→end_ok, t2→end_ok,
	// t3→end_ok (t4→end_ok already exists).
	var del, add int
	for _, patch := range p.Patches {
		switch patch.Op {
		case "delete_edge":
			del++
		case "add_edge":
			add++
		}
	}
	if del != 3 || add != 3 {
		t.Fatalf("patches del=%d add=%d, want 3/3 (chain→fork)", del, add)
	}

	// Re-analyze: title dedup must hold — no second proposal.
	if err := rsi.Analyze(context.Background(), sid); err != nil {
		t.Fatalf("re-analyze: %v", err)
	}
	if props := proposalsOf(t, bs, bp.ID); len(props) != 1 {
		t.Fatalf("proposals=%d after re-analyze, want 1 (dedup)", len(props))
	}

	// The drafted patches must survive the full validator (they were already
	// validated at create; prove the patched shape is a legal fork by
	// approving through the standard gate).
	if _, _, err := bs.ApproveProposal(context.Background(), p.ID, "human", "test"); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

// TestPhaseDAutoProposalSkipsDataFlowChain (D2): t2 references
// {{node.t1.output}} — forking would break the reference, so the translator
// must refuse (no proposal at all, even though serial_chain fires).
func TestPhaseDAutoProposalSkipsDataFlowChain(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	worker := seedAgent(t, st, "d2w-"+newID()[:8])

	content := strings.ReplaceAll(chain4JSON(true), "agent:WORKER", "agent:"+worker)
	bp, err := bs.Create(context.Background(), "chain4-dataflow", "desc", content, "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, versions, _ := bs.Get(context.Background(), bp.ID)
	v, err := bs.Publish(context.Background(), versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	goalID := runPinnedTrainGoalToDone(t, gs, rs, st, v.ID, worker, dom, "D2 dataflow goal")
	// Async analysis (daemon path) already ran the translator — it must have
	// skipped the chain because t2's brief references t1's output.
	waitForSessionReady(t, rsi, goalID)
	if props := proposalsOf(t, bs, bp.ID); len(props) != 0 {
		t.Fatalf("proposals=%d, want 0 (data-flow chain must not be forked)", len(props))
	}
}

// TestPhaseDAutoProposalIgnoresInferGoals (D3): an infer goal has no pinned
// spec — even if a serial_chain finding exists, nothing can be patched.
func TestPhaseDAutoProposalIgnoresInferGoals(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	worker := seedAgent(t, st, "d3w-"+newID()[:8])

	// infer-mode train goal with a hand-built serial chain: 3 sub-goals,
	// each depending on the previous one.
	g, err := gs.Create(context.Background(), Goal{
		Title: "D3 infer goal", AssigneeType: "agent", AssigneeID: worker,
		Status: "active", DomainID: dom, ExecutionMode: "train", SampleRate: 1, Category: "hootl",
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	var prev string
	var ids []string
	for i := 0; i < 3; i++ {
		deps := []string(nil)
		if prev != "" {
			deps = []string{prev}
		}
		sg, err := gs.CreateSubGoal(context.Background(), g.ID,
			"step "+string(rune('A'+i)), "d", worker, "", deps, "human", "")
		if err != nil {
			t.Fatalf("subgoal %d: %v", i, err)
		}
		ids = append(ids, sg.ID)
		prev = sg.ID
	}
	// finish each run in order (gated ones release as deps verify).
	for {
		var runID string
		err := st.DB().QueryRowContext(context.Background(),
			`SELECT r.id FROM run r WHERE r.goal_id=? AND r.role='subgoal' AND r.status IN ('queued','running')
			 ORDER BY r.created_at LIMIT 1`, g.ID).Scan(&runID)
		if err != nil || runID == "" {
			break
		}
		if err := rs.Finish(context.Background(), runID, "completed", "ok"); err != nil {
			t.Fatalf("finish: %v", err)
		}
	}
	goalObj, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(context.Background(), *goalObj); err != nil {
		t.Fatalf("owner enqueue: %v", err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatalf("owner run: %v", err)
	}
	if err := rs.Finish(context.Background(), ownerRun, "completed", "done"); err != nil {
		t.Fatalf("owner finish: %v", err)
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Async analysis (daemon path) already ran — the translator must have
	// bailed quietly: an infer goal pins no spec.
	waitForSessionReady(t, rsi, g.ID)
	// No blueprint exists at all; the translator must have bailed quietly.
	bps, _ := bs.List(context.Background())
	total := 0
	for _, b := range bps {
		total += len(proposalsOf(t, bs, b.ID))
	}
	if total != 0 {
		t.Fatalf("total proposals=%d, want 0 (infer goal has no spec)", total)
	}
}

// TestPhaseDVerifyPassRateMetric (D4): verify_pass_rate is computed from the
// graph (first-round passes / verified agents) and appears in ABMetricKeys.
func TestPhaseDVerifyPassRateMetric(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	dom := seedDomain(t, st)
	worker := seedAgent(t, st, "d4w-"+newID()[:8])

	g, err := gs.Create(context.Background(), Goal{
		Title: "D4 pass rate", AssigneeType: "agent", AssigneeID: worker,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(context.Background(), g.ID, "s", "d", worker, "", nil, "human", "")
	if err != nil {
		t.Fatalf("subgoal: %v", err)
	}
	var runID string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY created_at DESC LIMIT 1`, sg.ID).
		Scan(&runID); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := rs.Finish(context.Background(), runID, "completed", "ok"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// owner finalize.
	goalObj, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(context.Background(), *goalObj); err != nil {
		t.Fatalf("owner enqueue: %v", err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatalf("owner run: %v", err)
	}
	if err := rs.Finish(context.Background(), ownerRun, "completed", "done"); err != nil {
		t.Fatalf("owner finish: %v", err)
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Direct graph assembly: one verified agent with zero reworks → rate 1.0.
	rsiSvc := NewRSIService(st, events.NewBus())
	wf, err := rsiSvc.BuildGoalWorkflow(context.Background(), g.ID)
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	raw, ok := wf.Metrics["verify_pass_rate"]
	if !ok {
		t.Fatalf("verify_pass_rate missing from metrics: %v", wf.Metrics)
	}
	rate, ok := raw.(float64)
	if !ok || rate != 1.0 {
		t.Fatalf("verify_pass_rate=%v (%T), want 1.0 (single first-round pass)", raw, raw)
	}
	// ABMetricKeys carries it between rework_ratio and verify_overhead_ratio.
	found := false
	for i, k := range ABMetricKeys {
		if k == "verify_pass_rate" {
			found = true
			if ABMetricKeys[i-1] != "rework_ratio" || ABMetricKeys[i+1] != "verify_overhead_ratio" {
				t.Fatalf("ABMetricKeys order around verify_pass_rate wrong: %v", ABMetricKeys)
			}
		}
	}
	if !found {
		t.Fatalf("verify_pass_rate missing from ABMetricKeys: %v", ABMetricKeys)
	}
	// JSON serializability sanity (frontend consumes map[string]any).
	if _, err := json.Marshal(wf.Metrics); err != nil {
		t.Fatalf("metrics not JSON-serializable: %v", err)
	}
}
