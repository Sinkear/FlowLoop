# 时间线：四个蓝本训练任务、50 次运行与一次交付锁死

> **数据来源**：RPA 沙箱 DB（`goal / run / comment / gate_decision / activity_log / training_session / rsi_* / blueprint_*`）、`daemon.log`（+08:00）、daemon HTTP API `127.0.0.1:17091`。原始取证见 [sources/flowloop自动升级流水线v2-四任务执行记录与问题诊断.md](sources/flowloop自动升级流水线v2-四任务执行记录与问题诊断.md) 与 [sources/蓝本自迭代断链-根因确认.md](sources/蓝本自迭代断链-根因确认.md)。
> **时间口径**：DB 时间戳为 UTC，本文统一换算为 **UTC+8**。`wall` 指机器侧最早 run 开始 → 最晚 run 结束；`work` 指各 run 时长之和；`并行收益 = work / wall`；`关键路径（CP）` 取 workflow 图；`human_wait` = Σ`gate_decision.review_duration`；`占比 = wait / (wall + wait)`。
> **代码基线**：本仓库 `HEAD a3d2744`。事故运行版本构建自 `/tmp/flowloop-featnew`（`0.1.0-featnew.20261119`），源码已删除，行为以原始日志/DB/反汇编为准。

## 0. 口径与四处修正

原始文档的数字全部保留，但有四处需要先说明，否则总账对不上：

| # | 修正 | 说明 |
| --- | --- | --- |
| 1 | **opencode 创建→done 实为 15h39m05s** | 18:17:17 → 09:56:22。原始表写「16h29m（含 50min BUG 滞留）」把 50m 算了两次：批准等待 14h49m + 滞留 50m 已经是 15h39m。 |
| 2 | **openclaw 12.6h 授权暂停未进入 `human_wait`** | 事故 build 的 `/rsi` 只记最后一次评审闸门 362s；按当前 HEAD 的代码路径，这笔等待会被计入。两种口径的冲突见 [analysis.md](analysis.md) §6。 |
| 3 | **四个目标创建即 active，没有 `activated` 动作** | 活动日志只有 `created → commented → entered_review → review_ready`；owner run 在创建后 0.05–1s 派发。因此从未编译蓝本（见 [analysis.md](analysis.md) §2.5）。 |
| 4 | **round 5 是诊断副作用，不计入四轮趋势** | 11-21 11:09 为验证翻译器是否被调用，`PUT /logs/level → debug` + `POST /rsi/sessions/07f2df9f…/retry` 生成 round 5（trigger=manual_retry，指标与 round 4 一致）。 |

另有一处**计数差**需要标注，但不臆造补齐：原始轨迹表逐条列出 deepseek 12 条、minimax 14 条、openclaw 11 条、opencode 10 条 agent run，而 RSI 分别记为 **13 / 15 / 11 / 11**。差额是评审窗口内未列入表的短 run（review run），不影响时长与结论；总账以 RSI 计数为准。

## 1. 总账

| 指标 | deepseek | minimax | openclaw | opencode | 合计 / 整体 |
| --- | ---: | ---: | ---: | ---: | ---: |
| runs | 13 | 15 | 11 | 11 | **50** |
| 机器墙钟 wall | 185.9m | 83.3m | 137.9m | 79.0m | **486.1m**（29,166s） |
| 机器工作 work | 161.5m | 66.2m | 136.3m | 78.3m | **442.3m**（26,538s） |
| 关键路径 CP | 101.1m | 32.1m | 71.3m | 31.4m | — |
| 并行收益 work/wall | 0.87× | 0.79× | 0.99× | 0.99× | 0.91× |
| 评审等待 human_wait | 875.4m（52,524s） | 49.4m（2,961s） | 6.0m（362s） | 804.7m（48,283s） | **1,735.5m（104,130s ≈ 28.9h）** |
| 等待占比 | 82% | 37% | 4.2% | 91% | 78.1%（整体） |
| 另有未计等待 | 0 | 0 | **≈12.6h（授权暂停）** | 0 | ≈12.6h |
| 创建→done | 17h40m | 2h34m | 15h02m（含 12.6h 暂停） | **15h39m** | 合计 50h55m；整体跨度 18h29m |
| RSI 轮次 | round 2 | round 1 | round 3 | round 4 | 4 轮 + round 5（重放） |
| findings | 1 | 2 | 0 | 2 | **5**（3 confirmed / 2 open） |

