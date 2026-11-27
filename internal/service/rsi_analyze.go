package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/logging"
)

// Structure analysis (W-RSI). Input: the assembled WorkflowGraph + entity
// tables. Output: one rsi_report (summary/metrics/baseline/frozen workflow)
// + zero or more rsi_findings, one per fired rule. All rules are
// deterministic thresholds over the metrics — no LLM in the loop, nothing
// executes by itself. Scope (user decision 2026-11-08): T1 structure +
// T2 dispatch + T3 info-flow only; node-internal strategy and platform
// parameters are out of optimization scope (observed at most); the meta
// layer is never auto-changed.
//
// Nine rule families (docs/research/rsi-summary.md §4.3):
//
//	structure:  serial_chain, lost_parallelism, split_candidate, merge_candidate
//	dispatch:   wrong_assignee (lane imbalance)
//	info-flow:  ping_pong (consult loops), brief_quality (rework magnet),
//	            consult_repeat (mid-execution clarification)
//	node-level: retry_amplification, human_wait (entity-level, not spans)
const (
	ruleSerialChainThreshold = 4    // ≥4 agent nodes on the critical path (verify/review hops excluded)
	ruleSerialChainWallShare = 0.60 // chain holds ≥60% of the wall clock
	ruleSplitCount           = 1    // ≥1 heavy node — every heavy node is a split candidate
	ruleSplitShare           = 0.50 // node busy ≥50% of agent_busy_ms (not critical path)
	ruleLostParallelGain     = 2.0  // theoretical speedup ≥2×
	ruleLostSerialShare      = 0.30 // realized serialization loss >30% of wall
	ruleLostAgentBusyShare   = 0.50 // agent work dominates total work (a dispatch problem, not just serialization)
	ruleMergeWall            = 3    // ≥3 mergeable leaves
	ruleMergeSpread          = 0.60 // end spread ≤60% of the longest leaf (the leaves overlap in time)
	ruleMergeAddedWallMS     = 300000
	ruleMergeAddedWallShare  = 0.10
	ruleLaneImbalance        = 2.0    // busiest lane ≥2× the median of the other lanes
	ruleLaneMinBusyMS        = 300000 // ignore tiny imbalances below 5 min
	rulePingPongPairs        = 2      // ≥2 turnarounds in EACH direction (total ≥4)
	ruleReworkRatio          = 0.5
	ruleReworkMinSubGoals    = 2 // at least 2 affected sub-goals
	ruleReworkMinRejects     = 3 // and at least 3 rejected rounds
	ruleConsultRepeat        = 2 // ≥2 mid-execution consults for the same sub-goal
	ruleRetryAmplification   = 3.0
	ruleHumanWaitShare       = 0.30
	ruleHumanWaitFloorMS     = 600000  // wait must also exceed 10 min
	ruleHumanWaitAbsoluteMS  = 3600000 // OR absolute: ≥1h of human wait fires even when a long wall dilutes the share (P5, issue #31: R2 waited 86 min but 225 min wall held the share at 27.7%)
	ruleDispatchWaitShare    = 0.15
	ruleDispatchWaitFloorMS  = 300000 // dispatch delay must exceed 5 min to be a signal, not noise
)

// Finding scores share one scale so the "top finding" ordering means
// something: severity tier + a bounded magnitude bonus (0..10). The old
// per-rule products ranged into the thousands and let human_wait (seconds)
// and plan_deviation (fixed 80) permanently dominate the ranking.
const (
	scoreBaseCritical = 90.0
	scoreBaseWarn     = 60.0
	scoreBaseInfo     = 30.0
)

// ruleScore = severity base + 10 × clamp01((observed−threshold)/threshold).
// At the threshold the bonus is 0; at 2× the threshold it saturates at 10.
func ruleScore(base, observed, threshold float64) float64 {
	if threshold <= 0 {
		return base
	}
	r := (observed - threshold) / threshold
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return base + 10*r
}

