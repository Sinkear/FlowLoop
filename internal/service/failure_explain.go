package service

// failure_explain.go — the goal-level failure voice.
//
// The goal failure path used to flip goal.status='failed' silently: the raw
// executor error lived only in run.result_summary (rendered nowhere in the
// web UI), while every OTHER terminal path — cancelled runs
// (insertCancelledRunComment), node failures (node_feed.go), guest
// collaboration failures — posts a system feed comment. A user staring at a
// red "✕ 失败" card had no way to tell "the environment broke" from "the
// work itself failed", and could not see the bounded retry chain burning
// behind an "active" card either.
//
// classifyFailure maps the executor's raw transport/API error text onto a
// human cause + an actionable next step. ownerRunRetryingFeedContent /
// ownerRunTerminalFeedContent render the feed comments for the retry and
// terminal branches of the failed-run reconcile, through the same
// insertNodeCommentTx pipeline node failures use (决策 6-19: one comment per
// transition, published only after the commit).

import (
	"fmt"
	"strings"
	"time"
)

type failureExplanation struct {
	cause  string
	advice string
}

// failureRules are matched in order against a lowercased summary; the first
// matching rule wins, so transport-specific rules must precede generic ones
// (the ECONNREFUSED text embeds "Internal error", the 5xx text embeds
// "ECONNREFUSED"-adjacent noise — most specific first).
//
// Each rule has two marker sets: `markers` is any-of (one hit selects the
// rule); `also` is all-of — every string in `also` must ALSO appear in the
// summary for the rule to fire. `also` exists for co-occurrence pairs whose
// parts are too generic alone: OpenAI writes "The model `gpt-x` does not
// exist" — "does not exist" by itself also matches "container image does
// not exist" / "reference does not exist" (docker/infra launch failures that
// are NOT model problems), but the pairing with "model" is unambiguous.
var failureRules = []struct {
	markers []string
	also    []string
	explain failureExplanation
}{
	{
		markers: []string{"econnrefused", "connection refused"},
		explain: failureExplanation{
			cause:  "模型端点连接被拒——执行器配置的模型服务（本机网关或供应商 API）不可达",
			advice: "检查模型端点是否在运行、端口与网络是否可达（本机网关进程、代理、供应商服务状态）。恢复后「重开任务」即可继续，已完成的工作不会丢失。",
		},
	},
	{
		markers: []string{"etimedout", "timed out", "timeout", "context deadline exceeded"},
		explain: failureExplanation{
			cause:  "模型端点连接/响应超时",
			advice: "网络不稳或模型服务响应过慢；稍后「重开任务」重试，若持续超时请检查网络与模型服务负载。",
		},
	},
	{
		markers: []string{"enotfound", "getaddrinfo", "dns"},
		explain: failureExplanation{
			cause:  "模型端点域名解析失败（DNS 不可用或配置错误）",
			advice: "检查执行器配置里的模型地址拼写、本机 DNS/hosts/代理设置；恢复后「重开任务」即可继续。",
		},
	},
	{
		markers: []string{"econnreset", "socket hang up", "connection reset"},
		explain: failureExplanation{
			cause:  "模型端点连接被重置（网络中断或上游服务异常关闭连接）",
			advice: "通常是瞬时网络问题；稍后「重开任务」重试，反复出现请检查网络链路或供应商服务状态。",
		},
	},
	{
		markers: []string{"invalid api key", "invalid_api_key", "incorrect api key",
			"unauthorized", "authentication", "认证失败", "鉴权失败", "无效的密钥"},
		explain: failureExplanation{
			cause:  "模型认证失败（API key 无效、过期或配置错误）",
			advice: "检查执行器/网关里配置的模型 API key；换好 key 后「重开任务」即可继续。",
		},
	},
	{
		markers: []string{"rate limit", "too many requests", "请求过于频繁", "限流"},
		explain: failureExplanation{
			cause:  "模型供应商限流（请求过于频繁）",
			advice: "稍等片刻后「重开任务」重试；频繁出现可降低并行任务数或更换供应商/渠道。",
		},
	},
	{
		markers: []string{"quota", "insufficient", "额度", "余额不足", "配额", "payment required", "billing"},
		explain: failureExplanation{
			cause:  "模型额度/余额不足",
			advice: "为所用模型充值或更换有余额的 key/供应商，然后「重开任务」即可继续。",
		},
	},
	{
		// "does not exist" alone is too broad — rc.Summary carries arbitrary
		// launch failures ("container image does not exist", "reference
		// does not exist", "path does not exist"), and misdiagnosing those
		// as a model problem would violate the file's own
		// refusal-to-guess principle. Split: unambiguous phrases stay
		// unguarded; the bare phrase only fires when "model" co-occurs
		// (OpenAI: "The model `gpt-x` does not exist").
		markers: []string{"model unavailable", "model not found", "no such model"},
		explain: failureExplanation{
			cause:  "配置的模型不可用（模型名不存在或已下线）",
			advice: "检查 agent 运行时配置的模型名是否仍然有效；改为可用模型后「重开任务」即可继续。",
		},
	},
	{
		markers: []string{"does not exist", "doesn't exist"},
		also:    []string{"model"},
		explain: failureExplanation{
			cause:  "配置的模型不可用（模型名不存在或已下线）",
			advice: "检查 agent 运行时配置的模型名是否仍然有效；改为可用模型后「重开任务」即可继续。",
		},
	},
	{
		markers: []string{"模型不存在"},
		explain: failureExplanation{
			cause:  "配置的模型不可用（模型名不存在或已下线）",
			advice: "检查 agent 运行时配置的模型名是否仍然有效；改为可用模型后「重开任务」即可继续。",
		},
	},
	{
		markers: []string{"service unavailable", "bad gateway", "overloaded",
			"服务器内部错误", "服务暂时不可用"},
		explain: failureExplanation{
			cause:  "模型供应商服务端错误",
			advice: "供应商侧异常，稍后「重开任务」重试；持续失败可临时切换供应商/渠道。",
		},
	},
}

