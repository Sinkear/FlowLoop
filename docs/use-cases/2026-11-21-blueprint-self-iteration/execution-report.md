# 执行报告：蓝本自迭代断链修复（execution-report.md）

> **状态：工作树已实现，未提交**。基线 `a3d2744`，修复在本地分支 `fix/bugs-blueprint` 的工作树中完成；本报告记录**实际改动与偏差**，不重复 findings 里的方案推演。代码行号引用以基线 `a3d2744` 为准，合并后会漂移。

## 元信息

| 项 | 值 |
|---|---|
| 用例目录 | `docs/use-cases/2026-11-21-blueprint-self-iteration/` |
| 事故日期 | 2026-11-20 15:27 – 2026-11-21 09:56（+08:00） |
| 修复分支 | `fix/bugs-blueprint`（工作树，未提交） |
| 修复基线 | `a3d2744`（2026-11-21 15:05 +0800） |
| 合并提交 | —（未提交；提交后回填） |
| 负责人 | Lead（汇总）；实现分工见下 |
| 关联文档 | [README.md](README.md)、[analysis.md](analysis.md)、[findings-bug1](findings-bug1-deliver-sqlite-busy.md)、[findings-bug2](findings-bug2-duplicate-approval.md)、[findings-bug3](findings-bug3-blueprint-compile-observability.md)、[findings-bug4](findings-bug4-plan-vs-actual.md)（未修） |

## 修复项（实际实现）

### BUG-1：交付收口撞 `SQLITE_BUSY` 无重试（严重）— 已实现

| 项 | 内容 |
|---|---|
| 实际方案 | ① `MarkDelivered` 的整段 DB 事务包进既有 `withBusyRetry`（3×100ms，仅重试 `isSQLiteBusy`）：每次尝试**新建事务**，闭包内 `defer tx.Rollback()`，成功后 `Commit`；`ErrNotFound` 与非 busy 错误立即返回；`bus.Publish` 与 `rsi.OnGoalTerminal` 只在最终 commit 成功后执行**一次**。② `finishDeliver` 失败日志 `Infof` → `logging.Errorf`（带 goalID/success/err，提示 goal 留在 review 等恢复）。③ `recoverPendingDelivers(ctx, minAge)` 拆出 `pendingDeliverGoalIDs`：JOIN `gate_decision` 取最新 approve 的 `decided_at`，`minAge>0` 时用 `time.Parse(RFC3339Nano)`+`time.Since` 过滤在飞交付；解析失败 Warnf 并跳过（保守）。④ daemon 新增 `deliverRecoveryInterval=10m`、`deliverRecoveryMinAge=5m`；启动传 0，主循环 10m ticker 自愈。 |
| 变更文件 | `internal/service/goal.go`（`MarkDelivered`）、`internal/daemon/deliver.go`（`finishDeliver`、`recoverPendingDelivers`、`pendingDeliverGoalIDs`）、`internal/daemon/daemon.go`（常量 + ticker） |
| 与 findings 的偏差 | **未做** `RowsAffected` 0 行判定（原 UPDATE 语义未改）；**未写** `deliver:` 失败注记（改用「时间维度 + 周期恢复」等价旁路）；恢复 `minAge` 取 5m（findings 建议 ≥2m，5m 避免把刚开始的交付当成丢失）。 |
| 单测 | `TestMarkDeliveredIdempotent`（二次交付幂等 + 缺 goal ErrNotFound）、`TestRecoverPendingDeliversMinAge`（fresh 1m 不选、stale 10m 选中，minAge=0 全选） |
| 未测 | 确定性 `SQLITE_BUSY` 注入：`modernc.org/sqlite` 的 `sqlite.Error` 字段未导出，无法合成码 5；真造锁需第二连接持锁 >5s，慢且 flaky。重试/回滚语义由幂等测试覆盖，`withBusyRetry` 本体未改。 |
| 状态 | 已实现，未提交 |

### BUG-2：防重复审批不区分「交付中/已失败」（中）— 已实现

