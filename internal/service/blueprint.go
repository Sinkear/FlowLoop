package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/logging"
	"github.com/Sinkear/flowloop/internal/store"
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
	ID        string      `json:"id"`
	Type      string      `json:"type"` // task|condition|router|gate|spawn|end
	Title     string      `json:"title"`
	Task      *SpecTask   `json:"task,omitempty"`
	Condition *SpecCond   `json:"condition,omitempty"`
	Router    *SpecRouter `json:"router,omitempty"` // Phase B
	Gate      *SpecGate   `json:"gate,omitempty"`   // Phase B
	Spawn     *SpecSpawn  `json:"spawn,omitempty"`  // Phase B
	End       *SpecEnd    `json:"end,omitempty"`
}

type SpecTask struct {
	Assignee string      `json:"assignee"` // "agent:xx" | "squad:xx"
	Brief    string      `json:"brief"`    // template: {{goal.<field>}} / {{node.<id>.output}}
	Verify   *SpecVerify `json:"verify,omitempty"`
}

type SpecVerify struct {
	Mode     string `json:"mode,omitempty"`     // domain_default|machine|agent
	Verifier string `json:"verifier,omitempty"` // agent id when mode=agent
}

type SpecCond struct {
	Expr  string     `json:"expr"` // restricted-operator expression over goal fields
	Cases []SpecCase `json:"cases"`
	Else  string     `json:"else,omitempty"` // default "else"
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
	Prompt  string           `json:"prompt"`
	Actions []SpecGateAction `json:"actions"`
	Timeout *SpecGateTimeout `json:"timeout,omitempty"`
	// Auto is the runtime escape hatch (BUG-4 §C): when the sealed upstream
	// evidence matches the rule, the platform resolves the gate itself with
	// Action — the "minor/低风险 → auto 直通" branch the gate-only vocabulary
	// could not express. The rule is a single runtime test over ONE upstream
	// node's immutable output (node.<id>.output <op> "literal"), evaluated by
	// the gate sweeper; a human still decides whenever the rule misses.
	Auto *SpecGateAuto `json:"auto,omitempty"`
}

// SpecGateAuto: When is a runtime node test (see ParseNodeTest); Action is
// one of the gate's declared action ids. Deterministic by construction —
// the decision is a pure function of an already-sealed output.
type SpecGateAuto struct {
	When   string `json:"when"`
	Action string `json:"action"`
}

// ActionIDs returns the gate's registered action ids in declaration order —
// the port vocabulary both for V1 validation and for the resolve-time
// "action %q is not one gate %q declares" rejection.
func (g *SpecGate) ActionIDs() []string {
	ids := make([]string, 0, len(g.Actions))
	for _, a := range g.Actions {
		ids = append(ids, a.ID)
	}
	return ids
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
	ID   string `json:"id"`
	Name string `json:"name"`
	// SquadID is the owning squad (Phase B, D1). '' is the frozen legacy
	// bucket: old global workflows keep running for already-pinned goals,
	// but new tasks cannot select them.
	SquadID       string `json:"squad_id"`
	Description   string `json:"description"`
	CreatedByType string `json:"created_by_type"`
	CreatedByID   string `json:"created_by_id"`
	CreatedAt     string `json:"created_at"`
	// LatestVersionID is the newest PUBLISHED version's id — '' while the
	// blueprint has no published version. The web UI keys off this both for
	// the published/draft badge and the new-goal "bind blueprint" dropdown
	// (only published versions are pinnable — Create refuses drafts).
	LatestVersionID string `json:"latest_version_id"`
	// VersionCount / LatestVersion / LatestVersionStatus describe the version
	// history at a glance so the list page can be honest about state: a
	// blueprint with published v1 and a newer draft v3 is NOT simply
	// "published" — it has a pinnable version AND unfinished work.
	// VersionCount is 0 while the blueprint has no versions yet.
	VersionCount        int    `json:"version_count"`
	LatestVersion       int    `json:"latest_version"`
	LatestVersionStatus string `json:"latest_version_status"`
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
	ID             string `json:"id"`
	GoalID         string `json:"goal_id"`
	NodeKey        string `json:"node_key"`
	ParentKey      string `json:"parent_key"`
	NodeType       string `json:"node_type"`
	SubGoalID      string `json:"sub_goal_id"`
	State          string `json:"state"`
	ResolvedConfig string `json:"resolved_config"`
	CreatedAt      string `json:"created_at"`
}

// ── validation ──

var specNodeIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var specAssigneeRe = regexp.MustCompile(`^(agent|squad|capability):[a-z0-9_.-]+$`)
var capabilitySlugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

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

// B-3 (2026-11-18): all six node types — task, condition, router, gate,
// spawn, end — are publishable. The publish gate is the STATIC validator
// (V1–V6) alone; the phase set restriction is retired.

