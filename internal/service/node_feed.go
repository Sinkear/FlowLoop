package service

// node_feed.go — the platform's automatic node feed (决策 6-19).
//
// Every node-level outcome lands ONE comment in the goal feed, in the SAME
// transaction as the state transition it reports (invariant 13: publish only
// after commit). The reconciler and the verdict/gate paths therefore hand the
// returned event to their existing evs slice; no helper publishes on its own.
//
// Author voice follows the transition's authority, not the reporter:
//   - execution completed (machine verified / handed to a verifier) → the
//     ASSIGNEE agent reports its own work;
//   - verdict passed/rejected → the VERIFIER agent's judgment;
//   - failed/cancelled/spawn/router/gate → system (the platform decided —
//     retry cap exhausted, watchdog kill, machine routing, human gate).

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Sinkear/flowloop/internal/events"
)

// nodeFeedNoSummary is the one-line fallback when a run produced no summary.
// The feed must still show that the node settled — silence is what made the
// feed useless before 决策 6-19.
const nodeFeedNoSummary = "执行完成（无文字总结）"

// insertNodeCommentTx persists one node-feed comment and its activity row in
// the caller's transaction, returning the comment and the comment:created
// event the caller must publish AFTER its commit. Threading: an explicit
// ParentID wins; an empty one falls back to the run's trigger comment, the
// goal's root comment, then the run's wake_anchor (the same chain
// insertCancelledRunComment uses) so a comment is never orphaned.
func insertNodeCommentTx(ctx context.Context, tx *sql.Tx, c Comment) (Comment, events.Event, error) {
	if c.GoalID == "" {
		return c, events.Event{}, fmt.Errorf("node feed comment: goal_id is required")
	}
	if c.AuthorType == "" {
		return c, events.Event{}, fmt.Errorf("node feed comment: author_type is required")
	}
	if c.ID == "" {
		c.ID = newID()
	}
	if c.CreatedAt == "" {
		c.CreatedAt = now()
	}
	if c.ParentID == "" {
		c.ParentID = nodeFeedParentTx(ctx, tx, c)
	}
	var parentID any
	if c.ParentID != "" {
		parentID = c.ParentID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at,run_id,ask_human) VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.GoalID, c.AuthorType, c.AuthorID, parentID, c.Content, c.CreatedAt, c.RunID, c.AskHuman); err != nil {
		return c, events.Event{}, fmt.Errorf("insert node feed comment: %w", err)
	}
	// Same activity row the human/agent comment path writes, so the timeline
	// renders the node report exactly like any other comment.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO activity_log (id,goal_id,actor_type,actor_id,action,detail,created_at) VALUES (?,?,?,?,'commented',?,?)`,
		newID(), c.GoalID, c.AuthorType, c.AuthorID, "{}", c.CreatedAt); err != nil {
		return c, events.Event{}, fmt.Errorf("insert node feed activity: %w", err)
	}
	return c, events.Event{Topic: "comment:created", Payload: c}, nil
}

// nodeFeedParentTx resolves the thread anchor for a comment with no explicit
// parent: the run's trigger comment, then the goal root, then the run's
// wake_anchor. Best-effort — a missing anchor is not fatal (parent_id is
// nullable and an old goal may have no comment at all).
func nodeFeedParentTx(ctx context.Context, tx *sql.Tx, c Comment) string {
	if c.RunID != "" {
		var trigger string
		if err := tx.QueryRowContext(ctx,
			`SELECT trigger_comment_id FROM run WHERE id=?`, c.RunID).Scan(&trigger); err == nil && trigger != "" {
			return trigger
		}
	}
	var root string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM comment WHERE goal_id=? ORDER BY created_at ASC LIMIT 1`, c.GoalID).Scan(&root); err == nil {
		return root
	}
	if c.RunID != "" {
		var anchor string
		if err := tx.QueryRowContext(ctx,
			`SELECT wake_anchor FROM run WHERE id=?`, c.RunID).Scan(&anchor); err == nil {
			return anchor
		}
	}
	return ""
}

// subGoalFeedParentTx returns the dispatch comment that created the work item
// — the thread every per-attempt report of that node replies to, so retries do
// not fragment the feed into unrelated branches.
func subGoalFeedParentTx(ctx context.Context, tx *sql.Tx, subGoalID string) string {
	var id string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(dispatch_comment_id,'') FROM sub_goal WHERE id=?`, subGoalID).Scan(&id); err != nil {
		return ""
	}
	return id
}

