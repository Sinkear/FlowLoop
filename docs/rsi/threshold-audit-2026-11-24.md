# RSI 规则阈值与指标口径审核（2026-11-24）

审核对象：`internal/service/rsi_analyze.go` 的 12 个规则常量与 11 类 finding 判据、
`internal/service/plan_alignment.go` 的 `plan_deviation`，以及它们依赖的
`internal/service/workflow.go::computeWorkflowMetrics` 指标口径。

方法：逐常量做 `git log -S` 溯源；用真实 schema 复算指标；用合成工作流探针
（2 task + 2 verify、单节点图）验证触发边界；对照 `docs/RSI.md`、
`docs/research/rsi-summary.md §4.3`、`docs/rsi/self-iter-playbook.md`、
`docs/rsi/r1-proposals.md` 与 `docs/use-cases/2026-11-21-blueprint-self-iteration/` 的真实样本。

## 0. 结论摘要

1. **12 个阈值全部来自 2026-11-08 的一次 MVP 提交（4289095），此后从未改过**
   （`git log -S <常量> -- internal/service/rsi_analyze.go` 对每个常量都只命中 4289095）。
   它们是实现者当时按经验写的整数/百分比，没有数据集、没有分布、没有回放验证。
2. **指标口径改过（2026-11-14，2a4babe），但阈值没跟着改**：`parallel_speedup`
   从 `work/cp` 改成 `work/wall`、review 移出关键路径、新增 `human_wait_ms`；
   `lost_parallelism` 在这之前数学上恒不成立（`speedup ≡ gain`），0.7 系数
   直到 2a4babe 之后才第一次可能触发（2026-11-21 事故里报了 2 次），
   **从未用真实分布校准过**。仓库内也没有可用于校准的数据
   （本地 `~/.ouroflow/ouroflow.db` 的相关表全空）。
3. **4 个结构性缺陷**（靠调数值救不回来，必须改口径）：
   - `wrong_assignee` 用的 lane 指标只统计“有耗时的 lane”、且把 verify/consult
     时间混入，空闲成员根本不出现 → 在 2 个忙 lane 时 `max/mean ≥ 2` **数学上不可达**，
     而“有人闲着”的典型场景永远不触发。
   - `human_wait` 只读 goal 级 `gate_decision`；蓝图 gate 的决策写进
     `verification_result` + `blueprint_node.resolved_config`，**完全不进 gate_decision**
     → 蓝图流程里最大的人工等待是盲区。
   - `serial_chain` 用 `len(g.Critical)` 计数，关键路径包含 verify/consult 节点
     → “4 节点链”实际可能只是 2 个任务 + 2 次验证（探针已复现）。
   - finding `Score` 的量纲互不可比（人工等待用**秒**、plan_deviation 固定 80、
     其余用 10–40 的整数倍），排序与 `top_category` 因此失真。
4. **其余阈值多为拍脑袋的整数**：重试 ≥2 次（等于“重试过一次”就报警）、
   人工等待 30%、拆分 45%、合并 0.6、ping-pong 3 次等，都缺少“这条规则
   在真实会话里该触发多少次”的目标，也缺少精度反馈闭环
   （finding 的 `confirmed/dismissed` 状态没有回流到阈值）。
5. 建议顺序：**先修 P0 口径（lane / human_wait / serial_chain / 评分），再按 §4 调整
   P1 阈值，最后用 §5 的基线与回放流程校准**。文档里还有 7 处与实现不一致（§6）。

## 1. 这些数字从哪来：完整溯源

| 常量 | 现值 | 引入提交 | 文档同步情况 |
| --- | --- | --- | --- |
| `ruleSerialChainThreshold` | 4 | 4289095（2026-11-08） | `docs/RSI.md` 判据表同步 |
| `ruleLostParallelGain` | 2.0 | 同上 | 同步；但 0.7 系数只写在实现里 |
| `ruleSplitCount` / `ruleSplitShare` | 2 / 0.45 | 同上 | 同步（另有一条**错误**的 cp=0 回退说明，见 §2.4） |
| `ruleMergeWall` / `ruleMergeSpread` | 3 / 0.60 | 同上 | 同步 |
| `ruleLaneImbalance` | 2.0 | 同上 | 同步 |
| `rulePingPongPairs` | 3 | 同上 | 同步 |
| `ruleReworkRatio` | 0.5 | 同上 | 同步 |
| `ruleConsultRepeat` | 3 | 同上 | 同步 |
| `ruleRetryAmplification` | 2.0 | 同上 | 同步（注释却写“attempts ≥2× node count”，与实现“max Attempt≥2”不符） |
| `ruleHumanWaitShare` | 0.30 | 同上 | 同步；分母说明与实现也不完全一致 |

