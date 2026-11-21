# BUG-2 分析：防重复审批只认「最近一次批准」，交付失败后的重试被当成重复点

（findings-bug2-duplicate-approval.md）

> **范围**：`ResolveReview` 的重复决策守卫（`internal/service/goal.go:1917-2012`）及其在 2026-11-21 opencode 交付卡死中的表现。
> **不覆盖**：BUG-1 的 DB 失败本身（见 [findings-bug1-deliver-sqlite-busy.md](findings-bug1-deliver-sqlite-busy.md)）；审批 UI 的完整交互设计；`authorize` / `handoff_loop` 两种 park 的语义。
> **基线**：代码基线为 `HEAD = a3d2744`（2026-11-21 15:05 +0800）；事故运行版本是更旧的 featnew 构建（`0.1.0-featnew.20261119`，源码已删除），两者行号可能漂移。文中每条结论都给出 HEAD 是否已修的判定；HEAD 未提交的修复分支不算已修。

## 事故现场

opencode 的 `review_ready` 在 19:41:50 就绪，人工在 **09:06:34** 点了「批准并自动合入」。同一秒 daemon 的交付收口撞上 `SQLITE_BUSY`（BUG-1），goal 停在 `review`。随后 49 分 48 秒里，人又点了三次：

| 时间（11-21） | 动作 | 结果 | 距首次批准 |
|---|---|---|---|
| 09:06:34 | approve | `MarkDelivered` 失败（BUG-1）；状态仍 `review` | — |
| 09:07:01 | approve | `REJECTED (already approved — deliver in flight or its result pending)` | 27s |
| 09:09:09 | approve | 同上 | 2m35s |
| 09:17:59 | approve | 同上 | 11m25s |
| 09:56:20 | 人工杀 daemon（pid 12382） | 看门狗 2s 后拉起 21302，`recoverPendingDelivers` 补交付 | 49m46s |
| 09:56:22 | — | goal `done`，round 4 密封 | 49m48s |

三次拒绝的提示语一字不差，只有 daemon.log 里的 `Warnf` 留痕。人无法从 API 得知「交付还在飞」还是「交付早就死了」。

## 一页结论（按影响排序）

| # | 级别 | 结论 | HEAD 判定 |
|---|---|---|---|
| P0-1 | **一次批准后，守卫永久拒绝重试** | 守卫只读「最近一条 gate_decision 是 approve」+「run_id 相同」，**不读决定时间**；交付失败不改变状态，于是「1 秒前还在飞」与「14 小时前已死」被当成同一件事 | 未修（`goal.go:1956-1967`） |
| P0-2 | **失败态在守卫眼里不可见** | 唯一放行信号是 `review_request` 的 `deliver:` 前缀（`goal.go:1946`），而该注记只在 `success=false` 且事务内写（`:3266-3271`）；本次是 `success=true` 的内部 DB 错误，根本走不到，失败被 `deliver.go:331-332` 吞掉 | 未修 |
| P1-1 | **驳回也被连带禁止** | 批准后的 reject/redirect 同样被拒（`goal.go:1975-1977`），人连「打回让 agent 修」这条退路都没有，只能重启 daemon | 未修 |
| P1-2 | **错误文案不区分「在飞/已失败」** | 两种状态都是无错误码的 `ValidationError`，文案还把「check the deliver result」当成可执行动作——平台上根本没有这个查询入口 | 未修（`goal.go:1972-1973`） |
| P1-3 | **UI 只有组件本地态，刷新即回到可点** | HEAD 的 `goal-actions.tsx` 在 mutation 成功后 disable 按钮；但刷新/换端/批量栏（`goal-batch-bar.tsx:32`）会回到可点；源码注释仍自认 "the page shows no feedback while deliver runs" | 部分缓解，未系统修复 |
| P2 | **拒绝只有 Warnf，无可观测指标** | 生产线无法回答「有多少次批准被守卫拒绝、拒了多久」，只能 grep 日志 | 未修（`goal.go:1972`） |

**一句话**：这条守卫想解决的是「异步交付窗口内重复批准会堆 `gate_decision`」，但它把「窗口」实现成了一个**没有时间维度的状态谓词**；一旦交付失败且状态不变，窗口就变成永久的，重试与重复再也无法区分。

## 逐项分析

### 1. 守卫到底在守什么

