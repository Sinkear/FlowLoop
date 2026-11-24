# Spec（Issue #13）：workflow 指标三连——关键路径断链 / 串行损失语义 / 验证开销恒 0

> 来源：Issue #13 需求 + VOD 审视 + Issue Agent 已发布的 Spec（issuecomment-7121647648）。
> 本文件是 Coding Agent 的落地口径，**不改变** D1/D2/D3 已定案的决策，只把实现细节与金值的可复现方式固定下来。

## 目标

让 `GET /workflows/goals/{id}` 的 `metrics` / `critical_path` 与工作流面板文案，同真实执行语义一致：

1. 关键路径覆盖全部真实工序（不再在「从未执行的子目标」处断链）；
2. 名为「串行损失」的字段不再承载「空闲间隙」的数值（改名 + 兼容别名）；
3. 机器验证场景下的「验证开销」不再是无标注的 0%，改为「已度量的验证开销 + 覆盖率 + scope」。

面板指标是 workflow RSI 自迭代的输入（执行图 → structural findings / graph patches），失真会传导为错误的迭代结论。

## 现状与根因（main @56d78ad 核对）

| # | 根因 | 位置 |
|---|------|------|
| 1 | DP 守卫 `if !ok \|\| p.length == 0 { continue }`：无窗节点 `DurationMS=0` → `dp.length=0` → 后继直接丢弃该前驱，链在此断开 | `internal/service/workflow.go:487` |
| 1b | **次序问题（本 issue 发现的第二个断链成因）**：DP 按 `started_at_ms` 排序，无窗节点被折到序列最前（`si = -1`），其前驱尚未 relax → 即使去掉守卫，`dp[mid].prev` 也指不回上游，「同步环境清单」的 45764ms 仍然丢失。必须改为按 **explicit 边拓扑序** relax，时间序只作为「前驱结束 ≤ 后继开始」的有效性校验 | `workflow.go:437-504` |
| 2 | `serLoss := wall - cpMS`（`:618`）量的是空闲间隙，文案承诺「本可并行却被串起来的等待」 | `workflow.go:616-621`、`workflow-graph.tsx:410` |
| 3 | 机器验证审计行 `verifier_run_id=''`（schema 契约标记「machine round」）→ verify 节点取不到 run 窗 → `verifyBusy` 恒 0 → `verify_overhead_ratio=0` 冒充「验证免费」 | `subgoal.go:462`、`workflow.go:280` |

## 实现口径（已定案，勿再改）

### Bug 1 穿链

1. DP 只保留 `if !ok { continue }`，去掉 `p.length == 0`——`prev` 已指向更上游，重建即穿链，**不造窗口估算**（无窗就是 0 贡献）。
2. relax 顺序由「开始时间序」改为「explicit 边拓扑序（Kahn，开始时间序做 tie-break，环残留按时间序兜底）」：无窗节点必须在其前驱之后 relax，否则 `prev` 链断。
3. `critical_path` 只列**有 busy 的时间贡献**的工序：0 贡献节点（goal 根 / 从未执行 / 纯机器验证轮）允许被链穿过但不出现在路径数组里，`depth = len(critical_path)` = 真实工序数。理由：`depth` 语义是「关键路径工序数」，把没有时间贡献的节点算成一个工序会让 depth 与真实工序数不符（金值 goal：11 道工序）。

### Bug 2 口径更名

- `idle_gap_ms = max(0, wall_ms − critical_path_ms)`；
- `serialization_loss_ms` 保留**同值**并标 deprecated 别名（照 `queue_wait_ms` 的兼容模式）；
- `metrics_schema_version = 2`（跨版本趋势不可比）。

### Bug 3 验证开销

- 迁移 `verification_result.verify_ms INTEGER NOT NULL DEFAULT 0`、`machine_run_id TEXT NOT NULL DEFAULT ''`（`store.go` best-effort ALTER + `schema.sql` 同步）；
- 机器审计行写 `machine_run_id = rc.RunID`，`verifier_run_id=''` 的机器轮契约**不动**，`verify_ms` 本期写 0（未计时，phase 2 计时内核改造）；
- 新增 `verify_busy_measured_ms` / `verify_measured_rounds` / `verify_unmeasured_rounds` / `verify_coverage_ratio` / `verify_overhead_scope`（`unmeasured|partial|complete`）；`verify_overhead_ratio` 分子改用「已度量验证忙时」；
- verify 节点新增 `unmeasured`（bool）与 `measured_rounds` / `unmeasured_rounds`；`unmeasured=true` 表示该节点存在无计时窗口的轮次；
- 轮次分类：有 `verifier_run_id` 且能取到 run 窗 → measured；`verify_ms>0` → measured（phase 2）；其余 → unmeasured。

### Bug 4 文案

- 「串行损失」→「空闲间隙」，hint 与公式一致；「排队等待」拆成「依赖等待 / 派发等待」两行；
- 验证开销在 `coverage < 1` 时显示「未度量」并带 `N/M 轮已度量（机器验证未计时）`，不出现裸 0%；
- 返工率 hint 改为「连续拒绝轮 / 验证轮数」（与 `rework = rejRounds / verifyRounds` 一致）。

## Acceptance Criteria

- [ ] 目标金值 goal `9315615200a842ed88f7471befdd211b`（12 子目标、1 个「确认审计范围」从未 run、12 轮机器验证）：`critical_path_ms == work_sum_ms == 2947144`，`depth=11`（11 道工序，含首道「同步环境清单」45764ms；无窗节点以 0 贡献穿链）。
- [ ] `idle_gap_ms == 6406`（2953550 − 2947144），`serialization_loss_ms` 同值且注释 deprecated；`metrics_schema_version=2`。
- [ ] 机器验证场景：`verify_measured_rounds=0`、`verify_unmeasured_rounds=12`、`verify_overhead_scope="unmeasured"`、`verify_coverage_ratio=0`；UI 显示「未度量」+「0/12 轮已度量（机器验证未计时）」。
- [ ] verify 节点在无窗机器轮上带 `unmeasured=true`；`verifier_run_id=''` 仍是机器轮唯一契约标记，机器审计行新增 `machine_run_id`。
- [ ] UI：「空闲间隙」+ 依赖/派发等待两行；返工率 hint 分母为验证轮数。
- [ ] `internal/service/workflow_metrics_test.go` 既有 5 例通过 + 新增 3 例（无窗穿链、别名同值、未度量计数/scope）。
- [ ] 迁移幂等（重复 Open 不报致命错误），旧行 `verify_ms=0` / `machine_run_id=''` 与现状等价。

## Out of Scope（不动）

owner run 工作量挂图（`role='owner'` 不进图）、`wall_ms` 终点补 owner 收尾、`consult_request.status`、机器 gate 的 daemon 侧计时改造（phase 2）、历史 goal 指标回填。