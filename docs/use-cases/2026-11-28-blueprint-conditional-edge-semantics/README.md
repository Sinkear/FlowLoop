# User Case: 蓝本条件边语义割裂 — 图上画的分支，引擎不执行（图文不一致核实）

- 日期：2026-11-28
- 蓝本：自动升级流水线（051dbbd8f71ef729a1050ae9aa78a099）；11-26 三任务全部 pinned v1（16ebee24dd5c3e64f1328d5225c038c5）
- 承接：`2026-11-26-auto-upgrade-proposal-closure`（issue #31，P1–P7 已修）。本 case 为新问题集，不在 P1–P7 内。
- 证据来源：`sources/2026-11-28-沙箱数据库取证报告.md`（沙箱 DB 只读取证原文；DB=/root/job-envs/sandboxes/ouroflow-1795367359/.flowloop/flowloop.db，本机不可达）
- 证据链方法：本仓库源码逐行核实（编译/门控/校验三层）→ 沙箱 DB 只读取证（run/verdict/评论/gate_decision 时间戳）→ 引擎身份核对（沙箱 daemon 源码 HEAD=ac85f63 ≡ 本仓库 4ceb9c8）

## 结论（TL;DR）

用户原始论断分两层，核实后一层被推翻、一层成立：

1. **「画布看着串行是可视化问题，执行语义层已有条件分支」——后半不成立。** task 的 done/failed 端口在编译、门控、校验三层都没有执行语义（G1）。引擎把 15 条边全部当作无条件依赖：验证 PASS 后修复站照常排程（run 入队与 verdict 同事务、偏移 ~1.5ms）。**画布上的「感官串行」恰恰是执行真相的准确呈现**——不是画布说谎，是 spec 意图与引擎实现两头脱节。
2. **「Verifier prompt 写 2 轮、图只有 1 轮容量，图文不一致」——成立（G2）。** prompt L98–101 明确 2 轮修复/3 次验证；图容量 1 轮修复/2 次验证；实跑三次全 PASS、修复站全 NO-OP，回退语义从未被现实检验过。

一句话总结：**spec（画了条件分支）、引擎（不执行分支）、画布（不渲染分支）三方两两割裂。** 真条件语义只存在于 gate 端口 / router / condition（编译期 goal 求值）三种机制上；task 的 done/failed 是装饰品；「FAIL 回退修复」实际由 agent 评论软协议承载（G3），引擎零背书。

**2026-11-28 修复进度**：S1（账目/门禁三 bug：RetrySubGoal 镜像翻转、gate 复活 re-hold、决策收紧到 held）+ S2（画布渲染 handle）已落地，回归 6 测全绿，详见文末「执行记录」；S0 沙箱侧处理、S3–S5 后续迭代。

## 问题清单（G1–G6）

### G1（核心）：task done/failed 端口零执行语义——连边即无条件依赖

三层代码证据（行号=本仓库 HEAD，沙箱实跑源码同源，见「引擎身份锚」）：

| 层 | 证据 | 行号 |
|---|---|---|
| 编译期 | 剪枝只对 condition 节点求值：`if src := byID[e.Source]; src.Type == "condition" && e.SourceHandle != choice[src.ID] { continue }`；task 出边不论 handle 全部进入依赖闭包 | `internal/service/blueprint.go:1593-1627`、`:1629-1698`（ancestorTasks） |
| 运行期 | 门控只认状态不认边：`ready := true; for _, d := range g.deps { if statuses[d] != "verified" { ready = false } }`；剪枝调用点仅 router/gate 两处 | `internal/service/subgoal.go:1725-1731`；`internal/service/blueprint.go:2766`、`:2875`（pruneDeadBranchesTx :2550-2679） |
| 校验期 | 只校验 handle 是声明端口、source+handle 出边唯一；对 task 端口无任何语义消费 | `internal/service/blueprint.go:530-546`、`:601-606`；Kahn 拒环 `:649` |