```go
// internal/service/goal.go:1931-1933（前置）
if g.Status != "review" {
    logging.Warnf("review: goal %q is %s, not review — decision %s ignored", ...)
    return nil, NewCodedError(CodeGoalNotInReview, ...)
}
```

```go
// internal/service/goal.go:1946-1967（节选）
deliverFailed := strings.HasPrefix(g.ReviewRequest, "deliver:")
...
handoffLoopPark := strings.HasPrefix(g.ReviewRequest, "handoff_loop:")
...
authorizePark := strings.HasPrefix(g.ReviewRequest, "authorize:")

var lastDecision, lastRunID string
err = s.db.QueryRowContext(ctx,
    `SELECT decision, run_id FROM gate_decision
      WHERE goal_id = ? ORDER BY decided_at DESC LIMIT 1`,
    goalID).Scan(&lastDecision, &lastRunID)

if err == nil && !deliverFailed && !handoffLoopPark && !authorizePark && lastRunID == runID {
    if input.Decision == "approve" && lastDecision == "approve" {
        logging.Warnf("review: goal %q decision=approve REJECTED (already approved — deliver in flight or its result pending)", goalID)
        return nil, NewValidationError("goal already approved — waiting for the deliver step (or check the deliver result)")
    }
    if (input.Decision == "reject" || input.Decision == "redirect") && lastDecision == "approve" {
        logging.Warnf("review: goal %q decision=%s REJECTED (already approved — being delivered — available after the deliver finishes or fails)", ...)
        return nil, NewValidationError("goal already approved — being delivered; rejection is available after the deliver finishes or fails")
    }
}
```

守卫有三个维度：**状态**（必须 `review`）、**最近决策**（`approve`）、**run 身份**（`lastRunID == runID`，防止 reopen 后旧周期误伤）。源码注释（`goal.go:1935-1944`，含「live: the reopened goal's approve was rejected」）说明 `lastRunID` 这条是后来补的，HEAD 比事故 build 更完善。

但它缺第四个维度：**时间**。`SELECT` 明明按 `decided_at DESC` 排序，却**没把 `decided_at` 取出来**——排序键被丢掉，正是「有时间信息但不用」的典型。

### 2. 为什么一次失败就永久锁死

批准后的正常时序是：

```text
approve → gate_decision(approve) 落库 → status 仍 review
        → 发布 goal:approved → daemon 异步 merge/verify/push
        → MarkDelivered: UPDATE status='done'
```

`MarkDelivered` 失败时（BUG-1），事务回滚，**没有任何状态改变**：`status` 仍 `review`，`review_request` 仍为空，`gate_decision` 仍是那条 approve。于是守卫的输入在 1 秒后、14 小时后完全一致。它无法区分：

- 交付 goroutine 正在 merge（此时拒绝是对的）；
- 交付 goroutine 已经因 DB 错误退出，且没有重试、没有重启就不会再回来（此时拒绝是致命的）。

更糟的是，失败信号被设计放在了一个**本次走不到的分支**：

```go
// internal/service/goal.go:3266-3271（success=false 才写）
if _, err := tx.ExecContext(ctx,
    `UPDATE goal SET review_request = ? WHERE id = ?`,
    "deliver: "+note, goalID); err != nil { ... }
```

本次 `success=true`（交付内容层面成功），失败发生在 `MarkDelivered` 内部的 DB 写；函数返回 error 前根本没到这段。于是「失败注记 → `deliver:` 前缀 → 守卫放行」这条设计好的自愈链，对最需要它的故障类型恰好不生效。

### 3. 为什么人连「驳回」也点不了

上面的第二段守卫把 reject/redirect 一起拒了。设计意图可以理解：批准之后 merge 可能正在写仓库，此时 reject 会与交付竞态。但后果是——交付一旦静默失败，人既不能重试批准，也不能打回，唯一动作是杀进程。事故中确实如此（09:56:20 kill）。

而 P1-2 的文案「or check the deliver result」把一个不存在的操作写进了错误提示：API 没有 deliver 状态查询，UI 也没有；`status=review` + 空 `review_request` 就是全部信息。

### 4. 这不是「防重复」本身的错，而是生命周期建模缺失

把「异步交付」建模成「状态 + 谓词」而不是「状态机 + 超时」，必然出现两类错误：

1. **假阳性**（HEAD 已修一半）：reopen 后的新周期被旧 approve 误伤 → 用 `lastRunID` 挡住了；
2. **假阴性**（未修）：同一周期内交付死了，守卫仍认为它在飞。