// declaredPorts returns the outbound port ids a node type declares (V1/V3).
// task: done|failed (implicit; failed is optional — a leaf task may only
// carry done); condition: cases[].id + else; router: classes[].id + fallback;
// gate: actions[].id, plus "timeout" only when a timeout action is declared
// (an explicit action is the port the sweeper reuses; with no action the
// deadline PARKS the gate and there is no timeout edge to route).
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
		if n.Gate.Timeout != nil && n.Gate.Timeout.Action != "" {
			ports = append(ports, "timeout")
		}
		return ports
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
		if n.Gate.Timeout != nil && n.Gate.Timeout.Action != "" {
			ports = append(ports, "timeout") // V3: an explicit timeout action must go somewhere
		}
		return ports
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
				add("node %q: task.assignee must be agent:<id>, squad:<id> or capability:<role-slug>", n.ID)
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
		case "spawn":
			if n.Spawn == nil {
				add("node %q: spawn body is required", n.ID)
				break
			}
			if !specAssigneeRe.MatchString(n.Spawn.Assignee) {
				add("node %q: spawn.assignee must be agent:<id>, squad:<id> or capability:<role-slug>", n.ID)
			}
			if strings.TrimSpace(n.Spawn.Brief) == "" {
				add("node %q: spawn.brief is required", n.ID)
			}
			if n.Spawn.ChildType != "" && n.Spawn.ChildType != "task" {
				add("node %q: spawn.child_type must be \"task\" (the only compiled child form)", n.ID)
			}
			if n.Spawn.MaxChildren < 0 || n.Spawn.MaxChildren > maxSpawnChildren {
				add("node %q: spawn.max_children must be 1–%d (0 = default %d)", n.ID, maxSpawnChildren, defaultSpawnChildren)
			}
		case "router":
			if n.Router == nil {
				add("node %q: router body is required", n.ID)
				break
			}
			// §4.4: classes ≥2 (a single class is a linear edge, not a
			// decision), ids are ports (so they match the port pattern and
			// are unique), and the mandatory fallback names a declared
			// class — the port every miss resolves to.
			if len(n.Router.Classes) < 2 {
				add("node %q: router requires ≥2 classes (a single class is not a decision)", n.ID)
			}
			seenClass := map[string]bool{}
			for _, c := range n.Router.Classes {
				if !specNodeIDRe.MatchString(c.ID) {
					add("node %q: class id %q must match the port pattern", n.ID, c.ID)
				}
				if seenClass[c.ID] {
					add("node %q: duplicate class id %q", n.ID, c.ID)
				}
				seenClass[c.ID] = true
				if strings.TrimSpace(c.Title) == "" {
					add("node %q: class %q: title is required", n.ID, c.ID)
				}
			}
			if n.Router.Fallback == "" {
				add("node %q: router.fallback is required — every parse miss or unregistered choice lands there", n.ID)
			} else if !seenClass[n.Router.Fallback] {
				add("node %q: router.fallback %q must be a declared class id", n.ID, n.Router.Fallback)
			}
		case "gate":
			if n.Gate == nil {
				add("node %q: gate body is required", n.ID)
				break
			}
			// §4.2/§8 Q1: a human checkpoint with a closed action set —
			// actions are ports; the mandatory timeout names an action AND
			// a parseable duration (the sweeper enforces it at runtime).
			if strings.TrimSpace(n.Gate.Prompt) == "" {
				add("node %q: gate.prompt is required", n.ID)
			}
			if len(n.Gate.Actions) == 0 {
				add("node %q: gate requires ≥1 action", n.ID)
			}
			seenAction := map[string]bool{}
			for _, a := range n.Gate.Actions {
				if !specNodeIDRe.MatchString(a.ID) {
					add("node %q: action id %q must match the port pattern", n.ID, a.ID)
				}
				if seenAction[a.ID] {
					add("node %q: duplicate action id %q", n.ID, a.ID)
				}
				seenAction[a.ID] = true
				if strings.TrimSpace(a.Label) == "" {
					add("node %q: action %q: label is required", n.ID, a.ID)
				}
			}
			// Timeout policy is OPTIONAL. No timeout object = the gate holds
			// forever (parked for a human). A timeout WITHOUT an action is the
			// explicit park form: the deadline marks the gate for attention and
			// the sweeper records `parked_at`, but it never decides for the
			// human. An explicit action must be a declared action id — the
			// unattended path can only be one the author named.
			if n.Gate.Timeout != nil {
				if n.Gate.Timeout.After == "" {
					add("node %q: gate.timeout.after is required when gate.timeout is set (e.g. \"24h\")", n.ID)
				} else if _, err := time.ParseDuration(n.Gate.Timeout.After); err != nil {
					add("node %q: gate.timeout.after %q is not a parseable duration: %v", n.ID, n.Gate.Timeout.After, err)
				}
				if n.Gate.Timeout.Action != "" && !seenAction[n.Gate.Timeout.Action] {
					add("node %q: gate.timeout.action %q must be a declared action id", n.ID, n.Gate.Timeout.Action)
				}
			}
			// Auto rule (runtime branch, BUG-4 §C): ONE upstream-output test
			// plus an action it may resolve with. Both halves are structural:
			// the action must be a declared port, and the evidence source must
			// be a direct upstream node (checked with the edge set below).
			if n.Gate.Auto != nil {
				if strings.TrimSpace(n.Gate.Auto.When) == "" {
					add("node %q: gate.auto.when is required when gate.auto is set", n.ID)
				} else if _, _, err := ParseNodeTest(n.Gate.Auto.When); err != nil {
					add("node %q: gate.auto.when is not a runtime node test: %v", n.ID, err)
				}
				if n.Gate.Auto.Action == "" || !seenAction[n.Gate.Auto.Action] {
					add("node %q: gate.auto.action %q must be a declared action id", n.ID, n.Gate.Auto.Action)
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

	// Auto rule's evidence source must be a DIRECT upstream node of the gate:
	// a rule over a node that cannot reach the gate would either never fire or
	// fire on stale evidence from a different branch.
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != "gate" || n.Gate == nil || n.Gate.Auto == nil {
			continue
		}
		key, _, err := ParseNodeTest(n.Gate.Auto.When)
		if err != nil {
			continue // already reported above
		}
		upstream := false
		for _, e := range g.Edges {
			if e.Target == n.ID && e.Source == key {
				upstream = true
				break
			}
		}
		if !upstream {
			add("node %q: gate.auto.when references %q, which is not a direct upstream node of the gate", n.ID, key)
		}
	}

	// B-3 consistency: a gate's "timeout" port is the structural declaration
	// of the unattended path, and timeout.action is its behavioral encoding —
	// the two must not drift. Pin them: every timeout-port edge must land on
	// a node the timeout.action port also reaches (the timeout resolution
	// prunes with the ACTION port, so a divergent timeout edge would point
	// at a branch that never runs).
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != "gate" || n.Gate == nil || n.Gate.Timeout == nil || n.Gate.Timeout.Action == "" {
			continue
		}
		actionTargets := map[string]bool{}
		for _, e := range g.Edges {
			if e.Source == n.ID && e.SourceHandle == n.Gate.Timeout.Action && byID[e.Target] != nil {
				actionTargets[e.Target] = true
			}
		}
		for _, e := range g.Edges {
			if e.Source == n.ID && e.SourceHandle == "timeout" && byID[e.Target] != nil && !actionTargets[e.Target] {
				add("node %q: gate.timeout port must lead where timeout.action (%q) leads — %q diverges", n.ID, n.Gate.Timeout.Action, e.Target)
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
			// A root must start executable work. Spawn is allowed as a root
			// since Phase B: the planner runs first and fans the team out —
			// that IS an upstream context (the goal itself feeds it).
			if t := byID[r].Type; t != "task" && t != "spawn" {
				add("root node %q must be a task or spawn (got %s — a %s needs an upstream context)", r, t, t)
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
	// B-3: a gate's prompt is shown to the HUMAN deciding, so it may template
	// upstream evidence too; router class descriptions are platform contract
	// text (no {{…}} — the class list is rendered by the platform, and the
	// classification run gets its evidence from the goal, not from here).
	for i := range g.Nodes {
		n := &g.Nodes[i]
		var text string
		switch {
		case n.Task != nil:
			text = n.Task.Brief
		case n.Gate != nil:
			text = n.Gate.Prompt
		default:
			continue
		}
		if text == "" {
			continue
		}
		for _, m := range templateGoalRefRe.FindAllStringSubmatch(text, -1) {
			if !goalTemplateFields[m[1]] {
				add("node %q: brief refers to unknown goal field %q", n.ID, m[1])
			}
		}
		for _, m := range templateNodeRefRe.FindAllStringSubmatch(text, -1) {
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

// runtimeNodeTestRe is the runtime (beat-3) test vocabulary: the same
// operator set as a condition case, but the left side is ONE upstream node's
// sealed output instead of a goal field. It is deliberately single-clause:
// a decision that depends on several outputs should be a condition node with
// declared cases, not an implicit AND hidden inside a gate rule.
var runtimeNodeTestRe = regexp.MustCompile(`^node\.([a-z][a-z0-9_]{0,63})\.output\s+(==|!=|contains|starts_with|ends_with|exists|empty)\s*(.*)$`)

// ParseNodeTest parses "node.<key>.output <op> \"literal\"" and returns the
// node key plus the reusable condTest clause (Field holds the node key).
func ParseNodeTest(test string) (string, condTest, error) {
	t := strings.TrimSpace(test)
	m := runtimeNodeTestRe.FindStringSubmatch(t)
	if m == nil {
		return "", condTest{}, fmt.Errorf("must look like: node.<id>.output <op> <literal> (got %q)", t)
	}
	nodeKey, op, rest := m[1], m[2], strings.TrimSpace(m[3])
	ct := condTest{Field: nodeKey, Op: op}
	switch op {
	case "exists", "empty":
		if rest != "" {
			return "", condTest{}, fmt.Errorf("%s takes no literal (got %q)", op, rest)
		}
	default:
		if len(rest) < 2 || (rest[0] != '"' && rest[0] != '\'') || rest[len(rest)-1] != rest[0] {
			return "", condTest{}, fmt.Errorf("%s needs a quoted literal (got %q)", op, rest)
		}
		ct.Literal = rest[1 : len(rest)-1]
	}
	return nodeKey, ct, nil
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
		return nil, NewValidationError("workflow content is not valid JSON: " + err.Error())
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

// Spawn fan-out bounds (§11.4): max_children is validated 1–8 (0 = the
// default); the bound is enforced again at materialization time — the
// planner's JSON is untrusted output, the compile-time cap is the law.
const (
	maxSpawnChildren     = 8
	defaultSpawnChildren = 4
)

// reviewerRole is the reserved squad_member role that never executes work
// (the REVIEWER-ONLY RULE). Capability matching must never hand it an
// assignment — it is excluded from resolution and rejected as an explicit
// capability slug.
const reviewerRole = "reviewer"

// validateContent is the full publish gate: parse, V1–V5, V4 assignee
// resolution against the registry. B-3: all six node types publishable.
func (s *BlueprintService) validateContent(ctx context.Context, content string) (*SpecGraph, error) {
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return nil, NewValidationError("workflow content is not valid JSON: " + err.Error())
	}
	if len(g.Nodes) > maxSpecNodes {
		return nil, NewValidationError(fmt.Sprintf("workflow has %d nodes (max %d)", len(g.Nodes), maxSpecNodes))
	}
	if problems := ValidateSpec(&g); len(problems) > 0 {
		return nil, NewValidationError("workflow validation failed:\n  - " + strings.Join(problems, "\n  - "))
	}
	// V4: every task/spawn assignee resolves — with the Phase B capability
	// caveat: capability:<slug> names a ROLE, and roles live in
	// squad_member.role, so whether it resolves can only be answered
	// against the goal's own squad. Publish checks the slug shape only;
	// compile resolves it fail-closed (§11.3: zero matches = the goal
	// stays in backlog with a diagnostic, never a silently dropped node).
	for i := range g.Nodes {
		n := &g.Nodes[i]
		var assignee string
		switch {
		case n.Type == "task" && n.Task != nil:
			assignee = n.Task.Assignee
		case n.Type == "spawn" && n.Spawn != nil:
			assignee = n.Spawn.Assignee
		default:
			continue
		}
		kind, id, ok := strings.Cut(assignee, ":")
		if !ok {
			continue // V1 already reported the malformed shape
		}
		if kind == "capability" {
			if !capabilitySlugRe.MatchString(id) {
				return nil, NewValidationError(fmt.Sprintf("node %q: capability slug %q must match ^[a-z][a-z0-9_-]*$ and must not be %q (the reviewer role never executes)", n.ID, id, reviewerRole))
			}
			continue // resolution is goal-scoped (§11.3) — deferred to compile
		}
		var count int
		table := "agent"
		if kind == "squad" {
			table = "squad"
		}
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+table+` WHERE id=?`, id).Scan(&count); err != nil {
			return nil, fmt.Errorf("resolve assignee %s: %w", assignee, err)
		}
		if count == 0 {
			return nil, NewValidationError(fmt.Sprintf("node %q: assignee %s does not resolve to a registered %s (expected %s id, e.g. %s:<32-hex-id>)", n.ID, assignee, kind, kind, kind))
		}
		if n.Type == "task" && n.Task.Verify != nil && n.Task.Verify.Mode == "agent" && n.Task.Verify.Verifier != "" {
			if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, n.Task.Verify.Verifier, "verify.verifier agent"); err != nil {
				return nil, err
			}
		}
	}
	return &g, nil
}

// validateSpecAssigneeOwnership enforces D1 for squad-owned workflows: a
// workflow may assign work to its own squad (squad:<owner>) or to individual
// agents / role words, but never to a different squad. Legacy unowned
// workflows keep their historical semantics (ownerSquadID == "").
func validateSpecAssigneeOwnership(g *SpecGraph, ownerSquadID string) error {
	if ownerSquadID == "" {
		return nil
	}
	for _, n := range g.Nodes {
		var assignee string
		switch n.Type {
		case "task":
			if n.Task != nil {
				assignee = n.Task.Assignee
			}
		case "spawn":
			if n.Spawn != nil {
				assignee = n.Spawn.Assignee
			}
		}
		if !strings.HasPrefix(assignee, "squad:") {
			continue
		}
		id := strings.TrimPrefix(assignee, "squad:")
		if id != ownerSquadID {
			return NewValidationError(fmt.Sprintf("node %q: assignee %q names another squad — a workflow can only assign work to its owning squad (D1); use agent:<id> or capability:<role> instead", n.ID, assignee))
		}
	}
	return nil
}

// Create registers a legacy (unowned) blueprint with its draft v1. Kept for
// tests/backfill; new product paths must use CreateForSquad so every workflow
// has an owning squad (D1).
func (s *BlueprintService) Create(ctx context.Context, name, description, content, createdByType, createdByID string) (*Blueprint, error) {
	return s.CreateForSquad(ctx, "", name, description, content, createdByType, createdByID)
}

// CreateForSquad registers a workflow owned by squadID with its draft v1.
func (s *BlueprintService) CreateForSquad(ctx context.Context, squadID, name, description, content, createdByType, createdByID string) (*Blueprint, error) {
	if strings.TrimSpace(name) == "" {
		return nil, NewFieldRequiredError("name")
	}
	if squadID != "" {
		if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, squadID, "squad"); err != nil {
			return nil, err
		}
	}
	g, err := s.validateContent(ctx, content)
	if err != nil {
		return nil, err
	}
	if err := validateSpecAssigneeOwnership(g, squadID); err != nil {
		return nil, err
	}
	if description == "" {
		description = g.Description
	}
	bp := Blueprint{
		ID:            newID(),
		Name:          name,
		SquadID:       squadID,
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
		`INSERT INTO blueprint (id,name,squad_id,description,created_by_type,created_by_id,created_at) VALUES (?,?,?,?,?,?,?)`,
		bp.ID, bp.Name, bp.SquadID, bp.Description, bp.CreatedByType, bp.CreatedByID, bp.CreatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			if squadID == "" {
				return nil, NewConflictError(fmt.Sprintf("legacy workflow name %q already exists", name))
			}
			return nil, NewConflictError(fmt.Sprintf("workflow name %q already exists in this squad", name))
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
	g, err := s.validateContent(ctx, content)
	if err != nil {
		return nil, err
	}
	var ownerSquadID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT squad_id FROM blueprint WHERE id=?`, v.BlueprintID).Scan(&ownerSquadID); err != nil {
		return nil, fmt.Errorf("load workflow owner: %w", err)
	}
	if err := validateSpecAssigneeOwnership(g, ownerSquadID); err != nil {
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
	g, err := s.validateContent(ctx, v.Content)
	if err != nil {
		return nil, err
	}
	var ownerSquadID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT squad_id FROM blueprint WHERE id=?`, v.BlueprintID).Scan(&ownerSquadID); err != nil {
		return nil, fmt.Errorf("load workflow owner: %w", err)
	}
	if err := validateSpecAssigneeOwnership(g, ownerSquadID); err != nil {
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

// DeleteVersion removes a DRAFT version — the "I opened a junk draft and want
// it gone" path. Published versions are the blueprint's authority and history,
// so they stay; a draft is refused while anything still points at it (a goal
// pin — including a terminal goal's historical pin — or a proposal's base or
// applied version). The delete is conditional on status='draft' inside the
// transaction, so a concurrent publish cannot turn a checked draft into a
// deleted authority.
func (s *BlueprintService) DeleteVersion(ctx context.Context, versionID string) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete version: %w", err)
	}
	defer tx.Rollback()
	var version int
	var status, blueprintID string
	err = tx.QueryRowContext(ctx,
		`SELECT version,status,blueprint_id FROM blueprint_version WHERE id=?`, versionID).
		Scan(&version, &status, &blueprintID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete version: load: %w", err)
	}
	if status != "draft" {
		return NewConflictError(fmt.Sprintf("v%d is %s — only draft versions can be deleted (published versions are the blueprint's authority and history; open a new draft instead)", version, status))
	}
	var pinned, referenced int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE blueprint_version_id=?`, versionID).Scan(&pinned); err != nil {
		return fmt.Errorf("delete version: count pinned goals: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_proposal WHERE base_version_id=? OR applied_version_id=?`,
		versionID, versionID).Scan(&referenced); err != nil {
		return fmt.Errorf("delete version: count proposals: %w", err)
	}
	if pinned > 0 || referenced > 0 {
		return NewConflictError(fmt.Sprintf("v%d is still referenced (%d goal(s) pinned, %d proposal(s)) — resolve those before deleting it", version, pinned, referenced))
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM blueprint_version WHERE id=? AND status='draft'`, versionID)
	if err != nil {
		return fmt.Errorf("delete version: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NewConflictError(fmt.Sprintf("v%d was published while being deleted — only drafts can be deleted", version))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete version: commit: %w", err)
	}
	logging.Infof("blueprint: deleted draft v%d of %s", version, blueprintID)
	return nil
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

// blueprintSelect is the one projection every blueprint read path uses, so the
// list and the detail page can never disagree about a blueprint's state.
// Columns: id,name,description,created_by_type,created_by_id,created_at,
// latest published id, version count, newest version number, newest status.
const blueprintSelect = `SELECT b.id,b.name,b.squad_id,b.description,b.created_by_type,b.created_by_id,b.created_at,
	COALESCE((SELECT bv.id FROM blueprint_version bv WHERE bv.blueprint_id=b.id AND bv.status='published' ORDER BY bv.version DESC LIMIT 1), ''),
	(SELECT COUNT(*) FROM blueprint_version bv WHERE bv.blueprint_id=b.id),
	COALESCE((SELECT bv.version FROM blueprint_version bv WHERE bv.blueprint_id=b.id ORDER BY bv.version DESC LIMIT 1), 0),
	COALESCE((SELECT bv.status FROM blueprint_version bv WHERE bv.blueprint_id=b.id ORDER BY bv.version DESC LIMIT 1), '')
	FROM blueprint b`

// scanBlueprint keeps the column order in blueprintSelect and this call in sync.
func scanBlueprint(row interface{ Scan(dest ...any) error }, b *Blueprint) error {
	return row.Scan(
		&b.ID, &b.Name, &b.SquadID, &b.Description, &b.CreatedByType, &b.CreatedByID, &b.CreatedAt,
		&b.LatestVersionID, &b.VersionCount, &b.LatestVersion, &b.LatestVersionStatus,
	)
}

// List returns all blueprints with their version/发布 state summary.
func (s *BlueprintService) List(ctx context.Context) ([]Blueprint, error) {
	return s.ListBySquad(ctx, "")
}

// ListBySquad filters to one squad's workflows when squadID is non-empty.
// Empty means the global search view (legacy + every squad).
func (s *BlueprintService) ListBySquad(ctx context.Context, squadID string) ([]Blueprint, error) {
	q := blueprintSelect
	args := []any{}
	if squadID != "" {
		q += ` WHERE b.squad_id=?`
		args = append(args, squadID)
	}
	rows, err := s.st.DB().QueryContext(ctx, q+` ORDER BY b.created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list blueprints: %w", err)
	}
	defer rows.Close()
	var out []Blueprint
	for rows.Next() {
		var b Blueprint
		if err := scanBlueprint(rows, &b); err != nil {
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
	err := scanBlueprint(s.st.DB().QueryRowContext(ctx, blueprintSelect+` WHERE b.id=?`, id), &b)
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

// Delete removes a blueprint with its versions and proposals. It refuses
// while a non-terminal goal still pins a version of the blueprint (an active
// blueprint-driven goal must not lose its authority). Terminal goals keep
// their historical pin — those rows are history.
func (s *BlueprintService) Delete(ctx context.Context, id string) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete blueprint: %w", err)
	}
	defer tx.Rollback()
	// Proposals first: their base_version_id references the version rows that
	// are about to disappear. Deleting them explicitly avoids depending on
	// SQLite's sibling-cascade ordering (blueprint cascades versions AND
	// proposals; the order between the two is not specified).
	if _, err := tx.ExecContext(ctx, `DELETE FROM blueprint_proposal WHERE blueprint_id=?`, id); err != nil {
		return fmt.Errorf("delete blueprint proposals: %w", err)
	}
	// Count pins INSIDE the transaction, after the write lock is taken: a goal
	// pinned between a pre-flight count and the delete would otherwise leave a
	// dangling blueprint_version_id (goal has no FK on that column). With the
	// lock held, a concurrent Create either already committed (we see it and
	// refuse) or waits until this transaction settles.
	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal g JOIN blueprint_version v ON g.blueprint_version_id=v.id
		 WHERE v.blueprint_id=? AND g.status IN ('backlog','active','review')`, id).Scan(&active); err != nil {
		return fmt.Errorf("count pinned goals: %w", err)
	}
	if active > 0 {
		return NewConflictError(fmt.Sprintf("workflow is pinned by %d non-terminal goal(s) — resolve them first", active))
	}
	// A deleted workflow must not leave a dangling squad default. This is a
	// plain column (no FK) so the explicit clear is required.
	if _, err := tx.ExecContext(ctx,
		`UPDATE squad SET default_workflow_id='' WHERE default_workflow_id=?`, id); err != nil {
		return fmt.Errorf("clear squad default workflow: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blueprint_version WHERE blueprint_id=?`, id); err != nil {
		return fmt.Errorf("delete blueprint versions: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM blueprint WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete blueprint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete blueprint: %w", err)
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
	return specGraphToWorkflow(v.ID, g), nil
}

// specGraphToWorkflow assembles a spec graph into the WorkflowGraph shape the
// frontend renderer already understands (§4.12 one renderer, two flavors).
// Shared by stored-version rendering AND proposal diff preview (Phase C).
func specGraphToWorkflow(scopeID string, g *SpecGraph) *WorkflowGraph {
	wf := &WorkflowGraph{
		GoalID:      "blueprint:" + scopeID,
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
	return wf
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
		logging.Warnf("blueprint: goal %s reopened — %d spec rows exist, manual follow-up mode (new work is NOT bound to the spec)", goalID, existing)
		// Make the mode visible on the timeline instead of only in the log:
		// after a reopen the spec stops being the authority for new work
		// (BUG-4 缺口 G).
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO activity_log (id, goal_id, actor_type, actor_id, action, detail, created_at)
			 VALUES (?,?, 'system', '', 'blueprint_manual_followup', ?, ?)`,
			newID(), goalID, `{"spec_nodes":`+fmt.Sprint(existing)+`}`, now()); err != nil {
			return fmt.Errorf("record manual follow-up: %w", err)
		}
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
		return NewConflictError("pinned workflow version content_hash mismatch — refusing to compile a drifted spec")
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return NewValidationError("pinned workflow content is not valid JSON: " + err.Error())
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
	// The goal's own squad — the vocabulary a capability:<slug> resolves
	// against (§11.3) and the briefing fallback for agent: nodes (§11.5).
	goalSquadID := ""
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(assignee_id,'') FROM goal WHERE id=? AND assignee_type='squad'`,
		goalID).Scan(&goalSquadID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load goal squad: %w", err)
	}
	// Phase B: the workflow's owning squad is the authoritative vocabulary /
	// briefing scope. Legacy workflows (squad_id='') keep the old
	// goal-squad semantics so historical pins compile exactly as before.
	bpSquadID := ""
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(b.squad_id,'') FROM blueprint_version bv JOIN blueprint b ON b.id=bv.blueprint_id WHERE bv.id=?`, versionID).
		Scan(&bpSquadID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load workflow owner: %w", err)
	}
	ownerSquadID := goalSquadID
	if bpSquadID != "" {
		ownerSquadID = bpSquadID
	}
	// B-3: holder nodes (router/gate) compile to the goal's owner agent —
	// a squad goal routes decisions to its leader (resolveLeaderTx
	// semantics, replayed here because the goal-level helper lives on the
	// run service and the compiler is already inside its own transaction).
	goalOwnerID := ""
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(assignee_id,'') FROM goal WHERE id=? AND assignee_type='agent'`,
		goalID).Scan(&goalOwnerID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load goal owner: %w", err)
	}
	if goalOwnerID == "" {
		if ownerSquadID != "" {
			if err := tx.QueryRowContext(ctx,
				`SELECT leader_id FROM squad WHERE id=?`, ownerSquadID).Scan(&goalOwnerID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return NewValidationError("the goal's squad no longer exists — cannot resolve the decision holder")
				}
				return fmt.Errorf("load goal squad leader: %w", err)
			}
		} else {
			return NewValidationError("the goal has no owner agent — router/gate nodes need a decision holder")
		}
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
			if isExecutableNode(byID[id].Type) {
				continue // stop closure expansion at executables — their deps cover the rest
			}
			queue = append(queue, rev[id]...)
		}
		var out []string
		for id := range seen {
			if isExecutableNode(byID[id].Type) {
				out = append(out, id)
			}
		}
		sort.Strings(out)
		ancestorMemo[start] = out
		return out
	}

	ts := now()
	// Register every spec node's ledger row + create the executable
	// sub-goals (task|spawn both compile to a sub_goal). Two passes so
	// depends_on references exist before any ungated enqueue.
	//
	// CRITICAL: depends_on stores SUB_GOAL ids (the executor's gate looks
	// up sub_goal rows by id), not spec node keys — so every live
	// executable node's sub_goal id is PRE-ALLOCATED here, and
	// ancestorTasks' keys are mapped through sgByKey at insert time. (An
	// early version stored raw keys: the gate treated them as missing
	// dependencies and cascaded the dependents to failed on first wake.)
	//
	// runEvs collects the run:enqueued events for the beat-1 runs created
	// below: insertSubGoalTx enqueues the run of an ungated executable and
	// hands its event back to the caller, but the second-pass enqueue coalesces
	// on that same row and returns no event. Without collecting the first-pass
	// event here the run starts silently (BUG-3: compiled runs never reached
	// the event bus).
	var runEvs []events.Event
	sgByKey := map[string]string{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if isExecutableNode(n.Type) && live[n.ID] {
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
		case isExecutableNode(n.Type):
			state = "pending" // running stamped after enqueue below
		}
		if !isExecutableNode(n.Type) {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?)`,
				newID(), goalID, n.ID, n.Type, state, resolved, ts); err != nil {
				return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
			}
			continue
		}
		if !live[n.ID] {
			// Skipped executable: ledger row only, no sub_goal (it must never run).
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?)`,
				newID(), goalID, n.ID, n.Type, state, resolved, ts); err != nil {
				return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
			}
			continue
		}
		var assigneeSpec string
		switch n.Type {
		case "task":
			assigneeSpec = n.Task.Assignee
		case "spawn":
			assigneeSpec = n.Spawn.Assignee
		case "router", "gate":
			// §4.7: the leader is the executor of routing and gate
			// decisions — a holder node has no assignee of its own, it
			// compiles to the GOAL's owner (squad → leader agent).
			assigneeSpec = "agent:" + goalOwnerID
		}
		kind, id, _ := strings.Cut(assigneeSpec, ":")
		if kind != "agent" && kind != "squad" && kind != "capability" {
			return NewValidationError(fmt.Sprintf("node %q: assignee %q not resolvable at compile time", n.ID, assigneeSpec))
		}
		teamSquad := ""
		resolvedAgent := id
		switch kind {
		case "squad":
			// D1: an owned workflow cannot assign work to a different squad.
			// Create/update/publish validate this too; the compile-time guard
			// protects legacy drafts and any path that bypasses those gates.
			if ownerSquadID != "" && id != ownerSquadID {
				return NewValidationError(fmt.Sprintf("node %q: assignee %q names another squad — a workflow can only assign work to its owning squad", n.ID, assigneeSpec))
			}
			// A squad assignee compiles to its LEADER agent — runs are
			// claimed per agent (Claim JOINs the agent table), so a squad
			// id in run.agent_id would queue forever (same semantics as
			// the goal-level resolveLeaderTx). §11.5: the node's runs are
			// briefed with THIS squad's roster/playbook via team_squad_id.
			var leader string
			if err := tx.QueryRowContext(ctx, `SELECT leader_id FROM squad WHERE id=?`, id).Scan(&leader); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return NewValidationError(fmt.Sprintf("node %q: squad %q not found", n.ID, id))
				}
				return fmt.Errorf("resolve squad %s leader: %w", id, err)
			}
			resolvedAgent = leader
			teamSquad = id
		case "capability":
			// §11.3: capability:<role-slug> resolves against the goal's
			// OWN squad vocabulary (squad_member.role, LOWER(TRIM)
			// normalized), reviewers excluded, least-loaded first. V4
			// checked the slug shape at publish; resolution is
			// goal-scoped so it can only happen here — fail-closed: a
			// capability that matches nobody blocks activation with a
			// diagnostic, never a silently dropped node.
			if ownerSquadID == "" {
				return NewValidationError(fmt.Sprintf("node %q: assignee %q requires a squad-assigned goal (or squad-owned workflow) — capability is resolved against the workflow's squad", n.ID, assigneeSpec))
			}
			member, err := resolveCapabilityTx(ctx, tx, ownerSquadID, id)
			if err != nil {
				return err
			}
			resolvedAgent = member
			teamSquad = ownerSquadID
		}
		verifier := ""
		if n.Type == "task" && n.Task.Verify != nil && n.Task.Verify.Mode == "agent" {
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
		brief := ""
		switch n.Type {
		case "task":
			brief = interpolateGoal(n.Task.Brief, goalFields)
		case "spawn":
			// §11.4: the spawn sub_goal is the PLANNER item — the spec
			// brief plus the platform-fixed output contract. The
			// materializer parses the run's result_summary for the fenced
			// JSON child plan; everything outside the contract is the
			// planner's freedom (Phase B's elastic pocket).
			brief = interpolateGoal(n.Spawn.Brief, goalFields) + spawnPlannerInstruction(n, teamSquad)
		case "router":
			// §4.4: the classification item — the class list rendered as
			// the choice prompt, plus the fenced-JSON output contract. Any
			// miss at resolve time lands on the declared fallback; the run
			// itself is free-form reasoning before the fence.
			brief = interpolateGoal(routerClassPrompt(n), goalFields)
		case "gate":
			// §4.2/§8 Q1: the gate sub_goal is a HOLDER — it never gets a
			// run. The brief below is the decision card text the human
			// sees (prompt + actions + timeout), resolved of goal refs;
			// {{node.*}} refs resolve at release (beat 2) when the
			// upstream evidence exists.
			brief = interpolateGoal(gatePrompt(n), goalFields)
		}
		sg := SubGoal{
			ID:          sgByKey[n.ID],
			GoalID:      goalID,
			Title:       n.Title,
			Description: brief,
			AssigneeID:  resolvedAgent,
			VerifierID:  verifier,
			DependsOn:   depIDs,
			Status:      "running",
			TeamSquadID: teamSquad, // §11.5: '' = fall back to the goal's own squad
			CreatedAt:   ts0,
		}
		sgOut, runEv, err := s.insertSubGoalTx(ctx, tx, sg, sg.DependsOn, "agent", "")
		if err != nil {
			return fmt.Errorf("compile node %q: %w", n.ID, err)
		}
		if runEv != nil {
			runEvs = append(runEvs, *runEv)
		}
		sgByKey[n.ID] = sgOut.ID
		nodeResolved, err := marshalResolved(assigneeSpec, resolvedAgent, teamSquad)
		if err != nil {
			return fmt.Errorf("encode resolved config %s: %w", n.ID, err)
		}
		if n.Type == "spawn" {
			// The materializer enforces the cap at verified time (§11.4):
			// the plan JSON is untrusted LLM output, the cap here is law.
			if extra, err := mergeResolved(nodeResolved, map[string]string{
				"max_children": strconv.Itoa(maxSpawnChildrenCap(n)),
			}); err == nil {
				nodeResolved = extra
			}
		}
		if n.Type == "gate" {
			// B-3: snapshot the decision envelope into the ledger — the
			// timeout the sweeper enforces, the actions the decision card
			// renders, and (for an ungated gate) the hold timestamp from
			// which the sweeper counts. A gated gate re-stamps held_since
			// at release (wakeDependentsTx).
			nodeState := "pending"
			extra := map[string]string{}
			if n.Gate.Timeout != nil {
				// timeout_action may be "" — the deadline PARKS the gate
				// (records parked_at) instead of auto-deciding.
				extra["timeout_after"] = n.Gate.Timeout.After
				extra["timeout_action"] = n.Gate.Timeout.Action
			}
			if n.Gate.Auto != nil {
				// The runtime branch: the sweeper evaluates auto_when against
				// the named upstream node's sealed output; a match resolves the
				// gate as auto_action (decided_by=auto). Stamped here so the
				// ledger alone is enough to replay why a gate opened.
				extra["auto_when"] = n.Gate.Auto.When
				extra["auto_action"] = n.Gate.Auto.Action
			}
			if len(depIDs) == 0 {
				nodeState = "running" // held from the start — no upstream to wait for
				extra["held_since"] = ts
			}
			if merged, err := mergeResolved(nodeResolved, extra); err == nil {
				nodeResolved = merged
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,sub_goal_id,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?,?)`,
				newID(), goalID, n.ID, n.Type, sg.ID, nodeState, nodeResolved, ts); err != nil {
				return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,sub_goal_id,state,resolved_config,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			newID(), goalID, n.ID, n.Type, sg.ID, "pending", nodeResolved, ts); err != nil {
			return fmt.Errorf("insert blueprint_node %s: %w", n.ID, err)
		}
	}

	// Ungated executables (no deps) start immediately; gated ones have no
	// run and wait for wakeDependentsTx — beat 2 resolves their node refs
	// at release. GATES are excluded from enqueue in every case: an
	// ungated gate is held from compile (state=running, held_since
	// stamped); a gated gate holds at release (wakeDependentsTx). The
	// human decision — never a run — is the only thing that settles it.
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type == "gate" {
			continue // never enqueued — the decision card + sweeper own it
		}
		if !isExecutableNode(n.Type) || !live[n.ID] || len(ancestorTasks(n.ID)) > 0 {
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
	logging.Infof("blueprint: compiled %d nodes for goal %s (%d executable holders, %d condition/end ledger rows)", len(g.Nodes), goalID, len(sgByKey), len(g.Nodes)-len(sgByKey))
	// Run events fire only AFTER the caller commits (rollback must not
	// launch runs) — compileBlueprintTx returns them for post-commit publish.
	*out = runEvs
	return nil
}

// planMaterialization reports whether a goal is blueprint-pinned and how many
// compiled nodes its ledger holds. pinned=false is infer mode (no spec
// authority — the invariant does not apply).
func planMaterialization(ctx context.Context, q rowQueryer, goalID string) (pinned bool, versionID string, nodes int, err error) {
	if err = q.QueryRowContext(ctx,
		`SELECT COALESCE(blueprint_version_id,'') FROM goal WHERE id=?`, goalID).Scan(&versionID); err != nil {
		return false, "", 0, fmt.Errorf("load goal blueprint pin: %w", err)
	}
	if versionID == "" {
		return false, "", 0, nil
	}
	if err = q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, goalID).Scan(&nodes); err != nil {
		return true, versionID, 0, fmt.Errorf("count blueprint nodes: %w", err)
	}
	return true, versionID, nodes, nil
}

// assertPlanMaterializedTx enforces the P0 invariant: a blueprint-pinned goal
// that enters the execution plane must have a materialized plan. A pinned goal
// with zero blueprint_node rows is the incident's silent-deviation state — the
// owner would receive a "COMPILED plan" contract with nothing compiled and the
// only escape left is consult dispatch. Failing the transition (Create/Activate
// roll back) is strictly better than letting the run deviate silently.
func (s *GoalService) assertPlanMaterializedTx(ctx context.Context, tx *sql.Tx, goalID string) error {
	pinned, versionID, nodes, err := planMaterialization(ctx, tx, goalID)
	if err != nil {
		return err
	}
	if !pinned || nodes > 0 {
		return nil
	}
	logging.Errorf("blueprint: goal %s pins version %s but blueprint_node=0 — refusing to start the execution plane (plan not materialized)", goalID, versionID)
	return NewValidationError(fmt.Sprintf("goal is blueprint-driven but its plan was never materialized (blueprint_node=0 for version %q) — activate the pinned goal so the spec compiles", versionID))
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
// EnsurePlanMaterialized lazily compiles the pinned spec when a run is about
// to start on a goal whose ledger is still empty (reopen guard keeps the call
// idempotent). It returns true when it compiled now. This is the dispatch-time
// backstop for the incident class where a pinned goal reached execution
// without ever passing Activate.
// planMaterializeMu serializes the "blueprint_node=0 → compile" decision so
// two dispatch paths cannot both compile the same goal: the loser's INSERT
// would hit the ledger's UNIQUE(goal_id,node_key) and fail the whole run.
// Only unmaterialized goals take this path, so contention is negligible.
var planMaterializeMu sync.Mutex

func (s *GoalService) EnsurePlanMaterialized(ctx context.Context, goalID string) (bool, error) {
	planMaterializeMu.Lock()
	defer planMaterializeMu.Unlock()
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("ensure plan: begin: %w", err)
	}
	defer tx.Rollback()
	pinned, _, nodes, err := planMaterialization(ctx, tx, goalID)
	if err != nil {
		return false, err
	}
	if !pinned || nodes > 0 {
		return false, nil
	}
	var evs []events.Event
	if err := s.compileBlueprintTx(ctx, tx, goalID, &evs); err != nil {
		return false, fmt.Errorf("ensure plan: compile: %w", err)
	}
	if err := s.assertPlanMaterializedTx(ctx, tx, goalID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("ensure plan: commit: %w", err)
	}
	s.publishCompiledRuns(ctx, evs)
	return true, nil
}

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

// markGateHeldTx is the gate's "release": flip pending → running and stamp
// held_since into resolved_config. That timestamp is the sweeper's clock
// (SweepBlueprintGates reads it against the gate's timeout.after) and the
// guard counts the running gate as open work — the goal cannot finalize
// past an undecided human gate. Idempotent: a gate already held (compile
// stamped held_since, or a re-released ledger) just refreshes nothing.
func markGateHeldTx(ctx context.Context, tx *sql.Tx, subGoalID string) error {
	var cfg string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`, subGoalID).
		Scan(&cfg); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // not a compiled node — no-op
		}
		return fmt.Errorf("load gate ledger %s: %w", subGoalID, err)
	}
	var m map[string]any
	if json.Unmarshal([]byte(cfg), &m) != nil {
		m = map[string]any{}
	}
	if _, held := m["held_since"]; held {
		return nil // already held — do not restart the clock
	}
	merged, _ := mergeResolved(cfg, map[string]string{"held_since": now()})
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET state='running', resolved_config=? WHERE sub_goal_id=? AND state='pending'`,
		merged, subGoalID); err != nil {
		return fmt.Errorf("hold gate node: %w", err)
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
	snapshot := map[string]string{}
	var prev string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`, subGoalID).Scan(&prev); err == nil && prev != "" {
		_ = json.Unmarshal([]byte(prev), &snapshot) // best-effort merge; brief wins
	}
	snapshot["brief"] = resolved
	snapJSON, _ := json.Marshal(snapshot)
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`,
		string(snapJSON), subGoalID); err != nil {
		return fmt.Errorf("snapshot resolved brief: %w", err)
	}
	if len(missing) > 0 {
		logging.Warnf("blueprint: goal %s brief for %s had unresolvable refs: %v", goalID, subGoalID, missing)
	}
	return nil
}

// isBlueprintNodeTypeTx reports whether the sub_goal is a compiled node of
// the given spec type (” / no row = non-pinned goal or a plain task — no).
func isBlueprintNodeTypeTx(ctx context.Context, tx *sql.Tx, subGoalID, nodeType string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_node WHERE sub_goal_id=? AND node_type=?`,
		subGoalID, nodeType).Scan(&n); err != nil {
		return false, fmt.Errorf("check blueprint node type: %w", err)
	}
	return n > 0, nil
}

