package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/store"
)

// ── node feed tests (决策 6-19) ─────────────────────────────────────────────
// The platform lands ONE feed comment per node terminal state / verdict /
// machine decision, in the SAME transaction as the state transition, and
// publishes comment:created only after the commit. These tests pin the three
// primary shapes: machine-verified completion, verifier verdict, terminal
// failure (the retry-exhausted shape).

// newFeedCluster wires the services with an accessibly-owned bus so a test
// can observe the post-commit comment:created events (newTestCluster throws
// its bus away).
func newFeedCluster(t *testing.T) (*GoalService, *RunService, *store.Store, *events.Bus) {
	t.Helper()
	st := newTestStore(t)
	bus := events.NewBus()
	gs := NewGoalService(st, bus)
	rs := NewRunService(st, bus)
	cs := NewCommentService(st, bus)
	gs.SetRunService(rs)
	rs.SetGoalService(gs)
	cs.SetRunService(rs)
	cs.SetGoalService(gs)
	return gs, rs, st, bus
}

// subscribeCommentCreated captures comment:created payloads (the feed comment
// itself) so a test can prove the publish happens after commit.
func subscribeCommentCreated(bus *events.Bus) <-chan Comment {
	ch := make(chan Comment, 16)
	bus.Subscribe("comment:created", func(_ context.Context, e events.Event) {
		switch c := e.Payload.(type) {
		case Comment:
			ch <- c
		case *Comment:
			ch <- *c
		}
	})
	return ch
}

// awaitCommentEvent blocks until the published comment whose content contains
// marker arrives. A failure means the transaction committed but the event was
// never published (or was published before commit and lost).
func awaitCommentEvent(t *testing.T, ch <-chan Comment, marker string) Comment {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case c := <-ch:
			if strings.Contains(c.Content, marker) {
				return c
			}
		case <-deadline:
			t.Fatalf("no comment:created event carrying %q was published", marker)
			return Comment{}
		}
	}
}

// loadNodeComments returns the comments stamped with runID, oldest first.
func loadNodeComments(t *testing.T, ctx context.Context, st *store.Store, runID string) []Comment {
	t.Helper()
	rows, err := st.DB().QueryContext(ctx,
		`SELECT id, goal_id, author_type, author_id, parent_id, content, created_at, run_id
		 FROM comment WHERE run_id=? ORDER BY created_at, id`, runID)
	if err != nil {
		t.Fatalf("load comments for run %s: %v", runID, err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.GoalID, &c.AuthorType, &c.AuthorID, &c.ParentID, &c.Content, &c.CreatedAt, &c.RunID); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate comments: %v", err)
	}
	return out
}

// commentedActivityCount counts the 'commented' activity rows a goal already
// has — the node feed must add exactly one per inserted comment, the same as
// every other comment path.
func commentedActivityCount(t *testing.T, ctx context.Context, st *store.Store, goalID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_log WHERE goal_id=? AND action='commented'`, goalID).Scan(&n); err != nil {
		t.Fatalf("count commented activity: %v", err)
	}
	return n
}

func subGoalRunID(t *testing.T, ctx context.Context, st *store.Store, sgID, role string) string {
	t.Helper()
	var id string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id FROM run WHERE sub_goal_id=? AND role=? ORDER BY queued_at DESC, rowid DESC LIMIT 1`, sgID, role).Scan(&id); err != nil {
		t.Fatalf("load %s run for sub-goal %s: %v", role, sgID, err)
	}
	return id
}

