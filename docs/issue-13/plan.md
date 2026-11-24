# Plan（Issue #13）：实现方案与决策记录

## 决策表

| 决策 | 选项 | 选择 | 理由 |
|------|------|------|------|
| 断链修法 | A 去掉 `p.length == 0` 守卫 / B 造无窗节点估算窗 / C 改 DP 序 | **A + C** | 只去掉守卫在真实数据上**不够**：无窗节点被折到 relax 序列最前（`si=-1`），其前驱尚未 relax，`prev` 仍指不回上游（45764ms 仍丢）。拓扑序 relax 才能既穿链又不造假时长。B 会把「未执行」变成假时长，与 D2 的「未度量」原则冲突。 |
| `critical_path` 是否列出 0 贡献节点 | A 列（含 goal 根）/ B 只列有 busy 贡献的工序 | **B** | `depth` 语义是「关键路径工序数」，金值 11；把 goal 根 / 从未执行的子目标算成工序会让 depth 与真实工序数不符，且 UI 会把从未执行的节点标成「关键」。穿链机制不变（`prev` 穿过），只是不进路径数组。 |
| 拓扑序实现 | A 引第三方拓扑库 / B 就地 Kahn（时间序做 tie-break，环残留兜底）/ C 一次性 relax 后二次传播 | **B** | 图规模 10~40 节点，O(n²) 扫描足够；不引依赖；时间序 tie-break 沿用既有确定性要求（400 次迭代不许分裂）。环（脏 `depends_on`）残留按时间序追加，不 panic、不退化成空图。 |
| 「串行损失」 | A 删字段 / B 更名 + 同值别名 | **B** | `rsi.go` 的 `SerializationLossMS`、`ABMetricKeys`、delta 都按旧键消费；删字段会打断 RSI 轮次账本与同题 A/B。别名同值 + deprecated 注释，新读法用 `idle_gap_ms`。 |
| 验证轮次分类 | A 按 `verifier_run_id` 空值判机器轮并估时 / B 「有窗或有时长 = 已度量，其余未度量」 | **B** | 契约标记（`''` = machine round）语义保留；估时会虚高（owner run 窗把派发+执行整段算成验证）。未度量如实计数 + 覆盖率，不造数。 |
| 覆盖率分母 | A `verify_rounds`（Attempt 之和）/ B measured+unmeasured | **B（并以 A 兜底）** | 以轮次分类为准；若某节点声明了 Attempt 却没有任何分类（手工构造图 / 旧数据），余量按 unmeasured 兜底，保证 `measured + unmeasured == verify_rounds`。 |
| UI 未度量呈现 | A 数值留 0% + 备注 / B 数值位显示「未度量」 | **B** | 裸 0% 是「验证免费」的假信息；0% 与「未度量」在语义上必须分开。 |

## 改动文件与要点

### 1. `internal/service/workflow.go`

**a. DP（`computeWorkflowMetrics`）**

- 新增 `relaxOrder`：按 explicit 边 Kahn 拓扑序 relax，`order`（开始时间序）做同层 tie-break；环残留按 `order` 追加。
- 守卫 `if !ok || p.length == 0` → `if !ok`（无窗节点 0 贡献穿链）。
- 路径重建后只保留 `DurationMS > 0` 的工序（goal 根、无窗节点、纯机器验证轮不进数组），`depth = len(path)` 随之为真实工序数。
- 时间序有效性校验（`pn.StartedAtMS > 0 && n.StartedAtMS > 0 && ...`）原样保留：无窗节点不拦、有窗但时序倒挂的边仍被丢弃。

**b. 口径**

- `idle_gap_ms = max(0, wall - cp)`；`serialization_loss_ms` 同值别名 + deprecated 注释；`metrics_schema_version = 2`。

**c. 验证轮次**