// spawnPlanItem is one parsed row of the planner's fenced JSON child plan.
type spawnPlanItem struct {
	Title    string `json:"title"`
	Brief    string `json:"brief"`
	Assignee string `json:"assignee"`
}

// parseSpawnPlan extracts the LAST fenced ```json block from the planner's
// result_summary and unmarshals it as the child plan. The planner may write
// freely before the fence; the fence is the machine boundary.
func parseSpawnPlan(summary string) ([]spawnPlanItem, error) {
	re := regexp.MustCompile("(?s)```json\\s*(\\[.*?\\])\\s*```")
	matches := re.FindAllStringSubmatch(summary, -1)
	if len(matches) == 0 {
		return nil, NewValidationError("no ```json fenced block found — the planner must end with the child plan array")
	}
	var plan []spawnPlanItem
	if err := json.Unmarshal([]byte(matches[len(matches)-1][1]), &plan); err != nil {
		return nil, NewValidationError("the fenced json block is not a valid child plan array: " + err.Error())
	}
	return plan, nil
}

// resolveSpawnTx is the spawn verified-path gate (§11.4): a spawn sub_goal
// reaching "done" must carry a parseable child plan. On success the children
// are materialized as real sub_goal rows (depends_on=[spawn], created_by
// 'blueprint', briefed with the spawn's own squad) and the spawn's DIRECT
// successors get the child ids appended to their depends_on — so a
// successor runs only after the whole fan-out verified, reusing the
// executor's existing gate untouched (engine-zero-change rule). On a
// contract violation the spawn follows the bounded retry chain (same
// semantics as the failed path) and promoted=false — the caller must skip
// the verified transition. Idempotent: an already-materialized spawn
// promotes without touching anything (retry after a crash mid-chain).
func (s *GoalService) resolveSpawnTx(ctx context.Context, tx *sql.Tx, sg *SubGoal, rc goalRunContext) ([]events.Event, bool, error) {
	goalID := sg.GoalID
	// Ledger: node key + resolved config (max_children snapshot) + spec edges.
	var nodeKey, resolvedCfg string
	if err := tx.QueryRowContext(ctx,
		`SELECT node_key, COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`,
		sg.ID).Scan(&nodeKey, &resolvedCfg); err != nil {
		return nil, false, fmt.Errorf("load spawn ledger: %w", err)
	}
	maxKids := defaultSpawnChildren
	// mergeResolved stores values as JSON STRINGS (map[string]string), so a
	// typed *int unmarshal silently fails and the cap would stay at the
	// default — accept both string and number forms.
	var capCfg struct {
		MaxChildren any `json:"max_children"`
	}
	if json.Unmarshal([]byte(resolvedCfg), &capCfg) == nil && capCfg.MaxChildren != nil {
		switch v := capCfg.MaxChildren.(type) {
		case string:
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				maxKids = n
			}
		case float64:
			if v > 0 {
				maxKids = int(v)
			}
		}
	}

	// Idempotency: any blueprint-born sub_goal already gated on this spawn
	// means materialization happened (a crash after commit replays here).
	// Provenance: insertSubGoalTx stamps createdByType/ID onto the dispatch
	// comment's author_type/author_id — children are born author_type='system'
	// with author_id = the spawn's sub_goal id (the CHECK on comment.author_type
	// is frozen in existing databases, so no new author_type value may be added).
	rows, err := tx.QueryContext(ctx,
		`SELECT sg.id, sg.depends_on FROM sub_goal sg
		 JOIN comment c ON c.id = sg.dispatch_comment_id
		 WHERE sg.goal_id=? AND c.author_type='system' AND c.author_id=?`, goalID, sg.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load blueprint-born rows: %w", err)
	}
	for rows.Next() {
		var id, depsRaw string
		if err := rows.Scan(&id, &depsRaw); err != nil {
			rows.Close()
			return nil, false, err
		}
		for _, d := range decodeDeps(depsRaw) {
			if d == sg.ID {
				rows.Close()
				logging.Infof("blueprint: spawn %s already materialized — promoting idempotently", sg.ID)
				return nil, true, nil
			}
		}
	}
	rows.Close()

	// The planner's report is the run's result_summary.
	var summary string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(result_summary,'') FROM run WHERE id=?`, rc.RunID).Scan(&summary); err != nil {
		return nil, false, fmt.Errorf("load planner summary: %w", err)
	}
	plan, perr := parseSpawnPlan(summary)
	if perr != nil {
		return s.spawnContractFailedTx(ctx, tx, sg, rc, "拆解契约不满足: "+perr.Error())
	}
	if len(plan) > maxKids {
		return s.spawnContractFailedTx(ctx, tx, sg, rc, fmt.Sprintf("拆解契约不满足: 子项 %d 超过上限 %d", len(plan), maxKids))
	}
	// Validate members + materialize children.
	evs := []events.Event{}
	childIDs := make([]string, 0, len(plan))
	childTitles := make([]string, 0, len(plan))
	for i, item := range plan {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Brief) == "" {
			return s.spawnContractFailedTx(ctx, tx, sg, rc, fmt.Sprintf("拆解契约不满足: 子项 %d 缺 title 或 brief", i+1))
		}
		assignee := strings.TrimSpace(item.Assignee)
		if assignee == "" {
			return s.spawnContractFailedTx(ctx, tx, sg, rc, fmt.Sprintf("拆解契约不满足: 子项 %q 缺 assignee", item.Title))
		}
		if sg.TeamSquadID != "" {
			var ok int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM squad_member WHERE squad_id=? AND member_type='agent' AND member_id=?`,
				sg.TeamSquadID, assignee).Scan(&ok); err != nil {
				return nil, false, fmt.Errorf("validate child assignee: %w", err)
			}
			if ok == 0 {
				return s.spawnContractFailedTx(ctx, tx, sg, rc, fmt.Sprintf("拆解契约不满足: 子项 %q 的 assignee 不在拆解小队名册里（越界派工）", item.Title))
			}
		} else if assignee != sg.AssigneeID {
			return s.spawnContractFailedTx(ctx, tx, sg, rc, "拆解契约不满足: 无小队的拆解只能把子项派给规划者自己")
		}
		child := SubGoal{
			ID:          newID(),
			GoalID:      goalID,
			Title:       strings.TrimSpace(item.Title),
			Description: strings.TrimSpace(item.Brief),
			AssigneeID:  assignee,
			VerifierID:  "",
			DependsOn:   []string{sg.ID},
			Status:      "running",
			TeamSquadID: sg.TeamSquadID, // children inherit the spawn's squad (§11.5)
			CreatedAt:   now(),
		}
		// author_type='system' + author_id=<spawn sub_goal id> = the
		// provenance pair that survives the frozen comment CHECK constraint
		// (no new author_type value can be introduced post-hoc in SQLite).
		sgOut, ev, err := s.insertSubGoalTx(ctx, tx, child, child.DependsOn, "system", sg.ID)
		if err != nil {
			return nil, false, fmt.Errorf("materialize spawn child %q: %w", child.Title, err)
		}
		childIDs = append(childIDs, sgOut.ID)
		childTitles = append(childTitles, child.Title)
		// Ledger the elastic fan-out (§11.4 double bookkeeping): the schema
		// reserved node_key '<spawn_key>#<seq>' + parent_key for exactly this,
		// but no row was ever written — so "which extra work item came from
		// which spawn" was unanswerable. INSERT OR IGNORE keeps a crash replay
		// idempotent.
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO blueprint_node
			   (id, goal_id, node_key, parent_key, node_type, sub_goal_id, state, created_at)
			 VALUES (?,?,?,?, 'task', ?, 'running', ?)`,
			newID(), goalID, fmt.Sprintf("%s#%d", nodeKey, i+1), nodeKey, sgOut.ID, now()); err != nil {
			return nil, false, fmt.Errorf("ledger spawn child %q: %w", child.Title, err)
		}
		if ev != nil {
			evs = append(evs, *ev)
		}
	}
	// Re-gate the spawn's DIRECT successors (spec-node sub_goals whose
	// depends_on contains the spawn) on the children: they wait for the
	// whole fan-out to verify. The children themselves are excluded —
	// siblings stay parallel.
	if len(childIDs) > 0 {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, COALESCE(depends_on,'[]') FROM sub_goal WHERE goal_id=? AND id != ?`, goalID, sg.ID)
		if err != nil {
			return nil, false, fmt.Errorf("load successors: %w", err)
		}
		type succ struct {
			id   string
			deps []string
		}
		var successors []succ
		for rows.Next() {
			var id, depsRaw string
			if err := rows.Scan(&id, &depsRaw); err != nil {
				rows.Close()
				return nil, false, err
			}
			deps := decodeDeps(depsRaw)
			for _, d := range deps {
				if d == sg.ID {
					successors = append(successors, succ{id, deps})
					break
				}
			}
		}
		rows.Close()
		childSet := map[string]bool{}
		for _, c := range childIDs {
			childSet[c] = true
		}
		for _, su := range successors {
			if childSet[su.id] {
				continue
			}
			merged := append([]string(nil), su.deps...)
			for _, c := range childIDs {
				if !containsString(merged, c) {
					merged = append(merged, c)
				}
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE sub_goal SET depends_on=? WHERE id=?`, encodeDeps(merged), su.id); err != nil {
				return nil, false, fmt.Errorf("re-gate successor %s: %w", su.id, err)
			}
		}
	}
	// Snapshot the materialized plan into the ledger (RSI digestibility).
	snap, _ := mergeResolved(resolvedCfg, map[string]string{
		"children": strings.Join(childIDs, ","),
	})
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`, snap, sg.ID); err != nil {
		return nil, false, fmt.Errorf("snapshot spawn children: %w", err)
	}
	// 决策 6-19: the spawn's expansion IS this node's report — a SYSTEM
	// comment in the same transaction as the children, threaded under the
	// dispatch comment that asked for the split. The child titles make the
	// feed self-explanatory (no blueprint graph needed).
	if _, commentEv, err := insertNodeCommentTx(ctx, tx, Comment{
		GoalID:     goalID,
		AuthorType: "system",
		AuthorID:   "",
		ParentID:   subGoalFeedParentTx(ctx, tx, sg.ID),
		RunID:      rc.RunID,
		Content:    spawnNodeFeedContent(sg.Title, childTitles),
	}); err != nil {
		return nil, false, fmt.Errorf("insert spawn feed comment: %w", err)
	} else {
		evs = append(evs, commentEv)
	}
	logging.Infof("blueprint: spawn %s materialized %d children for goal %s", sg.ID, len(childIDs), goalID)
	return evs, true, nil
}

