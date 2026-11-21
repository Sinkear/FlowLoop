# 「自动升级流水线 v2」蓝本四任务执行记录与问题诊断

> 生成时间：2026-11-21
> 数据来源：agentwork-rpa 沙箱数据库 `/root/job-envs/sandboxes/agentwork-rpa-1795138507/.agentwork/agentwork.db`（goal / run / comment / gate_decision / activity_log / training_session / rsi_* / blueprint_* 表）、daemon 日志（`daemon.log`，时区 +08:00）、daemon HTTP API（`127.0.0.1:17091`）。
> 说明：数据库时间戳为 UTC，本文除特别标注外统一换算为本地时间（UTC+8）。

---

## 一、蓝本概况

| 项 | 值 |
|---|---|
| 蓝本名称 | 自动升级流水线 v2 |
| 蓝本 ID | `0fef5c50e1a0f058249870761e324785` |
| 版本 | v1（`9ae4d32f95ca4384bd54f494f3ed027c`），状态 published，2026-11-20 12:05:44 由 **human** 创建 |
| 蓝本描述 | Checker查新分级 → 人工卡点(major/高风险) → Upgrader升级 → Verifier独立验证(FAIL回退≤2轮) → Packager打包 → AI SHELL 唯一总结；账本驱动、每 env 泳道、保护名单、窗口约束由 agent prompt 承载（蓝本v2.1） |

节点结构（spec graph）：

| 节点 | 类型 | 承接者 | 职责 |
|---|---|---|---|
| t1 Checker·查新分级 | task | Checker (`f75f223f…`) | 扫描作业环境 release 源，产出 plans/batch-plan.json + 账本 upgrade-ledger.jsonl；分级：minor/低风险→auto，major_bump/高风险/批量>5→needs_approval；保护名单（agentwork-rpa/job-env-manager/openviking/devbridge）→protected_skip；窗口外只出计划不动手 |
| g1 分级卡点·major/高风险 | gate | human:owner | needs_approval 泳道逐条确认；拒绝=本批仅保留报告；超时 24h 自动 rejected |
| t2 Upgrader·升级+验收 | task | Upgrader (`cbc2615f…`) | 按账本逐 env 泳道执行升级（snapshot→stop→install→clean→configure→deploy→accept），特权操作一律向 AI SHELL 发结构化工单；修复循环≤2轮 |
| t3 Verifier·独立验证 | task | Verifier (`c6f15196…`) | 逐 env 跑 test_job_env.py（16条 base+差异件），报告落 reports/ 并追加账本；PASS→dispatch Packager；FAIL→回退 Upgrader（≤2轮）；禁止 review-only 替代脚本执行 |
| t4 Packager·打包 | task | Packager (`2b1998cf…`) | 出制品包+manifest，等待人工上传发布 |
| end_ok | end | — | 完成 |

四个任务全部为 `execution_mode=train`（训练任务）、`assignee_type=squad`、域=「自动升级」（`73eaf8c1…`，scratch 类型）、`blueprint_version_id=9ae4d32f…`、预算 budget_usd=2、采样率 1.0、内容捕获 redacted。

---

## 二、四任务执行轨迹（详细时间线）

### 2.1 deepseek harness 升级（goal `aada5a0c…`）

