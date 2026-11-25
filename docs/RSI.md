# W-RSI：工作流级训练模式与训练分析

W-RSI（Workflow-level Recursive Self-Improvement）把 flowloop 的一次 goal 执行过程冻结成一张**工作流编排图**（DAG），在任务结束后异步分析编排结构、分派与信息流，产出只读的优化提案与复测计划。**这里不训练模型权重**，也不改 agent harness 内部（prompt/工具/上下文策略由各 agent 自治）；优化对象是**编排层**：节点怎么拆、依赖怎么连、任务派给谁、信息怎么流转。

设计文档（论点、指标体系、Coze 教训、分期路线）见 `docs/research/rsi-summary.md`。

## 使用

1. 新建任务时选择运行模式：
   - **推理**（默认，`execution_mode=infer`）：行为与以前完全一致，零采集。
   - **训练**（`execution_mode=train`）：goal 终态（`done` / `failed`）时把全部编排事实冻结成 `workflow_json`，随后异步分析。
2. 训练高级项：
   - `content_capture`：`none` / `redacted`（默认）/ `full`。注意 v2 起图装配只依赖结构化事实（run/verification/consult/change/gate 行），`full` 不再采集工具正文，仅作为未来节级深挖的预留位。
   - `budget_usd`：会话预算上限（默认 2，当前只记录与展示，超过后不强制中止）。
   - `sample_rate`：采样率，`0 < rate <= 1`；未命中采样时会话标记 `skipped`。
3. 左侧「训练分析」(`/training`) 查看全部会话；点进详情看概览、发现与编排图。
4. **任务洞察面板**：「洞察」tab 展示**优化轮次账本**——goal 终态自动分析是第 1 轮（`terminal`），人工修完问题后点「重跑分析」产生复测轮（`manual_retry`），每轮冻结当时的指标/基线/发现，并给出与**同 domain 上一条 `rsi_round`**（可跨会话）的 **delta 对比**（加速比↑、返工率↓…即优化是否生效的结果展示）。训练列表页的趋势条逐条取自 `rsi_round` 账本行（跨会话按 `created_at` 排序；同一会话人工重跑也各算一点），**不是每个 R<n> 一个点**。
5. 编排可视化（只读，无需训练模式）：
   - goal 详情页「编排」区：`GET /workflows/goals/{goalId}` 实时装配该 goal 的 DAG。
   - squad 详情页「编排」区：`GET /workflows/squads/{squadId}` 近期 goal 的编排摘要表。

## 数据模型

| 表 | 作用 |
| --- | --- |
| `training_session` | 一次训练会话，与 goal 一对一；状态机 `waiting → analyzing → ready / failed / skipped`；冗余 `node_count / critical_path_ms / parallel_speedup / finding_count` 供列表直读 |
| `workflow_edge` | **预留表**：当前 Go 代码没有任何读/写路径（`rg workflow_edge` 只命中 schema/设计文档），DAG 边在读取时由 `sub_goal.depends_on` 与 run/verification_result/consult_request/change 实体现场装配；表内 `source` 列（explicit/inferred）是设计预留，不代表落库事实 |
| `rsi_report` | 最新一轮分析的指标 `metrics_json`、基线 `baseline_json`、冻结图 `workflow_json` 与中文叙述（每次分析重建） |
| `rsi_finding` | 规则发现：类别、严重度、置信度（L1/L2/L3）、证据、建议、确认状态（最新一轮） |
| `rsi_round` | **优化轮次账本**（洞察面板数据源）：每次分析追加一行——`round_no` 按 **domain** 递增、`trigger`（terminal/manual_retry）、冻结 metrics/baseline/findings、与同 domain 上一条账本的 `delta_json`；趋势数据由它逐行派生 |

- `sub_goal` 新增 `depends_on`（JSON 数组，leader 分派时声明；只存声明，不落 `workflow_edge`）；`sub_goal.id` 即图节点键 `sg:<sub_goal_id>`。
- `trace_span` 已在 v2 退役（迁移时 `DROP TABLE IF EXISTS`）；重试/人工等待改用实体级事实（`run.attempt`、`verification_result`、`gate_decision`）。

图节点键（代码实际生成）：`goal`（根）｜`sg:<sub_goal_id>`（执行节点）｜`v:<sub_goal_id>`（验证节点，注意不是 `verify:`）｜`review:<run_id>`｜`consult:<run_id>`｜`chg:<change_id>`（集成节点）。
边类型：`created`（goal→首节点）｜`depends_on`（显式依赖）｜`seq`（时间窗不重叠推断，`source=inferred`，**不参与关键路径**）｜`verified_by`｜`consult`｜`integrated`。

## 指标口径（workflow 级）

