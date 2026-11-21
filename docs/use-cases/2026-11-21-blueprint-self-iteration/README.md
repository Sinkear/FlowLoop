# 真实用例：蓝本自迭代断链与交付锁死（自动升级流水线 v2）

> 2026-11-20 15:27 — 11-21 09:56（UTC+8）。四个「蓝本绑定」训练任务全部跑完、4 轮 RSI 分析全部 ready，蓝本自迭代却是 **0 提案**；同一夜一次 `SQLITE_BUSY` 让 opencode 的批准交付卡死 **49 分 48 秒**，用户的 3 次重批被防重复保护全部拒绝。
>
> 本目录是两份现场取证文档的**代码级复核版**（不是重写历史）：原始材料一字未改地放在 [sources/](sources/)，其中《根因确认》的反汇编结论在此被映射到当前仓库 HEAD 的具体行号，并标出「事故 build 事实」与「当前 HEAD 行为」的差异。

| 项 | 内容 |
| --- | --- |
| 用例编号 | `2026-11-21-blueprint-self-iteration` |
| 发生时间 | 2026-11-20 15:27:23 — 2026-11-21 09:56:22（UTC+8；DB 存 UTC） |
| 蓝本 | 自动升级流水线 v2 `0fef5c50…`，v1 `9ae4d32f…` published（11-20 12:05:44，human） |
| 域 | 自动升级 `73eaf8c1…`（scratch） |
| 参与 Agent | AI SHELL `b6e7384e` / Checker `f75f223f` / Upgrader `cbc2615f` / Verifier `c6f15196` / Packager `2b1998cf` |
| 规模 | 4 goals / 50 runs；机器工作 442.3m、墙钟 486.1m、人工评审等待 1,735.5m（≈28.9h） |
| 结果 | 4 个升级全部 done；自迭代闭环 0 提案；1 次交付卡死 49m48s，靠重启 daemon 自愈 |
| 证据等级 | 根因 A 级（反汇编 + DB + 在线实验三重）；代码位置在本仓库 HEAD 复核 |
| 文档基线 | `git HEAD a3d2744`（2026-11-21 15:05:23 +0800）。事故运行版本构建自已删除的 `/tmp/ouroflow-featnew`（`0.1.0-featnew.20261119`，无 vcs stamp） |

## 一句话结论

> **蓝本目标在 `Create` 时直接以 `active` 落库，而编译只挂在 `Activate`（backlog→active）上**——四个目标从创建起就没有 `blueprint_node`；训练分析跑完后，翻译器 `draftAutoProposals → proposeForkForChain` 查不到「本目标物化节点」，走**无日志的静默 return**，于是 5 条有效 findings 全部停在 `rsi_report` 里，`blueprint_proposal` 0 行。
>
> 同一时段，opencode 的 approve 已成功落库，但异步交付在 `MarkDelivered` 的 `UPDATE goal … WHERE status='review'` 上撞到 **SQLITE_BUSY**；错误被 `finishDeliver` 以 `Infof` 吞掉、无重试、无 `deliver:` 失败注记——防重复保护因此把这套「已批准但交付丢了」的状态当作「交付进行中」，连续拒绝 3 次人工重批，直到 09:56 手动重启 daemon，启动补偿 `recoverPendingDelivers` 才补交付完成。
>
> **追加（2026-11-21，BUG-4）**：即使不谈提案，这四个目标的「执行」本身也没有落到蓝本上——`blueprint_node` 0 行意味着**没有编译产物**，owner 却收到「计划已 COMPILED、不要自己干、`subgoal create` 会被拒」的 contract；HEAD 的 consult 只是 prompt 约定 + detached worktree，而事故 build 的 consult 连只读契约都没注入——它用完整工具集实际执行了升级（Upgrader 266 次调用），owner 只负责派发。即便把 consult 严格读成只读，平台也没给 owner 第二条路：`subgoal list` 为空、拆活被拒。执行图偏离不是「小队不听话」，是平台没物化计划又堵死了自由编排。详见 [findings-bug4](findings-bug4-plan-vs-actual.md)。
>
> **再追加（用户 2026-11-21 复核）**：**蓝本从未被读取**——`t1→t2→t3→t4` 的顺序来自 AI SHELL 的 `system_prompt` 与 `squad.instructions`（staged 进 `AGENTS.md`），`AGENTS.md`/评论/50 个 run 的工具调用里蓝本与节点 id 零出现；蓝本 v1 只是同一 SOP 的结构化副本。用户同时确认：consult **只许提意见**（事故 build 的执行是缺陷，HEAD 需工具层强制，不能只靠 prompt）；g1 的 `timeout.action=rejected` **不是有意的**。连 persona 里写死的「第 1 步建 sub_goal」都被模型静默跳过（4 goal sub_goal 全 0），平台照常放行——顺序性完全寄存在 prompt 上。见 [findings-bug4](findings-bug4-plan-vs-actual.md) §1.3、§三.4/§三.5。