- **创建**：11-20 15:27:23（人工，hitl 类）
- **终态**：done（11-21 09:06:53 人工批准后当秒交付）

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
|---|---|---|---|---|
| 15:27:24–15:28:09 | owner 首跑 | AI SHELL | a4d8f26d | 46s |
| 15:28:07–15:33:31 | Checker 查新分级 | Checker | d0ec01c6 | 5m24s |
| 15:33:31–15:33:43 | owner 派发 | AI SHELL | b0015503 | 12s |
| 15:33:44–15:50:05 | 首轮 Verifier 评审 | Verifier | 86c6caaa | 16m21s |
| 15:50:05 | 进入评审（review_ready） | — | — | — |
| **15:56:53** | **人工 REJECT（第 1 次卡点，评审停留 407s）** | human | — | — |
| 15:56:53–15:57:14 | owner 唤醒续跑 | AI SHELL | f3ee9122 | 21s |
| 15:57:09–17:38:17 | Upgrader 升级 | Upgrader | 01f11c51 | **101m09s** |
| 17:38:18–17:38:40 | owner | AI SHELL | ab305978 | 23s |
| 17:38:37–17:58:02 | Verifier 独立验证 | Verifier | 7066b2f6 | 19m25s |
| 17:58:02–17:58:36 | owner | AI SHELL | 8620b9a9 | 35s |
| 17:58:32–18:34:03 | Packager 打包 | Packager | 53050d99 | 35m31s |
| 18:34:03–18:34:54 | owner | AI SHELL | 343fa2c8 | 52s |
| 18:34:55–18:38:16 | 末轮 Verifier 评审 | Verifier | f2653cab | 3m21s |
| 18:38:16 | 进入评审（review_ready） | — | — | — |
| **11-21 09:06:53** | **人工 APPROVE（评审停留 52,117s ≈ 14.5h，过夜）→ 当秒交付 done** | human | — | — |

- **升级结果**（AI SHELL 最终汇报，11-20 18:34）：上游 dsh 0.2.1-rc.2 → **0.2.2-rc.5**（github+npm 双源一致）；平台轴 1.0.21 → 1.0.22 → 打包盖章 **1.0.23**；dsh-tui launcher 0.16.1 → 0.20.2。Verifier 末轮结论：**PASS — 升级目标已达成**（完整拉取 12 条 comment feed + reports/ 证据复核）。
- **RSI 训练分析**（round 2）：7 节点/4 边；机器墙钟 185.9m，工作总量 161.5m，关键路径 101.1m；并行收益 0.87×；人工等待（评审闸门）52,524s ≈ 875.4m，**占总时长 82%**；发现 1 条（human_wait，confirmed）。

### 2.2 minimax-code 升级（goal `2da62c29…`）

- **创建**：11-20 15:36:08（人工，hitl）
- **终态**：done（11-20 18:10:26 人工批准后当秒交付）

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
|---|---|---|---|---|
| 15:36:08–15:36:48 | owner 首跑 | AI SHELL | 8067e4c1 | 40s |
| 15:36:40–15:38:09 | Checker | Checker | 0a25c03a | 1m29s |
| 15:38:10–15:38:27 | owner | AI SHELL | 5f468b3f | 17s |
| 15:38:25–15:49:39 | Upgrader（第 1 次） | Upgrader | 8eb10227 | 11m14s |
| 15:49:39–15:50:01 | owner | AI SHELL | b16e2b31 | 21s |
| 15:50:01–15:53:32 | 首轮 Verifier 评审 | Verifier | ac39eba8 | 3m31s |
| 15:53:32 | review_ready | — | — | — |
| **16:02:39** | **人工 REJECT（第 1 次卡点，评审停留 547s）** | human | — | — |
| 16:02:39–16:03:19 | owner 唤醒 | AI SHELL | 07f2d777 | 40s |
| 16:03:14–16:16:39 | Checker（第 2 次，重定级） | Checker | 64aaedc4 | 13m25s |
| 16:16:39–16:17:21 | owner | AI SHELL | fe97e51f | 42s |
| 16:17:19–16:49:25 | Upgrader（第 2 次） | Upgrader | cf21055f | **32m06s** |
| 16:49:25–16:53:44 | owner | AI SHELL | bec79436 | 4m19s |
| 16:52:01–16:59:59 | Verifier 独立验证 | Verifier | 9bef9045 | 7m58s |
| 16:59:59–17:25:38 | owner（总结） | AI SHELL | d2ec3c46 | 25m39s |
| 17:25:39–17:30:11 | 末轮 Verifier 评审 | Verifier | ca72249d | 4m32s |
| 17:30:11 | review_ready | — | — | — |
| **18:10:26** | **人工 APPROVE（评审停留 2,414s ≈ 40m）→ 当秒交付 done** | human | — | — |

