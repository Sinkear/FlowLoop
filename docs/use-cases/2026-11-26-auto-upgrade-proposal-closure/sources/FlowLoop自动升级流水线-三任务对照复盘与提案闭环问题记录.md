# 「自动升级流水线」三任务对照复盘与提案闭环问题记录

> 生成时间：2026-11-26 20:30（本地 UTC+8）
> 数据来源：ouroflow 沙箱数据库 `/root/job-envs/sandboxes/ouroflow-1795367359/.flowloop/flowloop.db`（blueprint / blueprint_version / blueprint_proposal / goal / run / sub_goal / verification_result / gate_decision / agent / rsi_report / rsi_round / rsi_finding / comment 表）、daemon 日志 `/root/job-envs/sandboxes/ouroflow-1795367359/.flowloop/daemon.log`（时区 +08:00）、goal 评论原文、制品目录 `/root/job-envs/sandboxes/ouroflow-1795367359/jobEnvPackage-backup-20261126/`。
> **时区约定：数据库时间戳一律为 UTC，daemon 日志为 +08:00。本文除标注 (UTC) 外均为本地时间（UTC+8）。** 本文档前序口头分析中有两处时区/归因错误，已在 §9 勘误。

---

## 0. 背景与问题缘起

用户在 FlowLoop Web 端查看「自动升级流水线」蓝本的版本提案 1/2/3，**感觉三个版本没有区别**。经查证：三个版本拓扑（节点/连线/布局）完全相同，差异仅存在于节点属性字段（assignee、brief）中，画布视图不可见。进一步复盘发现该蓝本在 11-26 以同一版本（v1）并发跑了三个升级任务（作为 RSI 训练对照），三轮分析产出 9 条 findings、2 条提案、1 条发布版本，但学习闭环在多个环节断裂。

本文档记录：① 三任务与学习链的完整取证数据；② 两条产品/机制改进诉求（版本对比视图字段级高亮、同基线提案冲突提示/rebase）；③ 其余在复盘中发现的系统性问题。

---

## 1. 关键结论速览

| # | 问题 | 严重度 | 一句话结论 |
|---|---|---|---|
| P1 | 版本对比视图只呈现拓扑，字段级差异不可见 | 产品体验 | v1→v3 唯一改动是 t_check 的 brief 追加，v1→v2 唯一改动是 2 行 assignee，拓扑零变化，画布上"看不出区别"是必然 |
| P2 | 同基线第二条提案 approve 造成 fork 覆盖 | 机制缺陷 | 两条提案都以 v1 为 base、6 秒内先后 approve，改派提案生成 v2（draft，从未发布）被 v3 挤出发布线，已批准的优化静默丢失 |
| P3 | 提案生成器只对 round 1 生成提案 | 机制缺陷 | R2/R3 共 6 条 findings（含全场最高分 70.0 与两个新类别）从未进入提案翻译流程 |
| P4 | 审批后未 spawn 下一轮 pinned 新版本的 goal | 机制缺陷 | v3 发布至今 0 次执行，`expected_metric=wall_ms down` 无从验证；11-23 另一蓝本存在健康对照 |
| P5 | RSI 发现盲区 + 学习样本被故障期污染 | 分析缺陷 | R2 最大的两块浪费（派发等待≈墙钟全长、人工等待 86 分钟）零 finding；R3 样本含 token 配额故障期 |
| P6 | goal 终态与节点状态漏水 | 状态机缺陷 | goal=done 但 Packager 节点 running/0 验证；gate 已 approve 但卡点节点仍 failed 且不能自愈恢复 |
| P7 | 不可重试错误被机械重试 | 容错缺陷 | 同一句"免费 Token 额度已用完"错误重试 9 次（每节点打满 attempt=3），跨两个时段共 10 次失败 run |

**对"v3 相对 v1 是否有效改动"的判定**：既不能证明有效，从机制上看大概率无效——0 次执行、机制错位、根因错配、优先级倒挂（详见 §6.1）。

---

## 2. 蓝本与版本数据

蓝本：**自动升级流水线**，ID `051dbbd8f71ef729a1050ae9aa78a099`（squad_id 归属下，描述：Checker 检查 → Upgrader 升级修复 → Verifier 独立验证（FAIL 回退修复一轮复验，最多 2 轮）→ 打包前人工卡点 → Packager 打包）。

### 2.1 版本清单（blueprint_version 表原值）

