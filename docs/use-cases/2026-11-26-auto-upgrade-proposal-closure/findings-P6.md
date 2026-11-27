# Findings P6：goal 终态漏水 + failed 下游无自愈

> 本文件是 P6 的问题档案：问题复述、证据（[facts.md](facts.md) §P6）、根因、影响、修复方案与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.7、§1）

附件 §6.7/§1 主张（P6 状态机两处缺陷）：

- **agent-browser 案例（终态漏水）**：goal 已置 `done`，但 `sub_goal` 仍 `running`、`verification_result` 0 条——终态与账本不符，事后审计无从对账；
- **openviking 案例（failed 不自愈）**：gate 19:34:02 人工 approve 后，两个级联 failed 的下游工作项（9d3236b9、3f1df997）至 20:09 仍 failed，Verifier 复验 PASS 也不解锁——"活人把门打开了，门后的东西却躺着不动"。

## 2. 证据（facts.md §P6 全部事实）

### 缺口一：MarkDelivered 零校验

1. 置 done 共 5 路径（`internal/service/goal.go`）：:1812（hootl auto-complete）、:1906（reconcile 主路径 promote）、:2478（CompleteFollowUp）、:3471（MarkDelivered）、:2525/:2639（Reopen 反向）。**reconcile 主路径有完整 finalization guard**（:1790-1795：pendingSG/pendingChanges/pendingConsults/pendingAskHuman/pendingBP 任一>0 则 Infof "finalization guard holds" 不 promote）。
2. **MarkDelivered（:3447-3485）success 分支零校验**：直接 `UPDATE goal SET status='done' WHERE status='review'`。注释假设 *"Pending sub-goals cannot exist here — the finalization guard keeps the goal out of the gate judgment"*——但该假设只对 reconcile 路径成立：**gate_decision approve→deliver 是另一条进 done 的路**，绕开了 finalization guard；`SubmitForReview`（:1060-1091）的人工提交路径有 pendingSG 校验（"goal still has pending sub-goals"），自动 gate 路径没有等价校验。agent-browser 案例（Packager sub_goal=running、0 条 verification_result、goal=done）正是从这条路漏水。

### 缺口二：failed 下游不在唤醒集合

3. `wakeDependentsTx`（`internal/service/subgoal.go:1698`）：先 `failGatedDependentsTx`（死依赖→`failGatedSubGoalTx`），再查 `gatedSubGoalsTx`；**`gatedSubGoalsTx`（:1798-1812）只查 `status='running'` 且无 run 的子目标**——failed 不在集合，gate approve/上游 verified 唤不动它们。
4. `failGatedSubGoalTx`（:1940）写评论"依赖 %s %s，无法满足门控，该工作项已置为 failed。" + `markBlueprintNodeTx('failed')` + `sub_goal.failed` 事件（cascade:true）——级联阵亡的 failed **从未有过 run**。
5. 人工重试有出口（`RetrySubGoal` :1580：failed→running+attempt=0+enqueue，带 unmetDeps 校验），但要求人记得逐个点。

## 3. 根因

- **缺口一**：MarkDelivered 的安全假设写在注释里而非代码里——guard 在 reconcile 路径，deliver 路径无等价校验，注释假设被 gate 自动路径打破；
- **缺口二**：`gatedSubGoalsTx` 的 `status='running'` 谓词把 failed 语义上当成了"人工已处理完"的终态，但级联 failed 是"从未跑过"的冻结态，不是"跑过且失败"的结论态——两类 failed 混在一个状态里，唤醒器无法区分。

判定：**成立（两处独立缺陷，共同构成"账本不闭合"）**。

## 4. 影响

- goal done 而 sub_goal running：审计无对账，"三任务对照"这类复盘必须逐表人肉核对；
- gate 人工放行后下游仍 failed：openviking 任务卡到人工逐个 RetrySubGoal 才恢复，RSI 的"闭环验证"更是无从谈起——验证器 PASS 的事实被状态机无视；
- 两缺口复合：goal 层"完成"与节点层"冻结"可以同时为真，系统对"到底完成没有"没有唯一答案。

## 5. 修复方案（[execution-report.md](execution-report.md) §1.P6）

1. **MarkDelivered 硬校验**：事务内 `SELECT COUNT(*) FROM sub_goal WHERE goal_id=? AND status NOT IN ('verified','cancelled','failed')`；pendingSG>0 → `UPDATE goal SET review_request='deliver:guard: N 个 sub_goal 仍未收口（running/pending）——先完成或取消它们，再重试交付'` → 返回 NewValidationError（goal 留 review，reject/stay 仍可用）。**`deliver:` 前缀是载荷**：`internal/daemon/deliver.go:84` 的 `pendingDeliverGoalIDs` 用 `review_request NOT LIKE 'deliver:%'` 排除恢复名单，防 10 分钟恢复器反复重放被拒 deliver 造成日志风暴。
2. **级联失败自愈 `reviveFailedDependentsTx`**（挂在 `wakeDependentsTx` 尾部）：扫 `status='failed'` 且 **NOT EXISTS** role='subgoal' run 且 **EXISTS** 级联评论（`content LIKE '%无法满足门控%'`）的 sub_goal——**收窄语义：只救 failGatedSubGoalTx 级联阵亡（从未跑过）的**；跑过又被重试链关死的 failed 留给人工 RetrySubGoal。候选的 deps 全 verified（firstDeadDep 仍死则跳过）→ UPDATE status='running', execution_attempt=0（RowsAffected=0 竞态跳过）→ blueprint_node failed→running → resolveReleasedBriefTx → enqueueSubGoalRunTx → system 评论"依赖级联失败已解除，自动复位并重新派发" → `sub_goal.retrying` 事件（revived:true, trigger_sub_goal_id）。
3. 收窄原因（设计决策）：初版只查 failed+无 run，会误救 `TestRetryGatedSubGoalRespectsDependsOn` 中 force-failed 的 B（无 run、无门控评论、期望留给人工）——自愈范围必须以"级联阵亡"的痕迹（门控评论）为界，而非以"没跑过"为界。

## 6. 修复状态

**已修复（本次 issue 处理，编译+测试通过）**。变更文件：`internal/service/goal.go`、`internal/service/subgoal.go`。新增 `TestCascadeFailedDependentRevivesWhenDependencyVerifies`（`internal/service/revive_cascade_test.go`，77 行：A 重试链耗尽→B 级联 failed 无 run→人工 RetrySubGoal(A) 完成→B 自动复活 running/0/1 run/带评论→完成→verified）；回归 `TestRetryGatedSubGoalRespectsDependsOn`、`TestDependsOnDependencyFailureCascades`、`TestWakeReleaseCarriesDispatchAnchor` 全绿。