- **升级结果**（AI SHELL 总结，17:24:40）：上游 @minimax-ai/code 0.4.12 → **0.7.9**（registry.npmjs.org dist-tags.latest 实测；非用户原以为的 0.7.7，之后已有 0.7.8/0.7.9）。Verifier 结论：**升级执行正确且已动态验证，但交付不完整——制品未发布，升级不可持久化**（该意见即第 1 次 REJECT 的直接原因；第二次流程把打包环节补齐后通过）。
- **RSI 训练分析**（round 1，本蓝本首轮基线）：8 节点/5 边；机器墙钟 83.3m，工作总量 66.2m，关键路径 32.1m；并行收益仅 **0.79×**（理论 2.1×）；人工等待 2,961s ≈ 49.4m，**占 37%**；发现 2 条（human_wait 37% + lost_parallelism「理论并行收益 2.1×，实际 0.8×」，均 confirmed）。

### 2.3 openclaw 升级（goal `b86244c3…`）

- **创建**：11-20 18:16:15（人工，hitl）
- **终态**：done（11-21 09:17:55 人工批准后当秒交付）

| 时间 | 阶段 | 执行者 | run | 时长 |
|---|---|---|---|---|
| 11-20 18:16:15–18:16:45 | owner 首跑 | AI SHELL | 0df2f67e | 30s |
| 18:16:42–18:26:56 | Checker | Checker | aaa59a64 | 10m14s |
| 18:26:56–18:27:28 | owner | AI SHELL | 24c2eea4 | 32s |
| 18:27:21–19:38:42 | Upgrader 升级 | Upgrader | af8480e6 | **71m21s** |
| 19:38:42–19:39:24 | owner | AI SHELL | 3e446f8f | 42s |
| 19:39:21–19:52:33 | Verifier 独立验证 | Verifier | 778ab1be | 13m12s |
| 19:52:33–19:53:10 | owner | AI SHELL | d1e6c3d6 | 37s |
| 19:53:07–20:34:37 | Packager 打包 | Packager | 93faf447 | 41m30s |
| 20:34:37–20:35:39 | owner（末段收口） | AI SHELL | 309b13a0 | 1m02s |
| 20:35:39 | **中途卡点（finalization guard：ask-human=1）——打包完成后暂停，等待人工处理「产物上传发布」** | — | — | — |
| *（过夜等待，约 12.6 小时）* | — | — | — | — |
| 11-21 ~09:08 | 人工恢复（唤醒 owner） | AI SHELL | ba2246b1 | 09:08:25–09:08:42（17s） |
| 09:08:42 | 进入评审（parked review） | — | — | — |
| 09:08:42–09:11:52 | Verifier 评审 | Verifier | d8416f47 | 3m10s |
| 09:11:52 | review_ready | — | — | — |
| **09:17:55** | **人工 APPROVE（评审停留 362s ≈ 6m）→ 当秒交付 done** | human | — | — |

- **升级结果**（AI SHELL 总结，20:35:37）：上游 2026.9.2 → **2026.11.6**（npm 最新版），环境 running；独立验证 **17 PASS / 0 FAIL**（34 条 base 用例，zz.013 为升级前既存缺口）；产物已出包自检通过。Verifier 复核：**通过（PASS）**。
- **RSI 训练分析**（round 3）：6 节点/4 边；机器墙钟 137.9m，工作总量 136.3m，关键路径 71.3m；并行收益 **0.99×**（最高）；人工等待仅 362s（**4.2%**）；发现 0 条——四个任务中执行最顺的一个。
- **注**：任务从创建到 done 总历时 15h02m，其中 12.6h 是中途「ask-human 卡点」的过夜等待；但 RSI 指标的 human_wait 只统计评审闸门（362s），**中途授权暂停不计入 human_wait**（见 4.4 节观察 O-3）。
- **异常观察**：唤醒 run ba2246b1 的事件流出现大量 `event seq gap … lost on the wire`（seq 乱序/丢包告警，09:08:30–09:08:37 连续十余条），疑似 machine→daemon 事件上报并发下的序号乱序，未影响任务结果，但值得留意。

### 2.4 opencode 升级（goal `ceb804a0…`）——触发 BUG 的那个

