# Review 状态机逐项分析（findings-review-semantics）

> 范围：goal 的 review 状态机——`internal/service/goal.go`（park 路径 / ResolveReview / reconcile / enqueueSquadReviewTx）、`internal/daemon/daemon.go`（审查简报）、`internal/notify/*`（审批卡）、`web/components/goal-actions.tsx`（审批面板）、`cmd/ouroflow-cli/main.go`（agent CLI）。
> 缺陷编号沿用事故报告：F1（批准/驳回语义倒置）、F2（一态两用、无授权载体）、F3（review_request 为空）、R2（wrapup 中继税）、R3（审查错帧）、C1（连带：带 pending sub_goal 可提审）。
> 本文为修复前的逐项分析；修复对照见同目录 `analysis.md` §6。

---

## 一、一页结论（按影响排序）

1. **F1（critical）｜approve 语义被验收独占，「批准并继续」不存在。** `ResolveReview` 的 approve 对普通 park（gate/submit/verify_no_verdict）一律走 deliver：merge+复验+push → `MarkDelivered` 关 done；scratch 域直接关单；零提交分支「approved as-is」也关单。授权场景（小队流水线中段）点批准 = 接受半成品关单。驳回反而是 goal 回 active + `human_iterations++` + 同事务 enqueue owner successor——**语义碰巧是「继续」**。状态机其实有能力表达「批准并继续」：`handoff_loop` park 的 approve 就是 release 回 active——但这个分支没有暴露给普通 gate park，UI/IM 按钮也没有第三选项。
2. **F2（critical）｜review 一态两用，且 wrapup 推导不感知剩余阶段。** 四条进 review 的路径（gate park / SubmitForReview / handoff_loop / verify_no_verdict）共用一个状态、一个审批面板、一套按钮文案；「授权下一步」没有自己的行。`deriveOwnerAttentionTx` 的 wrapup 位只看 sub_goal/change 表行状态（verified sub_goal 存在且无 ready change 且 run finished_at > 最近 spawn；或 10 分钟 idle 兜底）——4 阶段流水线跑完第 1 阶段就推导 wrapup，leader 被唤醒收尾，把「等授权」变成「提审验收」。
3. **F3（major）｜三个 park 分支的 review_request 故意为空。** `gatesForGoal` 对 scratch 域 / 未冻结 policy / 弱验证 strength 返回 `(true, "")`，注释明示「No review_request text — the approval panel itself communicates」——设计动机是不给用户看平台术语，副作用是授权闸门的卡/面板/兜底文案全部不知道「批的是什么、批准意味着什么」。兜底文案「（批准后平台自动合入）」对所有 park 类型统一播报，对授权场景是谎言。`SubmitForReview` 有缺省 reason「submitted for review」（英文、信息量低）。
4. **R3（major）｜审查简报硬编码验收框。** `daemon.go` assemblePrompt 的 reviewRun 分支对所有 park 类型统一「Review the goal's outcome — inspect the diff and the feed」；`enqueueSquadReviewTx` 只跳过 `handoff_loop:` 前缀。授权 park（无 diff）与 verify_no_verdict park（成果在 feed）都拿验收框 → 本案例 Verifier 用 ~3min 产出 REJECT「升级未执行」。
5. **R2（minor）｜「停下等人」必须付一次空跑。** 不变量「no review with live owner run」要求 park 只能发生在 run 结束的 reconcile 里；而 agent 发起审批的能力在决策 4-11 被移除（`goal.go:1551` 注释 "agents cannot request approval"）。于是修复前的停人路径 = wrapup 推导唤醒 leader（25s 空跑）→ run 结束 → reconcile park。这条 25s 的 run 产出≈0，是授权原语缺失的中继税。
6. **C1（连带）｜`SubmitForReview` 不检查 sub_goal，打破 `MarkDelivered` 的不变量。** MarkDelivered 的注释断言 "Pending sub-goals cannot exist here"，但 SubmitForReview 只查 live run。带 pending sub_goal 提审 → approve → deliver 关单 → sub_goal 行永远悬挂在非终态。

---

## 二、逐项分析

### F1 批准/驳回语义倒置 ｜ critical

**approve 的实际后果链**（`goal.go` ResolveReview → `internal/daemon/deliver.go`）：

```
approve ──(普通 park)──> publish goal:approved ──> daemon deliverGoal
  ├─ scratch 域 ──────────────> 直接关单（按汇报验收）
  ├─ 分支缺失 ────────────────> 报错留 review（可重试）
  ├─ branchHead==base ────────> 「no changes to deliver — approved as-is」关单
  └─ 有 diff ─────────────────> merge+复验+push → MarkDelivered → done
```

授权场景命中第三行：Checker 阶段无 diff，批准 = 「按现状验收」关单，升级永远不会执行。

**reject 的实际后果**（goal.go）：`UPDATE status='active', human_iterations+1, review_request=''` + 同事务 `enqueueOwnerIntentTx(..., "active", ..., decisionCommentID, "active")`——goal 回 active，owner 带着驳回意见重跑。**这就是「继续」**——倒置的另一半：人点驳回表达「不批这个升级」，平台却把流水线放行。

**唯一的反例证明修复可行**：`handoff_loop` park 的 approve = `UPDATE status='active', review_request=''`（release）——状态机有这个分支，只是没有接到普通 park 上，也没有按钮。

**根因**：平台没有「授权」原语。「review」被设计为「验收关单」单语义槽位（决策 7-1：机器已尽力、人做最后裁决），中途授权被塞进验收的行。