| 版本 | id | status | content_hash | created（本地） | changelog |
|---|---|---|---|---|---|
| v1 | `16ebee24dd5c3e64f1328d5225c038c5` | published | `d8ca712d8bbbfab3090735cd1d54888085f6a43845b4136f5dd51aa58ebee9e7` | 11-26 11:18:54 | （空） |
| v2 | `fef7788c59fc4301097cac5a3da890cd` | **draft（从未发布）** | `d4273dcc575788b49581328b621291ec49454086837b98c60d20ca0623ad1002` | 11-26 19:29:25 | 执行通道负载失衡（最忙 8437c7e866da8feec6f8de8a1292addb 累计 1393437ms，最闲 e954c148283f28004424f63cb5ed2aa8…） |
| v3 | `a5443d0bc750813230425288d64a7744` | published | `a037744d74093fd3c5a142519fc2b2f77113452259ada1d4a2162a166924e333` | 11-26 19:29:31 | （空） |

三个 content_hash 互不相同 → **数据层面有差异**；差异定位见 §2.3。

### 2.2 节点与执行通道（agent 表）

| 角色 | agent id | v1 承担节点 |
|---|---|---|
| Checker | `9f5e6c586107932f9de5e47ccba22a7c` | t_check |
| Upgrader | `8437c7e866da8feec6f8de8a1292addb` | t_upgrade、t_repair |
| Verifier | `08ef4c09d04e4e87c6b7a57938115c15` | t_verify、t_reverify |
| Packager | `294037b3f686f644887226cb7e659741` | t_package |
| AI SHELL | `e954c148283f28004424f63cb5ed2aa8` | leader/owner（不在蓝本节点内） |

### 2.3 三版本 content 实际 diff（json.tool 格式化后逐行比对）

**v1 → v2（改派提案的落地结果）**，全文仅 2 行差异：

```
t_upgrade.task.assignee: agent:8437c7e866da8feec6f8de8a1292addb (Upgrader) → agent:e954c148283f28004424f63cb5ed2aa8 (AI SHELL)
t_repair.task.assignee: agent:8437c7e866da8feec6f8de8a1292addb (Upgrader) → agent:e954c148283f28004424f63cb5ed2aa8 (AI SHELL)
```

**v1 → v3（当前线上版，计划纪律提案的落地结果）**，全文仅 1 处差异：

```
t_check.task.brief 末尾追加：
"\n\n## 计划节点纪律（RSI 自动补充）\n本 goal 绑定了蓝本规范图，执行时按编译出的节点走：
不要用 consult 派发替代节点执行，不要在计划外直接落盘产物；确需偏离时在计划元数据登记
plan_mode=manual，并在交付中说明原因。"
```

**v2 → v3**：上述 brief 追加 + 两处 assignee 改回 Upgrader（即 v3 完全不含 v2 的改派改动）。

**结论：三个版本拓扑（nodes/edges 9 节点 15 边）与布局零差异**，版本对比若只渲染拓扑图则 v1/v2/v3 视觉完全一致——这就是"感觉没区别"的直接原因。

---

## 3. 三个对照任务基本盘（全部 pinned v1）

`goal.blueprint_version_id` 三者均为 `16ebee24dd5c3e64f1328d5225c038c5`（v1），`execution_mode=train`，`category=hitl`。

| RSI 轮次 | goal | goal id | 创建（本地） | 终态 | run 数 | failed | cancelled | 升级内容 |
|---|---|---|---|---|---|---|---|---|
| R2（round_no=2） | 将Next AI Draw.io升级至最新版本 | `fca8572970a08571f7315508ddc36cff` | 11:20:40 | **done** | 14 | 0 | 1 | 模板 1.0.3→1.0.4 |
| R3（round_no=3） | 将openviking升级至最新版本 | `aa4541a9915cfee26fe0430bb1f0555d` | 14:18:41 | **active（卡死中）** | 31 | **9** | 1 | 应用 0.5.3→0.5.6，平台轴 1.0.9→1.0.10 |
| R1（round_no=1） | 将agent browser升级至最新版本 | `d5f0e5baffb036b3a1bf3e9609ae45a0` | 14:22:15 | **done** | 16 | 1 | 1 | 0.35.1→0.45.0（平台 1.0.1） |

> round_no 编号与创建顺序不一致：RSI 轮次按训练域内 terminal 先后编号，agent-browser 先 terminal（round 1），drawio 后（round 2）。

**失败原因单点归因**：10 次 failed run 的 `result_summary` 逐字相同——`prompt: acp: session/prompt: Internal error: 您当日免费模型Token额度已用完，每日零点刷新额度`。

**角色分布**（run 表 role 统计，三 goal 合并）：

| role | 执行者 | run 数 |
|---|---|---|
| owner | AI SHELL | 27 |
| subgoal | Upgrader | 15 |
| subgoal | Verifier | 6 |
| subgoal | Checker | 3 |
| subgoal | Packager | 2 |
| consult | Packager | 6 |
| consult | Checker | 2 |

**人工卡点决策**（gate_decision 表）：

