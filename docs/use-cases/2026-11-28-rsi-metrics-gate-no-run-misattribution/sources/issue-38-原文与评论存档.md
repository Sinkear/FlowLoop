# issue #38 原文与评论存档

- 来源：https://github.com/Sinkear/FlowLoop/issues/38 （GitHub REST API `repos/Sinkear/FlowLoop/issues/38` 与 `/comments`，2026-11-28 16:38 +08:00 抓取）
- 作者：Sinkear（OWNER）；issue open，created 2026-11-28T08:15:40Z；评论 created 2026-11-28T08:29:09Z
- 本文件为只读存档，正文与评论未做任何改动；分析见同目录 [../README.md](../README.md)。

---

## Issue 正文

标题：**[RSI/metrics] dispatch_wait_ms 把人工卡点/被合并节点的上游等待整段误记为派发延迟——自动 finding「派发等待占墙钟 74%」为假警报，建议方向也反了**

### 现象

RSI 第 4 轮（2026-11-28，goal：升级 opencode 至制品最新版本，goal_id `c9dea2b10b3d02220a775c10a63b7841`）自动产出 finding（`rsi_finding 0b6074ca…`，category=dispatch_wait）：

> 派发等待占墙钟 74%（36.8m）— 子任务就绪到开跑的派发延迟累计 36.8m，占墙钟 74%（墙钟 49.9m）。该指标不含依赖门控（dependency_wait_ms 单列）。派发慢通常来自并发队列、lane 争用或人工/系统派发节奏，而不是图结构问题。

证据字段：`dispatch_wait_ms=2208090 wall_ms=2994652 share=0.74 lane_count=5`。

**实测该 74% 是指标口径产物，不是真实派发瓶颈。** 36.8m 中没有一分钟来自队列或 lane 争用。

### 毫秒级反算验证

对 6 个 worker run 逐一重建时间轴（sub_goal 创建 → 依赖满足 → 启动）：

| sub_goal | 依赖 | ready→started (ms) | 实质 |
|---|---|---|---|
| 76e979f5 | 无 | 76 | 正常 |
| 28ca3abd | 76e979f5（有 run） | 486 | 正常 |
| 299ec51c | 28ca3abd（有 run） | 295 | 正常 |
| f5727b9f | 299ec51c（有 run） | 103 | 正常 |
| 2c5b5287 | f5727b9f（有 run） | 343,046 | 落在改派合并人工复核窗口内（`gate_decision.decided_at=07:13:37.29Z`，`review_duration=502s`，与 `human_wait_ms=502000` 一致）——**是等人工，不是排队** |
| 865e7b82 | **e8932e39（人工卡点·打包发布确认，被改派合并，无 run 记录）** | **1,864,082** | **ready 回退到自身 created_at，整段『等上游串行链 + 等人工卡点』31.1m 被记成派发等待** |

合计 2,208,091ms，与 metrics `dispatch_wait_ms=2208090` 仅差 1ms（取整）。`dependency_wait_ms=3208340` 反算同样吻合（差 1ms），且 `dispatch + dependency = Σ(创建→启动) = 5,416,430ms` 严格成立。

反过来看真实空转：`wall_ms − work_sum_ms = 2994652 − 2646854 ≈ 5.8m（12%）`。前 5 个节点首尾相接零间隙，全程 `parallel_width_peak=1`、5 条 lane 中 2 条零负载——根本不存在通道争用。

### 根因

`queueWait/dispatch_wait` 的 per-node 口径是 `started − ready`；`ready` 取依赖节点的 run 完成时间。当依赖节点是**被改派合并的节点或人工卡点节点（没有 run 记录）**时，ready 无从计算，回退（fallback）到子任务自身 `created_at`——于是『上游依赖链未完成 + 人工 gate 未放行』的整段时间被记为 dispatch_wait。具体到本轮：865e7b82（打包）的依赖 e8932e39 是人工卡点节点，因改派合并无 run，31.1m 全部误记。