实跑实证（取证报告 E1/E2/E5 + R2 对照组）：

- 三个 goal 第 1 轮验证全部 PASS（E1）。
- 修复站 run 入队时间 = 验证 verdict 之后 ~1.5ms，**同一事务**（E2）：R1 06:55:37.656→.657（run a126add3）、R2 04:43:42.622→.623（run 87962bc2）、R3 07:08:23.540→.542（run 6eb0e2b6）。
- **R2 是完美对照组**：0 FAIL、Verifier 评论「修复轮次: 0（首次验证即通过，无回退）」，修复站照样跑，Upgrader 自供「本节点为蓝图固定结构触发，无实际失败需修复…no-op repair」——执行 agent 层早知道这是固定结构。
- 27 行 blueprint_node 无一 skipped（E5）；end_stop 全程 pending——PASS 路径上没有任何剪枝发生。

代价账单（PASS 路径白付 repair+reverify 两站）：

| goal | 白付墙钟 | 说明 |
|---|---|---|
| R1 | ~9 分钟 | 06:55:37→07:04:13，修复站 NO-OP |
| R2 | ~1 小时 45 分 | 04:43:42→06:28:40，全部多余功（复验 run 耗时 1h41m） |
| R3 | ~5 小时 | 07:08:23 verify PASS → 修复站 3 attempt 全撞 token 配额失败 → 07:20:36 三站级联 failed → 08:03 人工重开（无 skip 原语，只能重跑）→ 重开后 6 次 token 失败 → 12:29:04 才完成。修复站的存在把「1 站故障」放大成「链上 3 站级联 + 人工重开」 |

### G2：Verifier prompt 与图轮次口径漂移（2 轮 vs 1 轮）

- **prompt 侧**（沙箱 DB agent 表 id=08ef4c09d04e4e87c6b7a57938115c15，system_prompt 6108 字符，原文见取证报告 B 节）：
  - L98「最大回退轮次：2 轮」+ L99–101 三行展开 = **最多 2 次修复、3 次验证**（第 0 轮 FAIL→回退；第 1 轮复验 FAIL→再回退；第 2 轮复验 FAIL→终止）。
  - L12「回退修复后=1 或 2」、L96「回退后第 1 轮=1，第 2 轮=2」、L117「已超过最大修复轮次(2轮)」同口径。
  - **prompt 内部自相矛盾**：L4「验证不通过回退给 Upgrader 修复，修复后再复验」是单数表述，与 L98–101 两轮表冲突。
- **图侧**：1 个 t_repair + 1 个 t_reverify，t_reverify --failed--> end_stop = **1 轮修复、2 次验证**。节点标题「回退修复（轮次 0→1）」「复验（第 2 轮·最后一轮）」、蓝本描述「FAIL 回退修复一轮复验」同口径（描述尾部「最多 2 轮」读作验证轮次则自洽）。
- **实跑侧**：三次全 PASS、修复全 NO-OP——回退语义从未被现实检验。
- 定口径：短期 prompt 归 1 轮（见 S0）；做完 S5 受限回环后统一翻 2 轮（届时需 prompt+图 v4+标题三处同步）。

### G3：「FAIL 回退」是 agent 评论软协议，引擎零背书（图文不一致的最深层）

- v1/v3 的 task 节点 task 键下**只有 assignee/brief，无 verify.mode/verify.verifier 键** → 编译期 verifier 字段为空 → 全部 machine 验证（domain verify commands）。verification_result 20 行 summary 全部是「machine verification passed」实证。
- 引擎从不读 Verifier 的 PASS/FAIL 报告：**即使第 1 轮验证真的 FAIL（报告层面），引擎也不会路由到 repair**——报告 FAIL 只作为评论存在，station 照样 machine-verify passed → verified → 修复站照常放行。「FAIL 走 t_verify --failed--> t_repair」由 Verifier 写「请 Upgrader 修复」评论软性承载。
- 设计者显然知情：蓝图描述与节点标题就是给 agent 的提示，「PASS 时 NO-OP」是 prompt 层约定。**这是 workaround 而非疏忽**——但代价（G1 账单）与风险（R3 放大）真实存在。