证据：`git show -s --format='%h %ad %s' 4289095` → `4289095 2026-11-08 workflow rsi mvp version`；
该提交一次性引入分析器（29 个文件、+3943/−1665），提交正文为空。

**设计文档里的原始判据与实现并不一致**（`docs/research/rsi-summary.md §4.3`）：

| 类别 | 研究稿判据 | 实现判据 |
| --- | --- | --- |
| serial_chain | 关键路径占比 ≥0.6 + `parallel_speedup<1.5` + ≥3 节点 | `len(Critical)≥4`，无占比/加速条件 |
| lost_parallelism | 两个独立节点 + wall ≥20% | `work/cp≥2` 且 `speedup<gain*0.7` |
| split_candidate | 单节点 cp 占比 ≥0.5 + step 数 ≥2× 中位数（或工具/文件簇 ≥2 组） | 2 个节点各 ≥45% **cp**，无步骤口径，分母含验证 hop |
| merge_candidate | ≥3 节点且每个 <2min、串行、单节点开销占比高 | ≥3 叶节点 + 结束散布 ≤0.6×max 时长，无“小”的绝对界 |
| wrong_assignee | 重试/失败率 ≥ 同 agent 基线 2×，或返工 ≥2 轮 | 改成“lane 负载失衡”——研究稿的这个判据**未实现**；lane 失衡本身不在 §4.3 表里 |
| over_verification | `verify_overhead≥0.25 && rework<0.05` | **未实现**（只有指标，无 finding） |
| ping_pong | 同一对 ≥2 个来回 | 同一对 ≥3 次（单向计数） |
| brief_quality | 被拒 brief 与通过 brief 的文本对比 | 用 `rework_ratio` 代理，无文本分析 |
| consult_repeat | 同一主题跨 ≥2 个 goal | 单 goal 内发起过 ≥3 次中途咨询 |

结论：**实现是从研究稿里“意译”出来的一组启发式，不是研究稿判据，也没有做过头采**。
`docs/rsi/self-iter-playbook.md` 要求“统计显著（n≥5）+ 归因 + 干预 + 回滚”，
但代码里只有 `retry_amplification` 用到 baseline（L2 升级），其余规则仍是单会话硬阈值。

## 2. P0 指标口径问题

### 2.1 lane 指标：`wrong_assignee` 不可达，且看不见空闲成员

`internal/service/workflow.go:554`：

```go
lane[n.AssigneeID] += n.DurationMS   // 所有非 review、耗时>0 的节点
```

- verify 节点（verifier agent）与 consult 节点（guest agent）的时长也被记进 lane，
  即使它们不是执行分派；integrate 节点没有 assignee。
- 没有跑过任何 run 的 squad 成员**不会出现在 `lanes` 里**，
  所以 `lane_count` 只是“忙过的 lane 数”，不是 squad 规模。
- `lane_imbalance = max / (sum/len)`（`:660`）在 2 个忙 lane 时最大只能趋近 2
  （`2·max/S < 2`），**永远达不到 2.0**；要触发必须 3 个以上 lane 且最忙者
  ≥2/3 总负载。

真实反例：`docs/rsi/r1-proposals.md` 的单节点 goal `lane_count=1`，
`wrong_assignee` 的 `lane_count≥2` 守卫直接把它排除了；而“1 个成员干了 90%，
另外 2 个闲着”这种最该报的场景，因为闲人不在 `lanes` 里，`imbalance` 反而只有 1.0。

**修法**：lane 分母应来自 squad roster（零填充），并且只统计 `Kind=="agent"` 的
节点；验证/咨询时间单独成列（或直接排除）。`lane_imbalance` 保留“最忙/均值”，
但规则阈值改为“最忙 ≥ 其他 lane 中位数的 2 倍”（对 2 lane 也成立），
或“最忙份额 ≥0.6 且 ≥3 个有成员的 lane”，并加“最忙 ≥5min”的下限防抖。

### 2.2 human_wait：蓝图 gate 是盲区

`internal/service/workflow.go:411-413`：

```sql
SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision WHERE goal_id=?
```

`gate_decision` 只有 goal 级 `ResolveReview` 会写（`internal/service/goal.go:2143`）。
蓝图 gate 的决策走 `verification_result`（`internal/service/blueprint.go:2646`、`:2739`）
和 `blueprint_node.resolved_config`（`blueprint.go:2710-2735`，含 `action/decided_by/decided_at`），
**不写 gate_decision**；而 `blueprint_node` 表（`internal/store/schema.sql:273-284`）
只有 `created_at`，没有 `decided_at` 列。