// spawnContractFailedTx runs the bounded retry chain for a spawn whose
// planner output violated the contract — the SAME semantics as the
// failed-path (attempt bump → retry run, or terminal fail + cascade) so
// spawn retries behave exactly like execution retries.
func (s *GoalService) spawnContractFailedTx(ctx context.Context, tx *sql.Tx, sg *SubGoal, rc goalRunContext, reason string) ([]events.Event, bool, error) {
	logging.Warnf("blueprint: spawn %s contract violation — %s", sg.ID, reason)
	if _, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET execution_attempt=execution_attempt+1 WHERE id=? AND status IN ('running','done')`,
		sg.ID); err != nil {
		return nil, false, fmt.Errorf("bump spawn attempt: %w", err)
	}
	var status string
	var attempt int
	if err := tx.QueryRowContext(ctx,
		`SELECT status, execution_attempt FROM sub_goal WHERE id=?`, sg.ID).Scan(&status, &attempt); err != nil {
		return nil, false, fmt.Errorf("reload spawn after contract fail: %w", err)
	}
	if status != "running" && status != "done" {
		return nil, false, nil // raced terminal (cancelled) — nothing to retry
	}
	var evs []events.Event
	// 决策 6-19: a refused planner contract is a SYSTEM event — the platform
	// rejected the plan, the planner did not report a success. Same attempt
	// information as the failed path.
	if _, commentEv, err := insertNodeCommentTx(ctx, tx, Comment{
		GoalID:     sg.GoalID,
		AuthorType: "system",
		AuthorID:   "",
		ParentID:   subGoalFeedParentTx(ctx, tx, sg.ID),
		RunID:      rc.RunID,
		Content:    spawnFailedNodeFeedContent(rc, sg.Title, attempt, attempt < maxExecutionAttempts, reason),
	}); err != nil {
		return nil, false, fmt.Errorf("insert spawn failure comment: %w", err)
	} else {
		evs = append(evs, commentEv)
	}
	if attempt < maxExecutionAttempts {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET status='running' WHERE id=?`, sg.ID); err != nil {
			return nil, false, fmt.Errorf("revive spawn for retry: %w", err)
		}
		evs = append(evs, events.Event{Topic: "sub_goal.retrying", Payload: map[string]any{
			"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "execution_attempt": attempt, "reason": reason,
		}})
		_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, "")
		if err != nil {
			return nil, false, fmt.Errorf("enqueue spawn retry run: %w", err)
		}
		if runEv != nil {
			evs = append(evs, *runEv)
		}
		return evs, false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='failed' WHERE id=? AND status IN ('running','done')`, sg.ID); err != nil {
		return nil, false, fmt.Errorf("fail spawn: %w", err)
	}
	if err := markBlueprintNodeTx(ctx, tx, sg.ID, "failed"); err != nil {
		return nil, false, err
	}
	evs = append(evs, events.Event{Topic: "sub_goal.failed", Payload: map[string]any{
		"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "execution_attempt": attempt, "reason": reason,
	}})
	cascadeEvs, err := s.failGatedDependentsTx(ctx, tx, sg.GoalID, sg.ID)
	if err != nil {
		return nil, false, err
	}
	evs = append(evs, cascadeEvs...)
	return evs, false, nil
}

// ── B-3 runtime: router resolution + gate decision + dead-branch pruning ──

// parseRouterChoice extracts the LAST fenced ```json block from the router
// run's result_summary and reads {"class":"<id>"} from it. Mirrors
// parseSpawnPlan's machine boundary: free reasoning before the fence, the
// fence is the decision. Returns "" when no fence or no class key — the
// caller maps that to the fallback (a router NEVER retries: fallback IS
// its failure semantics, §4.4).
func parseRouterChoice(summary string) string {
	re := regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")
	matches := re.FindAllStringSubmatch(summary, -1)
	if len(matches) == 0 {
		return ""
	}
	var choice struct {
		Class string `json:"class"`
	}
	if json.Unmarshal([]byte(matches[len(matches)-1][1]), &choice) != nil {
		return ""
	}
	return strings.TrimSpace(choice.Class)
}

