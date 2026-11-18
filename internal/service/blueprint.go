package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
	"github.com/eushing/agentwork/internal/store"
)

// Blueprint Phase A (docs/BLUEPRINT-PROPOSAL.md): the serialized orchestration
// authority. The spec graph is a versioned JSON document; a goal PINS one
// published version, and Activate compiles it into sub_goal rows + gating
// edges in ONE transaction (two-beat compile: beat 1 = structure at goal
// start, beat 2 = brief placeholders at node release). From then on the
// spec graph — not a natural-language playbook — is the order-of-execution
// authority: the finalization guard counts blueprint_node rows, and a goal
// cannot reach done while a spec node is pending/running (漏跑 becomes a
// structural impossibility instead of a prompt hope).
//
// Phase A node set: task | condition | end. router/gate/spawn exist in the
// schema and the validator's port model so Phase B wires them into the same
// publish path, but publishing a graph that USES them is refused until then.

// ── spec graph JSON (proposal §9, the single source of truth) ──

// SpecGraph is blueprint_version.content decoded.
type SpecGraph struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Nodes       []SpecNode `json:"nodes"`
	Edges       []SpecEdge `json:"edges"`
}

// SpecNode is one spec node. The type-specific body lives in the field named
// by type (task/condition/router/gate/spawn/end) — mirrors the approved
// JSON Schema so the same document validates on both sides.
type SpecNode struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"` // task|condition|router|gate|spawn|end
	Title     string       `json:"title"`
	Task      *SpecTask    `json:"task,omitempty"`
	Condition *SpecCond    `json:"condition,omitempty"`
	Router    *SpecRouter  `json:"router,omitempty"` // Phase B
	Gate      *SpecGate    `json:"gate,omitempty"`   // Phase B
	Spawn     *SpecSpawn   `json:"spawn,omitempty"`  // Phase B
	End       *SpecEnd     `json:"end,omitempty"`
}

type SpecTask struct {
	Assignee string     `json:"assignee"` // "agent:xx" | "squad:xx"
	Brief    string     `json:"brief"`    // template: {{goal.<field>}} / {{node.<id>.output}}
	Verify   *SpecVerify `json:"verify,omitempty"`
}

type SpecVerify struct {
	Mode     string `json:"mode,omitempty"`     // domain_default|machine|agent
	Verifier string `json:"verifier,omitempty"` // agent id when mode=agent
}

type SpecCond struct {
	Expr  string    `json:"expr"` // restricted-operator expression over goal fields
	Cases []SpecCase `json:"cases"`
	Else  string    `json:"else,omitempty"` // default "else"
}

type SpecCase struct {
	ID   string `json:"id"`
	Test string `json:"test"`
}

type SpecRouter struct { // Phase B — present in the schema, refused at publish
	Classes  []SpecRouterClass `json:"classes"`
	Fallback string            `json:"fallback"`
}
type SpecRouterClass struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type SpecGate struct { // Phase B
	Prompt  string          `json:"prompt"`
	Actions []SpecGateAction `json:"actions"`
	Timeout *SpecGateTimeout `json:"timeout,omitempty"`
}
type SpecGateAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type SpecGateTimeout struct {
	After  string `json:"after"`
	Action string `json:"action"`
}

type SpecSpawn struct { // Phase B
	Assignee    string `json:"assignee"`
	Brief       string `json:"brief"`
	ChildType   string `json:"child_type"`
	MaxChildren int    `json:"max_children,omitempty"`
}

type SpecEnd struct {
	Outcome string `json:"outcome,omitempty"` // success|failure
}

// SpecEdge encodes a branch choice on its source_handle port (proposal §4.3):
// task ports done|failed, condition ports cases[].id|else, router ports
// classes[].id|fallback, gate ports actions[].id|timeout.
type SpecEdge struct {
	Source       string `json:"source"`
	SourceHandle string `json:"source_handle,omitempty"`
	Target       string `json:"target"`
}

// ── DB rows (schema.sql §5) ──

type Blueprint struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	CreatedByType string `json:"created_by_type"`
	CreatedByID   string `json:"created_by_id"`
	CreatedAt     string `json:"created_at"`
}

