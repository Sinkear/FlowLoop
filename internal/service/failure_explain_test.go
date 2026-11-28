package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/store"
)

// liveECONNREFUSED is the exact result_summary the production executor wrote
// on 2026-11-28 when the local model gateway (magpie) was killed with its DSH
// session cgroup and every run's first model call hit a dead port. It embeds
// "Internal error" — the classifier must still read it as a transport
// refusal, not a generic 5xx.
const liveECONNREFUSED = "prompt: acp: session/prompt: Internal error: ECONNREFUSED: Unable to connect. Is the computer able to access the url?"

// TestClassifyFailure drives the ordered marker table with the live
// production summary plus one case per deterministic fault family. First
// match wins, so transport-specific markers must shadow generic ones.
func TestClassifyFailure(t *testing.T) {
	cases := []struct {
		name       string
		summary    string
		wantCause  string
		wantAdvice string
	}{
		{"live ECONNREFUSED", liveECONNREFUSED, "模型端点连接被拒", "检查模型端点"},
		{"connect timeout", "dial tcp: i/o timeout", "连接/响应超时", "重开任务"},
		{"dns failure", "getaddrinfo: nodename nor servname provided", "域名解析失败", "DNS"},
		{"conn reset", "read: connection reset by peer", "连接被重置", "网络"},
		{"bad api key", "Error code: 401 - Incorrect API key provided", "认证失败", "API key"},
		{"auth case-variant 401", "Upstream error: 401 Unauthorized", "认证失败", "API key"},
		{"auth case-variant sentence", "Authentication failed: token expired", "认证失败", "API key"},
		{"rate limited", "Error code: 429 - Rate limit reached", "限流", "稍等片刻"},
		{"quota exhausted", "billing hard limit reached, insufficient quota", "额度/余额不足", "充值"},
		{"model gone", "The model `gpt-x` does not exist", "模型不可用", "模型名"},
		{"non-model does-not-exist stays unknown", "Error: container image does not exist in registry", "未能自动识别", "原始报错"},
		{"upstream 5xx", "Error code: 503 - service unavailable", "服务端错误", "供应商"},
		{"unknown garbage", "kaboom mystery failure", "未能自动识别", "原始报错"},
		{"empty summary", "", "未能自动识别", "原始报错"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause, advice := classifyFailure(tc.summary)
			if !strings.Contains(cause, tc.wantCause) {
				t.Fatalf("cause = %q, want substring %q", cause, tc.wantCause)
			}
			if !strings.Contains(advice, tc.wantAdvice) {
				t.Fatalf("advice = %q, want substring %q", advice, tc.wantAdvice)
			}
		})
	}
}

// TestOwnerRunRetryingFeedContent: the retrying comment must carry the
// attempt counter, the backoff, the human cause, AND the raw summary —
// the user sees both the story and the evidence.
func TestOwnerRunRetryingFeedContent(t *testing.T) {
	rc := goalRunContext{Attempt: 1, Summary: liveECONNREFUSED}
	c := ownerRunRetryingFeedContent(rc, 1, 30*time.Second)
	for _, want := range []string{
		"⚠️ 执行失败 · 将自动重试",
		"第 1 次执行失败",
		"30 秒后",
		"上限 3 次，还剩 2 次",
		"原因：模型端点连接被拒",
		liveECONNREFUSED,
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("retrying feed comment missing %q:\n%s", want, c)
		}
	}
}

// TestOwnerRunTerminalFeedContent: the terminal comment must explain how the
// goal died (terminalNote), the human cause, the next step, and the raw error.
func TestOwnerRunTerminalFeedContent(t *testing.T) {
	rc := goalRunContext{Attempt: 3, Summary: liveECONNREFUSED}
	c := ownerRunTerminalFeedContent(rc, "第 3 次执行失败，重试次数已用尽（上限 3 次），任务终止。")
	for _, want := range []string{
		"❌ 任务失败",
		"重试次数已用尽",
		"原因：模型端点连接被拒",
		"下一步：",
		liveECONNREFUSED,
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("terminal feed comment missing %q:\n%s", want, c)
		}
	}
}