### 次生失真（同一根因）

- `critical_path_ms=1516210`（25.3m）只覆盖前 5 节点的忙时和链，**漏掉了末尾打包节点 865e7b82（1,130,644ms）**——关键路径在 gate 节点处断链；
- 因此 `idle_gap_ms=1478442`（24.6m）虚高，其中大部分实际是打包节点在正常干活；
- narrative 里『串行损失 24.6m』的表述也随之失真。

### 自动 finding 的建议方向也是错的

同一条 finding 给出的建议：

> 检查并发派发上限与 lane 通道数：dispatch 慢而依赖不重时，提高并行认领的通道数或预派发（ready 即派发）比改图更有效。

按真实构成，36.8m 中 31.1m 是等上游串行链 + 人工卡点，5.7m 是等改派合并人工复核——**对串行依赖链加通道 / 预派发完全无效**，finding 里『而不是图结构问题』的归因恰好说反了（本轮瓶颈恰恰是图结构：7 个子任务串成一串，其中一个是人工 gate）。建议方向与事实相反，会引导用户去调一个不存在的并发问题。

### 修复建议

1. **ready 时间回退策略**：依赖节点无 run 记录时，ready 应回退到『上游链最近一次可观测完成事件』——如 `gate_decision.decided_at`（gate 节点）、上游链最后一个有 run 的节点的 finished_at，而不是子任务自身 created_at；
2. **口径重叠防护**：当 dispatch_wait 与 dependency_wait 之和逼近 Σ(创建→启动) 且某节点依赖链含无 run 节点时，finding 模板应优先怀疑指标口径而非派发通道；
3. **critical_path 补全**：关键路径计算需把无 run 的 gate/合并节点按放行时间（decided_at）纳入路径，避免断链导致 idle_gap 虚高；
4. finding 文案在 `parallel_width_peak=1`、依赖链含 gate 节点时，不应输出『并发队列/lane 争用』类建议。

### 数据出处

live 库 `rsi_report b6ed2cf7180bddfe56cabd1052291b3c`（round 4，2026-11-28T07:38:14Z），`rsi_round 30769fc74c0627ad9c15a2945453646a`，metrics_schema_version=2；worker run 时间轴来自 `run`/`sub_goal`/`gate_decision` 表（UTC 原值）。以上反算脚本可复现，误差 ≤1ms。

---

## 评论（issuecomment-7236613076）

### 补充审计：dependency_wait_ms 及其余全部指标（同轮次数据）

#### dependency_wait_ms=3208340（53.5m）——你若觉得它大得反直觉，是对的，有两层问题

**问题 A（与主 issue 同根因，但方向相反——它漏记而不是多记）**

口径为 Σ(依赖节点 run 完成时间 − 本节点 created_at)。对 4 个有 run 依赖的节点反算 = 3,208,338ms，与 metrics 3,208,340 差 2ms，口径吻合。但 **865e7b82（打包）的 dependency_wait 记为 0**——它的依赖 e8932e39（人工卡点）无 run，整段 31.0m（上游链完成 07:17:50.534 − created 06:46:50.318）被划进了 dispatch_wait（即主 issue 中那 1,864,082ms）。按同一口径修正，dependency_wait 应为 **5,068,553ms（84.5m）**，而非 53.5m。dispatch 与 dependency 的分账在 gate 节点处是串的。

**问题 B（口径固有，属设计缺陷而非实现 bug）**

本 goal 的 7 个子任务在 06:46:50.3 同一时刻批量创建（goal 分解时），串行链上第 k 个节点的 dependency_wait ≈ 上游所有任务的累计完成时间——**上游在干活的每一分钟，同时被计为下游「在等依赖」**（跨节点重复计数）。所以出现 53.5m > work_sum 44.1m、甚至 > 墙钟 49.9m 的观感。聚合等待作为「累计人时」指标本身合理，错的是 finding/narrative 模板把它放进 share-of-wall 语境与墙钟直接比较——该比值无物理意义。