// TestNodeFeedCompletedWithoutVerifier: a sub-goal with no named verifier is
// machine-verified — the assignee's run summary lands as an AGENT comment
// authored by the assignee, threaded under the dispatch comment that created
// the work item, and the comment:created event is published after commit.
func TestNodeFeedCompletedWithoutVerifier(t *testing.T) {
	gs, rs, st, bus := newFeedCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "owner")
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: owner, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", worker, "", nil, "agent", owner)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	sgRun := subGoalRunID(t, ctx, st, sg.ID, "subgoal")
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET base_ref='b1', head_ref='h1' WHERE id=?`, sgRun); err != nil {
		t.Fatal(err)
	}
	ch := subscribeCommentCreated(bus)
	if err := rs.Finish(ctx, sgRun, "completed", "implemented the thing"); err != nil {
		t.Fatalf("finish assignee run: %v", err)
	}

	after, err := gs.GetSubGoal(ctx, sg.ID)
	if err != nil || after.Status != "verified" {
		t.Fatalf("no-verifier completion must verify the node, got %q (err %v)", after.Status, err)
	}
	comments := loadNodeComments(t, ctx, st, sgRun)
	if len(comments) != 1 {
		t.Fatalf("exactly one node report comment expected, got %d", len(comments))
	}
	c := comments[0]
	if c.AuthorType != "agent" {
		t.Fatalf("completion report must be authored by the executor (agent), got %q", c.AuthorType)
	}
	if c.AuthorID != worker {
		t.Fatalf("completion report author must be the assignee %s, got %s", worker, c.AuthorID)
	}
	for _, want := range []string{"✅ 节点完成 · work item", "机器校验通过", "implemented the thing"} {
		if !strings.Contains(c.Content, want) {
			t.Fatalf("completion comment missing %q:\n%s", want, c.Content)
		}
	}
	if c.ParentID != sg.DispatchCommentID {
		t.Fatalf("completion report must thread to the dispatch comment %s, got parent %q", sg.DispatchCommentID, c.ParentID)
	}
	if n := commentedActivityCount(t, ctx, st, g.ID); n != 1 {
		t.Fatalf("node feed must record exactly one 'commented' activity, got %d", n)
	}
	ev := awaitCommentEvent(t, ch, "✅ 节点完成")
	if ev.ID != c.ID || ev.RunID != sgRun {
		t.Fatalf("published event must carry the persisted comment (id %s run %s), got id %s run %s", c.ID, sgRun, ev.ID, ev.RunID)
	}
}

// TestNodeFeedVerifierVerdictThreadsToReport: a named verifier parks the node
// in 'verifying' with an AGENT report by the executor; the passed verdict is
// a second AGENT comment authored by the verifier, replying to that report
// (feed reads dispatch → execution → verdict as one thread).
func TestNodeFeedVerifierVerdictThreadsToReport(t *testing.T) {
	gs, rs, st, bus := newFeedCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "owner")
	worker := seedAgent(t, st, "worker")
	qa := seedAgent(t, st, "qa")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: owner, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", worker, qa, nil, "agent", owner)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	sgRun := subGoalRunID(t, ctx, st, sg.ID, "subgoal")
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET base_ref='b1', head_ref='h1' WHERE id=?`, sgRun); err != nil {
		t.Fatal(err)
	}
	ch := subscribeCommentCreated(bus)
	if err := rs.Finish(ctx, sgRun, "completed", "implemented the thing"); err != nil {
		t.Fatalf("finish assignee run: %v", err)
	}
	after, err := gs.GetSubGoal(ctx, sg.ID)
	if err != nil || after.Status != "verifying" {
		t.Fatalf("named verifier must park the node in verifying, got %q (err %v)", after.Status, err)
	}
	report := loadNodeComments(t, ctx, st, sgRun)
	if len(report) != 1 {
		t.Fatalf("exactly one execution report comment expected, got %d", len(report))
	}
	if report[0].AuthorType != "agent" || report[0].AuthorID != worker {
		t.Fatalf("execution report must be agent-authored by %s, got %s/%s", worker, report[0].AuthorType, report[0].AuthorID)
	}
	if !strings.Contains(report[0].Content, "🧪 节点执行完成") || !strings.Contains(report[0].Content, "等待质量裁决") {
		t.Fatalf("execution report must announce the verify hand-off:\n%s", report[0].Content)
	}
	if rc := awaitCommentEvent(t, ch, "🧪 节点执行完成"); rc.ID != report[0].ID {
		t.Fatalf("execution report event id %s != persisted %s", rc.ID, report[0].ID)
	}

	verifyRun := subGoalRunID(t, ctx, st, sg.ID, "verify")
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='running' WHERE id=?`, verifyRun); err != nil {
		t.Fatal(err)
	}
	if err := gs.VerifySubGoal(ctx, verifyRun, "passed", "tests green", "evidence: full suite"); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	after, err = gs.GetSubGoal(ctx, sg.ID)
	if err != nil || after.Status != "verified" {
		t.Fatalf("passed verdict must verify the node, got %q (err %v)", after.Status, err)
	}
	verdicts := loadNodeComments(t, ctx, st, verifyRun)
	if len(verdicts) != 1 {
		t.Fatalf("exactly one verdict comment expected, got %d", len(verdicts))
	}
	v := verdicts[0]
	if v.AuthorType != "agent" || v.AuthorID != qa {
		t.Fatalf("verdict must be agent-authored by the verifier %s, got %s/%s", qa, v.AuthorType, v.AuthorID)
	}
	if v.ParentID != report[0].ID {
		t.Fatalf("verdict must reply to the execution report %s, got parent %q", report[0].ID, v.ParentID)
	}
	for _, want := range []string{"🔍 验证通过 · work item", "tests green", "证据：evidence: full suite"} {
		if !strings.Contains(v.Content, want) {
			t.Fatalf("verdict comment missing %q:\n%s", want, v.Content)
		}
	}
	if ev := awaitCommentEvent(t, ch, "🔍 验证通过"); ev.ID != v.ID {
		t.Fatalf("verdict event id %s != persisted %s", ev.ID, v.ID)
	}
}

// TestNodeFeedFailedTerminal: the last allowed attempt failing terminal-fails
// the node. The platform reports it as a SYSTEM comment (the platform refused
// the attempt, not the agent's own words), naming the attempt count, the
// exhausted retry budget and the run summary.
func TestNodeFeedFailedTerminal(t *testing.T) {
	gs, rs, st, bus := newFeedCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "owner")
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: owner, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", worker, "", nil, "agent", owner)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	// Two attempts are already spent — the next failure is terminal.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE sub_goal SET execution_attempt=2 WHERE id=?`, sg.ID); err != nil {
		t.Fatal(err)
	}
	sgRun := subGoalRunID(t, ctx, st, sg.ID, "subgoal")
	ch := subscribeCommentCreated(bus)
	if err := rs.Finish(ctx, sgRun, "failed", "boom: compile error"); err != nil {
		t.Fatalf("finish failed run: %v", err)
	}
	after, err := gs.GetSubGoal(ctx, sg.ID)
	if err != nil || after.Status != "failed" {
		t.Fatalf("retry-exhausted failure must terminal-fail the node, got %q (err %v)", after.Status, err)
	}
	comments := loadNodeComments(t, ctx, st, sgRun)
	if len(comments) != 1 {
		t.Fatalf("exactly one failure report expected, got %d", len(comments))
	}
	c := comments[0]
	if c.AuthorType != "system" || c.AuthorID != "" {
		t.Fatalf("failure report must be a system comment, got %s/%q", c.AuthorType, c.AuthorID)
	}
	for _, want := range []string{"❌ 节点失败 · work item", "第 3 次执行失败", "重试次数已用尽", "boom: compile error"} {
		if !strings.Contains(c.Content, want) {
			t.Fatalf("failure comment missing %q:\n%s", want, c.Content)
		}
	}
	if c.ParentID != sg.DispatchCommentID {
		t.Fatalf("failure report must thread to the dispatch comment %s, got parent %q", sg.DispatchCommentID, c.ParentID)
	}
	if ev := awaitCommentEvent(t, ch, "❌ 节点失败"); ev.ID != c.ID || ev.RunID != sgRun {
		t.Fatalf("failure event must carry the persisted comment, got id %s run %s", ev.ID, ev.RunID)
	}
}