### G4：画布不渲染边语义——source_handle 在视图组装时被丢弃

用户观察：图上边看不到任何信息，不能体现「失败走复检、成功打包」，感官是无条件串行。代码核实（渲染链路三处断点）：

| 断点 | 证据 | 行号 |
|---|---|---|
| 视图组装丢 handle | `specGraphToWorkflow` 建 `WorkflowEdge{From, To, Kind:"spec", Source:"explicit"}`——SpecEdge 的 SourceHandle（`blueprint.go:149`）读过校验（:530-546）后被丢弃 | `internal/service/blueprint.go:1420-1422` |
| 传输结构无字段 | Go `WorkflowEdge{From,To,Kind,Source}` 与 TS 同构，均无 handle | `internal/service/workflow.go:49-54`、`web/lib/types.ts:984-989` |
| 渲染无标签 | `EdgeLine` 只画几何+关键路径/spec 样式，无任何端口标签 | `web/components/training/workflow-graph.tsx:353-393` |

对照：spec 编辑器文本列表反而渲染了 handle 标签（`web/components/workflows/workflow-spec-editor.tsx:573-582`，HANDLE_LABELS 映射）；模板层早有 gate reject/timeout 条件边写法（`web/lib/blueprint-templates.ts:152-157`）。**数据链路上 handle 存在、校验强制、只在画布渲染这一步断掉**——15 条 spec 边被画成 15 条同质直线，done/failed/approve/reject/timeout 一律不可见。

### G5：gate 决策预录与生效解耦（三 goal 系统性模式，机制未定）

| goal | gate_decision（人工） | 早于 reverify verified | gate 生效（verdict+下游入队） | 决策→生效 |
|---|---|---|---|---|
| R1 | 07:00:16.400Z（review 208s） | 4 分钟 | 07:17:27.526Z | 17 分钟 |
| R2 | 06:10:58Z（review 5170s） | 18 分钟 | 06:52:11.623Z | 41 分钟 |
| R3 | 11:34:02.863Z（review 338s） | **35 分钟**（决策时 gate 尚在级联 failed 态） | 12:12:50.829Z | 38 分钟 |

风险具体化：R2 的 18 分钟窗口内若复验 reject，人工已 approve。R3 更极端——决策落在 gate failed 态。机制待核查：UI 是「预录决策行+后应用」还是竞态；`ResolveBlueprintGate` 的 state 守卫（running/pending 即过，`blueprint.go:2826-2937`）是否允许了 pre-hold 决策；gate_decision 行与 resolve 生效是否解耦两步。不定性为缺陷，列为核查点。

**2026-11-28 机制裁定（本轮代码核实）**：上表的 gate_decision 行属 **goal 级 review 流**（`goal.go:2212` INSERT，run_id 指证据 run），与蓝图 gate（`ResolveBlueprintGate`）是**两条不联动的流**——「决策早于 reverify verified」是 goal-review 语义（人工在整个 goal 上提前批准），不是蓝图 gate 引擎 bug 的实证；R1 打包 run cancelled 归因 `goal_terminal`（MarkDelivered :3511-3512），即 goal-review approve→deliver 把 goal 关了并撤了在途 run，当时缺 P6 守卫（11-27 4ceb9c8 合入，HEAD 已堵同型）。引擎面真正的缺陷是两个，均已修：**Bug B**——级联 failed 的 gate 被人工 RetrySubGoal 拉活后当普通任务跑了一轮、machine verdict 自动放行（R3 的 gate verdict 摘要是 "machine verification passed" 而非「人工决策：approve」，daemon.log 无 gate resolved 行，实锤绕过人工卡点）；**Bug C**——`ResolveBlueprintGate` 的 state 守卫不检查 held_since，上游未全 verified 的 pre-hold 决策可提前放行下游。