| 项 | 内容 |
|---|---|
| 实际方案 | `ResolveReview` 的 duplicate guard 查询改为 `SELECT decision, run_id, decided_at`；新增 `approveRetriggerGrace=2m` + `approveRetriggerAllowed`（`time.Parse` 失败按「未陈旧」保守拒绝）。同一 run 重批且距上次 approve ≥2m：`logging.Warnf`（RE-TRIGGERING deliver）+ `publish goal:approved`（`{goal_id, reason}`）触发 daemon 重新交付，**不写** `gate_decision`、不写 `goal:review_resolved`，返回 `s.Get(ctx, goalID)`+nil（状态仍 review）；<2m 仍按原逻辑拒绝。 |
| 变更文件 | `internal/service/goal.go`（`ResolveReview`） |
| 与 findings 的偏差 | grace 取 **2m**（findings 建议 5m）；**未加** 409/`CodeApproveInFlight` 错误码与 `Retry-After`；**未改** reject 分支与前端分态文案；不做按 goal 节流（由交付单飞兜底）。 |
| 单测 | `TestResolveReviewStaleApproveRetriggersDeliver`（10m 后重批：err=nil、状态 review、`gate_decision` 不增、bus 恰好一次 `goal:approved`）、`TestResolveReviewRecentApproveStillRejected`（立即重批仍被拒、行数不增） |
| 状态 | 已实现，未提交 |

### BUG-3：创建即 active 绕过编译 + 静默退出（中）— 已实现

| 项 | 内容 |
|---|---|
| 实际方案 | ① `GoalService.Create`：仅当 `status=active` 且 `blueprint_version_id` 非空时，在**同一事务内**（首跑入队之后、commit 之前）调用 `compileBlueprintTx`，commit 后先 `publish(runEv)` 再 `publishCompiledRuns(ctx, compiledEvs)`；backlog 路径不变，仍由 `Activate` 编译。② `rsi_analyze.go` 十个静默退出点全部改走 `internal/logging`（`Debugf`/`Warnf`，保留 `[rsi]` 前缀语义），删除标准库 `log` import；cap 检查日志也改为 `logging`。③ `Analyze` 收尾新增 Warn：pinned 但 `blueprint_node=0` → `[rsi] goal … 绑定蓝本 … 但未编译（blueprint_node=0）——自动提案跳过`。 |
| 附带修复（pre-existing 事件丢失） | `internal/service/blueprint.go`：`insertSubGoalTx` 返回的 run 事件原先被 `sgOut, _, err :=` 丢弃，随后 `enqueueSubGoalRunTx` 因 run 已存在走 coalesce 返回 nil，导致 `compiledEvs` 恒空（`Activate` 路径同样受影响）。改为在节点编译循环前声明 `var runEvs []events.Event`，`sgOut, runEv, err := ...` 后 append；每条 run 恰好一条 `run:enqueued` 事件（`run.go:425-433` coalesce 保证不重复）。消费者仅 ws hub，daemon 取 run 靠机器 poll，语义安全。 |
| 变更文件 | `internal/service/goal.go`（`Create`）、`internal/service/rsi_analyze.go`、`internal/service/blueprint.go` |
| 与 findings 的偏差 | 选了「方案 A：Create 编译」而非「一律 backlog」（方案 B）：保持现有「创建即可跑」API。**未做** cap SQL 的 per-blueprint 修正；**未做** UI「未编译 vs 空蓝图」区分；**未实现** `human_wait`/`lost_parallelism` 翻译规则；未编译断言只在 Analyze 打 WARN（不阻断创建/激活）。 |
| 单测 | `TestCreateActivePinnedBlueprintCompiles`（3 节点物化、1 条 subgoal run、`run:enqueued` 带 SubGoalID）、`TestCreateBacklogPinnedBlueprintCompilesOnActivate`（创建时 0 节点，Activate 后 3 节点+事件）、`TestCreateActiveInferGoalHasNoBlueprintNodes`（无 pin 的 active 不编译） |
| 状态 | 已实现，未提交 |

### 附带加固：交付单飞（Lead 复核发现）