顺带修正主 issue 中 dispatch_wait 的真实构成：**真实派发等待 Σ(queued_at→started_at) 仅 344,051ms（5.7m）**，其中 343,046ms 属 2c5b5287 一项、恰落在改派合并人工复核窗口（entered_review 07:05:14.9 → gate decided 07:13:37.3，502s）。即 36.8m 的「派发等待」中 5.7m 是等人工复核、31.1m 是 gate 断链误记，**没有一分钟是队列/lane 争用**。

#### 其余指标逐项审计

**核验通过（原始时间事实全部正确）：**

| 指标 | 结论 |
|---|---|
| wall_ms=2994652 / wall_span_ms=2994728 | ✓ 首启动→末完成 / 首创建→末完成，反算吻合 |
| work_sum = agent_busy = 2646854 | ✓ = 6 个 worker run busy 之和 |
| lanes ×5 逐 agent busy | ✓ 与 run 时间轴一致 |
| lane_imbalance=2.1358 | ✓ max/mean = 1130644/529370.8 |
| parallel_speedup=0.8839 | ✓ work_sum/wall |
| parallel_width_peak=1 / retry_amplification=1 / consult_count=0 | ✓（佐证「无并发争用」） |
| human_wait_ms=502000 | ✓ 与 gate_decision.review_duration=502s 及 entered_review→decided 时间差三方吻合 |

**新发现问题（均系同一根因：gate 子任务无 run，指标层视其为透明）：**

| # | 指标 | 问题 | 量化 |
|---|---|---|---|
| 1 | critical_path_ms=1516210 | 只含前 5 节点 busy 和，**漏掉最大的末节点 865e7b82（1,130,643ms）**——图中 2c5b5287→865e7b82 的推断边存在，显式链却在 gate 处断掉 | 低报 42%，真实 ≈ 44.1m |
| 2 | idle_gap_ms = serialization_loss_ms = 1478442 | = wall − critical_path，随 #1 虚高 | 真实 ≈ 347,804ms（5.8m），虚高 4.2× |
| 3 | 图结构 node_count=13 | 7 个子任务只画了 6 个节点（13 = goal + 6 agent + 6 verify）；**2 条 depends_on 边指向不存在的节点 `sg:e8932e39`（悬空边）**；而 DB 中 865e7b82.depends_on 声明的 `e8932e39→865e7b82` 显式边在图中缺失，仅靠推断边兜底 | 图与数据源不自洽 |
| 4 | depth=5 | 同断链，显式链真实深 7 | 少算 2 |
| 5 | verify_rounds=6 | `verification_result` 实际 7 条 passed（**e8932e39 的 verdict 被漏计**） | 少计 1 |
| 6 | gate_wait_ms=0 | 明明存在 502s gate 等待却记 0，全部记在 human_wait——两指标分工语义不清（存疑，若是有意设计请说明） | — |
| 7 | human_wait_share=0.14357 | 502000/wall=0.1677 对不上；502000/(wall+human)=502000/3496652=0.14357 ✓——**分母是 wall+human_wait，而 idle_gap_share 用纯 wall 做分母**，两个 share 口径不一致 | — |

另注：`verify_busy_ms=0 / verify_overhead_scope=unmeasured` 是诚实标注（`verification_result.verify_ms` 全为 0、无 verifier run 记录），但意味着 `verify_overhead_ratio=0` 的语义是「未测」而非「零开销」，下游引用需注意。

#### 结论

本轮失真呈现清晰分层：**原始时间事实（wall/work/lanes/attempt/human_wait）全部正确；系统性失真全部集中在依赖 gate 无 run 节点的结构派生指标**（dispatch/dependency 分账、critical path、idle_gap、depth、verify 计数、share 分母）。主 issue 的修复建议（ready 回退到 gate_decision.decided_at / 上游可观测完成事件；critical_path 纳入 gate 节点）可一并覆盖上表 #1–#5；#6/#7 建议单独澄清口径。