后果：蓝图流程里人工审批（最频繁、最可能占 lead time 大头的那部分）在
`human_wait_ms` 里恒为 0，规则只在“交付评审”上触发。2026-11-21 的
蓝本自迭代事故里 human_wait 能触发 3 次，是因为那几个 goal 最终都走了 goal 级评审，
而不是因为 gate 等待被度量到了。

**修法**：沿 `blueprint_node` → gate 节点的 `created_at` 与
`resolved_config.decided_at`（或对应 `verification_result.created_at`）求
“gate 开门 → 人决策”的等待，计入 `human_wait_ms`；同时把 gate 的 park/timeout
等待（等超时自动动作）单独记 `gate_timeout_wait_ms`，避免把“没人看”算成“人在看”。
分母 `wall+human` 可以保留（它表示 lead time），但文档应改成“占 lead time 比例”，
并加绝对下限（如 ≥10min）防止 1 分钟等待的 30% 噪声。

### 2.3 serial_chain：把验证 hop 当成了串行工作节点

`internal/service/rsi_analyze.go:266`：`depth := len(g.Critical)`，
而 `g.Critical` 是“依赖约束下的最长链”，**包含 verify/consult/integrate 节点**。

探针（真实 `computeWorkflowMetrics`，2 task + 2 verify，30/10/30/10 分钟）：

```
critical=[sg:a v:a sg:b v:b] cp=4800000 wall=4800000
```

于是 `depth=4` 命中阈值，但真实串行**工作**只有 2 个任务。
在“每个任务都有 verifier run”的蓝图流程里，串行链规则会大量误报；
反过来，纯任务链 4 个节点才是它想抓的场景。

**修法**：只数 `Kind=="agent"` 的链上节点；阈值保持 4（“4 个串行任务”），
并加研究稿里的占比条件 `cp/wall ≥ 0.6`（排除“排队等待造成的长链”）。

### 2.4 split_candidate：分母用 cp，且被验证开销稀释

当前判据（`:281-283`）：关键路径上 `Kind=="agent"` 且 `DurationMS/cp ≥ 0.45`
的节点 ≥2 个。

- 两个重型节点各自 ≥45% cp ⇒ 两者合计 ≥90% cp，留给链上其他节点的
  （含 verify）≤10%。也就是说：**只要每个任务配了 >~11% 自身时长的验证，
  这条规则就数学上不可达**。两个 30 分钟任务各带 10 分钟验证时，
  每个任务只有 30/80=37.5% 的 cp 占比，规则不触发——即使这两个任务
  正是该拆的对象。
- 分母用 cp（依赖下界）会把验证 hop 算成“任务时间”，稀释重型节点的份额。
- `docs/RSI.md:73` 写的 `cp=0 回退 wall` 在代码里不存在（`:282` 要求 `cp>0`）。

**修法**：分母改成 **agent 节点总忙时**（新增指标 `agent_busy_ms` = work_sum −
verify − consult − integrate），单节点占 agent_busy ≥0.5 即单独成 finding
（`ruleSplitCount=1`），或“两个节点合计 ≥0.75 agent_busy”。
这样两个 30m 任务 + 各 10m 验证的场景恢复为 50%/50%，能正常触发；
四个等长任务则各 25%，不误报。

### 2.5 评分与排序不可比

| 规则 | Score 公式 | 典型量级 |
| --- | --- | --- |
| serial_chain | `depth*10` | 40–80 |
| split_candidate | `len(heavy)*25` | 50 |
| lost_parallelism | `gain*15` | 30–60 |
| merge_candidate | `len(leaves)*8` | 24 |
| wrong_assignee | `imbalance*20` | 40 |
| ping_pong | `pairs*15` | 45 |
| brief_quality | `rework*40` | 20–40 |
| consult_repeat | `late*10` | 30 |
| retry_amplification | `totalAttempts*10` | 20+ |
| human_wait | `human_wait_ms/1000`（**秒**） | 600–3600 |
| plan_deviation | 固定 80 | 80 |

`rsi_analyze.go:73` 按 score 降序，`rsi_report.top_category/top_title` 取第一条
（`internal/service/rsi.go:601`）。因此**只要 human_wait 触发，几乎必然霸榜**；
`plan_deviation` 固定 80，能压过 <80 秒的人工等待以及其余所有规则（上限 80）。
这不是“最严重的问题排最前”，而是“量纲最大的排最前”。

**修法**（保持简单、可解释）：`score = severity_base + magnitude_bonus`，
`severity_base` = critical 90 / warn 60 / info 30（plan_deviation 的
high→critical(90)、medium→warn(60)，统一到 `critical/warn/info` 词表），
`magnitude_bonus = round(10 * clamp01(magnitude / rule_scale))`。
这样同严重度内部可比，跨严重度按严重度排序，UI 的“高危/中/低”与 `top_category`
语义一致。同时把 `plan_deviation` 的 `medium/high` 映射到
`warn/critical`（或扩展 `web/lib/types.ts` 的 `RsiSeverity` 联合类型）。