> **实施（2026-11-21，用户确认后）**：BUG-4 的绑定链已落到工作树（未提交）——不变量（Create/Activate/终局守卫）、owner contract 说真话（pin 且 ledger>0）、dispatch 懒编译、consult 工具层只读、gate timeout 默认 park、consult 回答回填、spawn children 入 ledger、cap per-blueprint、reopen 显式活动、plan-vs-actual 对账（`GET /goals/{id}/plan-alignment` + RSI `plan_deviation` + 详情页卡片）。**2026-11-21 再补三项（用户「123 都要做」）**：gate `auto` 运行时判定（`auto.when`/`auto.action`，sweeper 对直接上游 sealed output 求值）、owner 写入归户（`Blueprint-Node:` trailer → `blueprint_write` → 计划外写入 deviation）、scratch `env_change` 账本（RPC/CLI/HTTP/详情卡 + scratch contract 记账要求）。至此 BUG-4 的 P0/P1/P2 机制均在工作树实现；未做项只剩「多证据 gate 求值 / 交付前硬拒绝 / 账本自动截获与回滚 / 前端编辑入口」，见 [execution-report](execution-report.md)。

## 场景：预期闭环 vs 实际

平台设计的蓝本自迭代闭环（代码事实，见 [analysis.md](analysis.md) §2）：

```text
Create(backlog) → Activate → compileBlueprintTx → blueprint_node 物化
        → 执行/训练 → 人工 approve → deliver → goal done
        → OnGoalTerminal → RSI Analyze → rsi_report ready + findings
        → rsi-translator: serial_chain finding → fork 补丁 → blueprint_proposal(pending)
        → 人工批准提案 → 新 draft 版本 → 下一轮任务
```

本次实际的链路：

```text
Create(active) ──✗ 无 compile──→ 执行（owner + consult 自由编排）
        → 人工 approve → deliver → done → RSI 分析 ready（5 findings）
        → translator 查 blueprint_node → 0 行 → return（无日志）──✗ 闭环断
```

四个训练任务是「创建即 active」的 HITL 目标（活动日志只有 `created → commented → entered_review → review_ready`，没有 `activated`；owner run 在创建后 0.05–1 秒内派发），绕过了唯一的编译入口。详见 [analysis.md](analysis.md) §2.4–§2.5。

## 影响面

| 面 | 事实 | 代价 | 证据 |
| --- | --- | --- | --- |
| 自迭代闭环 | 4 轮训练分析全 ready、5 条 findings，`blueprint_proposal` **0 行**、`blueprint_node` **0 行** | 蓝本 v2 一个版本都没被训练结果改进；下一轮任务仍跑旧 SOP | DB `count(*)=0`；[analysis.md](analysis.md) §2.3 |
| 状态机 | opencode 的 approve 落库但 `done` 永不到达；期间连 **reject 也被禁**（`goal.go:1975-1977`） | 49m48s 停摆；用户既不能重批也不能打回，只能重启进程 | daemon.log；[findings-bug1](findings-bug1-deliver-sqlite-busy.md) |
| 人工操作 | 09:07:01 / 09:09:09 / 09:17:59 三次点击批准全部拒绝，页面只有 "goal already approved" | 3 次无效操作 + 无失败解释；BUG-1 被放大成「按钮坏了」 | daemon.log ×3；[findings-bug2](findings-bug2-duplicate-approval.md) |
| 指标可信度 | openclaw 真实人工等待 ≈12.6h，`human_wait` 只记 **362s（4.2%）** | 「人工等待占大头」被低估 125 倍，蓝本优化方向被误导 | `gate_decision`；[analysis.md](analysis.md) §6 |
| 观测性 | 灌到 DEBUG 重放分析，`[rsi]` 日志仍为 0 条 | 断链只能靠翻 DB 发现；翻译器 10 个静默 return 无任何痕迹 | 在线实验；[findings-bug3](findings-bug3-blueprint-compile-observability.md) |
| 执行保真 | 4 个目标 `blueprint_node` 0 行；owner 收到「计划已编译」的 contract，但 `subgoal list` 为空、拆活被拒；consult 在 HEAD 仅为 prompt + worktree 约定（非工具强制），事故 build 中却是实际执行通道（Upgrader 266 calls 经 JEM API 改环境）；**顺序来自 persona/instructions，蓝本从未被读取** | 执行退化成咨询网：50 runs（owner 26 / consult 17 / review 7，0 个 subgoal run、0 change）里哪些是计划外编排无法归因；「按蓝图跑」无法从结果证明；persona 第 1 步建 sub_goal 被跳过也无人拦 | DB `blueprint_node=0`、`consult_request` 17 行；[findings-bug4](findings-bug4-plan-vs-actual.md) §1–§3 |
| 历史数据 | 4 个目标已 done，编译只发生在激活时，事后无法补 | 这 4 个目标永远无法验证「编译后会不会出提案」，需新建目标重跑 | 根因确认 §4；[analysis.md](analysis.md) §8 |

