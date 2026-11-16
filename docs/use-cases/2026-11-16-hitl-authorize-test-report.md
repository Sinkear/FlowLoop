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
- 副作用与处置：探针调用在主 goal 真实暂存了一条授权请求；已按「newest ask wins」语义用同 run 的后续 authorize 调用覆盖为正式修正卡点（该覆盖行为本身即特性实测项）。该修正卡点已获人批准——批准即清除暂存、goal 回 active 并唤醒 owner，探针副作用闭环（同一 run 的暂存请求被人批准撤销，亦验证了「approve = 清除暂存 + 继续」对非计划内请求同样成立）。
- owner 守卫的负向用例由单元测试覆盖（`goal_authorize_test.go`）：`TestAuthorizeNonOwnerRejected`、`TestAuthorizeFromDeadRunRejected`、`TestAuthorizeParkSkipsReviewerRuns`、`TestAuthorizeSurvivesPendingSubGoals`、`TestAuthorizeDuplicateDecisionIsIdempotent`。

### 场景② 中途澄清（goal comment --ask · 已执行）

执行：向人提出真实问题——场景③放行动作的 A/B 选择（A. 升级全局 CLI 入口到 v0.1.1；B. 不动全局入口，放行动作改为清理测试副产物）。与 authorize 的差异点：--ask 是「问询」，goal 保持 active，无审批卡。本轮按发现 5 的注记单独成轮，不带暂存 authorize 请求。

观测：
- 问询于 04:41 UTC 以 ask_human 评论发出，平台向人推送通知；goal 保持 active（问询不产生停摆）。
- 截至 12:19 UTC（约 7.6 小时）人未回复；期间平台在 06:37 UTC 触发 wrapup-idle 兜底唤醒（无在途工作 10 分钟即提醒 owner 收尾）——问询不阻止空闲兜底。
- 终局守卫的 stale-ask 收窄（读码确认 + 本例即实证场景）：只有**本轮 run** 发出的未答复 ask 才 hold 终局判定；上一轮 run 遗留的问询视为过期，不搁置 goal——防止已失效的问询把 goal 永久卡死。本 goal 的 A/B 问询正是上一轮遗留，本轮结束不会被它 hold。
- 结论：--ask 的「问询」半边（发出、通知、不停摆）符合设计；「回复唤醒」半边未被人触发，留待后续验证（见发现 6 的响应不对称）。

### 场景③ 权限放行（authorize 卡点 · 机制已两次覆盖，第三卡点未执行）

- 卡点机制本身已两次端到端实证：①提案审批卡点（人批准耗时约 41 分钟）与「探针副作用修正 + 继续放行」卡点（人批准耗时 24 秒）——均为真实人裁决放行，语义都是 approve=清除暂存+唤醒继续，而非交付合入，与验收批准正确分离。
- 计划中的第三卡点（授权一项影响全局/不可逆的动作）**未执行**：动作本应由场景②的 A/B 答复定夺，而问询未获回复。按人审核纪律，影响全局（升级所有会话共用的 CLI 入口）或不可逆（删除 goal）的动作不得基于一个过期的未答复问询擅自执行——该决定升级到验收卡点由人一并裁决（批准=两项都不做；拒绝可在理由中指定执行哪项，agent 下轮按意见执行）。

### 场景④ 结果验收（submit-review / 终局确认门 · 已执行）

- 本轮以 v0.1.1 CLI 调用 `goal submit-review`（理由：四场景完成、交付物为本报告）——**被守卫拒绝**：`HTTP 400: stop queued/running runs before submitting`。守卫统计 goal 上全部 queued/running run（含调用者自身），而 CLI 只能在 run 内调用——即 run 内 agent 永远无法对所在 goal 成功调用 submit-review（详见发现 9）。
- 终局路径机制（读码确认）：本 goal 所在域的验收策略未冻结（checks_compiled_at 为空），owner run 结束且终局守卫通过（无 pending sub-goal/change/consult、本轮无未答复 ask）后，确认门无条件触发——goal 停摆 review 等人验收，不会无人值守升为 done。即：submit-review 被拒不影响收尾，本轮结束时终局确认门挂出同一张验收卡，批准=异步 deliver 合入交付并关闭 goal（含本报告）。

## 三、发现与风险