### 2.6 其他口径小问题

- `serial_chain` 的 Explanation 文案写“整图 work/critical = X”，实际打印的是
  `parallel_speedup = work/wall`（`rsi_analyze.go:271`）——文案与数值不符。
- `human_wait` 的常量注释写“of wall clock”，实现的 share 分母是 `wall+wait`（lead time 占比）；
  公式本身合理（docs/RSI.md:56 写对了），要改的是注释与判据文案。
- `verify_overhead_ratio` 的分母是 `work_sum`（含 consult），会把咨询时间算进
  “验证开销”的稀释项；量级影响不大，但定义要写清。
- `consult_count` 是 consult 节点数（guest run 数），不是“咨询次数”；
  `consult_repeat` 与 `ping_pong` 又直接查 `consult_request` 行，
  两个口径不要混用。

## 3. 规则逐条审核

| # | 规则 | 现判据 | 实际语义 / 问题 | 建议判据 |
| --- | --- | --- | --- | --- |
| 1 | `serial_chain` | `len(Critical)≥4` | 含 verify/consult hop → 2 个任务+验证即触发；无占比条件，排队链也报 | 链上 **agent** 节点 ≥4 且 `cp/wall ≥0.6` |
| 2 | `split_candidate` | Critical 上 2 个 agent 节点各 ≥45% cp | 分母被验证 hop 稀释，任务带 >11% 验证即不可达；文档写的 cp=0 回退实现里没有 | 分母改 `agent_busy_ms`；单节点 ≥50%（或 2 节点合计 ≥75%）；`ruleSplitCount=1` |
| 3 | `lost_parallelism` | `work/cp≥2` 且 `speedup<gain*0.7` | 等价“串行损失 >30% wall”，但把派发/排队等待也算成“串行化”；0.7 未经校准 | 保留 gain 2.0；用 `serialization_loss/wall>0.30` + `agent_busy/work≥0.50`；dep/dispatch 等待只进 evidence，并据此分支建议 |
| 4 | `merge_candidate` | ≥3 agent 叶节点，end 散布 ≤0.6×max 时长 | 可能建议合并两个各 40min 的并行大节点；合并的收益（省 1 次派发/验证）与代价（并行转串行的 `Σ被合并叶时长`）从未比较 | 保留 3/0.6；改为“合并增量 `added ≤ min(5min, 10% wall)`”，其中并行叶 `added = Σ被合并叶时长`、窗口不重叠（已串行）时 `added = 0`；同 assignee 或同 verifier |
| 5 | `wrong_assignee` | `lane_imbalance≥2` 且 `lane_count≥2` | 2 lane 数学不可达；空闲者不计入；验证/咨询污染 lane | lane 按 squad roster 零填充、仅 agent 节点；最忙 ≥2× 其他 lane 中位数，或最忙份额 ≥0.6 且 ≥3 lane；最忙 ≥5min |
| 6 | `retry_amplification` | max Attempt ≥2 | `attempt` 是“总尝试数”，≥2 即“重试过一次就报”；与常量注释“attempts ≥2× node count”也不符 | `attempt ≥3`（即 2 次重试；若要 3 次重试则 ≥4）或 baseline+2；score 归一化 |
| 7 | `ping_pong` | 同 requester→target ≥3 次 | 单向计数；“owner 问一次专家”这类正常咨询也可能凑满 3 次；requester 为空的 owner 咨询混入 | 要求双向：≥2 个来回（每向 ≥2、合计 ≥4）；排除 `requester_agent_id=''` |
| 8 | `brief_quality` | `rework_ratio≥0.5` 且有 verify 节点轮数≥2 | 单个固执任务 1/2 被拒即触发；没有“多少个任务受影响”的界 | 受影响 sub_goal ≥2 且被拒轮次 ≥3（或单任务 Reworks≥2）；保留 0.5 |
| 9 | `consult_repeat` | 中途咨询（requester run 有 sub_goal）≥3 | 3 次澄清是正常设计行为，不是“重复咨询”；研究稿要求跨 goal 复现 | 同一 sub_goal ≥2 次，或同模式跨 ≥2 个 goal 复现（L2），否则只作观测 |
| 10 | `human_wait` | `share≥0.30` | 口径漏蓝图 gate（§2.2）；30% 是 lead time 占比，未经校准 | 先修口径；保留 0.30，加绝对下限 ≥10min；gate timeout 单列 |
| 11 | `plan_deviation` | 任一 deviation | 无阈值，任何计划偏离都报；固定 80 分压过其他规则；severity 词表跑出类型联合 | 保留“有偏离即报”的不变量语义；score/severity 归一化 |