## 修复清单

严重度沿用原始文档（BUG-1 严重 / BUG-2 中 / BUG-3 中 / 设计缺陷-4 低 / 观察项-5 低）。**BUG-4 是 2026-11-21 追加识别的结构缺口**（用户观察「实际执行图全是咨询接口」），原始两份文档只留了一句断言，未编号、未分析、未修复。

| ID | 级别 | 一句话 | HEAD 位置（代码事实） | 状态 | 详文 |
| --- | --- | --- | --- | --- | --- |
| BUG-1 | 严重 | 交付收口撞 `SQLITE_BUSY` 无重试、错误被吞、恢复只在启动时跑一次 | `internal/daemon/deliver.go:330-332`、`internal/service/goal.go:3233-3272`、`internal/daemon/daemon.go:475` | 工作树已修复，未提交（见 execution-report） | [findings-bug1](findings-bug1-deliver-sqlite-busy.md) |
| BUG-2 | 中 | 防重复审批只看「最近一次 approve」，不看到期时间，也不区分交付中/交付失败 | `internal/service/goal.go:1956-1977` | 工作树已修复，未提交（见 execution-report） | [findings-bug2](findings-bug2-duplicate-approval.md) |
| BUG-3 | 中 | 创建即 active 绕过编译；翻译器 10 个静默退出点（仅 4 处有日志）；未编译状态无任何告警 | `internal/service/goal.go:266-465`、`internal/service/goal.go:2205-2258`、`internal/service/rsi_analyze.go:215-398` | 工作树已修复（Create 编译 + 10 处观测性），未提交（见 execution-report） | [findings-bug3](findings-bug3-blueprint-compile-observability.md) |
| BUG-4 | 严重（结构） | **计划从未成为执行**：没有「pinned ⇒ 已编译」不变量（0 行=放行）；未编译时 owner 被喂「已编译」人设 + 拆活被拒 → 只剩咨询（HEAD consult 仅 prompt/worktree 约定，事故 build 的 consult 是实际执行通道）；**蓝本从未被读取**——顺序由 AI SHELL `system_prompt` + `squad.instructions` 驱动，连 persona 第 1 步建 sub_goal 都被模型跳过；gate 的 auto 直通不可表达（g1 必人工，24h→rejected，用户确认非有意）；owner 计划外写入不入账；spawn children 不在 ledger；无 plan-vs-actual 对账 | `internal/service/goal.go:1649`、`internal/daemon/team_context.go:107`、`:304-329`、`internal/service/subgoal.go:88-95`、`internal/daemon/daemon.go:1828`、`internal/service/blueprint.go:1525-1545`、`internal/service/blueprint.go:444-455` | **机制已修复（工作树，未提交）**：P0 不变量、contract 说真话、dispatch 懒编译、consult 工具层只读、gate timeout 默认 park、consult 回答回填、cap per-blueprint、spawn children 入 ledger、reopen 显式活动、plan-alignment 对账（服务/HTTP/RSI/UI）、提案→下一轮显式入口、gate `auto` 运行时判定、owner 写入归户、scratch `env_change` 账本均已实现；**未做**：多证据 gate 求值（应写 condition）、交付前硬拒绝、账本自动截获/回滚、前端账本编辑入口（见 [execution-report](execution-report.md) 与 findings-bug4 §5） | [findings-bug4](findings-bug4-plan-vs-actual.md) |
| 设计缺陷-4 | 低 | 批准后的异步交付窗口没有**服务端可查**的过渡态；HEAD 单卡已有本地按钮禁用（`goal-actions.tsx:302-310`），但刷新/换端/批量栏仍可重复点，源码注释仍自认 "the page shows no feedback" | `internal/service/goal.go:1935-1938`、`web/components/goal-actions.tsx:262-313` | 部分缓解（HEAD 单卡本地态）/未系统修复 | [analysis.md](analysis.md) §5.6、[findings-bug2](findings-bug2-duplicate-approval.md) P1-3 |
| 观察项-5 | 低 | openclaw 唤醒 run `ba2246b1` 出现十余条 `event seq gap … lost on the wire` | 事件协议侧，本仓库未复现 | 未修 | [timeline.md](timeline.md) §3 |