// latestNodeReportCommentTx returns the most recent AGENT comment written
// under one of the node's execution runs. A verdict replies to that comment
// (the work report it judges) instead of dangling at the goal root.
func latestNodeReportCommentTx(ctx context.Context, tx *sql.Tx, goalID, subGoalID string) string {
	var id string
	if err := tx.QueryRowContext(ctx, `
		SELECT c.id FROM comment c
		JOIN run r ON r.id = c.run_id
		WHERE c.goal_id=? AND r.sub_goal_id=? AND r.role='subgoal' AND c.author_type='agent'
		ORDER BY c.created_at DESC LIMIT 1`, goalID, subGoalID).Scan(&id); err != nil {
		return ""
	}
	return id
}

// ── content builders (private, one per node outcome) ──

// completedNodeFeedContent renders the assignee's execution report.
// verifying=true means an agent verifier was named and the quality gate is
// still open; the comment must not claim verification passed.
func completedNodeFeedContent(title, summary string, verifying bool) string {
	if verifying {
		return nodeFeedContent("🧪 节点执行完成 · "+title,
			"已提交给验证者，等待质量裁决。", summary)
	}
	return nodeFeedContent("✅ 节点完成 · "+title,
		"机器校验通过，交付物已就绪。", summary)
}

// failedNodeFeedContent renders a failed execution attempt. retrying is true
// while the bounded retry chain still has budget; only the last attempt
// terminal-fails the node.
func failedNodeFeedContent(rc goalRunContext, title string, attempt int, retrying bool) string {
	var state string
	if retrying {
		state = fmt.Sprintf("第 %d 次执行失败，已自动排入下一次重试（上限 %d 次）。", attempt, maxExecutionAttempts)
	} else {
		state = fmt.Sprintf("第 %d 次执行失败，重试次数已用尽，节点已终止。", attempt)
	}
	return nodeFeedContent("❌ 节点失败 · "+title, state, rc.Summary)
}

// cancelledNodeFeedContent renders a cancelled execution attempt. The summary
// of a cancelled run is platform noise ("cancelled by platform"), so the
// cancel reason is the authoritative body.
func cancelledNodeFeedContent(rc goalRunContext, title string, attempt int, retrying bool) string {
	reason := cancelReasonDescription(rc.CancelReason)
	if reason == "" {
		reason = "被平台中止（无结构化原因）"
	}
	state := fmt.Sprintf("第 %d 次执行被中止：%s。", attempt, reason)
	if retrying {
		state += "已自动排入下一次重试。"
	} else {
		state += "节点已终止。"
	}
	return nodeFeedContent("⏹ 节点执行被中止 · "+title, state, rc.Summary)
}

// verdictNodeFeedContent renders the verifier's judgment. A rejected verdict
// names the rework round it starts (quality_iteration is bumped by the same
// transaction, so the next round is QualityIteration+1).
func verdictNodeFeedContent(sg SubGoal, verdict, summary, evidence string) string {
	if verdict == "passed" {
		body := nodeFeedContent("🔍 验证通过 · "+sg.Title, "", summary)
		if ev := strings.TrimSpace(evidence); ev != "" {
			body += "\n\n证据：" + ev
		}
		return body
	}
	body := nodeFeedContent("🔁 验证未通过 · "+sg.Title, "", summary)
	body += fmt.Sprintf("\n\n已退回执行者返工（第 %d 轮）。", sg.QualityIteration+1)
	if ev := strings.TrimSpace(evidence); ev != "" {
		body += "\n\n证据：" + ev
	}
	return body
}

// spawnNodeFeedContent renders a spawn's successful expansion. The child
// titles are listed so the feed itself shows what the plan became without
// the reader opening the blueprint graph.
func spawnNodeFeedContent(title string, childTitles []string) string {
	var b strings.Builder
	b.WriteString("**🧩 子计划已展开 · ")
	b.WriteString(title)
	b.WriteString("**\n\n")
	if len(childTitles) == 0 {
		b.WriteString("拆解结果为空（没有可派发的执行子任务）。")
		return b.String()
	}
	fmt.Fprintf(&b, "已拆解为 %d 个执行子任务：", len(childTitles))
	for _, t := range childTitles {
		fmt.Fprintf(&b, "\n- %s", t)
	}
	return b.String()
}