补充两个值得单独成 finding 的信号（当前未覆盖，属于“规则扩容”而非“调阈值”）：

- `over_verification`：研究稿有指标判据（verify_overhead≥0.25 且 rework<0.05），
  实现里只有指标没有 finding。
- **派发等待**：2026-11-21 的 `lost_parallelism` 实际上是 0 个 sub_goal、
  工作全走 consult 造成的队列等待；建议把 dep/dispatch 等待写进 lost 的
  evidence 并据此改写建议，或单独作为一条 finding，别让 lost_parallelism 背锅。

### 3.1 算例：lost_parallelism 的 0.7 系数换成了什么

旧判据可以等价变形：

```
gain    = work / cp
speedup = work / wall
speedup < 0.7 × gain
⇔ work/wall < 0.7 × work/cp
⇔ wall > cp / 0.7 ≈ 1.43 × cp
⇔ (wall − cp) / wall > 30%
```

`wall − cp` 里混着三样东西：**依赖等待**（依赖声明过强、本可并行却串行）、
**派发等待**（run ready 了但没人认领）、**调度抖动**。规则想抓第一样，
实际却对三样一起报警。

| 场景 | work | cp | wall | gain | speedup | 旧规则 | 新规则 | 真实病灶 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 4 个独立 30m，立即派发 | 120m | 30m | 120m | 4.0 | 1.0 | 报 | 报（损失 75%，agent 占比 100%） | 只有一个 lane，缺并行容量 |
| 2 个独立 30m，派发被拖 90m | 60m | 30m | 120m | 2.0 | 0.5 | 报 | 报（损失 75%，agent 占比 100%，dispatch=90m 进 evidence） | 派发链路，建议应指向派发 |
| 3 个链式 20m | 60m | 60m | 60m | 1.0 | 1.0 | 不报 | 不报 | 依赖是真的 |

因此新判据 = `gain ≥2`（值得并行的量级门槛）+
`serialization_loss/wall >0.30`（损失够大）+
`agent_busy/work_sum ≥0.50`（工作是真实执行，不是验证/咨询开销）。

为什么不再用 `dispatch_wait/wall` 当否决条件：它的口径是 `start − ready`，
包含“ready 了但在等唯一的 lane 空出来”的时间。单 lane 串行——恰恰是最典型的
lost parallelism——会被误杀。dep/dispatch 等待仍写进 evidence，并据此分支建议：
dispatch 主导 → 派发链路；dep 主导 → 依赖声明过强/重排；`lane_count=1` → 缺并行容量。

2026-11-21 事故里的两次 `lost_parallelism`：0 个 sub_goal、工作全走 consult，
`agent_busy/work` ≈0，新护栏会直接压掉，交给 plan_deviation/咨询类 finding。

### 3.2 算例：wrong_assignee 的“2 倍”为什么永远到不了

旧公式 `imbalance = max / (sum/k)`，k = `len(lanes)`（只含“有耗时的 lane”）。

| lanes（A/B/C，分钟） | k | mean | max/mean | 旧规则 | 新规则（2× 其他有耗时 lane 中位数） | 真实情况 |
| --- | --- | --- | --- | --- | --- | --- |
| 90 / 10 / 0 | 2（C 不在） | 50 | 1.80 | 不报 | 90 ≥ 2×10 → 报 | C 闲死，该报 |
| 100 / 0 / 0 | 1 | 100 | 1.00 | 不报（k<2） | 退化：份额 1.0 → 报（squad≥2） | 单人吞活，该报 |
| 40 / 30 / 30 | 3 | 33.3 | 1.20 | 不报 | 40 < 2×30 → 不报 | 均衡，正确 |
| 60 / 20 / 20 | 3 | 33.3 | 1.80 | 不报 | 60 ≥ 2×20 → 报 | 明显偏斜，该报 |
| 50 / 25 / 0 | 2 | 37.5 | 1.33 | 不报 | 50 = 2×25 → 边界报 | 偏斜 |

k=2 时 `max/mean = 2·max/(max+min) < 2` 恒成立，旧阈值只能靠“最小 lane 趋近 0”
逼近 2；而真闲的人根本不在 lanes 里。用“其他**有耗时** lane 的中位数”而不是均值，
因为均值把最忙 lane 自己也算进分母（2 个 lane 时被稀释），中位数对单点异常稳健；
零耗时的成员只参与 lane 数量和份额，不进中位数（否则 2×0=0 会恒报）。

### 3.3 算例：合并末梢——min / max / sum 各是什么，限制的到底是什么

