# OuroFlow 文档索引

项目入口仍是根目录的 [README.md](../README.md)（英文）与 [README.zh.md](../README.zh.md)（中文）。
本目录集中存放产品/架构设计、调研、基准报告、RSI 自迭代记录与真实用例复盘；根目录不再散放文档。

> 约定：`DESIGN.md` 是产品与架构的冻结基线（代码注释中的 `DESIGN.md §N` / `决策 N-M` 均指向它）；文档之间的相对链接已按目录位置修正。

## 顶层指南

| 文件 | 说明 |
| --- | --- |
| [PATHS.md](PATHS.md) | 路径与数据布局：每个目录/环境变量，以及升级时必须保留的数据 |
| [UPGRADE.md](UPGRADE.md) | 保留数据的升级：stop → 备份 → 只替换 `bin/`、`web/` → start，含失败回滚 |
| [IDENTITY.md](IDENTITY.md) | Run 身份与指令归属：一次共用 workdir 身份劫持事故的复盘与三层修复 |
| [TEMPLATES.md](TEMPLATES.md) | 把最佳实践做成模板：registry + YAML 的消费链路与反向导出 |

## 设计与架构（design/）

| 文件 | 说明 |
| --- | --- |
| [design/DESIGN.md](design/DESIGN.md) | 产品与架构设计（冻结版）：产品定义、状态机、验收、工作区、交付、工程不变量、模块边界、决策记录 2-x/3-x/4-x/5-x/6-x/7-x |
| [design/Collaboration.md](design/Collaboration.md) | 协作机制优化与修复设计 v1（2026-08-13 全盘采纳，决策 5-1~5-10；含实现偏差注记） |
| [design/Collaboration.v2.md](design/Collaboration.v2.md) | 协作模型终稿 v2：Goal / Run / Workspace / Worktree / Change / Integration 八对象（决策 6-1~6-10） |

## 工作流蓝图（Blueprint）

| 文件 | 说明 |
| --- | --- |
| [BLUEPRINT-PROPOSAL.md](BLUEPRINT-PROPOSAL.md) | 工作流蓝图提案（2026-11-17 已批准，Phase A 启动）：把编排权威从自然语言迁到可序列化图数据 |
| [BLUEPRINT-SPEC.html](BLUEPRINT-SPEC.html) | 蓝本规范（Phase A–D 全量落地版）：图说了算的编译/执行/收口规范 |
| [blueprint-overview.html](blueprint-overview.html) | 图驱动自迭代总览（可交互 HTML） |
| [blueprint-overview.png](blueprint-overview.png) | 上述总览图截图 |
| [blueprint-overview.source.json](blueprint-overview.source.json) | 总览图的源数据（便于重新渲染） |

## RSI 自迭代（rsi/）

| 文件 | 说明 |
| --- | --- |
| [RSI.md](RSI.md) | W-RSI 总纲：把一次 goal 执行冻结为工作流图，异步分析编排结构并产出只读提案（不训练模型权重） |
| [rsi/self-iter-playbook.md](rsi/self-iter-playbook.md) | 自迭代手册：用 ouroflow 迭代 ouroflow——跑基准、量化编排开销、提案、批准、复测 |
| [rsi/proposals.md](rsi/proposals.md) | RSI R1 基线与瓶颈提案（round-1，只读分析产物） |
| [rsi/analysis-r3.md](rsi/analysis-r3.md) | RSI R3 分析报告：R2 过程暴露的问题清单与新提案 |
| [rsi/round-report-r3.md](rsi/round-report-r3.md) | RSI R3 轮次演进报告（R1→R3，含改进点与效果数字） |
| [rsi/review-v2.md](rsi/review-v2.md) | ouroflow-int-v2 自迭代分支审查报告 |
| [rsi/fix-v2.md](rsi/fix-v2.md) | ouroflow-int-v2 修复落地记录 |

## 调研（research/）

| 文件 | 说明 |
| --- | --- |
| [research/rsi-summary.md](research/rsi-summary.md) | W-RSI 调研总结与落地设计：学术/业界交叉核对、指标定义、可优化判定 |
| [research/rsi-academic.md](research/rsi-academic.md) | RSI 能力自迭代：学术调研（可验证、可回滚、不自我欺骗的闭环零件与坑） |
| [research/rsi-industry.md](research/rsi-industry.md) | RSI 能力自迭代：业界调研（agent 可观测性/评测平台、自优化产品、训练-推理开关） |
| [research/research-workflow-dsl-definitions.md](research/research-workflow-dsl-definitions.md) | 主流 LLM Agent / 工作流平台如何定义工作流：代码 vs 结构化 DSL，弹性放在哪一层 |
| [research/research-llm-supervisor-orchestration.md](research/research-llm-supervisor-orchestration.md) | LLM Supervisor/Orchestrator 编排的工程问题与业界对策（控制流收回代码、受约束路由） |
| [research/research-archive-turn3-main-report-and-self-iteration.md](research/research-archive-turn3-main-report-and-self-iteration.md) | 第三轮主报告与自迭代工作的归档（含会话/工具输出原始记录） |