- **创建**：11-20 18:17:17（人工，hitl）
- **终态**：done（本应 11-21 09:06:34 批准即完成；因 BUG-1 卡在 review，最终 09:56:22 经 daemon 重启自愈修复）

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
|---|---|---|---|---|
| 18:17:17–18:17:46 | owner 首跑 | AI SHELL | 4b0f1d57 | 29s |
| 18:17:42–18:32:43 | Checker | Checker | 413d5c47 | 15m01s |
| 18:32:43–18:32:56 | owner | AI SHELL | e41d9125 | 13s |
| 18:32:53–18:57:31 | Upgrader 升级 | Upgrader | 61c4a31f | 24m38s |
| 18:57:31–18:57:55 | owner | AI SHELL | 2c9ae753 | 24s |
| 18:57:52–19:05:03 | Verifier 独立验证 | Verifier | 6b17f208 | 7m11s |
| 19:05:03–19:05:15 | owner | AI SHELL | b12f7cb2 | 12s |
| 19:05:12–19:36:39 | Packager 打包 | Packager | e9c6bd71 | 31m27s |
| 19:36:39–19:37:20 | owner（收口） | AI SHELL | e91a4414 | 41s |
| 19:37:20 | parked review（gate hit） | — | — | — |
| 19:37:20–19:41:50 | Verifier 评审 | Verifier | edb5e39f | 4m30s（结论：goal 结果审核通过） |
| 19:41:50 | review_ready | — | — | — |
| **11-21 09:06:34** | **人工 APPROVE（评审停留 48,283s ≈ 13.4h，过夜）——批准落库成功，但交付收口遇 SQLITE_BUSY 失败（BUG-1）** | human | — | — |
| 09:07:01 / 09:09:09 / 09:17:59 | 用户再次点击批准，均被防重复保护拒绝（BUG-2 表现） | human | — | — |
| **09:56:20–09:56:22** | **运维介入：杀 daemon → 看门狗重启 → `recoverPendingDelivers` 自愈补交付 → done** | — | — | — |

- **升级结果**（AI SHELL 收尾汇报，19:37）：上游 opencode 2.0.14 → **2.3.2**（上游最新稳定版）；平台制品轴 1.0.x → 打包盖章；4 阶段（Checker→Upgrader→Verifier→Packager）全 PASS；Packager 产物等待人工上传发布。
- **RSI 训练分析**（round 4）：6 节点/4 边；机器墙钟 79.0m，工作总量 78.3m，关键路径 31.4m；并行收益 0.99×；人工等待 48,283s ≈ 804.7m，**占总时长 91%**；发现 2 条（human_wait 91% + lost_parallelism「理论 2.5× 实际 1.0×」，均 open）。

### 2.5 四任务汇总对比

| 指标 | deepseek | minimax | openclaw | opencode |
|---|---|---|---|---|
| goal ID | aada5a0c… | 2da62c29… | b86244c3… | ceb804a0… |
| 创建时间 | 11-20 15:27 | 11-20 15:36 | 11-20 18:16 | 11-20 18:17 |
| run 总数 | 13 | 15 | 11 | 11 |
| 人工卡点次数 | 2（1 REJECT + 1 APPROVE） | 2（1 REJECT + 1 APPROVE） | 2（1 中途授权 + 1 APPROVE） | 1（APPROVE，被 BUG 卡住后重试 4+ 次） |
| 评审等待时长 | 875.4m（82%） | 49.4m（37%） | 6m（4.2%，另有未计的 12.6h 中途授权暂停） | 804.7m（91%） |
| 机器工作总量 | 161.5m | 66.2m | 136.3m | 78.3m |
| 机器墙钟 | 185.9m | 83.3m | 137.9m | 79.0m |
| 并行收益实测 | 0.87× | 0.79× | 0.99× | 0.99× |
| RSI 轮次 | round 2 | round 1 | round 3 | round 4 |
| findings | 1（human_wait） | 2（human_wait + lost_parallelism） | 0 | 2（human_wait + lost_parallelism） |
| 升级结论 | dsh 0.2.1-rc.2→0.2.2-rc.5，PASS | @minimax-ai/code 0.4.12→0.7.9，PASS（首轮曾因制品未发布被拒） | 2026.9.2→2026.11.6，17 PASS/0 FAIL | 2.0.14→2.3.2，4 阶段全 PASS |
| 创建→done 总历时 | 17h40m | 2h34m | 15h02m | 16h29m（含 50min BUG 滞留） |