设一次 fan-out 有 n 个叶节点（无出边），时长 d₁…dₙ，它们几乎同时收尾。三个量：

| 量 | 定义 | 在合并里的角色 |
| --- | --- | --- |
| `max` | 最长的那个叶 `max(dᵢ)` | **保留者（keeper）**：合并后它继续执行，其余叶的活串到它后面 |
| `sum` | 所有叶时长之和 `Σdᵢ` | 只有**把所有叶都并成一个**时，墙钟才变成 `sum`；它不是合并的代价 |
| `min` | 最短的那个叶 `min(dᵢ)` | **受害者选择顺序**：从最短的开始并，每分钟增量最便宜 |
| `spread` | 最后一个结束时间 − 第一个结束时间 | 判定“几乎同时收尾”；用**结束时间**，不是时长（别和 max 混） |

合并前的墙钟（并行、同时收尾）≈ `max`。把受害者集合 V 并进 keeper 后，
keeper 的窗口变成 `max + Σ_{i∈V} dᵢ`，所以墙钟增量：

```
added = Σ_{i∈V} dᵢ          （V = 被合并的叶）
```

- 只并最短的 1 个叶：`added` 就是那个叶的时长（≈`min`）；
- 并 2 个最短叶：`added = 两个最短叶之和`；
- 把除 keeper 外全部并掉：`added = sum − max`——**这只是 `sum−max` 成立的特例**，
  不是通式（代码最多并 2 个受害者）。

具体例子：4 个叶，全部在 t≈30min 收尾。

| 叶 | 执行窗口 | 时长 dᵢ |
| --- | --- | --- |
| A | 0–30min | **30min**（keeper） |
| B | 16–28min | 12min |
| C | 25–29min | **4min** |
| D | 27–29min | **2min**（min） |

`max=30min`，`sum=48min`，`min=2min`；结束时间 30/28/29/29 → `spread=2min`，
`2/30=0.07 ≤0.6`，通过。预算 `added ≤ min(5min, 10%×wall)`；
合并前 wall=30min → 10% = 3min，预算 3min。

| 方案 | added | 与预算 | 结果 |
| --- | --- | --- | --- |
| D→A（并最短的） | 2min | 2 ≤ 3 | 接受：wall 30→32min，省 1 次派发+验证（约 1–2min），基本中性 |
| C→A | 4min | 4 > 3 | 拒绝：为省 1 次固定开销多串行 4min，不划算 |
| C,D→A | 6min | 6 > 3 | 拒绝 |
| B,C,D→A（全并） | 18min = sum−max | 18 ≫ 3 | 拒绝：墙钟 30→48min |

如果该 goal 的 wall=100min，预算变成 `min(5min, 10min)=5min`，C→A（4min）就划算，
C,D（6min）仍不行。

注意几点：

- keeper 取 max 而不是 min：取 min 会得到 `added = sum − min > sum − max`，更差；
- `min` 只是排序依据，不是门槛：把 2min 的叶并进 30min 的 keeper，增量小、
  省一次验证，正是划算的典型；
- `added` 的意义是“用固定开销（派发/验证/集成，估 1–2min/节点）换串行时长”，
  所以上限同时写绝对值和相对值（5min、10% wall），两者取小；
- 若叶窗口本来就不重叠（已串行），合并不改变那条 lane 的总时长，`added=0`，
  只省开销；但这类叶基本过不了 `spread ≤0.6` 的筛选，所以是兜底分支；
- 旧规则只看 `count≥3` 和 `spread≤0.6`，根本没算 added，所以会提出
  “把 4 个叶全并了”这种加 18min 的坏方案；新规则把成本显式化。

## 4. 建议的新常量与口径

```go
// structure
ruleSerialChainThreshold = 4     // 只数 agent 节点（保留 4）
ruleSerialChainWallShare = 0.60  // 新增：链时长占墙钟 ≥60%
ruleSplitCount           = 1     // 单节点即可成 finding
ruleSplitShare           = 0.50  // 分母改为 agent_busy_ms（新增指标）
ruleSplitPairShare       = 0.75  // 可选：两节点合计占 agent_busy 的比例
ruleLostParallelGain     = 2.0   // 不变
ruleLostSerialShare      = 0.30  // 替代 0.7 系数：serialization_loss/wall >0.30
ruleLostAgentBusyShare   = 0.50  // 新增：agent_busy/work_sum ≥0.50（排除 consult/验证占主导）
ruleMergeWall            = 3     // 不变（叶节点数）
ruleMergeSpread          = 0.60  // 不变
ruleMergeAddedWallMS     = 300000// 新增：合并串行增量 ≤5min（=Σ被合并叶时长）
ruleMergeAddedWallShare  = 0.10  // 新增：且增量 ≤10% wall

// dispatch
ruleLaneImbalance        = 2.0   // 语义改为“最忙 / 其他 lane 中位数”
ruleLaneMinBusyMS        = 300000// 新增：最忙 ≥5min 才报
ruleRetryAmplification   = 3.0   // 3 次 attempt = 重试 2 次

// info-flow
rulePingPongPairs        = 3     // 保留，但要求双向且每向 ≥2
ruleReworkRatio          = 0.5   // 不变
ruleReworkMinSubGoals    = 2     // 新增
ruleReworkMinRejects     = 3     // 新增
ruleConsultRepeat        = 3     // 保留；同 sub_goal ≥2 或跨 goal 复现

// observation
ruleHumanWaitShare       = 0.30  // 保留（口径修复后重标）
ruleHumanWaitFloorMS     = 600000// 新增：≥10min
```