| goal | decision | decided（本地） | review_duration |
|---|---|---|---|
| drawio | approve（human） | 14:10:58 | **5160s（86 分钟）** |
| agent-browser | approve（human） | 15:00:16 | 180s（3 分钟） |
| openviking | approve（human） | 19:34:02 | 300s（5 分钟） |

### 3.1 openviking（R3）关键时间线（本地时间）

| 时间 | 事件 |
|---|---|
| 14:18–15:08 | Checker→Upgrader(35m)→Verifier 全部完成并 verified（verification_result 表：机器闸门 passed，run f76279ae / 102844e1） |
| **15:08–15:20** | 回退修复 Upgrader attempt 1/2/3 **全部失败**（token 额度）；其间 Packager 出现 23s、2s 两个异常短 run |
| 16:04–16:23 | Packager 三次尝试后于 **16:23:44 发出 BLOCKED 停机上报**（评论原文见 §7.4：受限命名空间看不到 /root/runtime 与 /root/template，写传输脚本被 ACP 客户端硬拒） |
| 15:24 起 | goal parked for human authorization（daemon 日志 15:24:19） |
| 16:59–19:25 | 无任何 run（等待人工介入） |
| 19:25–19:34 | 人工 reopen（handoff_note 原文见 §7.3，模型切换 OpenGW/deepseek-v4.1-flash-1017）→ gate approve 19:34:02 |
| **19:43–19:54** | Upgrader 两批共 6 次重试**仍全部失败于同一 token 额度错误**（模型切换未立即生效） |
| 20:00–20:02 | Upgrader d723908f 完成，回退修复节点 machine gate passed（20:02:37） |
| 20:03:12/20:03:20 | Verifier 复验 run 10a5c622 入队/被 claim（间隔 8 秒，**非此前误称的 8 小时**） |
| 20:09 | 复验 PASS（20/0/13 SKIP，报告新鲜）；**同刻评论确认：下游「人工卡点」「Packager·打包」两节点 status=failed，mention 交接无法使其自动回 running，需 owner retry** |
| 20:09+ | owner run 87d6295a（19:48 启动）仍在 running，daemon 反复 `coalesced … (already running)` |

### 3.2 agent-browser（R1）状态漏水实锤

- goal 终态 = done；但 `sub_goal` 表 **「Packager·打包」status=running**，`verification_result` 0 条。
- Packager run ce18cfa（15:01–15:11）确已出包：制品目录时间戳 `agent-browser_1.0.2_202611260709`（15:09–15:10）与该 run 时段吻合，包内 metadata/template/runtime 三件齐全。**产物实际交付，节点簿记漏水**（勘误：前序口头分析称"包在流水线外落地"，据时间戳重判为流水线内 Packager run 所出）。
- 另有一个冗余 Packager run edf439c6 于 15:17 入队，一直未执行，至 19:26 才被 stopped——goal 早已标 done。
- Verifier 复验报告（评论原文）确认升级达成：0.35.1→0.45.0，34 用例 PASS=13/FAIL=0/SKIP=18/REDLINE=3，二进制真调用 `--version` = 0.45.0。

---

## 4. RSI 三轮分析数据（rsi_round / rsi_report / rsi_finding）

### 4.1 轮次清单

| 轮 | goal | round id | session id | 分析时刻（本地） | findings | critical | top_category |
|---|---|---|---|---|---|---|---|
| 1 | agent-browser | `18328b828579f9ad8a1229c87254c432` | `0d356b25…` | 15:14:19 | 3 | 0 | wrong_assignee |
| 2 | drawio | `6da141522f56192bd6d2f97a5d1b69df` | `c72a3207…` | 15:24:19 | **1** | 0 | wrong_assignee |
| 3 | openviking | `749d3b93145bb41253b0f971212e7706` | `ed517580…` | 15:34:19 | 5 | 0 | wrong_assignee |

### 4.2 三轮结构指标对比（metrics_json 原值换算）

| 指标 | R1 agent-browser | R2 drawio | R3 openviking | 趋势 |
|---|---|---|---|---|
| 图规模 | 15 节点/26 边 | 13 节点/29 边 | 12 节点/20 边 | — |
| 总工作量 / 墙钟 | 71.6m / 51.0m | 118.1m / **225.0m** | 57.4m / 62.1m | R2 墙钟=工作量 1.9 倍 |
| 关键路径（占墙钟） | 40.9m（80%） | 104.6m（46%，推断值） | 57.0m（92%） | — |
| 并行收益 parallel_speedup | **1.40×** | **0.52×** | 0.92× | 整体下行 |
| 通道不均衡 lane_imbalance | 2.84 | 2.20 | **3.75** | 恶化 |
| 最忙通道 busy 占比（Upgrader） | **57%**（1393437ms） | **44%** | **75%** | 恶化 |
| 空闲通道 busy | Packager=0，AI SHELL=0 | （见 R2 delta） | Packager=0，AI SHELL=0 | 三轮均有 0 负载通道 |
| 重试放大 retry_amplification | 1 | 1 | **3** | 恶化 |
| consult_count | 3 | 0 | 2 | — |
| 返工率 / 验证开销 | 0 / 0 | 0 / 0 | 0 / 0 | — |