type BlueprintVersion struct {
	ID          string `json:"id"`
	BlueprintID string `json:"blueprint_id"`
	Version     int    `json:"version"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
	Status      string `json:"status"` // draft|published|retired
	Changelog   string `json:"changelog"`
	CreatedAt   string `json:"created_at"`
}

// BlueprintNode is the per-goal instance ledger row — the alignment anchor
// between the spec node (node_key) and the executing sub_goal. state is the
// NODE's own lifecycle: pending → running → settled|failed, plus skipped
// (pruned by a condition choice). end nodes stay pending until
// settleBlueprintEndsTx flips them when every active node is terminal.
type BlueprintNode struct {
	ID            string `json:"id"`
	GoalID        string `json:"goal_id"`
	NodeKey       string `json:"node_key"`
	ParentKey     string `json:"parent_key"`
	NodeType      string `json:"node_type"`
	SubGoalID     string `json:"sub_goal_id"`
	State         string `json:"state"`
	ResolvedConfig string `json:"resolved_config"`
	CreatedAt     string `json:"created_at"`
}

// ── validation ──

var specNodeIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var specAssigneeRe = regexp.MustCompile(`^(agent|squad):[a-z0-9_.-]+$`)

// templateGoalRefRe matches {{goal.<field>}}; templateNodeRefRe matches
// {{node.<id>.output}} — the two brief placeholder forms (V5).
var templateGoalRefRe = regexp.MustCompile(`\{\{\s*goal\.([a-z_]+)\s*\}\}`)
var templateNodeRefRe = regexp.MustCompile(`\{\{\s*node\.([a-z][a-z0-9_]{0,63})\.output\s*\}\}`)

// goalTemplateFields is the V5 allowlist of {{goal.X}} interpolation fields.
var goalTemplateFields = map[string]bool{
	"id": true, "title": true, "description": true, "source_ref": true,
	"category": true, "domain_id": true, "connection_id": true,
	"execution_mode": true, "created_by_type": true,
}

// phaseANodeTypes are the node types a PUBLISHED graph may use in Phase A.
// router/gate/spawn validate structurally (so drafts carry them legibly) but
// publish refuses them until Phase B wires their execution paths.
var phaseANodeTypes = map[string]bool{"task": true, "condition": true, "end": true}

// declaredPorts returns the outbound port ids a node type declares (V1/V3).
// task: done|failed (implicit; failed is optional — a leaf task may only
// carry done); condition: cases[].id + else; router: classes[].id + fallback;
// gate: actions[].id + timeout.action (mandatory, see V3).
func (n *SpecNode) declaredPorts() []string {
	switch n.Type {
	case "task":
		return []string{"done", "failed"}
	case "condition":
		ports := make([]string, 0, len(n.Condition.Cases)+1)
		for _, c := range n.Condition.Cases {
			ports = append(ports, c.ID)
		}
		return append(ports, n.elsePort())
	case "router":
		ports := make([]string, 0, len(n.Router.Classes)+1)
		for _, c := range n.Router.Classes {
			ports = append(ports, c.ID)
		}
		return append(ports, n.Router.Fallback)
	case "gate":
		ports := make([]string, 0, len(n.Gate.Actions)+1)
		for _, a := range n.Gate.Actions {
			ports = append(ports, a.ID)
		}
		return append(ports, "timeout")
	case "spawn":
		return []string{"done", "failed"}
	case "end":
		return nil
	}
	return nil
}

func (n *SpecNode) elsePort() string {
	if n.Condition != nil && n.Condition.Else != "" {
		return n.Condition.Else
	}
	return "else"
}

// mandatoryPorts are the ports every declared edge MUST exist for (V3): an
// edge case covers the graph's liveness. task.failed and router.timeout are
// optional (a task may be trusted to always succeed; a router has fallback).
func (n *SpecNode) mandatoryPorts() []string {
	switch n.Type {
	case "task":
		return []string{"done"}
	case "condition":
		return n.declaredPorts() // every case + else must go somewhere
	case "router":
		ports := make([]string, 0, len(n.Router.Classes)+1)
		for _, c := range n.Router.Classes {
			ports = append(ports, c.ID)
		}
		return append(ports, n.Router.Fallback)
	case "gate":
		ports := make([]string, 0, len(n.Gate.Actions)+1)
		for _, a := range n.Gate.Actions {
			ports = append(ports, a.ID)
		}
		return append(ports, "timeout") // V3: gate timeout must go somewhere
	}
	return nil
}

// ValidateSpec runs the STATIC checks V1–V5 on a parsed graph and returns the
// problem list (empty = valid). V6 (content hash) is a storage-layer concern
// handled at write/read. V4 (assignee resolves to a registered agent/squad)
// needs the DB and is layered on by the service.
func ValidateSpec(g *SpecGraph) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// V1: shape.
	if strings.TrimSpace(g.Name) == "" {
		add("graph.name is required")
	}
	if len(g.Nodes) == 0 {
		add("graph.nodes must not be empty")
	}
	if len(g.Edges) == 0 {
		add("graph.edges must not be empty (a spec graph must connect nodes)")
	}
	byID := map[string]*SpecNode{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if !specNodeIDRe.MatchString(n.ID) {
			add("node %q: id must match ^[a-z][a-z0-9_]{0,63}$", n.ID)
		}
		if byID[n.ID] != nil {
			add("node %q: duplicate id", n.ID)
			continue
		}
		byID[n.ID] = n
	}
	typeOK := map[string]bool{"task": true, "condition": true, "router": true, "gate": true, "spawn": true, "end": true}
	hasEnd := false
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Title == "" {
			add("node %q: title is required", n.ID)
		}
		if !typeOK[n.Type] {
			add("node %q: unknown type %q", n.ID, n.Type)
			continue
		}
		switch n.Type {
		case "task":
			if n.Task == nil {
				add("node %q: task body is required", n.ID)
				break
			}
			if !specAssigneeRe.MatchString(n.Task.Assignee) {
				add("node %q: task.assignee must be agent:<id> or squad:<id>", n.ID)
			}
			if strings.TrimSpace(n.Task.Brief) == "" {
				add("node %q: task.brief is required", n.ID)
			}
			if n.Task.Verify != nil {
				switch n.Task.Verify.Mode {
				case "", "domain_default", "machine":
				case "agent":
					if n.Task.Verify.Verifier == "" {
						add("node %q: verify.mode=agent requires verify.verifier", n.ID)
					}
				default:
					add("node %q: verify.mode must be domain_default|machine|agent", n.ID)
				}
			}
		case "condition":
			if n.Condition == nil {
				add("node %q: condition body is required", n.ID)
				break
			}
			if len(n.Condition.Cases) == 0 {
				add("node %q: condition requires ≥1 case", n.ID)
			}
			seen := map[string]bool{}
			for _, c := range n.Condition.Cases {
				if !specNodeIDRe.MatchString(c.ID) {
					add("node %q: case id %q must match the port pattern", n.ID, c.ID)
				}
				if seen[c.ID] {
					add("node %q: duplicate case id %q", n.ID, c.ID)
				}
				seen[c.ID] = true
			}
			if _, err := ParseCondTest(n.Condition.Expr); err != nil {
				add("node %q: condition.expr: %v", n.ID, err)
			}
			for _, c := range n.Condition.Cases {
				if _, err := ParseCondTest(c.Test); err != nil {
					add("node %q: case %q: %v", n.ID, c.ID, err)
				}
			}
		case "end":
			if n.End != nil && n.End.Outcome != "" && n.End.Outcome != "success" && n.End.Outcome != "failure" {
				add("node %q: end.outcome must be success|failure", n.ID)
			}
			hasEnd = true
		}
	}
	if len(byID) > 0 && !hasEnd {
		add("graph must contain at least one end node")
	}

	// V1 (edges): referenced nodes exist, handles are declared ports,
	// no duplicate edges, end nodes have no outgoing edges.
	edgeSeen := map[string]bool{}
	for _, e := range g.Edges {
		src, sok := byID[e.Source]
		_, tok := byID[e.Target]
		if !sok {
			add("edge %s→%s: source node %q does not exist", e.Source, e.Target, e.Source)
			continue
		}
		if !tok {
			add("edge %s→%s: target node %q does not exist", e.Source, e.Target, e.Target)
			continue
		}
		if src.Type == "end" {
			add("edge %s→%s: end nodes have no outbound ports", e.Source, e.Target)
			continue
		}
		if e.SourceHandle == "" {
			add("edge %s→%s: source_handle is required", e.Source, e.Target)
			continue
		}
		declared := false
		for _, p := range src.declaredPorts() {
			if p == e.SourceHandle {
				declared = true
				break
			}
		}
		if !declared {
			add("edge %s→%s: %q is not a declared port of %s (%s)", e.Source, e.Target, e.SourceHandle, e.Source, strings.Join(src.declaredPorts(), "|"))
		}
		key := e.Source + "\x00" + e.SourceHandle
		if edgeSeen[key] {
			add("edge %s→%s: duplicate outbound branch for port %q", e.Source, e.Target, e.SourceHandle)
		}
		edgeSeen[key] = true
	}

	// V3: every mandatory port has an outbound edge.
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type == "end" {
			continue
		}
		for _, p := range n.mandatoryPorts() {
			if !edgeSeen[n.ID+"\x00"+p] {
				add("node %q: mandatory port %q has no outbound edge", n.ID, p)
			}
		}
	}

	// V2: acyclic; every node reachable from a root (no incoming edge) and
	// able to reach an end node. Roots themselves must be tasks (a condition
	// with no input has nothing to branch on).
	if len(byID) > 0 {
		adj := map[string][]string{}
		inDeg := map[string]int{}
		for _, e := range g.Edges {
			if byID[e.Source] == nil || byID[e.Target] == nil {
				continue
			}
			adj[e.Source] = append(adj[e.Source], e.Target)
			inDeg[e.Target]++
		}
		// Kahn cycle check.
		deg := map[string]int{}
		for id := range byID {
			deg[id] = inDeg[id]
		}
		queue := make([]string, 0, len(byID))
		for id, d := range deg {
			if d == 0 {
				queue = append(queue, id)
			}
		}
		visited := 0
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			visited++
			for _, nxt := range adj[id] {
				deg[nxt]--
				if deg[nxt] == 0 {
					queue = append(queue, nxt)
				}
			}
		}
		if visited != len(byID) {
			add("graph has a cycle (%d of %d nodes in a topological order)", visited, len(byID))
		}
		// Reachability: forward BFS from roots; backward BFS from end nodes.
		var roots []string
		for id := range byID {
			if inDeg[id] == 0 {
				roots = append(roots, id)
			}
		}
		if len(roots) == 0 {
			add("graph has no root node (all nodes have inbound edges)")
		}
		for _, r := range roots {
			if t := byID[r].Type; t != "task" {
				add("root node %q must be a task (got %s — a %s needs an upstream context)", r, t, t)
			}
		}
		reachFwd := bfsFrom(roots, adj)
		for id := range byID {
			if !reachFwd[id] {
				add("node %q is unreachable from any root", id)
			}
		}
		rev := map[string][]string{}
		for from, tos := range adj {
			for _, to := range tos {
				rev[to] = append(rev[to], from)
			}
		}
		var ends []string
		for id := range byID {
			if byID[id].Type == "end" {
				ends = append(ends, id)
			}
		}
		reachBack := bfsFrom(ends, rev)
		for id := range byID {
			if !reachBack[id] {
				add("node %q cannot reach any end node", id)
			}
		}
	}

	// V5: template refs — {{goal.<field>}} allowlist, {{node.<id>.output}}
	// references an existing node of type task (ancestor-ness is a compile-
	// time property — the release-time resolver checks it per pruned graph).
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Task == nil || n.Task.Brief == "" {
			continue
		}
		for _, m := range templateGoalRefRe.FindAllStringSubmatch(n.Task.Brief, -1) {
			if !goalTemplateFields[m[1]] {
				add("node %q: brief refers to unknown goal field %q", n.ID, m[1])
			}
		}
		for _, m := range templateNodeRefRe.FindAllStringSubmatch(n.Task.Brief, -1) {
			target, ok := byID[m[1]]
			if !ok {
				add("node %q: brief refers to unknown node %q", n.ID, m[1])
			} else if target.Type != "task" {
				add("node %q: brief refers to %s (%s) — only task nodes produce output", n.ID, m[1], target.Type)
			}
		}
	}
	return problems
}

func bfsFrom(starts []string, adj map[string][]string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string(nil), starts...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, adj[id]...)
	}
	return seen
}

// ── condition expression (Q2 定稿: restricted operator set) ──

// ParseCondTest parses one restricted clause:
//
//	goal.<field> <op> <literal>   op ∈ == != contains starts_with ends_with
//	goal.<field> <unary>          unary ∈ exists empty
//
// Literals are double- or single-quoted strings. The grammar is deliberately
// tiny: no arithmetic, no nested references, no scripting — branching stays
// deterministic code, never an interpreter with a big attack surface.
func ParseCondTest(test string) (condTest, error) {
	t := strings.TrimSpace(test)
	if t == "" {
		return condTest{}, errors.New("empty test")
	}
	var reOp = regexp.MustCompile(`^goal\.([a-z_]+)\s+(==|!=|contains|starts_with|ends_with|exists|empty)\s*(.*)$`)
	m := reOp.FindStringSubmatch(t)
	if m == nil {
		return condTest{}, fmt.Errorf("must look like: goal.<field> <op> <literal> (got %q)", t)
	}
	field, op, rest := m[1], m[2], strings.TrimSpace(m[3])
	if !goalTemplateFields[field] {
		return condTest{}, fmt.Errorf("unknown goal field %q", field)
	}
	ct := condTest{Field: field, Op: op}
	switch op {
	case "exists", "empty":
		if rest != "" {
			return condTest{}, fmt.Errorf("%s takes no literal (got %q)", op, rest)
		}
	default:
		if len(rest) < 2 || (rest[0] != '"' && rest[0] != '\'') || rest[len(rest)-1] != rest[0] {
			return condTest{}, fmt.Errorf("%s needs a quoted literal (got %q)", op, rest)
		}
		ct.Literal = rest[1 : len(rest)-1]
	}
	return ct, nil
}

// condTest is one parsed clause of a condition case.
type condTest struct {
	Field   string
	Op      string
	Literal string
}

// eval evaluates the clause against a goal field value.
func (c condTest) eval(value string) bool {
	switch c.Op {
	case "exists":
		return value != ""
	case "empty":
		return value == ""
	case "==":
		return value == c.Literal
	case "!=":
		return value != c.Literal
	case "contains":
		return strings.Contains(value, c.Literal)
	case "starts_with":
		return strings.HasPrefix(value, c.Literal)
	case "ends_with":
		return strings.HasSuffix(value, c.Literal)
	}
	return false
}

// evalCondTest evaluates one case test against a goal.
func evalCondTest(test string, goal map[string]string) (bool, error) {
	ct, err := ParseCondTest(test)
	if err != nil {
		return false, err
	}
	return ct.eval(goal[ct.Field]), nil
}

// hashContent is the V6 drift guard: sha256 hex of the canonical content.
// hashContent: see hook.go — same sha256 hex helper, declared once per package.

// parseSpecContent parses + hash-checks a stored version (V6).
func parseSpecContent(v *BlueprintVersion) (*SpecGraph, error) {
	if hashContent(v.Content) != v.ContentHash {
		return nil, NewConflictError(fmt.Sprintf("blueprint_version %s content_hash mismatch (stored=%s recomputed=%s) — content drift", v.ID, v.ContentHash, hashContent(v.Content)))
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(v.Content), &g); err != nil {
		return nil, NewValidationError("blueprint content is not valid JSON: " + err.Error())
	}
	return &g, nil
}

// ── BlueprintService: CRUD + publish gate ──

type BlueprintService struct {
	st  *store.Store
	bus *events.Bus
}

func NewBlueprintService(st *store.Store, bus *events.Bus) *BlueprintService {
	return &BlueprintService{st: st, bus: bus}
}

// maxSpecNodes bounds a compiled graph's fan-out sanity. Decision 6-8 caps
// LLM-initiated fan-out at 20 active sub-goals; a blueprint is human-authored
// so it gets headroom, but not unbounded — a paste accident must fail at
// publish, not at goal activation.
const maxSpecNodes = 64

// validateContent is the full publish gate: parse, V1–V5, V4 assignee
// resolution against the registry, Phase A type set.
func (s *BlueprintService) validateContent(ctx context.Context, content string, enforcePhaseA bool) (*SpecGraph, error) {
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return nil, NewValidationError("blueprint content is not valid JSON: " + err.Error())
	}
	if len(g.Nodes) > maxSpecNodes {
		return nil, NewValidationError(fmt.Sprintf("blueprint has %d nodes (max %d)", len(g.Nodes), maxSpecNodes))
	}
	if problems := ValidateSpec(&g); len(problems) > 0 {
		return nil, NewValidationError("blueprint validation failed:\n  - " + strings.Join(problems, "\n  - "))
	}
	// V4: every task assignee resolves to a registered agent/squad.
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != "task" || n.Task == nil {
			continue
		}
		kind, id, ok := strings.Cut(n.Task.Assignee, ":")
		if !ok {
			continue // V1 already reported the malformed shape
		}
		var count int
		table := "agent"
		if kind == "squad" {
			table = "squad"
		}
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table+` WHERE id=?`, id).Scan(&count); err != nil {
			return nil, fmt.Errorf("resolve assignee %s: %w", n.Task.Assignee, err)
		}
		if count == 0 {
			return nil, NewValidationError(fmt.Sprintf("node %q: assignee %s does not resolve to a registered %s (expected %s id, e.g. %s:<32-hex-id>)", n.ID, n.Task.Assignee, kind, kind, kind))
		}
		if n.Task.Verify != nil && n.Task.Verify.Mode == "agent" && n.Task.Verify.Verifier != "" {
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, n.Task.Verify.Verifier, "verify.verifier agent"); err != nil {
				return nil, err
			}
		}
	}
	if enforcePhaseA {
		for i := range g.Nodes {
			n := &g.Nodes[i]
			if !phaseANodeTypes[n.Type] {
				return nil, NewValidationError(fmt.Sprintf("node %q: type %q is not supported until Phase B (Phase A allows task|condition|end)", n.ID, n.Type))
			}
		}
	}
	return &g, nil
}

// Create registers a new blueprint with its draft v1.
func (s *BlueprintService) Create(ctx context.Context, name, description, content, createdByType, createdByID string) (*Blueprint, error) {
	if strings.TrimSpace(name) == "" {
		return nil, NewFieldRequiredError("name")
	}
	g, err := s.validateContent(ctx, content, true)
	if err != nil {
		return nil, err
	}
	if description == "" {
		description = g.Description
	}
	bp := Blueprint{
		ID:            newID(),
		Name:          name,
		Description:   description,
		CreatedByType: orDefault(createdByType, "human"),
		CreatedByID:   createdByID,
		CreatedAt:     now(),
	}
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blueprint (id,name,description,created_by_type,created_by_id,created_at) VALUES (?,?,?,?,?,?)`,
		bp.ID, bp.Name, bp.Description, bp.CreatedByType, bp.CreatedByID, bp.CreatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, NewConflictError(fmt.Sprintf("blueprint name %q already exists", name))
		}
		return nil, fmt.Errorf("insert blueprint: %w", err)
	}
	v := BlueprintVersion{
		ID:          newID(),
		BlueprintID: bp.ID,
		Version:     1,
		Content:     content,
		ContentHash: hashContent(content),
		Status:      "draft",
		CreatedAt:   now(),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,changelog,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		v.ID, v.BlueprintID, v.Version, v.Content, v.ContentHash, v.Status, v.Changelog, v.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert blueprint version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	logging.Infof("blueprint: created %q (%d nodes, %d edges) as draft v1", bp.Name, len(g.Nodes), len(g.Edges))
	return &bp, nil
}