趋势（rsi_round delta）：关键路径 32.1m → 101.1m → 71.3m → 31.4m；并行收益 0.79× → 0.87× → 0.99× → 0.99×（蓝本流程在后两次执行中已无结构性损耗，瓶颈完全转移到**人工评审等待**）。

---

## 三、为什么训练任务没有触发蓝本优化与自迭代

> **⚠️ 2026-11-21 更新：本节原为候选假设（A/B/C），现已被反汇编级证据推翻并定论——根因是「创建即 active 的蓝本目标从未编译」（blueprint_node 全表 0 行），翻译器因无物化节点可映射而静默退出。完整证据链与修复建议见《蓝本自迭代断链-根因确认.md》。以下原文保留供追溯。**

### 3.1 预期机制（平台设计）

train 模式 goal 到达终态（人工批准 → deliver 完成）→ `OnGoalTerminal` 触发 RSI 训练分析 → `training_session` 密封、`rsi_report` ready、`rsi_round` 冻结（含工作流图、指标、findings）→ **rsi-translator 把 findings 翻译成 `blueprint_proposal`**（pending 状态，等待人工批准）→ 批准后生成新 draft 版本（applied_version_id），形成蓝本自迭代闭环。

### 3.2 实际发生了什么（已确认的事实链）

1. ✅ 四个任务的训练分析**全部成功**：4 个 training_session 均 ready，rsi_round 1–4 冻结完整，产出 5 条 findings（3×human_wait、2×lost_parallelism，其中 3 条 confirmed、2 条 open），趋势 delta 正常计算。
2. ❌ **`blueprint_proposal` 表 0 行**；`GET /blueprints/{id}/proposals` 返回空。翻译器从未产出任何提案。
3. ❌ 整个 daemon.log（11-16 起，覆盖全部 4 次分析完成时刻）**没有任何一条 `[rsi]` 前缀日志**——而二进制中明确存在翻译器成功/失败/跳过的日志模板：`[rsi] auto-proposal drafted for goal %s`、`[rsi] auto-proposal for goal %s: %v`（错误）、`[rsi] auto-proposal cap check failed`、`[rsi] auto-proposal draft rejected by validator`、`translator proposal skipped (rsi.max_auto_rounds)`。一条都没出现，说明翻译步骤**没有执行到产生日志的那一行**。
4. ⚠️ `rsi.max_auto_rounds` 未在 app_settings 配置（走默认值）；其上限计数 SQL（统计 rsi-translator 名下 pending/approved 提案数）当前结果为 0，若机制正常不应封顶。
5. ⚠️ 二进制中存在专门的 nil 检查日志字符串 **`rsi: blueprint service not wired`**，以及 HTTP 层 `blueprint service not configured`——部署版明确考虑过「RSI 服务未接线蓝图服务」这一失败态。
6. ⚠️ **源码与二进制不一致**：工作区源码 `/root/workspace/agentwork-src` 完全没有 blueprint 模块（grep 无任何 blueprint 代码）；运行中的 daemon 二进制 panic 栈显示构建自 `/tmp/agentwork-featnew`（该目录已不存在）。即**当前无法从源码核对翻译器调用链的确切断点**。

### 3.3 结论（按可能性排序）

- **最可能（候选 A）**：部署 daemon 的 RSI 服务实例**没有接线 BlueprintService**（构建/启动装配缺一步），分析完成后翻译器直接走 nil 检查退出或被整体跳过。「零日志 + 零提案」与该假设完全吻合；`rsi: blueprint service not wired` 字符串的存在即为此失败态预留的。
- **候选 B**：翻译器存在但触发条件未满足（例如要求 findings 达到 confirmed+分数阈值、或要求 ≥N 轮趋势数据后才翻译），且跳过分支日志是 Debug 级、被默认 INFO 级别过滤。本轮 round 4 已有 2 条 open findings + 3 条 confirmed，若真有阈值也应早满足，故此可能性次之。
- **候选 C**：该 build 只实现了「3 次同构 infer_shape 自动起草新蓝图」（emergent draft 路径——11-20 09:45 曾对 verify-domain 域触发过一次 `drafting blueprint v1`），而「findings → 已有蓝本的 patch 提案」路径未启用。自动升级域 4 个任务全是 owner+consult 自由编排，**没有产生任何 infer_shape 行**（表内 3 行全部属于 verify-domain），emergent 路径天然不适用。