// pruneDeadBranchesTx cuts the branches a router/gate decision did NOT take.
// From the deciding node, descendants = the full-port downstream closure;
// chosenReachable = the closure following ONLY the chosen port (conditions
// along the way keep their compile-time chosen ports — a dead branch is
// dead at every level). Every descendant not chosen-reachable goes
// cancelled (its sub_goal, if any) and skipped (its blueprint_node, if
// still pending/running) — including end nodes on the dead path: only the
// live path's ends settle. Cascade-safe: failGatedDependentsTx runs after
// the cancellations in the callers, so dependents of cancelled rows die
// with the standard reason instead of hanging on a verified-never dep.
func (s *GoalService) pruneDeadBranchesTx(ctx context.Context, tx *sql.Tx, goalID, versionID, decideKey, chosenPort string) error {
	var content string
	if err := tx.QueryRowContext(ctx,
		`SELECT content FROM blueprint_version WHERE id=?`, versionID).Scan(&content); err != nil {
		return fmt.Errorf("load spec for pruning: %w", err)
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return fmt.Errorf("parse spec for pruning: %w", err)
	}
	byID := map[string]*SpecNode{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	// Condition choices already recorded at compile time (beat 1).
	condChoice := map[string]string{}
	rows, err := tx.QueryContext(ctx,
		`SELECT node_key, COALESCE(resolved_config,'') FROM blueprint_node WHERE goal_id=? AND node_type='condition'`,
		goalID)
	if err != nil {
		return fmt.Errorf("load condition choices: %w", err)
	}
	for rows.Next() {
		var key, cfg string
		if err := rows.Scan(&key, &cfg); err != nil {
			rows.Close()
			return err
		}
		var c struct {
			Chosen string `json:"chosen"`
		}
		if json.Unmarshal([]byte(cfg), &c) == nil && c.Chosen != "" {
			condChoice[key] = c.Chosen
		}
	}
	rows.Close()

	// Port filter: for a condition on its chosen port; for the deciding
	// node, ONLY the chosen port; everything else passes all ports.
	outbound := func(nodeKey, port string) bool {
		if nodeKey == decideKey {
			return port == chosenPort
		}
		if n := byID[nodeKey]; n != nil && n.Type == "condition" {
			return port == condChoice[nodeKey]
		}
		return true
	}
	// Descendants: walk ALL ports (what COULD run downstream).
	descAdj := map[string][]string{}
	// Chosen-reachable: walk filtered ports (what WILL run).
	liveAdj := map[string][]string{}
	for _, e := range g.Edges {
		if byID[e.Source] == nil || byID[e.Target] == nil {
			continue
		}
		descAdj[e.Source] = append(descAdj[e.Source], e.Target)
		if outbound(e.Source, e.SourceHandle) {
			liveAdj[e.Source] = append(liveAdj[e.Source], e.Target)
		}
	}
	descendants := bfsFrom([]string{decideKey}, descAdj)
	chosenReach := bfsFrom([]string{decideKey}, liveAdj)
	delete(descendants, decideKey)
	delete(chosenReach, decideKey)

	dead := map[string]bool{}
	for id := range descendants {
		if !chosenReach[id] {
			dead[id] = true
		}
	}
	if len(dead) == 0 {
		return nil
	}
	// Cancel the dead branches' non-terminal sub_goals (holder included).
	var deadKeys []string
	for id := range dead {
		deadKeys = append(deadKeys, id)
	}
	sort.Strings(deadKeys)
	for _, key := range deadKeys {
		sgKeyRow := tx.QueryRowContext(ctx,
			`SELECT COALESCE(sub_goal_id,'') FROM blueprint_node WHERE goal_id=? AND node_key=?`, goalID, key)
		var sgID string
		if err := sgKeyRow.Scan(&sgID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // no ledger row (never compiled) — nothing to do
			}
			return fmt.Errorf("load dead node %s: %w", key, err)
		}
		if sgID != "" {
			res, err := tx.ExecContext(ctx,
				`UPDATE sub_goal SET status='cancelled' WHERE id=? AND status NOT IN ('verified','cancelled','failed')`,
				sgID)
			if err != nil {
				return fmt.Errorf("cancel dead branch %s: %w", key, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if _, err := tx.ExecContext(ctx,
					`UPDATE run SET status='cancelled' WHERE sub_goal_id=? AND status='queued'`, sgID); err != nil {
					return fmt.Errorf("cancel dead-branch runs %s: %w", key, err)
				}
				logging.Infof("blueprint: dead branch %s cancelled (decision %s → %s)", key, decideKey, chosenPort)
			}
		}
		// Ledger: skipped beats settled for the graph's completion math —
		// settled/skipped/failed are all terminal for the guard.
		if _, err := tx.ExecContext(ctx,
			`UPDATE blueprint_node SET state='skipped', resolved_config=COALESCE(resolved_config,'') WHERE goal_id=? AND node_key=? AND state IN ('pending','running')`,
			goalID, key); err != nil {
			return fmt.Errorf("skip dead node %s: %w", key, err)
		}
	}
	// Anything gated on a now-dead sub_goal must cascade (standard chain).
	if err := s.cascadeDeadDepsTx(ctx, tx, goalID, deadKeys); err != nil {
		return err
	}
	return nil
}

// cascadeDeadDepsTx is pruneDeadBranchesTx's belt: after cancelling dead
// branches, any still-gated sub_goal whose deps are now dead rides the
// EXISTING failGatedDependentsTx cascade — one call per trigger is enough
// (the helper loops to a fixed point itself), but passing all dead keys
// keeps the log attribution straight.
func (s *GoalService) cascadeDeadDepsTx(ctx context.Context, tx *sql.Tx, goalID string, deadKeys []string) error {
	for _, k := range deadKeys {
		var sgID string
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(sub_goal_id,'') FROM blueprint_node WHERE goal_id=? AND node_key=?`, goalID, k).
			Scan(&sgID); err != nil || sgID == "" {
			continue
		}
		if _, err := s.failGatedDependentsTx(ctx, tx, goalID, sgID); err != nil {
			return err
		}
	}
	return nil
}

// resolveRouterTx is the router's completed-path gate (§4.4), the exact
// spawn hook shape: parse the fenced class choice; a REGISTERED class wins,
// anything else lands on the declared fallback with fallback_used=true (a
// router never retries — a bad classification IS the fallback branch).
// Then: dead branches prune, the holder promotes verified, the decision
// lands in resolved_config (auditable + RSI fallback-rate signal), and
// wakeDependentsTx releases the chosen branch.
func (s *GoalService) resolveRouterTx(ctx context.Context, tx *sql.Tx, sg *SubGoal, rc goalRunContext) ([]events.Event, bool, error) {
	goalID := sg.GoalID
	var nodeKey, resolvedCfg, versionID string
	if err := tx.QueryRowContext(ctx,
		`SELECT node_key, COALESCE(resolved_config,'') FROM blueprint_node WHERE sub_goal_id=?`, sg.ID).
		Scan(&nodeKey, &resolvedCfg); err != nil {
		return nil, false, fmt.Errorf("load router ledger: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT blueprint_version_id FROM goal WHERE id=?`, goalID).Scan(&versionID); err != nil {
		return nil, false, fmt.Errorf("load pinned version: %w", err)
	}
	// Registered classes + fallback from the spec (the source of truth —
	// resolved_config only carries assignee/holder data).
	var content string
	if err := tx.QueryRowContext(ctx,
		`SELECT content FROM blueprint_version WHERE id=?`, versionID).Scan(&content); err != nil {
		return nil, false, fmt.Errorf("load spec content: %w", err)
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return nil, false, fmt.Errorf("parse spec content: %w", err)
	}
	var router *SpecNode
	for i := range g.Nodes {
		if g.Nodes[i].ID == nodeKey {
			router = &g.Nodes[i]
			break
		}
	}
	if router == nil || router.Router == nil {
		return nil, false, fmt.Errorf("router node %q missing from the pinned spec", nodeKey)
	}
	registered := map[string]bool{}
	for _, c := range router.Router.Classes {
		registered[c.ID] = true
	}

	var summary string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(result_summary,'') FROM run WHERE id=?`, rc.RunID).Scan(&summary); err != nil {
		return nil, false, fmt.Errorf("load router summary: %w", err)
	}
	raw := parseRouterChoice(summary)
	chosen := ""
	fallbackUsed := false
	if registered[raw] {
		chosen = raw
	} else {
		chosen = router.Router.Fallback
		fallbackUsed = true
		if raw == "" {
			logging.Warnf("blueprint: router %s produced no fenced choice — falling back to %q", nodeKey, chosen)
		} else {
			logging.Warnf("blueprint: router %s chose unregistered class %q — falling back to %q", nodeKey, raw, chosen)
		}
	}
	// Prune everything the decision did not take, then settle the holder.
	if err := s.pruneDeadBranchesTx(ctx, tx, goalID, versionID, nodeKey, chosen); err != nil {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='verified' WHERE id=? AND status IN ('done','running')`, sg.ID)
	if err != nil {
		return nil, false, fmt.Errorf("verify router holder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, false, nil // raced terminal — nothing to promote
	}
	if err := markBlueprintNodeTx(ctx, tx, sg.ID, "settled"); err != nil {
		return nil, false, err
	}
	merged, _ := mergeResolved(resolvedCfg, map[string]string{
		"chosen": chosen, "fallback_used": strconv.FormatBool(fallbackUsed), "raw_choice": raw,
	})
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`, merged, sg.ID); err != nil {
		return nil, false, fmt.Errorf("snapshot router decision: %w", err)
	}
	// machine_run_id (issue #13): the router round is a machine gate — name the
	// run it judged in its own column (verifier_run_id='' stays the machine
	// marker) so the round can be attributed once the gate is timed.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,machine_run_id,status,summary,evidence,created_at) VALUES (?,?,?,'',?,'passed',?,?,?)`,
		newID(), goalID, sg.ID, rc.RunID,
		fmt.Sprintf("路由决策：选择类 %s", chosen),
		fmt.Sprintf("router decision on run %s (raw=%q fallback=%t)", rc.RunID, raw, fallbackUsed),
		now()); err != nil {
		return nil, false, fmt.Errorf("insert router verification_result: %w", err)
	}
	// 决策 6-19: the machine routing decision is a platform event in the
	// feed (SYSTEM voice), same transaction as the promotion.
	_, commentEv, err := insertNodeCommentTx(ctx, tx, Comment{
		GoalID:     goalID,
		AuthorType: "system",
		AuthorID:   "",
		ParentID:   subGoalFeedParentTx(ctx, tx, sg.ID),
		RunID:      rc.RunID,
		Content:    routerNodeFeedContent(sg.Title, nodeKey, chosen, raw, fallbackUsed),
	})
	if err != nil {
		return nil, false, fmt.Errorf("insert router feed comment: %w", err)
	}
	logging.Infof("blueprint: router %s chose %q (fallback=%t) for goal %s", nodeKey, chosen, fallbackUsed, goalID)
	wakeEvs, err := s.wakeDependentsTx(ctx, tx, goalID, sg.ID)
	if err != nil {
		return nil, false, err
	}
	wakeEvs = append(wakeEvs, commentEv)
	return wakeEvs, true, nil
}

