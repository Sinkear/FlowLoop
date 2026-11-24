# Task（Issue #13）：任务拆解与执行状态

分支：`feat/issue-13`（基于 `main @56d78ad`）；每个提交一个原子变更。

| # | 任务 | 文件 | 验收 | 状态 |
|---|------|------|------|------|
| T1 | 关键路径穿链：去掉 `p.length == 0` 守卫 + 拓扑序 relax + 路径只列有 busy 贡献的工序 | `internal/service/workflow.go`、`internal/service/workflow_metrics_test.go` | 无窗节点穿链用例：cp 恢复上游时长、depth 为工序数、goal 根不入路径 | done |
| T2 | 口径更名：`idle_gap_ms` + `serialization_loss_ms` deprecated 同值别名 + `metrics_schema_version=2` | `internal/service/workflow.go`、测试 | 别名同值用例 | done |
| T3 | 机器验证未度量口径：`verify_ms` / `machine_run_id` 迁移（store+schema）、机器审计行写 `machine_run_id`、覆盖率与 scope 指标、verify 节点 `unmeasured` | `internal/store/store.go`、`internal/store/schema.sql`、`internal/service/subgoal.go`、`internal/service/workflow.go`、测试 | 未度量计数/scope 用例 + 既有机器验证测试通过 | done |
| T4 | 面板文案与未度量呈现：空闲间隙、依赖/派发等待两行、验证「未度量」、返工率 hint、节点 `unmeasured` | `web/components/training/workflow-graph.tsx`、`web/components/training/format.ts`、`web/lib/types.ts` | tsc 通过 / 文案与公式一致 | done |
| T5 | 文档同步：spec/plan/task + `docs/RSI.md` 指标口径 | `docs/issue-13/*`、`docs/RSI.md` | 文档与实现一致 | done |
| T6 | 自审查（五轴）→ 建 PR → Issue 评论 → A2A QA | — | PR 已创建、评论已发、A2A 已交办 | pending |

## 回归清单

- [x] `TestWorkflowCriticalPathFanIn`（既有）
- [x] `TestWorkflowInferredSeqExcludedFromCriticalPath`（既有）
- [x] `TestWorkflowReviewIsolatedFromWorkAndSpeedup`（既有）
- [x] `TestWorkflowCriticalPathDeterministicTie`（既有）
- [x] `TestWorkflowLostParallelismInputs` / `TestLostParallelismRuleFires`（既有）
- [x] `TestRSIWorkflowEndToEnd` / `TestMachineVerifyRecordsResultAndWorkflowNode` / `TestBuildGoalWorkflow*`（既有，DB 级）
- [x] 新增 3 例（无窗穿链 / 别名同值 / 未度量计数与 scope）

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 拓扑序 relax 改变历史 goal 的 cp/depth | `metrics_schema_version=2` 标注跨版本不可比；穿链只增不减，不会凭空造时长 |
| `cp > wall` → `idle_gap < 0` | 沿用 `max(0, ...)` 夹取 |
| 迁移重复执行报 duplicate column | 沿用 `store.go` best-effort 模式（忽略 duplicate，继续后续语句） |
| 「未度量」被误读为功能缺失 | hint 明示「机器验证未计时」+ `verify_ms` 已预埋（phase 2 计时即可用） |