- `verification_result` 查询多取 `verify_ms`；`vState` 记录 `measured/unmeasured/busy`。
- 轮次分类：`verifier_run_id != ''` → 解析 run 窗（解析失败记 unmeasured）；`verify_ms > 0` → measured 且计入 busy；其余 → unmeasured。
- verify 节点写 `MeasuredRounds` / `UnmeasuredRounds` / `Unmeasured`。
- 指标：`verify_busy_measured_ms`、`verify_measured_rounds`、`verify_unmeasured_rounds`、`verify_coverage_ratio`、`verify_overhead_scope`；`verify_overhead_ratio` 分子改用已度量忙时。

### 2. `internal/service/subgoal.go`

- 机器验证审计行 INSERT 增列 `machine_run_id`（写 `rc.RunID`）；`verifier_run_id=''` 契约不动；`verify_ms` 走默认 0。

### 3. `internal/store/store.go` + `internal/store/schema.sql`

- 迁移表追加 `ALTER TABLE verification_result ADD COLUMN verify_ms INTEGER NOT NULL DEFAULT 0`、`ADD COLUMN machine_run_id TEXT NOT NULL DEFAULT ''`（best-effort，重复列错误吞掉）。
- `schema.sql` 的 `CREATE TABLE IF NOT EXISTS verification_result` 同步两列（新库免迁移）。

### 4. `web/components/training/workflow-graph.tsx`（+ `format.ts` / `lib/types.ts`）

- 「串行损失」→「空闲间隙」，取值 `idle_gap_ms`（回退 `serialization_loss_ms`），hint「墙钟 − 依赖约束最优墙钟」。
- 「排队等待」拆为「依赖等待」`dependency_wait_ms` 与「派发等待」`dispatch_wait_ms`（`queue_wait_ms` 仍是后端别名，不再单独展示）。
- 验证开销：`verify_overhead_scope` 非 `complete` 或 `coverage < 1` → 数值位「未度量」，hint 带 `N/M 轮已度量（机器验证未计时）`。
- 返工率 hint：「连续拒绝轮 / 验证轮数」。
- 节点卡：`unmeasured` 的 verify 节点不再显示裸 `0ms`，脚注/提示改「未度量」。
- `format.ts` 的 `DELTA_LABELS` 同键改名「空闲间隙」（键名仍是 deprecated 别名）；`types.ts` 补 `unmeasured?` / `measured_rounds?` / `unmeasured_rounds?`。

### 5. `internal/service/workflow_metrics_test.go`

新增 3 例（既有 5 例不动）：

1. `TestWorkflowCriticalPathChainsThroughWindowlessNode`：a(30m) → mid(无窗 0) → c(10m)，断言 `critical_path_ms=40m`（穿链恢复上游）、`depth=2`（mid 不进路径数组）、`wall_ms=40m`、goal 根不入路径。
2. `TestWorkflowIdleGapAliasSameValue`：wall 60m / cp 30m，断言 `idle_gap_ms == serialization_loss_ms == 30m` 且 `metrics_schema_version == 2`。
3. `TestWorkflowVerifyCoverageUnmeasured`：3 个 verify 节点（0 measured/12 unmeasured、部分度量、全部度量），断言计数、`verify_coverage_ratio`、`verify_overhead_scope` 三态与 `verify_overhead_ratio` 只用已度量忙时。

### 6. `docs/RSI.md`

指标口径段同步：`idle_gap_ms` + deprecated 别名、`depth` 定义、`metrics_schema_version`、验证覆盖率五项、`verify_overhead_ratio` 分子口径。

## 验证方式

- `go test ./internal/service/ -run 'TestWorkflow|TestLostParallelism|TestRSIWorkflow|TestMachineVerify|TestBuildGoalWorkflow|TestRSIGraph|Blueprint' -count=1`
- `go build ./...`
- 前端：`cd web && npx tsc --noEmit`（类型检查；无 node_modules 时跳过并说明）
- 迁移幂等：`store.Open` 同一库文件两次（既有测试路径 + 新增列默认值语义由 `TestRSIWorkflowEndToEnd` 覆盖）。