// ResolveBlueprintGate is the gate's human decision (§4.2/§8 Q1) and the
// sweeper's timeout landing: settle the holder verified, record the action
// in resolved_config, prune the branches the action did not take, and wake
// the chosen branch. decidedBy 'human' (API) or 'system' (timeout sweep).
// No-op on a non-held gate (already decided) returns ErrValidation so a
// double-click cannot re-decide.
func (s *GoalService) ResolveBlueprintGate(ctx context.Context, goalID, nodeKey, action, reason, decidedBy string) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var versionID, sgID, resolvedCfg string
	var state string
	if err := tx.QueryRowContext(ctx,
		`SELECT g.blueprint_version_id, bn.sub_goal_id, COALESCE(bn.resolved_config,''), bn.state
		 FROM goal g JOIN blueprint_node bn ON bn.goal_id=g.id
		 WHERE g.id=? AND bn.node_key=? AND bn.node_type='gate'`, goalID, nodeKey).
		Scan(&versionID, &sgID, &resolvedCfg, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewValidationError(fmt.Sprintf("goal %s has no gate node %q", goalID, nodeKey))
		}
		return fmt.Errorf("load gate: %w", err)
	}
	if state != "running" && state != "pending" {
		return NewConflictError(fmt.Sprintf("gate %q is already %s — a decision cannot be re-issued", nodeKey, state))
	}
	// The action must be one the gate declared (registered port).
	var content string
	if err := tx.QueryRowContext(ctx,
		`SELECT content FROM blueprint_version WHERE id=?`, versionID).Scan(&content); err != nil {
		return fmt.Errorf("load spec content: %w", err)
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return fmt.Errorf("parse spec content: %w", err)
	}
	var gate *SpecNode
	for i := range g.Nodes {
		if g.Nodes[i].ID == nodeKey {
			gate = &g.Nodes[i]
			break
		}
	}
	if gate == nil || gate.Gate == nil {
		return NewValidationError(fmt.Sprintf("node %q missing from the pinned spec", nodeKey))
	}
	registered := map[string]bool{}
	for _, a := range gate.Gate.Actions {
		registered[a.ID] = true
	}
	if !registered[action] {
		return NewValidationError(fmt.Sprintf("action %q is not one gate %q declares (registered: %s)", action, nodeKey, strings.Join(gate.Gate.ActionIDs(), ", ")))
	}
	// Settle the holder + prune + wake, all in this transaction.
	if err := s.pruneDeadBranchesTx(ctx, tx, goalID, versionID, nodeKey, action); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='verified' WHERE id=? AND status NOT IN ('verified','cancelled','failed')`, sgID)
	if err != nil {
		return fmt.Errorf("verify gate holder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NewConflictError(fmt.Sprintf("gate %q's holder is already terminal", nodeKey))
	}
	if err := markBlueprintNodeTx(ctx, tx, sgID, "settled"); err != nil {
		return err
	}
	merged, _ := mergeResolved(resolvedCfg, map[string]string{
		"action": action, "decided_by": decidedBy, "decided_at": now(),
	})
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_node SET resolved_config=? WHERE sub_goal_id=?`, merged, sgID); err != nil {
		return fmt.Errorf("snapshot gate decision: %w", err)
	}
	// A decision record the workflow graph can show as the gate's verify. The
	// authority is rendered from the SAME label the feed comment uses, so the
	// graph's audit row and the feed never disagree (human / auto rule /
	// timeout sweep).
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,summary,evidence,created_at) VALUES (?,?,?,'','passed',?,?,?)`,
		newID(), goalID, sgID,
		fmt.Sprintf("%s：%s（%s）", gateDecisionLabel(decidedBy), action, reason),
		fmt.Sprintf("gate decision by %s (node %s, action %s)", decidedBy, nodeKey, action),
		now()); err != nil {
		return fmt.Errorf("insert gate verification_result: %w", err)
	}
	// 决策 6-19: the gate decision is a SYSTEM comment in the same
	// transaction — the feed must name the deciding authority (human, auto
	// rule, or timeout sweep) instead of silently changing the graph.
	_, commentEv, err := insertNodeCommentTx(ctx, tx, Comment{
		GoalID:     goalID,
		AuthorType: "system",
		AuthorID:   "",
		ParentID:   subGoalFeedParentTx(ctx, tx, sgID),
		Content:    gateNodeFeedContent(nodeKey, action, reason, decidedBy),
	})
	if err != nil {
		return fmt.Errorf("insert gate feed comment: %w", err)
	}
	wakeEvs, err := s.wakeDependentsTx(ctx, tx, goalID, sgID)
	if err != nil {
		return err
	}
	wakeEvs = append(wakeEvs, commentEv)
	if err := tx.Commit(); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "sub_goal.verified", Payload: map[string]any{
		"goal_id": goalID, "sub_goal_id": sgID, "gate_node": nodeKey, "action": action,
	}})
	for _, ev := range wakeEvs {
		s.bus.Publish(ctx, ev)
	}
	logging.Infof("blueprint: gate %s resolved by %s → %s (goal %s)", nodeKey, decidedBy, action, goalID)
	return nil
}

// SweepBlueprintGates times out held gates (the sweeper's ONE job): every
// gate node in state running with a held_since older than timeout_after.
// A gate with an explicit timeout_action resolves with it (decidedBy=system);
// a gate with NO action (or no timeout policy) is PARKED — the deadline only
// records parked_at, never decides for the human. Returns the number
// resolved; errors are per-gate (logged, never fatal — the next tick
// retries).
func (s *GoalService) SweepBlueprintGates(ctx context.Context) (int, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT bn.goal_id, bn.node_key, COALESCE(bn.resolved_config,'') FROM blueprint_node bn
		 WHERE bn.node_type='gate' AND bn.state='running'`)
	if err != nil {
		return 0, fmt.Errorf("scan held gates: %w", err)
	}
	type heldGate struct {
		goalID, nodeKey, cfg string
	}
	var held []heldGate
	for rows.Next() {
		var h heldGate
		if err := rows.Scan(&h.goalID, &h.nodeKey, &h.cfg); err != nil {
			rows.Close()
			return 0, err
		}
		held = append(held, h)
	}
	rows.Close()
	resolved := 0
	for _, h := range held {
		var cfg struct {
			HeldSince     string `json:"held_since"`
			TimeoutAfter  string `json:"timeout_after"`
			TimeoutAction string `json:"timeout_action"`
			AutoWhen      string `json:"auto_when"`
			AutoAction    string `json:"auto_action"`
		}
		if json.Unmarshal([]byte(h.cfg), &cfg) != nil || cfg.HeldSince == "" {
			continue // never held (compile-stamped) — skip
		}
		// Runtime branch (BUG-4 §C) FIRST: if the sealed upstream evidence
		// satisfies the auto rule, the platform decides now — the deadline is
		// irrelevant. A miss falls through to the human/timeout path.
		if cfg.AutoWhen != "" {
			matched, excerpt, err := s.gateAutoMatched(ctx, h.goalID, h.nodeKey, cfg.AutoWhen)
			if err != nil {
				logging.Errorf("blueprint: gate auto %s/%s: %v", h.goalID, h.nodeKey, err)
			} else if matched {
				if err := s.ResolveBlueprintGate(ctx, h.goalID, h.nodeKey, cfg.AutoAction,
					fmt.Sprintf("自动判定：%s（上游产出：%s）", cfg.AutoWhen, excerpt), "auto"); err != nil {
					logging.Errorf("blueprint: gate auto resolve %s/%s: %v", h.goalID, h.nodeKey, err)
					continue
				}
				resolved++
				continue
			}
		}
		if cfg.TimeoutAfter == "" {
			continue // no deadline: park forever (a human decision card)
		}
		d, err := time.ParseDuration(cfg.TimeoutAfter)
		if err != nil {
			continue
		}
		heldAt, err := time.Parse(time.RFC3339Nano, cfg.HeldSince)
		if err != nil {
			continue
		}
		if time.Since(heldAt) < d {
			continue
		}
		if cfg.TimeoutAction == "" {
			// Park (the default): the deadline flags the gate for a human; it
			// never decides for them. Idempotent — parked_at is stamped once.
			var prior struct {
				ParkedAt string `json:"parked_at"`
			}
			_ = json.Unmarshal([]byte(h.cfg), &prior)
			if prior.ParkedAt != "" {
				continue
			}
			merged, err := mergeResolved(h.cfg, map[string]string{"parked_at": now()})
			if err != nil {
				logging.Errorf("blueprint: park gate %s/%s: %v", h.goalID, h.nodeKey, err)
				continue
			}
			if _, err := s.st.DB().ExecContext(ctx,
				`UPDATE blueprint_node SET resolved_config=? WHERE goal_id=? AND node_key=? AND state='running'`,
				merged, h.goalID, h.nodeKey); err != nil {
				logging.Errorf("blueprint: park gate %s/%s: %v", h.goalID, h.nodeKey, err)
				continue
			}
			logging.Warnf("blueprint: gate %s/%s timed out (timeout %s) with no action — parked for a human (no auto-decision)", h.goalID, h.nodeKey, cfg.TimeoutAfter)
			continue
		}
		if err := s.ResolveBlueprintGate(ctx, h.goalID, h.nodeKey, cfg.TimeoutAction,
			fmt.Sprintf("超时自动决策（held since %s, timeout %s）", cfg.HeldSince, cfg.TimeoutAfter), "system"); err != nil {
			logging.Errorf("blueprint: gate sweep: %s/%s: %v", h.goalID, h.nodeKey, err)
			continue
		}
		resolved++
	}
	if resolved > 0 {
		logging.Infof("blueprint: gate sweep resolved %d held gate(s)", resolved)
	}
	return resolved, nil
}

// gateAutoMatched evaluates a gate's runtime auto rule (auto_when) against the
// sealed output of the named upstream node. Evidence is the most recent
// completed subgoal run's result_summary — the same text a human would read on
// the decision card, so the automatic path and the manual path see one source.
// A missing upstream run is a MISS (not an error): the gate simply keeps
// waiting. Returns the matched flag plus a short, single-line excerpt for the
// decision record.
func (s *GoalService) gateAutoMatched(ctx context.Context, goalID, gateKey, rule string) (bool, string, error) {
	nodeKey, ct, err := ParseNodeTest(rule)
	if err != nil {
		return false, "", fmt.Errorf("parse gate auto rule %q: %w", rule, err)
	}
	var output string
	err = s.st.DB().QueryRowContext(ctx, `
		SELECT COALESCE(r.result_summary,'') FROM blueprint_node bn
		JOIN run r ON r.sub_goal_id = bn.sub_goal_id
		WHERE bn.goal_id=? AND bn.node_key=?
		  AND r.role='subgoal' AND COALESCE(r.result_summary,'')!=''
		ORDER BY r.finished_at DESC LIMIT 1`, goalID, nodeKey).Scan(&output)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil // no sealed evidence yet
	}
	if err != nil {
		return false, "", fmt.Errorf("load upstream output of %q for gate %q: %w", nodeKey, gateKey, err)
	}
	excerpt := strings.Join(strings.Fields(output), " ")
	if runes := []rune(excerpt); len(runes) > 200 {
		excerpt = string(runes[:200]) + "…"
	}
	return ct.eval(output), excerpt, nil
}

// HeldGate is one open human decision: the gate's node key, the resolved
// prompt ({{node.*}} already substituted at hold time), the registered
// action buttons, the hold clock, and the timeout policy. Decided/skipped
// gates are NOT listed — the card only exists while the decision is open.
type HeldGate struct {
	NodeKey       string           `json:"node_key"`
	Title         string           `json:"title"`
	Prompt        string           `json:"prompt"`
	Actions       []SpecGateAction `json:"actions"`
	HeldSince     string           `json:"held_since,omitempty"`
	TimeoutAfter  string           `json:"timeout_after,omitempty"`
	TimeoutAction string           `json:"timeout_action,omitempty"`
	AutoWhen      string           `json:"auto_when,omitempty"`
	AutoAction    string           `json:"auto_action,omitempty"`
	SubGoalID     string           `json:"sub_goal_id"`
}

// ListHeldGates returns a goal's open blueprint gates in node order — the
// human decision cards. Only state='running' (held) gates qualify: a
// pending gate is still waiting upstream (nothing to decide yet) and a
// settled/skipped one is history.
func (s *GoalService) ListHeldGates(ctx context.Context, goalID string) ([]HeldGate, error) {
	// Goal → pinned version → spec (the actions/prompt vocabulary).
	var versionID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT blueprint_version_id FROM goal WHERE id=?`, goalID).Scan(&versionID); err != nil {
		return nil, fmt.Errorf("load goal: %w", err)
	}
	if versionID == "" {
		return nil, NewValidationError("goal is not blueprint-driven")
	}
	var content string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT content FROM blueprint_version WHERE id=?`, versionID).Scan(&content); err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	var g SpecGraph
	if err := json.Unmarshal([]byte(content), &g); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	specGates := map[string]*SpecNode{}
	for i := range g.Nodes {
		if g.Nodes[i].Type == "gate" {
			specGates[g.Nodes[i].ID] = &g.Nodes[i]
		}
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT bn.node_key, COALESCE(bn.resolved_config,''), COALESCE(sg.description,''), COALESCE(bn.sub_goal_id,'')
		 FROM blueprint_node bn LEFT JOIN sub_goal sg ON sg.id=bn.sub_goal_id
		 WHERE bn.goal_id=? AND bn.node_type='gate' AND bn.state='running'
		 ORDER BY bn.created_at`, goalID)
	if err != nil {
		return nil, fmt.Errorf("scan held gates: %w", err)
	}
	defer rows.Close()
	out := []HeldGate{}
	for rows.Next() {
		var key, cfg, prompt, sgID string
		if err := rows.Scan(&key, &cfg, &prompt, &sgID); err != nil {
			return nil, err
		}
		n := specGates[key]
		if n == nil {
			continue // spec drifted — hide rather than guess
		}
		hg := HeldGate{
			NodeKey:   key,
			Title:     n.Title,
			Prompt:    prompt,
			Actions:   n.Gate.Actions,
			SubGoalID: sgID,
		}
		var c struct {
			HeldSince     string `json:"held_since"`
			TimeoutAfter  string `json:"timeout_after"`
			TimeoutAction string `json:"timeout_action"`
			AutoWhen      string `json:"auto_when"`
			AutoAction    string `json:"auto_action"`
		}
		if json.Unmarshal([]byte(cfg), &c) == nil {
			hg.HeldSince = c.HeldSince
			hg.TimeoutAfter = c.TimeoutAfter
			hg.TimeoutAction = c.TimeoutAction
			hg.AutoWhen = c.AutoWhen
			hg.AutoAction = c.AutoAction
		}
		out = append(out, hg)
	}
	return out, rows.Err()
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// isExecutableNode reports whether a spec node type compiles to a sub_goal
// (the depends_on gate participants). B-3: router and gate compile to HOLDER
// sub_goals — a router's sub_goal runs the classification; a gate's sub_goal
// exists to hold the depends_on edge and the node's ledger link, but it is
// NEVER enqueued (its run is the human decision, recorded on resolve).
func isExecutableNode(t string) bool {
	return t == "task" || t == "spawn" || t == "router" || t == "gate"
}

// routerClassPrompt renders the classification item's brief: the class list
// (id + title + description each) plus the platform-fixed output contract.
// §4.4: the LLM only ever names a REGISTERED class id inside the fence —
// anything else (no fence, unknown id, garbage) resolves to the declared
// fallback at resolve time, so a bad run degrades to the default path instead
// of retrying (fallback IS the router's failure semantics).
func routerClassPrompt(n *SpecNode) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s\n\n把目标分进恰好一个类。可选项（只能从中选一个，写 id）：\n", n.Title))
	for _, c := range n.Router.Classes {
		b.WriteString(fmt.Sprintf("- %s｜%s", c.ID, c.Title))
		if strings.TrimSpace(c.Description) != "" {
			b.WriteString("｜" + c.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("- 默认类（拿不准/无法判断时选它）：%s\n", n.Router.Fallback))
	bt := "```"
	b.WriteString(fmt.Sprintf("\n输出契约：报告正文自由发挥，但必须以一个 %sjson 代码块结尾，内容只有 {\"class\":\"<上面某个 id>\"}。\n", bt))
	b.WriteString("不要发明未列出的类。除该代码块外的任何内容都只是你的分析。\n")
	return b.String()
}

// gatePrompt renders the gate holder's decision-card brief: the human-facing
// prompt, the action buttons, and the timeout policy. {{node.*}} refs stay
// raw here and resolve at release (beat 2) when upstream evidence exists —
// same mechanism as task briefs.
func gatePrompt(n *SpecNode) string {
	var b strings.Builder
	b.WriteString("【人工决策点】\n\n")
	b.WriteString(n.Gate.Prompt)
	b.WriteString("\n\n可选动作：\n")
	for _, a := range n.Gate.Actions {
		b.WriteString(fmt.Sprintf("- %s（%s）\n", a.ID, a.Label))
	}
	if n.Gate.Timeout != nil && n.Gate.Timeout.Action != "" {
		b.WriteString(fmt.Sprintf("\n若无人决策，超过 %s 后自动执行动作 %q。\n", n.Gate.Timeout.After, n.Gate.Timeout.Action))
	} else if n.Gate.Timeout != nil {
		b.WriteString(fmt.Sprintf("\n若无人决策，超过 %s 后仅标记为挂起（park），不会自动推进。\n", n.Gate.Timeout.After))
	} else {
		b.WriteString("\n该决策点没有超时策略：无人决策时保持挂起。\n")
	}
	if n.Gate.Auto != nil {
		b.WriteString(fmt.Sprintf("\n自动判定：当上游产出满足 `%s` 时，平台自动执行动作 %q（无需人工）；不满足则等人决策。\n", n.Gate.Auto.When, n.Gate.Auto.Action))
	}
	b.WriteString("该工作项由人工决定推进，执行者请勿代答——等待决策即可。")
	return b.String()
}