理由统一为三条：**分母改用不掺验证/咨询的“纯执行工作量”（agent_busy）或不含排队假象的
墙钟，而不是被验证 hop 稀释的 cp；阈值落在“业务上值得看一眼”的最小可行动幅度；
每条都有绝对下限防抖**。

## 5. 校准协议（P2，避免再拍脑袋）

1. **先有数据再谈数值**。`rsi_round` 已冻结每轮 metrics/baseline/findings，
   同 domain 的 ready 会话 ≥5 条后用 baseline（文档：中位数，<5 为空）。
   每个规则的量（depth、lane 份额、串行损失比、重试次数、等待占比）取
   **同 domain 历史 p50/p90/p95 与 MAD**，阈值放在：
   - 稀缺资源规则（split/merge/wrong_assignee）：p90 或 median+2·MAD；
   - 观测类（human_wait/lost_parallelism）：p90，且只在“绝对代价 > 阈值”时升级为 finding。
2. **用 finding 状态做精度反馈**：`rsi_finding.status ∈ {open, confirmed, dismissed}`
   已有，但从未回流。按类别统计 confirmed/(confirmed+dismissed)，
   精度 <40% 的规则先提高阈值或降级为观测。
3. **回放验证**：拿 10–20 个历史 goal 的 `workflow_json` 离线重算
   （规则是纯函数，无需真实执行），对比新旧判据的触发率与 top_category 变化；
   阈值改动必须写进 `docs/RSI.md` 判据表并配单测（每条规则一个合成图）。
4. **A/B 指标**：`ABMetricKeys`（`internal/service/rsi.go:901-909`）目前没有
   `serialization_loss`、`lane_imbalance`，而这两项正是本次要改的规则依据，
   建议加入，否则改动无法在 A/B 面板上验证。
5. **不要整批同时改**：先修 P0 口径（不改阈值）跑一周，确认指标可信，再按 §4
   分批调阈值；每批保留 rollback（常量回退即可）。

## 6. 文档/代码不一致清单（一并修）

1. `docs/RSI.md` split_candidate 的“`cp=0` 时回退 `wall_ms`”不存在于实现。
2. `docs/RSI.md` serial_chain“explicit 关键路径节点数 ≥4”没说明包含 verify/consult。
3. 两个“提案上限”不是一回事，但数字互相打架：playbook §5 的人工提案上限 3 条/轮，
   代码的 `translatorMaxPerRun=4` 是自动 draft/次；文档要写明各自口径。
4. `ruleRetryAmplification` 注释“attempts ≥2× node count” vs 实现“max Attempt ≥2”。
5. `serial_chain` Explanation 文案“work/critical” vs 数值 `work/wall`。
6. `web/lib/types.ts:813` `RsiSeverity` 不含 `medium/high`，但 plan_deviation 会写。
7. `web/components/training/format.ts` 的 L2 文案“单 run 统计：同一 run 内多次出现”
   与代码/文档（L2 = 低于同 domain baseline）矛盾。

## 7. 复现证据

- 常量溯源：`for k in <12 个常量>; do git log --oneline -S "$k" -- internal/service/rsi_analyze.go; done`
  → 每个都只输出 `4289095 workflow rsi mvp version`。
- MVP 的 lost_parallelism 不可达：`git show 4289095:internal/service/workflow.go | grep -n "speedup ="` 
  → `:533 speedup = float64(workSum) / float64(cpMS)`；同提交 `rsi_analyze.go:269-270`
  → `gain := work/cp; if gain >= 2.0 && speedup < gain*0.7`，两值恒等。
- 关键路径含 verify：临时探针（已删除）用 2 task + 2 verify 构造图，
  `BuildGoalWorkflow` 后打印 `critical=[sg:a v:a sg:b v:b]`、`cp=wall=4800000`。