- 整体跨度：11-20 15:27:23 → 11-21 09:56:22 = **18h28m59s**。四任务「创建→done」相加为 50h55m，是因为四个任务高度重叠，不能当墙钟看。
- `blueprint_proposal` 全表 **0 行**、`blueprint_node` 全表 **0 行**（取证见 [analysis.md](analysis.md) §2.3）。

## 2. 逐任务时间线

### 2.1 deepseek harness 升级（goal `aada5a0c30b82d579adefeecf352f6e8`）

- 创建：11-20 15:27:23（人工，hitl）；终态：done（11-21 09:06:53 人工批准后当秒交付）。

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
| --- | --- | --- | --- | --- |
| 15:27:24–15:28:09 | owner 首跑 | AI SHELL | `a4d8f26d` | 46s |
| 15:28:07–15:33:31 | Checker 查新分级 | Checker | `d0ec01c6` | 5m24s |
| 15:33:31–15:33:43 | owner 派发 | AI SHELL | `b0015503` | 12s |
| 15:33:44–15:50:05 | 首轮 Verifier 评审 | Verifier | `86c6caaa` | 16m21s |
| 15:50:05 | 进入评审（`review_ready`） | — | — | — |
| **15:56:53** | **人工 REJECT（第 1 次卡点，评审停留 407s）** | human | — | 407s |
| 15:56:53–15:57:14 | owner 唤醒续跑 | AI SHELL | `f3ee9122` | 21s |
| 15:57:09–17:38:17 | Upgrader 升级 | Upgrader | `01f11c51` | **101m09s** |
| 17:38:18–17:38:40 | owner | AI SHELL | `ab305978` | 23s |
| 17:38:37–17:58:02 | Verifier 独立验证 | Verifier | `7066b2f6` | 19m25s |
| 17:58:02–17:58:36 | owner | AI SHELL | `8620b9a9` | 35s |
| 17:58:32–18:34:03 | Packager 打包 | Packager | `53050d99` | 35m31s |
| 18:34:03–18:34:54 | owner | AI SHELL | `343fa2c8` | 52s |
| 18:34:55–18:38:16 | 末轮 Verifier 评审 | Verifier | `f2653cab` | 3m21s |
| 18:38:16 | `review_ready` | — | — | — |
| **11-21 09:06:53** | **人工 APPROVE（评审停留 52,117s ≈ 14.5h，过夜）→ 当秒交付 done** | human | — | 52,117s |

- 升级结果：dsh 0.2.1-rc.2 → **0.2.2-rc.5**（github+npm 双源一致）；平台轴 1.0.21 → 1.0.22 → 打包盖章 **1.0.23**；dsh-tui launcher 0.16.1 → 0.20.2。末轮 Verifier：**PASS**。
- RSI（round 2）：7 节点 / 4 边；CP 101.1m（四轮最高）；1 条 `human_wait`（82%，confirmed）。
- 关键观察：**09:06:53 的批准与 opencode 09:06:34 的失败交付只差 19 秒**，两者共享同一个 SQLite 写通道（见 §3）。

### 2.2 minimax-code 升级（goal `2da62c29032314718bf76425a8b3f06b`）

- 创建：11-20 15:36:08（人工，hitl）；终态：done（11-20 18:10:26）。

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
| --- | --- | --- | --- | --- |
| 15:36:08–15:36:48 | owner 首跑 | AI SHELL | `8067e4c1` | 40s |
| 15:36:40–15:38:09 | Checker | Checker | `0a25c03a` | 1m29s |
| 15:38:10–15:38:27 | owner | AI SHELL | `5f468b3f` | 17s |
| 15:38:25–15:49:39 | Upgrader（第 1 次） | Upgrader | `8eb10227` | 11m14s |
| 15:49:39–15:50:01 | owner | AI SHELL | `b16e2b31` | 21s |
| 15:50:01–15:53:32 | 首轮 Verifier 评审 | Verifier | `ac39eba8` | 3m31s |
| 15:53:32 | `review_ready` | — | — | — |
| **16:02:39** | **人工 REJECT（第 1 次卡点，评审停留 547s）** | human | — | 547s |
| 16:02:39–16:03:19 | owner 唤醒 | AI SHELL | `07f2d777` | 40s |
| 16:03:14–16:16:39 | Checker（第 2 次，重定级） | Checker | `64aaedc4` | 13m25s |
| 16:16:39–16:17:21 | owner | AI SHELL | `fe97e51f` | 42s |
| 16:17:19–16:49:25 | Upgrader（第 2 次） | Upgrader | `cf21055f` | **32m06s** |
| 16:49:25–16:53:44 | owner | AI SHELL | `bec79436` | 4m19s |
| 16:52:01–16:59:59 | Verifier 独立验证 | Verifier | `9bef9045` | 7m58s |
| 16:59:59–17:25:38 | owner（总结） | AI SHELL | `d2ec3c46` | 25m39s |
| 17:25:39–17:30:11 | 末轮 Verifier 评审 | Verifier | `ca72249d` | 4m32s |
| 17:30:11 | `review_ready` | — | — | — |
| **18:10:26** | **人工 APPROVE（评审停留 2,414s ≈ 40m）→ 当秒交付 done** | human | — | 2,414s |