// defaultFailureExplanation is the honest fallback: the platform refuses to
// guess a cause it cannot recognize, and points the human at the raw error
// instead of hiding it behind a generic "执行失败".
var defaultFailureExplanation = failureExplanation{
	cause:  "执行器未能完成本轮任务（原因未能自动识别）",
	advice: "查看下方原始报错定位；若是环境问题（执行器/模型/网络），恢复后「重开任务」即可继续，已完成的工作不会丢失。",
}

// classifyFailure maps a failed run's raw summary onto a human cause and an
// actionable next step. Matching is case-insensitive substring; an empty or
// unrecognized summary falls through to the default explanation.
func classifyFailure(summary string) (cause string, advice string) {
	s := strings.ToLower(strings.TrimSpace(summary))
	for _, r := range failureRules {
		matched := false
		for _, m := range r.markers {
			if strings.Contains(s, m) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// all-of guard: a rule with `also` only fires when every co-occurrence
		// partner is present too (see the model/"does not exist" pairing).
		guarded := true
		for _, a := range r.also {
			if !strings.Contains(s, a) {
				guarded = false
				break
			}
		}
		if guarded {
			return r.explain.cause, r.explain.advice
		}
	}
	return defaultFailureExplanation.cause, defaultFailureExplanation.advice
}

// ownerRunRetryingFeedContent renders the feed comment for a failed owner-run
// attempt whose bounded retry just queued a successor: the user sees the
// attempt count, the human cause, and that the platform will retry by itself
// — instead of a card that looks stuck on "active".
func ownerRunRetryingFeedContent(rc goalRunContext, attempt int, delay time.Duration) string {
	cause, _ := classifyFailure(rc.Summary)
	state := fmt.Sprintf("第 %d 次执行失败，%s自动重试（上限 %d 次，还剩 %d 次）。\n\n原因：%s\n\n任务保持进行中，无需操作。",
		attempt, humanRetryDelay(delay), maxAttempts, maxAttempts-attempt, cause)
	return nodeFeedContent("⚠️ 执行失败 · 将自动重试", state, rc.Summary)
}

// ownerRunTerminalFeedContent renders the feed comment when the goal actually
// settles failed. terminalNote names how it got there (retries exhausted /
// environment error / import goal), then the human cause and the next step.
func ownerRunTerminalFeedContent(rc goalRunContext, terminalNote string) string {
	cause, advice := classifyFailure(rc.Summary)
	state := terminalNote + "\n\n原因：" + cause + "\n\n下一步：" + advice
	return nodeFeedContent("❌ 任务失败", state, rc.Summary)
}

// humanRetryDelay renders the retry backoff for the feed note.
func humanRetryDelay(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("约 %d 分钟后", int(d.Minutes()))
	}
	return fmt.Sprintf("%d 秒后", int(d.Seconds()))
}
