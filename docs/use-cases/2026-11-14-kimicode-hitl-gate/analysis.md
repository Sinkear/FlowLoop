# 案例分析：kimicode 高风险升级的人工授权闸门（F1/F2/F3/R3）

> 分析对象：2026-11-14 18:26「升级 kimicode」HITL 任务（category=hitl），事件时间线见 §1。
> 代码基线：`/home/sinkear/developer/flowloop`（修复前 HEAD `a843af0`）。
> 配套：`findings-review-semantics.md`（review 状态机逐项分析 F1/F2/F3/R3 + C1）。
> 本文是结论与数据指标；修复对照见 §6。

---

## 0. 结论先行

这次事件**不是**调度浪费问题（那是上一个 case 的主题），而是**语义问题**：平台把「中途人工授权闸门」塞进了唯一的一个 review 状态里，于是：

- **批准按钮说谎**：F1——授权场景下「批准」= 接受未完成的现状、关单、（有 diff 时）合入。人想要的「批准 = 放行继续」不存在。
- **驳回按钮碰运气正确**：F1 倒置的另一半——人点「驳回」的本意可能是「别批」，平台却把 goal 放回 active 继续，**碰巧**是正确的，但理由评论会被下一轮 owner 当成「返工意见」而不是「别这么干」。
- **闸门不带说明书**：F3——提审时 `review_request` 为空（scratch/未冻结/弱验证 park 的设计选择），审批卡/面板只有证据包，没有「审什么、批准意味着什么」。
- **审查员审查了一个不存在的交付物**：R3——授权 park 也拉 reviewer run，简报硬编码「inspect the diff」，于是 Verifier 用 3 分钟产出一条误导性 REJECT「升级未执行」——**未执行正是闸门的目的**。
- **目标永不暂停**：F2——平台没有「流水线中途等待人工授权」的状态载体，wrapup 推导只数 sub_goal/change 行，1 个 verified 就推导收尾，于是 2.6.2 的升级闸门停在了「Checker 阶段完成、未派发 Upgrader」的半成品状态上。

一句话：**审批面板的两个按钮里，只有一个（驳回）能用，而且用对了也是歪打正着。**

正面确认（昨天的修复没有回退）：verify verdict 正常落库（B1 未复发）；双 mention 被 `dedupe_consult` 消化（S1 防护生效）。

---

## 1. 事件时间线与数据指标

### 1.1 时间线（2026-11-14，墙钟）

| # | 时刻 | 时长 | 主体 | 动作 |
|---|---|---|---|---|
| ① | 18:26:35 | — | human | 评论派单：升级 kimicode（category=hitl） |
| ② | 18:26:35 起 | 125s | AI SHELL（leader） | 解析意图，判定先走 Checker 检查阶段（双 mention 被 dedupe_consult 去重，S1 防护生效，只起一条 run） |
| ③ | ~18:28 起 | 76s | Checker | 三处取证版本 0.39.1 一致；2.6.2 为 dist-tags.latest；risk=high、major_bump=true → 按规则停在人工闸门，**未派发 Upgrader**（正确行为） |
| ④ | Checker 完成后 | 98s | 平台 verify run | 裁决 verdict 落库（B1 未复发），sub_goal → verified |
| ⑤ | 18:31:24 | 25s | AI SHELL（wrapup 中继） | reconcile 推导 wrapup（只看到 1 个 verified sub_goal）→ leader 被唤醒收尾 → goal 进 review，**review_request 为空** |
| ⑥ | 18:35 起 | ~3min | Verifier（review run） | 验收框（「inspect the diff」）套在授权闸门上 → 产出 REJECT「升级未执行：Upgrader/Verifier/Packager 均未派发」 |
| ⑦ | 18:35 后 | ≥数分钟 | human | 面对无说明书的审批卡决策 |

### 1.2 数据指标（过闸成本）

**当前（修复前）的人工授权过闸成本：**

| 指标 | 值 | 说明 |
|---|---|---|
| 多余 LLM run | **2 次**（R2 wrapup 中继 25s + R3 审查 run ~3min） | 目标设计为 0 |
| 误导性审查意见 | **1 条** | R3 的 REJECT「升级未执行」——未执行正是闸门目的 |
| 闸门语义可用按钮 | **0/2** | 批准 = 关单（错）；驳回 = 继续（歪打正着）；「批准并继续」不存在 |
| 审批卡携带说明 | **0 字节** | review_request 为空（scratch 分支 park），卡上只有证据包 |
| leader 判段 → 人可决策 | **~5min**（18:26:35 → 18:31:24 park） | 其中 125s 领队 + 76s Checker + 98s verify 是必要工作；25s wrapup 是中继税 |
| park → 审查意见落地 | **~4min** | review 窗口默认流程 |

