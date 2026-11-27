# Findings P7：重试不识别环境故障 + 故障样本污染基线

> 本文件是 P7 的问题档案：问题复述、证据（[facts.md](facts.md) §P7）、根因、影响、修复方案与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.8、§1）

附件 §6.8/§1 主张（P7 容错）：

> 同一"免费 Token 额度已用完"错误重试 9 次，跨两个时段（15:08-15:20 ×3；19:43-19:54 ×6，且发生在人工已切换模型并 reopen 之后——模型切换对进行中的重试批次不生效），累计 10 个 failed run。
> 建议：错误分类，环境/配额类 fail-fast 并上报人工；故障期指标不入 RSI 样本。

## 2. 证据（facts.md §P7 全部事实）

1. **重试决策**（`internal/service/goal.go:1957`）：`if rc.Attempt < maxAttempts && !isImportGoal && !nonRetryable` → 重试；`maxAttempts=3`（:125）；backoff `retryBackoffFor`（:157-160）：attempt2=30s、attempt3=60s、cap 5min。
2. **nonRetryable 名单**（:134-138）：`isNonRetryable(rc.Summary)` 命中才免重试（摘要加尾注"（环境/配置错误，重试无效）"）；改前白名单**仅一条 "Model unavailable"**——"免费 Token 额度已用完"不在名单 → 每个时段机械打满 3 次重试。附件 9 次重试（每时段 3 连发跨多 run）与之吻合。
3. **模型切换不生效**：重试批次继承创建时的模型配置，人工切模型只影响新 run——19:43-19:54 的 6 连败发生在 reopen 之后，正是这一语义。
4. **基线无健康过滤**：`baseline()`（`internal/service/rsi_analyze.go:819-830`）取同域最近 20 份 ready 报告的中位数，不区分样本是否来自故障期——R3 报告（15:34 分析）含 15:08-15:20 故障期指标，`retry_amplification=3` 系 token 故障所致，非蓝本问题。

## 3. 根因

- **名单按历史事故逐个补**："Model unavailable" 是上一次故障的产物（R2 提案 3 的语义化重放），从未系统化分类"哪些错误重试必然无效"——日配额耗尽、余额不足、payment required 都是确定性失败，重试只是烧日志；
- **基线假设样本同质**：中位数统计默认样本来自同一分布，故障期样本混入后，share/等待类对比的基线被拉向"故障态"。

判定：**成立（名单过窄 + 基线无健康过滤，两处独立缺陷）**。

## 4. 影响

- 10 个 failed run 的日志噪音 + 每次重试间隔 30-60s 的虚假"还在干活"信号，人工在 19:43-19:54 期间误以为问题已缓解；
- 故障期指标进 RSI 样本后，baseline 中位数失真，可能让后续健康轮次的对比声明（"比基线差/好"）失真；
- 与 P5 复合：R3 的 retry_amplification finding 描述的是故障而非蓝本，若被翻译成结构变更（如加 retry 边），会把故障当设计缺陷固化进蓝图。

## 5. 修复方案（[execution-report.md](execution-report.md) §1.P7）

1. **名单扩容**（`internal/service/goal.go:134` 区）：在 "Model unavailable" 之外新增 8 个确定性失败标记——"额度已用完"、"已用完"、"quota exceeded"、"insufficient quota"、"配额不足"、"余额不足"、"insufficient balance"、"payment required"。**有意不收** `"rate limit exceeded"`（瞬态限速，backoff 可恢复，与日配额不同）与 `"billing"`（过宽，误伤面大）。
2. **基线剔除故障样本**（`internal/service/rsi_analyze.go` `baseline()`）：查询加 `rp.metrics_json, g.id`；**先收集样本、`rows.Close()` 后再过滤**（防 `:memory:` 单连接库在 rows 迭代中嵌套查询死锁——`TestAnalyzeRoundNumberingScopedByWorkflowOwner` 曾因此挂起 600s）；每样本经 `goalHadFaultRuns` 判定：`SELECT result_summary FROM run WHERE goal_id=? AND status='failed'` 任一命中 `isNonRetryable` → 不入中位数池。查询失败 fail-open（不剔除，保底可用）。

## 6. 修复状态

**已修复（本次 issue 处理，编译+测试通过）**。变更文件：`internal/service/goal.go`、`internal/service/rsi_analyze.go`。回归：`TestAnalyzeRoundNumberingScopedByWorkflowOwner` 全绿（该测试曾暴露单连接死锁，修复后通过）；全量 `go test ./internal/...` 绿（skill zip 环境用例除外，见 execution-report §3）。