R1 lanes 原值：`{Verifier: 577693, Packager: 0, Upgrader: 1393437, Checker: 484052, AI SHELL: 0}`（ms）。

### 4.3 R2 delta_json 原值（对照 R1，关键字段）

| 字段 | from（R1） | to（R2） |
|---|---|---|
| dispatch_wait_ms | 63,118 | **13,498,787（≈3.75h ≈ 墙钟全长）** |
| human_wait_ms | 208,000 | **5,170,000（86m，与 gate review_duration=5160s 吻合）** |
| serialization_loss_ms | 605,415 | 7,221,940 |
| dependency_wait_ms | 6,367,288 | 14,824,911 |
| parallel_speedup | 1.4036 | 0.5248 |
| consult_count | 3 | 0 |

R3 delta（对照 R2）：dispatch_wait 705ms（-99.99%）、human_wait 507,000ms、parallel_speedup 回升至 0.9242、retry_amplification **1→3**。

### 4.4 Findings 全量清单（9 条，全部 severity=warn、status=confirmed）

| 轮 | finding id | category | score | 标题摘要 |
|---|---|---|---|---|
| R1 | `b0182f71427bf4265bfcf9261ce282f9` | wrong_assignee | 64.4 | 负载不均：Upgrader 占用 57% 执行时长（5 通道，最忙 23.2m，其余中位 8.1m） |
| R1 | `7e2632623a56d510bc4e38f7db3d14f0` | serial_chain | 63.4 | 关键路径为 5 个执行节点串行链（链长 40.9m 占墙钟 80%） |
| R1 | `6dfc1ffda77b7f085daabbc6042d1cb5` | plan_deviation | 60.0 | 计划与执行不一致：计划 9 节点/已落地 7/咨询 3 次 |
| R2 | `c0826626b8156f791a477fe319b5b75f` | wrong_assignee | 68.0 | 负载不均：Upgrader 占用 44% 执行时长 |
| R3 | `60c6eb1bd3dd781a41ec0702bdcbbd95` | wrong_assignee | **70.0（全场最高）** | 负载不均：Upgrader 占用 **75%** 执行时长（其余中位 6.7m） |
| R3 | `05dae552f6b8f5a7605662227282d6c3` | serial_chain | 65.3 | 关键路径为 4 个执行节点串行链（占墙钟 92%） |
| R3 | `edf37eebc6b853792d1fa3115727f3a9` | split_candidate | 62.5 | 1 个重型节点（Upgrader·升级+修复）占执行时长 ≥50%，可拆分并行 |
| R3 | `177d608f8d0cd50afc7b9ac8ee8c08e9` | retry_amplification | 60.0 | 重试放大：单个子任务最多尝试 3 次（6 个子任务共 8 次执行） |
| R3 | `e492b0a89e9e0f420b3cdc13db9f0b97` | plan_deviation | 60.0 | 计划 9 节点/已落地 7/咨询 2 次 |

三轮报告 narrative 均注明：**"尚无足够历史（<5 个就绪会话）计算基线，本报告只做单次观察。"**

---

## 5. 提案与版本落地链（blueprint_proposal + daemon 日志）

### 5.1 提案清单

| 提案 id | 标题 | 来源 finding | base_version | ops | 状态 | 审批（本地） | 落地版本 |
|---|---|---|---|---|---|---|---|
| `c685fb3a9a9a42602529b40c1bd7cbca` | 把最忙通道的任务改派给空闲通道（t_upgrade、t_repair） | R1 wrong_assignee（b0182f71，64.4） | **v1** | 2（update_node assignee×2） | approved | 19:29:25（human） | **v2（draft，从未发布）** |
| `78a79c9f0640e95c371673ae78fdc610` | 在执行入口固化计划节点纪律（t_check） | R1 plan_deviation（6dfc1ffd，60.0） | **v1** | 1（update_node brief） | approved | 19:29:31（human） | **v3（published）** |

两提案 hypothesis 均注明"由 finding 确定性翻译生成（无 LLM）"，expected_metric=wall_ms、expected_direction=down。

**finding→提案→落地→验证 转化率：9 → 2 → 1 → 0。** 详见 §6.3 映射表。

### 5.2 daemon 日志关键行（原文摘录）

提案生成（**全天仅此两行，均在 R1 分析时刻 15:14:19**；15:24 R2、15:34 R3 时刻无任何 rsi 提案行）：