### F2 一态两用、wrapup 不感知剩余阶段 ｜ critical

- 进 review 四路径 + 各自 reason 形态：gate park（gate 名 / 空）、SubmitForReview（「submitted for review」）、handoff_loop（「handoff_loop: …」）、verify_no_verdict（「verify_no_verdict: <title>」）。前缀（handoff_loop:、verify_no_verdict:）是 service 层的内部路由键——UI 无感。
- wrapup 推导：`deriveOwnerAttentionTx` 只数行；idle 兜底 `wrapupIdleAfter=10m`。无「计划/阶段」实体（sub_goal 是执行中的行，阶段间隙没有载体）。
- 事件走向：Checker verified + 无 ready change → wrapup 位 → leader 简报 TERMINAL RULE「最终 sub_goal verified 且无 ready change → submit-review 或交 human」→ leader 只能提审或收尾——**两条路都是验收语义**，没有「我在等授权」可说。

### F3 review_request 为空 ｜ major

- `gatesForGoal` 三处 `(true, "")`：scratch / compiledAt=="" / strength=="weak"。
- 消费端：审批卡（`buildReviewCard`：Reason 非空才渲染卡点行）、Web ReviewPanel（`goal.review_request` 条件渲染）、notify fallback（无 reason 也播报「批准后平台自动合入」）。
- 设计意图（注释原文）：「the approval panel itself communicates」——把解释权交给 UI，但 UI 没有拿到任何可解释的材料。F3 与 F1 复合：说明书缺失 + 按钮语义错误 = 审核人无从判断后果。

### R3 审查错帧 ｜ major

- `enqueueSquadReviewTx`（goal.go:2463+）：`handoff_loop:` 前缀 return nil（无 diff 可审）——**说明平台已经知道「有些 park 没有 diff」**，只是授权/verify_no_verdict 不在跳过表里。
- `daemon.go` reviewRun 分支的 wakeContent 硬编码「Review the goal's outcome — inspect the diff and the feed.」——对所有 park 类型一视同仁。
- 本案例后果：Verifier 的 REJECT 意见被审批卡置顶展示（审查意见区），直接误导人的决策。

### R2 wrapup 中继税 ｜ minor

- 机制链条：wrapup 推导唤醒 leader（25s）→ leader 无事可做、只能结束（其终结动作被 goal.go 的 gate 分支消费）→ reconcile park。若无这次唤醒，goal 会停在 active 且无人再动——直到 idle 兜底（10 分钟）后 idleWrapupTx 再拉一次。
- `comment --ask` 通道（决策 7-3）语义正确（goal 留 active、人答复唤醒 owner），但它是「问问题」，不是「停、等放行」：没有审批卡、没有状态呈现、人答复后 owner 的继续仍是默认行为（ask 的答复只是内容，不是放行动作）。

### C1 SubmitForReview 缺 sub_goal 检查 ｜ 连带

- `SubmitForReview` 拒绝条件只有：非 active / 有 queued|running run。带 pending sub_goal 的 goal 可提审。
- `MarkDelivered` 成功分支不触碰 sub_goal 行（只有 done+清 handoff_note/review_request+取消 queued）→ pending 行悬挂。
- 与 F2 同根：提审（验收语义）被用作流水线中的「叫人」手段时，一切约束（只有终态可验收）都不存在。

---

## 三、修复设计（与实现对照）

**原语选择**：不复用 handoff_loop 的内部前缀做 API（那是治理循环的治理语义），新增显式的 authorize 通道；`review_request` 复用为「park 类型 + 人话」的载体（`authorize: ` 前缀 = service 路由键，后缀 = statement = 卡点行/F3 说明书）。

**park 时机**（与 R2 的机制约束一致）：`RequestAuthorization` 在 run 仍 running 时 STAGE（goal 留 active，`review_request='authorize: …'`）；run 结束的 reconcile 在 finalization guard 之前消费 staged 请求 park——预占绕过 guard（pending sub_goal 是闸门的正常伴随态）与 hootl auto-complete（显式的人工请求不应被 auto 跳过）。

**决策语义**（ResolveReview authorizePark 特判）：

- approve = `UPDATE status='active', review_request=''` + 同事务 enqueue owner successor（wake note = "The human approved the authorization request — continue with the next step of the plan."）——照抄普通 reject 的 P0-2 事务形状。
- reject/redirect = 不特判，直接走普通 reject 路径（回 active + human_iterations++ + 带 opinion 的 successor）——「驳回 = 打回重规划」在授权语境下语义正确。
- duplicate-decision guard 豁免 authorizePark（无 deliver 异步窗口；二次点击落「goal is no longer in review」）。

**F3 面**：statement 落 feed（agent 评论，人话）+ 卡点行（trimPrefix）+ `authorization_requested` activity；notify fallback 与按钮 label/processed card/toast 全部按 authorizePark 分支，授权场景绝口不提合入。

**R3 面**：`enqueueSquadReviewTx` 跳过表加 `authorize:`；审查简报按 `review_request` 前缀分支（verify_no_verdict 得到「按 feed 证据裁决该工作项」专门文案；授权 park 不再有 reviewer run，简报分支自然到不了）。

**测试**：`internal/service/goal_authorize_test.go`，8 用例覆盖 approve=release、reject=打回、reviewer 跳过、guard 预占、死 run/非 owner 拒绝、提审守卫、双击幂等。