// resolveCapabilityTx resolves capability:<role-slug> against a squad's
// member vocabulary (§11.3): roles are LOWER(TRIM(role)) normalized, the
// reviewer role is structurally excluded (it never executes), members must
// be registered agents, and multiple hits pick the least-loaded (fewest
// non-terminal sub_goals, counting both assignee and verifier hats), ties
// broken by earliest squad_membership. Fail-closed: zero matches is an
// error, never a dropped node.
func resolveCapabilityTx(ctx context.Context, tx *sql.Tx, squadID, slug string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(slug))
	var member string
	if err := tx.QueryRowContext(ctx, `
		SELECT sm.member_id FROM squad_member sm
		WHERE sm.squad_id=? AND sm.member_type='agent'
		  AND LOWER(TRIM(sm.role))=?
		  AND LOWER(TRIM(sm.role)) != ?
		  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=sm.member_id)
		ORDER BY (
		  SELECT COUNT(*) FROM sub_goal sg
		  WHERE (sg.assignee_id=sm.member_id OR sg.verifier_id=sm.member_id)
		    AND sg.status NOT IN ('verified','cancelled','failed')
		), sm.created_at, sm.id
		LIMIT 1`, squadID, norm, reviewerRole).Scan(&member); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// §11.8 alignment surface: the error lists the vocabulary the
			// goal squad actually has, so the blueprint author can fix the
			// reference (or the squad owner can register/assign the word).
			have := []string{}
			rows, qerr := tx.QueryContext(ctx, `
				SELECT DISTINCT LOWER(TRIM(sm.role)) FROM squad_member sm
				WHERE sm.squad_id=? AND sm.role!=''
				  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=sm.member_id)
				ORDER BY 1`, squadID)
			if qerr == nil {
				for rows.Next() {
					var r string
					if rows.Scan(&r) == nil && r != reviewerRole {
						have = append(have, r)
					}
				}
				rows.Close()
			}
			return "", NewValidationError(fmt.Sprintf(
				"no eligible member: goal squad has no agent with role %q (capability roles are squad_member.role values, normalized lowercase; reviewers never execute); roles present in this squad: [%s]",
				norm, strings.Join(have, ", ")))
		}
		return "", fmt.Errorf("resolve capability %s: %w", slug, err)
	}
	return member, nil
}