// UpdateDraft replaces a draft version's content (validation gate applies).
func (s *BlueprintService) UpdateDraft(ctx context.Context, versionID, content string) (*BlueprintVersion, error) {
	v, err := s.getVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != "draft" {
		return nil, NewConflictError(fmt.Sprintf("version %d is %s — only drafts are editable (published versions are immutable; create a new draft instead)", v.Version, v.Status))
	}
	if _, err := s.validateContent(ctx, content, true); err != nil {
		return nil, err
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE blueprint_version SET content=?, content_hash=? WHERE id=?`,
		content, hashContent(content), versionID); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}
	return s.getVersion(ctx, versionID)
}

// Publish flips draft → published after the full gate. Publishing is the only
// path that makes a version bindable to goals.
func (s *BlueprintService) Publish(ctx context.Context, versionID, changelog string) (*BlueprintVersion, error) {
	v, err := s.getVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != "draft" {
		return nil, NewConflictError(fmt.Sprintf("version %d is already %s", v.Version, v.Status))
	}
	// The publish gate re-validates the STORED content (not the caller's
	// memory of it): a drifted row fails the V6 hash check inside.
	if _, err := s.validateContent(ctx, v.Content, true); err != nil {
		return nil, err
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE blueprint_version SET status='published', changelog=? WHERE id=?`, changelog, versionID); err != nil {
		return nil, fmt.Errorf("publish version: %w", err)
	}
	logging.Infof("blueprint: published %s v%d", v.BlueprintID, v.Version)
	s.bus.Publish(ctx, events.Event{Topic: "blueprint.published", Payload: map[string]any{
		"blueprint_id": v.BlueprintID, "version_id": v.ID, "version": v.Version,
	}})
	return s.getVersion(ctx, versionID)
}