// Analyze is idempotent: it deletes the previous report+findings for the
// session and rebuilds them, so Retry always converges.
func (s *RSIService) Analyze(ctx context.Context, sessionID string) error {
	var goalID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT goal_id FROM training_session WHERE id=?`, sessionID).Scan(&goalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	graph, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		return fmt.Errorf("build workflow: %w", err)
	}
	graph.Frozen = true
	metrics := graph.Metrics
	baseline := s.baseline(ctx, sessionID)

	findings := s.structureFindings(ctx, goalID, graph)
	findings = append(findings, s.dispatchFindings(ctx, goalID, graph)...)
	findings = append(findings, s.infoFlowFindings(ctx, goalID, graph)...)
	findings = append(findings, s.nodeFindings(ctx, goalID, graph, metrics, baseline)...)
	// Plan-vs-actual is a first-class analysis dimension (BUG-4): a run that
	// deviated from its compiled spec must enter the training loop.
	findings = append(findings, s.planAlignmentFindings(ctx, goalID)...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Score > findings[j].Score })
	for i := range findings {
		findings[i].ID = newID()
		findings[i].Status = "open"
		findings[i].CreatedAt = now()
	}

	summary := summarize(graph, findings)
	narr := narrative(graph, findings, baseline)

	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM rsi_finding WHERE report_id IN (SELECT id FROM rsi_report WHERE session_id=?)`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rsi_report WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	reportID := newID()
	wfJSON, _ := json.Marshal(graph)
	mj, _ := json.Marshal(metrics)
	bj, _ := json.Marshal(baseline)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rsi_report (id,session_id,status,summary,metrics_json,baseline_json,workflow_json,narrative,created_at,finished_at)
		 VALUES (?,?, 'ready', ?,?,?,?,?,?,?)`,
		reportID, sessionID, summary, string(mj), string(bj), string(wfJSON), narr, now(), now()); err != nil {
		return err
	}
	for i := range findings {
		findings[i].ReportID = reportID
		ev, _ := json.Marshal(findings[i].Evidence)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?, 'open', ?)`,
			findings[i].ID, reportID, findings[i].Category, findings[i].Severity, findings[i].Confidence,
			findings[i].Title, findings[i].Explanation, findings[i].Suggestion, string(ev), findings[i].Score, findings[i].CreatedAt); err != nil {
			return err
		}
	}
	var cpMS int64
	if v, ok := metrics["critical_path_ms"].(int64); ok {
		cpMS = v
	}
	speedup, _ := metrics["parallel_speedup"].(float64)
	if _, err := tx.ExecContext(ctx,
		`UPDATE training_session SET node_count=?, critical_path_ms=?, parallel_speedup=?, finding_count=?
		 WHERE id=?`,
		len(graph.Nodes), cpMS, speedup, len(findings), sessionID); err != nil {
		return err
	}

	// Insight-panel ledger: one optimization round per analysis run, numbered
	// across a comparable cohort (each R<n> is a new goal/session, so a
	// per-session MAX always produced round_no=1 and an empty delta).
	//
	// Phase C (D3/§8.8): a workflow's rounds are numbered per OWNING SQUAD, so
	// a delta compares the same workflow's topology across projects. An
	// auto-decomposed session has no workflow, so its cohort stays the project
	// — grouping those by squad would compare unrelated shapes.
	var squadID, workflowID, domainID string
	_ = tx.QueryRowContext(ctx,
		`SELECT `+squadSnapshotSQL+`, `+workflowSnapshotSQL+`, COALESCE(g.domain_id,'')
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id
		 LEFT JOIN blueprint_version gbv ON gbv.id=g.blueprint_version_id
		 WHERE ts.id=?`, sessionID).Scan(&squadID, &workflowID, &domainID)
	scope, scopeArgs := roundScope(squadID, workflowID, domainID)
	var prevNo, sessionRounds int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(r.round_no),0) FROM rsi_round r
		 JOIN training_session ts ON ts.id=r.session_id
		 JOIN goal g ON g.id=ts.goal_id
		 LEFT JOIN blueprint_version gbv ON gbv.id=g.blueprint_version_id
		 WHERE `+scope, scopeArgs...).Scan(&prevNo); err != nil {
		return fmt.Errorf("round scope: %w", err)
	}
	_ = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_round WHERE session_id=?`, sessionID).Scan(&sessionRounds)
	roundNo := prevNo + 1
	trigger := "manual_retry"
	if sessionRounds == 0 {
		trigger = "terminal"
	}
	var prevMetrics string
	if prevNo > 0 {
		_ = tx.QueryRowContext(ctx,
			`SELECT r.metrics_json FROM rsi_round r
			 JOIN training_session ts ON ts.id=r.session_id
			 JOIN goal g ON g.id=ts.goal_id
			 LEFT JOIN blueprint_version gbv ON gbv.id=g.blueprint_version_id
			 WHERE `+scope+` ORDER BY r.created_at DESC LIMIT 1`, scopeArgs...).Scan(&prevMetrics)
	}
	criticalCount := 0
	for _, f := range findings {
		if f.Severity == "critical" {
			criticalCount++
		}
	}
	topCat, topTitle := "", ""
	if len(findings) > 0 {
		topCat, topTitle = findings[0].Category, findings[0].Title
	}
	fj, _ := json.Marshal(findings)
	dj, _ := json.Marshal(computeRoundDelta(decodeMap(prevMetrics), metrics))
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rsi_round (id,session_id,goal_id,round_no,trigger,metrics_json,baseline_json,delta_json,
		  finding_count,critical_count,top_category,top_title,findings_json,summary,narrative,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), sessionID, goalID, roundNo, trigger, string(mj), string(bj), string(dj),
		len(findings), criticalCount, topCat, topTitle, string(fj), summary, narr, now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Phase D (proposal §13): deterministic finding→proposal translation.
	// LLM+P split — every RSI rule (deterministic) may emit a finding; the
	// translator (rsi_translate.go, also deterministic) turns each category
	// into a concrete spec patch; humans stay the gate (the proposal stays
	// pending until approved). Failures never break the analysis itself.
	//
	// Run BEFORE the rsi:ready event: the event is the "analysis (including
	// deterministic translation) is complete" signal, so a client that reacts
	// to it must not find an empty proposal list (the blank-proposal race
	// reported in the 2026-11-21 blueprint-self-iteration use case).
	blocks := s.draftAutoProposals(ctx, goalID, graph, findings)
	// P3 (issue #31): rounds after a pending round-1 proposal used to draft
	// nothing with zero trace. The block list is the "why" — surface it on the
	// goal feed so the human reviewer sees WHICH proposal to approve/reject
	// instead of an empty proposal panel.
	if len(blocks) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "🔒 本轮分析有 %d 条可执行优化建议未生成提案——待审提案占用了相关节点（或同名提案已存在）：\n", len(blocks))
		for i, blk := range blocks {
			switch blk.Reason {
			case "touched":
				fmt.Fprintf(&b, "%d. [%s]《%s》：待审提案 %s 已占用节点/边 %s\n", i+1, blk.FindingCategory, blk.ProposalTitle, blk.OwnerProposalID, blk.Key)
			case "same-title":
				fmt.Fprintf(&b, "%d. [%s]《%s》：同名提案 %s 已存在\n", i+1, blk.FindingCategory, blk.ProposalTitle, blk.OwnerProposalID)
			default:
				fmt.Fprintf(&b, "%d. [%s]《%s》：%s\n", i+1, blk.FindingCategory, blk.ProposalTitle, blk.Reason)
			}
		}
		b.WriteString("请先审批或拒绝上述待审提案，下一轮分析即可继续自动转化。")
		if _, err := s.st.DB().ExecContext(ctx,
			`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,'system','',NULL,?,?)`,
			newID(), goalID, b.String(), now()); err != nil {
			logging.Warnf("[rsi] surface translation blocks on goal %s: %v", goalID, err)
		}
	}
	// 'ready' means the whole analysis pipeline settled — findings persisted
	// AND deterministic proposals drafted. It is set after the tx (so a crash
	// leaves the session retryable) and before the event below, so readers of
	// training_session.status never observe a half-done analysis.
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='ready', finished_at=?, error='' WHERE id=?`, now(), sessionID); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "rsi:ready", Payload: map[string]any{
		"session_id": sessionID, "goal_id": goalID, "findings": len(findings), "round_no": roundNo,
	}})
	return nil
}

// computeRoundDelta compares the insight-panel headline metrics between two
// consecutive rounds: {key: {from, to, pct}} for every key present in both.
// pct is omitted when from == 0 (a 0→x jump has no meaningful percentage).
// roundScope builds the WHERE clause (and args) that defines which earlier
// rounds this one is comparable to: same owning squad AND same workflow when
// the run used a workflow; the project when it auto-decomposed. Historical
// rounds keep the round_no assigned under the old per-domain rule — a
// renumbering would silently rewrite history the review already showed.
func roundScope(squadID, workflowID, domainID string) (string, []any) {
	if workflowID != "" {
		return squadSnapshotSQL + `=? AND ` + workflowSnapshotSQL + `=?`,
			[]any{squadID, workflowID}
	}
	return `COALESCE(g.domain_id,'')=? AND ` + workflowSnapshotSQL + `=''`,
		[]any{domainID}
}

func computeRoundDelta(prev, cur map[string]any) map[string]any {
	out := map[string]any{}
	if len(prev) == 0 {
		return out
	}
	for _, k := range []string{"parallel_speedup", "critical_path_ms", "serialization_loss_ms",
		"dependency_wait_ms", "dispatch_wait_ms", "verify_overhead_ratio", "review_overhead_ratio",
		"rework_ratio", "consult_count", "human_wait_ms", "retry_amplification"} {
		pf, ok1 := num(prev[k])
		cf, ok2 := num(cur[k])
		if !ok1 || !ok2 {
			continue
		}
		d := map[string]any{"from": pf, "to": cf}
		if pf != 0 {
			d["pct"] = (cf - pf) / pf
		}
		out[k] = d
	}
	return out
}

// ── helpers over metrics ────────────────────────────────────────────────

// mi/mf read a metric that may have come from two places: the in-memory graph
// built during analysis (int64/float64 as computed) or a json.Marshal'd graph
// re-read later (rsi_report.workflow_json, rsi_round.delta_json) where every
// number comes back as float64. Accepting only one shape made the same metric
// silently read as 0 on the other path — proposals derived from a frozen graph
// then looked like "no measurable signal".
func mi(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	}
	return 0
}
func mf(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// agentNodes returns the agent (sub-goal) nodes.
func agentNodes(g *WorkflowGraph) []WorkflowNode {
	out := []WorkflowNode{}
	for _, n := range g.Nodes {
		if n.Kind == "agent" {
			out = append(out, n)
		}
	}
	return out
}

func nodeByKey(g *WorkflowGraph, key string) *WorkflowNode {
	for i := range g.Nodes {
		if g.Nodes[i].Key == key {
			return &g.Nodes[i]
		}
	}
	return nil
}

// metricLanes decodes the lanes metric. Live graphs carry map[string]int64;
// after a JSON round-trip through rsi_report it is map[string]any.
func metricLanes(m map[string]any) map[string]int64 {
	out := map[string]int64{}
	switch v := m["lanes"].(type) {
	case map[string]int64:
		for k, n := range v {
			out[k] = n
		}
	case map[string]any:
		for k, x := range v {
			if n, ok := num(x); ok {
				out[k] = int64(n)
			}
		}
	}
	return out
}

// sameExecPair reports whether every leaf is executed by the same agent and
// verified by the same verifier — a merge across different executors would
// silently move one person's work onto another.
func sameExecPair(g *WorkflowGraph, leaves []WorkflowNode) bool {
	verifier := map[string]string{}
	for _, n := range g.Nodes {
		if n.Kind == "verify" && n.SubGoalID != "" {
			verifier[n.SubGoalID] = n.AssigneeID
		}
	}
	firstAssignee := leaves[0].AssigneeID
	firstVerifier := verifier[leaves[0].SubGoalID]
	for _, n := range leaves[1:] {
		if n.AssigneeID != firstAssignee || verifier[n.SubGoalID] != firstVerifier {
			return false
		}
	}
	return true
}

// ── T1 structure rules ──────────────────────────────────────────────────

func (s *RSIService) structureFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}
	m := g.Metrics
	cp := mi(m, "critical_path_ms")
	work := mi(m, "work_sum_ms")
	wallMS := mi(m, "wall_ms")
	agentBusy := mi(m, "agent_busy_ms")
	if agentBusy <= 0 {
		agentBusy = work
	}

	// serial_chain: a long forced chain of EXECUTION nodes. Verification hops
	// are structural, not serialization: counting them let two 30-min tasks
	// plus their verifications pass as a "4-node chain".
	agentChain := 0
	chainKeys := []string{}
	for _, k := range g.Critical {
		n := nodeByKey(g, k)
		if n != nil && n.Kind == "agent" {
			agentChain++
			chainKeys = append(chainKeys, k)
		}
	}
	cpShare := 0.0
	if wallMS > 0 {
		cpShare = float64(cp) / float64(wallMS)
	}
	if agentChain >= ruleSerialChainThreshold && cpShare >= ruleSerialChainWallShare {
		out = append(out, RsiFinding{
			Category: "serial_chain", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("关键路径为 %d 个执行节点串行链", agentChain),
			Explanation: fmt.Sprintf("关键路径含 %d 个执行节点（另有验证/咨询跳数），链长 %s，占墙钟 %.0f%%。若链上存在人为引入的依赖（本应并行），这是最大的提速杠杆。",
				agentChain, humanMS(cp), 100*cpShare),
			Suggestion: "逐边检查链上的 depends_on/seq：拆掉仅因「先做完再做」习惯而引入的依赖，把无数据依赖的节点改为并行派发。",
			Evidence: []string{fmt.Sprintf("agent_chain=%d critical_path=%s", agentChain, strings.Join(chainKeys, " → ")),
				fmt.Sprintf("critical_path_ms=%d wall_ms=%d cp_share=%.2f", cp, wallMS, cpShare)},
			Score: ruleScore(scoreBaseWarn, cpShare, ruleSerialChainWallShare),
		})
	}

	// split_candidate: one node carries a large share of agent busy time. The
	// denominator is agent_busy_ms so verification/consult/integration time no
	// longer crowds the candidate out of the old 45%-of-critical-path window.
	heavy := []WorkflowNode{}
	heaviest := 0.0
	for _, k := range g.Critical {
		n := nodeByKey(g, k)
		if n == nil || n.Kind != "agent" || agentBusy <= 0 {
			continue
		}
		share := float64(n.DurationMS) / float64(agentBusy)
		if share >= ruleSplitShare {
			heavy = append(heavy, *n)
			if share > heaviest {
				heaviest = share
			}
		}
	}
	if len(heavy) >= ruleSplitCount {
		names, ev := []string{}, []string{}
		for _, n := range heavy {
			share := float64(n.DurationMS) / float64(agentBusy)
			names = append(names, n.Label)
			ev = append(ev, fmt.Sprintf("%s: %s（占 agent busy %.0f%%）", n.Label, humanMS(n.DurationMS), 100*share))
		}
		out = append(out, RsiFinding{
			Category: "split_candidate", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("%d 个重型节点占执行时长 ≥%d%%，可拆分并行", len(heavy), int(ruleSplitShare*100)),
			Explanation: fmt.Sprintf("节点 [%s] 各自占 agent busy ≥%d%%。若其内部是「多个独立子项顺序处理」，拆成并行子节点可把该段时长从求和降为取最大。",
				strings.Join(names, "、"), int(ruleSplitShare*100)),
			Suggestion: "把这些节点的产出物按数据依赖拆分：每份独立子项开一个 sub_goal（声明 depends_on 只指向真正的前置），由同一 squad 并行认领。",
			Evidence:   ev,
			Score:      ruleScore(scoreBaseWarn, heaviest, ruleSplitShare),
		})
	}

	// lost_parallelism: the realized serialization/queue loss is large AND the
	// work is mostly agent execution, so the remedy is dispatch/dependency
	// shaping rather than "the machine was slow".
	if cp > 0 && work > 0 {
		gain := float64(work) / float64(cp)
		idleShare := mf(m, "idle_gap_share")
		agentShare := 0.0
		if work > 0 {
			agentShare = float64(agentBusy) / float64(work)
		}
		if gain >= ruleLostParallelGain && idleShare > ruleLostSerialShare && agentShare >= ruleLostAgentBusyShare {
			sugg := "对照 seq 边（虚线）复核：时间先后 ≠ 数据依赖。把不共享读写对象的相邻节点改为可并行。"
			if laneCount := mi(m, "lane_count"); laneCount <= 1 {
				sugg = "只有 1 条执行通道：先增加可并行认领的执行者/通道，再拆依赖；否则拆出的节点仍会排队。"
			}
			out = append(out, RsiFinding{
				Category: "lost_parallelism", Severity: "info", Confidence: "L1",
				Title: fmt.Sprintf("理论并行收益 %.1f×，空闲间隙 %.0f%%", gain, 100*idleShare),
				Explanation: fmt.Sprintf("总工作量 %s，墙钟 %s，其中空闲间隙 %s（%.0f%% 墙钟）；执行型工作占 %.0f%%。收益差距主要来自依赖声明过强、派发过晚或人工等待，而不是机器本身慢。",
					humanMS(work), humanMS(wallMS), humanMS(mi(m, "idle_gap_ms")), 100*idleShare, 100*agentShare),
				Suggestion: sugg,
				Evidence: []string{
					fmt.Sprintf("gain=%.2f idle_gap_share=%.2f agent_busy_share=%.2f", gain, idleShare, agentShare),
					fmt.Sprintf("dependency_wait_ms=%d dispatch_wait_ms=%d lane_count=%d", mi(m, "dependency_wait_ms"), mi(m, "dispatch_wait_ms"), mi(m, "lane_count")),
				},
				Score: ruleScore(scoreBaseInfo, idleShare, ruleLostSerialShare),
			})
		}
	}

	// merge_candidate: several leaves overlap in time and are executed AND
	// verified by the same pair. Merging the cheapest ones must only add a
	// bounded serialization increment (5 min or 10% of wall) — the old rule
	// capped leaf DURATION, which excluded the small-but-serial leaves that
	// are exactly the ones worth merging.
	leaves := endingLeaves(g)
	if len(leaves) >= ruleMergeWall {
		sort.Slice(leaves, func(i, j int) bool { return leaves[i].EndedAtMS < leaves[j].EndedAtMS })
		spread := leaves[len(leaves)-1].EndedAtMS - leaves[0].EndedAtMS
		var maxDur int64
		for _, n := range leaves {
			if n.DurationMS > maxDur {
				maxDur = n.DurationMS
			}
		}
		if maxDur > 0 && float64(spread)/float64(maxDur) <= ruleMergeSpread && sameExecPair(g, leaves) {
			budget := int64(ruleMergeAddedWallMS)
			if shareBudget := int64(ruleMergeAddedWallShare * float64(wallMS)); shareBudget < budget {
				budget = shareBudget
			}
			asc := append([]WorkflowNode(nil), leaves...)
			sort.SliceStable(asc, func(i, j int) bool {
				if asc[i].DurationMS != asc[j].DurationMS {
					return asc[i].DurationMS < asc[j].DurationMS
				}
				return asc[i].Key < asc[j].Key
			})
			var added int64
			victims := []string{}
			for _, n := range asc {
				if len(victims) >= 2 || added+n.DurationMS > budget {
					continue
				}
				added += n.DurationMS
				victims = append(victims, n.Label)
			}
			if len(victims) > 0 {
				out = append(out, RsiFinding{
					Category: "merge_candidate", Severity: "info", Confidence: "L1",
					Title: fmt.Sprintf("末梢节点可合并：%d 个几乎同时收尾，合并省派发开销", len(leaves)),
					Explanation: fmt.Sprintf("节点 [%s] 在 %s 窗口内先后结束（最长 %s），且由同一执行者/验证者完成。合并最便宜的 %d 个后墙钟只增加 %s（预算 %s）。",
						shortNames(leaves), humanMS(spread), humanMS(maxDur), len(victims), humanMS(added), humanMS(budget)),
					Suggestion: "若这些节点共享上下文，合并为一个 sub_goal 的多产出物；仅当需要不同执行者并行时才保留拆分。",
					Evidence: []string{
						fmt.Sprintf("leaves=%d end_spread_ms=%d added_ms=%d budget_ms=%d", len(leaves), spread, added, budget),
						fmt.Sprintf("merge_candidates=%s", strings.Join(victims, "、")),
					},
					Score: ruleScore(scoreBaseInfo, float64(added), float64(budget)),
				})
			}
		}
	}
	return out
}

// ── T2 dispatch rules ───────────────────────────────────────────────────

func (s *RSIService) dispatchFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}
	m := g.Metrics
	lanes := metricLanes(m)
	if len(lanes) < 2 {
		return out
	}
	busiest, busyMS := "", int64(0)
	for a, v := range lanes {
		if v > busyMS || (v == busyMS && (busiest == "" || a < busiest)) {
			busiest, busyMS = a, v
		}
	}
	if busyMS < ruleLaneMinBusyMS {
		return out
	}
	others := []int64{}
	for a, v := range lanes {
		if a != busiest {
			others = append(others, v)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i] < others[j] })
	med := others[len(others)/2]
	share := 0.0
	if total := mi(m, "agent_busy_ms"); total > 0 {
		share = float64(busyMS) / float64(total)
	}
	fired := false
	score := scoreBaseWarn
	if med == 0 {
		fired = true
		score = ruleScore(scoreBaseWarn, share, 0.60)
	} else if float64(busyMS) >= ruleLaneImbalance*float64(med) {
		fired = true
		score = ruleScore(scoreBaseWarn, float64(busyMS), ruleLaneImbalance*float64(med))
	} else if share >= 0.60 && len(lanes) >= 3 {
		fired = true
		score = ruleScore(scoreBaseWarn, share, 0.60)
	}
	if !fired {
		return out
	}
	out = append(out, RsiFinding{
		Category: "wrong_assignee", Severity: "warn", Confidence: "L1",
		Title: fmt.Sprintf("负载不均：%s 占用 %.0f%% 的执行时长", busiest, 100*share),
		Explanation: fmt.Sprintf("%d 条执行通道（含 0 负载的空闲成员）：最忙通道 %s 执行 %s，占 agent busy %.0f%%，其余通道中位数 %s。关键路径大概率压在同一个执行者身上。",
			len(lanes), busiest, humanMS(busyMS), 100*share, humanMS(med)),
		Suggestion: "把最忙通道上可独立的 sub_goal 分派给空闲成员（squad 页编排图可直观看通道分布）；或在派发时按 in-flight 数选人。",
		Evidence: []string{
			fmt.Sprintf("busiest=%s busiest_ms=%d median_others_ms=%d share=%.2f lanes=%d", busiest, busyMS, med, share, len(lanes)),
			laneDump(m),
		},
		Score: score,
	})
	return out
}

// ── T3 info-flow rules ──────────────────────────────────────────────────

func (s *RSIService) infoFlowFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}
	m := g.Metrics

	// ping_pong: consult_request round trips. Reciprocity is required — one
	// side asking the same peer three times is "brief is thin", not ping-pong.
	counts := map[string]int{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT requester_agent_id,target_agent_id FROM consult_request WHERE goal_id=?`, goalID)
	if err == nil {
		for rows.Next() {
			var req, tgt string
			if err := rows.Scan(&req, &tgt); err != nil {
				continue
			}
			if req == "" || tgt == "" {
				continue
			}
			counts[req+"\x00"+tgt]++
		}
		rows.Close()
	}
	pairKeys := make([]string, 0, len(counts))
	for k := range counts {
		pairKeys = append(pairKeys, k)
	}
	sort.Strings(pairKeys)
	seen := map[string]bool{}
	for _, k := range pairKeys {
		if seen[k] {
			continue
		}
		parts := strings.SplitN(k, "\x00", 2)
		rev := parts[1] + "\x00" + parts[0]
		if counts[k] < rulePingPongPairs || counts[rev] < rulePingPongPairs {
			continue
		}
		seen[k], seen[rev] = true, true
		total := counts[k] + counts[rev]
		out = append(out, RsiFinding{
			Category: "ping_pong", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("咨询乒乓：%s 与 %s 往返 %d 次", parts[0], parts[1], total),
			Explanation: fmt.Sprintf("%s ↔ %s 各问 %d 次（合计 %d 次）。每次往返都是一整轮 run（排队+执行+等待），信息本可一次给全。",
				parts[0], parts[1], counts[k], total),
			Suggestion: "把第一次咨询的问题清单化（一次性列出全部待确认项）；或在派发时把对方上下文直接写进 sub_goal 描述，省掉来回。",
			Evidence:   []string{fmt.Sprintf("pair=%s↔%s each=%d,%d total=%d", parts[0], parts[1], counts[k], counts[rev], total)},
			Score:      ruleScore(scoreBaseWarn, float64(total), float64(2*rulePingPongPairs)),
		})
	}

	// brief_quality: verification keeps rejecting across MULTIPLE sub-goals —
	// the brief is the magnet. One noisy task with two rejects is not enough.
	rework := mf(m, "rework_ratio")
	reworked := []string{}
	rejects := 0
	maxReworks := 0
	for _, n := range g.Nodes {
		if n.Kind == "verify" && n.Reworks > 0 && n.Attempt >= 2 {
			rejects += n.Reworks
			if n.Reworks > maxReworks {
				maxReworks = n.Reworks
			}
			if r := sgLabel(g, n.SubGoalID); r != "" {
				reworked = append(reworked, fmt.Sprintf("%s（%d 轮 %d 拒）", r, n.Attempt, n.Reworks))
			}
		}
	}
	// Two shapes justify the rule: rework spread over multiple sub-goals
	// (a systemic brief problem), or one task rejected repeatedly (a single
	// badly-written brief). A lone reject on one round stays below the bar.
	if rework >= ruleReworkRatio && ((len(reworked) >= ruleReworkMinSubGoals && rejects >= ruleReworkMinRejects) || maxReworks >= 2) {
		out = append(out, RsiFinding{
			Category: "brief_quality", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("返工率 %.0f%%：多个任务简报疑似信息不足", rework*100),
			Explanation: fmt.Sprintf("验证轮次中 %.0f%% 被拒，涉及 %d 个任务、共 %d 次拒绝：%s。反复被拒通常不是执行问题，而是 brief 缺少验收标准或关键上下文。",
				rework*100, len(reworked), rejects, strings.Join(reworked, "；")),
			Suggestion: "在 sub_goal 描述里补：验收标准（verifier 按什么判 pass）、输入产出物路径、已知约束；必要时先 consult 再派发（前移而不是事后返工）。",
			Evidence:   reworked,
			Score:      ruleScore(scoreBaseWarn, rework, ruleReworkRatio),
		})
	}

	// consult_repeat: the SAME sub-goal asked mid-execution repeatedly. The old
	// rule counted the whole goal and said nothing about which brief to fix.
	type sgConsults struct {
		sg string
		n  int
	}
	list := []sgConsults{}
	crows, err := s.st.DB().QueryContext(ctx,
		`SELECT r.sub_goal_id, COUNT(*) FROM consult_request cr JOIN run r ON r.id=cr.requester_run_id
		 WHERE cr.goal_id=? AND r.sub_goal_id!='' GROUP BY r.sub_goal_id HAVING COUNT(*) >= ?
		 ORDER BY COUNT(*) DESC`, goalID, ruleConsultRepeat)
	if err == nil {
		for crows.Next() {
			var x sgConsults
			if err := crows.Scan(&x.sg, &x.n); err != nil {
				continue
			}
			list = append(list, x)
		}
		crows.Close()
	}
	if len(list) > 0 {
		total := 0
		ev := []string{}
		for _, x := range list {
			total += x.n
			ev = append(ev, fmt.Sprintf("%s=%d", sgLabel(g, x.sg), x.n))
		}
		maxN := list[0].n
		out = append(out, RsiFinding{
			Category: "consult_repeat", Severity: "info", Confidence: "L1",
			Title:       fmt.Sprintf("%d 个子任务在执行中途反复咨询（共 %d 次）", len(list), total),
			Explanation: "同一个 sub_goal 的执行者边干边问，说明派发时上下文没给全。中途咨询把该节点的墙钟拉长为「执行+等待回复」。",
			Suggestion:  "把这些咨询问题前移到 sub_goal 创建前（先 consult 再拆任务），或在描述中链接参考产出物。",
			Evidence:    ev,
			Score:       ruleScore(scoreBaseInfo, float64(maxN), float64(ruleConsultRepeat)),
		})
	}
	return out
}