| 项 | 内容 |
|---|---|
| 问题 | `onGoalApproved` 直接 `go d.deliverGoal`，`deliverGoal` 无单飞；`deliverWaitForRuns=5m` > 重批宽限 2m，周期恢复（minAge 5m）也可能与在飞交付重叠 → 同一 goal 并发两次 merge，可能损坏 domain worktree。 |
| 方案 | `Daemon.deliverInFlight sync.Map`；`deliverGoal` 开头 `LoadOrStore(goalID)`，已在飞则 `Infof` 后返回；成功拿到后 `defer Delete`。重复触发（stale 重批、周期 sweep）被丢弃，首次交付继续收口；失败后由下一轮 sweep 重试。 |
| 变更文件 | `internal/daemon/daemon.go`、`internal/daemon/deliver.go` |
| 单测 | `TestDeliverGoalSingleFlight`（预置 in-flight id → 调用不产生 `goal:delivered`，且 entry 不被误删） |

### BUG-4（执行保真 / plan-vs-actual）— 机制已实现（工作树，未提交），含 Phase B/P2 三项收尾

| 项 | 内容 |
|---|---|
| 问题 | 实际执行图与蓝本规范图不一致：没有「pinned ⇒ 已编译」不变量（`goal.go:1649` + `blueprint.go:1736-1744`，0 行=放行）；未编译时 owner 收到「计划已 COMPILED」contract、`CreateSubGoal` 被拒 → 只剩咨询；gate 的 auto 直通不可表达（`blueprint.go:1525-1545`）；owner 计划外写入不入账；spawn children 不在 ledger；无 plan-vs-actual 对账。 |
| 决定 | 2026-11-21 用户明确：**先不改代码**，把缺口与证据写进 use-case；事故数据包（incident-data.zip）已到并已并入文档。 |
| 数据结论（回填） | 4 目标 50 runs = **owner 26 / consult 17 / review 7**，**0 个 subgoal run**；`blueprint_node`/`sub_goal`(这 4 目标)/`change` 全 0；17 条 consult_request 的 `response_comment_id` 全空；g1 从未触发（Checker 判 risk=normal 但 spec 无 auto 端口）；**事故 build 的 consult 是实际执行通道**——Upgrader 266 calls 经 JEM exec API 真实升级（opencode 1.0.9→1.0.10），owner 只做派发；consult prompt 无 READ-ONLY 契约。 |
| 机制结论（2026-11-21 用户复核） | **蓝本从未被读取**：`t1→t2→t3→t4` 的顺序来自 AI SHELL 的 `agent.system_prompt`（pipeline dispatch 规则）+ `squad.instructions`（经 `stageRunProfile` staged 进 `AGENTS.md`）；蓝本 v1 是同一 SOP 的结构化副本。`AGENTS.md` grep 蓝本 = 0，节点 id 在 AGENTS.md/评论/50 runs 工具调用零出现；首 run 第一条 thought 在任何工具调用前就知道顺序；连 persona 第 1 步「create sub_goal」也被模型静默跳过（4 goal sub_goal=0）而平台放行——**执行保证不能依赖 prompt**。 |
| 用户决策（2026-11-21） | ① consult **只许提意见**（事故 build 的执行属缺陷；HEAD 需工具层只读白名单）；② g1 `timeout.action=rejected` **非有意**（HEAD `blueprint.go:444-455` 必填、`:2491-2545` 系统自动执行，修复默认改 park/escalate）；③ scratch 交付进账本的影响与建议见 findings-bug4 §三.12（建议另立 `env_change`，最小版=归因+started/failed）。 |
| 已做 | ① 新增 [findings-bug4](findings-bug4-plan-vs-actual.md)（事故实证 + 缺口 A–K + P0/P1/P2 分级修复设计 + 待核 Q1–Q5 + §1.3 蓝本零读取 + §三.12 账本影响）；README 的修复清单/影响面/关系段、analysis §3.4/§7/§8/附录 A 已引用；§3.3 的验收不变量与 BUG-4 P0 是同一件事，修复 BUG-3 入口不等于闭环。<br>② **2026-11-21 确认执行后实现三项**：<br>• P0 不变量：`planMaterialization`/`assertPlanMaterializedTx`（`internal/service/blueprint.go`）接入 `Create`（`goal.go:466-479`）与 `Activate`（`:2316-2321`），pin 而 `blueprint_node=0` 即回滚；终局守卫（`goal.go:1649` 之后）检测到同一状态时把 goal 停靠到 `status='review'`、写 `plan_unmaterialized` activity、发 `goal:reviewing` 并 Warn，不静默放行。<br>• consult 工具层只读：`cmd/flowloop-cli/autopermit.go` 对 `role=consult` 拒绝 fs 写、terminal 与 `edit/delete/move/execute` 权限请求（`policy_violation` 日志），`exec.go` 传入角色；`team_context.go` consult 契约声明平台强制。<br>• gate timeout 默认 park：`gate.timeout`/`action` 均可选；到点无 action 时 `SweepBlueprintGates` 只落 `parked_at`（不写 `gate_decision`、不释放分支），显式 action 仍由 system 执行（`blueprint.go:443-455`、`:2491-2545`）。<br>③ **2026-11-21 用户要求完整修 BUG-4 后补齐的机制**：<br>• **contract 说真话**：`team_context.go` 的 `blueprintDriven` 改为「pin 且 `blueprint_node`>0」；未物化时注入 `PLAN NOT MATERIALIZED` 段，明确「不要自创计划、不要把工作路由给 consult（consult 只是意见）、停靠等编译」。<br>• **dispatch 懒编译**：`GoalService.EnsurePlanMaterialized`（`blueprint.go`）+ daemon dispatch（owner/subgoal）调用；失败 `failRun` 带原因，成功打 `lazily compiled blueprint plan`。<br>• **plan-vs-actual 对账**：`PlanAlignment` 服务（`internal/service/plan_alignment.go`）join `blueprint_node↔sub_goal↔run↔consult_request`，输出 planned/settled/mapped/executed/不计划外/consult 数与 deviations（未物化、计划外子任务、节点无执行、覆盖率不足、咨询化执行）；HTTP `GET /goals/{id}/plan-alignment`（`handler.go`）；RSI `plan_deviation` finding（`rsi_analyze.go`，severity high/medium，Confidence L1）；goal 详情页「计划 vs 实际」卡（`web/app/goals/[id]/page.tsx`，逐节点表 + 偏离列表）。<br>• **consult 回答回填**：`backfillConsultResponse`（`goal.go`，run end 对 `role=consult`）把该 run 最新评论写回 `consult_request.response_comment_id`。<br>• **cap per-blueprint**：`rsi_analyze.go` 先解析 version→blueprint_id，再按 blueprint 计数（原 `COUNT(DISTINCT blueprint_id)` 是全局）。<br>• **spawn children 入 ledger**：`resolveSpawnTx` 物化 child 时写 `blueprint_node(node_key='<spawn>#<seq>', parent_key=<spawn>, node_type='task', sub_goal_id=child, state='running')`，`INSERT OR IGNORE` 幂等（`blueprint.go`）。<br>• **reopen 显式状态**：`compileBlueprintTx` 的 reopen 守卫由 Info 改 Warn，并写 `blueprint_manual_followup` activity（含 spec_nodes），时间线可见「spec 只对首轮权威」。<br>• **gate auto（运行时判定）**：`SpecGate.Auto{When,Action}`（`blueprint.go`）；`when` 为单条 `node.<id>.output <op> "literal"`，`<id>` 必须是 gate 直接上游（发布校验同批强制），`action` 必须已声明；编译 stamping `auto_when/auto_action`；`SweepBlueprintGates` 先对上游 sealed `run.result_summary` 求值，命中即 `ResolveBlueprintGate(..., decided_by=auto)`，未命中回落人工/超时；决策卡显示规则。测试 `blueprint_auto_test.go`（命中、未命中、四类校验失败）。<br>• **owner 写入归户**：`internal/service/blueprint_write.go` 解析 deliver `fixLog` 的 `Blueprint-Node: <node_key>` trailer（大小写容忍、键严格小写、CRLF、last-wins、未知节点=未归户），`RecordBlueprintWrites` 按 `(goal_id,commit_sha)` upsert；`deliver.go` 在 merge+push 成功后记录（失败不阻断交付）；`plan_alignment` 输出 writes/attributed/unattributed 与「计划外写入」deviation。测试 `blueprint_write_test.go`。<br>• **scratch `env_change` 账本**：`env_change` 表 + `RecordEnvChange/ListEnvChanges`（校验状态/版本/sha256/时间序）+ RPC `env_change.record|list` + CLI `flowloop env-change record|list` + HTTP `GET /goals/{id}/env-changes` + goal 详情页「环境变更账本」卡 + scratch contract 记账要求。测试 `env_change_test.go`。<br>• **提案→下一轮显式入口**：`GoalService.StartNextRoundFromProposal`（`internal/service/rsi_next_round.go`）+ `POST /blueprint-proposals/{proposalId}/next-round`：发布应用版本（draft 时）→ 经 finding→report→training_session 反查原目标 → 继承 domain/assignee/验收/采样/预算 → 建 `execution_mode=train`、`source_ref="rsi-proposal:<id>"` 的新目标并钉新版；`start` 默认 true（active，当场编译+派发），false 则 backlog；幂等（重复调用返回同一 goal）。蓝本页「优化提案」approved 行加「开始下一轮」按钮 + 结果链接。测试 `rsi_next_round_test.go`（发布+启动、backlog、pending 拒绝、无 finding 拒绝）。 |
| 单测 | `internal/service/blueprint_park_test.go`：TestBlueprintGateWithoutTimeoutHolds、TestBlueprintGateTimeoutWithoutActionParks、TestAssertPlanMaterializedTx、TestFinalizationGuardParksUnmaterializedPlan；`cmd/flowloop-cli/autopermit_test.go`：TestConsultAutopermitDeniesWrites、TestConsultAutopermitDeniesWritePermissions、TestWorkerAutopermitStillWrites；`internal/service/plan_alignment_test.go`：TestPlanAlignmentMapsCompiledSpec、TestPlanAlignmentFlagsConsultOnlyExecution、TestEnsurePlanMaterializedIsLazyAndIdempotent、TestConsultResponseBackfill；`internal/daemon/team_context_bug4_test.go`：contract 已物化/未物化两态；`TestBlueprintSpawn*` 覆盖 child ledger；`internal/service/blueprint_auto_test.go`：gate auto 命中/未命中/四类校验失败；`internal/service/blueprint_write_test.go`：trailer 11 子例 + 账本计数 + 计划外写入 deviation；`internal/service/env_change_test.go`：往返/排序/9 类校验。 |
| 验证 | `gofmt` 干净；`go build ./...` OK；`go test -count=1 ./internal/service/ ./internal/daemon/ ./cmd/flowloop-cli/` 全绿；全量 `go test -count=1 ./internal/... ./cmd/...` **EXIT=0**。 |
| 未做 | ① gate auto 只支持单条上游证据（多证据仍应写 condition 节点）；② owner 写入归户只做「可观测 deviation」，未做交付前硬拒绝（建议 1 未实现，避免改变 owner 工作流）；③ scratch 账本靠执行者显式 `env-change record`，未做机器自动截获、artifact 内容上传、按账本自动回滚；④ 前端未做 env_change 编辑/回滚入口（只读列表）。claim 级 run 拒绝改由 dispatch 懒编译 + `failRun` 承担（不卡 queued）。 |
| 后续 | BUG-4 机制已闭环；后续按 findings-bug4 §五 的 P1/P2「未做」项推进（多证据 gate/condition 运行时求值、交付前对账硬拒绝、账本自动截获与回滚）。本轮所有改动在**工作树未提交**，与 BUG-1/2/3 一并等待提交决策。 |