// NewDraft opens version N+1 from a published version (the Phase C iteration
// path — proposals patch content, publish bumps, A/B pins).
func (s *BlueprintService) NewDraft(ctx context.Context, blueprintID, changelog string) (*BlueprintVersion, error) {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var maxVer int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM blueprint_version WHERE blueprint_id=?`, blueprintID).Scan(&maxVer); err != nil {
		return nil, fmt.Errorf("load max version: %w", err)
	}
	var content, hash string
	if err := tx.QueryRowContext(ctx,
		`SELECT content, content_hash FROM blueprint_version WHERE blueprint_id=? AND version=?`, blueprintID, maxVer).
		Scan(&content, &hash); err != nil {
		return nil, fmt.Errorf("load latest version: %w", err)
	}
	v := BlueprintVersion{
		ID:          newID(),
		BlueprintID: blueprintID,
		Version:     maxVer + 1,
		Content:     content,
		ContentHash: hash,
		Status:      "draft",
		Changelog:   changelog,
		CreatedAt:   now(),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,changelog,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		v.ID, v.BlueprintID, v.Version, v.Content, v.ContentHash, v.Status, v.Changelog, v.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert new draft: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *BlueprintService) getVersion(ctx context.Context, versionID string) (*BlueprintVersion, error) {
	var v BlueprintVersion
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,blueprint_id,version,content,content_hash,status,changelog,created_at FROM blueprint_version WHERE id=?`, versionID).
		Scan(&v.ID, &v.BlueprintID, &v.Version, &v.Content, &v.ContentHash, &v.Status, &v.Changelog, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load blueprint version: %w", err)
	}
	return &v, nil
}