`lastRunID` 是「按周期隔离」，但周期内没有「已开始 / 进行中 / 失败 / 可重试」的子状态。BUG-1 的 `MarkDelivered` 也没有把「已尝试交付」落成任何可观测事实（没有 `deliver_started` 活动、没有 attempt 计数），守卫只能靠猜。

### 5. UI 侧：本地态掩盖了系统缺陷

HEAD 的 `web/components/goal-actions.tsx` 在 mutation 成功后：

```tsx
// web/components/goal-actions.tsx:302-310（节选）
disabled={resolve.isPending || resolveOutcome === "approved"}
...
? (authorizePark ? "已批准，继续中…" : scratch ? "已批准，完成" : "已批准，合入中…")
```

单卡单次操作时，人不会再点。但：

- 刷新页面 / 换标签 / 换端 → `resolveOutcome` 归零，按钮恢复；
- `goal-batch-bar.tsx:32` 的批量「批准合入」不共享单卡本地态；
- 事故中三次重试间隔 27s / 2m35s / 11m25s，**事故 build 很可能没有这层保护**（否则无法解释连续点击），或人绕过了它。

所以 P1-3 只能算「部分缓解」：系统层仍没有「已批准，交付中」这个服务端可查询的状态。

## 复现条件

| # | 条件 | 预期（当前 HEAD） |
|---|---|---|
| R1 | 任一 repo 域 goal 处于 `review`，最近 `gate_decision=approve` 且 `run_id` 相同；让 `MarkDelivered` 的 `UPDATE` 返回 `SQLITE_BUSY`（注入或并发持锁） | 交付失败，goal 停在 `review`，无 `deliver:` 注记 |
| R2 | 紧接着再 POST approve（任意间隔，1s 或 14h） | 400 `goal already approved — waiting for the deliver step (or check the deliver result)` |
| R3 | 改 POST reject | 400 `goal already approved — being delivered; rejection is available after the deliver finishes or fails` |
| R4 | 不重启 daemon，等待 | 永不恢复；`recoverPendingDelivers` 只在启动时跑一次 |
| R5 | 重启 daemon | 恢复并 `done`；此时再 approve 返回 `CodeGoalNotInReview` |

## 影响面

| 维度 | 影响 |
|---|---|
| 可用性 | 单次 DB 抖动 → 该 goal 交付永久停摆，直到人工重启 daemon |
| 人工成本 | 事故中 4 次点击、1 次 kill/拉起、49m48s 延迟；操作者无法从错误信息判断该等还是该重启 |
| 数据正确性 | 三次重试**没有**产生重复 `gate_decision`（守卫确实挡住了写入）——副作用是「拒绝也不留痕」，审计只能靠 daemon.log |
| 指标 | `review_duration` 把等待期算进审批时长（opencode 48,283s），污染 RSI 的 human_wait / lead_time |
| 连带 | reject/redirect 被禁；若交付实际已 merge 而人误以为失败，也无从核对 |

## 修复方案

分三层，建议一起做；只做第 1 层也能消除事故主因。

### 第 1 层：给守卫加时间维度（必须）

```go
// 查询同时取 decided_at
SELECT decision, run_id, decided_at FROM gate_decision
 WHERE goal_id = ? ORDER BY decided_at DESC LIMIT 1

// 守卫
if lastDecision == "approve" && lastRunID == runID && !deliverFailed {
    if time.Since(lastDecidedAt) < approveGrace {   // 建议 5 分钟，与 deliverWaitForRuns 对齐，不得短于它
        // 在飞：409 + Retry-After
    }
    // 超过 grace 仍是 review：交付已失联 → 重新发布 goal:approved（不写 gate_decision）
}
```

要点：

- **重新发布而不是重写决策**：不要再 INSERT `gate_decision`，否则一次抖动会在审计里变成多次批准；
- **必须节流**：重发前检查 `activity_log` 里最近一条 `deliver_retriggered`（或进程内 per-goal 时间戳），同一 grace 窗口只触发一次，避免连点导致多个交付 goroutine；
- grace 与 `deliverWaitForRuns = 5*time.Minute`（`deliver.go:42`）的关系要写死：repo 域交付本身可能等 5 分钟，grace 不应短于它；建议 **5 分钟**，与现有等待一致；
- 若 BUG-1 的周期恢复（10 分钟 ticker）先落地，重发可由恢复器统一做，API 只负责返回「已安排重试」——两者选其一，不要双写。