### 3.4 验证与修复建议

1. **验证手段（低成本）**：把 daemon 日志级别调到 DEBUG 重启，然后对任一已 ready 会话调 `POST /rsi/sessions/{id}/retry` 重放分析，观察是否出现 `[rsi] auto-proposal` / `translator proposal skipped` / `blueprint service not wired` 任一日志——出现哪条即可定位候选 A/B/C。
2. **找回 featnew 源码**（GitCode 仓库，二进制 module 路径 `github.com/eushing/agentwork`），核对 `OnGoalTerminal → translator` 调用链与 daemon 装配代码（`rsi.go` / `blueprint.go` / handler 装配处），确认 BlueprintService 是否传参。
3. 无论根因是哪个，都建议给翻译器补一条**每次分析完成后的 INFO 级 trace**（起草了/跳过了/原因），否则这类「静默断链」下次还要靠翻数据库才能发现。

---

## 四、「点了批准，状态不立即更新」的原因分析

先说结论：**正常路径下 scratch 域任务的批准→done 是亚秒级的**（minimax 18:10:26、deepseek 09:06:53、openclaw 09:17:55 三次批准都是同一秒内完成交付）。你观察到的「不立即更新」来自三个不同来源，其中一个是 bug：

### 4.1 设计层面：批准 ≠ 状态翻转，中间隔着一个异步交付

点批准时平台做两件事，**分在两个事务里**：

1. **同步**：写 `gate_decision`（approve 落库，review_duration 停表）——这一步永远立刻成功；
2. **异步**：起一个 deliver goroutine 跑交付（repo 域=合并+push；scratch 域=直接调 `MarkDelivered` 把 goal 置 done）。

goal 离开 `review` 状态靠的是第 2 步的 `MarkDelivered`。所以：

- scratch 域：交付是直通收口，正常亚秒完成，肉眼上「立刻更新」；
- repo 域：交付要等合并+推送，这段窗口内状态不动，而**前端没有任何「交付中」提示**——这一点源码注释自己承认：*"the page shows no feedback while deliver runs"*。

### 4.2 opencode 那次：批准被记录了，状态却永远不动（BUG-1，见第五节）

这就是你反复点批准的这次：approve 09:06:34 已落库，但同一秒 `MarkDelivered` 遇到 `database is locked (SQLITE_BUSY)` 失败且无重试，goal 永久滞留 review；之后每次再点都被防重复保护拒绝（BUG-2），页面毫无提示。

### 4.3 openclaw 那次：你点的第一个「批准」根本不是评审闸门

openclaw 在 20:35 Packager 打包完成后触发的是蓝本 g1 类的**中途卡点**（ask-human=1，产物等待人工处理）。你次日上午点的第一个批准走的是「授权/恢复」语义——goal 从暂停回到 **active**，AI SHELL 又跑了一段收尾 run、再触发一轮 Verifier 评审 run，约 9 分钟后才**再次进入评审状态**，那次才是真正的 goal review，再点一次批准才 done。状态在 active→review→done 之间来回跳，观感上就是「批准了没反应」。这类「一次任务两次人工确认、语义不同但界面长得一样」的交互，是误判的主要来源。

### 4.4 相关观察（记录在案）

- **O-1**：gate_decision 写入与 goal 状态翻转不在同一事务，正是 BUG-1 能把「已批准」和「未完成交付」拆开的机制性原因。
- **O-2**：防重复保护被拒时只在 daemon 端打 WARN，前端拿到的只是一句 "goal already approved"，没有区分「交付进行中（请稍候）」和「交付已失败（请重试/打回）」。
- **O-3**：RSI 的 human_wait 指标只统计评审闸门，**中途授权暂停不计**（openclaw 真实人工等待 ≈12.7h，指标只记 6m）——对「人工等待占大头」的量化严重失真，这也会误导蓝本优化方向。
- **O-4**：`/rsi/overview` 的 p95_lead_time_ms ≈ 15.65h 与各任务实际相符，说明端到端口径的统计是准的，失真的只有 human_wait 分解。