func (s *BlueprintService) GetVersion(ctx context.Context, versionID string) (*BlueprintVersion, error) {
	return s.getVersion(ctx, versionID)
}

// List returns all blueprints with their version/发布 state summary.
func (s *BlueprintService) List(ctx context.Context) ([]Blueprint, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,name,description,created_by_type,created_by_id,created_at FROM blueprint ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list blueprints: %w", err)
	}
	defer rows.Close()
	var out []Blueprint
	for rows.Next() {
		var b Blueprint
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &b.CreatedByType, &b.CreatedByID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListVersions returns one blueprint's versions newest first.
func (s *BlueprintService) ListVersions(ctx context.Context, blueprintID string) ([]BlueprintVersion, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,blueprint_id,version,content,content_hash,status,changelog,created_at
		 FROM blueprint_version WHERE blueprint_id=? ORDER BY version DESC`, blueprintID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()
	var out []BlueprintVersion
	for rows.Next() {
		var v BlueprintVersion
		if err := rows.Scan(&v.ID, &v.BlueprintID, &v.Version, &v.Content, &v.ContentHash, &v.Status, &v.Changelog, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns the blueprint with its versions.
func (s *BlueprintService) Get(ctx context.Context, id string) (*Blueprint, []BlueprintVersion, error) {
	var b Blueprint
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,name,description,created_by_type,created_by_id,created_at FROM blueprint WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Description, &b.CreatedByType, &b.CreatedByID, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load blueprint: %w", err)
	}
	versions, err := s.ListVersions(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return &b, versions, nil
}

// Delete refuses while a non-terminal goal still pins a version of the
// blueprint (an active blueprint-driven goal must not lose its authority).
// Terminal goals keep their historical pin — the rows are history.
func (s *BlueprintService) Delete(ctx context.Context, id string) error {
	var active int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal g JOIN blueprint_version v ON g.blueprint_version_id=v.id
		 WHERE v.blueprint_id=? AND g.status IN ('backlog','active','review')`, id).Scan(&active); err != nil {
		return fmt.Errorf("count pinned goals: %w", err)
	}
	if active > 0 {
		return NewConflictError(fmt.Sprintf("blueprint is pinned by %d non-terminal goal(s) — resolve them first", active))
	}
	res, err := s.st.DB().ExecContext(ctx, `DELETE FROM blueprint WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete blueprint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ── spec graph → WorkflowGraph shape (the read-only frontend view) ──

// SpecWorkflowGraph assembles a version's spec graph into the SAME shape
// workflow.go emits for execution graphs (proposal §4.12: the frontend
// renders the spec graph with zero layout changes). Node kinds extend the
// execution set with the spec-only kinds; edges carry kind "spec" with
// source "explicit" — the spec graph has no inferred edges by definition.
func (s *BlueprintService) SpecWorkflowGraph(ctx context.Context, versionID string) (*WorkflowGraph, error) {
	v, err := s.getVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	g, err := parseSpecContent(v)
	if err != nil {
		return nil, err
	}
	wf := &WorkflowGraph{
		GoalID:      "blueprint:" + v.ID,
		GeneratedAt: now(),
		Frozen:      true, // a published spec version is immutable
		Nodes:       make([]WorkflowNode, 0, len(g.Nodes)),
		Edges:       make([]WorkflowEdge, 0, len(g.Edges)),
		Metrics:     map[string]any{},
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		wn := WorkflowNode{Key: n.ID, Kind: n.Type, Label: n.Title, Status: "spec"}
		if n.Task != nil {
			wn.AssigneeID = n.Task.Assignee
		}
		wf.Nodes = append(wf.Nodes, wn)
	}
	for _, e := range g.Edges {
		wf.Edges = append(wf.Edges, WorkflowEdge{
			From: e.Source, To: e.Target, Kind: "spec", Source: "explicit",
		})
	}
	// Metric keys the metrics row can render without special cases.
	wf.Metrics["node_count"] = len(wf.Nodes)
	wf.Metrics["edge_count"] = len(wf.Edges)
	var tasks, conds int
	for i := range g.Nodes {
		switch g.Nodes[i].Type {
		case "task":
			tasks++
		case "condition":
			conds++
		}
	}
	wf.Metrics["task_count"] = tasks
	wf.Metrics["condition_count"] = conds
	sort.SliceStable(wf.Nodes, func(i, j int) bool { return wf.Nodes[i].Key < wf.Nodes[j].Key })
	return wf, nil
}

// ── compile (beat 1) + node lifecycle hooks — GoalService methods ──

// compileBlueprintTx is the beat-1 compiler, called INSIDE Activate's
// transaction: blueprint version → evaluated conditions → pruned graph →
// one sub_goal per surviving task (gated via depends_on, the existing DAG
// executor) + one blueprint_node row per spec node. Idempotent by the
// backlog→active guard above it; if blueprint_node rows exist for the goal
// (reopen), the compile is skipped and the goal runs in manual follow-up
// mode (documented proposal §7 Phase A). Returns the beat-1 run events for
// post-commit publish via out (never fire inside the transaction — a
// rollback must not launch runs).
func (s *GoalService) compileBlueprintTx(ctx context.Context, tx *sql.Tx, goalID string, out *[]events.Event) error {
	var versionID string
	if err := tx.QueryRowContext(ctx,
		`SELECT blueprint_version_id FROM goal WHERE id=?`, goalID).Scan(&versionID); err != nil {
		return fmt.Errorf("load goal blueprint pin: %w", err)
	}
	if versionID == "" {
		return nil // infer mode — no spec authority, unchanged semantics
	}
	// Reopen guard: rows already exist → nothing to compile (manual mode).
	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&existing); err != nil {
		return fmt.Errorf("count blueprint nodes: %w", err)
	}
	if existing > 0 {
		logging.Infof("blueprint: goal %s reopened — spec rows exist, manual follow-up mode", goalID)
		return nil
	}
	var content, hash string
	if err := tx.QueryRowContext(ctx,
		`SELECT content, content_hash FROM blueprint_version WHERE id=?`, versionID).
		Scan(&content, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewValidationError("the goal's pinned blueprint version no longer exists")
		}
		return fmt.Errorf("load pinned version: %w", err)
	}
	if hashContent(content) != hash {
		return NewConflictError("pinned blueprint version content_hash mismatch — refusing to compile a drifted spec")
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return NewValidationError("pinned blueprint content is not valid JSON: " + err.Error())
	}

	// Goal fields for condition evaluation + {{goal.*}} interpolation.
	var title, description, sourceRef, category, domainID, connID, createdByType string
	if err := tx.QueryRowContext(ctx,
		`SELECT title, COALESCE(description,''), COALESCE(source_ref,''), category, COALESCE(domain_id,''), COALESCE(connection_id,''), created_by_type FROM goal WHERE id=?`,
		goalID).Scan(&title, &description, &sourceRef, &category, &domainID, &connID, &createdByType); err != nil {
		return fmt.Errorf("load goal for compile: %w", err)
	}
	goalFields := map[string]string{
		"id": goalID, "title": title, "description": description, "source_ref": sourceRef,
		"category": category, "domain_id": domainID, "connection_id": connID,
		"execution_mode": "", "created_by_type": createdByType,
	}

	byID := map[string]*SpecNode{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}

	// Evaluate conditions: choice[nodeKey] = chosen port. First true case
	// wins; no case matches → else port. An eval error fails the activation
	// (better now than mid-run).
	choice := map[string]string{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != "condition" || n.Condition == nil {
			continue
		}
		chosen := ""
		for _, c := range n.Condition.Cases {
			ok, err := evalCondTest(c.Test, goalFields)
			if err != nil {
				return NewValidationError(fmt.Sprintf("condition %q case %q: %v", n.ID, c.ID, err))
			}
			if ok {
				chosen = c.ID
				break
			}
		}
		if chosen == "" {
			chosen = n.elsePort()
		}
		choice[n.ID] = chosen
	}

	// Prune: walk from the roots following ONLY chosen condition ports; a
	// node not reachable in the pruned graph is skipped. Roots are nodes
	// with NO incoming edges in the FULL graph — in-degree must count every
	// edge, otherwise a node whose only inbound edge was pruned (e.g. the
	// cold branch of a condition) masquerades as a root and comes back live.
	// (Phase A: conditions are all evaluated at beat 1 — expr may only
	// reference goal fields, so nothing about them can change mid-run.)
	inDeg := map[string]int{}
	adj := map[string][]string{}
	for _, e := range g.Edges {
		if byID[e.Source] == nil || byID[e.Target] == nil {
			continue
		}
		inDeg[e.Target]++
		if src := byID[e.Source]; src.Type == "condition" && e.SourceHandle != choice[src.ID] {
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
	}
	var roots []string
	for i := range g.Nodes {
		if inDeg[g.Nodes[i].ID] == 0 {
			roots = append(roots, g.Nodes[i].ID)
		}
	}
	live := bfsFrom(roots, adj)

	// Pruned adjacency (for ancestor computation): edges among live nodes
	// with chosen condition ports only.
	prunedAdj := map[string][]string{}
	for _, e := range g.Edges {
		if byID[e.Source] == nil || byID[e.Target] == nil {
			continue
		}
		if !live[e.Source] || !live[e.Target] {
			continue
		}
		if src := byID[e.Source]; src.Type == "condition" && e.SourceHandle != choice[src.ID] {
			continue
		}
		prunedAdj[e.Source] = append(prunedAdj[e.Source], e.Target)
	}

	// Ancestor task closure per task node (depends_on = the transitive task
	// ancestors in the pruned graph — the executor's gate semantics then
	// enforce order exactly like a leader-declared DAG: release is
	// transitively chained, so direct-dep declarations suffice and a
	// transitive superset is equivalent).
	rev := map[string][]string{}
	for from, tos := range prunedAdj {
		for _, to := range tos {
			rev[to] = append(rev[to], from)
		}
	}
	ancestorMemo := map[string][]string{}
	var ancestorTasks func(string) []string
	ancestorTasks = func(start string) []string {
		if m, ok := ancestorMemo[start]; ok {
			return m
		}
		seen := map[string]bool{}
		queue := append([]string(nil), rev[start]...)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			if byID[id].Type == "task" {
				continue // stop closure expansion at tasks — their deps cover the rest
			}
			queue = append(queue, rev[id]...)
		}
		var out []string
		for id := range seen {
			if byID[id].Type == "task" {
				out = append(out, id)
			}
		}
		sort.Strings(out)
		ancestorMemo[start] = out
		return out
	}

	ts := now()
	// Register every spec node's ledger row + create the task sub-goals.
	// Two passes so depends_on references exist before any ungated enqueue.
	//
	// CRITICAL: depends_on stores SUB_GOAL ids (the executor's gate looks
	// up sub_goal rows by id), not spec node keys — so every live task's
	// sub_goal id is PRE-ALLOCATED here, and ancestorTasks' keys are mapped
	// through sgByKey at insert time. (An early version stored raw keys:
	// the gate treated them as missing dependencies and cascaded the
	// dependents to failed on first wake.)
	sgByKey := map[string]string{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type == "task" && live[n.ID] {
			sgByKey[n.ID] = newID()
		}
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		state := "pending"
		resolved := ""
		switch {
		case !live[n.ID]:
			state = "skipped"
			resolved = pruneReason()
		case n.Type == "condition":
			state = "settled"
			resolved = `{"chosen":"` + choice[n.ID] + `"}`
		case n.Type == "task":
			state = "pending" // running stamped after enqueue below
		}
		if n.Type != "task" {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?)`,
				newID(), goalID, n.ID, n.Type, state, resolved, ts); err != nil {
				return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
			}
			continue
		}
		if !live[n.ID] {
			// Skipped task: ledger row only, no sub_goal (it must never run).
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?)`,
				newID(), goalID, n.ID, n.Type, state, resolved, ts); err != nil {
				return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
			}
			continue
		}
		kind, id, _ := strings.Cut(n.Task.Assignee, ":")
		if kind != "agent" && kind != "squad" {
			return NewValidationError(fmt.Sprintf("node %q: assignee %q not resolvable at compile time", n.ID, n.Task.Assignee))
		}
		verifier := ""
		if n.Task.Verify != nil && n.Task.Verify.Mode == "agent" {
			verifier = n.Task.Verify.Verifier
		}
		// Map ancestor spec keys → compiled sub_goal ids (see the pre-alloc
		// note above): the executor gates on sub_goal ids.
		depIDs := make([]string, 0, 8)
		for _, key := range ancestorTasks(n.ID) {
			if id, ok := sgByKey[key]; ok {
				depIDs = append(depIDs, id)
			}
		}
		ts0 := now()
		sg := SubGoal{
			ID:          sgByKey[n.ID],
			GoalID:      goalID,
			Title:       n.Title,
			Description: interpolateGoal(n.Task.Brief, goalFields),
			AssigneeID:  id,
			VerifierID:  verifier,
			DependsOn:   depIDs,
			Status:      "running",
			CreatedAt:   ts0,
		}
		sgOut, _, err := s.insertSubGoalTx(ctx, tx, sg, sg.DependsOn, "agent", "")
		if err != nil {
			return fmt.Errorf("compile node %q: %w", n.ID, err)
		}
		sgByKey[n.ID] = sgOut.ID
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,sub_goal_id,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			newID(), goalID, n.ID, n.Type, sg.ID, "pending", "", ts); err != nil {
			return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
		}
	}

	// Ungated tasks (no deps) start immediately; gated ones have no run and
	// wait for wakeDependentsTx — beat 2 resolves their node refs at release.
	var runEvs []events.Event
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != "task" || !live[n.ID] || len(ancestorTasks(n.ID)) > 0 {
			continue
		}
		sgID := sgByKey[n.ID]
		var dispatchID string
		if err := tx.QueryRowContext(ctx,
			`SELECT dispatch_comment_id FROM sub_goal WHERE id=?`, sgID).Scan(&dispatchID); err != nil {
			return fmt.Errorf("load dispatch anchor %q: %w", n.ID, err)
		}
		_, ev, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sgID, dispatchID)
		if err != nil {
			return fmt.Errorf("enqueue compiled task %q: %w", n.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE blueprint_node SET state='running' WHERE goal_id=? AND node_key=?`, goalID, n.ID); err != nil {
			return fmt.Errorf("stamp running %s: %w", n.ID, err)
		}
		if ev != nil {
			runEvs = append(runEvs, *ev)
		}
	}
	logging.Infof("blueprint: compiled %d nodes for goal %s (%d live tasks, %d skipped)", len(g.Nodes), goalID, len(sgByKey), len(g.Nodes)-len(sgByKey))
	// Run events fire only AFTER the caller commits (rollback must not
	// launch runs) — compileBlueprintTx returns them for post-commit publish.
	*out = runEvs
	return nil
}

