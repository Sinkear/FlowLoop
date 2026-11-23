# OuroFlow 文档索引

项目入口仍是根目录的 [README.md](../README.md)（英文）与 [README.zh.md](../README.zh.md)（中文）。
本目录集中存放产品/架构说明、设计与协作决策、专题指南、调研、基准与 RSI 记录、真实用例复盘；根目录不再散放文档。

> **先读这一行**：想知道「项目现在怎么工作」，读 [OUROFLOW-SPEC.html](OUROFLOW-SPEC.html)；想知道「为什么这样设计、边界在哪」，读 [design/DESIGN.md](design/DESIGN.md) 与 [design/Collaboration.v2.md](design/Collaboration.v2.md)。

## 文档角色地图

| 角色 | 文件 | 地位 |
| --- | --- | --- |
| **全项目详细说明（现行正本）** | [OUROFLOW-SPEC.html](OUROFLOW-SPEC.html) | 产品、架构、运行、协作、交付、蓝图与 RSI 的总入口；**当前行为与流程以此为准** |
| **产品与架构决策基线** | [design/DESIGN.md](design/DESIGN.md) | 决策 2-x~8-x、状态机、验收与不变量；代码注释的 `DESIGN.md §N` 指向它 |
| **协作模型终稿** | [design/Collaboration.v2.md](design/Collaboration.v2.md) | 8 对象、四原语、16 条 runtime invariants；取代 v1 |
| **蓝图设计依据与 schema 存档** | [BLUEPRINT-PROPOSAL.md](BLUEPRINT-PROPOSAL.md) | Phase A–D + §14 的设计原文；schema/节点语义的正式记录 |
| **已归档（不再维护）** | [archive/](archive/) | 被取代但有历史价值的设计稿；现行事实以 SPEC 为准 |

## 顶层指南

| 文件 | 说明 |
| --- | --- |
| [PATHS.md](PATHS.md) | 路径与数据布局：每个目录/环境变量，以及升级时必须保留的数据 |
| [UPGRADE.md](UPGRADE.md) | 保留数据的升级：stop → 备份 → 只替换 `bin/`、`web/` → start，含失败回滚 |
| [IDENTITY.md](IDENTITY.md) | Run 身份与指令归属：一次共用 workdir 身份劫持事故的复盘与三层修复 |
| [TEMPLATES.md](TEMPLATES.md) | 把最佳实践做成模板：registry + YAML 的消费链路与反向导出 |
| [RSI.md](RSI.md) | W-RSI 总纲：把一次 goal 执行冻结为工作流图，异步分析编排结构并产出只读提案（不训练模型权重） |

## 工作流蓝图（Blueprint）

| 文件 | 说明 |
| --- | --- |
| [BLUEPRINT-PROPOSAL.md](BLUEPRINT-PROPOSAL.md) | 蓝图设计依据（已落地 Phase A–D + §14）：把编排权威从自然语言迁到可序列化图数据；含 §11–§14 定稿与 schema |
| [blueprint-overview.html](blueprint-overview.html) | 图驱动自迭代总览（可交互 HTML） |
| [blueprint-overview.png](blueprint-overview.png) | 上述总览图截图 |
| [blueprint-overview.source.json](blueprint-overview.source.json) | 总览图的源数据（便于重新渲染） |

## 图示资产（assets/）

| 文件 | 说明 |
| --- | --- |
| [assets/ouroflow-self-iteration-en.svg](assets/ouroflow-self-iteration-en.svg) | 根 README 首页主流程图（英文）：编译图 vN → 执行 → 留痕 → RSI 诊断 → 图补丁 → 人批准发布 vN+1 |
| [assets/ouroflow-self-iteration-zh.svg](assets/ouroflow-self-iteration-zh.svg) | 同上，中文版（README.zh.md 首页使用） |

## 设计与协作（design/）