### G6：记录图账目缺陷（存量数据，两处）

- **R3 镜像漂移**：sub_goal 表全 verified，但 blueprint_node 停 failed（gate_pack/t_repair/t_reverify/t_package 四行）；end_ok/end_stop 反而 settled（R1/R2 同位是 pending）。复活/重开路径（reviveFailedDependentsTx）更新 sub_goal 但 markBlueprintNodeTx 未闭环。**根因已钉死（Bug A，已修）**：`RetrySubGoal` 翻转 sub_goal failed→running 但不碰 blueprint_node，而 `markBlueprintNodeTx`（blueprint.go:2049-2059）单调守卫 `WHERE state IN ('pending','running')`——级联失败先置节点 failed（`failGatedSubGoalTx` subgoal.go:2070），人工 Retry 后节点永久 failed，后续 verify 的 settle 镜像被 WHERE 吞掉。reviveFailedDependentsTx 自己有镜像翻转（:1869-1872），11-26 走的是人工 RetrySubGoal 路径（当时 P6 revive 尚未合入）。
- **R1 打包站账目**：t_package sub_goal=running、唯一 run edf439c6 入队即 cancelled、无完成 run、verification_result 无该站行，**goal 却 done**——「升级完成」实际没打包。run.cancel_reason 未取证（补查点）。P6 的 MarkDelivered 硬校验 11-27 才合入，11-26 实跑不受保护；修复后同型应被拦，R1 存量行需沙箱人工核对。**裁定（2026-11-28）**：cancel_reason=`goal_terminal`——goal-review approve→deliver 关 goal 时撤的在途 run（goal.go MarkDelivered :3511-3512），**goal-review 流与蓝图流不联动的产物，非引擎级联 bug**；同型在 HEAD 已被 P6 MarkDelivered 硬校验（goal.go:3473-3501，`status NOT IN ('verified','cancelled','failed')` 计数>0 即拒、goal 留 review）堵死，**无需代码修复**；R1 存量账目（goal done 但缺打包 verdict）仍需沙箱人工核对收尾。

## 数据与指标

### 三 goal 对照总表（11-26，全 train 模式，pinned v1，终态全 done）

| | R1 agent-browser（d5f0e5ba） | R2 drawio（fca85729） | R3 openviking（aa4541a9） |
|---|---|---|---|
| 首验 verdict | passed 06:55:37.656Z | passed 04:43:42.622Z（15P/0F/16S/3R） | passed 07:08:23.540Z |
| 修复站入队 | +1.5ms 同事务（a126add3） | +1.5ms 同事务（87962bc2） | +1.7ms 同事务（6eb0e2b6） |
| 修复站行为 | NO-OP（06:58:53 评论自报） | no-op repair（自报「蓝图固定结构触发」） | 3 attempt 撞 token 配额全 failed → 级联 |
| gate 决策 | approve 07:00:16（早 4m） | approve 06:10:58（早 18m） | approve 11:34:02（早 35m，failed 态预录） |
| Packager verdict | **无行**（run cancelled，G6） | passed 07:05:39.478Z | passed 12:29:04.108Z |
| skipped 节点 | 0 | 0 | 0 |

### RSI findings（11-26 三任务）与本案的因果链

- R1 serial_chain 63.4（关键路径 5 执行节点串行，链长 40.9m 占墙钟 80%）、R3 65.3（4 节点 92%）、R2 46% 未触发。
- **serial_chain 的根因之一现已实锤**：PASS 路径无条件排程的 repair/reverify 两站进了关键链。S4 完成后 PASS 路径执行站自动缩为 check→upgrade→verify→gate→package。
- R3 split_candidate 62.5（Upgrader·升级+修复重型节点可拆）、retry_amplification 60（6 子任务 8 次执行）。