```
2026-11-26T15:14:19+08:00 [INFO] blueprint: proposal "自动提案：把最忙通道的任务改派给空闲通道（t_upgrade、t_repair）" created against 16ebee24 (2 ops)
2026-11-26T15:14:19+08:00 [INFO] [rsi] auto-proposal drafted for goal d5f0e5baffb036b3a1bf3e9609ae45a0: …(wrong_assignee)
2026-11-26T15:14:19+08:00 [INFO] blueprint: proposal "自动提案：在执行入口固化计划节点纪律（t_check）" created against 16ebee24 (1 ops)
2026-11-26T15:14:19+08:00 [INFO] [rsi] auto-proposal drafted for goal d5f0e5baffb036b3a1bf3e9609ae45a0: …(plan_deviation)
```

审批落地（fork 覆盖发生瞬间）：

```
2026-11-26T19:29:25+08:00 [INFO] blueprint: proposal "…改派…" approved → draft v2 (fef7788c)
2026-11-26T19:29:31+08:00 [INFO] blueprint: proposal "…计划节点纪律…" approved → draft v3 (a5443d0b)
```

**11-26 全天没有任何 `rsi: next round for proposal → goal … (pin …)` 日志行**；goal 表亦无任何 goal pin v2/v3。

### 5.3 健康闭环对照（11-23，另一蓝本「沙箱资产审计流水线」`4725c5dd…`）

```
2026-11-23T10:21:25 [INFO] blueprint: proposal "自动提案：并行化串行链（…10 节点）" created against 1ecaaf32 (18 ops)
2026-11-23T10:31:34 [INFO] blueprint: proposal "…" approved → draft v3 (7d83f4cd)
2026-11-23T10:31:46 [INFO] rsi: next round for proposal "…" → goal 9605d89dce0075a9fd4c8bc890bed0ca (pin 7d83f4cd, active)
```

该对照证明：① serial_chain 类 finding 可被翻译为提案（同日 R1/R3 的 serial_chain finding 未被翻译不是"不可译"）；② 审批后平台本应自动 spawn 下一轮 pinned 新版本的 goal 用于验证——11-26 这条链没有发生。

---

## 6. 问题详情、证据与改进诉求

### 6.1 【判定】v3 相对 v1 是否有效改动 → **不能证明有效，机制上大概率无效**

四条依据（数据见 §2.3、§4、§5）：

1. **0 执行**：v3 发布（19:29:31）至今无任何 goal pin 它（goal 表 + daemon 日志双重证实），`expected_metric=wall_ms down` 无从检验。
2. **机制错位**：plan_deviation 的证据是"leader 用 consult 派发替代节点执行"（run 统计：consult 派发 Packager×6、Checker×2，owner AI SHELL×27），但 patch 把纪律文本写进 `t_check` 的 brief——那是发给 Checker 执行检查节点时读的，约束不了做咨询派发决策的 leader；9 节点中也只有这 1 个节点加了文本。
3. **根因错配**：R3 的 plan_deviation（7/9 落地）发生在 token 配额故障期——10 次同错误连环失败导致的节点未落地，往 brief 写"要按计划走"不解决资源中断。
4. **优先级倒挂**：v3 修复的 plan_deviation 在 R1 三个 finding 中得分最低（60.0）；逐轮恶化、得分一路走高（64.4→68.0→70.0）的 wrong_assignee，其解药在被 fork 覆盖丢失的 v2 里。

### 6.2 【P1·产品】版本对比视图应高亮字段级差异

**现象**：用户对比 v1/v2/v3 "感觉没区别"。三版本拓扑零差异（§2.3），全部差异在 assignee（2 行）与 brief（1 处追加）字段。
**诉求**：版本对比视图（diff 视图）应做**节点属性级 diff**——高亮 assignee 变更、brief 文本增删（类似代码 diff 的行级标注），而非仅渲染拓扑图对比。蓝本内容本身就是单一 JSON（`blueprint_version.content`），字段级 diff 在数据上完全可行。
**附带建议**：版本列表应展示 changelog 摘要与 diff 统计（当前 v3 changelog 为空，v2 只有一句截断的负载描述）。

### 6.3 【P2·机制】同基线第二条提案 approve 应冲突提示或自动 rebase

**现象**：两条提案 base_version 均为 v1、间隔 6 秒先后 approve。第一条落地为 draft v2 后，第二条**仍基于 v1** 生成 v3 并发布，v2 的改派改动被静默丢弃（§2.3 diff 证实 v3 不含 assignee 变更）。
**后果量化**：被丢弃的改派提案针对的 wrong_assignee，其目标指标（最忙通道 busy 占比）此后从 57%→75% 恶化到全场最高分 70.0（§4.2、§4.4）；而 19:43–19:54 token 故障期 AI SHELL/Packager 通道 busy=0 全程围观（§3.1）——正是改派本可缓解的场景。
**诉求**：approve 第二条提案时检测 `base_version_id` 是否落后于该蓝本最新版本（含 draft）：**要么提示冲突让人工合并，要么自动 rebase 到最新版本后再生成新 draft**。至少在 changelog/审批卡片上标注"本提案不含 v2 的改动，v2 将被覆盖"。