// ── node-level (entity-derived) rules ───────────────────────────────────

func (s *RSIService) nodeFindings(ctx context.Context, goalID string, g *WorkflowGraph, metrics, baseline map[string]any) []RsiFinding {
	out := []RsiFinding{}

	// retry_amplification: some node ran far more than once. Two retries
	// (attempt=3) is the threshold: the first retry may be a transient
	// failure, the second means the failure mode repeats.
	attempts := 0
	for _, n := range agentNodes(g) {
		attempts += n.Attempt
	}
	nodes := len(agentNodes(g))
	maxAttempts := int(mf(metrics, "retry_amplification"))
	if nodes > 0 && float64(maxAttempts) >= ruleRetryAmplification {
		conf := "L1"
		if b := numOr(baseline["retry_amplification"]); b > 0 && float64(maxAttempts) > b {
			conf = "L2"
		}
		out = append(out, RsiFinding{
			Category: "retry_amplification", Severity: "warn", Confidence: conf,
			Title:       fmt.Sprintf("重试放大：单个子任务最多尝试 %d 次", maxAttempts),
			Explanation: fmt.Sprintf("%d 个子任务共 %d 次执行。重试通常意味着失败模式重复（环境/凭据/简报），重试本身不修复原因。", nodes, attempts),
			Suggestion:  "查看失败 sub_goal 的 cancel_reason/错误：若是可自愈类（依赖缺失、锁竞争），把前置检查加进 brief；若是权限类，改为派发前校验。",
			Evidence:    []string{fmt.Sprintf("max_attempts=%d runs=%d nodes=%d", maxAttempts, attempts, nodes)},
			Score:       ruleScore(scoreBaseWarn, float64(maxAttempts), ruleRetryAmplification),
		})
	}

	// human_wait: both final delivery reviews (gate_decision) and blueprint
	// gate decisions (holder run windows) are human wall-clock. Two trigger
	// shapes (P5, issue #31): share ≥30% (with a 10-min floor against tiny
	// goals), OR absolute ≥1h — a 225-minute wall clock can hold the share
	// below 30% while the humans still burned 86 minutes doing nothing.
	humanMSv := mi(metrics, "human_wait_ms")
	wall := mi(metrics, "wall_ms")
	gateWait := mi(metrics, "gate_wait_ms")
	share := mf(metrics, "human_wait_share")
	shareFired := share >= ruleHumanWaitShare && humanMSv >= ruleHumanWaitFloorMS
	absFired := humanMSv >= ruleHumanWaitAbsoluteMS
	if shareFired || absFired {
		out = append(out, RsiFinding{
			Category: "human_wait", Severity: "info", Confidence: "L1",
			Title: fmt.Sprintf("人工等待占总时长 %.0f%%", 100*share),
			Explanation: fmt.Sprintf("人工决策/审查累计 %s（其中蓝图 gate 等待 %s，交付审查 %s），机器执行墙钟 %s（合计 %s）。这是工作流里最大的非机器等待。",
				humanMS(humanMSv), humanMS(gateWait), humanMS(humanMSv-gateWait), humanMS(wall), humanMS(wall+humanMSv)),
			Suggestion: "把高频 gate 的证据包固化成模板（结论+差异+回滚方案置顶），缩短人工判读时间；纯技术性 gate 可评估改为自动通过+事后抽查。",
			Evidence: []string{
				fmt.Sprintf("human_wait_ms=%d gate_wait_ms=%d wall_ms=%d share=%.2f", humanMSv, gateWait, wall, share),
				fmt.Sprintf("trigger=%s", map[bool]string{true: "share", false: "absolute"}[shareFired]),
			},
			Score: ruleScore(scoreBaseInfo, share, ruleHumanWaitShare),
		})
	}

	// dispatch_wait (P5, issue #31): pure orchestration delay — started_at
	// minus ready_at, dependency gating excluded. It was collected
	// (dispatch_wait_ms) and fed into lost_parallelism evidence but had NO
	// rule of its own, so a scheduler that just dispatched late produced
	// findings about "too many dependencies" instead of "dispatch is slow".
	if dw := mi(metrics, "dispatch_wait_ms"); wall > 0 && dw >= ruleDispatchWaitFloorMS {
		if dwShare := float64(dw) / float64(wall); dwShare >= ruleDispatchWaitShare {
			out = append(out, RsiFinding{
				Category: "dispatch_wait", Severity: "info", Confidence: "L1",
				Title:       fmt.Sprintf("派发等待占墙钟 %.0f%%（%s）", 100*dwShare, humanMS(dw)),
				Explanation: fmt.Sprintf("子任务就绪到开跑的派发延迟累计 %s，占墙钟 %.0f%%（墙钟 %s）。该指标不含依赖门控（dependency_wait_ms 单列）。派发慢通常来自并发队列、lane 争用或人工/系统派发节奏，而不是图结构问题。",
					humanMS(dw), 100*dwShare, humanMS(wall)),
				Suggestion: "检查并发派发上限与 lane 通道数：dispatch 慢而依赖不重时，提高并行认领的通道数或预派发（ready 即派发）比改图更有效。",
				Evidence: []string{
					fmt.Sprintf("dispatch_wait_ms=%d wall_ms=%d share=%.2f lane_count=%d", dw, wall, dwShare, mi(metrics, "lane_count")),
				},
				Score: ruleScore(scoreBaseInfo, dwShare, ruleDispatchWaitShare),
			})
		}
	}
	return out
}