### 引擎身份锚（防「代码分析对象 ≠ 实跑二进制」）

- 沙箱 daemon 二进制 2026-11-27 11:20 构建（v0.0.1-beta.5），嵌入源码路径 /tmp/ouroflow-src，HEAD=ac85f63238b9d5aa0c20cda7782035c72f096ac0 = 2026-11-27 10:07 merge PR #32（fix/issue-31）≡ 本仓库 4ceb9c8。
- grep 行号与本仓库 HEAD 逐行对应：「graph has a cycle」→ blueprint.go:649；pruneDeadBranchesTx → :2550/:2560；调用点 :2766/:2875。
- 11-26 实跑为当日旧 daemon，但 git 考古确认当天提交均为 UI 侧（4ffc9c3/220e2cf/f9adf61/63d1b0f），编译/门控语义与 HEAD 相同。

## 拟定解决方案（用户已确认全部要改；状态截至 2026-11-28 本轮修复，S3–S5 为后续迭代）

| # | 改法 | 落点 | 状态 |
|---|---|---|---|
| S0 | **Verifier prompt 归 1 轮口径**（7 行 diff，见下表；蓝本/图/节点标题不动——它们已是 1 轮口径） | 沙箱 DB agent 表 08ef4c09 system_prompt（产品 UI 编辑） | 沙箱侧处理，本仓库不动（m00300 明确排除） |
| S1 | **账目缺陷核查+修复**：G5 gate 预录机制查清（ResolveBlueprintGate 时机/gate_decision 写入路径）；G6 revive 镜像闭环（markBlueprintNodeTx 补进复活后 verified 路径）；R1 打包站 cancel_reason 补取证 | `internal/service/subgoal.go`、`blueprint.go` + gate UI 决策路径 | ✅ 已实施（本轮，见「执行记录」）：Bug A RetrySubGoal 镜像翻转；Bug B 两条复活路径 gate re-hold 不 enqueue；Bug C ResolveBlueprintGate 收紧到 held；G6-R1 裁定 HEAD 已堵（P6）无需修 |
| S2 | **画布边语义渲染**（G4）：WorkflowEdge 加 handle 字段（Go+TS 两端）；specGraphToWorkflow 保留 handle；EdgeLine 渲染端口标签（复用 spec 编辑器 HANDLE_LABELS）+ 条件支样式（failed/reject 虚线等）；与 spec 编辑器同源 | `internal/service/blueprint.go:1420-1422`、`internal/service/workflow.go:49-54`、`web/lib/types.ts:984-989`、`web/components/training/workflow-graph.tsx:353-393` | ✅ 已实施（本轮，见「执行记录」）：Go WorkflowEdge.Handle → TS handle? → EDGE_HANDLE_META 标签按端口语义着色（done/approve=ok、failed/reject=err、timeout=warn） |
| S3 | **C 级：skip/resume 人工原语**——R3 事故里「无需重跑已 PASS 站」无原语可表达，只能全链重跑 | 引擎 + UI | 待实施（后续迭代） |
| S4 | **A 级：task 条件边真语义化**（先出设计文档）：最小改法 = verdict passed 时在剪掉「仅经 failed 边可达且无其他活前驱」的后代（复用 pruneDeadBranchesTx 机制，skip reason=upstream-passed）；**前置设计决策**：rejected/machine-fail 的引擎语义是「同节点返工」（quality_iteration 无上限 / execution_attempt≤3）而图语义是「走 failed 边」，两套并存冲突必须先裁定，否则双重修复；gate 决策收紧到全依赖 settled | 设计文档 + compile/wakeDependentsTx/prune | 待实施（后续迭代；gate held 收紧部分本轮已随 Bug C 落地） |
| S5 | **B 级：受限回环边**：新边类型 loop（validateSpec 豁免环检测、仅 failed handle、轮次上限计数、break 出边到 end_stop）；完成后 prompt/图/引擎统一翻 2 轮口径，出 v4 | validateSpec + compile + loop 语义 | 待实施（后续迭代） |