## 报告与实施记录（reports/）

| 文件 | 说明 |
| --- | --- |
| [reports/benchmark-report.md](reports/benchmark-report.md) | R1 构建/测试基准测量报告（基线 `0bf354e`，冷/温口径） |
| [reports/benchmark-report-r2.md](reports/benchmark-report-r2.md) | R2 实施后基准复测（基线 `365e108`，P1/P2/P3 上线后） |
| [reports/benchmark-report-r3.md](reports/benchmark-report-r3.md) | R3 基准复测（新基线 `e7c2572`，冷缓存口径、无依赖并行重派） |
| [reports/impl-notes.md](reports/impl-notes.md) | RSI R2 实施说明：P1 测试并行化、P2 CDN 守卫 fail→skip、P3 不可重试快速失败 |
| [reports/proposals.md](reports/proposals.md) | RSI R1 优化提案（optimizer 只读复跑版；与 [rsi/proposals.md](rsi/proposals.md) 是不同轮次的两次提案） |

## 真实用例（use-cases/）

| 用例 | 文件 | 说明 |
| --- | --- | --- |
| [2026-11-icode-upgrade](use-cases/2026-11-icode-upgrade/README.md) | [README.md](use-cases/2026-11-icode-upgrade/README.md) · [timeline.md](use-cases/2026-11-icode-upgrade/timeline.md) · [analysis.md](use-cases/2026-11-icode-upgrade/analysis.md) · [execution-report.md](use-cases/2026-11-icode-upgrade/execution-report.md) · [findings-dispatch.md](use-cases/2026-11-icode-upgrade/findings-dispatch.md) · [findings-runtime.md](use-cases/2026-11-icode-upgrade/findings-runtime.md) · [findings-watchdog.md](use-cases/2026-11-icode-upgrade/findings-watchdog.md) | icode 0.28.0 → 0.35.2 升级的小队全流程真实执行报告与事后复盘（派发/运行时/看门狗三条链路） |
| [2026-11-14-kimicode-hitl-gate](use-cases/2026-11-14-kimicode-hitl-gate/analysis.md) | [analysis.md](use-cases/2026-11-14-kimicode-hitl-gate/analysis.md) · [findings-review-semantics.md](use-cases/2026-11-14-kimicode-hitl-gate/findings-review-semantics.md) | kimicode 高风险升级的人工授权闸门（F1/F2/F3/R3）案例与 review 状态机逐项分析 |
| [2026-11-16-new-goal-tab-vanish](use-cases/2026-11-16-new-goal-tab-vanish/analysis.md) | [analysis.md](use-cases/2026-11-16-new-goal-tab-vanish/analysis.md) · [findings-goal-create-tab-vanish.md](use-cases/2026-11-16-new-goal-tab-vanish/findings-goal-create-tab-vanish.md) | `/goals/new` 切 tab 丢对话、建任务半路被杀的根因与修复 |
| [2026-11-21-blueprint-self-iteration](use-cases/2026-11-21-blueprint-self-iteration/README.md) | [README.md](use-cases/2026-11-21-blueprint-self-iteration/README.md) · [timeline.md](use-cases/2026-11-21-blueprint-self-iteration/timeline.md) · [analysis.md](use-cases/2026-11-21-blueprint-self-iteration/analysis.md) · [execution-report.md](use-cases/2026-11-21-blueprint-self-iteration/execution-report.md) · [findings-bug1-deliver-sqlite-busy.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug1-deliver-sqlite-busy.md) · [findings-bug2-duplicate-approval.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug2-duplicate-approval.md) · [findings-bug3-blueprint-compile-observability.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug3-blueprint-compile-observability.md) · [findings-bug4-plan-vs-actual.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug4-plan-vs-actual.md) | 蓝本自迭代断链事故：四任务执行记录、根因确认、BUG-1/2/3 修复与 BUG-4（执行保真）缺口分析（绑定链、提案闭环与 P2 账本均已修）（原始材料在 [sources/](use-cases/2026-11-21-blueprint-self-iteration/sources/) 下；修复状态见 [execution-report.md](use-cases/2026-11-21-blueprint-self-iteration/execution-report.md)） |

---

维护约定：新增文档按主题归入上述子目录，并在本索引补一行说明；根目录只保留 README 入口。