// ── baseline & presentation ─────────────────────────────────────────────

// baseline returns the median structure metrics of the last N ready
// sessions (excluding this one). <5 samples → empty map (no comparison
// claims until there is history to compare against).
func (s *RSIService) baseline(ctx context.Context, sessionID string) map[string]any {
	// Same-domain history only: comparing a benchmark task against an
	// unrelated goal's metrics makes L2 claims meaningless.
	// P7 (issue #31): fault-window sessions are skipped as samples. Live:
	// R3's analysis ran inside the 19:43-19:54 quota outage, so its
	// dispatch_wait/wall/retry numbers describe the OUTAGE, not the
	// blueprint — letting them into the median would shift every future
	// comparison toward the broken window. A session whose goal had ANY
	// non-retryable (quota/billing) failed run is marked degraded here.
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT rp.metrics_json, g.id FROM rsi_report rp
		 JOIN training_session ts ON ts.id=rp.session_id
		 JOIN goal g ON g.id=ts.goal_id
		 WHERE rp.status='ready' AND rp.session_id!=?
		   AND COALESCE(g.domain_id,'')=(
		     SELECT COALESCE(g2.domain_id,'') FROM training_session t2 JOIN goal g2 ON g2.id=t2.goal_id WHERE t2.id=?)
		 ORDER BY rp.created_at DESC LIMIT 20`, sessionID, sessionID)
	if err != nil {
		return map[string]any{}
	}
	defer rows.Close()
	// Collect BEFORE the fault filter: the in-memory test store pins ONE
	// connection, so a nested goalHadFaultRuns query while this cursor is
	// open would wait forever for the pool's only conn (analyze-side
	// deadlock, seen in the round-scope test hang).
	type sample struct {
		metrics string
		goalID  string
	}
	var collected []sample
	for rows.Next() {
		var smp sample
		if err := rows.Scan(&smp.metrics, &smp.goalID); err != nil {
			continue
		}
		collected = append(collected, smp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return map[string]any{}
	}
	samples := []map[string]any{}
	for _, smp := range collected {
		if s.goalHadFaultRuns(ctx, smp.goalID) {
			continue
		}
		samples = append(samples, decodeMap(smp.metrics))
	}
	if len(samples) < 5 {
		return map[string]any{}
	}
	keys := []string{"critical_path_ms", "parallel_speedup", "idle_gap_ms",
		"idle_gap_share", "dependency_wait_ms", "dispatch_wait_ms", "verify_overhead_ratio",
		"review_overhead_ratio", "rework_ratio", "consult_count", "human_wait_ms", "human_wait_share",
		"gate_wait_ms", "retry_amplification", "agent_busy_ms", "lane_imbalance"}
	out := map[string]any{}
	for _, k := range keys {
		vals := []float64{}
		for _, smp := range samples {
			if v, ok := num(smp[k]); ok {
				vals = append(vals, v)
			}
		}
		if len(vals)*2 > len(samples) { // majority coverage required
			sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
			out[k] = vals[len(vals)/2]
		}
	}
	return out
}

// goalHadFaultRuns reports whether the session's goal ended any run with a
// non-retryable (quota/billing/model) failure — the marker of an execution
// window the orchestrator itself could not run healthily in. Such sessions
// must not donate baseline samples (P7, issue #31).
func (s *RSIService) goalHadFaultRuns(ctx context.Context, goalID string) bool {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT result_summary FROM run WHERE goal_id=? AND status='failed'`, goalID)
	if err != nil {
		return false // cannot tell → do not exclude (fail-open for the sample)
	}
	defer rows.Close()
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			continue
		}
		if isNonRetryable(summary) {
			return true
		}
	}
	return false
}