**finding → 提案 → 落地 → 验证 全量映射**（转化率 2/9 → 1/9 → 0/9）：

| finding（score） | 提案 | 落地 | 验证 |
|---|---|---|---|
| R1 wrong_assignee（64.4） | ✅ 改派提案 | v2 draft，**被 fork 覆盖丢失** | — |
| R1 plan_deviation（60.0） | ✅ 计划纪律提案 | v3 published | **0 执行，未验证** |
| R1 serial_chain（63.4） | ❌ 未翻译 | — | — |
| R2 wrong_assignee（68.0） | ❌ 未翻译 | — | — |
| R3 wrong_assignee（70.0 最高分） | ❌ 未翻译 | — | — |
| R3 serial_chain（65.3） | ❌ 未翻译 | — | — |
| R3 split_candidate（62.5，三轮唯一真并行杠杆） | ❌ 未翻译 | — | — |
| R3 retry_amplification（60.0） | ❌ 未翻译 | — | — |
| R3 plan_deviation（60.0） | ❌ 未翻译 | — | — |

### 6.4 【P3·机制】提案生成器只对 round 1 生成提案

**证据**：daemon 日志 11-26 全天 `[rsi] auto-proposal drafted` 仅出现于 15:14:19（R1 分析时刻，且两条提案的 goal 均为 `d5f0e5baf…`＝R1 的 agent-browser）；R2（15:24:19）与 R3（15:34:19）分析时刻无任何提案生成行（§5.2）。R2/R3 共 6 条 findings——含全场最高分 70.0 的 wrong_assignee、全新类别 split_candidate / retry_amplification——全部未进入翻译流程。
**对照**：11-23 生成器曾把 serial_chain 翻译成 18 ops 提案（§5.3），证明能力存在，是**本轮触发条件未覆盖**。
**待查**（代码层）：生成器是否只处理 domain 首轮 terminal、或按 category 去重导致后续轮次命中同类 finding 时跳过。代码位于 `/root/runtime/ouroflow/`。

### 6.5 【P4·机制】审批后未 spawn 下一轮验证 goal

**证据**：11-26 无 `rsi: next round for proposal → goal (pin …)` 日志行；goal 表无 pin v2/v3 的 goal（§5.2）。11-23 健康对照证明该环节本应自动发生（§5.3）。
**后果**：学习闭环最后一跳断裂——任何已发布改进版都没有机会用同场景 goal 自证，RSI 训练域永远停在"单次观察"（§4.4 narrative 原文）。
**待查**：spawn 条件是否被当时 active 的 openviking goal 阻塞（15:24 日志显示其 parked），或与 P3 同源。

### 6.6 【P5·分析】RSI 发现盲区与样本污染

- **盲区 1**：R2 `dispatch_wait=13,498,787ms`（≈墙钟全长）与 `human_wait=5,170,000ms`（86m，与 gate review_duration=5160s 吻合）是三轮中最大的两块浪费，**无任何 finding 类别覆盖**（现有 5 类：wrong_assignee/serial_chain/plan_deviation/split_candidate/retry_amplification）。
- **盲区 2（阈值方向性问题，推断待源码确认）**：serial_chain 在 R1（链占墙钟 80%）、R3（92%）触发，R2 链占墙钟约 46%（104.6m/225.0m）未触发——**等待越严重、发现越少**，而 R2 恰是浪费最大的一轮。建议规则同时看链绝对时长/墙钟比。
- **样本污染**：R3 分析时刻 15:34 处于 token 配额故障期（15:08–15:20 连环失败、16:23 Packager BLOCKED），冻结图中 retry_amplification=3、"6 子任务 8 次执行"均为故障所致；其建议（"把前置检查加进 brief"）治不了资源耗尽。且三 goal 并发共享执行通道（14:18–15:13 重叠），lane busy 数据互相污染，并非干净的 v1 三重复制。
- **建议**：RSI 采样剔除或标注非健康执行段（failed run 密集期）；报告 narrative 已注明"单次观察"，但提案翻译环节并未据此降权。

### 6.7 【P6·状态机】goal 终态与节点状态漏水