// marshalResolved snapshots the assignee resolution into the node's
// resolved_config — the per-node record RSI digests: what the spec asked
// for, which agent actually got it, which squad briefs its runs.
func marshalResolved(assigneeSpec, resolvedAgent, teamSquad string) (string, error) {
	b, err := json.Marshal(map[string]string{
		"assignee_spec":  assigneeSpec,
		"resolved_agent": resolvedAgent,
		"team_squad_id":  teamSquad,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// mergeResolved adds extra keys into an existing resolved_config snapshot
// (best-effort: unparseable input is replaced wholesale).
func mergeResolved(resolved string, extra map[string]string) (string, error) {
	m := map[string]string{}
	if err := json.Unmarshal([]byte(resolved), &m); err != nil {
		m = map[string]string{}
	}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// maxSpawnChildrenCap resolves a spawn node's effective fan-out cap (§11.4).
func maxSpawnChildrenCap(n *SpecNode) int {
	if n.Spawn != nil && n.Spawn.MaxChildren > 0 {
		return n.Spawn.MaxChildren
	}
	return defaultSpawnChildren
}

// spawnPlannerInstruction is the platform-fixed output contract appended to
// a spawn node's brief (§11.4): the planner plans, the executor executes,
// and the fence is the machine-checkable boundary between them. The
// materializer parses exactly one fenced json block from the run's
// result_summary; children carry agent ids the materializer validates
// against the spawn's own squad (the planner may only hand work inside its
// lane — the anti-scope-creep rule).
func spawnPlannerInstruction(n *SpecNode, teamSquadID string) string {
	maxKids := n.Spawn.MaxChildren
	if maxKids <= 0 {
		maxKids = defaultSpawnChildren
	}
	rosterHint := "（这里没有小队名册——填你自己的智能体 id）"
	if teamSquadID != "" {
		rosterHint = "（该小队成员见你的团队简报名单）"
	}
	bt := "`"
	fence := bt + bt + bt
	return "\n\n[拆解输出契约] 你这一步是\"拆解规划\"，不是亲自执行。在回复的结尾，必须输出一个 " + fence + "json 代码块，内容是子工作项数组（0 项 = 判定无需拆解，也是合法输出），每项形如 {\"title\":\"...\",\"brief\":\"...\",\"assignee\":\"<agent-id>\"}：\n" +
		"- title/brief 用执行者能直接行动的语言（brief 里可引用任务字段与你的结论，但不要使用 {{node.*}} 模板——子项在你之后执行，模板无法解析）；\n" +
		"- assignee 填该小队成员的智能体 id（32 位十六进制），不要填名字" + rosterHint + "；\n" +
		"- 按成员的角色词派工，角色词体现岗位与专有技能的差异（如 proprietary-implementer 与 design-implementer 是两个岗位）——把每一项分给擅长它的成员，而不是分给空闲的人；名册里成员名后括号内标注了各自的专有技能\n" +
		"- 最多 " + strconv.Itoa(maxKids) + " 项，超出会被拒绝并重试"
}

// ═══════════════════════════════════════════════════════════════════════
// Phase C (proposal §12): structured patch proposals → approve → new draft.
// The optimization loop's write face: findings/humans produce PATCHES, the
// validator enforces legality, approval opens a new DRAFT version
// (changelog = hypothesis) — never auto-published. 双簿记: the spec is the
// plan, execution graphs stay records; this loop is the bridge.
// ═══════════════════════════════════════════════════════════════════════

// BlueprintPatch is one structured edit op. Exactly one payload field is set
// per op kind:
//   - add_node:    Node (full SpecNode)
//   - update_node: ID + Node (same id, same type, whole body replaced)
//   - delete_node: ID (cascades: connected edges removed too)
//   - add_edge:    Edge
//   - delete_edge: Edge (source+source_handle+target identify)
type BlueprintPatch struct {
	Op   string    `json:"op"`
	ID   string    `json:"id,omitempty"`
	Node *SpecNode `json:"node,omitempty"`
	Edge *SpecEdge `json:"edge,omitempty"`
}

// BlueprintProposal is one stored patch proposal row.
type BlueprintProposal struct {
	ID                string           `json:"id"`
	BlueprintID       string           `json:"blueprint_id"`
	BaseVersionID     string           `json:"base_version_id"`
	Title             string           `json:"title"`
	Hypothesis        string           `json:"hypothesis"`
	ExpectedMetric    string           `json:"expected_metric"`
	ExpectedDirection string           `json:"expected_direction"`
	Patches           []BlueprintPatch `json:"patches"`
	FindingID         string           `json:"finding_id"`
	Status            string           `json:"status"` // pending|approved|rejected
	AppliedVersionID  string           `json:"applied_version_id"`
	DecidedAt         string           `json:"decided_at"`
	DecidedByType     string           `json:"decided_by_type"`
	DecidedByID       string           `json:"decided_by_id"`
	CreatedByType     string           `json:"created_by_type"`
	CreatedByID       string           `json:"created_by_id"`
	CreatedAt         string           `json:"created_at"`
}

// applyBlueprintPatches applies ops to a parsed spec graph in order. Pure
// structural edits only — legality (V1-V6) is validateContent's job, called
// by BOTH create (fail fast) and approve (fail closed on drift).
func applyBlueprintPatches(g *SpecGraph, patches []BlueprintPatch) error {
	for i, p := range patches {
		switch p.Op {
		case "add_node":
			if p.Node == nil {
				return NewValidationError(fmt.Sprintf("patches[%d]: add_node requires node", i))
			}
			g.Nodes = append(g.Nodes, *p.Node)
		case "update_node":
			if p.Node == nil || p.ID == "" {
				return NewValidationError(fmt.Sprintf("patches[%d]: update_node requires id + node", i))
			}
			if p.Node.ID != p.ID {
				return NewValidationError(fmt.Sprintf("patches[%d]: update_node node.id %q != op.id %q", i, p.Node.ID, p.ID))
			}
			found := false
			for j := range g.Nodes {
				if g.Nodes[j].ID == p.ID {
					if g.Nodes[j].Type != p.Node.Type {
						return NewValidationError(fmt.Sprintf("patches[%d]: update_node cannot change type (%q → %q)", i, g.Nodes[j].Type, p.Node.Type))
					}
					g.Nodes[j] = *p.Node
					found = true
					break
				}
			}
			if !found {
				return NewValidationError(fmt.Sprintf("patches[%d]: update_node target %q not found", i, p.ID))
			}
		case "delete_node":
			if p.ID == "" {
				return NewValidationError(fmt.Sprintf("patches[%d]: delete_node requires id", i))
			}
			nodes := g.Nodes[:0]
			for _, n := range g.Nodes {
				if n.ID != p.ID {
					nodes = append(nodes, n)
				}
			}
			g.Nodes = nodes
			edges := g.Edges[:0]
			for _, e := range g.Edges {
				if e.Source != p.ID && e.Target != p.ID {
					edges = append(edges, e)
				}
			}
			g.Edges = edges
		case "add_edge":
			if p.Edge == nil {
				return NewValidationError(fmt.Sprintf("patches[%d]: add_edge requires edge", i))
			}
			g.Edges = append(g.Edges, *p.Edge)
		case "delete_edge":
			if p.Edge == nil {
				return NewValidationError(fmt.Sprintf("patches[%d]: delete_edge requires edge", i))
			}
			edges := g.Edges[:0]
			for _, e := range g.Edges {
				if !(e.Source == p.Edge.Source && e.SourceHandle == p.Edge.SourceHandle && e.Target == p.Edge.Target) {
					edges = append(edges, e)
				}
			}
			g.Edges = edges
		default:
			return NewValidationError(fmt.Sprintf("patches[%d]: unknown op %q (add_node|update_node|delete_node|add_edge|delete_edge)", i, p.Op))
		}
	}
	return nil
}

// patchedContent applies patches to the base version content and re-serializes.
// Both create and approve call this — base content + patches are immutable, so
// the result is deterministic (same spirit as V6 content_hash).
func (s *BlueprintService) patchedContent(ctx context.Context, base *BlueprintVersion, patches []BlueprintPatch) (string, *SpecGraph, error) {
	g, err := parseSpecContent(base)
	if err != nil {
		return "", nil, err
	}
	if err := applyBlueprintPatches(g, patches); err != nil {
		return "", nil, err
	}
	if _, err := s.validateContent(ctx, mustMarshalSpec(g)); err != nil {
		return "", nil, err
	}
	out, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshal patched spec: %w", err)
	}
	return string(out), g, nil
}

func mustMarshalSpec(g *SpecGraph) string {
	b, _ := json.Marshal(g)
	return string(b)
}

// CreateProposal stores a pending patch proposal. The patch is validated
// against the base version IMMEDIATELY (fail fast) — the proposal API surface
// should never accept an illegal graph, even as a draft.
func (s *BlueprintService) CreateProposal(ctx context.Context, blueprintID, baseVersionID, title, hypothesis, expectedMetric, expectedDirection string, patches []BlueprintPatch, findingID, createdByType, createdByID string) (*BlueprintProposal, error) {
	if strings.TrimSpace(title) == "" {
		return nil, NewFieldRequiredError("title")
	}
	if len(patches) == 0 {
		return nil, NewValidationError("patches must not be empty — a proposal carries at least one op")
	}
	if expectedDirection != "" && expectedDirection != "up" && expectedDirection != "down" {
		return nil, NewValidationError(`expected_direction must be "up" or "down" (or empty)`)
	}
	if baseVersionID == "" {
		// default: the blueprint's latest published version
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT bv.id FROM blueprint_version bv WHERE bv.blueprint_id=? AND bv.status='published' ORDER BY bv.version DESC LIMIT 1`,
			blueprintID).Scan(&baseVersionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, NewValidationError("blueprint has no published version to patch yet")
			}
			return nil, fmt.Errorf("load latest published: %w", err)
		}
	}
	base, err := s.getVersion(ctx, baseVersionID)
	if err != nil {
		return nil, err
	}
	if base.BlueprintID != blueprintID {
		return nil, NewValidationError("base_version_id does not belong to this blueprint")
	}
	if base.Status != "published" {
		return nil, NewValidationError("base version must be published (drafts are editable directly)")
	}
	if _, _, err := s.patchedContent(ctx, base, patches); err != nil {
		return nil, err
	}
	patchesJSON, err := json.Marshal(patches)
	if err != nil {
		return nil, fmt.Errorf("marshal patches: %w", err)
	}
	p := BlueprintProposal{
		ID:                newID(),
		BlueprintID:       blueprintID,
		BaseVersionID:     baseVersionID,
		Title:             title,
		Hypothesis:        hypothesis,
		ExpectedMetric:    expectedMetric,
		ExpectedDirection: expectedDirection,
		Patches:           patches,
		FindingID:         findingID,
		Status:            "pending",
		CreatedByType:     orDefault(createdByType, "human"),
		CreatedByID:       createdByID,
		CreatedAt:         now(),
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO blueprint_proposal (id,blueprint_id,base_version_id,title,hypothesis,expected_metric,expected_direction,patches_json,finding_id,status,created_by_type,created_by_id,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.BlueprintID, p.BaseVersionID, p.Title, p.Hypothesis, p.ExpectedMetric, p.ExpectedDirection, string(patchesJSON), p.FindingID, p.Status, p.CreatedByType, p.CreatedByID, p.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert blueprint proposal: %w", err)
	}
	logging.Infof("blueprint: proposal %q created against %s (%d ops)", p.Title, p.BaseVersionID[:8], len(p.Patches))
	return &p, nil
}

func (s *BlueprintService) getProposal(ctx context.Context, proposalID string) (*BlueprintProposal, error) {
	var patchesJSON string
	p := &BlueprintProposal{}
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,blueprint_id,base_version_id,title,hypothesis,expected_metric,expected_direction,patches_json,finding_id,status,applied_version_id,decided_at,decided_by_type,decided_by_id,created_by_type,created_by_id,created_at
		 FROM blueprint_proposal WHERE id=?`, proposalID).
		Scan(&p.ID, &p.BlueprintID, &p.BaseVersionID, &p.Title, &p.Hypothesis, &p.ExpectedMetric, &p.ExpectedDirection, &patchesJSON, &p.FindingID, &p.Status, &p.AppliedVersionID, &p.DecidedAt, &p.DecidedByType, &p.DecidedByID, &p.CreatedByType, &p.CreatedByID, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load proposal: %w", err)
	}
	if err := json.Unmarshal([]byte(patchesJSON), &p.Patches); err != nil {
		return nil, fmt.Errorf("decode patches_json for proposal %s: %w", p.ID, err)
	}
	return p, nil
}

func (s *BlueprintService) GetProposal(ctx context.Context, proposalID string) (*BlueprintProposal, error) {
	return s.getProposal(ctx, proposalID)
}

func (s *BlueprintService) ListProposals(ctx context.Context, blueprintID string) ([]BlueprintProposal, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,blueprint_id,base_version_id,title,hypothesis,expected_metric,expected_direction,patches_json,finding_id,status,applied_version_id,decided_at,decided_by_type,decided_by_id,created_by_type,created_by_id,created_at
		 FROM blueprint_proposal WHERE blueprint_id=? ORDER BY created_at DESC`, blueprintID)
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}
	defer rows.Close()
	var out []BlueprintProposal
	for rows.Next() {
		var patchesJSON string
		p := BlueprintProposal{}
		if err := rows.Scan(&p.ID, &p.BlueprintID, &p.BaseVersionID, &p.Title, &p.Hypothesis, &p.ExpectedMetric, &p.ExpectedDirection, &patchesJSON, &p.FindingID, &p.Status, &p.AppliedVersionID, &p.DecidedAt, &p.DecidedByType, &p.DecidedByID, &p.CreatedByType, &p.CreatedByID, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan proposal: %w", err)
		}
		if err := json.Unmarshal([]byte(patchesJSON), &p.Patches); err != nil {
			return nil, fmt.Errorf("decode patches_json for proposal %s: %w", p.ID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ApproveProposal applies the patches (re-validated, fail-closed on drift) and
// opens a NEW DRAFT version with changelog = hypothesis. Publishing stays a
// separate human act — 「提案不自动应用」原则原样保留.
func (s *BlueprintService) ApproveProposal(ctx context.Context, proposalID, decidedByType, decidedByID string) (*BlueprintProposal, *BlueprintVersion, error) {
	p, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != "pending" {
		return nil, nil, NewConflictError(fmt.Sprintf("proposal is already %s — a decision cannot be re-issued", p.Status))
	}
	base, err := s.getVersion(ctx, p.BaseVersionID)
	if err != nil {
		return nil, nil, err
	}
	content, _, err := s.patchedContent(ctx, base, p.Patches)
	if err != nil {
		return nil, nil, fmt.Errorf("re-validate patch on approve (base may have drifted): %w", err)
	}

	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var maxVer int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM blueprint_version WHERE blueprint_id=?`, p.BlueprintID).Scan(&maxVer); err != nil {
		return nil, nil, fmt.Errorf("load max version: %w", err)
	}
	v := BlueprintVersion{
		ID:          newID(),
		BlueprintID: p.BlueprintID,
		Version:     maxVer + 1,
		Content:     content,
		ContentHash: hashContent(content),
		Status:      "draft",
		Changelog:   p.Hypothesis, // §12.1: changelog = hypothesis
		CreatedAt:   now(),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,changelog,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		v.ID, v.BlueprintID, v.Version, v.Content, v.ContentHash, v.Status, v.Changelog, v.CreatedAt); err != nil {
		return nil, nil, fmt.Errorf("insert approved draft: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE blueprint_proposal SET status='approved', applied_version_id=?, decided_at=?, decided_by_type=?, decided_by_id=? WHERE id=? AND status='pending'`,
		v.ID, now(), decidedByType, decidedByID, p.ID); err != nil {
		return nil, nil, fmt.Errorf("mark proposal approved: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	p.Status = "approved"
	p.AppliedVersionID = v.ID
	p.DecidedAt = now()
	p.DecidedByType = decidedByType
	p.DecidedByID = decidedByID
	logging.Infof("blueprint: proposal %q approved → draft v%d (%s)", p.Title, v.Version, v.ID[:8])
	return p, &v, nil
}

func (s *BlueprintService) RejectProposal(ctx context.Context, proposalID, decidedByType, decidedByID string) (*BlueprintProposal, error) {
	p, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	if p.Status != "pending" {
		return nil, NewConflictError(fmt.Sprintf("proposal is already %s — a decision cannot be re-issued", p.Status))
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE blueprint_proposal SET status='rejected', decided_at=?, decided_by_type=?, decided_by_id=? WHERE id=? AND status='pending'`,
		now(), decidedByType, decidedByID, proposalID); err != nil {
		return nil, fmt.Errorf("mark proposal rejected: %w", err)
	}
	p.Status = "rejected"
	p.DecidedAt = now()
	p.DecidedByType = decidedByType
	p.DecidedByID = decidedByID
	return p, nil
}

// BlueprintDiff is the structured three-color diff between base and patched
// spec graphs (§12.2). Nodes are aligned by id; changed nodes carry both
// snapshots so the frontend renders amber with field-level detail.
type BlueprintDiff struct {
	ProposalID     string         `json:"proposal_id"`
	BaseVersionID  string         `json:"base_version_id"`
	Base           *WorkflowGraph `json:"base"`
	Patched        *WorkflowGraph `json:"patched"`
	PatchedContent string         `json:"patched_content"`
	NodesAdded     []string       `json:"nodes_added"`
	NodesRemoved   []string       `json:"nodes_removed"`
	NodesChanged   []DiffNode     `json:"nodes_changed"`
	EdgesAdded     []string       `json:"edges_added"`
	EdgesRemoved   []string       `json:"edges_removed"`
}

type DiffNode struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Base    map[string]any `json:"base"`
	Patched map[string]any `json:"patched"`
}

func edgeKey(e SpecEdge) string {
	return e.Source + " →" + e.SourceHandle + "→ " + e.Target
}

func (s *BlueprintService) ProposalDiff(ctx context.Context, proposalID string) (*BlueprintDiff, error) {
	p, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	base, err := s.getVersion(ctx, p.BaseVersionID)
	if err != nil {
		return nil, err
	}
	baseG, err := parseSpecContent(base)
	if err != nil {
		return nil, err
	}
	patchedG, err := cloneSpec(baseG)
	if err != nil {
		return nil, err
	}
	if err := applyBlueprintPatches(patchedG, p.Patches); err != nil {
		return nil, err
	}

	d := &BlueprintDiff{
		ProposalID:    p.ID,
		BaseVersionID: p.BaseVersionID,
		Base:          specGraphToWorkflow(p.BaseVersionID, baseG),
		Patched:       specGraphToWorkflow("proposal:"+p.ID, patchedG),
		// non-nil empties: JSON emits [] (not null) — the frontend maps over
		// these lists directly.
		NodesAdded:   []string{},
		NodesRemoved: []string{},
		NodesChanged: []DiffNode{},
		EdgesAdded:   []string{},
		EdgesRemoved: []string{},
	}
	patchedJSON, _ := json.MarshalIndent(patchedG, "", "  ")
	d.PatchedContent = string(patchedJSON)

	baseNodes := map[string]SpecNode{}
	for _, n := range baseG.Nodes {
		baseNodes[n.ID] = n
	}
	patchedNodes := map[string]SpecNode{}
	for _, n := range patchedG.Nodes {
		patchedNodes[n.ID] = n
	}
	for _, n := range patchedG.Nodes {
		if _, ok := baseNodes[n.ID]; !ok {
			d.NodesAdded = append(d.NodesAdded, n.ID)
		}
	}
	for _, n := range baseG.Nodes {
		if _, ok := patchedNodes[n.ID]; !ok {
			d.NodesRemoved = append(d.NodesRemoved, n.ID)
		}
	}
	for _, b := range baseG.Nodes {
		pn, ok := patchedNodes[b.ID]
		if !ok {
			continue
		}
		bj, pj := mustMarshalSpec(&SpecGraph{Nodes: []SpecNode{b}}), mustMarshalSpec(&SpecGraph{Nodes: []SpecNode{pn}})
		if bj != pj {
			d.NodesChanged = append(d.NodesChanged, DiffNode{
				ID:      b.ID,
				Kind:    b.Type,
				Base:    jsonMapOf(b),
				Patched: jsonMapOf(pn),
			})
		}
	}
	baseEdges := map[string]bool{}
	for _, e := range baseG.Edges {
		baseEdges[edgeKey(e)] = true
	}
	patchedEdges := map[string]bool{}
	for _, e := range patchedG.Edges {
		patchedEdges[edgeKey(e)] = true
	}
	for k := range patchedEdges {
		if !baseEdges[k] {
			d.EdgesAdded = append(d.EdgesAdded, k)
		}
	}
	for k := range baseEdges {
		if !patchedEdges[k] {
			d.EdgesRemoved = append(d.EdgesRemoved, k)
		}
	}
	sort.Strings(d.NodesAdded)
	sort.Strings(d.NodesRemoved)
	sort.Strings(d.EdgesAdded)
	sort.Strings(d.EdgesRemoved)
	return d, nil
}

func cloneSpec(g *SpecGraph) (*SpecGraph, error) {
	b, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	var out SpecGraph
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func jsonMapOf(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// ── 涌现固化 (§12.4): deterministic execution-graph → blueprint translation ──

// emergentDraftName is the auto-draft's unique name (UNIQUE(squad_id,name) ⇒
// the same shape re-triggering does not double-draft). label is "固化" or
// "近似固化" (§14.1) — the two convergence paths never collide on a name.
func emergentDraftName(sig string, domainName string, label string) string {
	if len(sig) > 8 {
		sig = sig[:8]
	}
	if domainName == "" {
		domainName = "未命名域"
	}
	return label + "：" + sig + "·" + domainName
}

// DraftFromGoalExecution translates a finished infer goal's sub_goals into a
// blueprint v1 DRAFT (tasks + done edges + shared end). Deterministic: no LLM
// — the structure is already known from execution; the validator is the gate.
// nameSig keys the draft NAME: exact L1 signature for a 固化 draft, the L0
// skeleton signature for a 近似固化 draft (later near-miss triples dedup
// into the same draft instead of piling up).
func (s *BlueprintService) DraftFromGoalExecution(ctx context.Context, goalID, domainID, nameSig, label string) (*Blueprint, error) {
	// Idempotency: same shape in the same domain → same name → skip on conflict.
	var domainName string
	if domainID != "" {
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM domain WHERE id=?`, domainID).Scan(&domainName)
	}
	name := emergentDraftName(nameSig, domainName, label)
	// Emergent drafts are owned by the goal's squad. A single-agent goal has
	// no squad to own a workflow, so it records its shape but never drafts one
	// (D5: no ownerless new workflows).
	var goalSquadID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(assignee_id,'') FROM goal WHERE id=? AND assignee_type='squad'`, goalID).Scan(&goalSquadID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load goal squad for draft: %w", err)
	}
	if goalSquadID == "" {
		logging.Infof("blueprint: goal %s has no owning squad — emergent draft skipped", goalID[:8])
		return nil, nil
	}
	var exists int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM blueprint WHERE name=? AND squad_id=?`, name, goalSquadID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check emergent draft existence: %w", err)
	}
	if exists > 0 {
		logging.Infof("blueprint: emergent draft %q already exists — skip", name)
		return nil, nil
	}

	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,title,description,assignee_id,verifier_id,depends_on FROM sub_goal WHERE goal_id=? ORDER BY created_at`, goalID)
	if err != nil {
		return nil, fmt.Errorf("load sub-goals: %w", err)
	}
	defer rows.Close()
	type sgRow struct {
		ID, Title, Description, AssigneeID, VerifierID, DependsOn string
	}
	var sgs []sgRow
	for rows.Next() {
		var r sgRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Description, &r.AssigneeID, &r.VerifierID, &r.DependsOn); err != nil {
			return nil, fmt.Errorf("scan sub-goal: %w", err)
		}
		sgs = append(sgs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sgs) == 0 {
		return nil, nil // nothing structural to freeze (feed-only goal)
	}
	idx := map[string]string{} // sub_goal id → sg node id
	for i := range sgs {
		idx[sgs[i].ID] = fmt.Sprintf("sg%d", i+1)
	}
	g := SpecGraph{Name: name}
	for _, r := range sgs {
		brief := strings.TrimSpace(r.Description)
		if brief == "" {
			brief = r.Title
		}
		n := SpecNode{
			ID: idx[r.ID], Type: "task", Title: r.Title,
			Task: &SpecTask{Assignee: "agent:" + r.AssigneeID, Brief: brief},
		}
		if r.VerifierID != "" {
			n.Task.Verify = &SpecVerify{Mode: "agent", Verifier: "agent:" + r.VerifierID}
		}
		g.Nodes = append(g.Nodes, n)
	}
	// Root tasks (no deps) hang off nothing — V2 allows task|spawn roots.
	for _, r := range sgs {
		var deps []string
		if r.DependsOn != "" && r.DependsOn != "[]" {
			_ = json.Unmarshal([]byte(r.DependsOn), &deps)
		}
		for _, dep := range deps {
			if src, ok := idx[dep]; ok {
				g.Edges = append(g.Edges, SpecEdge{Source: src, SourceHandle: "done", Target: idx[r.ID]})
			}
		}
	}
	// One shared end for every task with no outbound edge.
	outDeg := map[string]int{}
	for _, e := range g.Edges {
		outDeg[e.Source]++
	}
	g.Nodes = append(g.Nodes, SpecNode{ID: "end_ok", Type: "end", Title: "完成", End: &SpecEnd{Outcome: "success"}})
	for _, r := range sgs {
		if outDeg[idx[r.ID]] == 0 {
			g.Edges = append(g.Edges, SpecEdge{Source: idx[r.ID], SourceHandle: "done", Target: "end_ok"})
		}
	}
	content, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, err
	}
	bp, err := s.CreateForSquad(ctx, goalSquadID, name,
		"自动起草（"+label+"）：同域连续 3 个 infer 执行图"+map[bool]string{true: "工作骨架同构（细节有差异）", false: "结构相同"}[label == "近似固化"]+"，由平台确定性翻译生成（"+map[bool]string{true: "§14.1 L0 骨架收敛", false: "§12.4 精确同构"}[label == "近似固化"]+"）。请审查后发布；发布前不影响任何任务。",
		string(content), "agent", "rsi-emergent")
	if err != nil {
		// A shape collision with existing blueprint names is a benign skip.
		if strings.Contains(err.Error(), "already exists") {
			logging.Infof("blueprint: emergent draft %q name collision — skip", name)
			return nil, nil
		}
		return nil, err
	}
	logging.Infof("blueprint: emergent draft %q created from goal %s (%d tasks) — awaiting human review", name, goalID[:8], len(sgs))
	return bp, nil
}
