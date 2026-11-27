# Execution Report：issue #31 P1–P7 修复执行与验证

| 项 | 值 |
|---|---|
| 日期 | 2026-11-26 ~ 2026-11-27 |
| 分支 | `fix/issue-31`（基线 `a7a60bb` = PR #29 合入后） |
| 关联 issue | https://github.com/Sinkear/FlowLoop/issues/31 |
| 变更规模 | 15 文件 +1177/−58；新增 `internal/service/revive_cascade_test.go`；`docs/use-cases/2026-11-26-auto-upgrade-proposal-closure/` 全目录 |
| 构建 | `go build ./...` 通过 |
| 测试 | `go test ./internal/...` 全绿（7 个 skill zip 测试除外，见 §未测/环境说明） |
| web | 7 文件 `tsc` 通过（web-fixes 队友实施，Lead 审查） |
| 文档 | analysis.md + findings-P1..P7 + timeline.md（usecase-docs-2 队友），README 回填 |

## 1. 修复项 × 实际方案 × 变更文件

### P1 版本对比字段级 diff（能力缺口）

**实际方案**：后端新增 `BlueprintService.VersionDiff(ctx, baseVersionID, targetVersionID)` + `BlueprintVersionDiff` 结构（json tag 与 web 契约一致：`base_version_id,target_version_id,base,patched,changelog_base,changelog_target,nodes_*,edges_*`，`DiffNode` 复用提案 diff 形状）；路由 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}`（handler.go:171）。同版本/跨蓝本拒绝。web 侧：`BlueprintVersionDiff`+`StructuredGraphDiff` 类型、`useBlueprintVersionDiff` hook、版本列表"版本对比"按钮 + `VersionCompareDialog`（changelog 双栏 + DiffCanvas×2 + 结构增删改 + `NodeFieldChanges` 字段级渲染——base/patched 键并集逐叶比对，assignee=负责人/brief=简报 中文标签，长文本红绿双栏 pre）；`ProposalDiffChanges` 改收 `StructuredGraphDiff` 供提案/版本两种 diff 共用。

**变更文件**：`internal/service/blueprint.go`（+VersionDiff/BlueprintVersionDiff）、`internal/server/handler/{blueprint,handler}.go`、`web/lib/{types,api,queries}.ts`、`web/components/blueprint-diff.tsx`、`web/components/workflows/workflow-versions.tsx`

**与 findings 偏差**：无（findings-P1 §5 方案照搬）。附件附带建议"版本列表展示 changelog 摘要"由对比对话框的 changelog 双栏承接。

### P2 approve 自动 rebase（fork 覆盖）

**实际方案**：`ApproveProposal` 先查蓝本最新版本（`ORDER BY version DESC LIMIT 1`，含 draft）；latest != base → 补丁在最新内容上重放；失败 → `NewConflictError`（fail-closed）；成功 → changelog 追加"（rebase：基于 v%d，已合并到最新 v%d 上）"+ Infof。

**变更文件**：`internal/service/blueprint.go`

**与 findings 偏差**：无。

### P3 碰撞守卫可见化 + keys 释放（后续轮零提案）

**实际方案**：三层——①守卫查询加 released 条件（approved 且 applied draft 已 published 的提案释放 patch keys，修复"R1 提案永久占位"）；②`translateCtx` 增 owners/blocked，touched 命中与同标题 dedup 从 Debugf 静默跳过升级为 Warnf（命名占用提案 id）并记入 `[]translationBlock`；③`draftAutoProposals` 返回 blocks，`Analyze` 把 blocks 写成 goal feed system 评论，逐条列 category/标题/占用提案/节点 key + 尾句"请先审批或拒绝上述待审提案，下一轮分析即可继续自动转化"。

**变更文件**：`internal/service/rsi_translate.go`、`internal/service/rsi_analyze.go`

**与 findings 偏差**：附件/README 早期口径提过"审批 UI 提示"，实际落点改为 goal feed system 评论（分析时刻推送，比审批卡片更靠近发现问题的时刻）；不自动清障（审批权仍在人）。

### P4 approve 自动建下一轮 backlog goal（0 执行）

**实际方案**：approve handler 对 RSI 提案（`p.FindingID != ""` 且 Goal 服务已接线）自动调 `StartNextRoundFromProposal(start=false)`——建 **backlog** 轮（不自动 start，人仍是执行发令者，与代码既有设计哲学一致）；approve 响应恒带 `next_round_goal_id`（空串=未建），另有 `next_round_created`/`next_round_error`；创建失败不 fail 响应（批准已成功，人可再点幂等按钮）。web：doApprove 读响应回调页面层 toast（ok 色+goal 链接 / warn amber），SectionCard 与确认框文案改为"批准 = 落一份新草稿，并自动创建下一轮训练 goal（backlog，未启动，需人工启动）"；人工"查看/开始下一轮"按钮保留（幂等：source_ref 相同返回同一 goal，created=false）。

**变更文件**：`internal/server/handler/blueprint.go`、`web/components/workflows/workflow-proposals.tsx`、`web/app/workflows/[id]/page.tsx`

**与 findings 偏差**：findings-P4 早期口径"nudge/自动化选项二选一"，实际落点是自动建轮（backlog）+ toast 引导，比 nudge 更进一步但保持"人不发令不执行"。

### P5 human_wait 双触发 + dispatch_wait 新规则（规则盲区）

**实际方案**：①human_wait 规则改双触发：`share≥0.30 && wait≥10min` **或** `wait≥1h`（`ruleHumanWaitAbsoluteMS=3600000`），Evidence 标 `trigger=share|absolute`；翻译器 `translateHumanWait` 同步双触发（否则 absolute 触发的 finding 被翻译器旧单条件 skip）。②新增 dispatch_wait 规则：`dispatch_wait_ms≥5min 且 dwShare=dw/wall≥0.15` → info finding（`ruleDispatchWaitFloorMS=300000`/`ruleDispatchWaitShare=0.15`）；**不注册翻译器**——lane 数是运行时配置不在蓝图 spec 里，无诚实结构变换，finding 进报告供人读。③serial_chain 阈值方向未动（附件自标推断，R2 46% 为推断值，需先取得关键路径准确口径，见 findings-P5 未竟事项）。

**变更文件**：`internal/service/rsi_analyze.go`、`internal/service/rsi_translate.go`

**与 findings 偏差**：附件建议含"serial_chain 阈值方向复核"，本轮未动该阈值（推断值不动实码的原则）。

### P6 deliver 硬校验 + 级联失败自愈（收口漏水）

**实际方案**：①`MarkDelivered` success 分支事务内 `SELECT COUNT(*) FROM sub_goal WHERE goal_id=? AND status NOT IN ('verified','cancelled','failed')`；>0 → `UPDATE goal SET review_request='deliver:guard: …'`（`deliver:` 前缀是载荷：`pendingDeliverGoalIDs` 用 `NOT LIKE 'deliver:%'` 排除恢复器重放，防 10 分钟 ticker 反复重试被拒 deliver 的日志风暴）→ return NewValidationError，goal 留 review。②`wakeDependentsTx` 尾部接 `reviveFailedDependentsTx`：候选=failed + 无 role='subgoal' run + 带 failGatedSubGoalTx 级联评论（`content LIKE '%无法满足门控%'`）+ deps 全 verified → 复位 running/attempt=0 + blueprint_node failed→running + resolveReleasedBriefTx + 重新派发 + system 评论 + `sub_goal.retrying` 事件。**收窄理由**：初版无级联评论条件会误伤 `TestRetryGatedSubGoalRespectsDependsOn` 的人工重试路径契约；跑过的 failed 归重试链+人工，级联阵亡（从未跑过）归自动复活。

**变更文件**：`internal/service/goal.go`、`internal/service/subgoal.go`、`internal/service/revive_cascade_test.go`（新）

**与 findings 偏差**：附件建议"gate approve、上游 verified 时复位 failed"——实际挂到 `wakeDependentsTx`（上游 verified 的统一收口点，gate holder verified 也走它），非单独挂在 gate resolve。

### P7 不可重试错误 fail-fast + 故障样本剔除（容错）

**实际方案**：①`nonRetryableMarkers` 扩容：`+「额度已用完」「已用完」「quota exceeded」「insufficient quota」「配额不足」「余额不足」「insufficient balance」「payment required」`；**有意不收** `rate limit exceeded`（瞬态限速可 backoff 恢复，与日配额语义不同）与 `billing`（过宽误伤）。②`baseline()` 故障样本剔除：先收集 `[]{metrics,goalID}` 再 rows.Close() 后过滤（`:memory:` 测试库 SetMaxOpenConns(1)，迭代中嵌套 `goalHadFaultRuns` 查询会死锁——`TestAnalyzeRoundNumberingScopedByWorkflowOwner` 曾挂 600s 的真因）；每样本 goal 有任何 `isNonRetryable(result_summary)` 的 failed run → 不入中位数池（列名 `result_summary`，非 summary）。查询失败 fail-open。

**变更文件**：`internal/service/goal.go`、`internal/service/rsi_analyze.go`

**与 findings 偏差**：无。

## 2. 单测

| 测试 | 覆盖 | 结果 |
|---|---|---|
| `TestBlueprintVersionDiffListsFieldChanges`（新，blueprint_phase_c_test.go） | P1：base→v2 nodes_changed 恰 [n2] 且 patched task.assignee=agent:other、changelog_base；v2→v3 nodes_added=[n_r] 且 n2 无 change（rebase 存活）；同版本/跨蓝本拒绝 | PASS |
| `TestApproveProposalAutoRebasesSibling`（新） | P2：同基线两提案，后批者 v3 同时含 v2 改动+自身改动，changelog 含 "rebase" | PASS |
| `TestCascadeFailedDependentRevivesWhenDependencyVerifies`（新，revive_cascade_test.go） | P6：A 重试链耗尽→B 级联 failed（无 run）→人工 RetrySubGoal(A)+完成→B 自动复活（running/0/1 run/带复位评论）→完成→verified | PASS |
| 既有门控系列 | P6 收窄不误伤：`TestRetryGatedSubGoalRespectsDependsOn`、`TestDependsOnDependencyFailureCascades`、`TestWakeReleaseCarriesDispatchAnchor` | PASS |
| 既有 RSI 系列 | P3/P4/P5 回归：`TestNextRound*`（5）、`TestDraftProposalFromFinding*`（2）、`TestPhaseDAutoProposal*`（4）、`TestBlueprintProposal*`（3）、`TestAnalyzeRoundNumberingScopedByWorkflowOwner`（曾暴露 baseline 死锁，修复后 PASS） | PASS |

## 3. 未测 / 环境说明

- **skill zip 测试 7 个**（`TestCreateFromZip*`/`TestSeedBuiltinSkills`/`TestEnsureBuiltinRejectsForeignName`/`TestDeleteBuiltinRejected`）：失败根因=本机 `$HOME/.flowloop/runs/skills` 无写权限（预存环境限制，与本次改动无关）；`HOME=/tmp/dsh-testhome GOPATH=/home/sinkear/go go test ./internal/service/skills/...` 全 PASS。
- **web 端到端**：仅 tsc 类型检查；版本对比对话框/批准 toast 未起 dev server 做浏览器验收（后续可补）。
- **P5 serial_chain 阈值方向**：未修（附件推断值不动实码）。
- **沙箱数据复验**（附件 §10 命令集）：DB 在沙箱机 `/root/job-envs/sandboxes/ouroflow-1795367359/`，本机不可直接访问；存量 v2→v3 合并建议见 README。
- 模块缓存：`proxy.golang.org` 不可达，`GOPATH` 必须指 `/home/sinkear/go`。

## 4. 状态

- 七项修复全部落盘，构建+测试收敛（§2），文档档案齐（analysis/findings/timeline/README）。
- 待办：commit（fix/issue-31）、issue #31 回帖（文案已备，发布前需仓库 owner 确认）。