**各 run 的必要性判定：**

| Run | 时长 | 判定 | 理由 |
|---|---|---|---|
| leader 解析（125s） | 必要 | 判段 + 派发 Checker 是 leader 的职责 |
| Checker（76s） | 必要 | 检查产物存在且结论正确（0.39.1→2.6.2 高风险） |
| verify（98s） | 必要 | 工人不自证，平台契约内（B1 修复生效） |
| wrapup 中继（25s） | **被迫必要**（设计上纯浪费） | 授权原语缺失：goal 只能在 owner run 结束时 park，而 park 需要一次 run 完成——于是平台专门唤醒 leader 一次只为让它「结束」 |
| 审查 run（~3min） | **负资产** | 错帧产出误导意见；授权闸门没有 diff 可审 |

**修复后（`goal authorize`）的目标成本**：0 次多余 run、0 条误导意见、闸门卡自带说明书（statement）、批准 = 继续、驳回 = 带意见打回。

### 1.3 讽刺闭环（事件的实际走向）

R3 的错帧 REJECT × F1 的语义倒置，构成了一个讽刺闭环：

- 人若信 Verifier 的意见点**驳回** → goal 回 active、owner 带着意见重跑 → **碰运气正确**（闸门确实应该继续等），但意见内容（「升级未执行」）与场景（「请授权」）完全错位。
- 人若点**批准** → goal 关单（零提交分支 "approved as-is"，或半成品合入），卡面却说「平台正在自动合入」→ **2.6.2 升级永远不会发生**，且面板显示的动作与实际动作不符。

---

## 2. 根因：一个状态承载两种语义（F2）

平台的 goal 状态机：`backlog | active | done | failed | review | cancelled`。「review」是唯一的暂停-等人-决策状态，但它被四条路径共用：

| 进 review 的路径 | 真实语义 | approve 的实际后果 |
|---|---|---|
| gate park（`gatesForGoal` 命中 / scratch / 未冻结 / 弱验证） | **验收**：机器已尽力，人做最后裁决 | deliver：merge+复验+push → done |
| `SubmitForReview`（owner 显式提审） | **验收**：收尾确认 | 同上（有零提交分支） |
| `handoff_loop`（≥8 次交接） | **治理**：所有权循环，人裁决 | release 回 active（继续）——**唯一证明状态机有能力表达「批准并继续」** |
| verify_no_verdict（verify 无裁决） | **补救**：缺裁决，人补判 | 验收框 |
| **（缺失）中途人工授权** | **授权**：下一步危险，人放行 | **应为：release 回 active + 唤醒 owner 继续** |

「中途授权」没有自己的行，于是被塞进「验收」的行——**F1 的批准=关单语义就来自这里**。wrapup 推导（`deriveOwnerAttentionTx`）只看 sub_goal/change 表行的状态，没有「计划还有哪些阶段未派发」的概念（F2）——1 个 verified sub_goal 就足以推导「最终态到了」，把一个 4 阶段流水线的第 1 阶段完成态当成最终态。

## 3. F3：闸门不带说明书

三个 park 分支（scratch 域 / 未冻结 policy / 弱验证 strength）的 `reason=""` 是**设计选择**（代码注释：「平台术语不给用户看，审批面板自会说明」），但「审批面板自会说明」在授权场景下落空：

- 审批卡（`buildReviewCard`）：reason 为空 → 不渲染卡点行 → 卡上只有标题+证据包。
- Web ReviewPanel：`review_request` 为空 → 不显示卡点行。
- 文案兜底（notify fallback）：「（批准后平台自动合入）」——**对授权场景是谎言**。

审核人看到的是：一个 Checker 阶段的 verify 证据 + 「批准后自动合入」，没有任何「这是升级前的授权闸门、批的是 2.6.2 高风险升级、批准后 leader 将派发 Upgrader」的信息。**F3 与 F1 复合**：即使人理解了场景，也没有按钮能表达正确意图。

## 4. R3：审查员审了个寂寞

授权 park 拉起 reviewer run（`enqueueSquadReviewTx` 只跳过 `handoff_loop:`），简报硬编码「Review the goal's outcome — inspect the diff and the feed」。授权闸门上：

- diff 是空的（闸门目的就是**还没动**）；
- 审查简报引导 reviewer 去检查「成果」；
- reviewer 的「负责任」表现 = 报告「升级未执行」并 REJECT。

这不是 reviewer 的错——是**简报把验收框套在授权闸门上**。`verify_no_verdict` park 同样会踩这个坑（有成果可审但简报引导看 diff）。

## 5. 为什么 goal 不能直接 park（R2 中继税的机制）

