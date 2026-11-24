package service

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// Workflow-level observation (W-RSI, 2026-11-08). The orchestration graph is
// ASSEMBLED READ-TIME from entity tables (goal / sub_goal / run /
// verification_result / consult_request / change) — there is no node table
// and no span collection, so the graph can never drift from execution state.
// Declared dependencies live in sub_goal.depends_on; every other relation is
// derived from structure or, when only time-order suggests it, marked
// "inferred" (dashed in the UI). Inferred edges are DISPLAY EVIDENCE ONLY:
// the critical-path DP ignores them, otherwise two independent tasks that
// merely happened to run one after the other would be chained into a fake
// dependency and every serial execution would report speedup 1.0.

// WorkflowNode is one node of the orchestration graph. kind:
// goal|agent|verify|review|consult|integrate.
type WorkflowNode struct {
	Key           string  `json:"key"`
	Kind          string  `json:"kind"`
	Label         string  `json:"label"`
	AssigneeID    string  `json:"assignee_id"`
	SubGoalID     string  `json:"sub_goal_id,omitempty"`
	RunID         string  `json:"run_id,omitempty"`
	Status        string  `json:"status"`
	Attempt       int     `json:"attempt"`     // run count (agent nodes) / verification rounds (verify nodes)
	Reworks       int     `json:"reworks"`     // rejected verification rounds on this node
	DurationMS    int64   `json:"duration_ms"` // busy time (sum of run walls)
	StartedAtMS   int64   `json:"started_at_ms"`
	EndedAtMS     int64   `json:"ended_at_ms"`
	CriticalShare float64 `json:"critical_share"` // duration / critical path (filled by metrics)
	// Verify nodes only (issue #13): rounds split by whether the round had a
	// timing source. Unmeasured=true means at least one round happened without
	// one, so duration_ms=0 reads as "not timed" rather than "free".
	Unmeasured       bool `json:"unmeasured,omitempty"`
	MeasuredRounds   int  `json:"measured_rounds,omitempty"`
	UnmeasuredRounds int  `json:"unmeasured_rounds,omitempty"`
}

// WorkflowEdge is one directed edge. kind:
// created|depends_on|seq|verified_by|consult|integrated. source:
// explicit (declared or structural by construction) | inferred (time-order
// heuristic).
type WorkflowEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// WorkflowGraph is the full assembled graph for one goal. Frozen snapshots
// are stored in rsi_report.workflow_json at analysis time.
type WorkflowGraph struct {
	GoalID      string         `json:"goal_id"`
	GeneratedAt string         `json:"generated_at"`
	Frozen      bool           `json:"frozen"`
	Nodes       []WorkflowNode `json:"nodes"`
	Edges       []WorkflowEdge `json:"edges"`
	Critical    []string       `json:"critical_path"` // node keys, root → sink
	Metrics     map[string]any `json:"metrics"`
}

// WorkflowGoalSummary is the mini-DAG row shown in the squad orchestration
// section and the Training list.
type WorkflowGoalSummary struct {
	GoalID          string  `json:"goal_id"`
	GoalTitle       string  `json:"goal_title"`
	Status          string  `json:"status"`
	NodeCount       int     `json:"node_count"`
	EdgeCount       int     `json:"edge_count"`
	CriticalPathMS  int64   `json:"critical_path_ms"`
	ParallelSpeedup float64 `json:"parallel_speedup"`
	ReworkRatio     float64 `json:"rework_ratio"`
}