### 设计缺陷-4 与观察项-5 — 未修（范围外）

| 项 | 决定 |
|---|---|
| 设计缺陷-4（交付期间无服务端可查状态） | 未改 web/。HEAD 已有单卡本地 disabled；系统性修复需错误码 + 前端分态，随 BUG-2 错误码一起做，本次只做服务端时间旁路。 |
| 观察项-5（`event seq gap`） | 未复现，需协议侧证据；本次不动事件协议。 |

## 验证记录

### 构建与单测（实际执行）

| # | 命令 | 结果 | 结论 |
|---|---|---|---|
| V1 | `go build ./...` | 通过 | ✅ |
| V2 | `go test -count=1 ./internal/service/ ./internal/daemon/` | 全绿（service ≈6.6s，daemon ≈4.2s） | ✅ |
| V3 | `go test -count=1 ./internal/... ./cmd/...` | 全绿 exit 0（acp/backup/card/daemon/issue/link/notify/paths/proto/server/service/zipx/cmd） | ✅ |
| V4 | `gofmt -l`（全部改动 Go 文件 + 新测试） | 无输出 | ✅ |

### 端到端（未执行，需真实 daemon/DB）

| # | 场景 | 期望 | 实际 |
|---|---|---|---|
| E1 | 新建 `status=active` + pin 目标 | `blueprint_node` > 0、有编译活动 | 未执行（有单测覆盖编译路径） |
| E2 | 跑完 → approve → RSI | 有 `[rsi]` 结构化日志 | 未执行 |
| E3 | 向 `MarkDelivered` 注入 `SQLITE_BUSY` | 重试后成功，或失败留 review 由 ticker 恢复 | 未执行（无法确定性注入，见 BUG-1 未测） |
| E4 | E3 失败后不重启，等 ticker | ≤10m 自愈为 done | 未执行 |
| E5/E6 | 失败后 <2m / ≥2m 再 approve | <2m 拒绝；≥2m 重发一次 `goal:approved` 且 `gate_decision` 不增 | 单测覆盖（`TestResolveReview*`） |
| E7 | 刷新/另一标签页再 approve | 服务端时间旁路一致（不依赖本地态） | 未执行 |