- 升级结果：@minimax-ai/code 0.4.12 → **0.7.9**（`dist-tags.latest` 实测；用户原以为 0.7.7，实际已有 0.7.8/0.7.9）。首轮 Verifier 拒绝原因：**升级执行正确、动态验证通过，但制品未发布、升级不可持久化**——这是流程内 REJECT 的正面样本。
- RSI（round 1，蓝本首轮基线）：8 节点 / 5 边；CP 32.1m；并行收益仅 **0.79×（理论 2.1×）**；2 条 findings（`human_wait` 37% + `lost_parallelism`，均 confirmed）。
- 关键观察：**REJECT → 重定级 → 重升级 → 补打包** 的回路完整跑通，说明「训练任务 + RSI 发现 + 蓝本建议」的素材是有效的——只是没被翻译器接走。

### 2.3 openclaw 升级（goal `b86244c3cb0845a1de927f423cd45aa4`）

- 创建：11-20 18:16:15（人工，hitl）；终态：done（11-21 09:17:55）。

| 时间 | 阶段 | 执行者 | run | 时长 |
| --- | --- | --- | --- | --- |
| 11-20 18:16:15–18:16:45 | owner 首跑 | AI SHELL | `0df2f67e` | 30s |
| 18:16:42–18:26:56 | Checker | Checker | `aaa59a64` | 10m14s |
| 18:26:56–18:27:28 | owner | AI SHELL | `24c2eea4` | 32s |
| 18:27:21–19:38:42 | Upgrader 升级 | Upgrader | `af8480e6` | **71m21s** |
| 19:38:42–19:39:24 | owner | AI SHELL | `3e446f8f` | 42s |
| 19:39:21–19:52:33 | Verifier 独立验证 | Verifier | `778ab1be` | 13m12s |
| 19:52:33–19:53:10 | owner | AI SHELL | `d1e6c3d6` | 37s |
| 19:53:07–20:34:37 | Packager 打包 | Packager | `93faf447` | 41m30s |
| 20:34:37–20:35:39 | owner（末段收口） | AI SHELL | `309b13a0` | 1m02s |
| **20:35:39** | **中途卡点（finalization guard：ask-human=1）——打包完成，等人工处理「产物上传发布」** | — | — | — |
| *（过夜等待 ≈12.6h）* | — | — | — | — |
| 11-21 ~09:08:25 | 人工恢复（唤醒 owner） | AI SHELL | `ba2246b1` | 09:08:25–09:08:42（17s） |
| 09:08:42 | 进入评审（parked review） | — | — | — |
| 09:08:42–09:11:52 | Verifier 评审 | Verifier | `d8416f47` | 3m10s |
| 09:11:52 | `review_ready` | — | — | — |
| **09:17:55** | **人工 APPROVE（评审停留 362s ≈ 6m）→ 当秒交付 done** | human | — | 362s |

- 升级结果：上游 2026.9.2 → **2026.11.6**（npm 最新），环境 running；独立验证 **17 PASS / 0 FAIL**（34 条 base 用例，zz.013 为升级前既存缺口）；产物出包自检通过，Verifier 复核 PASS。
- RSI（round 3）：6 节点 / 4 边；CP 71.3m；并行收益 **0.99×**；`human_wait` 仅 362s（4.2%）；**0 findings**——四任务中执行最顺的一个。
- **口径警告**：真实人工等待 ≈12.6h（占 15h02m 的 84%），指标只记 6m。若按 HEAD 行为计入，openclaw 从「0 findings、最顺」变成「human_wait ≈84%」，会直接改变蓝本优化优先级。
- 异常观察：唤醒 run `ba2246b1` 在 09:08:30–09:08:37 连续十余条 `event seq gap … lost on the wire`，run 正常 completed，未影响结果；疑为多事件源并发上报的序号乱序（观察项-5）。