// parseTimeMS parses an RFC3339Nano store timestamp into epoch millis.
func parseTimeMS(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// addBusy accumulates a run window into a node window.
func addBusy(n *WorkflowNode, start, finish string) {
	if ms, ok := parseTimeMS(start); ok && (n.StartedAtMS == 0 || ms < n.StartedAtMS) {
		n.StartedAtMS = ms
	}
	if ms, ok := parseTimeMS(finish); ok && ms > n.EndedAtMS {
		n.EndedAtMS = ms
	}
	if ms1, ok1 := parseTimeMS(start); ok1 {
		if ms2, ok2 := parseTimeMS(finish); ok2 && ms2 > ms1 {
			n.DurationMS += ms2 - ms1
		}
	}
}

// BuildGoalWorkflow assembles the orchestration graph for one goal.
func (s *RSIService) BuildGoalWorkflow(ctx context.Context, goalID string) (*WorkflowGraph, error) {
	var gTitle, gStatus string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status,title FROM goal WHERE id=?`, goalID).Scan(&gStatus, &gTitle); err != nil {
		return nil, err
	}

	graph := &WorkflowGraph{
		GoalID:      goalID,
		GeneratedAt: now(),
		Nodes:       []WorkflowNode{},
		Edges:       []WorkflowEdge{},
		Metrics:     map[string]any{},
	}
	graph.Nodes = append(graph.Nodes, WorkflowNode{Key: "goal", Kind: "goal", Label: gTitle, Status: gStatus})

	// ── sub-goal nodes + windows ────────────────────────────────────────
	type sgRow struct {
		id, title, assignee, verifier, status, createdAt, dependsOn string
		qualityIter                                                 int
	}
	sgRows := map[string]*sgRow{}
	sgOrder := []string{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,title,assignee_id,verifier_id,status,quality_iteration,created_at,depends_on
		 FROM sub_goal WHERE goal_id=? ORDER BY created_at ASC`, goalID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r sgRow
		if err := rows.Scan(&r.id, &r.title, &r.assignee, &r.verifier, &r.status, &r.qualityIter, &r.createdAt, &r.dependsOn); err != nil {
			rows.Close()
			return nil, err
		}
		sgRows[r.id] = &r
		sgOrder = append(sgOrder, r.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type win struct {
		start, end, busy int64
		attempts         int
	}
	wins := map[string]*win{}
	rrows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id,started_at,finished_at FROM run
		 WHERE goal_id=? AND sub_goal_id!='' AND role='subgoal'`, goalID)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var sgid, st, fi string
		if err := rrows.Scan(&sgid, &st, &fi); err != nil {
			rrows.Close()
			return nil, err
		}
		w, ok := wins[sgid]
		if !ok {
			w = &win{}
			wins[sgid] = w
		}
		w.attempts++
		if ms, ok := parseTimeMS(st); ok && (w.start == 0 || ms < w.start) {
			w.start = ms
		}
		if ms, ok := parseTimeMS(fi); ok && ms > w.end {
			w.end = ms
		}
		if ms1, ok1 := parseTimeMS(st); ok1 {
			if ms2, ok2 := parseTimeMS(fi); ok2 && ms2 > ms1 {
				w.busy += ms2 - ms1
			}
		}
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, err
	}

	// Declared dependencies, decoded once: used for gating-aware wait metrics
	// and for the explicit depends_on edges below.
	depsMap := map[string][]string{}
	for _, id := range sgOrder {
		var deps []string
		if err := json.Unmarshal([]byte(sgRows[id].dependsOn), &deps); err != nil {
			continue
		}
		depsMap[id] = deps
	}

	// Dispatch wait is capped against the ORCHESTRATION SPAN below, not the
	// busy wall: wall_ms only counts nodes with a positive busy window, so an
	// in-flight run (finished_at='') contributes nothing and a single running
	// sub-goal would report queue wait > wall (#11). The span runs from the
	// earliest ready moment to the latest sub-goal finish — or now while a run
	// is still in flight.
	var depWait, dispatchWait, spanStart, spanEnd int64
	nowMS, _ := parseTimeMS(now())
	inFlight := false
	for _, id := range sgOrder {
		r := sgRows[id]
		n := WorkflowNode{Key: "sg:" + id, Kind: "agent", Label: r.title, AssigneeID: r.assignee,
			SubGoalID: id, Status: r.status, Attempt: 1, Reworks: r.qualityIter}
		// Split queue time into dependency gate wait and dispatch delay:
		// ready_at = max(sub_goal.created_at, latest dependency run end).
		// A malformed/empty created_at must not turn a dependency's epoch
		// timestamp into a ~55-year "wait": without a valid creation anchor
		// both split metrics are unknown and stay 0.
		createdMS, createdOK := parseTimeMS(r.createdAt)
		readyMS := int64(0)
		if createdOK {
			readyMS = createdMS
			for _, dep := range depsMap[id] {
				if wd := wins[dep]; wd != nil && wd.end > readyMS {
					readyMS = wd.end
				}
			}
			if spanStart == 0 || readyMS < spanStart {
				spanStart = readyMS
			}
		}
		if w := wins[id]; w != nil {
			n.Attempt = w.attempts
			n.StartedAtMS, n.EndedAtMS, n.DurationMS = w.start, w.end, w.busy
			if createdOK {
				if readyMS > createdMS {
					depWait += readyMS - createdMS
				}
				if w.start > 0 && w.start > readyMS {
					dispatchWait += w.start - readyMS
				}
			}
			if w.start > 0 && (spanStart == 0 || w.start < spanStart) {
				spanStart = w.start
			}
			if w.end > spanEnd {
				spanEnd = w.end
			}
			if w.start > 0 && w.end == 0 {
				inFlight = true
			}
		}
		graph.Nodes = append(graph.Nodes, n)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "goal", To: n.Key, Kind: "created", Source: "explicit"})
	}
	if inFlight && nowMS > spanEnd {
		spanEnd = nowMS
	}
	span := spanEnd - spanStart
	if span < 0 {
		span = 0
	}

	// Declared dependencies (explicit DAG) — best-effort JSON decode.
	for _, id := range sgOrder {
		var deps []string
		if err := json.Unmarshal([]byte(sgRows[id].dependsOn), &deps); err != nil || len(deps) == 0 {
			continue
		}
		for _, d := range deps {
			if _, ok := sgRows[d]; ok {
				graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + d, To: "sg:" + id, Kind: "depends_on", Source: "explicit"})
			}
		}
	}

	// Inferred sequence edges: B started strictly after A ended with no
	// declared edge either way. Dashed evidence only — never a strong claim.
	hasEdge := func(a, b string) bool {
		for _, e := range graph.Edges {
			if (e.From == a && e.To == b) || (e.From == b && e.To == a) {
				return true
			}
		}
		return false
	}
	for _, a := range sgOrder {
		wa := wins[a]
		if wa == nil || wa.end == 0 {
			continue
		}
		for _, b := range sgOrder {
			if a == b {
				continue
			}
			wb := wins[b]
			if wb == nil || wb.start == 0 || wb.start <= wa.end {
				continue
			}
			if hasEdge("sg:"+a, "sg:"+b) {
				continue
			}
			graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + a, To: "sg:" + b, Kind: "seq", Source: "inferred"})
		}
	}

	// ── verification nodes (one per sub-goal, rounds aggregated) ────────
	// A verification round is MEASURED when it has a timing source: a verifier
	// run window, or a machine round that the daemon actually timed (verify_ms
	// > 0, phase 2). Everything else is UNMEASURED — the machine gate runs
	// inside the daemon on the owner run's window, so 0ms here means "we did not
	// time it", never "verification is free" (issue #13, D2). The empty
	// verifier_run_id stays the machine-round marker.
	type vState struct {
		node       WorkflowNode
		runIDs     []string
		measured   int
		unmeasured int
		busy       int64
	}
	vmap := map[string]*vState{}
	vrows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id,status,verifier_run_id,verify_ms FROM verification_result WHERE goal_id=? ORDER BY created_at ASC`, goalID)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var sgid, status, vrun string
		var verifyMS int64
		if err := vrows.Scan(&sgid, &status, &vrun, &verifyMS); err != nil {
			vrows.Close()
			return nil, err
		}
		v, ok := vmap[sgid]
		if !ok {
			verifier := ""
			if r := sgRows[sgid]; r != nil {
				verifier = r.verifier
			}
			v = &vState{node: WorkflowNode{Key: "v:" + sgid, Kind: "verify", Label: "验证",
				AssigneeID: verifier, SubGoalID: sgid, Status: "passed"}}
			vmap[sgid] = v
		}
		v.node.Attempt++
		if status == "rejected" {
			v.node.Reworks++
			v.node.Status = "rejected"
		}
		switch {
		case vrun != "":
			// Verifier round: measured only if its run window is readable.
			v.runIDs = append(v.runIDs, vrun)
		case verifyMS > 0:
			// Machine round with its own timing (phase 2): measured without a
			// window — the gate has no separable start/end on the graph.
			v.measured++
			v.busy += verifyMS
		default:
			v.unmeasured++
		}
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	for sgid, v := range vmap {
		for _, rid := range v.runIDs {
			var st, fi string
			if err := s.st.DB().QueryRowContext(ctx,
				`SELECT started_at,finished_at FROM run WHERE id=?`, rid).Scan(&st, &fi); err != nil {
				v.unmeasured++ // the round names a run the store no longer has
				continue
			}
			v.measured++
			addBusy(&v.node, st, fi)
		}
		v.node.DurationMS += v.busy
		if rest := v.node.Attempt - v.measured - v.unmeasured; rest > 0 {
			// Defensive: a round must land in exactly one bucket, so the counts
			// always add up to Attempt (= verify_rounds) for readers.
			v.unmeasured += rest
		}
		v.node.MeasuredRounds = v.measured
		v.node.UnmeasuredRounds = v.unmeasured
		v.node.Unmeasured = v.unmeasured > 0
		graph.Nodes = append(graph.Nodes, v.node)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + sgid, To: v.node.Key, Kind: "verified_by", Source: "explicit"})
	}

	// ── review + consult nodes ──────────────────────────────────────────
	crows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,role,agent_id,status,started_at,finished_at FROM run
		 WHERE goal_id=? AND role IN ('review','consult')`, goalID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var id, role, agent, status, st, fi string
		if err := crows.Scan(&id, &role, &agent, &status, &st, &fi); err != nil {
			crows.Close()
			return nil, err
		}
		n := WorkflowNode{Key: role + ":" + id, Kind: role,
			Label:      map[string]string{"review": "审查", "consult": "咨询"}[role],
			AssigneeID: agent, RunID: id, Status: status}
		addBusy(&n, st, fi)
		graph.Nodes = append(graph.Nodes, n)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Consult structure edges: requester node → guest consult node.
	qrows, err := s.st.DB().QueryContext(ctx,
		`SELECT requester_run_id,guest_run_id FROM consult_request WHERE goal_id=? AND guest_run_id!=''`, goalID)
	if err != nil {
		return nil, err
	}
	for qrows.Next() {
		var reqID, guestID string
		if err := qrows.Scan(&reqID, &guestID); err != nil {
			qrows.Close()
			return nil, err
		}
		to := "consult:" + guestID
		if !nodeKeyExists(graph, to) {
			continue
		}
		from := "goal"
		var sgid string
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT sub_goal_id FROM run WHERE id=?`, reqID).Scan(&sgid); err == nil && sgid != "" {
			if _, ok := sgRows[sgid]; ok {
				from = "sg:" + sgid
			}
		}
		graph.Edges = append(graph.Edges, WorkflowEdge{From: from, To: to, Kind: "consult", Source: "explicit"})
	}
	qrows.Close()
	if err := qrows.Err(); err != nil {
		return nil, err
	}

	// ── integration nodes ───────────────────────────────────────────────
	chrows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,sub_goal_id,status FROM change WHERE goal_id=?`, goalID)
	if err != nil {
		return nil, err
	}
	for chrows.Next() {
		var id, sgid, status string
		if err := chrows.Scan(&id, &sgid, &status); err != nil {
			chrows.Close()
			return nil, err
		}
		n := WorkflowNode{Key: "chg:" + id, Kind: "integrate", Label: "集成", SubGoalID: sgid, Status: status}
		graph.Nodes = append(graph.Nodes, n)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + sgid, To: n.Key, Kind: "integrated", Source: "explicit"})
	}
	chrows.Close()
	if err := chrows.Err(); err != nil {
		return nil, err
	}

	computeWorkflowMetrics(graph)

	// "排队等待" measures DISPATCH delay only: started_at − ready_at, where
	// ready_at = max(created_at, latest dependency finish). Dependency gating
	// is reported separately as dependency_wait_ms and must never be folded
	// back in, otherwise a serial chain accumulates waits beyond the span.
	//
	// Concurrent dispatches can still sum past the orchestration span (N
	// parallel lanes waiting at once), so the total is capped at the span —
	// earliest ready to last finish, or now while a run is in flight. The
	// overflow is kept in queue_wait_overlap_ms instead of being dropped.
	graph.Metrics["wall_span_ms"] = span
	if span > 0 && dispatchWait > span {
		graph.Metrics["queue_wait_overlap_ms"] = dispatchWait - span
		graph.Metrics["queue_wait_capped"] = true
		dispatchWait = span
	}
	graph.Metrics["dependency_wait_ms"] = depWait
	graph.Metrics["dispatch_wait_ms"] = dispatchWait
	// queue_wait_ms is a compatibility alias for dispatch_wait_ms.
	graph.Metrics["queue_wait_ms"] = dispatchWait

	// Sub-goals that never started executing: no run window at all and no
	// settled outcome. A blueprint gate HOLDER is excluded even before its
	// decision lands — by construction it never gets a run (the human/auto
	// decision is its run), so counting it would label a decision node "未执行".
	gateHolders := map[string]bool{}
	if grows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id FROM blueprint_node WHERE goal_id=? AND node_type='gate' AND sub_goal_id!=''`, goalID); err == nil {
		for grows.Next() {
			var sgid string
			if grows.Scan(&sgid) == nil && sgid != "" {
				gateHolders[sgid] = true
			}
		}
		grows.Close()
	}
	unrun := int64(0)
	for _, id := range sgOrder {
		if wins[id] != nil || gateHolders[id] {
			continue
		}
		switch sgRows[id].status {
		case "verified", "cancelled", "failed":
			continue // settled without a run of its own — not pending dispatch
		}
		unrun++
	}
	graph.Metrics["unrun_sub_goals"] = unrun

	// Human gate wait is entity-level (gate_decision), not derivable from run
	// windows. The share uses wall+wait so "占比" can never exceed 100%.
	wall := mi(graph.Metrics, "wall_ms")
	var humanWaitMS int64
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision WHERE goal_id=?`, goalID).Scan(&humanWaitMS)
	graph.Metrics["human_wait_ms"] = humanWaitMS
	share := 0.0
	if denom := wall + humanWaitMS; denom > 0 {
		share = float64(humanWaitMS) / float64(denom)
	}
	graph.Metrics["human_wait_share"] = share
	return graph, nil
}

func nodeKeyExists(g *WorkflowGraph, key string) bool {
	for _, n := range g.Nodes {
		if n.Key == key {
			return true
		}
	}
	return false
}

// topoOrder returns the nodes in topological order of the explicit edges
// (preds). The incoming slice is already start-time sorted and is used as the
// tie-break, so relaxation order stays deterministic across runs. A cycle in
// hand-edited depends_on data leaves nodes with no satisfiable predecessor:
// they are appended in start-time order instead of being dropped, so the pass
// can never silently lose a node (the time-order validity check below still
// keeps a cyclic edge from extending a chain).
func topoOrder(order []WorkflowNode, preds map[string][]string, byKey map[string]*WorkflowNode) []WorkflowNode {
	out := make([]WorkflowNode, 0, len(order))
	emitted := make(map[string]bool, len(order))
	for len(out) < len(order) {
		progress := false
		for _, n := range order {
			if emitted[n.Key] {
				continue
			}
			ready := true
			for _, from := range preds[n.Key] {
				if _, ok := byKey[from]; !ok {
					continue
				}
				if !emitted[from] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			emitted[n.Key] = true
			out = append(out, n)
			progress = true
		}
		if !progress { // cycle residue: keep the start-time order, never drop
			for _, n := range order {
				if !emitted[n.Key] {
					emitted[n.Key] = true
					out = append(out, n)
				}
			}
			break
		}
	}
	return out
}

// computeWorkflowMetrics fills Critical, CriticalShare, and Metrics. The
// critical path is a longest chain of node busy durations: nodes relax in
// topological order of the explicit edges and an edge only extends the chain
// when the source finishes before the target starts, so the pass is acyclic by
// construction even when the drawn graph has rework cycles. Relaxation must be
// topological — start-time order alone silently broke the chain at every
// windowless node (see the DP comment below).
func computeWorkflowMetrics(g *WorkflowGraph) {
	byKey := map[string]*WorkflowNode{}
	for i := range g.Nodes {
		byKey[g.Nodes[i].Key] = &g.Nodes[i]
	}
	order := append([]WorkflowNode(nil), g.Nodes...)
	sort.SliceStable(order, func(i, j int) bool {
		si, sj := order[i].StartedAtMS, order[j].StartedAtMS
		if order[i].Kind == "goal" || order[i].StartedAtMS == 0 {
			si = -1
		}
		if order[j].Kind == "goal" || order[j].StartedAtMS == 0 {
			sj = -1
		}
		return si < sj
	})

	preds := map[string][]string{} // target → sources (already processed earlier)
	for _, e := range g.Edges {
		// Inferred seq edges are evidence for display/queue analysis only. They
		// must not constrain the critical path: a pure time-order relation is
		// not a dependency, and admitting it makes every serial run look like
		// a 1.0-speedup chain.
		if e.Source == "inferred" {
			continue
		}
		if _, ok := byKey[e.From]; !ok {
			continue
		}
		if _, ok := byKey[e.To]; !ok {
			continue
		}
		preds[e.To] = append(preds[e.To], e.From)
	}

	type dp struct {
		length int64
		prev   string
	}
	best := map[string]dp{}
	for _, n := range topoOrder(order, preds, byKey) {
		// Review runs are unattributed by construction; letting one relax as
		// its own chain made a 120-min review the "critical path" of a 30-min
		// execution. They stay on the graph and in review_busy_ms only.
		if n.Kind == "review" {
			continue
		}
		d := dp{length: n.DurationMS}
		for _, from := range preds[n.Key] {
			p, ok := best[from]
			// A predecessor whose own chain is empty (synthetic goal root, a
			// sub-goal that never ran) must still be traversed: `prev` already
			// points at the node before it, so chaining costs 0ms but keeps the
			// upstream work on the path. Guarding on p.length == 0 here dropped
			// every upstream node at such a gap (a 45764ms first step fell off
			// a 12-item chain). No window is invented — a windowless node simply
			// contributes 0.
			if !ok {
				continue
			}
			pn := byKey[from]
			// Time-order validity: source chain must end before target starts
			// (nodes without windows — goal root, not-yet-run — always chain).
			if pn.StartedAtMS > 0 && n.StartedAtMS > 0 && pn.StartedAtMS+pn.DurationMS > n.StartedAtMS {
				continue
			}
			// Chain length = predecessor chain + THIS node's busy time. Using
			// d.length here would double-count every fan-in predecessor.
			cand := p.length + n.DurationMS
			if cand > d.length || (cand == d.length && from < d.prev) {
				d = dp{length: cand, prev: from}
			}
		}
		best[n.Key] = d
	}

	path := []string{}
	var tail string
	var cpMS int64
	// Deterministic tail selection: walk the start-time-sorted order and break
	// equal-length ties by node key. Map iteration order must never decide the
	// critical path (400 runs of equal nodes previously split 361/39).
	for _, n := range order {
		b := best[n.Key]
		if b.length > cpMS || (b.length == cpMS && cpMS > 0 && (tail == "" || n.Key < tail)) {
			cpMS = b.length
			tail = n.Key
		}
	}
	for tail != "" {
		path = append([]string{tail}, path...)
		tail = best[tail].prev
	}
	// Only work items that actually carry busy time are工序. The synthetic goal
	// root, a sub-goal that never ran, and a machine verification round without
	// timing are traversed by the chain above (they keep the link to their
	// upstream) but contribute 0ms: listing them as critical工序 would inflate
	// depth past the real work-item count and highlight a node that never
	// consumed wall time.
	critical := make([]string, 0, len(path))
	for _, key := range path {
		if n, ok := byKey[key]; ok && n.DurationMS > 0 {
			critical = append(critical, key)
		}
	}
	g.Critical = critical
	for i := range g.Nodes {
		g.Nodes[i].CriticalShare = 0
		if cpMS > 0 {
			g.Nodes[i].CriticalShare = float64(g.Nodes[i].DurationMS) / float64(cpMS)
		}
	}

	// Aggregates. Review runs are real elapsed work but have no attribution
	// edge, so they are reported separately instead of inflating work_sum /
	// speedup / lane imbalance (a review node with no incoming edge would
	// otherwise become its own critical path and inflate speedup).
	var workSum, maxNode, wallStart, wallEnd, verifyBusy, reviewBusy int64
	lane := map[string]int64{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.DurationMS <= 0 {
			continue
		}
		if n.Kind == "review" {
			reviewBusy += n.DurationMS
			continue
		}
		workSum += n.DurationMS
		if n.DurationMS > maxNode {
			maxNode = n.DurationMS
		}
		if n.Kind == "verify" {
			verifyBusy += n.DurationMS
		}
		if n.AssigneeID != "" {
			lane[n.AssigneeID] += n.DurationMS
		}
		if n.StartedAtMS > 0 && (wallStart == 0 || n.StartedAtMS < wallStart) {
			wallStart = n.StartedAtMS
		}
		if n.EndedAtMS > wallEnd {
			wallEnd = n.EndedAtMS
		}
	}
	wall := wallEnd - wallStart
	if wall < 0 {
		wall = 0
	}

	// Parallel width peak: sweep of work intervals (review excluded).
	type iv struct {
		t     int64
		delta int
	}
	ints := []iv{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "review" {
			continue
		}
		if n.StartedAtMS > 0 && n.EndedAtMS > n.StartedAtMS {
			ints = append(ints, iv{t: n.StartedAtMS, delta: 1}, iv{t: n.EndedAtMS, delta: -1})
		}
	}
	sort.Slice(ints, func(i, j int) bool { return ints[i].t < ints[j].t })
	width, peak := 0, 0
	for _, x := range ints {
		width += x.delta
		if width > peak {
			peak = width
		}
	}

	var verifyRounds, rejRounds, consultCount int
	// verify_pass_rate (Phase D §13): share of verified work items that
	// passed verification on the FIRST round. Node.Reworks counts rejected
	// verification rounds, so a verified agent node with Reworks==0 passed
	// first-try. Computed from the graph (already assembled from
	// verification_result rows) — zero new instrumentation.
	verifyPassed, verifyTotal := 0, 0
	verifyMeasured, verifyUnmeasured := 0, 0
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "verify" {
			verifyRounds += n.Attempt
			rejRounds += n.Reworks
			verifyMeasured += n.MeasuredRounds
			verifyUnmeasured += n.UnmeasuredRounds
		}
		if n.Kind == "agent" && n.Status == "verified" {
			verifyTotal++
			if n.Reworks == 0 {
				verifyPassed++
			}
		}
		if n.Kind == "consult" {
			consultCount++
		}
	}
	// Rounds whose timing nobody recorded are UNMEASURED, not zero-cost: the
	// machine gate runs inside the daemon on the owner run's window. Counting
	// them keeps a 0% honest ("we did not time it") instead of claiming
	// verification is free (issue #13, D2). A hand-built graph may declare
	// Attempt without the split — the remainder is unmeasured, so the two
	// counters always add up to verify_rounds.
	if rest := verifyRounds - verifyMeasured - verifyUnmeasured; rest > 0 {
		verifyUnmeasured += rest
	}
	verifyCoverage := 0.0
	if covered := verifyMeasured + verifyUnmeasured; covered > 0 {
		verifyCoverage = float64(verifyMeasured) / float64(covered)
	}
	verifyScope := "unmeasured"
	if verifyMeasured+verifyUnmeasured > 0 {
		switch {
		case verifyUnmeasured == 0:
			verifyScope = "complete"
		case verifyMeasured == 0:
			verifyScope = "unmeasured"
		default:
			verifyScope = "partial"
		}
	}

	// idle_gap = actual wall - dependency-constrained minimum wall. The number
	// is the one the old serialization_loss_ms carried, but it measures IDLE
	// time (dispatch/gate/queue gaps), NOT a serialization penalty: work that
	// runs one after another because a dependency says so is required labour,
	// and calling it a loss made a 39-minute necessary chain read as
	// optimizable. The old key stays as a deprecated alias so RSI round
	// ledgers, AB metric keys and frozen reports keep reading a number
	// (issue #13, D1: keep the value, fix the name).
	idleGap := wall - cpMS
	if idleGap < 0 {
		idleGap = 0
	}
	// parallel_speedup is actual throughput vs. one-lane execution:
	// work_sum/wall. 1.0 = fully serial, 2.0 = two lanes' worth of work in
	// the same wall. The old work/cp ratio was an average parallelism along
	// the chain, not a speedup.
	speedup := 0.0
	if wall > 0 {
		speedup = float64(workSum) / float64(wall)
	}
	verifyOverhead := 0.0
	reviewOverhead := 0.0
	if workSum > 0 {
		// Numerator = MEASURED verification busy only. Unmeasured rounds
		// contribute no time (they are reported by coverage + scope, never by a
		// fabricated estimate), so a machine-only goal reads 0% WITH
		// verify_overhead_scope="unmeasured" — the pair is the honest answer.
		verifyOverhead = float64(verifyBusy) / float64(workSum)
		reviewOverhead = float64(reviewBusy) / float64(workSum)
	}
	var maxAttempts int
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "agent" && n.Attempt > maxAttempts {
			maxAttempts = n.Attempt
		}
	}
	rework := 0.0
	if verifyRounds > 0 {
		rework = float64(rejRounds) / float64(verifyRounds)
	}
	verifyPass := 0.0
	if verifyTotal > 0 {
		verifyPass = float64(verifyPassed) / float64(verifyTotal)
	}
	laneMax, laneSum := int64(0), int64(0)
	for _, v := range lane {
		if v > laneMax {
			laneMax = v
		}
		laneSum += v
	}
	laneImbalance := 0.0
	if len(lane) > 0 && laneSum > 0 {
		laneImbalance = float64(laneMax) / (float64(laneSum) / float64(len(lane)))
	}

	m := g.Metrics
	// metrics_schema_version = 2 (2026-11-24, issue #13): serialization_loss_ms
	// renamed to idle_gap_ms (old key kept as an alias) and the critical path
	// now chains through windowless nodes, so cp/depth/idle_gap are NOT
	// comparable with version-1 reports. Trend comparisons must respect this.
	m["metrics_schema_version"] = int64(2)
	m["node_count"] = len(g.Nodes)
	m["edge_count"] = len(g.Edges)
	m["depth"] = int64(len(g.Critical))
	m["work_sum_ms"] = workSum
	m["wall_ms"] = wall
	m["critical_path_ms"] = cpMS
	m["parallel_speedup"] = speedup
	m["idle_gap_ms"] = idleGap
	// serialization_loss_ms is DEPRECATED: same value as idle_gap_ms, kept only
	// so existing readers (rsi.go SerializationLossMS, ABMetricKeys, delta keys,
	// frozen rsi_report rows) do not break. New readers must use idle_gap_ms.
	m["serialization_loss_ms"] = idleGap
	m["parallel_width_peak"] = peak
	m["verify_overhead_ratio"] = verifyOverhead
	m["verify_busy_measured_ms"] = verifyBusy
	m["verify_measured_rounds"] = int64(verifyMeasured)
	m["verify_unmeasured_rounds"] = int64(verifyUnmeasured)
	m["verify_coverage_ratio"] = verifyCoverage
	// unmeasured | partial | complete. "unmeasured" means no round had a timing
	// source (machine gate inside the daemon), so the ratio above is a floor,
	// not a measurement (issue #13, D2).
	m["verify_overhead_scope"] = verifyScope
	m["review_busy_ms"] = reviewBusy
	m["review_overhead_ratio"] = reviewOverhead
	m["retry_amplification"] = float64(maxAttempts)
	m["verify_rounds"] = int64(verifyRounds)
	m["rework_ratio"] = rework
	m["verify_pass_rate"] = verifyPass
	m["consult_count"] = consultCount
	m["lane_count"] = len(lane)
	m["lane_imbalance"] = laneImbalance
	m["lanes"] = lane
}

// WorkflowGoalSummaryFromGraph compresses a graph into the list row shape.
func WorkflowGoalSummaryFromGraph(g *WorkflowGraph) WorkflowGoalSummary {
	s := WorkflowGoalSummary{GoalID: g.GoalID, NodeCount: len(g.Nodes), EdgeCount: len(g.Edges)}
	if v, ok := g.Metrics["critical_path_ms"].(int64); ok {
		s.CriticalPathMS = v
	}
	if v, ok := g.Metrics["parallel_speedup"].(float64); ok {
		s.ParallelSpeedup = v
	}
	if v, ok := g.Metrics["rework_ratio"].(float64); ok {
		s.ReworkRatio = v
	}
	for _, n := range g.Nodes {
		if n.Key == "goal" {
			s.GoalTitle, s.Status = n.Label, n.Status
			break
		}
	}
	return s
}
