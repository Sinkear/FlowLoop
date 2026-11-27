# User Case: 自动升级流水线 — 三任务对照复盘与提案闭环问题（issue #31）

- 日期：2026-11-26
- 蓝本：自动升级流水线（051dbbd8f71ef729a1050ae9aa78a099）
- 关联 issue：https://github.com/Sinkear/FlowLoop/issues/31
- 来源材料：`sources/FlowLoop自动升级流水线-三任务对照复盘与提案闭环问题记录.md`（附件原文，未改动）
- 复盘范围：2026-11-26 三任务对照复盘（R1 agent-browser / R2 drawio / R3 openviking，全 pinned v1=16ebee24dd5c3e64f1328d5225c038c5）

## 结论（TL;DR）

附件 §1 提出的 P1–P7 七项问题，经代码逐项核实：**P2/P3/P4/P7 判定为代码缺陷成立**（机制根因已落行），**P5/P6 部分成立**（规则盲区/收口缺失），**P1 判定为产品能力缺口**（服务端能力已存在、暴露不足）。修复清单见下。

| # | 问题 | 核实结论 | 根因落点 | 修复 |
|---|------|----------|----------|------|
| P1 | 版本对比无字段级 diff，人工对比易漏 | 成立（能力缺口） | web 版本对比视图未复用服务端 `BlueprintDiff` 的字段级 `DiffNode` | **已修复**：新增 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}`（`BlueprintService.VersionDiff`）+ web 版本对比对话框字段级渲染（assignee/brief 中文标签、红绿双栏） |
| P2 | 同基线双提案先后批准，后批者 fork 丢弃前批者改动 | 成立（代码缺陷） | `internal/service/blueprint.go:3749` ApproveProposal 只对 `p.BaseVersionID` 重校验，不检查 base 落后 | **已修复**：approve 时检测 base 落后→在最新版本内容上重放补丁（auto-rebase），重放失败 fail-closed 报冲突；changelog 标注 rebase 来源 |
| P3 | 生成器只处理 round 1，后续轮零提案 | 成立（代码缺陷，但机制与附件推断不同：碰撞守卫非 round 过滤） | `internal/service/rsi_translate.go:173-194` 跨轮碰撞守卫 + `:304-309` touched 静默跳过 + `:310-319` 同标题 dedup；R1 提案一直 pending 占用节点，R2/R3 翻译物全部触碰同节点 → 全部被静默跳过（仅 Debugf，日志不可见） | **已修复**：①approved+published 提案释放 keys ②跳过升级 Warnf 并记录 blocked 明细 ③blocked 写 goal feed system 评论引导人工审批 |
| P4 | 提案批准后未自动 spawn 下一轮 | 成立（机制现状=设计如此，但闭环断点缺引导） | `internal/service/rsi_next_round.go:31` StartNextRoundFromProposal 为纯人工触发（独立 HTTP 按钮），审批后无自动路径、无 nudge | **已修复**：approve RSI 提案时自动 `StartNextRoundFromProposal(start=false)` 建 backlog 轮（不自动启动，人仍是发令者）；响应恒带 `next_round_goal_id`；web toast 引导+按钮幂等保留 |
| P5 | 规则盲区：dispatch_wait 无对应规则；human_wait 规则未触发 | 部分成立（human_wait 稀释 + dispatch_wait 盲区实锤；serial_chain 方向为附件推断，未复核） | `rsi_analyze.go:761-783` human_wait 仅 share≥0.30 单触发（86 分钟等待被 225 分钟墙钟稀释到 27.7%）；`dispatch_wait_ms` 只有指标采集（`workflow.go:559`）无独立规则/翻译器 | human_wait 双触发（share 或绝对值 ≥1h）+ 新增 dispatch_wait 规则（floor 5min、share≥0.15，不注册翻译器）+ baseline 剔除故障样本 |
| P6 | 目标完成收口缺陷，训练 goal 直接 done 且未校验 sub_goal 终态 | 成立（两处独立缺陷） | `goal.go:3447-3485` MarkDelivered 零校验（注释假设被 gate approve 自动路径打破→agent-browser 案例 done 漏水）；`subgoal.go:1798-1812` gatedSubGoalsTx 只扫 running→gate approve/上游 verified 无法自愈级联 failed（openviking 案例） | MarkDelivered 硬校验（未收口 sub_goal>0 拒绝交付，`deliver:` 前缀防恢复器重放）+ `reviveFailedDependentsTx` 级联失败自愈（收窄：仅救带门控级联评论、从未跑过的 failed） |
| P7 | 容错缺陷：不可重试错误反复重试 | 成立（名单过窄 + 基线无健康过滤） | `goal.go:134-138` nonRetryableMarkers 白名单仅 "Model unavailable"→"额度已用完" 重试 9 次跨两时段共 10 failed run；`rsi_analyze.go:819-830` baseline 不分健康/故障样本（R3 报告含故障期指标） | 名单扩容 8 词（额度/quota/balance/payment 类；有意不收 rate limit exceeded 与 billing）+ baseline 先收集后过滤 `goalHadFaultRuns`（fail-open） |

核实过程与证据见 `analysis.md`；逐项问题详情见 `findings-P1.md` … `findings-P7.md`；执行结果见 `execution-report.md`。

## 影响面

- 三轮训练（R1/R2/R3）零后续轮执行；v2 (fef7788c) 从未发布，其 assignee 修正被 v3 的 fork 覆盖丢弃；发布版 a5443d0b 缺失 t_check 纪律块之外的全部修正。
- 后续任何多轮 RSI 复盘都会复现 P3（碰撞守卫静默封死）与 P4（无自动下一轮）。

## 修复清单

| # | 修复内容 | 变更文件 | 状态 |
|---|----------|----------|------|
| P3 | 碰撞守卫 released 条件（approved+published 释放 keys）+ owners/blocked 记录 + Warnf + goal feed system 评论引导审批 | `internal/service/rsi_translate.go`、`internal/service/rsi_analyze.go` | ✅ |
| P4 | approve 自动建 backlog 下一轮（`start=false`，不自动启动）+ 响应 `next_round_goal_id` 恒发 + web toast 引导 + 按钮幂等保留 | `internal/server/handler/blueprint.go`、`web/components/workflows/workflow-proposals.tsx`、`web/app/workflows/[id]/page.tsx` | ✅ |
| P2 | ApproveProposal auto-rebase：base 落后则对最新内容重放补丁，失败 fail-closed 报冲突，changelog 标注 rebase | `internal/service/blueprint.go` | ✅ |
| P1 | `GET /blueprint-versions/{versionId}/diff/{targetVersionId}` 字段级版本 diff + web VersionCompareDialog（结构增删改 + assignee/brief 字段级红绿对比 + changelog 双栏） | `internal/service/blueprint.go`、`internal/server/handler/{blueprint,handler}.go`、`web/lib/{types,api,queries}.ts`、`web/components/{blueprint-diff,workflows/workflow-versions}.tsx` | ✅ |
| P5 | human_wait 双触发（share≥0.30&&≥10min 或绝对值≥1h）+ translateHumanWait 同步双触发 + 新增 dispatch_wait 规则（≥5min 且 share≥0.15，不注册翻译器）+ baseline 剔除故障样本 | `internal/service/rsi_analyze.go`、`internal/service/rsi_translate.go` | ✅（serial_chain 方向未动：附件推断未复核，不盲改） |
| P6 | MarkDelivered 硬校验（未收口 sub_goal>0 → `deliver:guard` 拒绝，`deliver:` 前缀防恢复器重放风暴）+ `reviveFailedDependentsTx` 级联失败自愈（收窄：仅救带「无法满足门控」级联评论、从未跑过的 failed） | `internal/service/goal.go`、`internal/service/subgoal.go` | ✅ |
| P7 | nonRetryableMarkers 扩容 8 词（额度已用完/quota exceeded/insufficient quota/配额不足/余额不足/insufficient balance/payment required 等；有意不收 rate limit exceeded、billing）+ baseline `goalHadFaultRuns` 先收集后过滤（防单连接死锁） | `internal/service/goal.go`、`internal/service/rsi_analyze.go` | ✅ |

存量修复（历史数据）：11-26 的 v2（fef7788c）改派改动已被 v3 覆盖、v3 零执行——历史合并需在沙箱 DB（`/root/job-envs/sandboxes/ouroflow-1795367359/…`）上人工操作（把 v2 的 assignee 改动合并进 published 版或重新分析生成提案），不在本仓库代码修复范围内；P2 的 auto-rebase 保证未来不再发生同型覆盖。

## 指标

- `go build ./...`：exit 0。
- `go test ./internal/...`：**全绿**，仅 7 个 skill zip 用例失败＝预存环境限制（`$HOME/.flowloop/runs/skills` 无写权限；用 `HOME=/tmp/dsh-testhome GOPATH=/home/sinkear/go` 跑即全 PASS），与本次改动无关。
- 新增测试 3 个：`TestBlueprintVersionDiffListsFieldChanges`（P1）、`TestApproveProposalAutoRebasesSibling`（P2）、`TestCascadeFailedDependentRevivesWhenDependencyVerifies`（P6，`internal/service/revive_cascade_test.go`）。
- 回归系列全绿：`TestNextRound*`(5)、`TestDraftProposalFromFinding*`(2)、`TestPhaseDAutoProposal*`(4)、`TestBlueprintProposal*`(3)、`TestAnalyzeRoundNumberingScopedByWorkflowOwner`、`TestRetryGatedSubGoalRespectsDependsOn`、`TestDependsOnDependencyFailureCascades`、`TestWakeReleaseCarriesDispatchAnchor`。
- web：7 文件 `tsc --noEmit` exit 0；未起 dev server 做人工验收（见 execution-report.md §3）。
- 代码量：15 文件 +1177/−58（不含 docs）；user case 文档 10 文件。

## 目录

```
├── README.md            本文档：结论、影响面、修复清单、指标
├── sources/             附件原文（未改动）
├── facts.md             事实底座：P1–P7 核实事实与根因落行（供扩写引用）
├── analysis.md          逐项核实过程与证据
├── findings-P1.md … findings-P7.md   逐项问题详情
├── timeline.md          事故时间线与口径修正表
├── execution-report.md  修复执行与验证结果
└── issue-31-reply-draft.md  issue #31 回帖底稿（已发布：issuecomment-7207757244）
```