平台的不变量：**没有 review 与 live owner run 共存**（人若在 run 进行中批准，run 自己的完成 reconcile 会立刻把刚激活的 goal 再 park/完成一次）。所以 park 必须发生在 run 结束的 reconcile 里。但「agent 发起授权」被决策 4-11 移除后（`goal.go:1551` 注释 "agents cannot request approval"），agent 唯一的停人手段是评论 `--ask`（goal 留 active，人答复后唤醒 owner 继续）——语义正确但没有审批卡、没有状态呈现，且 owner 会被 wrapup 推导再次唤醒（R2）：因为没有任何「我在等人」的状态载体，reconcile 看到无 in-flight、有 1 个 verified sub_goal，就推导 wrapup。

于是修复前的「停人」路径必须付 25s 的中继税：平台唤醒 leader → leader 无事可做只能结束 → 结束触发的 reconcile 才让 goal 停下来。

## 6. 修复对照（本次提交）

新增 authorize 原语（`goal authorize --statement S`，RPC `goal.authorize`）：

| 缺陷 | 修复 | 位置 |
|---|---|---|
| **F1** 批准=关单 | authorize park 的 approve = release 回 active + 同事务 enqueue owner successor（wake note = "The human approved … continue"）；reject = 普通打回路径（带意见+human_iterations++）；duplicate-decision guard 豁免 authorize park（无 deliver 异步窗口） | `internal/service/goal.go` `ResolveReview` authorizePark 特判 |
| **F2** 无中途暂停载体 | `RequestAuthorization`：owner run 内 STAGE `review_request="authorize: <人话陈述>"`（goal 留 active）；run 结束的 reconcile 在 finalization guard **之前**消费 staged 请求 park（pending sub_goal 是闸门的正常伴随态，不是阻碍） | `goal.go` `RequestAuthorization` + `reconcileOnRunEndOnce` authorize 预占分支 |
| **F3** 闸门无说明书 | statement 落 feed（agent 评论）、审批卡卡点行（trimPrefix 后的人话）、Web 面板、`authorization_requested` activity；fallback 文案按 park 类型分支 | `goal.go` RequestAuthorization、`internal/notify/card.go`、`notify.go`、`web/components/goal-actions.tsx` |
| **R3** 审查员错帧 | `enqueueSquadReviewTx` 跳过 `authorize:` park（无 diff 可审）；审查简报按 `review_request` 前缀分支（verify_no_verdict 有专门文案，授权 park 不再拉 reviewer） | `goal.go` `enqueueSquadReviewTx`、`internal/daemon/daemon.go` assemblePrompt |
| **R2** wrapup 中继税 | owner 契约与 leader 简报写明 authorize 用法（「派发前高风险步骤 → `goal authorize`」），goal 不再需要为了「停下」而被空唤醒一次 | `internal/daemon/team_context.go` roleContract、`internal/service/squad.go` MID-PIPELINE HUMAN GATE |
| **C1**（连带）带 pending sub_goal 可提审 | `SubmitForReview` 拒绝 pending sub_goal（守住 `MarkDelivered` 的 "Pending sub-goals cannot exist here" 断言），错误信息指路 authorize | `goal.go` `SubmitForReview` |
| **UI 语义**（F1 的呈现面） | 卡/面板按钮 label 按 park 类型切换（「批准并继续」vs「批准并自动合入」）、批准后的文案/toast/processed card 均按 authorizePark 分支（「agent 将继续执行下一步」，绝不提合入） | `card.go`、`connector.go`、`notify.go`、`goal-actions.tsx` |

回归测试：`internal/service/goal_authorize_test.go`（8 个用例：approve=release+successor、reject=打回、reviewer 跳过、pending sub_goal 存活、死 run 拒绝、非 owner 拒绝、提审守卫、双击幂等）。

## 7. 遗留与后续

- **F2 的完整解**（计划态载体：每阶段带 `gate=human_required` 位、park 为确定性迁移、wrapup 便签从处方式改陈述式）是中期项——authorize 原语落地后，阶段间隙的暂停已可表达，计划态载体是把「闸门位」从 agent 的判断升级为计划的声明。
- `comment --ask` 仍是无状态问问题的正确通道（goal 留 active），authorize 是「停、等放行」——两者并存，契约里已写明分工。
- 审查简报的验收文案对普通 gate park 保持不变（那里有 diff 可审）；`verify_no_verdict` park 得到了专门的「按 feed 证据裁决该工作项，而不是报告 diff 缺失」简报。
- hootl goal 若 agent 主动 authorize，会得到人审（预占分支先于 hootl auto-complete）——显式的人工请求不应被 auto 跳过，这是有意的。
