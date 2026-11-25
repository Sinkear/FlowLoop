package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Sinkear/flowloop/internal/logging"
)

// ═══════════════════════════════════════════════════════════════════════
// Phase D (proposal §13): deterministic finding→blueprint-patch translator.
//
// The RSI rules (rsi_analyze.go) diagnose; the translator below turns a
// finding into a concrete, reviewable spec patch; humans stay the gate —
// a drafted proposal is PENDING until approved, and approval only opens a
// NEW DRAFT version (never publishes). No LLM is involved anywhere in this
// file: every transform is derived from the execution graph + the pinned
// spec, and every draft passes the full V1–V6 validator inside
// CreateProposal (fail-closed).
//
// Phase D originally translated only serial_chain. The rules already cover
// eleven categories, and analysis repeatedly produced findings the old
// translator silently dropped (3×human_wait + 2×lost_parallelism in
// docs/use-cases/2026-11-21-blueprint-self-iteration). Each category now
// has its own template here, registered in findingTranslators.
//
// Shared safety rails:
//   - goal must be blueprint-pinned AND compiled (blueprint_node > 0);
//   - the transform only ever touches spec nodes it fully understands;
//     anything ambiguous is a silent skip, never a guess;
//   - per-category at most one proposal per analysis, at most
//     translatorMaxPerRun proposals total, and the per-blueprint
//     rsi.max_auto_rounds budget still applies;
//   - dedup by patch effect (same patch list from two categories drafts
//     once) and by exact title (re-analyzing cannot stack duplicates);
//   - a validation refusal is a legitimate skip; I/O errors are logged but
//     never break the analysis itself.
// ═══════════════════════════════════════════════════════════════════════

const (
	translatorCreatedByType = "agent"
	translatorCreatedByID   = "rsi-translator"
	// translatorMaxPerRun bounds how many auto-proposals one analysis may
	// open. The human gate is the real safety net, but a burst of proposals
	// is unreviewable — keep the batch small and let the next round continue.
	translatorMaxPerRun = 4
)

// proposalDraft is a translator's output before it becomes a gated proposal.
type proposalDraft struct {
	Title      string
	Hypothesis string
	Metric     string
	Direction  string
	Patches    []BlueprintPatch
}

// translateCtx holds everything a translator needs, loaded once per analysis.
type translateCtx struct {
	goalID    string
	versionID string
	version   *BlueprintVersion
	spec      *SpecGraph
	byID      map[string]*SpecNode
	keyBySG   map[string]string // execution "sg:<id>" (unprefixed) → spec node key
	sgByKey   map[string]string // spec node key → execution sub_goal id
	g         *WorkflowGraph
	remaining int // remaining cap budget; -1 = uncapped
	seen      map[string]bool
	touched   map[string]bool // node/edge keys already claimed by a draft (this run + live proposals)
	cats      map[string]bool
}

type findingTranslator func(context.Context, *RSIService, *translateCtx, RsiFinding) (*proposalDraft, error)

// findingTranslators is the complete translation registry. Every category
// emitted by the analyzer has an entry; categories without a deterministic
// structural transform (none today) would be deliberately absent and are
// then surfaced by the "no transform matched" log line.
var findingTranslators = map[string]findingTranslator{
	"serial_chain":        translateSerialChain,
	"lost_parallelism":    translateLostParallelism,
	"split_candidate":     translateSplitCandidate,
	"merge_candidate":     translateMergeCandidate,
	"human_wait":          translateHumanWait,
	"wrong_assignee":      translateWrongAssignee,
	"brief_quality":       translateBriefQuality,
	"retry_amplification": translateRetryAmplification,
	"consult_repeat":      translateConsultRepeat,
	"ping_pong":           translatePingPong,
	"plan_deviation":      translatePlanDeviation,
}

// draftAutoProposals runs every matching translator over this analysis'
// findings, then drafts the resulting patches through CreateProposal.
func (s *RSIService) draftAutoProposals(ctx context.Context, goalID string, g *WorkflowGraph, findings []RsiFinding) {
	var versionID string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(blueprint_version_id,'') FROM goal WHERE id=?`, goalID).Scan(&versionID)
	if err != nil {
		logging.Warnf("[rsi] auto-proposal: load blueprint pin for goal %s: %v", goalID, err)
		return
	}
	if versionID == "" {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: no pinned blueprint version (infer mode)", goalID)
		return // infer goal — no spec to patch
	}
	if s.blueprintSvc == nil {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: blueprint service not wired", goalID)
		return // not wired in main — nothing to translate through
	}
	// Direct observable signal for BUG-3: a pinned goal whose spec graph was
	// never compiled has an empty blueprint_node map, so the translator cannot
	// map execution nodes back to spec keys and the auto-iteration chain
	// dead-ends silently. That is exactly the "创建即 active 绕过 Activate"
	// shape — make it loud instead of a no-op.
	var nodeCount int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&nodeCount); err != nil {
		logging.Warnf("[rsi] auto-proposal: count blueprint_node for goal %s: %v", goalID, err)
	} else if nodeCount == 0 {
		logging.Warnf("[rsi] goal %s 绑定蓝本 %s 但未编译（blueprint_node=0）——自动提案跳过", goalID, versionID)
		return
	}
	// Auto-iteration round cap (§14.2): analysis is never capped — only the
	// automatic PRODUCTION of optimization artifacts is. Translator
	// proposals are counted per blueprint via created_by_id='rsi-translator';
	// human proposals/approvals/publishes are never subject to the cap.
	remaining := -1
	if cap := s.autoRoundsCap(ctx); cap >= 0 {
		// The cap is PER BLUEPRINT (rsi.max_auto_rounds), not per platform:
		// COUNT(DISTINCT blueprint_id) previously let a busy blueprint consume
		// every other blueprint's budget (BUG-4 P2, findings §三.11).
		var bpID string
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COALESCE(blueprint_id,'') FROM blueprint_version WHERE id=?`, versionID).Scan(&bpID); err != nil {
			// Without the blueprint id the per-blueprint counter would be
			// computed on '' (every blueprint shares it) — skip drafting rather
			// than let one broken lookup silently disable the cap.
			logging.Warnf("[rsi] auto-proposal cap: load blueprint for version %s: %v", versionID, err)
			return
		}
		var n int
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM blueprint_proposal WHERE blueprint_id=? AND created_by_id='rsi-translator' AND status IN ('pending','approved')`,
			bpID,
		).Scan(&n); err != nil {
			// Same fail-closed rule as the blueprint lookup above: a broken
			// counter must not turn into an uncapped drafting run.
			logging.Warnf("[rsi] auto-proposal cap check failed for goal %s: %v", goalID, err)
			return
		} else if n >= cap {
			logging.Infof("[rsi] auto-iteration cap %d reached — translator proposal skipped for goal %s (rsi.max_auto_rounds)", cap, goalID)
			return
		} else {
			remaining = cap - n
		}
	}
	t, err := s.buildTranslateCtx(ctx, goalID, versionID)
	if err != nil {
		logging.Warnf("[rsi] auto-proposal: load spec for goal %s: %v", goalID, err)
		return
	}
	t.g = g
	t.remaining = remaining
	t.seen = map[string]bool{}
	t.touched = map[string]bool{}
	// Collision guard across runs: an existing pending/approved proposal owns
	// its nodes/edges. Drafting a second proposal touching them would stack
	// conflicting patches on the same base version (approving both loses the
	// first one's edits when the second is published).
	if prow, err := s.st.DB().QueryContext(ctx,
		`SELECT p.patches_json FROM blueprint_proposal p JOIN blueprint_version v ON v.id=p.base_version_id
		 WHERE v.blueprint_id=? AND p.status IN ('pending','approved')`, t.version.BlueprintID); err == nil {
		for prow.Next() {
			var raw string
			if err := prow.Scan(&raw); err != nil {
				break
			}
			var patches []BlueprintPatch
			if err := json.Unmarshal([]byte(raw), &patches); err != nil {
				continue
			}
			for k := range patchKeys(patches) {
				t.touched[k] = true
			}
		}
		prow.Close()
	}
	t.cats = map[string]bool{}
	for _, f := range findings {
		t.cats[f.Category] = true
	}

	attempted := map[string]bool{}
	drafted := 0
	for _, f := range findings {
		if attempted[f.Category] {
			continue // one transform per category per analysis
		}
		translate, ok := findingTranslators[f.Category]
		if !ok {
			continue
		}
		draft, err := translate(ctx, s, t, f)
		if err != nil {
			// Leave the category open: a lower-scoring finding may still be
			// translatable, and findings are score-sorted.
			logging.Errorf("[rsi] translate %s for goal %s: %v", f.Category, goalID, err)
			continue
		}
		if draft == nil {
			continue
		}
		attempted[f.Category] = true
		created, err := s.draftProposal(ctx, t, draft, f)
		if err != nil {
			logging.Errorf("[rsi] auto-proposal for goal %s: %v", goalID, err)
			continue
		}
		if created {
			drafted++
		}
		if t.remaining == 0 || drafted >= translatorMaxPerRun {
			break
		}
	}
	if drafted == 0 {
		logging.Debugf("[rsi] auto-proposal: no deterministic transform matched for goal %s (categories=%s)", goalID, strings.Join(findingCategories(findings), ","))
	}
}

func findingCategories(findings []RsiFinding) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		if !seen[f.Category] {
			seen[f.Category] = true
			out = append(out, f.Category)
		}
	}
	sort.Strings(out)
	return out
}

func (s *RSIService) buildTranslateCtx(ctx context.Context, goalID, versionID string) (*translateCtx, error) {
	version, err := s.blueprintSvc.getVersion(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("load pinned version: %w", err)
	}
	spec, err := parseSpecContent(version)
	if err != nil {
		return nil, fmt.Errorf("parse pinned version: %w", err)
	}
	t := &translateCtx{
		goalID:    goalID,
		versionID: versionID,
		version:   version,
		spec:      spec,
		byID:      make(map[string]*SpecNode, len(spec.Nodes)),
		keyBySG:   map[string]string{},
		sgByKey:   map[string]string{},
	}
	for i := range spec.Nodes {
		t.byID[spec.Nodes[i].ID] = &spec.Nodes[i]
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id, node_key FROM blueprint_node WHERE goal_id=? AND COALESCE(sub_goal_id,'')!=''`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sgID, nodeKey string
		if err := rows.Scan(&sgID, &nodeKey); err != nil {
			return nil, err
		}
		t.keyBySG[sgID] = nodeKey
		t.sgByKey[nodeKey] = sgID
	}
	return t, rows.Err()
}