### 第 2 层：把状态说清楚（必须）

| 场景 | 错误码（新增） | HTTP | 文案 |
|---|---|---|---|
| 批准后 grace 内 | `CodeApproveInFlight` | 409 + `Retry-After` | 交付进行中（已等待 Xs），请稍后重试 |
| grace 后仍 review 且无失败注记 | `CodeDeliverStalled` | 409 | 交付已失联，已安排重试（或：可重试批准） |
| 有 `deliver:` 注记 | `CodeDeliverFailed` | 409 | 上次交付失败：<原因>；可重新批准 |
| 已 leave review | 复用 `CodeGoalNotInReview` | 409 | 目标已不在审批中 |

前端按码渲染，而不是匹配中文串。

### 第 3 层：让 reject 有出路（建议）

- grace 内仍拒绝 reject/redirect（保护进行中的 merge）；
- grace 后允许 reject，但返回 warning：「交付可能已合入，驳回将重新打开目标」；
- 前提是服务端能判断「交付是否已进入 merge」——当前没有这个事实，需要 BUG-1 修复时顺手落一个 `deliver_started` 活动（或 `deliver_attempt` 计数）。没有这个事实前，保守做法是**保持 reject 禁用，但提供显式 `POST /goals/{id}/deliver/retry` 管理动作**。

### 第 4 层：可观测性

- 每次守卫拒绝：结构化日志 + 计数器 `review_decision_rejected_total{reason="in_flight|stalled"}`；
- 每次重发：`deliver_retriggered` 活动 + 指标；
- 看板：`review` 状态停留 > grace 的 goal 数（应恒为 0）。

## 验收

| # | 场景 | 预期 |
|---|---|---|
| A1 | 批准后 10s，交付被注入 BUSY | 再 approve → 409 `CodeApproveInFlight`，无新 `gate_decision` |
| A2 | 同上，等待超过 grace（测试可用可配置短 grace） | 再 approve → 触发一次 `goal:approved` 重发；`gate_decision` 仍只有 1 条；`deliver_retriggered` 活动 1 条 |
| A3 | 重发后连续再点 3 次 | 只有第一次触发交付；其余 409 且无新活动 |
| A4 | 交付成功 | goal `done`；再 approve → `CodeGoalNotInReview` |
| A5 | 交付失败且落 `deliver:` 注记 | approve 直接放行（不等待 grace） |
| A6 | grace 内 reject | 409；grace 后按第 3 层策略返回明确文案 |
| A7 | 重启 daemon 的旧路径 | 仍能恢复（周期恢复上线后，此路径应变为兜底） |

## HEAD 判定汇总

| 结论 | HEAD 状态 | 依据 |
|---|---|---|
| 守卫无时间维度 | 未修 | `goal.go:1959` 查询不取 `decided_at`；`:1967` 条件无时间项 |
| 失败态不可见 | 未修 | `:1946` 只认 `deliver:`；`:3266-3271` 只在 `success=false` 写；`deliver.go:331-332` 吞错 |
| reject 被连带禁止 | 未修 | `goal.go:1975-1977` |
| 无错误码/分态 | 未修 | `:1973`、`:1977` 均为 `NewValidationError(string)` |
| UI 本地态 | 部分缓解 | `goal-actions.tsx:302-310`；刷新/批量栏绕过 |
| 拒绝无指标 | 未修 | `:1972` 仅 `Warnf` |

## 待核

| # | 问题 | 影响 |
|---|---|---|
| T6 | 事故 build 是否已含 `lastRunID == runID` 这条判据？HEAD 注释称它来自一次 live 事故 | 只影响「事故 build 比 HEAD 旧多少」的叙述，不影响缺陷结论 |
| T7 | 事故现场的三次点击是否来自同一页面实例（本地态是否生效） | 决定 P1-3 是「事故 build 无 UI 保护」还是「人绕过了本地态」 |
| T8 | 生产是否有其它客户端（CLI/机器人）直接调用决策 API | 决定「只修 UI」是否足够；服务端守卫必须独立正确 |

## 相关

- BUG-1：[findings-bug1-deliver-sqlite-busy.md](findings-bug1-deliver-sqlite-busy.md)
- 根因与全局视图：[analysis.md](analysis.md) §5
- 事故时间线：[timeline.md](timeline.md) §3