实施顺序：S0+S1 立即 → S2 画布（可与 S0 并行，纯视图链路）→ S3 → S4 → S5。

### S0：Verifier prompt 具体 diff（7 行，L99/L4 保留）

| 行 | 现文 | 改为 |
|---|---|---|
| L12 | 当前修复轮次（首次=0，回退修复后=1 或 2） | 当前修复轮次（首次=0，回退修复后=1） |
| L96 | 标注当前修复轮次（首次=0，回退后第 1 轮=1，第 2 轮=2） | 标注当前修复轮次（首次=0，回退修复后=1） |
| L98 | 最大回退轮次：2 轮 | 最大回退轮次：1 轮 |
| L100 | 第 1 轮（回退修复后复验）FAIL → 再回退给 Upgrader | 第 1 轮（回退修复后复验）FAIL → 终止，人工介入，不交给 Packager |
| L101 | 第 2 轮（二次回退修复后复验）FAIL → 终止，人工介入，不交给 Packager | 删除 |
| L117 | 明确标注“已超过最大修复轮次(2轮)，需人工介入” | 明确标注“已超过最大修复轮次(1轮)，需人工介入” |

S5 完成后翻回 2 轮需三处同步：prompt 上表逆操作 + 图 v4 加 loop 边（t_reverify --failed[loop,max=2]--> t_repair + break 出边）+ 节点标题轮次区间 0→2。

## 执行记录（2026-11-28 本轮：S1+S2 落地）

依据 m00300 指令「透过业务执行数据修改代码中的 bug」；S0（Verifier prompt）明确排除（沙箱侧处理），S3/S4/S5 为后续迭代。工作树改动 5 文件 +144/−15，`go build ./...`、`go vet`、`go test ./internal/...` 全绿（service 包 8.8s 全量回归），web `tsc --noEmit` EXIT=0。

### Bug A — RetrySubGoal 镜像翻转（G6-R3 镜像漂移根因，已修）

`internal/service/subgoal.go` RetrySubGoal 在 UPDATE sub_goal 之后补：

```sql
UPDATE blueprint_node SET state='running' WHERE sub_goal_id=? AND state='failed'
```

与 reviveFailedDependentsTx（:1869-1872）同形守卫；markBlueprintNodeTx 单调性不动（settled/skipped 仍不可回退），只把 failed 翻回 running 让后续 settle 镜像能落。效果：R3 型事故（节点永久 failed 挂在 verified 子目标下）不再发生；既有存量四行需沙箱侧人工修正或随下一轮 v4 编译自然消解。

### Bug B — gate 复活必须 re-hold，绝不 enqueue（G5 引擎面，已修）

两处对称特判，gate holder 一律不产生 worker run（编译不变量「GATES are excluded from enqueue in every case…The human decision — never a run — is the only thing that settles it」，blueprint.go:1902-1909）：

- **RetrySubGoal（subgoal.go:1649-1668）**：gate 分支 = resolveReleasedBriefTx + markGateHeldTx + 系统评论「门禁工作项 … 已人工复位：依赖已满足，重新等待人工决策（不产生执行 run）。」；非 gate 走原 enqueueSubGoalRunTx。
- **reviveFailedDependentsTx（subgoal.go:1913-1934）**：gate 分支 = markGateHeldTx + 评论「门禁工作项 … 之前的依赖级联失败已解除（触发：…），已复位并重新等待人工决策。」+ 事件 payload 附 `gate_reheld:true`，continue 跳过 enqueue。
- **markGateHeldTx（blueprint.go:2103）**：UPDATE 守卫从 `AND state='pending'` 放开到 `AND state IN ('pending','running')`——retry/revive 先翻节点 failed→running 再盖戳，原守卫会盖不上 held_since（sweeper 与 Bug C 的 held 检查都依赖此键）。settled/skipped/failed 仍不动。