---

## 五、Bug 记录（本次排障沉淀）

### BUG-1【严重】交付收口遇 SQLITE_BUSY 无重试 → goal 永久滞留 review

- **现象**：opencode 升级任务（`ceb804a0…`）人工批准后状态永不更新。
- **证据**：daemon.log `2026-11-21T09:06:34+08:00 daemon: MarkDelivered ceb804a0…: mark delivered: database is locked (5) (SQLITE_BUSY)`。同一秒 deepseek 任务也在走交付流程，两个 goroutine 抢同一个 SQLite 写锁，输的那个没有重试。
- **代码位置**（工作区源码，deliver.go `finishDeliver`）：`MarkDelivered` 报错只 `logging.Infof` 然后返回，无重试、无补偿入队。
- **影响**：一次瞬时锁冲突 = 状态机永久卡死；期间任务无法打回、无法重批。
- **自愈机制及其局限**：`recoverPendingDelivers`（决策 2-9）会在 daemon 启动时补跑「批准了但没交付」的 goal（判定条件：status=review + 最新 gate_decision=approve + review_request 无 deliver: 前缀），交付操作幂等。但它**只在启动时跑一次**——daemon 长期不重启就永远轮不到。本次即靠手动杀 daemon（pid 12382）→ 看门狗 5 秒拉起 → 自愈补交付恢复。
- **修复建议**：① `MarkDelivered` 加 SQLITE_BUSY 重试（开启 `busy_timeout` PRAGMA 或指数退避重试 2–3 次）；② 交付失败的 goal 在 review_request 上落 `deliver:` 前缀注记（该机制已存在），让防重复保护放行重批；③ `recoverPendingDelivers` 从「仅启动时」改为定时扫描（如每 10 分钟），消除对重启的依赖。

### BUG-2【中】防重复审批保护无法区分「交付进行中」与「交付已失败/丢失」

- **现象**：opencode 任务滞留期间，用户于 09:07:01、09:09:09、09:17:59 三次再点批准，均被拒，页面无有效提示；整个滞留持续 50 分钟直到人工介入。
- **证据**：daemon.log 三条 `review: … decision=approve REJECTED (already approved — deliver in flight or its result pending)`。
- **代码位置**：goal.go `ResolveReview` 的 duplicate-decision guard——`lastRunID==runID && lastDecision==approve` 即拒绝，没有「approve 落库后超过 N 分钟仍未交付」的旁路。
- **影响**：BUG-1 的故障被此保护放大成「用户点击无效且无解释」；guard 原本要保护的是异步合并窗口的竞态，方向正确，但缺失败态识别。
- **修复建议**：① guard 增加「上次 approve 距今超过阈值（如 5 分钟）且 review_request 无 deliver: 失败注记」→ 触发一次 recoverPendingDelivers 而非拒绝；② 前端对拒绝原因分态展示（交付中 vs 交付失败）；③ 交付失败时主动通知（现有的 feishu/webhook 通知链路可复用）。

### BUG-3【中】蓝本训练分析 → 优化提案的翻译链路静默断链

- **现象**：4 个 train 任务的分析全部完成、findings 有效（人工等待占比 37%~91%，明显可优化），但蓝本零提案、零自迭代，且无任何日志。
- **证据与分析**：见第三节。根因未最终定位（候选：RSI 服务未接线 BlueprintService / 触发条件未满足 / 提案路径未启用），源码缺失（featnew 目录已不在）导致无法静态核实。
- **修复建议**：按 3.4 节验证步骤定位后修复；无论哪种，补齐「分析完成必留痕」的 INFO 日志。

### 设计缺陷-4【低】批准后的异步交付窗口无 UI 反馈

- **现象/证据**：源码注释自认 "the page shows no feedback while deliver runs"；repo 域交付耗时期间用户无从判断点击是否生效。
- **建议**：goal 卡片增加「交付中」过渡态（监听 goal_delivered / deliver_failed 事件即可）。