// draftProposal is the single gate every template goes through: effect
// dedup, title dedup, CreateProposal (validation + provenance), budget.
func (s *RSIService) draftProposal(ctx context.Context, t *translateCtx, d *proposalDraft, f RsiFinding) (bool, error) {
	if d == nil || len(d.Patches) == 0 || strings.TrimSpace(d.Title) == "" {
		return false, nil
	}
	sig, err := json.Marshal(d.Patches)
	if err != nil {
		return false, fmt.Errorf("signature for %q: %w", d.Title, err)
	}
	if t.seen[string(sig)] {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: %q has the same patch effect as an earlier draft", t.goalID, d.Title)
		return false, nil
	}
	keys := patchKeys(d.Patches)
	for k := range keys {
		if t.touched[k] {
			logging.Debugf("[rsi] auto-proposal skipped for goal %s: %q touches %s already owned by another proposal", t.goalID, d.Title, k)
			return false, nil
		}
	}
	var existing int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_proposal p JOIN blueprint_version v ON v.id=p.base_version_id
		 WHERE v.blueprint_id=? AND p.title=?`, t.version.BlueprintID, d.Title).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: proposal %q already exists (dedup)", t.goalID, d.Title)
		return false, nil
	}
	if _, err := s.blueprintSvc.CreateProposal(ctx, t.version.BlueprintID, t.versionID, d.Title, d.Hypothesis,
		d.Metric, d.Direction, d.Patches, f.ID, translatorCreatedByType, translatorCreatedByID); err != nil {
		// A validation refusal means the transform shape is not acceptable to
		// the spec rules — that is a legitimate silent skip, not an error worth
		// failing the analysis for. Real I/O errors still surface.
		if errors.Is(err, ErrValidation) {
			logging.Warnf("[rsi] auto-proposal draft rejected by validator (goal %s, %s): %v", t.goalID, d.Title, err)
			return false, nil
		}
		return false, fmt.Errorf("create auto-proposal %q: %w", d.Title, err)
	}
	t.seen[string(sig)] = true
	for k := range keys {
		t.touched[k] = true
	}
	if t.remaining > 0 {
		t.remaining--
	}
	logging.Infof("[rsi] auto-proposal drafted for goal %s: %s (%s)", t.goalID, d.Title, f.Category)
	return true, nil
}

// ─────────────────────────────────────────────────────────────────────
// serial_chain / lost_parallelism: chain → fork
// ─────────────────────────────────────────────────────────────────────

// translateSerialChain keeps the original Phase D title/shape: a pure task
// chain t1→…→tk→end becomes a fork (every step wired straight to the end).
func translateSerialChain(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	return s.chainForkDraft(t, f,
		"并行化串行链",
		"串行链 %s 共 %d 步全部经依赖边顺序执行，但 brief 之间没有任何数据引用——"+
			"fork 后所有步骤可并行，关键路径与 wall 时间应显著下降。本提案由 serial_chain finding 确定性翻译生成（无 LLM），"+
			"请审查后批准。")
}

// translateLostParallelism translates the same chain→fork shape when the
// analysis measured a large gap between theoretical and actual parallelism.
// It defers to serial_chain when both findings describe the same analysis:
// the chain title is the more precise description of the fix.
func translateLostParallelism(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	if t.cats["serial_chain"] {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: lost_parallelism defers to serial_chain (same fork transform)", t.goalID)
		return nil, nil
	}
	d, err := s.chainForkDraft(t, f,
		"并行化可并行的串行段",
		"关键路径 %s 共 %d 步串行执行，理论并行收益远高于实际（lost_parallelism）——"+
			"这些步骤之间没有数据引用，fork 后可并行，并行加速应显著上升。本提案由 lost_parallelism finding 确定性翻译生成（无 LLM），"+
			"请审查后批准。")
	if d != nil {
		d.Metric, d.Direction = "parallel_speedup", "up"
	}
	return d, err
}

func (s *RSIService) chainForkDraft(t *translateCtx, f RsiFinding, titlePrefix, hypothesisFormat string) (*proposalDraft, error) {
	// 1. Execution-graph side: walk the critical path, keep the maximal run
	//    of consecutive agent nodes linked by explicit depends_on edges.
	chain := criticalTaskChain(t.g)
	if len(chain) < 2 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: critical task chain len=%d (<2) — nothing to fork", t.goalID, len(chain))
		return nil, nil // single-task chain: nothing to parallelize
	}

	// 2. Map execution nodes (sg:<id>) to spec node keys via blueprint_node.
	specKeys := make([]string, 0, len(chain))
	for _, nodeKey := range chain {
		sk, ok := t.keyBySG[strings.TrimPrefix(nodeKey, "sg:")]
		if !ok {
			logging.Debugf("[rsi] auto-proposal skipped for goal %s: chain node %q not mapped to a spec node (chain mapped=%d/%d, blueprint_node mapped=%d)",
				t.goalID, nodeKey, len(specKeys), len(chain), len(t.keyBySG))
			return nil, nil // chain member not spec-mapped (spawn child etc.) — bail out
		}
		specKeys = append(specKeys, sk)
	}

	// 3. The chain must be a pure task chain whose last member flows
	//    directly into ONE shared end node.
	endKey := ""
	for _, sk := range specKeys {
		n := t.byID[sk]
		if n == nil || n.Type != "task" {
			logging.Debugf("[rsi] auto-proposal skipped for goal %s: spec node %q is not a task (only pure task chains are translated)", t.goalID, sk)
			return nil, nil
		}
	}
	for _, e := range t.spec.Edges {
		if e.Source == specKeys[len(specKeys)-1] && e.SourceHandle == "done" {
			if target := t.byID[e.Target]; target != nil && target.Type == "end" {
				if endKey != "" && endKey != e.Target {
					logging.Debugf("[rsi] auto-proposal skipped for goal %s: chain forks into multiple end nodes (%q vs %q)", t.goalID, endKey, e.Target)
					return nil, nil
				}
				endKey = e.Target
			}
		}
	}
	if endKey == "" {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: chain does not end directly at an end node", t.goalID)
		return nil, nil
	}

	// 4. Data-flow check: no chain member's brief may reference another
	//    member's output. One cross-reference aborts the whole transform —
	//    the fork would silently break the reference (拍2 resolves only
	//    from ancestors).
	for _, a := range specKeys {
		nA := t.byID[a]
		if nA.Task == nil {
			return nil, nil
		}
		for _, b := range specKeys {
			if a == b {
				continue
			}
			if strings.Contains(nA.Task.Brief, "{{node."+b+".") {
				logging.Debugf("[rsi] auto-proposal skipped for goal %s: chain member %q references %q output — fork would break the data flow", t.goalID, a, b)
				return nil, nil
			}
		}
	}

	// 4b. Outside references: a spec node outside the chain that reads a
	//     NON-tail member's output depends on it transitively today
	//     (depends_on is derived from spec edges). After the fork that member
	//     becomes a sibling, so the reference would resolve to
	//     "(上游节点未执行)". Abort conservatively. References to the tail
	//     stay valid: its dependents keep their edges to it.
	members := map[string]bool{}
	for _, sk := range specKeys {
		members[sk] = true
	}
	for i := range t.spec.Nodes {
		n := &t.spec.Nodes[i]
		if members[n.ID] {
			continue
		}
		text := ""
		if n.Task != nil {
			text = n.Task.Brief
		} else if n.Gate != nil {
			text = n.Gate.Prompt
		}
		for _, sk := range specKeys[:len(specKeys)-1] {
			if strings.Contains(text, "{{node."+sk+".") {
				logging.Debugf("[rsi] auto-proposal skipped for goal %s: outside node %q references non-tail chain member %q — fork would orphan the reference", t.goalID, n.ID, sk)
				return nil, nil
			}
		}
	}

	// 5. Verify each adjacent pair really is a done→depends chain edge in
	//    the spec (the execution graph said depends_on; the spec is truth).
	for i := 0; i+1 < len(specKeys); i++ {
		found := false
		for _, e := range t.spec.Edges {
			if e.Source == specKeys[i] && e.SourceHandle == "done" && e.Target == specKeys[i+1] {
				found = true
				break
			}
		}
		if !found {
			logging.Debugf("[rsi] auto-proposal skipped for goal %s: spec edge %q→%q missing — spec shape diverged from execution", t.goalID, specKeys[i], specKeys[i+1])
			return nil, nil
		}
	}

	// 6. Compose the chain→fork patches: delete the k−1 chain edges, wire
	//    every member straight to the shared end.
	patches := make([]BlueprintPatch, 0, 2*len(specKeys))
	for i := 0; i+1 < len(specKeys); i++ {
		patches = append(patches, BlueprintPatch{Op: "delete_edge", Edge: &SpecEdge{
			Source: specKeys[i], SourceHandle: "done", Target: specKeys[i+1]}})
		patches = append(patches, BlueprintPatch{Op: "add_edge", Edge: &SpecEdge{
			Source: specKeys[i], SourceHandle: "done", Target: endKey}})
	}
	title := fmt.Sprintf("自动提案：%s（%s）", titlePrefix, strings.Join(specKeys, "‖"))
	hypothesis := fmt.Sprintf(hypothesisFormat, strings.Join(specKeys, "→"), len(specKeys))
	return &proposalDraft{
		Title:      title,
		Hypothesis: hypothesis,
		Metric:     "wall_ms",
		Direction:  "down",
		Patches:    patches,
	}, nil
}

// criticalTaskChain filters the critical path to agent (task) nodes, then
// returns the maximal run of consecutive agent nodes linked by explicit
// depends_on edges. Verify/consult nodes interleaved on the raw path are
// skipped first — a blueprint chain A→B stays a chain even when the path
// renders as A,v:A,B.
func criticalTaskChain(g *WorkflowGraph) []string {
	adj := map[string]bool{}
	for _, e := range g.Edges {
		if e.Kind == "depends_on" && e.Source == "explicit" {
			adj[e.From+"→"+e.To] = true
		}
	}
	kindOf := map[string]string{}
	for i := range g.Nodes {
		kindOf[g.Nodes[i].Key] = g.Nodes[i].Kind
	}
	agents := make([]string, 0, len(g.Critical))
	for _, key := range g.Critical {
		if kindOf[key] == "agent" {
			agents = append(agents, key)
		}
	}
	var best, cur []string
	for _, key := range agents {
		if len(cur) > 0 && !adj[cur[len(cur)-1]+"→"+key] {
			if len(cur) > len(best) {
				best = cur
			}
			cur = nil
		}
		cur = append(cur, key)
	}
	if len(cur) > len(best) {
		best = cur
	}
	return best
}

// ─────────────────────────────────────────────────────────────────────
// split_candidate: heavy task → spawn (parallel children)
// ─────────────────────────────────────────────────────────────────────

// translateSplitCandidate turns up to two critical-path tasks that consume
// most of the critical path into spawn nodes: a spawn compiles to a planner
// sub-goal that materializes child tasks at runtime, so the work of one
// serial node becomes parallel children. The node ID is preserved (delete +
// add with the same ID), every incident edge is re-added so the graph keeps
// its shape, and the brief moves onto the spawn (which stays a planner
// task, so no data-flow reference can be broken by children).
func translateSplitCandidate(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	agentBusy := float64(mi(t.g.Metrics, "agent_busy_ms"))
	if agentBusy <= 0 {
		agentBusy = float64(mi(t.g.Metrics, "work_sum_ms"))
	}
	if agentBusy <= 0 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: split_candidate without measurable agent busy time", t.goalID)
		return nil, nil
	}
	type heavy struct {
		key string
		dur int64
	}
	var heavyNodes []heavy
	for _, key := range t.g.Critical {
		n := t.execNode(key)
		if n == nil || n.Kind != "agent" || n.DurationMS <= 0 {
			continue
		}
		if float64(n.DurationMS)/agentBusy < ruleSplitShare {
			continue
		}
		specKey, ok := t.keyBySG[strings.TrimPrefix(key, "sg:")]
		if !ok {
			continue
		}
		sn := t.byID[specKey]
		if sn == nil || sn.Type != "task" || sn.Task == nil {
			continue
		}
		// A spawn only fans out inside a team: compile sets team_squad_id
		// only for squad:/capability: assignees. A plain agent: node's
		// spawn children can only be claimed by that same agent, and the
		// executor's per-agent single-flight serializes them — the split
		// would buy planning overhead with zero parallelism.
		if !strings.HasPrefix(sn.Task.Assignee, "squad:") && !strings.HasPrefix(sn.Task.Assignee, "capability:") {
			logging.Debugf("[rsi] auto-proposal skipped node %q for goal %s: agent: assignee cannot fan out (no team squad)", specKey, t.goalID)
			continue
		}
		if sn.Task.Verify != nil && sn.Task.Verify.Mode != "" && sn.Task.Verify.Mode != "domain_default" {
			// spawn has no verify body: converting would silently drop a
			// custom verifier contract. Skip rather than guess.
			continue
		}
		heavyNodes = append(heavyNodes, heavy{key: key, dur: n.DurationMS})
	}
	if len(heavyNodes) == 0 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: split_candidate derived no heavy node", t.goalID)
		return nil, nil
	}
	sort.Slice(heavyNodes, func(i, j int) bool {
		if heavyNodes[i].dur != heavyNodes[j].dur {
			return heavyNodes[i].dur > heavyNodes[j].dur
		}
		return heavyNodes[i].key < heavyNodes[j].key
	})
	if len(heavyNodes) > 2 {
		heavyNodes = heavyNodes[:2]
	}

	deletePatches := make([]BlueprintPatch, 0, len(heavyNodes))
	addPatches := make([]BlueprintPatch, 0, len(heavyNodes))
	var ids []string
	for _, h := range heavyNodes {
		key := t.keyBySG[strings.TrimPrefix(h.key, "sg:")]
		n := t.byID[key]
		if n == nil || n.Type != "task" || n.Task == nil {
			continue
		}
		// V5: a {{node.<id>.output}} reference requires the target to be a
		// task. Turning this node into a spawn would break every such
		// reference, and a spawn brief itself is not templated.
		if t.templateRefers(key) || strings.Contains(n.Task.Brief, "{{node.") {
			logging.Debugf("[rsi] auto-proposal skipped node %q for goal %s: brief/gate templates reference it — spawn conversion would break V5", key, t.goalID)
			continue
		}
		spawn := &SpecNode{
			ID:    key,
			Type:  "spawn",
			Title: n.Title,
			Spawn: &SpecSpawn{Assignee: n.Task.Assignee, Brief: n.Task.Brief, ChildType: "task", MaxChildren: 2},
		}
		deletePatches = append(deletePatches, BlueprintPatch{Op: "delete_node", ID: key})
		addPatches = append(addPatches, BlueprintPatch{Op: "add_node", ID: key, Node: spawn})
		ids = append(ids, key)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// Re-add every edge incident to a converted node, deduped: converting two
	// adjacent nodes would otherwise re-add the same edge twice (V1 rejects a
	// duplicate (source, handle) outbound / duplicate edge).
	edgePatches := make([]BlueprintPatch, 0, 8)
	seenEdge := map[string]bool{}
	for _, id := range ids {
		for _, e := range t.spec.Edges {
			if e.Source != id && e.Target != id {
				continue
			}
			k := e.Source + "\x00" + e.SourceHandle + "\x00" + e.Target
			if seenEdge[k] {
				continue
			}
			seenEdge[k] = true
			edgePatches = append(edgePatches, BlueprintPatch{Op: "add_edge", Edge: &SpecEdge{
				Source: e.Source, SourceHandle: e.SourceHandle, Target: e.Target}})
		}
	}
	patches := append(append(deletePatches, addPatches...), edgePatches...)
	return &proposalDraft{
		Title: fmt.Sprintf("自动提案：把重型节点拆成并行子任务（%s）", strings.Join(ids, "、")),
		Hypothesis: fmt.Sprintf("节点 %s 占执行时长（agent busy）%.0f%% 以上，是并行度的硬瓶颈——"+
			"改为 spawn 后编译为规划子任务，运行期按 max_children=2 拆成并行子节点，关键路径应下降。"+
			"本提案由 split_candidate finding 确定性翻译生成（无 LLM），请审查后批准。",
			strings.Join(ids, "、"), ruleSplitShare*100),
		Metric:    "critical_path_ms",
		Direction: "down",
		Patches:   patches,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// merge_candidate: merge small leaves into the slowest one
// ─────────────────────────────────────────────────────────────────────

// translateMergeCandidate merges up to two small ending leaves into the
// leaf that already dominates wall time: the victims' inbound done edges are
// redirected to the keeper, their briefs are appended to it, and the victim
// nodes are deleted. Overhead shrinks and the tail consolidates.
func translateMergeCandidate(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	type leaf struct {
		key string
		dur int64
	}
	var leaves []leaf
	seen := map[string]bool{}
	for _, n := range endingLeaves(t.g) {
		key, ok := t.keyBySG[strings.TrimPrefix(n.Key, "sg:")]
		if !ok || seen[key] {
			continue
		}
		sn := t.byID[key]
		if sn == nil || sn.Type != "task" || sn.Task == nil {
			continue
		}
		seen[key] = true
		leaves = append(leaves, leaf{key: key, dur: n.DurationMS})
	}
	if len(leaves) < 2 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: merge_candidate derived %d leaves (<2)", t.goalID, len(leaves))
		return nil, nil
	}
	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].dur != leaves[j].dur {
			return leaves[i].dur > leaves[j].dur
		}
		return leaves[i].key < leaves[j].key
	})
	keeper := leaves[0]
	// Merge cost is the serialization increment, not the leaf duration: only
	// merge while the accumulated victim time stays inside 5 min / 10% wall.
	// Cheapest leaves go first (they buy overhead reduction for the least wall
	// impact); the keeper is the longest leaf so it stays the constraint.
	budget := int64(ruleMergeAddedWallMS)
	if shareBudget := int64(ruleMergeAddedWallShare * float64(mi(t.g.Metrics, "wall_ms"))); shareBudget < budget {
		budget = shareBudget
	}
	var victims []leaf
	added := int64(0)
	for i := len(leaves) - 1; i >= 0; i-- {
		if leaves[i].key == keeper.key || len(victims) >= 2 {
			continue
		}
		if added+leaves[i].dur > budget {
			continue
		}
		victims = append(victims, leaves[i])
		added += leaves[i].dur
	}
	if len(victims) == 0 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: merge_candidate has no victim inside the added-wall budget", t.goalID)
		return nil, nil
	}
	verifier := map[string]string{}
	for _, n := range t.g.Nodes {
		if n.Kind == "verify" {
			verifier[n.SubGoalID] = n.AssigneeID
		}
	}

	patches := make([]BlueprintPatch, 0, 8)
	redirected := map[string]bool{}
	merged := make([]string, 0, len(victims))
	var mergedTitles []string
	for _, v := range victims {
		vn := t.byID[v.key]
		if vn == nil || vn.Task == nil {
			continue
		}
		if vn.Task.Verify != nil && vn.Task.Verify.Mode != "" && vn.Task.Verify.Mode != "domain_default" {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: custom verify contract would be lost in the merge", v.key, t.goalID)
			continue
		}
		if t.templateRefers(v.key) || strings.Contains(vn.Task.Brief, "{{node."+keeper.key+".") {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: template reference would break V5", v.key, t.goalID)
			continue
		}
		if verifier[t.sgByKey[v.key]] != verifier[t.sgByKey[keeper.key]] {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: different verifier than keeper %q", v.key, t.goalID, keeper.key)
			continue
		}
		if vn.Task.Assignee != t.byID[keeper.key].Task.Assignee {
			// The victims' briefs are executed by the KEEPER's assignee; the
			// analyzer's own suggestion conditions the merge on "同一执行者".
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: different assignee than keeper %q", v.key, t.goalID, keeper.key)
			continue
		}
		if strings.Contains(t.byID[keeper.key].Task.Brief, "{{node."+v.key+".") {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: keeper references its output", v.key, t.goalID)
			continue
		}
		crossRef := false
		for _, other := range victims {
			if other.key == v.key {
				continue
			}
			if strings.Contains(vn.Task.Brief, "{{node."+other.key+".") {
				logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: brief references co-merged leaf %q", v.key, t.goalID, other.key)
				crossRef = true
				break
			}
		}
		if crossRef {
			continue
		}
		// delete_node cascades OUTBOUND edges too. Merging is only safe when
		// those edges are terminal (victim.done→end) and the end stays
		// reachable from another predecessor; a victim that branches into a
		// real node would silently lose a dependency (that downstream task
		// could release before the merged work finishes), and a sole
		// predecessor would orphan the target. Skip those shapes instead.
		outboundUnsafe := false
		for _, e := range t.spec.Edges {
			if e.Source != v.key {
				continue
			}
			tgt := t.byID[e.Target]
			if tgt == nil || tgt.Type != "end" {
				outboundUnsafe = true
				break
			}
			reachable := false
			for _, e2 := range t.spec.Edges {
				if e2.Target == e.Target && e2.Source != v.key {
					reachable = true
					break
				}
			}
			if !reachable {
				outboundUnsafe = true
				break
			}
		}
		if outboundUnsafe {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: outbound edges are not terminal/redundant — cascade would lose a dependency", v.key, t.goalID)
			continue
		}
		var inbound []SpecEdge
		ok := true
		for _, e := range t.spec.Edges {
			if e.Target != v.key {
				continue
			}
			if e.SourceHandle != "done" || e.Source == v.key || e.Source == keeper.key {
				ok = false // unknown branch shape or a cycle — do not guess
				break
			}
			inbound = append(inbound, e)
		}
		if !ok {
			logging.Debugf("[rsi] auto-proposal skipped leaf %q for goal %s: non-done/cyclic inbound edges", v.key, t.goalID)
			continue
		}
		for _, e := range inbound {
			patches = append(patches, BlueprintPatch{Op: "delete_edge", Edge: &SpecEdge{
				Source: e.Source, SourceHandle: e.SourceHandle, Target: e.Target}})
			k := e.Source + "\x00done\x00" + keeper.key
			if redirected[k] || t.edgeExists(e.Source, "done", keeper.key) {
				continue
			}
			redirected[k] = true
			patches = append(patches, BlueprintPatch{Op: "add_edge", Edge: &SpecEdge{
				Source: e.Source, SourceHandle: "done", Target: keeper.key}})
		}
		patches = append(patches, BlueprintPatch{Op: "delete_node", ID: v.key})
		merged = append(merged, v.key)
		mergedTitles = append(mergedTitles, vn.Title)
	}
	if len(merged) == 0 {
		return nil, nil
	}
	// Append the victims' briefs to the keeper last: the keeper patch must
	// carry the final brief (victims are deleted in the same patch list).
	kn := t.byID[keeper.key]
	var b strings.Builder
	b.WriteString(strings.TrimRight(kn.Task.Brief, "\n"))
	for _, v := range merged {
		b.WriteString("\n\n## 合并交付：")
		b.WriteString(t.byID[v].Title)
		b.WriteString("\n")
		b.WriteString(t.byID[v].Task.Brief)
	}
	keeperCopy := *kn
	taskCopy := *kn.Task
	taskCopy.Brief = b.String()
	keeperCopy.Task = &taskCopy
	patches = append(patches, BlueprintPatch{Op: "update_node", ID: keeper.key, Node: &keeperCopy})
	return &proposalDraft{
		Title: fmt.Sprintf("自动提案：合并同时收尾的末梢节点（%s → %s）", strings.Join(merged, "、"), keeper.key),
		Hypothesis: fmt.Sprintf("末梢节点 %s 几乎同时收尾，却与 %s 一样各占一次调度、验证与收尾开销；"+
			"把入边改接到 %s 并把简报并入其 brief，最多增加 %s 串行时间（预算 %s），换取减少末端开销。"+
			"本提案由 merge_candidate finding 确定性翻译生成（无 LLM），请审查后批准。",
			strings.Join(mergedTitles, "、"), keeper.key, keeper.key, humanMS(added), humanMS(budget)),
		Metric:    "wall_ms",
		Direction: "down",
		Patches:   patches,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// human_wait: pre-delivery evidence pack
// ─────────────────────────────────────────────────────────────────────

// translateHumanWait inserts a "人工审核证据包" task right before a success
// end node: the wait measured by the rules is the final human delivery
// review (goal-level gate_decision), so the honest spec-side fix is to have
// the machine assemble the evidence the reviewer would otherwise dig for.
func translateHumanWait(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	share := mf(t.g.Metrics, "human_wait_share")
	if share < ruleHumanWaitShare || mi(t.g.Metrics, "human_wait_ms") < ruleHumanWaitFloorMS {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: human_wait_share=%.2f wait=%dms below share/floor", t.goalID, share, mi(t.g.Metrics, "human_wait_ms"))
		return nil, nil
	}
	// Idempotence: once an evidence pack exists in the pinned spec, a later
	// analysis would otherwise insert pack_2, pack_3, … before every end node
	// it can find (each round's share keeps the finding alive).
	for i := range t.spec.Nodes {
		if n := &t.spec.Nodes[i]; n.Title == "人工审核证据包" || strings.HasPrefix(n.ID, "rsi_review_pack") {
			logging.Debugf("[rsi] auto-proposal skipped for goal %s: evidence pack %q already in the spec", t.goalID, n.ID)
			return nil, nil
		}
	}
	var end *SpecNode
	var inbound []SpecEdge
	for i := range t.spec.Nodes {
		n := &t.spec.Nodes[i]
		if n.Type != "end" || n.End == nil || n.End.Outcome == "failure" {
			continue
		}
		var got []SpecEdge
		for _, e := range t.spec.Edges {
			if e.Target != n.ID || e.SourceHandle != "done" {
				continue
			}
			if src := t.byID[e.Source]; src != nil && src.Type == "task" && src.Task != nil {
				got = append(got, e)
			}
		}
		if len(got) > 0 {
			end = n
			inbound = got
			break
		}
	}
	if end == nil {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: no success end node with direct task inbound", t.goalID)
		return nil, nil
	}
	packID := t.freeNodeID("rsi_review_pack")
	assignee := t.byID[inbound[0].Source].Task.Assignee
	pack := &SpecNode{
		ID:    packID,
		Type:  "task",
		Title: "人工审核证据包",
		Task: &SpecTask{
			Assignee: assignee,
			Brief: "整理本次交付的人工审核证据包（不改实现）：\n" +
				"1. 变更结论与影响面；\n" +
				"2. 关键差异（文件/行为前后对比）；\n" +
				"3. 验证证据（测试、指标或观测结果）；\n" +
				"4. 风险与回滚方案。\n" +
				"输出单一 markdown，供人工审核在最短时间内判读。",
		},
	}
	patches := []BlueprintPatch{{Op: "add_node", ID: packID, Node: pack}}
	for _, e := range inbound {
		patches = append(patches,
			BlueprintPatch{Op: "delete_edge", Edge: &SpecEdge{Source: e.Source, SourceHandle: "done", Target: end.ID}},
			BlueprintPatch{Op: "add_edge", Edge: &SpecEdge{Source: e.Source, SourceHandle: "done", Target: packID}})
	}
	patches = append(patches, BlueprintPatch{Op: "add_edge", Edge: &SpecEdge{Source: packID, SourceHandle: "done", Target: end.ID}})
	return &proposalDraft{
		Title: "自动提案：交付前预置人工审核证据包",
		Hypothesis: fmt.Sprintf("人工等待占交付总时长的 %.0f%%（human_wait）；把复核所需的结论、差异、证据与回滚方案"+
			"在 %s 前由机器汇总，人工审核可快速判读，等待时间应下降。"+
			"本提案由 human_wait finding 确定性翻译生成（无 LLM），请审查后批准。", share*100, packID),
		Metric:    "human_wait_ms",
		Direction: "down",
		Patches:   patches,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// wrong_assignee: rebalance the busiest lane onto an idle lane
// ─────────────────────────────────────────────────────────────────────

// translateWrongAssignee moves up to two tasks off the busiest lane onto the
// idlest lane when the two differ by more than the imbalance threshold.
func translateWrongAssignee(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	lanes := t.lanes()
	if len(lanes) < 2 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: wrong_assignee needs ≥2 lanes, got %d", t.goalID, len(lanes))
		return nil, nil
	}
	type lane struct {
		agent string
		ms    int64
	}
	all := make([]lane, 0, len(lanes))
	for agent, ms := range lanes {
		if agent == "" {
			continue
		}
		all = append(all, lane{agent: agent, ms: ms})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ms != all[j].ms {
			return all[i].ms > all[j].ms
		}
		return all[i].agent < all[j].agent
	})
	if len(all) < 2 {
		return nil, nil
	}
	busy, idle := all[0], all[len(all)-1]
	if busy.ms < ruleLaneMinBusyMS || float64(idle.ms)*2 > float64(busy.ms) {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: lanes are balanced (busy=%dms idle=%dms)", t.goalID, busy.ms, idle.ms)
		return nil, nil
	}
	var patches []BlueprintPatch
	var ids []string
	for i := range t.spec.Nodes {
		if len(ids) >= 2 {
			break
		}
		n := &t.spec.Nodes[i]
		if n.Type != "task" || n.Task == nil || n.Task.Assignee != "agent:"+busy.agent {
			continue
		}
		nCopy := *n
		taskCopy := *n.Task
		taskCopy.Assignee = "agent:" + idle.agent
		nCopy.Task = &taskCopy
		patches = append(patches, BlueprintPatch{Op: "update_node", ID: n.ID, Node: &nCopy})
		ids = append(ids, n.ID)
	}
	if len(ids) == 0 {
		// The work was dispatched through a squad leader or a sub-squad, so no
		// spec task carries the busy agent id directly — nothing safe to move.
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: no spec task assigned to busy lane %s", t.goalID, busy.agent)
		return nil, nil
	}
	return &proposalDraft{
		Title: fmt.Sprintf("自动提案：把最忙通道的任务改派给空闲通道（%s）", strings.Join(ids, "、")),
		Hypothesis: fmt.Sprintf("执行通道负载失衡（最忙 %s 累计 %dms，最闲 %s 仅 %dms）——"+
			"把 %s 改派到空闲成员，可缩短排空时间。"+
			"本提案由 wrong_assignee finding 确定性翻译生成（无 LLM），请审查后批准。",
			busy.agent, busy.ms, idle.agent, idle.ms, strings.Join(ids, "、")),
		Metric:    "wall_ms",
		Direction: "down",
		Patches:   patches,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// Brief-hardening templates (no structural change)
// ─────────────────────────────────────────────────────────────────────

const (
	briefAcceptanceBlock = "## 验收标准（RSI 自动补充）\n" +
		"开工前在简报中显式补齐，缺少任一项视为不合格：\n" +
		"1. 交付物路径（将新增/改动的文件或产物）；\n" +
		"2. 完成判据（验证方可以机器判定的检查项，一条一行）；\n" +
		"3. 已知约束（依赖版本、兼容性、不可改动的接口）；\n" +
		"4. 失败回滚（回滚步骤与影响面）。"
	briefPreflightBlock = "## 执行前置检查（RSI 自动补充）\n" +
		"本节点出现过重试/重跑。开工前逐项确认并写入输出：\n" +
		"1. 依赖与工具版本；\n" +
		"2. 目标文件当前状态（是否被并发修改）；\n" +
		"3. 验证命令可运行；\n" +
		"4. 失败时的最小复现步骤。"
	briefConsultBlock = "## 派发前确认清单（RSI 自动补充）\n" +
		"本节点在多轮执行中反复咨询。开工前把待确认问题一次性列全（目标、边界、依赖、验收口径），" +
		"一次咨询拿齐答复后连续执行，避免中途往返。"
	briefPingPongBlock = "## 一次性问题清单（RSI 自动补充）\n" +
		"本节点与协作方反复往返咨询。下次开工先写一份一次性问题清单（全部待确认项 + 必要上下文：目标/约束/已有产出），" +
		"一次发出，避免多轮乒乓。"
	briefPlanDisciplineBlock = "## 计划节点纪律（RSI 自动补充）\n" +
		"本 goal 绑定了蓝本规范图，执行时按编译出的节点走：不要用 consult 派发替代节点执行，" +
		"不要在计划外直接落盘产物；确需偏离时在计划元数据登记 plan_mode=manual，并在交付中说明原因。"
)

// translateBriefQuality appends acceptance criteria to tasks whose verify
// nodes show rework (Reworks>0 && Attempt≥2).
func translateBriefQuality(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	var keys []string
	rejects := 0
	maxReworks := 0
	for i := range t.g.Nodes {
		n := &t.g.Nodes[i]
		if n.Kind != "verify" || n.Reworks <= 0 || n.Attempt < 2 {
			continue
		}
		rejects += n.Reworks
		if n.Reworks > maxReworks {
			maxReworks = n.Reworks
		}
		if key, ok := t.keyBySG[n.SubGoalID]; ok {
			keys = append(keys, key)
		}
	}
	// Same shape as the analyzer: rework spread over multiple sub-goals, or a
	// single task rejected repeatedly (a genuinely bad brief).
	widespread := len(keys) >= ruleReworkMinSubGoals && rejects >= ruleReworkMinRejects
	if !widespread && maxReworks < 2 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: brief_quality needs ≥%d affected sub-goals and ≥%d rejects, or one task with ≥2 rejects (got %d/%d, max %d)",
			t.goalID, ruleReworkMinSubGoals, ruleReworkMinRejects, len(keys), rejects, maxReworks)
		return nil, nil
	}
	return t.briefHardening("brief_quality", "## 验收标准（RSI 自动补充）", briefAcceptanceBlock,
		"为返工节点补充验收标准", "rework_ratio", keys)
}

// translateRetryAmplification appends a preflight checklist to tasks whose
// execution attempts exceed the retry threshold.
func translateRetryAmplification(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	threshold := int(ruleRetryAmplification)
	var keys []string
	for _, n := range t.g.Nodes {
		if n.Kind != "agent" || n.Attempt < threshold {
			continue
		}
		if key, ok := t.keyBySG[strings.TrimPrefix(n.Key, "sg:")]; ok {
			keys = append(keys, key)
		}
	}
	return t.briefHardening("retry_amplification", "## 执行前置检查（RSI 自动补充）", briefPreflightBlock,
		"为重试节点补充执行前置检查", "wall_ms", keys)
}

// translateConsultRepeat appends a one-shot confirmation checklist to tasks
// whose executions repeatedly asked for consultation.
func translateConsultRepeat(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT r.sub_goal_id FROM consult_request cr JOIN run r ON r.id=cr.requester_run_id
		 WHERE cr.goal_id=? AND COALESCE(r.sub_goal_id,'')!=''
		 GROUP BY r.sub_goal_id HAVING COUNT(*) >= ?`, t.goalID, ruleConsultRepeat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var sgID string
		if err := rows.Scan(&sgID); err != nil {
			return nil, err
		}
		if key, ok := t.keyBySG[sgID]; ok {
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return t.briefHardening("consult_repeat", "## 派发前确认清单（RSI 自动补充）", briefConsultBlock,
		"把执行中途的咨询前移", "wall_ms", keys)
}

// translatePingPong appends a one-shot question list to tasks whose assignee
// repeatedly bounced consultations with the same counterpart.
func translatePingPong(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT requester_agent_id, target_agent_id FROM consult_request WHERE goal_id=?`, t.goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pairCount := map[string]int{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		if from == "" || to == "" {
			continue
		}
		pairCount[from+"\x00"+to]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	repeaters := map[string]bool{}
	seenPair := map[string]bool{}
	for pair, n := range pairCount {
		if seenPair[pair] {
			continue
		}
		parts := strings.SplitN(pair, "\x00", 2)
		rev := parts[1] + "\x00" + parts[0]
		if n < int(rulePingPongPairs) || pairCount[rev] < int(rulePingPongPairs) {
			continue
		}
		// Reciprocity only: one side asking repeatedly is a thin brief, not a
		// two-way loop; both directions must repeat.
		seenPair[pair], seenPair[rev] = true, true
		repeaters[parts[0]] = true
		repeaters[parts[1]] = true
	}
	var keys []string
	for i := range t.spec.Nodes {
		n := &t.spec.Nodes[i]
		if n.Type != "task" || n.Task == nil {
			continue
		}
		for agent := range repeaters {
			if n.Task.Assignee == "agent:"+agent {
				keys = append(keys, n.ID)
				break
			}
		}
	}
	return t.briefHardening("ping_pong", "## 一次性问题清单（RSI 自动补充）", briefPingPongBlock,
		"为咨询乒乓的节点补充一次性问题清单", "wall_ms", keys)
}

// translatePlanDeviation appends execution discipline to the graph entry
// tasks: plan_alignment measured sub-goals that executed outside the
// compiled plan.
func translatePlanDeviation(ctx context.Context, s *RSIService, t *translateCtx, f RsiFinding) (*proposalDraft, error) {
	var keys []string
	inbound := map[string]bool{}
	for _, e := range t.spec.Edges {
		inbound[e.Target] = true
	}
	for i := range t.spec.Nodes {
		n := &t.spec.Nodes[i]
		if n.Type == "task" && n.Task != nil && !inbound[n.ID] {
			keys = append(keys, n.ID)
		}
	}
	return t.briefHardening("plan_deviation", "## 计划节点纪律（RSI 自动补充）", briefPlanDisciplineBlock,
		"在执行入口固化计划节点纪律", "wall_ms", keys)
}

// briefHardening builds update_node patches appending an idempotent block to
// the brief of each usable task node. It returns nil when nothing is left to
// harden (unknown nodes, non-tasks, or blocks already present).
func (t *translateCtx) briefHardening(category, marker, block, titlePrefix, metric string, keys []string) (*proposalDraft, error) {
	var patches []BlueprintPatch
	var ids []string
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		n := t.byID[key]
		if n == nil || n.Type != "task" || n.Task == nil {
			continue
		}
		if strings.Contains(n.Task.Brief, marker) {
			continue
		}
		cp := *n
		task := *n.Task
		task.Brief = strings.TrimRight(n.Task.Brief, "\n") + "\n\n" + block
		cp.Task = &task
		patches = append(patches, BlueprintPatch{Op: "update_node", ID: n.ID, Node: &cp})
		ids = append(ids, key)
	}
	if len(patches) == 0 {
		logging.Debugf("[rsi] auto-proposal skipped for goal %s: %s derived no task to harden", t.goalID, category)
		return nil, nil
	}
	return &proposalDraft{
		Title: fmt.Sprintf("自动提案：%s（%s）", titlePrefix, strings.Join(ids, "、")),
		Hypothesis: fmt.Sprintf("该 goal 的 %s 规则命中了 %d 个节点；把对应的检查要求固化进节点简报，"+
			"可减少同类返工/等待。本提案由 %s finding 确定性翻译生成（无 LLM），请审查后批准。",
			category, len(ids), category),
		Metric:    metric,
		Direction: "down",
		Patches:   patches,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

func (t *translateCtx) execNode(key string) *WorkflowNode {
	for i := range t.g.Nodes {
		if t.g.Nodes[i].Key == key {
			return &t.g.Nodes[i]
		}
	}
	return nil
}

// patchKeys returns the node/edge identities a patch list owns. Used by the
// collision guard so two drafts never edit the same node or edge.
func patchKeys(patches []BlueprintPatch) map[string]bool {
	out := map[string]bool{}
	add := func(id string) {
		if id != "" {
			out["n:"+id] = true
		}
	}
	for _, p := range patches {
		add(p.ID)
		if p.Node != nil {
			add(p.Node.ID)
		}
		if p.Edge != nil {
			out["e:"+p.Edge.Source+"\x00"+p.Edge.SourceHandle+"\x00"+p.Edge.Target] = true
			add(p.Edge.Source)
			add(p.Edge.Target)
		}
	}
	return out
}

func (t *translateCtx) edgeExists(source, handle, target string) bool {
	for _, e := range t.spec.Edges {
		if e.Source == source && e.SourceHandle == handle && e.Target == target {
			return true
		}
	}
	return false
}

// templateRefers reports whether any task brief or gate prompt references the
// node's output. V5 requires such targets to be tasks, so structural
// transforms that change a node's type (or delete it) must bail on these.
func (t *translateCtx) templateRefers(id string) bool {
	needle := "{{node." + id + "."
	for i := range t.spec.Nodes {
		n := &t.spec.Nodes[i]
		if n.Task != nil && strings.Contains(n.Task.Brief, needle) {
			return true
		}
		if n.Gate != nil && strings.Contains(n.Gate.Prompt, needle) {
			return true
		}
	}
	return false
}

// lanes returns the per-agent busy milliseconds recorded in workflow metrics.
func (t *translateCtx) lanes() map[string]int64 {
	switch v := t.g.Metrics["lanes"].(type) {
	case map[string]int64:
		return v
	case map[string]any:
		out := make(map[string]int64, len(v))
		for k, raw := range v {
			if f, ok := num(raw); ok {
				out[k] = int64(f)
			}
		}
		return out
	default:
		return nil
	}
}

// freeNodeID derives a valid, unused spec node id from a base string.
func (t *translateCtx) freeNodeID(base string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "n_" + s
	}
	if len(s) > 60 {
		s = s[:60]
	}
	id := s
	for i := 2; t.byID[id] != nil; i++ {
		suffix := fmt.Sprintf("_%d", i)
		base := s
		if max := 64 - len(suffix); len(base) > max {
			base = base[:max]
		}
		id = base + suffix
	}
	return id
}

// ─────────────────────────────────────────────────────────────────────
// Manual generation (Phase C, WEB-IA-REDESIGN §4.9)
// ─────────────────────────────────────────────────────────────────────

// DraftProposalFromFinding is the MANUAL counterpart of draftAutoProposals:
// a human picks one finding on a review and asks for a proposal from it. It
// reuses the same deterministic translators and the same CreateProposal
// validator, but deliberately bypasses the rails that would make a button
// press look successful while doing nothing:
//
//   - no "one transform per analysis" rule (the human asked for THIS finding,
//     not for the category's best-scoring one);
//   - no rsi.max_auto_rounds budget and no translatorMaxPerRun batch cap —
//     those bound UNATTENDED production, not a reviewed click;
//   - no silent dedup: an existing proposal for the finding is returned as-is
//     (idempotent), and every refusal comes back as a real error with its
//     reason, because the user is waiting on this one request.
//
// The base version is the version the GOAL pinned, exactly like the auto path:
// a manual proposal patches the same spec the run executed, never "latest".
func (s *RSIService) DraftProposalFromFinding(ctx context.Context, findingID string) (*BlueprintProposal, error) {
	if strings.TrimSpace(findingID) == "" {
		return nil, NewValidationError("finding id is required")
	}
	if s.blueprintSvc == nil {
		return nil, fmt.Errorf("blueprint service not wired — proposals are unavailable")
	}
	var f RsiFinding
	var evidence, goalID, versionID, goalStatus, sessionID string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT f.id,f.report_id,f.category,f.severity,f.confidence,f.title,f.explanation,f.suggestion,f.evidence_json,f.score,f.status,f.created_at,
		        rp.session_id, g.id, COALESCE(g.blueprint_version_id,''), COALESCE(g.status,'')
		 FROM rsi_finding f
		 JOIN rsi_report rp ON rp.id=f.report_id
		 JOIN training_session ts ON ts.id=rp.session_id
		 JOIN goal g ON g.id=ts.goal_id
		 WHERE f.id=?`, findingID).
		Scan(&f.ID, &f.ReportID, &f.Category, &f.Severity, &f.Confidence, &f.Title, &f.Explanation, &f.Suggestion,
			&evidence, &f.Score, &f.Status, &f.CreatedAt, &sessionID, &goalID, &versionID, &goalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load finding %s: %w", findingID, err)
	}
	f.Evidence = decodeStrings(evidence)
	// Idempotency: one finding can only ever own one proposal. Returning the
	// existing one makes a double click harmless and lets the UI link to it.
	var existingID, blueID string
	err = s.st.DB().QueryRowContext(ctx,
		`SELECT p.id, p.blueprint_id FROM blueprint_proposal p WHERE p.finding_id=? ORDER BY p.created_at DESC LIMIT 1`,
		findingID).Scan(&existingID, &blueID)
	if err == nil && existingID != "" {
		if p, err := s.findProposal(ctx, blueID, existingID); err == nil {
			return p, nil
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check existing proposal for finding %s: %w", findingID, err)
	}
	// The three "no spec to patch" refusals are product states, not bugs: say
	// which one it is instead of failing with a generic message, because the UI
	// must explain why there is nothing to propose.
	if versionID == "" {
		return nil, NewValidationError("本次任务没有固定工作流（自动拆解），发现不能转成提案")
	}
	var nodeCount int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&nodeCount); err != nil {
		return nil, fmt.Errorf("count blueprint_node for goal %s: %w", goalID, err)
	}
	if nodeCount == 0 {
		return nil, NewValidationError("本次任务的工作流没有编译（blueprint_node=0），没有可修改的节点")
	}
	translate, ok := findingTranslators[f.Category]
	if !ok {
		return nil, NewValidationError(fmt.Sprintf("类别 %q 没有确定性转换，这条发现只能人工处理", f.Category))
	}
	t, err := s.buildTranslateCtx(ctx, goalID, versionID)
	if err != nil {
		return nil, fmt.Errorf("load pinned spec: %w", err)
	}
	// The translators read the execution graph; the FROZEN copy from the report
	// is what the analysis actually saw, so a proposal is never derived from a
	// graph that drifted after the run.
	var wfJSON string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(workflow_json,'') FROM rsi_report WHERE session_id=?`, sessionID).Scan(&wfJSON); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load frozen workflow: %w", err)
	}
	t.g = decodeWorkflow(wfJSON)
	if t.g == nil {
		if t.g, err = s.BuildGoalWorkflow(ctx, goalID); err != nil {
			return nil, fmt.Errorf("rebuild goal workflow: %w", err)
		}
	}
	t.remaining = -1
	t.seen = map[string]bool{}
	t.touched = map[string]bool{}
	t.cats = map[string]bool{}
	// Nodes/edges already claimed by a pending or approved proposal: patching
	// them again would stack conflicting edits on the same base version. The
	// auto path drops the draft silently; a manual request must be told.
	if prow, err := s.st.DB().QueryContext(ctx,
		`SELECT p.patches_json FROM blueprint_proposal p JOIN blueprint_version v ON v.id=p.base_version_id
		 WHERE v.blueprint_id=? AND p.status IN ('pending','approved')`, t.version.BlueprintID); err == nil {
		for prow.Next() {
			var raw string
			if err := prow.Scan(&raw); err != nil {
				break
			}
			var patches []BlueprintPatch
			if err := json.Unmarshal([]byte(raw), &patches); err != nil {
				continue
			}
			for k := range patchKeys(patches) {
				t.touched[k] = true
			}
		}
		prow.Close()
	}
	draft, err := translate(ctx, s, t, f)
	if err != nil {
		return nil, fmt.Errorf("translate finding: %w", err)
	}
	if draft == nil || len(draft.Patches) == 0 || strings.TrimSpace(draft.Title) == "" {
		return nil, NewValidationError("这条发现没有可安全转换的结构改动，无法生成提案")
	}
	keys := patchKeys(draft.Patches)
	conflict := ""
	for k := range keys {
		if t.touched[k] {
			conflict = k
			break
		}
	}
	if conflict != "" {
		return nil, NewConflictError(fmt.Sprintf("提案会改动节点 %q，而已有待审提案占用了它 — 先处理那条提案", conflict))
	}
	// Exact-title dedup: the same transform from a re-run would produce a
	// byte-identical title. Returning that proposal is truthful (it IS the
	// patch), and it keeps the review from stacking twin proposals.
	var twinID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT p.id FROM blueprint_proposal p JOIN blueprint_version v ON v.id=p.base_version_id
		 WHERE v.blueprint_id=? AND p.title=? ORDER BY p.created_at DESC LIMIT 1`,
		t.version.BlueprintID, draft.Title).Scan(&twinID); err == nil && twinID != "" {
		if p, err := s.findProposal(ctx, t.version.BlueprintID, twinID); err == nil {
			return p, nil
		}
	}
	p, err := s.blueprintSvc.CreateProposal(ctx, t.version.BlueprintID, versionID, draft.Title, draft.Hypothesis,
		draft.Metric, draft.Direction, draft.Patches, f.ID, translatorCreatedByType, translatorCreatedByID)
	if err != nil {
		return nil, err // ErrValidation → 400 with the validator's own reason
	}
	logging.Infof("[rsi] manual proposal drafted from finding %s (goal %s, status %s): %s", findingID, goalID, goalStatus, p.Title)
	return p, nil
}

// findProposal resolves one proposal inside its blueprint's list. The service
// exposes no single-proposal read, and the list is small (one blueprint's
// proposals), so a linear scan keeps the code path identical to the UI's.
func (s *RSIService) findProposal(ctx context.Context, blueprintID, proposalID string) (*BlueprintProposal, error) {
	list, err := s.blueprintSvc.ListProposals(ctx, blueprintID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == proposalID {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}