- **agent-browser**：goal=done，但「Packager·打包」sub_goal=running、verification_result=0 条；冗余 Packager run 从 15:17 挂到 19:26 才 stopped（§3.2）。产物已实际交付（15:09 制品时间戳吻合 Packager run ce18cfa），**属簿记漏水而非漏打包**。
- **openviking**：gate_decision 19:34:02 已 approve，但「人工卡点·打包发布确认」(9d3236b9) 与「Packager·打包」(3f1df997) sub_goal 至 20:09 仍 status=failed；Verifier 复验 PASS 后的交接评论原文确认："mention 交接未能让 failed 节点自动回到 running……需 owner 对这两个下游节点执行 retry/重新派发"——**approve/上游 PASS 均无法自愈 failed 节点**。
- **建议**：① goal 置 done 前硬校验全部 sub_goal 终态；② gate approve、上游节点 verified 时自动将下游 failed 节点复位为 running 或触发 owner 重派。

### 6.8 【P7·容错】不可重试错误被机械重试

- 10 次 failed run 逐字同一错误（token 额度每日刷新）；同一节点重试打满 attempt=3，两批空转：15:08–15:20（×3）与 19:43–19:54（×6，**且发生在人工已切换模型的 reopen 之后**，说明模型切换对重试批次未即时生效）。
- retry_amplification finding 的建议方向（brief 前置检查）对此无效。
- **建议**：按错误文案/类型分类，资源配额类（429/额度类）直接 fail-fast 上报人工，不进入重试循环；此类时段产生的指标不计入 RSI 样本。

---

## 7. 关键原文摘录（供独立复核）

### 7.1 改派提案 hypothesis（blueprint_proposal.c685fb3a）

> 执行通道负载失衡（最忙 8437c7e866da8feec6f8de8a1292addb 累计 1393437ms，最闲 e954c148283f28004424f63cb5ed2aa8 仅 0ms）——把 t_upgrade、t_repair 改派到空闲成员，可缩短排空时间。本提案由 wrong_assignee finding 确定性翻译生成（无 LLM），请审查后批准。

### 7.2 计划纪律提案 hypothesis（blueprint_proposal.78a79c9f）

> 该 goal 的 plan_deviation 规则命中了 1 个节点；把对应的检查要求固化进节点简报，可减少同类返工/等待。本提案由 plan_deviation finding 确定性翻译生成（无 LLM），请审查后批准。

### 7.3 openviking reopen handoff_note（goal.aa4541a9）

> Reopened: 流水线仅剩打包一步：升级 0.5.3→0.5.6 已完成且 Verifier 第 1 轮 PASS，此前因执行模型当日免费额度耗尽导致回退修复/打包连环失败、工作流卡死。现已将流水线智能体模型切换为 OpenGW/deepseek-v4.1-flash-1017（实测可用），请从打包节点恢复流水线，无需重跑已 PASS 的升级与验证

### 7.4 openviking Packager BLOCKED 上报（16:23:44 评论，节选）

> 状态: BLOCKED（未出包；未执行 pack / verify / scan）……本 Packager 运行在受限命名空间：看不到 /root/job-envs 与 /root/template/openviking……落地传输脚本时被客户端硬拒：write_file 返回「Edit approval denied by ACP client」；改用 printf 生成 /tmp/xfer.py 返回「BLOCKED: User denied this command — stop, do not retry, do not rephrase」。按规则立即停机等待人工，未做任何绕过尝试。

### 7.5 openviking Verifier 复验交接（20:09 评论，节选）

> 【状态提示·流程卡点】Verifier 复验已完成并 PASS，但下游节点未自动恢复——本轮复验结果：openviking，PASS=20 / FAIL=0 / SKIP=13 / REDLINE_EXCLUDED=3，failed=0，环境 running，报告新鲜（20:03:50 > 最后修复 20:02:29）。观察到的节点状态：3f1df997 Packager·打包 = failed、9d3236b9 人工卡点·打包发布确认 = failed；mention 交接未能让 failed 节点自动回到 running。结论：流程当前停在打包节点，需 owner（AI SHELL）对这两个下游节点执行 retry/重新派发。

### 7.6 R1 Verifier 复验报告（agent-browser，节选）

> 验证轮次: 复验第 2 轮（最后一轮），repair_round=1（第 1 轮回退修复为 NO-OP，无代码改动）……34 条用例 → PASS=13 / FAIL=0 / SKIP=18 / REDLINE=3……exec 真调用 agent-browser --version = 0.45.0 ✓……已知遗留：env/template 的 upstream[].version 声明仍为 0.35.1（运行时实际 0.45.0），需按目标要求提 PR 更新声明。

---

## 8. 修复建议优先级