func summarize(g *WorkflowGraph, findings []RsiFinding) string {
	m := g.Metrics
	top := "无明显结构问题"
	if len(findings) > 0 {
		top = fmt.Sprintf("首要发现：%s", findings[0].Title)
	}
	return fmt.Sprintf("%d 节点 / %d 边，关键路径 %s（墙钟 %s），并行收益 %.2f×。%s",
		len(g.Nodes), len(g.Edges), humanMS(mi(m, "critical_path_ms")), humanMS(mi(m, "wall_ms")),
		mf(m, "parallel_speedup"), top)
}

func narrative(g *WorkflowGraph, findings []RsiFinding, baseline map[string]any) string {
	var b strings.Builder
	m := g.Metrics
	fmt.Fprintf(&b, "本次训练会话把该 goal 的编排过程冻结为一张 %d 节点 / %d 边的工作流图（agent/verify/review/consult/integrate 节点，显式依赖与时间推断边）。\n\n", len(g.Nodes), len(g.Edges))
	fmt.Fprintf(&b, "结构指标：总工作量 %s，墙钟 %s，关键路径 %s（%d 节点），并行收益 %.2f×，串行损失 %s，峰值并行宽度 %d；验证开销占比 %.0f%%，返工率 %.0f%%，咨询 %d 次，通道 %d 条（不均衡度 %.2f）。\n\n",
		humanMS(mi(m, "work_sum_ms")), humanMS(mi(m, "wall_ms")), humanMS(mi(m, "critical_path_ms")), len(g.Critical),
		mf(m, "parallel_speedup"), humanMS(mi(m, "idle_gap_ms")), mi(m, "parallel_width_peak"),
		mf(m, "verify_overhead_ratio")*100, mf(m, "rework_ratio")*100, mi(m, "consult_count"), mi(m, "lane_count"), mf(m, "lane_imbalance"))
	if len(baseline) > 0 {
		fmt.Fprintf(&b, "与近 20 次就绪会话的中位数基线对比：关键路径 %s → %s，并行收益 %.2f× → %.2f×。\n\n",
			humanMS(int64(numOr(baseline["critical_path_ms"]))), humanMS(mi(m, "critical_path_ms")),
			numOr(baseline["parallel_speedup"]), mf(m, "parallel_speedup"))
	} else {
		b.WriteString("尚无足够历史（<5 个就绪会话）计算基线，本报告只做单次观察。\n\n")
	}
	if len(findings) == 0 {
		b.WriteString("九类结构规则均未触发：编排结构在本次样本上没有可判定的瓶颈。")
	} else {
		fmt.Fprintf(&b, "结构发现（按得分排序，均为只读提案，不自动执行）：\n")
		for i, f := range findings {
			fmt.Fprintf(&b, "%d. [%s/%s] %s — %s\n   建议：%s\n", i+1, f.Category, f.Severity, f.Title, f.Explanation, f.Suggestion)
		}
	}
	return b.String()
}

// ── small formatting helpers ────────────────────────────────────────────

func humanMS(ms int64) string {
	switch {
	case ms <= 0:
		return "0ms"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(ms)/60000)
	}
}
func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}
func numOr(v any) float64 {
	f, _ := num(v)
	return f
}
func shortNames(nodes []WorkflowNode) string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Label)
	}
	return strings.Join(names, "、")
}
func laneDump(m map[string]any) string {
	lanes := metricLanes(m)
	if len(lanes) == 0 {
		return "{}"
	}
	parts := []string{}
	for k, v := range lanes {
		parts = append(parts, fmt.Sprintf("%s=%dms", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
func sgLabel(g *WorkflowGraph, sgid string) string {
	for _, n := range g.Nodes {
		if n.Kind == "agent" && n.SubGoalID == sgid {
			return n.Label
		}
	}
	return ""
}
func endingLeaves(g *WorkflowGraph) []WorkflowNode {
	outgoing := map[string]bool{}
	for _, e := range g.Edges {
		outgoing[e.From] = true
	}
	out := []WorkflowNode{}
	for _, n := range g.Nodes {
		if n.Kind == "agent" && n.EndedAtMS > 0 && !outgoing[n.Key] {
			out = append(out, n)
		}
	}
	return out
}