> 状态以 [execution-report.md](execution-report.md) 为准：该文件已记录实际改动、与 findings 建议的偏差、包级测试结果和未做的范围；提交号待合并后回填。本目录其余文件的代码行号一律固定在 HEAD `a3d2744`，合并后行号会漂移。

## 关键指标

| 任务 | 升级结果 | runs | 人工评审等待 | 机器工作 | 机器墙钟 | 并行收益 | 创建→done | RSI 轮次 | findings |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| deepseek `aada5a0c` | dsh 0.2.1-rc.2 → 0.2.2-rc.5，PASS | 13 | **875.4m（82%）** | 161.5m | 185.9m | 0.87× | 17h40m | round 2 | 1 |
| minimax `2da62c29` | @minimax-ai/code 0.4.12 → 0.7.9，PASS（首轮因制品未发布被拒） | 15 | **49.4m（37%）** | 66.2m | 83.3m | 0.79× | 2h34m | round 1 | 2 |
| openclaw `b86244c3` | 2026.9.2 → 2026.11.6，17 PASS / 0 FAIL | 11 | **6.0m（4.2%）** + 12.6h 未计 | 136.3m | 137.9m | 0.99× | 15h02m（含 12.6h 暂停） | round 3 | 0 |
| opencode `ceb804a0` | 2.0.14 → 2.3.2，4 阶段全 PASS | 11 | **804.7m（91%）** | 78.3m | 79.0m | 0.99× | **15h39m** | round 4 | 2 |
| **合计** | 4/4 升级完成 | **50** | **1,735.5m（≈28.9h）** | **442.3m** | **486.1m** | — | 合计 **50h55m**（四任务有重叠；整体跨度 **18h29m**） | 4 轮 + 1 次重放 | **5** |

口径：评审等待 = Σ`gate_decision.review_duration`；占比 = wait / (wall + wait)。findings 共 5 条（3×`human_wait` + 2×`lost_parallelism`，3 confirmed / 2 open）。

两处必须带上的口径修正（推导见 [timeline.md](timeline.md) §0）：

- **opencode 创建→done = 15h39m05s**（18:17:17 → 09:56:22）。原始表写 16h29m，是把 50m 的 BUG 滞留重复计入（14h49m 批准等待 + 50m 滞留）；滞留已经包含在 15h39m 里。
- **openclaw 的 12.6h 中途授权暂停不进 `human_wait`**（事故 build 的 362s 只覆盖最后一次评审闸门）。按 HEAD 的代码路径，这笔等待反而**会**被计入——两种口径的冲突见 [analysis.md](analysis.md) §6，需用 featnew 源码/原 DB 复核。

RSI 趋势（`rsi_round` delta）：

| 轮次 | 1 (minimax) | 2 (deepseek) | 3 (openclaw) | 4 (opencode) |
| --- | ---: | ---: | ---: | ---: |
| 关键路径 | 32.1m | 101.1m | 71.3m | 31.4m |
| 并行收益 | 0.79× | 0.87× | 0.99× | 0.99× |
| 人工等待占比 | 37% | 82% | 4.2%（失真） | 91% |

后两轮并行收益 0.99× = 机器侧已无结构性并行损耗，**瓶颈完全转移到人工评审等待**；而这唯一的「自迭代输入」在本次事件里根本没有被翻译成提案。

## 目录