| 优先级 | 事项 | 对应问题 |
|---|---|---|
| ① | 查提案生成器为何只处理 round 1（损失 2/3 学习材料，最致命） | P3 |
| ② | 查 next-round goal spawn 未触发的原因（否则 v3 永远无验证机会） | P4 |
| ③ | 同基线提案 approve 强制 rebase/冲突提示 | P2 |
| ④ | 版本对比视图字段级 diff 高亮（assignee/brief） | P1 |
| ⑤ | 补 dispatch_wait/human_wait finding 规则；修 serial_chain 墙钟占比阈值方向 | P5 |
| ⑥ | goal done 硬校验 + failed 节点自愈恢复 | P6 |
| ⑦ | 不可重试错误分类 fail-fast；RSI 剔除故障期样本 | P7 |
| 存量修复 | 将 v2 的改派 assignee 改动合并进当前 published 版本（v3），使已批准优化生效 | P2 |

---

## 9. 勘误（前序口头分析中的错误，本文已修正）

1. **"Verifier 复验 run 空挂 8 小时才被 claim"——错误**。时区换算失误：run 10a5c622 的 `queued_at/started_at = 12:03:12Z` 即本地 20:03:12，daemon claim 于 20:03:20，间隔 **8 秒**。owner run 87d6295a 为 19:48（本地）启动，截至 20:09 仍在 running（≥21 分钟），属实但不构成 8 小时。
2. **"agent-browser 的包是流水线外落地的"——归因存疑，已修正**。制品时间戳（15:09–15:10）与流水线内 Packager run ce18cfa（15:01–15:11）吻合，重判为流水线内产出；真实问题降级为"节点簿记漏水"（sub_goal 卡 running、0 验证、goal 照标 done），见 §6.7。
3. 三轮指标对比中 R2 的"链占墙钟 46%"为推断值（由 104.6m/225.0m 计算），serial_chain 规则阈值本身需查规则源码确认，见 §6.6。

---

## 10. 复验命令附录

```bash
DB=/root/job-envs/sandboxes/ouroflow-1795367359/.flowloop/flowloop.db
LOG=/root/job-envs/sandboxes/ouroflow-1795367359/.flowloop/daemon.log

# 1. 版本与差异
sqlite3 $DB "SELECT id,version,status,content_hash,changelog,created_at FROM blueprint_version WHERE blueprint_id='051dbbd8f71ef729a1050ae9aa78a099' ORDER BY version;"
# content diff：导出三版本 content 后 python3 -m json.tool 再 diff（见 §2.3）

# 2. 提案与 finding 挂钩
sqlite3 $DB "SELECT id,title,finding_id,base_version_id,applied_version_id,status,decided_at FROM blueprint_proposal WHERE blueprint_id='051dbbd8f71ef729a1050ae9aa78a099';"

# 3. 三个 goal 及其 pin 版本
sqlite3 $DB "SELECT id,title,status,blueprint_version_id,created_at FROM goal WHERE blueprint_version_id='16ebee24dd5c3e64f1328d5225c038c5';"

# 4. 失败 run 归因
sqlite3 $DB "SELECT status,cancel_reason,substr(result_summary,1,60),count(*) FROM run WHERE goal_id IN ('fca8572970a08571f7315508ddc36cff','aa4541a9915cfee26fe0430bb1f0555d','d5f0e5baffb036b3a1bf3e9609ae45a0') AND status IN ('failed','cancelled') GROUP BY 1,2,3;"

# 5. RSI 轮次与 findings
sqlite3 $DB "SELECT round_no,goal_id,finding_count,top_title,metrics_json,delta_json FROM rsi_round WHERE goal_id IN ('fca8572970a08571f7315508ddc36cff','aa4541a9915cfee26fe0430bb1f0555d','d5f0e5baffb036b3a1bf3e9609ae45a0') ORDER BY round_no;"
sqlite3 $DB "SELECT f.id,f.category,f.score,f.status,f.title FROM rsi_finding f WHERE f.report_id IN (SELECT id FROM rsi_report WHERE session_id IN ('0d356b25bcd642eb6b7a773f87c0ba00','c72a32071c13a57ff57ef006d8ce5de1','ed5175805adc3f49dbb4fb3f46c1c692')) ORDER BY f.score DESC;"

# 6. 状态漏水
sqlite3 $DB "SELECT g.title,g.status,sg.title,sg.status FROM goal g JOIN sub_goal sg ON sg.goal_id=g.id WHERE g.id IN ('d5f0e5baffb036b3a1bf3e9609ae45a0','aa4541a9915cfee26fe0430bb1f0555d');"
sqlite3 $DB "SELECT goal_id,decision,decided_by,decided_at,review_duration FROM gate_decision WHERE goal_id IN ('fca8572970a08571f7315508ddc36cff','aa4541a9915cfee26fe0430bb1f0555d','d5f0e5baffb036b3a1bf3e9609ae45a0');"

# 7. 提案生成/审批/spawn 日志（注意日志为 +08:00）
grep -a "\[rsi\]" $LOG | grep "2026-11-26"
grep -a "blueprint: proposal" $LOG | grep "2026-11-26"
grep -a "next round for proposal" $LOG
```