// TestNodeFeedCancelledSubGoalStillReports pins the Major A9 fix: a real
// cancellation flips the sub_goal to 'cancelled' BEFORE the killed run's late
// outcome is reconciled (CancelSubGoal → daemon kills the run → Finish
// cancelled). The node report must still land — system-authored, threaded to
// the dispatch — without resurrecting the node or enqueueing a retry.
func TestNodeFeedCancelledSubGoalStillReports(t *testing.T) {
	gs, rs, st, bus := newFeedCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "owner")
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: owner, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", worker, "", nil, "agent", owner)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	runID := subGoalRunID(t, ctx, st, sg.ID, "subgoal")
	// The daemon's cancel path: the run is in flight, CancelSubGoal flips the
	// sub_goal, then the killed run's late terminal outcome arrives via Finish.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='running', cancel_reason='stopped' WHERE id=?`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.CancelSubGoal(ctx, sg.ID); err != nil {
		t.Fatalf("cancel sub-goal: %v", err)
	}
	ch := subscribeCommentCreated(bus)
	if err := rs.Finish(ctx, runID, "cancelled", "cancelled by platform"); err != nil {
		t.Fatalf("finish cancelled run: %v", err)
	}
	after, err := gs.GetSubGoal(ctx, sg.ID)
	if err != nil || after.Status != "cancelled" {
		t.Fatalf("cancelled node must stay cancelled (no resurrection), got %q (err %v)", after.Status, err)
	}
	comments := loadNodeComments(t, ctx, st, runID)
	if len(comments) != 1 {
		t.Fatalf("exactly one cancellation report expected, got %d", len(comments))
	}
	c := comments[0]
	if c.AuthorType != "system" || c.AuthorID != "" {
		t.Fatalf("cancellation report must be a system comment, got %s/%q", c.AuthorType, c.AuthorID)
	}
	for _, want := range []string{"⏹ 节点执行被中止 · work item", "第 1 次执行被中止", "stopped by user", "节点已终止", "cancelled by platform"} {
		if !strings.Contains(c.Content, want) {
			t.Fatalf("cancellation comment missing %q:\n%s", want, c.Content)
		}
	}
	if c.ParentID != sg.DispatchCommentID {
		t.Fatalf("cancellation report must thread to the dispatch comment %s, got parent %q", sg.DispatchCommentID, c.ParentID)
	}
	// No resurrection: the cancel did not buy the node a retry run.
	var runs int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE sub_goal_id=? AND role='subgoal'`, sg.ID).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("a cancelled node must not be retried, got %d runs (err %v)", runs, err)
	}
	if ev := awaitCommentEvent(t, ch, "⏹ 节点执行被中止"); ev.ID != c.ID || ev.RunID != runID {
		t.Fatalf("cancellation event must carry the persisted comment, got id %s run %s", ev.ID, ev.RunID)
	}
}

// findNodeComment returns the newest comment in a goal containing marker.
// Gate reports have no run (a gate decision is not a run end), so the
// run_id-scoped loader cannot see them.
func findNodeComment(t *testing.T, ctx context.Context, st *store.Store, goalID, marker string) Comment {
	t.Helper()
	var c Comment
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id, goal_id, author_type, author_id, COALESCE(parent_id,''), content, created_at, COALESCE(run_id,'')
		 FROM comment WHERE goal_id=? AND content LIKE ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		goalID, "%"+marker+"%").Scan(&c.ID, &c.GoalID, &c.AuthorType, &c.AuthorID, &c.ParentID, &c.Content, &c.CreatedAt, &c.RunID); err != nil {
		t.Fatalf("find comment %q in goal %s: %v", marker, goalID, err)
	}
	return c
}

// latestVerificationSummary reads the newest verification_result summary for a
// sub-goal (the graph's audit row, which must agree with the feed comment).
func latestVerificationSummary(t *testing.T, ctx context.Context, st *store.Store, sgID string) string {
	t.Helper()
	var s string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT summary FROM verification_result WHERE sub_goal_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, sgID).Scan(&s); err != nil {
		t.Fatalf("load latest verification summary for %s: %v", sgID, err)
	}
	return s
}

// TestNodeFeedGateCommentsNameAuthority: the gate comment and the
// verification_result audit row must render the SAME deciding authority —
// Minor #3: the record used to hardcode 人工决策 while the comment could say
// 自动判定/超时自动决策.
func TestNodeFeedGateCommentsNameAuthority(t *testing.T) {
	ctx := context.Background()

	t.Run("human", func(t *testing.T) {
		gs, _, st, goalID, gateSGID := gateScenario(t)
		if err := gs.ResolveBlueprintGate(ctx, goalID, "n_gate", "approve", "lgtm", "human"); err != nil {
			t.Fatalf("resolve gate: %v", err)
		}
		gateSG, err := gs.GetSubGoal(ctx, gateSGID)
		if err != nil {
			t.Fatal(err)
		}
		c := findNodeComment(t, ctx, st, goalID, "🚪 门决策 · n_gate")
		if c.AuthorType != "system" || c.AuthorID != "" {
			t.Fatalf("gate decision must be a system comment, got %s/%q", c.AuthorType, c.AuthorID)
		}
		if c.ParentID != gateSG.DispatchCommentID {
			t.Fatalf("gate comment must thread to the dispatch comment %s, got parent %q", gateSG.DispatchCommentID, c.ParentID)
		}
		for _, want := range []string{"决策人：人工决策", "动作：approve", "理由：lgtm"} {
			if !strings.Contains(c.Content, want) {
				t.Fatalf("human gate comment missing %q:\n%s", want, c.Content)
			}
		}
		if got := latestVerificationSummary(t, ctx, st, gateSGID); got != "人工决策：approve（lgtm）" {
			t.Fatalf("verification_result summary must match the feed voice, got %q", got)
		}
	})

	t.Run("auto", func(t *testing.T) {
		gs, _, st, goalID, _ := autoGateScenario(t,
			`{"when": "node.n_seed.output contains \"ready to ship\"", "action": "approve"}`,
			"ready to ship — all checks pass")
		if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 1 {
			t.Fatalf("auto match must resolve the gate, got %d (%v)", n, err)
		}
		gateSG := subGoalByTitle(t, gs, goalID, "sign-off")
		c := findNodeComment(t, ctx, st, goalID, "🚪 门决策 · n_gate")
		if !strings.Contains(c.Content, "决策人：自动判定（上游条件满足）") {
			t.Fatalf("auto gate comment must name the auto rule:\n%s", c.Content)
		}
		if c.ParentID != gateSG.DispatchCommentID {
			t.Fatalf("gate comment must thread to the dispatch comment %s, got parent %q", gateSG.DispatchCommentID, c.ParentID)
		}
		if got := latestVerificationSummary(t, ctx, st, gateSG.ID); !strings.HasPrefix(got, "自动判定（上游条件满足）：approve（") {
			t.Fatalf("verification_result summary must carry the auto authority, got %q", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		gs, _, st, goalID, gateSGID := gateScenario(t)
		old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
		cfg := resolvedConfigOf(t, st, goalID, "n_gate")
		patched := strings.Replace(cfg,
			`"held_since":"`+heldSinceOf(t, st, goalID, "n_gate")+`"`,
			`"held_since":"`+old+`"`, 1)
		if patched == cfg {
			t.Fatalf("held_since backdate failed to patch: %s", cfg)
		}
		if _, err := st.DB().ExecContext(ctx,
			`UPDATE blueprint_node SET resolved_config=? WHERE goal_id=? AND node_key=?`,
			patched, goalID, "n_gate"); err != nil {
			t.Fatalf("backdate: %v", err)
		}
		if n, err := gs.SweepBlueprintGates(ctx); err != nil || n != 1 {
			t.Fatalf("timeout sweep must resolve the gate, got %d (%v)", n, err)
		}
		gateSG, err := gs.GetSubGoal(ctx, gateSGID)
		if err != nil {
			t.Fatal(err)
		}
		c := findNodeComment(t, ctx, st, goalID, "🚪 门决策 · n_gate")
		if !strings.Contains(c.Content, "决策人：超时自动决策") {
			t.Fatalf("timeout gate comment must name the sweep:\n%s", c.Content)
		}
		if c.ParentID != gateSG.DispatchCommentID {
			t.Fatalf("gate comment must thread to the dispatch comment %s, got parent %q", gateSG.DispatchCommentID, c.ParentID)
		}
		if got := latestVerificationSummary(t, ctx, st, gateSGID); !strings.HasPrefix(got, "超时自动决策：reject（") {
			t.Fatalf("verification_result summary must carry the timeout authority, got %q", got)
		}
	})
}

// routerFeedComment drives seed → release → decision and returns the routing
// report plus the router holder and the goal ID.
func routerFeedComment(t *testing.T, summary string) (Comment, *SubGoal, string) {
	t.Helper()
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	v := seedPublishedContent(t, bs, "router-bp", routerJSON(worker))
	g, err := gs.Create(ctx, Goal{
		Title: "router goal", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	seedSG := subGoalByTitle(t, gs, g.ID, "prep")
	finishSubGoalRunCompleted(t, rs, st, seedSG.ID)
	routeSG := subGoalByTitle(t, gs, g.ID, "route by size")
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, routeSG.ID), "completed", summary); err != nil {
		t.Fatalf("finish router run: %v", err)
	}
	c := findNodeComment(t, ctx, st, g.ID, "🔀 路由决策 · route by size")
	return c, routeSG, g.ID
}

// TestNodeFeedRouterDecisionComment: the router's machine choice lands as one
// system comment threaded to the dispatch, naming the chosen class and the raw
// model choice — including the fallback wording when the choice was rejected.
func TestNodeFeedRouterDecisionComment(t *testing.T) {
	t.Run("registered", func(t *testing.T) {
		c, routeSG, _ := routerFeedComment(t, routerSummary("quick"))
		if c.AuthorType != "system" || c.AuthorID != "" {
			t.Fatalf("router decision must be a system comment, got %s/%q", c.AuthorType, c.AuthorID)
		}
		if c.ParentID != routeSG.DispatchCommentID {
			t.Fatalf("router comment must thread to the dispatch comment %s, got parent %q", routeSG.DispatchCommentID, c.ParentID)
		}
		for _, want := range []string{"选择分支 `quick`"} {
			if !strings.Contains(c.Content, want) {
				t.Fatalf("router comment missing %q:\n%s", want, c.Content)
			}
		}
		if strings.Contains(c.Content, "原始选择") {
			t.Fatalf("a registered choice needs no confusing 'raw choice' aside:\n%s", c.Content)
		}
	})

	t.Run("unregistered", func(t *testing.T) {
		// A parsed but unregistered class: the fallback runs, the raw id stays
		// visible so the decision is auditable.
		c, routeSG, _ := routerFeedComment(t, routerSummary("middle"))
		if c.ParentID != routeSG.DispatchCommentID {
			t.Fatalf("fallback comment must thread to the dispatch comment %s, got parent %q", routeSG.DispatchCommentID, c.ParentID)
		}
		for _, want := range []string{"未匹配到已注册的类", "原始选择：middle", "fallback 分支 `long`"} {
			if !strings.Contains(c.Content, want) {
				t.Fatalf("unregistered comment missing %q:\n%s", want, c.Content)
			}
		}
	})

	t.Run("fallback", func(t *testing.T) {
		// No fenced block → the declared fallback class, flagged in the feed.
		c, routeSG, _ := routerFeedComment(t, "我觉得应该走长路径，改动太大了。")
		if c.ParentID != routeSG.DispatchCommentID {
			t.Fatalf("fallback comment must thread to the dispatch comment %s, got parent %q", routeSG.DispatchCommentID, c.ParentID)
		}
		for _, want := range []string{"未匹配到已注册的类", "fallback 分支 `long`"} {
			if !strings.Contains(c.Content, want) {
				t.Fatalf("fallback comment missing %q:\n%s", want, c.Content)
			}
		}
	})
}

// TestNodeFeedSpawnExpansionComment: a spawn node reports the child expansion
// (never the executor's "节点完成" — the spawn resolver already owns the node's
// one report), system-authored and threaded to the dispatch, listing the child
// titles.
func TestNodeFeedSpawnExpansionComment(t *testing.T) {
	gs, rs, bs, st := bpCluster(t)
	ctx := context.Background()
	goalID, planner := bpSpawnFixture(t, gs, bs, st)
	if _, err := gs.Activate(ctx, goalID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	sgs, err := gs.ListSubGoals(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	var spawnSG *SubGoal
	for i := range sgs {
		if len(sgs[i].DependsOn) == 0 {
			spawnSG = &sgs[i]
			break
		}
	}
	if spawnSG == nil {
		t.Fatalf("no ungated spawn sub-goal among %d compiled", len(sgs))
	}
	spawnRun := latestSubGoalRunID(t, st, spawnSG.ID)
	kids := `[{"title":"child-a","brief":"做甲","assignee":"` + planner + `"},{"title":"child-b","brief":"做乙","assignee":"` + planner + `"}]`
	if err := rs.Finish(ctx, spawnRun, "completed", plannerSummary(kids)); err != nil {
		t.Fatalf("finish planner run: %v", err)
	}
	comments := loadNodeComments(t, ctx, st, spawnRun)
	if len(comments) != 1 {
		t.Fatalf("spawn must land exactly ONE report (the expansion, not a second 节点完成), got %d", len(comments))
	}
	c := comments[0]
	if c.AuthorType != "system" || c.AuthorID != "" {
		t.Fatalf("spawn expansion must be a system comment, got %s/%q", c.AuthorType, c.AuthorID)
	}
	for _, want := range []string{"🧩 子计划已展开 · 拆解规划", "已拆解为 2 个执行子任务", "- child-a", "- child-b"} {
		if !strings.Contains(c.Content, want) {
			t.Fatalf("spawn expansion comment missing %q:\n%s", want, c.Content)
		}
	}
	if strings.Contains(c.Content, "✅ 节点完成") {
		t.Fatalf("spawn node must not also emit a completion report:\n%s", c.Content)
	}
	if c.ParentID != spawnSG.DispatchCommentID {
		t.Fatalf("spawn comment must thread to the dispatch comment %s, got parent %q", spawnSG.DispatchCommentID, c.ParentID)
	}
}

// 门决策的决策方文案不允许把未知值说成「人工决策」：图谱审计行与 feed 评论
// 共用 gateDecisionLabel，未知/空 decidedBy 必须显式暴露，而不是默认成人做的。
func TestGateDecisionLabelRejectsUnknownAsHuman(t *testing.T) {
	cases := map[string]string{
		"human":  "人工决策",
		"auto":   "自动判定（上游条件满足）",
		"system": "超时自动决策",
		"":       "未记录决策方",
		"robot":  "未记录决策方（robot）",
	}
	for in, want := range cases {
		if got := gateDecisionLabel(in); got != want {
			t.Errorf("gateDecisionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