| 文件 | 内容 |
| --- | --- |
| [README.md](README.md) | 本文件：结论、场景、影响面、修复清单、指标总账、目录与使用方式 |
| [timeline.md](timeline.md) | 四任务全量时间线（50 runs）、09:00–10:00 争用窗口、RSI 轮次对比、口径修正 |
| [analysis.md](analysis.md) | 根因链 E1–E5、创建即 active 绕过编译、翻译器静默退出、SQLITE_BUSY 无重试、防重复无失败态、指标口径、修复对照、遗留取舍、代码位置附录 |
| [findings-bug1-deliver-sqlite-busy.md](findings-bug1-deliver-sqlite-busy.md) | BUG-1：交付 `SQLITE_BUSY` 无重试 → 永久 review；复现条件、修复方案、验收 |
| [findings-bug2-duplicate-approval.md](findings-bug2-duplicate-approval.md) | BUG-2：防重复审批无失败态识别、无时间维度；状态矩阵与验收 |
| [findings-bug3-blueprint-compile-observability.md](findings-bug3-blueprint-compile-observability.md) | BUG-3：创建即 active 不编译 + 翻译器静默退出 + 未编译无告警；验收含「必须出 WARN」 |
| [findings-bug4-plan-vs-actual.md](findings-bug4-plan-vs-actual.md) | BUG-4（追加）：事故数据实证的计划 vs 实际对照（50 runs = 26/17/7、0 subgoal run、0 change、17 consult 全空 response）、未编译时的咨询化执行、consult 实际执行通道、gate auto 不可表达、owner 计划外写入、spawn/reopen 缺口、plan-vs-actual 对账缺失；**不变量/contract/懒编译/对账/回填/ledger/只读/park/提案推进/gate auto/写入归户/scratch 账本均已实现（工作树）**，含 P0/P1/P2 分级修复设计与待核 Q1–Q5 |
| [execution-report.md](execution-report.md) | 修复执行报告（BUG-1/2/3 已实现；BUG-4 机制已实现：不变量/contract/懒编译/对账/回填/ledger/只读/park/提案推进/gate auto/写入归户/scratch 账本；Lead 汇总验证） |
| [sources/](sources/) | 两份未改动的现场取证文档（反汇编、DB、日志、在线实验原始记录） |

## 使用方式

1. 只想知道发生了什么：读本文件「一句话结论」「影响面」「关键指标」三节，约 3 分钟。
2. 想核对时间账：读 [timeline.md](timeline.md) §0 口径与 §1 总账，再按任务看 §2 的 run 明细。
3. 想复核根因或引用行号：读 [analysis.md](analysis.md) §2（自迭代）与 §3（状态机），每条结论都带 `文件:行` 或 DB/日志证据，并标注【已确认】/【推断】。
4. 想动手修：按 BUG 编号读 findings 的「复现条件 → 影响 → 修复方案 → 验收」；**BUG-4 的绑定链已修**（P0 不变量、contract 说真话、懒编译、consult 只读、gate park、对账、回填、spawn ledger、cap、reopen；工作树未提交），剩余多证据 gate/硬拒绝/账本自动截获见 execution-report「未做」；事故数据包已到并已并入 findings-bug4（§1 事实表、§6 已补证/仍待核 Q1–Q5）。
5. 想知道修完没有：读 [execution-report.md](execution-report.md)（已由 Lead 汇总实际改动与验证结果；提交号待回填）；未列出的修复项一律按 findings 的「HEAD 判定」列视为未修。
6. 引用本用例：代码位置以 `HEAD a3d2744` 为准；事故运行版本的行为以 [sources/蓝本自迭代断链-根因确认.md](sources/蓝本自迭代断链-根因确认.md) 的反汇编/DB 记录为准，两者不可混用。

## 与原始文档、旧结论的关系

- `sources/` 两份文档保留 2026-11-21 的原始结论与取证命令，**本文档集不修改它们**。
- 原《四任务执行记录》§3 的三条候选（A 未接线 / B 触发条件未满足 / C 路径未启用）已被《根因确认》推翻并定论：真正原因是**从未编译 + 静默退出**，不是接线或阈值问题。本目录用 HEAD 源码再次确认了这条链路。
- 本目录新增的是：当前仓库可核对的行号、「事故 build vs HEAD」差异、两处数字口径修正、以及把「修复建议」落成带验收的 findings。
- 当前源码比事故 build 新（含 `blueprint` 模块与 `SetBlueprint` 接线，`cmd/ouroflow-daemon/main.go:154`），但 **BUG-1/2/3 的缺陷在 HEAD 依然存在**（见各 findings 的 HEAD 判定）；修复分支的工作树已实现 BUG-1/2/3 与交付单飞，未提交内容不作为本目录的基线（以 execution-report 为准）。
- **BUG-4 是本目录追加的**：原始两份文档只留了一句「AI SHELL 是拿着蓝本描述当 prompt 自由编排的……平台层面没有节点级的计划图 vs 实际执行图绑定」（[sources/蓝本自迭代断链-根因确认.md](sources/蓝本自迭代断链-根因确认.md) 第 58 行），没有把它编号、分析或修复。本目录把它展开成可判定的缺口清单，并明确**未修**、待事故环境数据补证。