// pruneReason records why a node was cut from the executable graph. In
// Phase A the only pruner is a condition choice (V2 makes structural
// unreachability a publish error), so the fact of pruning is the ledger
// datum; the resolving condition is re-derivable from the condition node's
// own resolved_config row.
func pruneReason() string {
	return `{"pruned":true}`
}

// interpolateGoal resolves {{goal.<field>}} placeholders in a brief.
// {{node.*}} placeholders are NOT touched here — they resolve at beat 2
// (release), when the upstream run's result_summary exists.
func interpolateGoal(brief string, goal map[string]string) string {
	return templateGoalRefRe.ReplaceAllStringFunc(brief, func(m string) string {
		sub := templateGoalRefRe.FindStringSubmatch(m)
		if v, ok := goal[sub[1]]; ok {
			return v
		}
		return m
	})
}

// publishCompiledRuns publishes beat-1 run events after the caller's commit
// (Activate). Never called inside the transaction — a rollback must not
// launch runs.
func (s *GoalService) publishCompiledRuns(ctx context.Context, evs []events.Event) {
	for _, ev := range evs {
		s.bus.Publish(ctx, ev)
	}
}

// markBlueprintNodeTx mirrors a sub_goal terminal transition onto its spec
// node (no-op for non-blueprint goals — the row lookup misses). Terminal
// states are monotonic: settled/skipped/failed never regress.
func markBlueprintNodeTx(ctx context.Context, tx *sql.Tx, subGoalID, state string) error {
	if subGoalID == "" || state == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET state=? WHERE sub_goal_id=? AND state IN ('pending','running')`,
		state, subGoalID); err != nil {
		return fmt.Errorf("mark blueprint node %s %s: %w", subGoalID, state, err)
	}
	return nil
}

// markBlueprintNodeRunningTx stamps the release transition (pending →
// running) when wakeDependentsTx enqueues a gated compiled task's first run.
func markBlueprintNodeRunningTx(ctx context.Context, tx *sql.Tx, subGoalID string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET state='running' WHERE sub_goal_id=? AND state='pending'`,
		subGoalID); err != nil {
		return fmt.Errorf("mark blueprint node running: %w", err)
	}
	return nil
}