### 观察项-5【低】machine→daemon 事件流 seq 乱序告警

- openclaw 唤醒 run（ba2246b1，09:08:30–09:08:37）期间出现十余条 `event seq gap … lost on the wire`。未影响结果（run 正常 completed），疑为多事件源并发上报时序号回绕/乱序，建议在 machine 事件协议侧核实。

---

## 六、附录

### A. 关键实体 ID 速查

| 实体 | ID |
|---|---|
| 蓝本 | 0fef5c50e1a0f058249870761e324785（自动升级流水线 v2）/ 版本 9ae4d32f95ca4384bd54f494f3ed027c |
| 域「自动升级」 | 73eaf8c18dc678a260cec1feda0d3b18（scratch） |
| goal deepseek | aada5a0c30b82d579adefeecf352f6e8 |
| goal minimax | 2da62c29032314718bf76425a8b3f06b |
| goal openclaw | b86244c3cb0845a1de927f423cd45aa4 |
| goal opencode | ceb804a0d2ca59a0bc1cf59cdb9c3a2b |
| AI SHELL（owner/总结） | b6e7384ea0186c471283e264f740dd70 |
| Checker | f75f223f1ed15828ec38fe5b4c7afc2a |
| Upgrader | cbc2615fed06deaade6078edcb2c4d80 |
| Verifier | c6f15196103bbc32b696d9406a554ebc |
| Packager | 2b1998cf3d023b744b52f98398046e55 |
| 执行机器 | 4f7b861d-0c7d-4718-a68c-307b79be6e77（agentwork-host） |
| daemon（RPA 沙箱内） | 127.0.0.1:17091，DB /workspace/.agentwork/agentwork.db |

### B. 训练会话与轮次

| goal | training_session | sealed（=批准交付时刻） | rsi_round | finding 数 |
|---|---|---|---|---|
| minimax | 12ef9c3e… | 11-20 18:10:26 | 1 | 2 |
| deepseek | 9f777236… | 11-21 09:06:53 | 2 | 1 |
| openclaw | 5489d1bc… | 11-21 09:17:55 | 3 | 0 |
| opencode | 07f2df9f… | 11-21 09:56:22（随自愈补交付触发） | 4 | 2 |

### C. 本次运维操作记录

- 11-21 09:56:20 经用户确认后 kill agentwork-daemon（旧 pid 12382）；沙箱看门狗 09:56:22 拉起新 daemon（pid 21302）。
- `recoverPendingDelivers` 自愈：`replaying pending deliver for ceb804a0…` → `deliver: 无仓库交付——产物在项目目录，按汇报验收` → `goal_delivered` 事件正常发出，goal 状态 review → **done**。
- 修复后核验：该时刻无 queued/running run 被影响；opencode 任务的训练分析（round 4）也在该时刻正常密封，未因重启丢失。

### D. 取证命令备忘（复现本文数据）

```bash
DB=/root/job-envs/sandboxes/agentwork-rpa-1795138507/.agentwork/agentwork.db
# 任务轨迹
sqlite3 $DB "SELECT r.role, a.name, r.status, r.started_at, r.finished_at FROM run r JOIN agent a ON a.id=r.agent_id WHERE r.goal_id='<goal_id>' ORDER BY r.created_at;"
# 人工决策与等待时长
sqlite3 $DB "SELECT decision, decided_at, review_duration FROM gate_decision WHERE goal_id='<goal_id>' ORDER BY decided_at;"
# RSI 指标 / findings / 轮次
curl -s http://127.0.0.1:17091/rsi/overview | python3 -m json.tool
sqlite3 $DB "SELECT round_no, summary, delta_json FROM rsi_round ORDER BY round_no;"
# 蓝本与提案
curl -s http://127.0.0.1:17091/blueprints/0fef5c50e1a0f058249870761e324785/proposals
grep -E "MarkDelivered|REJECTED|recoverPendingDelivers|auto-proposal|not wired" \
  /root/job-envs/sandboxes/agentwork-rpa-1795138507/.agentwork/daemon.log
```