- 本地库无校准样本：`cp ~/.ouroflow/ouroflow.db* /tmp/` 后用 sqlite 查
  `goal/run/sub_goal/rsi_report` 均为 0 行。
## 8. 实施状态（2026-11-24，P0+P1 已落地）

用户确认（2026-11-24）：merge 用 §3.3 的 added 口径、retry 用 `attempt ≥3`、P0+P1 一起做，
并追问“两条规则是否会撞脚”。以下是代码里实际落地的口径（与 §3/§4 的差异已在括号内注明）。

**P0 指标口径**
- `serial_chain`：只数 explicit 关键路径上的 **agent** 节点（verify 跳点不再计入），
  并要求该链占 `wall_ms ≥ 60%`。
- `lanes`：按 squad 名册（leader + `squad_member` agent；agent 型 goal 即该 agent）
  **零填充**；lane 只累计 `Kind=="agent"` 的 busy（verify/consult/integrate 移出）；
  新增 `agent_busy_ms / agent_busy_share / verify_busy_ms / consult_busy_ms / squad_size`。
- `human_wait_ms = Σ gate_decision.review_duration + blueprint gate 持有者 run busy`
  （新增 `gate_wait_ms`）；gate 持有者不再作为 agent 节点进入 work/lane/wall。
- 评分统一：`severity 基数（critical 90 / warn 60 / info 30）+ 10×clamp01((观测−阈值)/阈值)`；
  `plan_deviation` 用同一刻度（severity high→critical、medium→warn）。
- `ABMetricKeys` 增加 `serialization_loss_share / human_wait_share / gate_wait_ms /
  lane_imbalance / agent_busy_ms`；baseline 键同步扩展。

**P1 阈值/判据**
- `split_candidate`：分母改为 `agent_busy_ms`，阈值 50%，`ruleSplitCount=1`（每个重型节点都是候选）。
- `lost_parallelism`：保留 `gain ≥2.0`，新增 `serialization_loss_share > 0.30` 且
  `agent_busy_share ≥ 0.50`（用 agent 工作占比排除“纯派发/等待”场景；原计划的
  `dispatch_wait/wall ≤0.25` 会误杀单通道排队，未采用）。
- `merge_candidate`：同一 assignee 与 verifier；被合并叶时长累加 `added ≤ min(5min, 10%×wall)`；
  spread ≤ 0.6×最长叶不变。
- `wrong_assignee`：名册 ≥2、最忙 ≥5min，触发条件 = 其余 lane 中位数为 0，或最忙 ≥2×中位数，
  或（最忙占比 ≥60% 且名册 ≥3）。
- `retry_amplification`：`attempt ≥3`（= 2 次重试）。
- `ping_pong`：同一对 **双向** 各 ≥2 次（合计 ≥4）。
- `brief_quality`：受影响 sub_goal ≥2 且被拒轮次 ≥3，或单任务被拒 ≥2（保留 rework ≥0.5）。
- `consult_repeat`：同一 sub_goal ≥2 次。
- `human_wait`：`share ≥0.30` **且** `human_wait_ms ≥10min`。
- 译文器（`internal/service/rsi_translate.go`）同步采用上述口径（split 用 agent busy、
  merge 用 added 预算、wrong 用 5min 下限、retry 用 3、ping 双向、consult 按 sub_goal、
  human 加 10min 下限）。

**规则撞脚（本次新增的防撞机制）**
- 同一轮内：每个 draft 记录其触碰的节点/边身份（`patchKeys`：`n:<id>`、
  `e:<src>\x00<handle>\x00<tgt>`）；后一个 draft 与已触碰集合相交即跳过（Debug 日志）。
- 跨轮：启动时会读取同一 blueprint 上 `pending/approved` 提案的 `patches_json`，
  把它们触碰的身份并入 touched 集合，避免与在线提案重复改同一个节点。
- 保留的串行优先级：`lost_parallelism` 在同轮已有 `serial_chain` 时让位；结构类
  （fork/split/merge/human pack）先于简报类（brief/retry/consult/ping/plan_deviation）。
- **仍存在的限制**：两个提案若 base 相同，批准 P1 后再批准 P2 发布时 P1 的改动会丢失
  （`ApproveProposal` 不做 base 新鲜度检查）。这是工作流层问题，不是翻译器能解决的；
  建议操作上“批准→发布→再批准下一条”，或后续给 ApproveProposal 加 stale-base 拒绝/重算。

**验证**：`go build ./...`、`go vet ./internal/service/`、`go test ./internal/service -count=1`
全绿；新增 `TestTranslatorCollisionGuard`（brief_quality + retry_amplification 改同一节点时
只产 1 条提案并可通过 ApproveProposal 的 V1–V6 校验）。