指标由 `internal/service/workflow.go` 的 `computeWorkflowMetrics` 在读取时从节点/边计算；JSON 键名与单位以 API 返回为准（时间为毫秒）。

- `metrics_schema_version`：口径版本（当前 `2`）。v2（issue #13）把 `serialization_loss_ms` 更名 `idle_gap_ms`，并让关键路径穿过无窗节点；v1 与 v2 的 `critical_path_ms / depth / idle_gap_ms` **不可直接比较**，趋势对比必须按版本分段。
- `node_count / edge_count / depth`：节点数、边数、关键路径上的**工序数**（`depth = len(critical_path)`）。0 busy 贡献的节点（`goal` 根、从未执行的子目标、无计时的机器验证轮）可被链穿过但不出现在路径里——把「没花的墙钟」算成一个工序会让 depth 虚高。
- `work_sum_ms`：agent + verify + consult + integrate 节点的 busy 时长之和；**不含 review 与 blueprint gate 决策持有者**（gate 等待单列，见 `gate_wait_ms`）。
- `wall_ms`：上述工作节点窗口的 max(end) − min(start)；不含 review 与 goal 根节点。只统计已有结束时间的节点，**仍在运行的 run 不计入**（所以运行中的编排 `wall_ms` 可能为 0）。
- `wall_span_ms`：编排跨度 = 最早就绪时刻（`sub_goal.created_at`，被依赖结束推后）→ 最后一个 sub_goal run 结束；仍有 run 在飞时右端取 `now`。它是排队等待的收敛上限——`wall_ms` 只含已结束节点，不能当上限（#11）。
- `critical_path_ms`：只沿 explicit/结构边（`created` / `depends_on` / `verified_by` / `consult` / `integrated`）做最长链 DP，并要求前驱结束 ≤ 后继开始；**排除 `source=inferred` 的 `seq` 边**，review 节点也不入链。它是「依赖约束下的最短墙钟」，不是速度指标。DP 按 explicit 边的**拓扑序** relax（时间序只做 tie-break），因此无窗节点（从未执行的子目标、`duration_ms=0` 的审计工序）以 0 贡献**穿链**、不会把上游工序甩出路径；也不为无窗节点造估算窗口。
- `idle_gap_ms = max(0, wall_ms − critical_path_ms)`：墙钟里未被依赖链解释的**空闲间隙**（派发/门控/抖动）。它**不是串行损失**——依赖内必需的串行是必需劳动，不是损失（issue #13, D1）。`idle_gap_share = idle_gap_ms / wall_ms`：空闲间隙占墙钟比例，是 `lost_parallelism` 的判据。
- `serialization_loss_ms`：**deprecated 别名**，与 `idle_gap_ms` 同值，仅为旧读者（`rsi.go` 的 `SerializationLossMS`、`ABMetricKeys`、round delta 键）保留；新代码与新文档一律用 `idle_gap_ms`。- `parallel_speedup = work_sum_ms / wall_ms`（`wall_ms=0` 时为 0）：**实际吞吐加速比**，串行 = 1.0；两个 30min 独立任务并行，wall=30、work=60 → 2.0。旧公式 `work_sum / critical_path` 已废弃，不再作为速度展示。
- `parallel_width_peak`：工作节点区间扫描的并发峰值（不含 review）。
- `verify_overhead_ratio = verify busy（已度量）/ work_sum_ms`；`review_busy_ms` 与 `review_overhead_ratio = review busy / work_sum_ms` 单独统计（验证与评审分开，评审不再混入验证开销）。
- 验证轮次的度量范围（issue #13, D2）：机器验证轮在 daemon 内完成、与执行 run 共用时间窗，账面上没有可分离的计时，所以分子只算「已度量」的验证忙时，并另发覆盖率与 scope，让 0% 读作「没量到」而不是「验证免费」：
  - `verify_rounds`：verify 节点 attempt 之和（验证轮数）。
  - `verify_measured_rounds` / `verify_unmeasured_rounds`：分子/分母的拆分；二者之和恒等于 `verify_rounds`。已度量 = 有验证 run 窗，或机器轮 `verify_ms > 0`（phase 2 计时）；未度量 = 机器轮未计时、或 verify 的 run 窗不可读。
  - `verify_busy_measured_ms`：已度量验证忙时（= `verify_overhead_ratio` 的分子）。
  - `verify_coverage_ratio = 已度量轮次 / 轮次总数`（0..1）。
  - `verify_overhead_scope`：`complete`（全部轮次已度量）/ `partial` / `unmeasured`（一轮都没量到；无验证轮次时也是它）。UI 在非 `complete` 时把数值位显示为「未度量」。
  - `verification_result` 新增 `verify_ms`（0 = 未计时，phase 2 填充）与 `machine_run_id`（机器 gate 判定的 run）；`verifier_run_id=''` 仍是**机器轮唯一标记**，不复用。
