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
// heuristic). handle: spec edges only — the source port the branch choice
// is encoded on (task done|failed, gate approve|reject|timeout, …). The
// canvas renders it so a conditional edge reads as conditional, not as an
// unconditional arrow (G4, 2026-11-28).
type WorkflowEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Handle string `json:"handle,omitempty"`
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
	var gTitle, gStatus, gAssigneeType, gAssigneeID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status,title,COALESCE(assignee_type,''),COALESCE(assignee_id,'') FROM goal WHERE id=?`,
		goalID).Scan(&gStatus, &gTitle, &gAssigneeType, &gAssigneeID); err != nil {
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

	// Blueprint gates are human decisions, not agent work: their holder
	// sub-goals never get a run (the human/auto decision IS the run — see
	// ResolveBlueprintGate), so a run-window lookup sees nothing for them.
	// The observable lifecycle lives in blueprint_node.resolved_config:
	// held_since (stamped by markGateHeldTx) and decided_at (stamped by
	// ResolveBlueprintGate). Those two timestamps feed three things (#38):
	//   1. gateDecidedMS — the latest observable completion event a
	//      dependent's ready_at falls back to, so waiting for the gate
	//      lands in dependency_wait_ms instead of being misread as
	//      dispatch delay;
	//   2. gateHoldMS — the human hold window (decided_at − held_since)
	//      reported as gate_wait_ms (the holder-run fallback below is
	//      legacy data only: by construction there is no holder run);
	//   3. the gate NODE added to the graph in the loop below, so explicit
	//      depends_on edges have both endpoints and the critical path can
	//      chain through the gate with 0 work.
	gateHolders := map[string]bool{}
	gateDecidedMS := map[string]int64{}
	gateHoldMS := map[string]int64{}
	if grows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id, COALESCE(resolved_config,'') FROM blueprint_node WHERE goal_id=? AND node_type='gate' AND COALESCE(sub_goal_id,'')!=''`, goalID); err == nil {
		for grows.Next() {
			var sgid, cfg string
			if err := grows.Scan(&sgid, &cfg); err != nil {
				break
			}
			gateHolders[sgid] = true
			var m map[string]any
			if json.Unmarshal([]byte(cfg), &m) == nil {
				if dec, ok := m["decided_at"].(string); ok {
					if decMS, ok2 := parseTimeMS(dec); ok2 {
						gateDecidedMS[sgid] = decMS
						if held, ok3 := m["held_since"].(string); ok3 {
							if heldMS, ok4 := parseTimeMS(held); ok4 && decMS > heldMS {
								gateHoldMS[sgid] = decMS - heldMS
							}
						}
					}
				}
			}
		}
		grows.Close()
	}

	// Lane roster: every agent that was eligible to take work. Lanes are
	// zero-filled over the roster so an idle teammate is visible; the old
	// map only held agents with busy time, which made "one lane idle" —
	// precisely the imbalance worth fixing — invisible to the rule.
	roster := []string{}
	addRoster := func(a string) {
		if a == "" {
			return
		}
		for _, x := range roster {
			if x == a {
				return
			}
		}
		roster = append(roster, a)
	}
	if gAssigneeType == "squad" && gAssigneeID != "" {
		var leader string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT COALESCE(leader_id,'') FROM squad WHERE id=?`, gAssigneeID).Scan(&leader)
		addRoster(leader)
		if mrows, err := s.st.DB().QueryContext(ctx,
			`SELECT member_id FROM squad_member WHERE squad_id=? AND member_type='agent'`, gAssigneeID); err == nil {
			for mrows.Next() {
				var m string
				if err := mrows.Scan(&m); err != nil {
					break
				}
				addRoster(m)
			}
			mrows.Close()
		}
	} else if gAssigneeType == "agent" {
		addRoster(gAssigneeID)
	}
	graph.Metrics["squad_size"] = len(roster)
	graph.Metrics["squad_agents"] = roster

	// Dispatch wait is capped against the ORCHESTRATION SPAN below, not the
	// busy wall: wall_ms only counts nodes with a positive busy window, so an
	// in-flight run (finished_at='') contributes nothing and a single running
	// sub-goal would report queue wait > wall (#11). The span runs from the
	// earliest ready moment to the latest sub-goal finish — or now while a run
	// is still in flight.
	var depWait, dispatchWait, gateWait, spanStart, spanEnd int64
	nowMS, _ := parseTimeMS(now())
	inFlight := false
	// readyOf returns the latest OBSERVABLE completion event on a
	// dependency's upstream chain (#38): the dependency's own run end, a
	// gate's decided_at, or — when the dependency has neither (merged away,
	// never ran) — the same quantity over ITS dependencies, recursively.
	// Before this fallback, a dependency without a run made ready_at
	// collapse back to the DEPENDENT's own created_at, so the whole "wait
	// for the upstream serial chain + wait for the human gate" was billed
	// to dispatch_wait_ms — 74% of wall on a goal whose real dispatch delay
	// was minutes. 0 = nothing observable upstream.
	var readyOf func(id string, seen map[string]bool) int64
	readyOf = func(id string, seen map[string]bool) int64 {
		if seen[id] {
			return 0 // hand-edited depends_on cycle: stop, do not stack
		}
		if w := wins[id]; w != nil && w.end > 0 {
			return w.end
		}
		if dec := gateDecidedMS[id]; dec > 0 {
			return dec
		}
		seen[id] = true
		defer delete(seen, id)
		var latest int64
		for _, up := range depsMap[id] {
			if t := readyOf(up, seen); t > latest {
				latest = t
			}
		}
		return latest
	}
	for _, id := range sgOrder {
		r := sgRows[id]
		if gateHolders[id] {
			// The gate is a graph NODE (#38): Kind="gate", zero work. Its
			// window is the decision instant — a zero-width marker, so it
			// never inflates parallel width or wall, but the critical-path
			// DP chains through it (zero-work chaining) and every explicit
			// depends_on edge now has both endpoints. gate_wait_ms = Σ
			// human hold windows (held→decided); the holder-run fallback
			// is legacy data only — by construction a gate holder never
			// gets a run.
			gn := WorkflowNode{Key: "sg:" + id, Kind: "gate", Label: r.title, SubGoalID: id, Status: r.status}
			if dec := gateDecidedMS[id]; dec > 0 {
				gn.StartedAtMS, gn.EndedAtMS = dec, dec
			}
			graph.Nodes = append(graph.Nodes, gn)
			graph.Edges = append(graph.Edges, WorkflowEdge{From: "goal", To: gn.Key, Kind: "created", Source: "explicit"})
			gateWait += gateHoldMS[id]
			if gateHoldMS[id] == 0 {
				if w := wins[id]; w != nil {
					gateWait += w.busy
				}
			}
			continue
		}
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
				// ready_at is anchored to the latest OBSERVABLE completion
				// event upstream: run end, gate decided_at, or the
				// dependency's own upstream chain (#38).
				if t := readyOf(dep, map[string]bool{}); t > readyMS {
					readyMS = t
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
	// Gate holders ARE graph nodes now, so their edges keep both directions:
	// upstream → gate (the old code emitted these against a nonexistent
	// node — dangling), and gate → dependent (the old code dropped these,
	// which severed the critical path at every human gate — #38).
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
		if gateHolders[a] {
			continue
		}
		wa := wins[a]
		if wa == nil || wa.end == 0 {
			continue
		}
		for _, b := range sgOrder {
			if a == b || gateHolders[b] {
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
		// Gate verdicts count like any other verification round
		// (ResolveBlueprintGate inserts one passed row per decision): the
		// old skip here made verify_rounds undercount every gated goal
		// (#38 — 6 reported, 7 actually passed). They land as UNMEASURED
		// rounds: the human decision is not timed as a run window.
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
			if _, ok := sgRows[sgid]; ok && !gateHolders[sgid] {
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
		if gateHolders[sgid] {
			continue
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
	// dependency_wait_ms is per-node accumulated wall clock (created→ready),
	// so across N waiting nodes it sums node-minutes and can exceed the span:
	// one minute of upstream work can be "waited on" by several downstream
	// nodes at once (#38, comment B). It is a per-node attribution total —
	// never compare it to wall_ms as if it were a span.
	graph.Metrics["dependency_wait_ms"] = depWait
	graph.Metrics["dispatch_wait_ms"] = dispatchWait
	// queue_wait_ms is a compatibility alias for dispatch_wait_ms.
	graph.Metrics["queue_wait_ms"] = dispatchWait
	graph.Metrics["gate_node_count"] = int64(len(gateHolders))

	// Sub-goals that never started executing: no run window at all and no
	// settled outcome. A blueprint gate HOLDER is excluded even before its
	// decision lands — by construction it never gets a run (the human/auto
	// decision is its run), so counting it would label a decision node "未执行".
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

	// Human wait has two sources: final delivery reviews (gate_decision rows,
	// outside the execution span) and blueprint gate hold windows
	// (held_since→decided_at from blueprint_node.resolved_config, inside the
	// span). Both are human wall-clock, so they are summed. The share
	// denominator is wall+wait so it can never exceed 100% — a DIFFERENT
	// question from idle_gap_share (idle/machine-wall, computed in
	// computeWorkflowMetrics): "what fraction of total elapsed time did
	// humans hold" vs "what fraction of machine wall was idle". The two
	// shares are deliberately not on the same denominator (#38, G8).
	wall := mi(graph.Metrics, "wall_ms")
	var reviewWaitMS int64
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision WHERE goal_id=?`, goalID).Scan(&reviewWaitMS)
	humanWaitMS := reviewWaitMS + gateWait
	graph.Metrics["gate_wait_ms"] = gateWait
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
	var workSum, maxNode, wallStart, wallEnd int64
	var agentBusy, verifyBusy, consultBusy, reviewBusy int64
	lane := map[string]int64{}
	// Zero-fill over the roster so an idle agent is a visible lane. Without a
	// goal assignee (old goals / tests) fall back to busy agents only.
	if roster, ok := g.Metrics["squad_agents"].([]string); ok {
		for _, a := range roster {
			lane[a] = 0
		}
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.DurationMS <= 0 {
			continue
		}
		switch n.Kind {
		case "review":
			reviewBusy += n.DurationMS
			continue
		case "verify":
			verifyBusy += n.DurationMS
		case "consult":
			consultBusy += n.DurationMS
		case "agent":
			agentBusy += n.DurationMS
			if n.AssigneeID != "" {
				lane[n.AssigneeID] += n.DurationMS
			}
		}
		workSum += n.DurationMS
		if n.DurationMS > maxNode {
			maxNode = n.DurationMS
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
	idleGapShare := 0.0
	if wall > 0 {
		idleGapShare = float64(idleGap) / float64(wall)
	}
	agentBusyShare := 0.0
	if workSum > 0 {
		agentBusyShare = float64(agentBusy) / float64(workSum)
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
	// metrics_schema_version = 3 (2026-11-28, issue #38): blueprint gate
	// holders are graph NODES now (Kind="gate", zero work), so node_count
	// includes them and explicit edges through gates have endpoints — the
	// critical path chains through human gates instead of breaking there.
	// ready_at falls back to the latest observable upstream event (gate
	// decided_at / run end), re-attributing the old misread dispatch_wait
	// into dependency_wait; gate_wait_ms now measures held→decided windows;
	// verify_rounds counts gate verdicts. NONE of these are comparable with
	// version-2 reports — baseline() filters samples on this version.
	m["metrics_schema_version"] = int64(3)
	m["node_count"] = len(g.Nodes)
	m["edge_count"] = len(g.Edges)
	m["depth"] = int64(len(g.Critical))
	m["work_sum_ms"] = workSum
	m["wall_ms"] = wall
	m["critical_path_ms"] = cpMS
	m["parallel_speedup"] = speedup
	m["idle_gap_ms"] = idleGap
	m["idle_gap_share"] = idleGapShare
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
	m["agent_busy_ms"] = agentBusy
	m["agent_busy_share"] = agentBusyShare
	m["verify_busy_ms"] = verifyBusy
	m["consult_busy_ms"] = consultBusy
	m["squad_size"] = len(lane)
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