| 文件 | 说明 |
| --- | --- |
| [design/DESIGN.md](design/DESIGN.md) | 产品与架构**决策基线**：产品定义、状态机、验收、工作区、交付、工程不变量、模块边界、决策 2-x~8-x、后置问题 |
| [design/Collaboration.v2.md](design/Collaboration.v2.md) | 协作模型终稿：Goal / Run / Sub-goal / Workspace / Worktree / Change / Verification 八对象、四原语、权限矩阵、16 条 invariants；附录 A–D 保留 v1 的教学骨架 |

## RSI 自迭代（rsi/）

| 文件 | 说明 |
| --- | --- |
| [rsi/self-iter-playbook.md](rsi/self-iter-playbook.md) | 自迭代手册：用 OuroFlow 迭代 OuroFlow——跑基准、量化编排开销、提案、批准、复测 |
| [rsi/r1-proposals.md](rsi/r1-proposals.md) | RSI R1 两套提案汇编：A 组编排层（`critical_path_ms=293,501`）与 B 组测试层 P1/P2/P3，分列来源与状态 |
| [rsi/r3-report.md](rsi/r3-report.md) | R3 轮次报告：S1–S4 问题清单、R1→R3 演进与效果数字、P-A/P-B/P-C 提案与遗留（合并 analysis + round-report） |
| [rsi/int-v2-review-fix.md](rsi/int-v2-review-fix.md) | `ouroflow-int-v2` 审查与修复落地：17 场景、P0/P1/P2、`ae12054` → fix 分支 10 commits、验证与残留 |

## 调研（research/）

| 文件 | 说明 |
| --- | --- |
| [research/rsi-summary.md](research/rsi-summary.md) | W-RSI 调研总结与落地设计：学术/业界交叉核对、指标定义、可优化判定（第三版，Phase C 前；指标口径以 SPEC 第 20 章为准） |
| [research/rsi-academic.md](research/rsi-academic.md) | RSI 能力自迭代：学术调研（可验证、可回滚、不自我欺骗的闭环零件与坑） |
| [research/rsi-industry.md](research/rsi-industry.md) | RSI 能力自迭代：业界调研（agent 可观测性/评测平台、自优化产品、训练-推理开关） |
| [research/research-workflow-dsl-definitions.md](research/research-workflow-dsl-definitions.md) | 主流 LLM Agent / 工作流平台如何定义工作流：代码 vs 结构化 DSL，弹性放在哪一层 |
| [research/research-llm-supervisor-orchestration.md](research/research-llm-supervisor-orchestration.md) | LLM Supervisor/Orchestrator 编排的工程问题与业界对策（控制流收回代码、受约束路由） |
| [research/research-archive-turn3-main-report-and-self-iteration.md](research/research-archive-turn3-main-report-and-self-iteration.md) | 第三轮主报告与自迭代工作的归档（含会话/工具输出原始记录；历史材料，勿据此改代码） |

## 报告与实施记录（reports/）

| 文件 | 说明 |
| --- | --- |
| [reports/benchmark-history.md](reports/benchmark-history.md) | R1→R3 基准历史（`0bf354e` / `365e108` / `e7c2572`，冷/温口径与跨轮对比）＋ R2 实施说明（P1/P2/P3）；引用历史数字必须带口径 |

## 真实用例（use-cases/）

