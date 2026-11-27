已对照源码逐项核实本 issue 的 P1–P7（核实基线 a7a60bb，分支 fix/issue-31），七项全部修复完成，附完整复盘文档。

## 核实结论

| # | 判定 | 真实根因（代码落行） |
|---|------|---------------------|
| P1 | 成立（能力缺口） | 路由表无版本对版本 diff 端点；服务端 `BlueprintDiff` 字段级能力已存在但未暴露，版本对比只能并排拓扑图 |
| P2 | 成立（代码缺陷） | `ApproveProposal`（internal/service/blueprint.go:3737）只对提案自己的 `BaseVersionID` 重放补丁，不检查 base 是否落后——同基线第二条提案从 v1 fork 出 v3，v2 的 assignee 修正被静默丢弃 |
| P3 | 成立，**但机制与附件推断不同** | 不是"生成器只处理 round 1"：`round_no` 不参与任何过滤。真实根因＝跨轮碰撞守卫（internal/service/rsi_translate.go:173-194）把该蓝本所有 pending/approved 提案的 patch keys 灌入 touched，后续轮翻译物触碰同节点即被静默跳过（:304-319，仅 Debugf 不可见）。R1 两提案 15:14 起 pending 占用 t_check/t_upgrade/t_repair，封死 R2/R3 全部翻译物 |
| P4 | 成立，**"被 parked goal 阻塞"不成立** | `StartNextRoundFromProposal` 是批准→下一轮的唯一链路，纯人工按钮触发，approve 从不联动；11-23 健康对照的 12 秒间隔是人工快速点击 |
| P5 | 部分成立 | human_wait 27.7%＜30% 阈值被 225 分钟墙钟稀释（86 分钟人工等待不可见，实锤）；dispatch_wait 只有指标采集无独立规则（实锤）；serial_chain 方向为附件推断，未复核未盲改 |
| P6 | 成立（两处） | ① `MarkDelivered`（internal/service/goal.go:3447）零校验：注释假设"pending sub_goal 到不了这里"被 gate approve 自动路径打破 → agent-browser 案例 done 漏水；② `gatedSubGoalsTx`（internal/service/subgoal.go:1798）唤醒集合只扫 running → openviking 案例级联 failed 在 gate approve / 上游 verified 后无法自愈 |
| P7 | 成立 | `nonRetryableMarkers` 白名单仅 "Model unavailable" 一条 → "额度已用完"机械重试 9 次跨两时段共 10 个 failed run；且 baseline 不分健康/故障样本，R3 报告含故障期指标 |

## 修复方案（15 文件 +1177/−58）

- **P3**：approved 且 draft 已 published 的提案释放 keys（改动已入权威版本，触碰同节点是合法迭代）；跳过升级 Warnf 并记录 blocked 明细；blocked 写 goal feed system 评论引导"审批或拒绝待审提案后下一轮分析即可继续"
- **P4**：approve RSI 提案时自动调 `StartNextRoundFromProposal(start=false)` 建 backlog 轮（不自动启动，人仍是执行发令者）+ web toast 引导；人工"开始下一轮"按钮幂等保留
- **P2**：approve 检测 base 落后 → 对最新版内容重放补丁（auto-rebase），重放失败 fail-closed 报冲突；changelog 标注 rebase 来源
- **P1**：新增 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}` + web 版本对比对话框（assignee/brief 字段级红绿 diff + changelog 双栏）
- **P5**：human_wait 双触发（share≥30% 或绝对值≥1h，长等待不再被墙钟稀释）+ 新增 dispatch_wait 规则（≥5min 且 share≥15%；不注册翻译器——lane 数是运行时配置不在蓝图 spec 里，无诚实结构变换，finding 进报告供人读）
- **P6**：MarkDelivered 硬校验（未收口 sub_goal>0 拒绝交付；`deliver:` 前缀进 review_request 防 daemon 恢复器重放风暴）+ `reviveFailedDependentsTx` 级联失败自动复活（收窄：仅救带"无法满足门控"级联评论、从未跑过的 failed；跑过被重试链关死的留给人工 RetrySubGoal）
- **P7**：名单扩容 8 词（额度已用完/quota exceeded/insufficient quota/配额不足/余额不足/insufficient balance/payment required 等；有意不收 rate limit exceeded（瞬态可 backoff 恢复）与 billing（误伤面大））+ RSI baseline 剔除故障样本（`goalHadFaultRuns`）

## 验证

- `go build ./...` exit 0；`go test ./internal/...` 全绿（7 个 skill zip 用例失败＝`$HOME/.flowloop/runs/skills` 无写权限的预存环境限制，换可写 HOME 即全 PASS，与本 issue 无关）
- 新增单测：`TestBlueprintVersionDiffListsFieldChanges`（P1）、`TestApproveProposalAutoRebasesSibling`（P2）、`TestCascadeFailedDependentRevivesWhenDependencyVerifies`（P6）
- web `tsc --noEmit` exit 0

## 文档与存量说明

完整复盘已落档 `docs/use-cases/2026-11-26-auto-upgrade-proposal-closure/`（README / analysis / findings-P1..P7 / timeline / execution-report）。修复代码在 `fix/issue-31` 分支，待审查后提交并发 PR。

存量修复：11-26 的 v2（fef7788c）改派改动已被 v3 覆盖、v3 零执行——历史数据合并需在沙箱 DB 人工操作（把 v2 的 assignee 改动合并进 published 版或重新分析）；P2 的 auto-rebase 保证未来不再发生同型覆盖。