### 2.4 opencode 升级（goal `ceb804a0d2ca59a0bc1cf59cdb9c3a2b`）——触发 BUG 的那个

- 创建：11-20 18:17:17（人工，hitl）；终态：done（本应 09:06:34 批准即完成，实际 09:56:22 由重启自愈补交付）。

| 时间（11-20） | 阶段 | 执行者 | run | 时长 |
| --- | --- | --- | --- | --- |
| 18:17:17–18:17:46 | owner 首跑 | AI SHELL | `4b0f1d57` | 29s |
| 18:17:42–18:32:43 | Checker | Checker | `413d5c47` | 15m01s |
| 18:32:43–18:32:56 | owner | AI SHELL | `e41d9125` | 13s |
| 18:32:53–18:57:31 | Upgrader 升级 | Upgrader | `61c4a31f` | 24m38s |
| 18:57:31–18:57:55 | owner | AI SHELL | `2c9ae753` | 24s |
| 18:57:52–19:05:03 | Verifier 独立验证 | Verifier | `6b17f208` | 7m11s |
| 19:05:03–19:05:15 | owner | AI SHELL | `b12f7cb2` | 12s |
| 19:05:12–19:36:39 | Packager 打包 | Packager | `e9c6bd71` | 31m27s |
| 19:36:39–19:37:20 | owner（收口） | AI SHELL | `e91a4414` | 41s |
| 19:37:20 | parked review（gate hit） | — | — | — |
| 19:37:20–19:41:50 | Verifier 评审 | Verifier | `edb5e39f` | 4m30s（结论：goal 结果审核通过） |
| 19:41:50 | `review_ready` | — | — | — |
| **11-21 09:06:34** | **人工 APPROVE（评审停留 48,283s ≈ 13.4h，过夜）——`gate_decision` 落库成功，但交付 `MarkDelivered` 遇 `SQLITE_BUSY`** | human | — | 48,283s |
| 09:07:01 / 09:09:09 / 09:17:59 | 用户再次批准，均被防重复保护拒绝 | human | — | — |
| **09:56:20–09:56:22** | **运维介入：杀 daemon（pid 12382）→ 看门狗 2s 拉起（pid 21302）→ `recoverPendingDelivers` 补交付 → done** | system | — | 49m48s 停摆 |

- 升级结果：opencode 2.0.14 → **2.3.2**（上游最新稳定版）；平台制品轴打包盖章；4 阶段（Checker→Upgrader→Verifier→Packager）全 PASS；Packager 产物等待人工上传发布。
- RSI（round 4）：6 节点 / 4 边；CP 31.4m；并行收益 0.99×；`human_wait` 48,283s = **804.7m（91%）**；2 条 findings（`human_wait` + `lost_parallelism` 理论 2.5× / 实际 1.0×，均 **open**）。
- 交付失败原文（daemon.log）：

```text
2026-11-21T09:06:34+08:00 daemon: MarkDelivered ceb804a0d2ca59a0bc1cf59cdb9c3a2b:
    mark delivered: database is locked (5) (SQLITE_BUSY)
```

- 恢复原文：

```text
2026-11-21T09:56:22+08:00 daemon: replaying pending deliver for ceb804a0…
2026-11-21T09:56:22+08:00 daemon: deliver ceb804a0…: 无仓库交付——产物在项目目录，按汇报验收
```

## 3. 09:00–10:00 的争用窗口（BUG 现场）

这是四任务里唯一一次「多个交付/审批在同一分钟结算」的窗口。原始日志按秒展开：

| 本地时间 | 事件 | 结果 | 证据 |
| --- | --- | --- | --- |
| 09:06:34 | opencode 人工 APPROVE → `ResolveReview` 写 `gate_decision`（`review_duration=48283`）→ 发布 `goal:approved` → deliver goroutine → **`MarkDelivered` UPDATE 失败** | goal 停在 review | daemon.log `SQLITE_BUSY` |
| 09:06:53 | deepseek 人工 APPROVE → approve + deliver 当秒完成 → `goal:delivered` → `OnGoalTerminal` 密封会话 | done；round 2 生成 | 活动日志/`training_session` |
| 09:07:01 | opencode 第 2 次点击 APPROVE | 拒绝：`already approved — deliver in flight or its result pending` | daemon.log |
| 09:08:25 | openclaw 人工恢复 → 唤醒 run `ba2246b1`（17s） | 事件流出现 `event seq gap` | daemon.log |
| 09:09:09 | opencode 第 3 次点击 | 再次拒绝 | daemon.log |
| 09:11:52 | openclaw Verifier 评审结束 → `review_ready` | 等待第二次人工确认 | 活动日志 |
| 09:17:55 | openclaw 人工 APPROVE → 当秒交付 done → 密封会话 | done；round 3 生成 | 活动日志 |
| 09:17:59 | opencode 第 4 次点击 | 再次拒绝（距首次批准 11m25s） | daemon.log |
| 09:56:20 | 运维 `kill` daemon（pid 12382） | 进程退出 | 运维记录 |
| 09:56:22 | 看门狗拉起 pid 21302 → 启动即 `recoverPendingDelivers` → 重放 opencode 交付 | **done**；round 4 密封，未丢分析 | daemon.log |