// spawnFailedNodeFeedContent renders a spawn that violated the planner
// contract — a system event (the platform refused the plan), never the
// planner agent's own voice.
func spawnFailedNodeFeedContent(rc goalRunContext, title string, attempt int, retrying bool, reason string) string {
	var state string
	if retrying {
		state = fmt.Sprintf("第 %d 次拆解失败，已自动排入下一次重试（上限 %d 次）。", attempt, maxExecutionAttempts)
	} else {
		state = fmt.Sprintf("第 %d 次拆解失败，重试次数已用尽，节点已终止。", attempt)
	}
	body := nodeFeedContent("❌ 子计划拆解失败 · "+title, state+"\n"+reason, rc.Summary)
	return body
}

// routerNodeFeedContent renders the routing decision: the chosen branch, the
// raw model choice when it disagreed with the registered class, and whether
// the fallback path was taken.
func routerNodeFeedContent(title, nodeKey, chosen, raw string, fallbackUsed bool) string {
	if fallbackUsed {
		if raw == "" {
			return fmt.Sprintf("**🔀 路由决策 · %s**\n\n节点 %s 未匹配到已注册的类（模型未给出选择），按声明使用 fallback 分支 `%s`。", title, nodeKey, chosen)
		}
		return fmt.Sprintf("**🔀 路由决策 · %s**\n\n节点 %s 未匹配到已注册的类（原始选择：%s），按声明使用 fallback 分支 `%s`。", title, nodeKey, raw, chosen)
	}
	if raw != "" && raw != chosen {
		return fmt.Sprintf("**🔀 路由决策 · %s**\n\n节点 %s 选择分支 `%s`（原始选择：%s）。", title, nodeKey, chosen, raw)
	}
	return fmt.Sprintf("**🔀 路由决策 · %s**\n\n节点 %s 选择分支 `%s`。", title, nodeKey, chosen)
}

// gateDecisionLabel renders the gate's deciding authority in the platform's
// fixed voice. Shared by the verification_result audit row and the feed
// comment so a gate's graph record and its feed message can never disagree
// about who decided.
func gateDecisionLabel(decidedBy string) string {
	switch decidedBy {
	case "auto":
		return "自动判定（上游条件满足）"
	case "system":
		return "超时自动决策"
	default:
		return "人工决策"
	}
}

// gateNodeFeedContent renders a gate decision. decidedBy is 'human' (the API
// decision), 'auto' (the runtime auto rule matched), or 'system' (the timeout
// sweeper). The comment names the authority so the feed never hides an
// automatic decision behind a human-looking one.
func gateNodeFeedContent(nodeKey, action, reason, decidedBy string) string {
	return fmt.Sprintf("**🚪 门决策 · %s**\n\n决策人：%s\n动作：%s\n理由：%s", nodeKey, gateDecisionLabel(decidedBy), action, reason)
}

// nodeFeedContent assembles heading + optional note + summary body, with the
// standard no-summary fallback. A summary that merely repeats the platform
// heading (an agent pasting the board text) is stripped of that duplicate
// line rather than echoed twice.
func nodeFeedContent(heading, note, summary string) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(heading)
	b.WriteString("**")
	if note != "" {
		b.WriteString("\n\n")
		b.WriteString(note)
	}
	b.WriteString("\n\n")
	b.WriteString(nodeFeedSummaryText(summary))
	return b.String()
}

// nodeFeedSummaryText trims the run summary and drops a leading line that is
// just the platform heading again (decisions 6-19: the template must read as
// one coherent post, not heading + duplicate heading).
func nodeFeedSummaryText(summary string) string {
	s := strings.TrimSpace(summary)
	if s == "" {
		return nodeFeedNoSummary
	}
	first, rest, _ := strings.Cut(s, "\n")
	h := strings.Trim(strings.TrimSpace(first), "*")
	for _, p := range []string{
		"✅ 节点完成", "🧪 节点执行完成", "❌ 节点失败", "❌ 节点执行失败",
		"⏹ 节点执行被中止", "🔍 验证通过", "🔁 验证未通过",
		"🧩 子计划已展开", "❌ 子计划拆解失败", "🔀 路由决策", "🚪 门决策",
	} {
		if strings.HasPrefix(h, p) {
			if r := strings.TrimSpace(rest); r != "" {
				return r
			}
			return nodeFeedNoSummary
		}
	}
	return s
}