- `rework_ratio = rejected verify 轮数 / verify_rounds`（分母是验证轮数，不是任务数）。
- `consult_count`：consult 节点数。
- `agent_busy_ms / agent_busy_share`：只统计 **agent** 节点的 busy（verify/consult/integrate 不计）；share = agent busy ÷ `work_sum_ms`。
- `verify_busy_ms / consult_busy_ms`：验证者与咨询 guest 的 busy，单独统计，不进 lane。
- `lane_count / lane_imbalance`：按 assignee 分桶的 **agent** 负载；桶来自 squad 名册（leader + `squad_member` 的 agent；agent 型 goal 就是该 agent）并**零填充**，未执行任何工作的 agent 也在桶里且计 0；imbalance = 最忙 lane ÷ 名册均值。
- `dependency_wait_ms`：sub_goal 创建 → 其依赖全部 run 结束（ready）的**累计**等待，衡量依赖门控；累计口径，可超过墙钟。
- `dispatch_wait_ms`：ready → 首个 sub_goal run 开始的**累计**等待，衡量派发链路；累计口径，超过 `wall_span_ms` 的部分会被收敛（见下）。
- `queue_wait_ms`：`dispatch_wait_ms` 的 **legacy 兼容别名**（两者相等）。v2 之前的旧报表把它定义为 `dependency_wait_ms + dispatch_wait_ms`，该口径已 **breaking**：不再含依赖门控等待，新代码与新文档只看拆分字段。
- `queue_wait_capped`：`true` = 累计派发等待超过 `wall_span_ms`，已收敛到编排跨度（只在超限时出现）。
- `queue_wait_overlap_ms`：收敛掉的超出部分（累计值 − `wall_span_ms`），只在 `queue_wait_capped=true` 时出现。
- `unrun_sub_goals`：无任何 run 窗口、且未终态（非 `verified`/`cancelled`/`failed`）的子目标数。blueprint gate holder 恒不计——gate 由人工/自动决策 settle，构造上就没有 run；verified 节点也不计，避免把已决策节点标成「未执行」。
- `human_wait_ms`：`Σ gate_decision.review_duration × 1000`（最终交付 review）**加上** blueprint gate 决策持有者 sub_goal 的 run busy（`gate_wait_ms`，gate 决策不写 gate_decision）；`human_wait_share = human_wait_ms / (wall_ms + human_wait_ms)`（0..1，分母含人工等待，因此不会超过 100%）。- `retry_amplification`：agent 节点中的**最大** Attempt（float；无 agent 节点为 0），不是平均重试次数。
- `squad_size`：名册 agent 数（squad 取 leader + agent 成员；否则 1）。
- `lanes`：上述分桶明细（内部展示用；已包含 0 值名册项）。

基线与 delta：

- baseline = **同 domain** 最近 20 个 `ready` 会话 metrics 的中位数，键覆盖 `critical_path_ms / parallel_speedup / idle_gap_ms / idle_gap_share / dependency_wait_ms / dispatch_wait_ms / verify_overhead_ratio / review_overhead_ratio / rework_ratio / consult_count / human_wait_ms / human_wait_share / gate_wait_ms / agent_busy_ms / lane_imbalance / retry_amplification`；样本 < 5 时 baseline 为空，某键覆盖不足半数时该键不进 baseline。- delta = 当前分析 vs **同 domain 上一条 `rsi_round`**（跨 session，不要求同一会话）；`round_no` 按 domain 递增，会话首次分析 `trigger=terminal`，其后 `manual_retry`；首轮无上一条时 `delta={}`。结构为 `{key: {from, to, pct}}`（`from=0` 时省略 `pct`）。
- 「样本 <5 不给 delta」不成立：delta 只依赖上一条账本；<5 只约束 baseline。跨轮历史累计的 L2 升级当前未实现，finding 默认 L1，仅 `retry_amplification` 在超出同 domain baseline 时标 L2。

## 可优化判定（结构/分派/信息流 9 类 + human_wait 观测）

范围决策（2026-11-08）：**T1 结构 + T2 分派 + T3 信息流全做；节点内部（T4）不做主动优化；平台参数（T5）只观测；元层（T6）永不自动改。**