关键事实：**批准落库与状态翻转不在同一事务**。`gate_decision` 是独立 `Exec`（`internal/service/goal.go:2007`），写完即生效；`goal.status` 的翻转在异步 deliver 里（`MarkDelivered`，`internal/service/goal.go:3233-3272`）。两者之间任何一步失败，就会出现「已批准 + 未完成」的中间态——而防重复保护恰好把这个中间态当成「交付进行中」。

## 4. RSI 四轮对比

| 轮次 | goal | 触发 | 节点/边 | wall | work | CP | 并行收益 | human_wait | 占比 | findings |
| ---: | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1 | minimax | 交付完成自动 | 8 / 5 | 83.3m | 66.2m | 32.1m | 0.79×（理论 2.1×） | 2,961s | 37% | 2（`human_wait`、`lost_parallelism`），confirmed |
| 2 | deepseek | 交付完成自动 | 7 / 4 | 185.9m | 161.5m | 101.1m | 0.87× | 52,524s | 82% | 1（`human_wait`），confirmed |
| 3 | openclaw | 交付完成自动 | 6 / 4 | 137.9m | 136.3m | 71.3m | 0.99× | 362s | 4.2% | 0 |
| 4 | opencode | 交付完成自动 | 6 / 4 | 79.0m | 78.3m | 31.4m | 0.99× | 48,283s | 91% | 2（`human_wait`、`lost_parallelism`），open |
| 5 | opencode | **`manual_retry`**（诊断重放） | 6 / 4 | 79.0m | 78.3m | 31.4m | 0.99× | 48,283s | 91% | 2（同上），open |

趋势与解读：

- **关键路径 32.1m → 101.1m → 71.3m → 31.4m** 与 **并行收益 0.79× → 0.87× → 0.99× → 0.99×**：蓝本流程到后两轮已无结构性并行损耗，机器侧没什么可再「优化」了。
- 四轮里 **3 条 `human_wait`** 指向同一个瓶颈：人工评审等待占 37%–91%。但翻译器只实现了 `serial_chain → fork` 一条规则，**`human_wait` 和 `lost_parallelism`（除串行链外）都没有对应模板**。所以即使 BUG-3 的编译问题修好，这 4 个目标也未必能出提案——见 [analysis.md](analysis.md) §8。
- 5 条 findings 中 2 条 `lost_parallelism`、3 条 `human_wait`；其中 3 confirmed / 2 open。opencode 的两条都还是 open（人工未复核）。
- 四轮 `trigger` 全是「交付完成自动分析」；只有 round 5 是人工 `manual_retry`。这反证了 `OnGoalTerminal → Analyze → draftAutoProposals` 链路本身是通的（详见 [analysis.md](analysis.md) §2.2）。

## 5. 结论

1. **四个升级任务本身是成功的**：4/4 done、4/4 升级目标达成、4/4 训练会话 ready、5 条 findings 有效。失败的不是任务，而是「发现 → 提案 → 下一版蓝本」的最后一步。
2. **时间账的真相是人工等待**：1,735.5m（28.9h）评审等待 + 12.6h 未计授权等待，远超机器工作 442.3m。机器并行收益已经做到 0.99×，继续优化机器编排的边际收益接近零。
3. **一次 50 分钟的停摆是三个独立缺陷的叠加**：`SQLITE_BUSY` 无重试（BUG-1）× 失败被吞成 `Infof` × 防重复保护无时间/失败态（BUG-2），再叠加恢复只在启动时跑一次。任何一个环节修好，停摆都不会发生或不会持续 49m48s。
4. **自迭代 0 提案不是「没触发」，而是「触发了但静默退出」**：round 5 的 `manual_retry` 证明翻译器在链路里，但 `blueprint_node=0` 让它在节点映射处 `return nil`，且一行日志都没有（BUG-3）。