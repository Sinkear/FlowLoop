# 人审核新特性实测报告（goal：测试agentwork人审核新特性）

- 日期：2026-11-16
- 被测版本：daemon/CLI v0.1.1（dist 构建，含提交 75a170e「优化中间态需要人工审核作为停止等待，扩展review语义提供更多的语义学行为」）
- 执行者：AI SHELL（goal owner run）
- 实现依据：`internal/service/goal.go`（RequestAuthorization / ResolveReview / reconcileOnRunEnd）、`cmd/agentwork-cli/main.go`（goal authorize / goal submit-review）、`internal/service/goal_authorize_test.go`、`internal/notify/card.go`

## 一、被测特性语义速览（读码结论）

1. **authorize（F1 授权原语）**：goal owner run 在任务中途调用 `goal authorize --statement S` 暂存授权请求（`review_request = "authorize: " + S`）；**该 run 结束时** goal 停摆为 review（复用验收停摆形态，但 `auto_approve` 强制 false——明确要人决策的请求绝不静默通过，也不会被 hootl 自动完成吞掉）。
2. **裁决语义**：approve = goal 同步回到 active 并以「继续下一步」唤醒 owner（重置 attempt，不是交付）；reject = 带人的理由回到 active 并唤醒 owner（human_iterations++）。两者都清除暂存。
3. **submit-review（验收路径）**：与 authorize 严格分离——要求无排队/运行中 run 且无未终结 sub-goal；approve = 异步 deliver（合入交付并关闭 goal）。
4. **守卫**：仅 active goal 可发起 authorize；仅**存活且 owner** 的 run 可暂存（run token 绑定 run→goal/agent，role/存活双重防御）；同 run 再次调用覆盖暂存（newest ask wins）。

## 二、实测记录

### 场景① 提案审批（authorize 卡点 · 通过）

| 步骤 | 观测 |
| --- | --- |
| 暂存 | `goal authorize --statement "【提案审批】…"` 返回 goal id；`review_request` 出现 `authorize:` 前缀，goal 保持 active（暂存期不改变状态） |
| 停摆 | owner run 结束后 goal → review（feed 中授权语句以 agent 评论原文呈现，审批卡显示「卡点」行） |
| 裁决 | 人点击批准；平台唤醒语：「The human approved the authorization request — continue with the next step of the plan.」 |
| 恢复 | goal → active，`review_request` 清空，新 owner run 被唤醒继续执行（未触发 deliver/合并） |

**结论**：停摆/恢复/唤醒语义与实现一致；approve 确实是「继续」而非「交付合入」，与 submit-review 的批准按钮语义正确分离。

### 任意 agent 可执行（通过 · 全链路）

- 已创建临时 hootl goal（scratch 域，指派给 bench——普通执行 agent、非平台特殊角色），任务书明确要求 bench 用 v0.1.1 CLI 在**自己负责的 goal** 上调用 `goal authorize`。
- bench 第一轮 run（owner 角色）：用新版 CLI 成功插入等待人审核状态（PATH 上的旧版 CLI 无 authorize 子命令，bench 按任务书改用全路径调用）；run 结束 → 其 goal 停摆，审批卡呈现 bench 的 statement 原文。
- **用户批准 bench 的卡点** → 平台正确唤醒 bench 继续 → bench 第二轮 run 报告结果并结束 → hootl goal 自动完成（done，无需再次人审）。
- 全链路时间线（run 历史）：第一轮 run 完成 → 停摆等审 → 用户批准 → 第二轮 run 完成 → done，全程约 1 分钟。
- 结论：**authorize 原语对任意 agent 可用**——不限于 owner 角色/特定 persona，只要 agent 是该 goal 的执行 owner run；hootl goal 上 agent 明确发起的 authorize 停摆**优先于 hootl 自动完成**（「明确要人决策的 agent 就该得到人决策」），批准恢复后自动完成路径不受影响。

### 负向探针（发现）

- 方法：以伪造的 `AGENTWORK_RUN_ID`（全零串）调用 authorize，预期「run not found」拒绝。
- 实际：调用**成功**。结论：**run 身份由签名 token 决定，客户端 env 身份被忽略**——身份伪造无效，token 权威（正向安全发现）。
- 副作用与处置：探针调用在主 goal 真实暂存了一条授权请求；已按「newest ask wins」语义用同 run 的后续 authorize 调用覆盖为正式修正卡点（该覆盖行为本身即特性实测项）。
- owner 守卫的负向用例由单元测试覆盖（`goal_authorize_test.go`）：`TestAuthorizeNonOwnerRejected`、`TestAuthorizeFromDeadRunRejected`、`TestAuthorizeParkSkipsReviewerRuns`、`TestAuthorizeSurvivesPendingSubGoals`、`TestAuthorizeDuplicateDecisionIsIdempotent`。

### 场景② 中途澄清（goal comment --ask · 待执行）

计划：执行中向人提出真实问题（场景③放行动作的 A/B 选择），goal 不停摆、人的回复唤醒 owner run。与 authorize 的差异点：--ask 是「问询」，goal 保持 active，无审批卡。

### 场景③ 权限放行（authorize 卡点 · 待执行）

计划：授权一项超出本工作区/影响全局的动作。候选（由场景②的澄清答复定夺）：
- A. 把全局 CLI 入口（`~/.agentwork/bin/agentwork` 符号链接，现指向 11-13 旧版构建）升级到 v0.1.1 dist 构建——影响所有后续会话的 agent CLI 入口；旧目标路径记录在案可回滚。
- B. 不动全局入口；放行动作改为清理测试副产物（删除临时 bench goal，删除不可逆）。

### 场景④ 结果验收（submit-review · 待执行）

计划：全部场景完成后 `goal submit-review --reason …` 收尾；批准 = 异步 deliver 合入交付并关闭 goal（含本报告）。

## 三、发现与风险

1. **部署一致性**：PATH 上的 CLI 入口（`~/.agentwork/bin/agentwork`）仍是旧版构建（无 authorize 子命令），而 daemon 已是 v0.1.1——任意 agent 使用新原语前需升级 CLI 入口（场景③候选 A 即解决此问题）。
2. **token 权威性**：伪造 env 身份对 authorize 无效（见负向探针）——符合「run token 绑定身份」的设计意图；goal 级 RPC（如 goal comments）同样绑定 token 所属 goal，改写 `AGENTWORK_GOAL_ID` 无法跨 goal 读取。
3. **暂存语义**：authorize 的暂存在 goal 仍 active 时对外不可见（UI 只对 review 态渲染 review_request）；同 run 重复调用覆盖（newest ask wins）——实测符合。
4. **停摆优先级**：authorize 停摆先于 finalization guard 与 hootl 自动完成生效；`auto_approve` 强制 false；squad 审查 run 不入队 authorize 停摆（无 diff 可审，人只读 statement 裁决）。
5. **与 --ask 的组合**：暂存的 authorize 请求会在 run 结束时无条件停摆——若同轮既发 --ask 又带暂存请求，审批卡与问询并存，且 review 期间到达的唤醒意图只能排队（approve 会被取消）。本次测试因此把 --ask 放在卡点清除之后的独立轮次（实测注记）。

## 四、验收结论

（全部场景完成后填写）
