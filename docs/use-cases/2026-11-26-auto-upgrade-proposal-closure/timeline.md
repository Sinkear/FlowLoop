# Timeline：11-26 自动升级流水线事故时间线与口径修正

> 时间均取自 sources/ 附件 §5.2/§5.3/§9 及其引用的 daemon 日志、DB 记录；行号为附件行号。口径修正表 = 附件自勘误（§9）+ 本次源码核实对附件推断的更正。

## 1. 口径修正表

| # | 原口径 | 修正为 | 依据 |
|---|--------|--------|------|
| 1 | 「Verifier 复验 run 空挂 8 小时才被 claim」 | 时区换算失误：`12:03:12Z`＝本地 20:03:12，claim 于 20:03:20，间隔 **8 秒**；owner run 87d6295a（19:48 启动）至 20:09 仍在 running（≥21 分钟）属实但不构成 8 小时 | 附件 §9.1 自勘误 |
| 2 | 「agent-browser 的包是流水线外落地的」 | 制品时间戳（15:09–15:10）与流水线内 Packager run ce18cfa（15:01–15:11）吻合，重判为流水线内产出；真实问题降级为**节点簿记漏水**（sub_goal 卡 running、0 验证、goal 照标 done） | 附件 §9.2 自勘误 |
| 3 | P3 待查：「生成器只处理 domain 首轮 / 按 category 去重」 | 不成立：round_no 不参与任何过滤；真实机制＝跨轮碰撞守卫（`rsi_translate.go:173-194` 灌 `t.touched`）+ touched 命中与同标题 dedup 的 Debugf **静默**跳过（`:304-319`），R1 两提案 pending 占用节点后封死 R2/R3 | 本次源码核实（findings-P3.md） |
| 4 | P4 待查：「spawn 被当时 parked 的 openviking goal 阻塞 / 与 P3 同源」 | 不成立：无任何阻塞关系；`StartNextRoundFromProposal` 是唯一链路且**纯人工按钮**触发，approve 不联动；11-23 的 12 秒间隔＝人工手速，非自动 | 本次源码核实（findings-P4.md） |
| 5 | P2：「fork 覆盖」现象描述 | 机制落行：`ApproveProposal` 只对提案自己的 `BaseVersionID` 重放补丁（`blueprint.go:3737-3794`），不检查 base 是否落后于蓝本最新版本——同基线第二条提案从 v1 内容 fork 出 v3，v2 改动被静默丢弃 | 本次源码核实（findings-P2.md） |
| 6 | P6：「done 漏水 / failed 不自愈」现象描述 | 机制落行：`MarkDelivered` 零校验且其注释假设被 gate approve 自动路径打破（`goal.go:3447-3485`）；`gatedSubGoalsTx` 只扫 `status='running'`（`subgoal.go:1798-1812`），failed 下游不在任何唤醒集合 | 本次源码核实（findings-P6.md） |

## 2. 时间线总账

### 2.1 11-23 健康对照（同一链路正常走通，pin `7d83f4cd`）

| 时刻 | 事件 |
|------|------|
| 10:21:25 | 自动提案生成：「并行化串行链（…10 节点）」，against `1ecaaf32`，18 ops |
| 10:31:34 | 提案 approved → draft v3（`7d83f4cd`） |
| 10:31:46 | `rsi: next round for proposal … → goal 9605d89dce… (pin 7d83f4cd, active)` ——approve 后 **12 秒**＝人工快速点击按钮，非自动 |

### 2.2 11-26 事故日（三任务：R1 agent-browser / R2 drawio / R3 openviking，全 pinned v1=`16ebee24`）

| 时刻 | 事件 | 关联问题 |
|------|------|----------|
| 14:18–15:08 | agent-browser：Checker→Upgrader(35m)→Verifier 全部完成 verified（机器闸门 passed，run `f76279ae`/`102844e1`） | — |
| 15:00:16 | agent-browser gate approve（human，180s） | — |
| 15:08–15:20 | 回退修复 Upgrader attempt 1/2/3 全部失败（**token 额度**）；其间 Packager 出现 23s、2s 两个异常短 run | **P7** |
| **15:14:19** | R1（agent-browser，goal `d5f0e5ba…`）分析：**两条提案生成**——「改派 t_upgrade/t_repair」（wrong_assignee）＋「计划节点纪律 t_check」（plan_deviation）。全天 `[rsi] auto-proposal drafted` 仅此两行 | — |
| 15:17 | 冗余 Packager run `edf439c6` 入队，一直未执行 | **P6**（簿记） |
| **15:24:19** | R2（drawio）分析时刻：**零提案行**（1 条 wrong_assignee finding 未翻译）；同刻 openviking goal parked for human authorization | **P3** |
| **15:34:19** | R3（openviking）分析时刻：**零提案行**（5 条 findings 未翻译，含最高分 70.0） | **P3**（＋故障期样本污染，P5/P7） |
| 16:04–16:23 | Packager 三次尝试后 **16:23:44 发出 BLOCKED 停机上报**（受限命名空间；写传输脚本被 ACP 客户端硬拒） | — |
| 16:59–19:25 | 无任何 run（等待人工介入） | — |
| 19:26 | 冗余 Packager run `edf439c6` 被 stopped——goal 早已标 done | **P6**（goal done 但 sub_goal 长期未收口） |
| **19:29:25** | 改派提案 approved → draft **v2（`fef7788c`）** | **P2**（此后从未发布） |
| **19:29:31** | 计划纪律提案 approved → draft **v3（`a5443d0b`）**（随后 published）——**从 v1 fork，不含 v2 改动** | **P2** |
| 19:34:02 | openviking gate approve（human，300s；19:25 人工 reopen＋切模型在前） | — |
| **19:43–19:54** | Upgrader 两批共 **6 次重试**全部失败于同一 token 额度错误——**人工已切模型，模型切换对重试批次未即时生效** | **P7** |
| 20:00–20:02 | Upgrader `d723908f` 完成，回退修复节点 machine gate passed（20:02:37） | — |
| 20:03:12/20:03:20 | Verifier 复验 run `10a5c622` 入队/被 claim（间隔 **8 秒**，见勘误 #1） | — |
| **20:09** | 复验 PASS（20/0/13 SKIP）；**同刻评论确认下游「人工卡点」(9d3236b9)、「Packager·打包」(3f1df997) 仍 failed，mention 交接无法使其自动回 running，需 owner retry** | **P6** |
| 20:09+ | owner run `87d6295a`（19:48 启动）仍在 running，daemon 反复 `coalesced … (already running)` | — |
| 全天 | **无任何 `rsi: next round for proposal` 日志行；goal 表无 pin v2/v3 的 goal** | **P4** |

### 2.3 事故日终局快照

- **agent-browser**：goal=done，但 Packager sub_goal=running、verification_result=0 条；产物已实际交付（制品时间戳 15:09–15:10 与 run `ce18cfa` 15:01–15:11 吻合）——**簿记漏水而非漏打包**（勘误 #2）。
- **openviking**：Verifier PASS 后流程停在打包节点，两个 failed 下游至 20:09+ 仍需 owner 手工 retry（P6 自愈缺口的原样案例）。
- **v2/v3**：v2（改派修正）从未发布即被 v3 覆盖语义；v3 published 后 0 执行（P4）——两条改进路径同时归零。