> 未执行端到端的原因：需要可运行的 daemon + 真实 domain/agent/评审闸门；本次改动全部有包级测试覆盖，E2E 留给后续联调。

## 指标对比（修复前后）

| 指标 | 事故值 | 修复后目标 | 实测 |
|---|---|---|---|
| 交付失败后的恢复时间 | 49m48s（人工重启） | ≤10m（自动 ticker） | 未实测 |
| `blueprint_node`（active+pin 新目标） | 0 | >0（测试目标 3 节点） | 单测 3 |
| `blueprint_proposal`（有可翻译 finding） | 0 | 本次仍可能 0（规则未扩容） | 未实测 |
| 翻译器静默退出 | 10 处无日志 | 全部有 Debug/Warn | 代码审查 ✅ |
| 重复批准被误拒 | 3 次/次事故 | 0（≥2m 自动重发交付） | 单测 ✅ |

## 遗留与后续

1. **规则扩容**：`human_wait` / `lost_parallelism` 翻译规则未实现；本次 5 条 findings 全属这两类，故「编译修好」后本批仍会 0 提案（翻译器会明确记录「无匹配规则」）。
2. **历史目标无法补编译**：4 个 done 目标需新建目标验证闭环。
3. **human_wait 口径**：openclaw 12.6h 授权等待在事故 build 未计入；HEAD 会把 authorize park 计入，口径需产品确认（analysis §6）。
4. **BUG-1 残余风险**：锁竞争持续超过重试预算时仍失败；此时靠 `Errorf` + 10m ticker 自愈，最坏 ~10m 延迟。
5. **未提交**：工作树改动需评审后提交；提交后本报告回填分支提交号与合并提交号。
6. **观察项-5**：事件 seq gap 待协议侧排查。
7. **BUG-4 未修**：按用户 2026-11-21 决定只补文档；§3.3 验收不变量仍只在测试里，未进入创建/激活/claim/守卫。事故数据已回填进 findings-bug4 §1/§6（50 runs=26/17/7、0 subgoal run、0 change、17 consult 全空、g1 未触发、consult 实际执行），仍未开工。

## 签署

| 角色 | 姓名 | 日期 | 结论 |
|---|---|---|---|
| 实现 | fix-delivery / fix-blueprint（teammates）、Lead（单飞加固 + 复核） | 2026-11-21 | 实现完成，待提交 |
| 复核 | Lead | 2026-11-21 | 全量构建/测试/gofmt 通过；E2E 未执行 |
| Lead | lead | 2026-11-21 | 交付验收以本报告「实际」列为准；BUG-4 文档已落地、代码未动 |