1. **部署一致性**：PATH 上的 CLI 入口（`~/.agentwork/bin/agentwork`）仍是旧版构建（无 authorize 子命令），而 daemon 已是 v0.1.1——任意 agent 使用新原语前需升级 CLI 入口（场景③候选 A 即解决此问题）。
2. **token 权威性**：伪造 env 身份对 authorize 无效（见负向探针）——符合「run token 绑定身份」的设计意图；goal 级 RPC（如 goal comments）同样绑定 token 所属 goal，改写 `AGENTWORK_GOAL_ID` 无法跨 goal 读取。
3. **暂存语义**：authorize 的暂存在 goal 仍 active 时对外不可见（UI 只对 review 态渲染 review_request）；同 run 重复调用覆盖（newest ask wins）——实测符合。
4. **停摆优先级**：authorize 停摆先于 finalization guard 与 hootl 自动完成生效；`auto_approve` 强制 false；squad 审查 run 不入队 authorize 停摆（无 diff 可审，人只读 statement 裁决）。
5. **与 --ask 的组合**：暂存的 authorize 请求会在 run 结束时无条件停摆——若同轮既发 --ask 又带暂存请求，审批卡与问询并存，且 review 期间到达的唤醒意图只能排队（approve 会被取消）。本次测试因此把 --ask 放在卡点清除之后的独立轮次（实测注记）。
6. **卡点与问询的响应不对称（UX 发现）**：两次 authorize 卡点分别约 41 分钟、24 秒获人裁决（审批卡直达裁决入口），而 --ask 问询 7.6+ 小时无人回复——问询以普通评论形态存在，显著性远低于审批卡。建议：问询也应生成醒目的待办卡片（或升级为轻量卡点），否则「中途澄清」实际依赖人主动刷 feed。
7. **stale-ask 收窄（设计确认 + 本例实证）**：终局守卫只 hold 本轮 run 发出的未答复 ask；上一轮遗留的问询视为过期、不搁置 goal。本例 A/B 问询即上一轮遗留，本轮终局判定不受其 hold——设计意图（防止失效问询卡死 goal）与实现一致。
8. **wrapup-idle 兜底与平台韧性**：goal 无在途工作 10 分钟后平台主动唤醒 owner 收尾（本轮唤醒即来自它，116 分钟空闲后触发）；首次唤醒因机器离线失败（run failed：machine offline），平台自动以 attempt 2 重试成功——执行层自愈可用。
9. **submit-review 的在途 run 守卫 vs 平台收尾话术的张力（实测确认）**：守卫要求 goal 上无 queued/running run（含调用者自身），而平台的 wrapup-idle 唤醒话术恰在 owner 被唤醒时让它调用 submit-review——从 run 内调用必然自拒（本轮实测：HTTP 400 stop queued/running runs before submitting）。实际效果：run 内 agent 无法走 submit-review 收尾，收尾由终局确认门兜住（本 goal 正是如此）。建议：话术改为「结束回合即可，确认门会挂出验收卡」，或守卫放行调用者自身（其 run 结束即满足前置）。

## 四、验收结论

**特性可用性判定：通过（附 4 项发现与建议）。**

| 场景 | 原语 | 结果 |
| --- | --- | --- |
| ① 提案审批 | `goal authorize` 卡点 | ✅ 端到端通过（暂存→停摆→人批准→唤醒继续，41 分钟裁决） |
| 任意 agent 可执行 | bench 在自己的 hootl goal 上全链路 | ✅ 通过（插入等待人审核状态→停摆→批准→唤醒→hootl 自动收尾 done，全程约 1 分钟） |
| 负向探针 | 伪造 run 身份调 authorize | ✅ 被身份令牌权威性化解（伪造无效）；owner 守卫负向用例由单元测试覆盖 |
| ② 中途澄清 | `goal comment --ask` | ⚠️ 问询半边通过（发出/通知/不停摆）；回复唤醒半边未被人触发（7.6+ 小时无回复，见发现 6） |
| ③ 权限放行 | 第二 authorize 卡点 | ✅ 机制两次端到端覆盖（含 24 秒裁决）；第三卡点（全局影响动作）因问询无答复按纪律未执行，决定升级至验收卡点 |
| ④ 结果验收 | `goal submit-review` + 终局确认门 | ⚠️ submit-review 被 run 内调用守卫拒绝（实测确认，见发现 9）；终局确认门按设计在本轮结束挂出验收卡，人验收=deliver 合入交付并关闭 goal |

交付物：本报告（docs/use-cases/2026-11-16-hitl-authorize-test-report.md）。遗留待人裁决项：场景③候选动作 A/B（随验收卡点一并裁决，见场景③小节）。