| 类别 | 层 | 触发条件 |
| --- | --- | --- |
| `serial_chain` | 结构 | explicit 关键路径上的 **agent** 节点数 ≥ 4（verify/review 跳点不计），且该链占 `wall_ms` ≥ 60% |
| `split_candidate` | 结构 | 关键路径上某 **agent** 节点 busy ÷ `agent_busy_ms` ≥ 50%（≥1 个即可） |
| `lost_parallelism` | 结构 | `gain = work_sum / critical_path ≥ 2.0`，且 `idle_gap_share > 0.30`，且 `agent_busy_share ≥ 0.50` |
| `merge_candidate` | 结构 | ≥ 3 个无出边的 **agent** 叶节点、结束散布 ≤ 最长叶 × 0.6、同一 assignee 与 verifier；被合并叶时长之和 ≤ min(5min, 10% × wall) |
| `wrong_assignee` | 分派 | 名册 ≥ 2、最忙 lane busy ≥ 5min，且（其余 lane 中位数为 0 ／ 最忙 ≥ 2× 中位数 ／ 最忙占比 ≥ 60% 且名册 ≥ 3） |
| `retry_amplification` | 分派 | agent 节点最大 Attempt ≥ 3（= 2 次重试；高于同 domain baseline 时升 L2） |
| `ping_pong` | 信息流 | 同一对 requester↔target **双向**各 ≥ 2 次（合计 ≥ 4） |
| `brief_quality` | 信息流 | rework_ratio ≥ 0.5，且（受影响 sub_goal ≥ 2 且被拒轮次 ≥ 3 ／ 单任务被拒 ≥ 2） |
| `consult_repeat` | 信息流 | 同一 sub_goal 执行中途咨询 ≥ 2 次 |
| `human_wait` | 观测 | `human_wait_share ≥ 0.30` 且 `human_wait_ms ≥ 10min`（含 blueprint gate 等待） |

发现默认 L1（人工复核）；`retry_amplification` 超出同 domain baseline 时标 L2。当前**没有**「同类历史 ≥ 2 次自动升 L2」的跨轮累计逻辑。产出=**只读提案 + 复测计划**，不自动执行。

评分统一到 0–100 的可比刻度：`score = severity 基数（critical 90 / warn 60 / info 30）+ 10 × clamp01((观测值 − 阈值) / 阈值)`。旧的按规则乘积打分（human_wait 用秒、plan_deviation 固定 80）会让排序与 `top_category` 失真；severity 词表统一为 `critical / warn / info`（旧 `high/medium` 仅在 Web 侧做兼容映射）。

`plan_deviation` 保持「有偏离即报」的不变量语义（score 用同一归一化刻度，severity high→critical、medium→warn）。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/rsi/sessions` | 会话列表（裸数组） |
| GET | `/rsi/sessions/{id}` | `{session, report, findings, workflow}` |
| GET | `/rsi/sessions/{id}/rounds` | 优化轮次账本（洞察面板）：每轮冻结指标 + delta 对比 |
| GET | `/rsi/overview?days=30` | 总览：会话数、成功率、P95 交付、人工等待、平均并行加速/验证开销/返工率、发现数、`trend`（逐条 `rsi_round` 账本行，含 session/goal/round_no；跨会话，非每个 R<n> 一个点） |
| PATCH | `/rsi/findings/{id}` | body `{"status":"confirmed"}` 或 `dismissed` |
| POST | `/rsi/sessions/{id}/retry` | 重新分析 |
| GET | `/workflows/goals/{goalId}` | 实时装配 goal 的 WorkflowGraph |
| GET | `/workflows/squads/{squadId}?limit=10` | squad 近期 goal 的编排摘要 |

## 采集与隐私

- 图装配只读结构化行（run / sub_goal / verification_result / consult_request / change / gate_decision），**不读取工具正文与 prompt**；`content_capture` 不再有落盘正文路径。
- 训练会话与推理会话指标分开统计，避免观测者效应；推理模式零查询。

## 限制

- 不训练模型权重；分析器目前是确定性规则 + 统计，不含 LLM 归因。
- `seq` 边由时间窗推断，run 重叠的真实依赖会被画成并行；显式 `depends_on` 声明越完整图越准。
- `budget_usd` 仅展示，超预算不会中断执行。
- token / 成本维度在 v2 移除（trace 退役）；`budget_usd` 列保留作未来节级成本回归的挂点。
- 提案不自动应用、不复测；改动需人工确认并另行 A/B 验证。

## 测试

```sh
go build ./...
go test ./...
# TestErrorCodesExported 校验后端码表本身（唯一、非空）。
# TestCodesMatchRemoteTermJSON 是 code ↔ 远端 term.json 的跨层漂移守卫：
#   - 当前基线（ae12054）：CDN 不可达 / 非 200 / 响应畸形 / 缺码都 t.Skip，只留诊断信息；
#   - fix/hardening 合入后：默认 FAIL + 到期 allowlist（未知缺码立刻 FAIL，允许清单
#     截至 2026-12-31 UTC；`FLOWLOOP_SKIP_CDN_DRIFT=1` 是离线/降级 CDN 的显式逃生口）。
cd web && npx tsc --noEmit && npm run build
```