### Bug C — ResolveBlueprintGate 收紧到 held（G5 预录面，已修）

`internal/service/blueprint.go` ResolveBlueprintGate 在 state 守卫后加 held 检查：resolved_config 无 `held_since` 键 → `NewConflictError "gate %q is not held yet — upstream dependencies have not all verified; a decision now would bypass them"`。合法性论证：非门控 gate compile 即盖 held_since（:1879-1882），门控 gate 在 wakeDependentsTx 释放（依赖全 verified）时盖——「held ⇒ 上游已齐」成立；SweepBlueprintGates 只扫 held 行，auto/timeout 路径不受影响。

### G4 — 画布边渲染 handle（S2，已修）

- Go `WorkflowEdge` 加 `Handle string \`json:"handle,omitempty"\``（workflow.go:49-58）；specGraphToWorkflow 保留 `Handle: e.SourceHandle`（blueprint.go:1422）。
- TS `WorkflowEdge` 加 `handle?: string`（web/lib/types.ts:990）。
- 画布 `web/components/training/workflow-graph.tsx`：新增 `EDGE_HANDLE_META`（done=完成/ok、approve=批准/ok、failed=失败/err、reject=驳回/err、timeout=超时/warn、human/auto/else/fallback=中性 ink-3）；`EdgeLine` 对 isSpec 且带 handle 的边在三次贝塞尔中点画端口标签（9px font-mono、paintOrder:stroke 光晕用 surface-1）。语义色走主题令牌不写死 hex，两主题均有效。与 spec 编辑器 HANDLE_LABELS（workflow-spec-editor.tsx:72-80）同口径。

### 回归测试（internal/service/blueprint_gate_integrity_test.go，6 个全绿）

| 测试 | 覆盖 |
|---|---|
| TestRetrySubGoalMirrorSettlesAfterRetry | Bug A：耗尽重试链→failed 节点→Retry→节点 running→verify→节点 settled（R3 漂移回归） |
| TestRetrySubGoalGateReholdsInsteadOfRun | Bug B 手工路径：Retry gate→0 run、节点 running、held_since 在、系统评论在；决策仍可正常收口 |
| TestReviveFailedGateReholdsInsteadOfRun | Bug B 复活路径：真实级联（seed 耗尽→gate 级联 failed）→seed 修复 verified→gate re-hold 无 run→决策仍通过 |
| TestResolveBlueprintGateRefusesPreHoldDecision | Bug C：上游未跑（gate pending 无 held_since）→决策被拒、错误含 "not held yet"、节点保持 pending |
| TestResolveBlueprintGateHeldStillDecides | Bug C 反向：held gate 决策必须通过（不过度拦截） |
| TestSpecGraphToWorkflowKeepsHandle | G4：approve/reject/timeout/done 四类 handle 全保留，JSON 含 `"handle":"approve"` |
| TestMarkGateHeldTxKeepsFirstStamp | OCR 审查后补锁：已 held 的 gate 二次 markGateHeldTx 必须无操作——哨兵 held_since 幸存，sweeper 时钟不被重入重置（wakeDependentsTx 每次后续 settle 都会重扫 held gate，靠函数内既有早退守卫保证幂等） |

全量回归 `go test ./internal/...`：daemon 4.1s、service 8.8s 等全绿，无既有测试破坏。

### OCR 门禁审查（2026-11-28，open-code-review 委派模式）

提交前用 ocr CLI 对 5 个改动文件做规则审查。结论：**1 个 Major 候选经核实为误报（撤回），其余全数通过**。