// pendingBlueprintNodesTx counts spec nodes still holding the goal open.
// End nodes do NOT count as pending — settleBlueprintEndsTx flips them when
// this count reaches zero (they are the goal's own completion markers).
func (s *GoalService) pendingBlueprintNodesTx(ctx context.Context, tx *sql.Tx, goalID string) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=? AND node_type != 'end' AND state IN ('pending','running')`,
		goalID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending blueprint nodes: %w", err)
	}
	return n, nil
}

// settleBlueprintEndsTx flips end nodes to settled once every active node is
// terminal — the spec-graph completion marker. Idempotent; called from the
// finalization guard's path (any run end), so a skipped flip self-heals.
func (s *GoalService) settleBlueprintEndsTx(ctx context.Context, tx *sql.Tx, goalID string) error {
	pending, err := s.pendingBlueprintNodesTx(ctx, tx, goalID)
	if err != nil || pending > 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET state='settled', resolved_config=COALESCE(resolved_config,'') WHERE goal_id=? AND node_type='end' AND state='pending'`,
		goalID); err != nil {
		return fmt.Errorf("settle blueprint ends: %w", err)
	}
	return nil
}

// resolveReleasedBriefTx is beat 2: a gated compiled task is being released —
// resolve {{node.<key>.output}} placeholders against the upstream sub_goal's
// last run result_summary, then snapshot the resolved brief into the node's
// resolved_config (the RSI-digestible instantiation record). No-op for briefs
// without node refs.
func (s *GoalService) resolveReleasedBriefTx(ctx context.Context, tx *sql.Tx, goalID, subGoalID string) error {
	var description string
	if err := tx.QueryRowContext(ctx,
		`SELECT description FROM sub_goal WHERE id=?`, subGoalID).Scan(&description); err != nil {
		return fmt.Errorf("load released brief: %w", err)
	}
	if !strings.Contains(description, "{{node.") && !strings.Contains(description, "{{goal.") {
		return nil
	}
	resolved := description
	var missing []string
	// Replace every {{node.<key>.output}} with the upstream node's last run
	// result_summary (or an explicit empty marker — never the raw placeholder:
	// the assignee must not receive template syntax).
	resolved = templateNodeRefRe.ReplaceAllStringFunc(description, func(m string) string {
		sub := templateNodeRefRe.FindStringSubmatch(m)
		upstreamKey := sub[1]
		var upstreamSG string
		if err := tx.QueryRowContext(ctx,
			`SELECT sub_goal_id FROM blueprint_node WHERE goal_id=? AND node_key=? AND sub_goal_id != ''`,
			goalID, upstreamKey).Scan(&upstreamSG); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				missing = append(missing, upstreamKey)
				return "(上游节点未执行)"
			}
			return "(引用解析失败)"
		}
		var summary sql.NullString
		if err := tx.QueryRowContext(ctx,
			`SELECT result_summary FROM run WHERE sub_goal_id=? AND role='subgoal' AND result_summary != '' ORDER BY finished_at DESC LIMIT 1`,
			upstreamSG).Scan(&summary); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "(引用解析失败)"
		}
		if !summary.Valid || summary.String == "" {
			return "(上游节点无产出摘要)"
		}
		return summary.String
	})
	if _, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET description=? WHERE id=?`, resolved, subGoalID); err != nil {
		return fmt.Errorf("write resolved brief: %w", err)
	}
	snapshot, _ := json.Marshal(map[string]string{"brief": resolved})
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`,
		string(snapshot), subGoalID); err != nil {
		return fmt.Errorf("snapshot resolved brief: %w", err)
	}
	if len(missing) > 0 {
		logging.Warnf("blueprint: goal %s brief for %s had unresolvable refs: %v", goalID, subGoalID, missing)
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
