# User Case: RSI dispatch_wait 指标把 gate/被合并节点的上游等待整段误记为派发延迟（issue #38）——「派发等待占墙钟 74%」假警报核实与八项派生指标失真分析

- **日期**：2026-11-28
- **关联 issue**：[Sinkear/FlowLoop#38](https://github.com/Sinkear/FlowLoop/issues/38)（open，正文 + 1 条评论，逐字存档见 [sources/issue-38-原文与评论存档.md](sources/issue-38-原文与评论存档.md)）
- **蓝本/数据出处**：RSI 第 4 轮复盘（goal：升级 opencode 至制品最新版本，goal_id `c9dea2b10b3d02220a775c10a63b7841`；live 库 `rsi_report b6ed2cf7180bddfe56cabd1052291b3c`（round 4，2026-11-28T07:38:14Z）、`rsi_round 30769fc74c0627ad9c15a2945453646a`，metrics_schema_version=2；7 个子任务 06:46:50.318 同时刻批量创建，其中 1 个人工卡点节点 e8932e39、6 个 worker run）
- **承接**：
  - [2026-11-26-auto-upgrade-proposal-closure](../2026-11-26-auto-upgrade-proposal-closure/README.md)（issue #31）——其 P5 引入了 dispatch_wait 规则（`rsi_analyze.go:815` 注释自证 "dispatch_wait (P5, issue #31)"），该规则在本轮第一次被记录到实跑触发，即产出本 case 的假警报；
  - [2026-11-28-blueprint-conditional-edge-semantics](../2026-11-28-blueprint-conditional-edge-semantics/README.md)——其 S1/Bug B 把「gate holder 永不产生 run」钉成引擎不变量（`workflow.go:567-570` 注释自述 "by construction it never gets a run"），使本 case 的缺陷从边缘现象变成**系统性必然**。
- **证据来源**：issue 正文与评论（GitHub REST API 抓取，只读存档）；本仓库 HEAD 源码逐行核实。
- **证据链方法**：issue 报告人已给出毫秒级反算（6 节点逐一重建时间轴，合计与 metrics 差 ≤1ms；`dispatch + dependency = Σ(创建→启动) = 5,416,430ms` 严格成立），本 case 的工作是**在 HEAD 源码上把 issue 的每一项论断核实到具体行**。live 库本机不可达（本机 3 个 DB 均非 live 库，与上一 case 同格局），证据链 = issue 反算（自洽、误差 ≤1ms）+ 源码实锤，不做 DB 复算。

---

## 结论（TL;DR）

**分层判定：issue 的全部论断成立，且已实锤到行。**

1. **74% 是指标口径产物，不是真实派发瓶颈（G1，根因实锤）**。`dispatch_wait_ms=2208090`（占墙钟 73.7%）中约 36.8m 没有一分钟来自并发队列或 lane 争用：31.1m 是打包节点 865e7b82 的依赖 e8932e39（人工卡点，被改派合并，无 run）触发 ready 回退到自身 `created_at`，把「等上游串行链 + 等人工卡点」整段误记为派发等待（`internal/service/workflow.go:286-308`）；5.7m（节点 2c5b5287，343,046ms）落在改派合并人工复核窗口内（`gate_decision.review_duration=502s`），是等人工不是排队。真实派发等待 Σ(queued_at→started_at) 仅 344,051ms——指标虚高 **6.4 倍**。
2. **失真呈清晰分层（issue 评论结论，本 case 全部核实）**：原始时间事实（wall/work_sum/lanes/lane_imbalance/parallel_speedup/parallel_width_peak/human_wait）全部正确；**8 组结构性派生指标全部失真**（dispatch/dependency 分账、critical_path、idle_gap、depth、图边自洽、verify_rounds、share 分母、gate_wait），同一个根因——**gate 子任务无 run，而指标层把它当作透明**。
3. **自动 finding 的归因方向恰好说反（G3）**。规则（`internal/service/rsi_analyze.go:815-834`）没有 gate/并行宽度守卫，Explanation 文案硬编码「而不是图结构问题」；本轮真实瓶颈恰恰是图结构（7 个子任务串成一串、含 1 个人工 gate、parallel_width_peak=1、5 条 lane 中 2 条零负载）。三重反讽：P5 引入该规则是为了补盲区，首次实跑触发却指向一个不存在的并发问题，并建议用户「提高通道数/预派发」——对该轮完全无效。
4. **根因是两层假设的系统性冲突，不是一行笔误**。引擎层不变量：「gate holder 永不产生 run（人工/自动决策即它的 run）」；指标层假设：「每个依赖都有 run 表完成时间可观测」。两层各自自洽，合起来矛盾，且无交叉校验。`workflow.go:547-550` 注释明文要求「Dependency gating … must never be folded back in, otherwise a serial chain accumulates waits beyond the span」——意图存在，但无 run 依赖的 ready 回退恰恰把串行链等待折叠回了 dispatch_wait；`:557-561` 的 span 封顶只防「总和超过 span」这一种症状，防不住「串行链误记但总和仍在 span 以内」。
5. **修复主路径明确、数据已在（S1–S5，已实施）**。gate 的放行时间（decided_at）已由 `ResolveBlueprintGate` 写入 `blueprint_node.resolved_config`（`internal/service/blueprint.go:2832-2935`，按 sub_goal_id 键），ready 回退有现成数据源，无需 schema 变更。注意：`gate_decision` 表无 sub_goal_id 列（`internal/store/schema.sql:534-547`，goal 级 review 流），issue 建议 1 字面上的「回退到 gate_decision.decided_at」对蓝图 gate 不可直接落地，落点应为 resolved_config。

---

## 问题清单

### G1（根因）：ready 回退缺陷——无 run 依赖使上游链等待整段误记为 dispatch_wait

**机制**。per-node 派发口径 = `started_at − ready_at`，`ready_at = max(created_at, 依赖 run 完成时间)`。wins 只从 run 表构建（`internal/service/workflow.go:162-164`，`role='subgoal'`）；`readyMS` 以节点自身 `createdMS` 起步，仅当依赖 `wins[dep]` 非 nil 才提升（`workflow.go:289-294`）。gate holder（`blueprint_node` 中 `node_type='gate' AND sub_goal_id!=''`，`workflow.go:212-223`）按引擎不变量永不产生 run（`workflow.go:567-570`），于是 `wins[e8932e39]=nil` → 打包节点 865e7b82 的 `readyMS` 停留在自身 `created_at`（06:46:50.318，批量创建时刻）→ `dispatchWait += start − readyMS`（`workflow.go:306-308`）把「等上游串行链（至 07:17:50.534）+ 等人工卡点」整段 1,864,082ms 记成派发延迟。

| 位置 | 事实 |
| --- | --- |
| internal/service/workflow.go:162-164 | wins 仅认 run 表：`SELECT sub_goal_id,started_at,finished_at FROM run WHERE goal_id=? AND sub_goal_id!='' AND role='subgoal'` |
| internal/service/workflow.go:212-223 | gateHolders 查询：`node_type='gate' AND COALESCE(sub_goal_id,'')!=''` |
| internal/service/workflow.go:286-294 | `readyMS = createdMS` 起步；仅当 `wins[dep] != nil` 才提升 ready |
| internal/service/workflow.go:303-305 | `depWait += readyMS − createdMS`（dependency 口径） |
| internal/service/workflow.go:306-308 | `dispatchWait += w.start − readyMS`（dispatch 口径）——回退时整段记入 dispatch |
| internal/service/workflow.go:547-550 | 注释明文：「Dependency gating is reported separately as dependency_wait_ms and must never be folded back in, otherwise a serial chain accumulates waits beyond the span」——无 run 依赖的回退恰好违反此意图 |
| internal/service/workflow.go:557-561 | span 封顶只处理 `dispatchWait > span` 的溢出（记入 queue_wait_overlap_ms）；串行链误记总额 2,208,090 < span 2,994,728，封顶未触发——**该守卫结构上防不住本故障模式** |
| internal/service/workflow.go:567-570 | 引擎不变量自述：「A blueprint gate HOLDER … by construction it never gets a run (the human/auto decision is its run)」 |
| internal/service/workflow_queue_wait_test.go:129-130, 284-285 | 测试断言口径 = started−ready，且封顶到 wall_span_ms——只覆盖有 run 依赖的用例 |

**实跑实证（issue 反算，合计与 metrics 差 ≤1ms）**：

| sub_goal | 依赖 | ready→started (ms) | 实质 |
| --- | --- | --- | --- |
| 76e979f5 | 无 | 76 | 正常 |
| 28ca3abd | 76e979f5（有 run） | 486 | 正常 |
| 299ec51c | 28ca3abd（有 run） | 295 | 正常 |
| f5727b9f | 299ec51c（有 run） | 103 | 正常 |
| 2c5b5287 | f5727b9f（有 run） | 343,046 | 落在改派合并人工复核窗口（entered_review 07:05:14.9 → decided 07:13:37.29Z，502s）——等人工，不是排队 |
| 865e7b82 | **e8932e39（人工卡点·打包发布确认，被改派合并，无 run）** | **1,864,082** | **ready 回退到自身 created_at，31.1m 上游链+卡点等待全记成 dispatch** |

真实派发等待 Σ(queued_at→started_at) = 344,051ms（5.7m）；dispatch_wait_ms 虚高 2,208,090/344,051 ≈ **6.4 倍**。佐证「无并发争用」：真实空转 wall−work_sum ≈ 5.8m（12%），parallel_width_peak=1，5 条 lane 中 2 条零负载。

### G2（同根因反向）：dependency_wait 在 gate 处串账——865e7b82 漏记 31.0m

**机制**。同一循环里 `depWait += readyMS − createdMS`（`workflow.go:303-305`）。gate 依赖无 run → `readyMS` 回退到自身 `createdMS` → 差值为 **0**：865e7b82 的 dependency_wait 记 0，而本应按口径记「上游链完成 07:17:50.534 − created 06:46:50.318」= 31.0m。**同一根因的两个方向：G1 把这段记进了 dispatch（多记），G2 把它从 dependency 里划走（漏记）——分账在 gate 节点处是串的。**

| 位置 | 事实 |
| --- | --- |
| internal/service/workflow.go:303-305 | dependency 口径 = ready−created；无 run 依赖回退后恒为 0 |
| internal/service/workflow.go:562 | `dependency_wait_ms=3208340` 写入——只含 4 个有 run 依赖节点的反算值（3,208,338ms，差 2ms 吻合） |

**实跑实证**：修正后全局 dependency_wait_ms 应为 **5,068,553ms（84.5m）** 而非 3,208,340（53.5m）。恒等式 `dispatch + dependency = Σ(创建→启动) = 5,416,430ms` 严格成立——这个恒等式本身说明两项是同一时间轴的切分，切分点在无 run 依赖处错了。

### G3：dispatch_wait 规则无守卫 + 归因文案硬编码反方向

**机制**。规则触发条件仅 `dw ≥ 300000ms && dwShare ≥ 0.15`（`internal/service/rsi_analyze.go:55-56` 常量、`:820-821` 判断），不看依赖链是否含无 run 节点、不看 parallel_width_peak。Explanation/Suggestion 均为硬编码模板，把触发即归因于「并发队列、lane 争用或人工/系统派发节奏，而不是图结构问题」。

| 位置 | 事实（逐字） |
| --- | --- |
| internal/service/rsi_analyze.go:815-819 | 规则由 P5/issue #31 引入（注释自证） |
| internal/service/rsi_analyze.go:55-56 | `ruleDispatchWaitShare=0.15`、`ruleDispatchWaitFloorMS=300000` |
| internal/service/rsi_analyze.go:820-821 | 触发条件：`wall > 0 && dw >= FloorMS && dwShare >= Share`——无 gate/无 run 依赖/并行宽度守卫 |
| internal/service/rsi_analyze.go:825 | Explanation 模板：「……派发慢通常来自并发队列、lane 争用或人工/系统派发节奏，而不是图结构问题。」 |
| internal/service/rsi_analyze.go:827 | Suggestion 模板：「检查并发派发上限与 lane 通道数：dispatch 慢而依赖不重时，提高并行认领的通道数或预派发（ready 即派发）比改图更有效。」 |
| internal/service/rsi_analyze.go:829 | Evidence 字段：`dispatch_wait_ms=%d wall_ms=%d share=%.2f lane_count=%d` |

**实跑实证**：本轮 finding（category=dispatch_wait，`rsi_finding 0b6074ca…`）：「派发等待占墙钟 74%（36.8m）…而不是图结构问题」。真实构成 31.1m 等上游串行链+人工卡点、5.7m 等人工复核——对串行依赖链加通道/预派发完全无效；本轮瓶颈恰恰是图结构（7 子任务串行+人工 gate）。**归因与事实相反，会引导用户调一个不存在的并发问题。**

### G4：critical_path 在 gate 处断链 → idle_gap 虚高 4.2 倍、depth 少算 2、narrative「串行损失」失真

**机制**。gate holder 节点不进 `graph.Nodes`（`workflow.go:273-277` continue），其显式入边（865e7b82.depends_on 声明的 `e8932e39→865e7b82`）在边循环被跳过（`workflow.go:338-339`）；关键路径 DP 只收**显式边**且两端节点必须存在（`workflow.go:691-696`），推断边（`workflow.go:688-690`）明确排除。于是 865e7b82 在图里只连着 goal 的 created 边（链长=自身 busy 1,130,644ms），与前 5 节点显式链（1,516,210ms）比较后 DP 选了后者——**最大节点被排除在关键路径之外**。

| 位置 | 事实 |
| --- | --- |
| internal/service/workflow.go:273-277 | gate holder 不进图：`if gateHolders[id] { …; continue }` |
| internal/service/workflow.go:331-345 | 显式边循环：依赖是 gate holder 时跳过该边（:338-339） |
| internal/service/workflow.go:688-690 | 推断边排除于关键路径（注释「must not constrain the critical path」） |
| internal/service/workflow.go:691-696 | preds/DP 只收显式边且两端必须在 byKey |
| internal/service/workflow.go:910-913 | `idle_gap = wall − critical_path`——cp 低报则 idle_gap 虚高 |
| internal/service/workflow.go:975 | `depth = len(g.Critical)`，Critical 只列 DurationMS>0 节点（:764-769） |
| internal/service/rsi_analyze.go:948-955 | narrative 模板把 idle_gap_ms 印成「串行损失 %s」 |

**实跑实证**：critical_path_ms=1,516,210 只含前 5 节点 busy 链，漏掉末节点 865e7b82（1,130,644ms）——低报 42%，真实 ≈ 44.1m（=work_sum，纯串行链）；idle_gap_ms=1,478,442 随之虚高（真实 ≈ 347,804ms，虚高 4.2 倍，其中大部分实际是打包节点在正常干活）；depth=5，显式链真实深 7（少算 2）；narrative「串行损失 24.6m」双重失真——数值虚高 4.2 倍，且 idle_gap 的语义本来就是 IDLE 而非串行惩罚（`workflow.go:902-909` 注释自证，serialization_loss_ms 仅留 deprecated 别名）。

### G5：图结构不自洽——2 条悬空边 + 1 条缺失显式边（同一个循环两个方向各坏一头）

**机制**。显式边循环遍历全部 sub_goal（**含 gate holder**，`workflow.go:331-345`），却只跳过「依赖 d 是 gate holder」的边（`:338-339`）——方向不对称：gate 作依赖时边被丢（e8932e39→865e7b82 缺失，仅靠推断边兜底，而推断边又不进关键路径）；gate 作节点时它自己的 depends_on 照发 `sg:<上游>→sg:e8932e39`，而 sg:e8932e39 不是图节点——**悬空边**。

| 位置 | 事实 |
| --- | --- |
| internal/service/workflow.go:331-345 | 边循环含 gate holder 自身的出边；仅按「依赖是 gate holder」单向跳过 |
| 实跑：node_count=13 | 7 个子任务只画 6 个 agent 节点（13 = goal + 6 agent + 6 verify）；2 条 depends_on 边指向不存在的 `sg:e8932e39`；DB 声明的 `e8932e39→865e7b82` 显式边在图中缺失 |

### G6：verify_rounds 漏计 gate 的 verdict

**机制**。`ResolveBlueprintGate` 放行 gate 时在同一事务里 INSERT 一条 `verification_result`（status='passed'，verifier_run_id=''，`internal/service/blueprint.go:2832-2935`）——但 workflow 图的 verification_result 循环遇到 gate holder 直接 continue（`internal/service/workflow.go:407-408`），这行 verdict 被丢弃。

**实跑实证**：verify_rounds=6，`verification_result` 实际 7 条 passed（e8932e39 的 verdict 漏计 1 条）。

### G7：gate_wait_ms 恒 0（死指标）与 human_wait 分工语义不清

**机制**。gateWait 只从 `wins[id].busy` 累计（`workflow.go:273-277`）——而 gate holder 永无 run，wins 恒 nil，gateWait 恒 0（`:593` 写入）。人工等待实际全部来自 goal 级 review 流（`gate_decision` 表 SUM(review_duration)×1000，`:590-592`）。**蓝图 gate 的持卡时间（held→decided）则哪里都不记**：不在 gate_wait、不在 human_wait、经 G1 还污染 dispatch。本轮 502s 记 0，全部记在 human_wait_ms=502000——issue 标注「分工语义不清（存疑，若是有意设计请说明）」。

### G8：share 口径两处不一致 + 聚合等待进 share-of-wall 语境

**机制 A（分母不一致）**。human_wait_share 分母 = wall + humanWaitMS（`workflow.go:595-599`）；idle_gap_share 分母 = 纯 wall（`:914-917`）。同为 share 语义不同分母。

**机制 B（聚合语义）**。dependency_wait 是「累计人时」口径：串行链上第 k 个节点的等待 ≈ 上游全部完成时间，**上游在干活的每一分钟同时被计为下游在等**（跨节点重复计数）。作为累计人时本身合理；错的是 finding/narrative 把它（经 G1 串账后以 dispatch_wait 面目）放进 share-of-wall 语境与墙钟直接比较——该比值无物理意义（本轮 53.5m > work_sum 44.1m、甚至 > 墙钟 49.9m 的观感即由此来）。

**实跑实证**：human_wait_share=0.14357 = 502000/(wall+human)=502000/3496652，而 502000/wall=0.1677 对不上——分母确实是 wall+human；与 idle_gap_share 的纯 wall 口径不一致。

---

## 数据与指标

### 指标审计分层（issue 评论结论，本 case 逐项核实）

**核验通过——原始时间事实全部正确：**

| 指标 | 结论 |
| --- | --- |
| wall_ms=2994652 / wall_span_ms=2994728 | ✓ 首启动→末完成 / 首创建→末完成，反算吻合 |
| work_sum = agent_busy = 2646854 | ✓ = 6 个 worker run busy 之和 |
| lanes ×5 逐 agent busy | ✓ 与 run 时间轴一致 |
| lane_imbalance=2.1358 | ✓ max/mean = 1130644/529370.8 |
| parallel_speedup=0.8839 | ✓ work_sum/wall |
| parallel_width_peak=1 / retry_amplification=1 / consult_count=0 | ✓（佐证「无并发争用」） |
| human_wait_ms=502000 | ✓ 与 gate_decision.review_duration=502s 及 entered_review→decided 时间差三方吻合 |

**全部失真——均系同一根因（gate 子任务无 run，指标层视其为透明）：**

| 指标 | 失真 | 量化 |
| --- | --- | --- |
| dispatch_wait_ms=2208090 | G1：上游链+卡点等待误记为派发 | 真实 Σ(queued→started) 仅 344,051ms，虚高 6.4 倍 |
| dependency_wait_ms=3208340 | G2：865e7b82 记 0 | 修正应为 5,068,553ms（84.5m） |
| critical_path_ms=1516210 | G4：gate 处断链，漏最大末节点 | 低报 42%，真实 ≈ 44.1m（=work_sum） |
| idle_gap_ms=serialization_loss_ms=1478442 | G4：随 cp 低报虚高 | 真实 ≈ 347,804ms，虚高 4.2 倍 |
| depth=5 | G4：断链 | 显式链真实深 7，少算 2 |
| node_count=13 / 图边 | G5：gate 不进图 | 7 子任务只画 6 节点；2 条悬空边 + 1 条显式边缺失 |
| verify_rounds=6 | G6：gate verdict 被跳过 | 实际 7 条 passed，少计 1 |
| gate_wait_ms=0 | G7：wins 恒 nil | 死指标，502s 全记 human_wait |
| human_wait_share=0.14357 | G8：分母 wall+human | 与 idle_gap_share 纯 wall 口径不一致 |

### 恒等式与诊断信号

- `dispatch + dependency = Σ(创建→启动) = 5,416,430ms` 严格成立——两项是同一时间轴的切分，切分点错在无 run 依赖处；
- 反算合计与 metrics 差 ≤1ms（dispatch 2,208,091 vs 2,208,090；dependency 3,208,338 vs 3,208,340）——**口径核实无误，失真来自口径本身而非数据错误**；
- 真实空转 = wall − work_sum ≈ 5.8m（12%），前 5 节点首尾相接零间隙。

### 时间线锚点（UTC，自 issue）

```
06:46:50.318  7 个子任务批量创建（goal 分解）；865e7b82 created_at 即此（ready 回退的锚点）
07:05:14.9    entered_review（改派合并人工复核开始）
07:13:37.29   gate_decision.decided_at（复核 502s 结束；human_wait_ms=502000）
07:17:50.534  上游串行链最后有 run 节点 finished_at
≈07:17:54     865e7b82 started（created+1,864,082ms）——上游完成后真实派发等待为秒级
   之后       865e7b82 busy 1,130,644ms（最大单节点）
07:38:14      rsi_report round 4 落库（metrics_schema_version=2）
```

---

## 拟定解决方案

> S1–S5 **已于同日实施落地**（用户批准「可以按这个方案执行」）；下表「状态」列为实施结果，落点列为实施前行号。S1–S3 对应 issue 修复建议 1/2/3，S4 覆盖 issue 建议 4 与图/计数一致性，S5 对应评论 #6/#7 与问题 B 的口径澄清。

| # | 改法 | 落点 | 状态 |
| --- | --- | --- | --- |
| S1 | **ready 回退策略**：依赖无 run 记录时，ready 回退到「上游链最近一次可观测完成事件」而非自身 created_at——gate holder → `blueprint_node.resolved_config.decided_at`（`ResolveBlueprintGate` 已写入，`blueprint.go:2832-2935`）或该 gate 的 verification_result.created_at；被改派合并节点 → 上游链最后一个有 run 节点的 finished_at。一并修正 G1/G2 的分账 | internal/service/workflow.go:286-308（+ wins 构建 :162-164 扩展）。注意 `gate_decision` 表无 sub_goal_id（schema.sql:534-547），issue 字面的「回退到 gate_decision.decided_at」对蓝图 gate 不可直接落地 | ✅ 已实施：新增递归 helper `readyOf`（run end → gate decided_at → 上游链最近可观测完成事件，环防护）；gateHolders 查询扩展读 resolved_config，解析出 `gateDecidedMS`/`gateHoldMS` 两 map |
| S2 | **口径重叠防护**：finding 触发前自检——`dispatch+dependency ≈ Σ(创建→启动)` 且某节点依赖链含无 run 节点（gateHolders 非空/unrun_sub_goals>0）时，优先怀疑口径而非派发通道：降级、不触发或在 Evidence 中显式标注「依赖链含无 run 节点，dispatch_wait 可能含上游/卡点等待」 | internal/service/rsi_analyze.go:815-834（触发守卫）+ :55-56 常量 | ✅ 已实施（按数据驱动分支落地）：规则触发后按 `gate_node_count>0 && parallel_width_peak<=1` 判定「串行链+gate」形状，Explanation/Suggestion 切换为图结构归因，Evidence 增补 `gate_node_count=%d parallel_width_peak=%d`；S1 修复使 dispatch/dependency 分账本身已不再重叠，本守卫成为第二道防线 |
| S3 | **critical_path 补全**：无 run 的 gate/合并节点按放行时间（decided_at）以 0 工作穿越节点纳入关键路径 DP；同时 idle_gap/depth/narrative「串行损失」随之自愈 | internal/service/workflow.go:273-277（节点进图）、:331-345（边对称）、:665-777（DP 接纳 windowless 节点——现有 :715-724 的 0 贡献穿链逻辑可复用） | ✅ 已实施：gate 以 `Kind="gate"`、0 工作、decided_at 零宽窗口进图；DP 的 0 贡献穿链（p.length==0 也穿链）与 windowless 时间序守卫天然放行；反例测试断言 cp=work+pack 穿链成立 |
| S4 | **图与计数一致性**（覆盖 issue 建议 4）：gate 节点进图（配合 S3）使 2 条悬空边有了 To 端点、缺失显式边有了 From；verify_rounds 计入 gate verdict（不再跳过）或单列 gate_verdicts；dispatch_wait 规则在 `parallel_width_peak=1` 且依赖链含 gate 时不得输出「并发队列/lane 争用」类建议，文案归因改为数据驱动 | internal/service/workflow.go:407-408、:331-345；internal/service/rsi_analyze.go:825-827 | ✅ 已实施：显式边循环删除 `gateHolders[d]` 跳过（gate 入边保留、出边端点存在，悬空边消失）；verify 循环删除 gate verdict 跳过（计入 unmeasured 轮）；规则 shape-aware 归因见 S2 |
| S5 | **口径澄清与统一**：human_wait_share 与 idle_gap_share 分母统一或显式标注各自口径；gate_wait_ms 语义决定（死指标→移除，或改为从 resolved_config 计 gate 持卡时间并与 human_wait 明确分工）；dependency_wait 标注「累计人时」语义，禁止进 share-of-wall 语境 | internal/service/workflow.go:590-599、:914-917；internal/service/rsi_analyze.go:952（narrative 模板） | ✅ 已实施：gate_wait_ms 改为 Σ(held_since→decided_at) 持卡窗口（legacy：无窗口时回退 holder-run busy）；human_wait 分工注释更新（两源：gate_decision 交付审查 + resolved_config 持卡窗口）；两个 share 分母差异以注释钉死为**故意的**（G8）；dependency_wait_ms 人时语义注释；`metrics_schema_version` 2→3，baseline() 按 v3 过滤旧样本（趋势史从 v3 重启） |

---

## 执行记录

- **2026-11-28（本 session）**：GitHub REST API 抓取 issue #38 正文+评论 → 只读存档 [sources/issue-38-原文与评论存档.md](sources/issue-38-原文与评论存档.md)；对照 HEAD 源码逐行核实 8 组问题（G1–G8）全部实锤；撰写本 README。
- **2026-11-28（同日实施，用户批准后）**：S1–S5 全部落地。核心改动：
  - `internal/service/workflow.go`：gateHolders 查询扩展读 `resolved_config`（解析 `decided_at`/`held_since`）；新增递归 `readyOf`（ready 回退链：run end → gate decided_at → 上游链最近可观测完成事件，环防护）；gate 以 `Kind="gate"` 0 工作节点进图（decided_at 零宽窗口）；显式边循环不再丢 gate 入边（悬空边消失）；verify 循环不再跳过 gate verdict（计入 unmeasured 轮）；`gate_wait_ms` 改为 Σ 持卡窗口；新增 `gate_node_count`；`metrics_schema_version` 2→3（口径断点注释：node_count/cp/depth/dispatch/dependency/gate_wait/verify_rounds 均不可与 v2 报告比较）。
  - `internal/service/rsi_analyze.go`：dispatch_wait 规则 shape-aware（`gate_node_count>0 && parallel_width_peak<=1` → 归因切图结构，否则保留经典 lane 归因）；baseline() 按 `metrics_schema_version>=3` 过滤旧样本（v1/v2 中位数不可比，趋势史从 v3 重启）。
  - 反例测试（全部新增）：`TestQueueWaitGateDependencyAnchorsReadyToDecision`（workflow_queue_wait_test.go——gate 依赖下 40m 等待落 dependency_wait 而非 dispatch_wait、gate 持卡 30m 单列、gate 节点零宽窗口、gate→下游显式边存在、cp 穿链=work+pack、idle_gap=人等 30m、verify_rounds=1、gate_node_count=1、parallel_width_peak 仍为 1）；`TestDispatchWaitFindingAttributionIsShapeAware`（workflow_metrics_test.go——串行+gate 形状输出图结构归因并显式否定加通道/预派发，队列形状保留经典归因）；harness 新增 `gateSettled`/`gateVerdict` helper。
  - 断言更新：`TestWorkflowIdleGapAliasSameValue` 的 schema_version 2→3。全模块 `go test ./...` 通过。
- **核实方式**：live 库（`rsi_report b6ed2cf7…`）本机不可达——本机 `~/.flowloop/flowloop.db` 为测试库（goal 4 条全是测试任务，rsi_report/rsi_round/gate_decision/blueprint_node/verification_result 全 0 行），`~/.ouroflow/flowloop.db` 无 goal 表，`~/.flowloop/ouroflow.db` 仅 1 条旧 goal。证据链 = issue 反算（自洽、误差 ≤1ms、恒等式成立）+ HEAD 源码逐行核实；与 [2026-11-28-blueprint-conditional-edge-semantics](../2026-11-28-blueprint-conditional-edge-semantics/README.md) 的「沙箱 DB 本机不可达」同格局，不做 DB 复算。实施后的行为验证以反例测试为准（fixture 复刻 issue 的时间结构）。

## 附录

### A1 与既有 case 的因果链（为什么这次不是意外）

- **2026-11-26（issue #31）P5** 引入 dispatch_wait 规则：当时把它当「纯编排延迟」收集并建规则，默认指标口径干净；
- **2026-11-28-blueprint S1/Bug B** 把「gate holder 永不产生 run」钉成引擎不变量并写入注释——引擎层从此**设计上**保证每张含 gate 的图都会出现无 run 节点；
- **本 case**：不变量与指标层「每个依赖都有 run 完成时间」的隐含假设系统性冲突，首次被完整记录。**这不是边缘 bug：只要蓝本含人工 gate 且下游节点有 run，dispatch_wait/dependency_wait/critical_path/verify_rounds 必然按上述方式失真**；触发概率随 gate 使用率上升。三层教训：新指标上线时应对照引擎不变量做「无 run 依赖」反例测试（`workflow_queue_wait_test.go` 现有用例全部是 run 依赖）；规则模板的归因文案应是数据驱动而非硬编码；分层指标（原始事实 vs 结构派生）应标注派生依赖的失效条件。

### A2 verify 诚实标注备注（issue 另注）

`verify_busy_ms=0 / verify_overhead_scope=unmeasured` 是诚实标注（verification_result.verify_ms 全为 0、无 verifier run 记录），但意味着 `verify_overhead_ratio=0` 的语义是「未测」而非「零开销」，下游引用需注意。

### 目录

```
2026-11-28-rsi-metrics-gate-no-run-misattribution/
├── README.md                                  ← 本文件（分析与结论）
└── sources/
    └── issue-38-原文与评论存档.md              ← issue 正文+评论逐字存档（只读）
```