// TestFailedOwnerRunLeavesFeedTrace is the end-to-end guard for 决策 6-19
// (user m00313): a failed owner run used to flip goal.status silently — the
// raw error lived only in run.result_summary and NO surface rendered it.
// Now every failed attempt posts a system feed comment: ⚠️ while retries
// remain, ❌ when the goal settles failed.
func TestFailedOwnerRunLeavesFeedTrace(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	runs, _ := rs.List(ctx, g.ID)
	if len(runs) == 0 {
		t.Fatal("expected birth owner run")
	}

	// failAttempt marks the run at the given attempt failed in the DB (what
	// run.Finish does before the daemon reconciles), then runs the reconcile.
	failAttempt := func(runID string, attempt int) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx,
			`UPDATE run SET status='failed', attempt=?, finished_at=?, result_summary=? WHERE id=?`,
			attempt, now(), liveECONNREFUSED, runID); err != nil {
			t.Fatalf("fail run: %v", err)
		}
		if err := gs.ReconcileOnRunEnd(ctx, goalRunContext{
			RunID: runID, GoalID: g.ID, AgentID: a, Role: "owner",
			Status: "failed", Attempt: attempt, Summary: liveECONNREFUSED,
		}); err != nil {
			t.Fatalf("reconcile failed run: %v", err)
		}
	}
	nextRunID := func(attempt int) string {
		t.Helper()
		var id string
		if err := st.DB().QueryRowContext(ctx,
			`SELECT id FROM run WHERE goal_id=? AND attempt=? ORDER BY queued_at DESC LIMIT 1`,
			g.ID, attempt).Scan(&id); err != nil {
			t.Fatalf("run at attempt %d: %v", attempt, err)
		}
		return id
	}
	sysComments := func() []string {
		t.Helper()
		rows, err := st.DB().QueryContext(ctx,
			`SELECT content FROM comment WHERE goal_id=? AND author_type='system' ORDER BY created_at`, g.ID)
		if err != nil {
			t.Fatalf("query system comments: %v", err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatalf("scan comment: %v", err)
			}
			out = append(out, c)
		}
		return out
	}

	// Attempt 1 fails → retry queued, ⚠️ comment, goal stays active.
	failAttempt(runs[0].ID, 1)
	if status := goalStatus(t, st, g.ID); status != "active" {
		t.Fatalf("goal must stay active while retries remain, got %q", status)
	}
	if c := strings.Join(sysComments(), "\n"); !strings.Contains(c, "⚠️ 执行失败 · 将自动重试") || !strings.Contains(c, "第 1 次执行失败") {
		t.Fatalf("retrying failure must leave a system comment, got:\n%s", c)
	}

	// Attempt 2 fails → second ⚠️ comment.
	failAttempt(nextRunID(2), 2)
	if c := sysComments(); len(c) != 2 {
		t.Fatalf("expected 2 retrying comments after attempt 2, got %d", len(c))
	}

	// Attempt 3 fails → terminal: ❌ comment + goal failed.
	failAttempt(nextRunID(3), 3)
	if status := goalStatus(t, st, g.ID); status != "failed" {
		t.Fatalf("goal must be failed after exhausted retries, got %q", status)
	}
	c := strings.Join(sysComments(), "\n")
	for _, want := range []string{"❌ 任务失败", "重试次数已用尽", "原因：模型端点连接被拒", liveECONNREFUSED} {
		if !strings.Contains(c, want) {
			t.Fatalf("terminal failure comment missing %q:\n%s", want, c)
		}
	}
}

// TestNonRetryableFailureGoesTerminalImmediately: an auth failure (决策:
// deterministic faults cannot heal with time) must skip the retry theater
// and post the ❌ comment with the 环境/配置 note on attempt 1.
func TestNonRetryableFailureGoesTerminalImmediately(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	runs, _ := rs.List(ctx, g.ID)
	if len(runs) == 0 {
		t.Fatal("expected birth owner run")
	}
	// Real-world auth errors arrive in provider case ("Unauthorized",
	// "Authentication failed") — the marker match must be case-insensitive
	// (isNonRetryable lowercases the summary; see MAJOR-1 regression).
	const authErr = "Upstream error: 401 Unauthorized: invalid authentication credentials"
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='failed', attempt=1, finished_at=?, result_summary=? WHERE id=?`,
		now(), authErr, runs[0].ID); err != nil {
		t.Fatalf("fail run: %v", err)
	}
	if err := gs.ReconcileOnRunEnd(ctx, goalRunContext{
		RunID: runs[0].ID, GoalID: g.ID, AgentID: a, Role: "owner",
		Status: "failed", Attempt: 1, Summary: authErr,
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if status := goalStatus(t, st, g.ID); status != "failed" {
		t.Fatalf("auth failure is non-retryable: goal must be failed at attempt 1, got %q", status)
	}
	rows, err := st.DB().QueryContext(ctx,
		`SELECT content FROM comment WHERE goal_id=? AND author_type='system'`, g.ID)
	if err != nil {
		t.Fatalf("query system comments: %v", err)
	}
	defer rows.Close()
	var all string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		all += c + "\n"
	}
	for _, want := range []string{"❌ 任务失败", "环境/配置类错误", "认证失败", authErr} {
		if !strings.Contains(all, want) {
			t.Fatalf("terminal comment missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "⚠️ 执行失败 · 将自动重试") {
		t.Fatal("non-retryable failure must not post a retrying comment")
	}
}

func goalStatus(t *testing.T, st *store.Store, goalID string) string {
	t.Helper()
	ctx := context.Background()
	var status string
	if err := st.DB().QueryRowContext(ctx, `SELECT status FROM goal WHERE id=?`, goalID).Scan(&status); err != nil {
		t.Fatalf("goal status: %v", err)
	}
	return status
}

// TestIsNonRetryableCaseInsensitive: MAJOR-1 regression guard — provider
// summaries arrive in arbitrary case ("401 Unauthorized", "Model
// Unavailable"); a case-sensitive match would burn the full retry budget
// on deterministic faults and post self-contradicting comments
// (cause says auth, terminal note says retries exhausted).
func TestIsNonRetryableCaseInsensitive(t *testing.T) {
	for _, s := range []string{
		"Upstream error: 401 Unauthorized",
		"Authentication failed: token expired",
		"Model Unavailable: provider rejected",
		"Invalid API Key provided",
		"  free quota 已用完  ",
	} {
		if !isNonRetryable(s) {
			t.Errorf("isNonRetryable(%q) = false, want true (case-insensitive match)", s)
		}
	}
	for _, s := range []string{
		liveECONNREFUSED,
		"Error: container image does not exist in registry",
		"",
		"   ",
		"kaboom mystery failure",
	} {
		if isNonRetryable(s) {
			t.Errorf("isNonRetryable(%q) = true, want false (retryable/empty)", s)
		}
	}
}