| 用例 | 文件 | 说明 |
| --- | --- | --- |
| [2026-11-icode-upgrade](use-cases/2026-11-icode-upgrade/README.md) | [README.md](use-cases/2026-11-icode-upgrade/README.md) · [timeline.md](use-cases/2026-11-icode-upgrade/timeline.md) · [analysis.md](use-cases/2026-11-icode-upgrade/analysis.md) · [execution-report.md](use-cases/2026-11-icode-upgrade/execution-report.md) · [findings-dispatch.md](use-cases/2026-11-icode-upgrade/findings-dispatch.md) · [findings-runtime.md](use-cases/2026-11-icode-upgrade/findings-runtime.md) · [findings-watchdog.md](use-cases/2026-11-icode-upgrade/findings-watchdog.md) | icode 0.28.0 → 0.35.2 升级的小队全流程真实执行报告与事后复盘（历史记录） |
| [2026-11-14-kimicode-hitl-gate](use-cases/2026-11-14-kimicode-hitl-gate/analysis.md) | [analysis.md](use-cases/2026-11-14-kimicode-hitl-gate/analysis.md) · [findings-review-semantics.md](use-cases/2026-11-14-kimicode-hitl-gate/findings-review-semantics.md) | kimicode 高风险升级的人工授权闸门（F1/F2/F3/R3）案例与 review 状态机逐项分析（历史记录） |
| [2026-11-16-new-goal-tab-vanish](use-cases/2026-11-16-new-goal-tab-vanish/analysis.md) | [analysis.md](use-cases/2026-11-16-new-goal-tab-vanish/analysis.md) · [findings-goal-create-tab-vanish.md](use-cases/2026-11-16-new-goal-tab-vanish/findings-goal-create-tab-vanish.md) | `/goals/new` 切 tab 丢对话、建任务半路被杀的根因与修复（历史记录） |
| [2026-11-21-blueprint-self-iteration](use-cases/2026-11-21-blueprint-self-iteration/README.md) | [README.md](use-cases/2026-11-21-blueprint-self-iteration/README.md) · [timeline.md](use-cases/2026-11-21-blueprint-self-iteration/timeline.md) · [analysis.md](use-cases/2026-11-21-blueprint-self-iteration/analysis.md) · [execution-report.md](use-cases/2026-11-21-blueprint-self-iteration/execution-report.md) · [findings-bug1-deliver-sqlite-busy.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug1-deliver-sqlite-busy.md) · [findings-bug2-duplicate-approval.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug2-duplicate-approval.md) · [findings-bug3-blueprint-compile-observability.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug3-blueprint-compile-observability.md) · [findings-bug4-plan-vs-actual.md](use-cases/2026-11-21-blueprint-self-iteration/findings-bug4-plan-vs-actual.md) | 蓝本自迭代断链事故：执行记录、根因与 BUG-1/2/3 修复、BUG-4 缺口分析（历史记录；原始材料在 [sources/](use-cases/2026-11-21-blueprint-self-iteration/sources/)） |

## 归档（archive/）

归档区只放「被取代但仍具历史价值 / 代码锚点价值」的文档：**不再维护、不保证链接可用，任何现行事实以 [OUROFLOW-SPEC.html](OUROFLOW-SPEC.html) 为准。**

| 文件 | 说明 |
| --- | --- |
| [archive/DESIGN-vision-2026-08.html](archive/DESIGN-vision-2026-08.html) | 2026-08 架构愿景（Phase A 之前）：产品边界与目标态设计；已被 SPEC + DESIGN.md 取代 |
| [archive/Collaboration.v1-2026-08.md](archive/Collaboration.v1-2026-08.md) | 协作机制 v1（2026-08-13 采纳）：历史设计史；教学骨架已并入 [Collaboration.v2.md](design/Collaboration.v2.md) 附录 A–D |

> `marketing/` 存放对外文案资料（如 GitHub SEO），**不是产品文档**，不随代码行为同步。site 的 `/docs/*` 页面是对外译文摘要，与 `docs/` 双轨；事实以本目录为准。

---

## 维护约定

1. **全项目详细说明只有一个入口**：[OUROFLOW-SPEC.html](OUROFLOW-SPEC.html)。任何行为、架构、流程、接口、配置、运维或里程碑变更，**必须在同一个 PR 内更新 SPEC 对应章节**；只改代码不改文档视为未完成。
2. 决策级变更（新的工程不变量、状态机或边界取舍）同时写入 [design/DESIGN.md](design/DESIGN.md) 决策记录（编号顺延）；协作模型变更同时更新 [design/Collaboration.v2.md](design/Collaboration.v2.md)。
3. 蓝图 schema 或节点语义变更，除 SPEC 外还必须同步 [BLUEPRINT-PROPOSAL.md](BLUEPRINT-PROPOSAL.md)。
4. 新增文档按主题归入上述子目录，并在本索引补一行说明；根目录只保留 README 入口。
5. 被取代的文档移入 `archive/` 并在顶部加「归档 + 取代者」横幅，不要静默删除；删除前先 `grep` 全仓引用，避免断链与代码注释失锚。