- 误报经过：候选指认「markGateHeldTx 守卫放宽后 wakeDependentsTx 重入会重盖 held_since，sweeper 超时时钟被反复重置」。`git show HEAD` 证实函数内**既有**早退守卫 `if _, held := m["held_since"]; held { return nil }`（幂等靠它而非 state 守卫）——重入路径到不了 mergeResolved。误报根因：git diff 的 3 行上下文窗口只露出 held 检查块的尾 `}`，审查时把块边界错认成 SELECT 错误块的 `}`。教训已吸收：非局部论断必须先 file_read/git show 建立前提，再看 diff。
- 顺手收益：以哨兵测试 TestMarkGateHeldTxKeepsFirstStamp 锁死早退契约——未来重构（尤其围绕 state 守卫的改动）不能再无声破坏超时语义。
- 存留 Minor（不阻断）：ResolveBlueprintGate 前置状态守卫的 `pending` 通道因 held 检查成为实际死分支，无害防御纵深；跨版本升级窗口（早于 Phase B-3 的 daemon 置 hold 的 gate 会被新守卫拒绝，需手工补 held_since）仅记录。

### 本轮明确不改（记录定案）

- **G6-R1（R1 打包站）**：HEAD 的 P6 MarkDelivered 硬校验已堵同型，存量行属沙箱人工核对，无本仓库代码动作。
- **G2/G3（prompt、评论软协议）**：沙箱侧处理（m00300）。
- **G1（S4）、回环（S5）、skip 原语（S3）**：后续迭代，前置设计决策（rejected 语义 vs failed 边语义并存冲突）未裁定前不动。

## 附录

### v1 拓扑（9 节点 15 边；v1=v3 边零差异，v3 仅 t_check.brief 加计划纪律段）

```
t_check    = task  Checker·升级前检查               agent:9f5e6c58
t_upgrade  = task  Upgrader·升级+修复               agent:8437c7e8
t_verify   = task  Verifier·独立验证（第 1 轮）        agent:08ef4c09
t_repair   = task  Upgrader·回退修复（轮次 0→1）      agent:8437c7e8
t_reverify = task  Verifier·复验（第 2 轮·最后一轮）   agent:08ef4c09
gate_pack  = gate  人工卡点·打包发布确认  actions=[approve,reject] timeout=48h→approve
t_package  = task  Packager·打包                  agent:294037b3
end_ok / end_stop = end  完成 / 终止（失败或需人工介入）

t_check   --[done]--> t_upgrade        t_check   --[failed]--> end_stop
t_upgrade --[done]--> t_verify         t_upgrade --[failed]--> end_stop
t_verify  --[done]--> gate_pack        t_verify  --[failed]--> t_repair
t_repair  --[done]--> t_reverify      t_repair  --[failed]--> end_stop
t_reverify --[done]--> gate_pack      t_reverify --[failed]--> end_stop
gate_pack --[approve]--> t_package     gate_pack --[reject]--> end_stop
gate_pack --[timeout]--> t_package
t_package --[done]--> end_ok          t_package --[failed]--> end_stop
```

### 版本与消费

| 版本 | id | 状态 | 说明 |
|---|---|---|---|
| v1 | 16ebee24… | published（11-26 03:18） | 11-26 三 goal 全 pin 此版 |
| v2 | fef7788c… | draft 从未发布 | wrong_assignee 翻译（改派 t_upgrade/t_repair 至 AI SHELL），被 v3 fork 覆盖（issue #31 P2，auto-rebase 已修未来同型） |
| v3 | a5443d0b… | published（11-26 11:29） | 仅 t_check.brief 加计划纪律段；11-27 由第 4 个 goal 3227ce64（升级 deepseek-harness，infer 模式）消费，done |

### 目录

```
├── README.md                        本文档：问题、数据指标、拟定方案（S0–S5 待回填）
└── sources/
    └── 2026-11-28-沙箱数据库取证报告.md   沙箱 DB 只读取证原文（A–F 六节，未改动）
```
