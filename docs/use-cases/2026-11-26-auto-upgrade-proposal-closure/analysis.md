# 分析：P1–P7 逐项核实过程与证据（issue #31）

> 本文件是 issue #31 复盘的逐项核实记录：把附件提出的 P1–P7 七项主张对照代码逐条核实，每项按「附件主张 → 代码核实 → 判定 → 修复方案」展开；全部行号与结论原样引自 [facts.md](facts.md)（Lead 核实、已编译验证），本文件不另行复核代码。

- **核实基线**：分支 `fix/issue-31`，基线 commit `a7a60bb`（PR #29 合入后）
- **核实对象**：附件 [sources/FlowLoop自动升级流水线-三任务对照复盘与提案闭环问题记录.md](sources/FlowLoop自动升级流水线-三任务对照复盘与提案闭环问题记录.md) §1 提出的 P1–P7
- **背景数据**：蓝本「自动升级流水线」`051dbbd8f71ef729a1050ae9aa78a099`；R1 agent-browser / R2 drawio / R3 openviking 三任务全 pinned v1=`16ebee24dd5c3e64f1328d5225c038c5`

## 0. 判定总表

| # | 附件主张（章节） | 判定 | 关键依据 | 修复状态 |
|---|---|---|---|---|
| P1 | 版本对比无字段级 diff（§6.2） | **成立（产品能力缺口）** | 路由表无版本对版本 diff 端点；服务端 `BlueprintDiff` 字段级能力已存在但未暴露 | 已修复（端点 + web UI，tsc 过） |
| P2 | 同基线双提案先后批准 fork 覆盖（§6.3） | **成立（代码缺陷）** | `ApproveProposal` 只对 `p.BaseVersionID` 重放，从不检查 base 落后 | 已修复（auto-rebase + 单测） |
| P3 | 生成器只对 round 1 生成提案（§6.4） | **成立（代码缺陷），机制与附件推断不同** | 真实根因＝跨轮碰撞守卫静默跳过；round_no 不参与过滤 | 已修复（keys 释放 + 可见化 + feed 评论 + 回归） |
| P4 | 审批后未 spawn 下一轮（§6.5） | **成立（纯人工断点），机制与附件推断不同** | 唯一链路为人工按钮，无自动路径；"被 parked goal 阻塞"不成立 | 已修复（approve 自动建 backlog 轮 + toast 引导） |
| P5 | 规则盲区 + 样本污染（§6.6） | **部分成立** | human_wait 被长墙钟稀释、dispatch_wait 无规则【代码确认】；serial_chain 阈值方向仍为推断 | 部分修复（双触发 + dispatch_wait 规则 + 基线剔样本；serial_chain 未动） |
| P6 | goal 终态与节点状态漏水（§6.7） | **成立（两处：MarkDelivered 零校验 + failed 不自愈）** | 自动 deliver 路径绕过 pendingSG 校验；唤醒集合不含 failed 节点 | 已修复（硬校验 + 级联自愈 + 单测） |
| P7 | 不可重试错误被机械重试（§6.8） | **成立** | `nonRetryableMarkers` 白名单仅 "Model unavailable"，quota 类错误不在名单 | 已修复（名单扩容 + baseline 剔故障样本） |

两处**机制更正**（附件推断 ≠ 代码事实，详文 §3/§4）：

1. **P3**：附件 §6.4 推断"生成器只处理首轮"——不成立。round_no 不参与任何过滤；真实根因是跨轮碰撞守卫把 R1 两条 pending 提案的 patch keys 灌入 `t.touched`，R2/R3 全部翻译物触碰同节点被静默跳过。
2. **P4**：附件 §6.5 推断"spawn 被当时 parked 的 openviking goal 阻塞，或与 P3 同源"——不成立。与 P3 无关、无阻塞关系；`StartNextRoundFromProposal` 本就是纯人工触发，11-26 只是无人点按钮。

---

## 1. P1 版本对比无字段级 diff —— 成立（产品能力缺口）

### 附件主张（§6.2）

> 现象：用户对比 v1/v2/v3「感觉没区别」。三版本拓扑零差异（§2.3：9 节点 15 边与布局完全相同），全部差异在 assignee（2 行）与 brief（1 处追加）字段。
> 诉求：版本对比视图（diff 视图）应做**节点属性级 diff**——高亮 assignee 变更、brief 文本增删（类似代码 diff 的行级标注），而非仅渲染拓扑图对比。蓝本内容本身就是单一 JSON（`blueprint_version.content`），字段级 diff 在数据上完全可行。
> 附带建议：版本列表应展示 changelog 摘要与 diff 统计（当时 v3 changelog 为空、v2 只有一句截断的负载描述）。

### 代码核实

1. **路由表无版本对版本 diff 端点**（`internal/server/handler/handler.go:155-176`）：只有 `GET /workflows/blueprints/{versionId}`、`GET /blueprint-versions/{versionId}/graph`（单版本 graph）、`GET /blueprint-proposals/{proposalId}/diff`（提案 diff）——「版本 vs 版本」的对比没有任何 HTTP 入口。
2. **服务端字段级 diff 能力已存在、可复用**（`internal/service/blueprint.go:3816-3838`）：`BlueprintDiff{NodesChanged []DiffNode{ID, Kind, Base map[string]any, Patched map[string]any}}`——节点属性级（含 assignee/brief）的结构化 diff 类型与计算逻辑已经写好，只是没有版本对比的调用方。
3. **web 现状只渲染提案 diff**（`web/lib/types.ts:1230` `BlueprintProposalDiff`；`web/components/blueprint-diff.tsx:169` `ProposalDiffChanges`）：版本对比只能并排拓扑图，assignee/brief 字段差异不可见——与附件「画布上看不出区别是必然」的现象吻合。

### 判定

**成立（产品能力缺口）**：不是算不出 diff（服务端能力在 `internal/service/blueprint.go:3816-3838`），而是没有端点暴露、web 没有视图接入。三版本差异只在节点属性里，唯一的对比视图只画拓扑，必然「感觉没区别」。

### 修复方案

新增 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}`，返回：

```
BlueprintVersionDiff{
  base_version_id, target_version_id, base, patched,
  nodes_added / nodes_removed / nodes_changed{id, kind, base, patched},
  edges_added / edges_removed,
  changelog_base, changelog_target
}
```

复用服务端 `BlueprintDiff` 的字段级能力；web 版本对比 UI 接入（高亮 assignee/brief 变更 + changelog 摘要与 diff 统计）。详文 [findings-P1.md](findings-P1.md)。

---

## 2. P2 同基线双提案先后批准 fork 覆盖 —— 成立（代码缺陷）

### 附件主张（§6.3）

> 两条提案 base_version 均为 v1、间隔 6 秒先后 approve。第一条落地为 draft v2 后，第二条**仍基于 v1** 生成 v3 并发布，v2 的改派改动被静默丢弃（§2.3 diff 证实 v3 不含 assignee 变更）。
> 诉求：approve 第二条提案时检测 `base_version_id` 是否落后于该蓝本最新版本（含 draft）：**要么提示冲突让人工合并，要么自动 rebase 到最新版本后再生成新 draft**。至少在 changelog/审批卡片上标注"本提案不含 v2 的改动，v2 将被覆盖"。

### 代码核实

1. **根因（改前）**：`internal/service/blueprint.go` 的 `ApproveProposal`（原 `:3737-3794`）只对 `p.BaseVersionID` 做 patchedContent 重放（原 `:3749`），**从不检查 base 是否已落后蓝本最新版本**。同基线第二条提案 approve 时从 v1 内容 fork 出 v3，v2 改动被静默丢弃。
2. **事故链**（facts.md 与附件 §5.1/§5.2 互证）：11-26 两条提案同 base v1；19:29:25 批准改派提案 → draft v2 `fef7788c`；19:29:31 批准计划纪律提案 → draft v3 `a5443d0b`（从 v1 fork，不含 v2 改动）；v3 后被发布 → published 版缺 v2 的 assignee 修正。
3. **后果量化**（facts.md；附件 §4.2/§4.4）：被丢弃的改派提案针对的 wrong_assignee，最忙通道 busy 占比 57%→75% 恶化到全场最高分 70.0；19:43–19:54 token 故障期 AI SHELL/Packager busy=0 全程围观——正是改派本可缓解的场景。

### 判定

**成立（代码缺陷）**。approve 的重放基座是提案自带的 base 版本而非蓝本最新版本；同基线多提案先后批准时，后批者必然覆盖前批者。附件建议的"冲突提示或自动 rebase"二选一，修复取自动 rebase + fail-closed。

### 修复方案（已实施并编译通过）

`ApproveProposal` 先查蓝本最新版本（`ORDER BY version DESC LIMIT 1`，含 draft）；latest != base 时把补丁在最新内容上重放（auto-rebase）：

- 重放校验失败 → `NewConflictError("提案基于 v%d，但蓝本最新版本已是 v%d，补丁在最新内容上校验失败（%v）——请退回重新分析或人工合并")`（fail-closed，不静默丢改动）；
- 重放成功 → changelog = `p.Hypothesis + "（rebase：基于 v%d，已合并到最新 v%d 上）"`，并 `Infof "blueprint: approve %q auto-rebased from v%d onto v%d"`。

存量数据修复（附件 §8 存量行，对应 P2）：把 v2 的 assignee 改动合并进当前 published 版——历史数据需沙箱 DB 操作，本仓库修复以机制防重演为主，存量合并写入文档建议。详文 [findings-P2.md](findings-P2.md)。

---

## 3. P3 后续轮零提案 —— 成立，机制与附件推断不同

### 附件主张（§6.4）

> daemon 日志 11-26 全天 `[rsi] auto-proposal drafted` 仅出现于 15:14:19（R1 分析时刻，且两条提案的 goal 均为 `d5f0e5baf…`＝R1 的 agent-browser）；R2（15:24:19）与 R3（15:34:19）分析时刻无任何提案生成行。R2/R3 共 6 条 findings——含全场最高分 70.0 的 wrong_assignee、全新类别 split_candidate / retry_amplification——全部未进入翻译流程。
> 待查（代码层）：生成器是否只处理 domain 首轮 terminal、或按 category 去重导致后续轮次命中同类 finding 时跳过。

### 代码核实 —— **与附件推断不同**

附件的待查方向（"只处理首轮 / 按 category 去重"）**不成立**，代码里 **round_no 不参与过滤**。真实根因链（facts.md）：

1. **跨轮碰撞守卫**（`internal/service/rsi_translate.go:173-194`）：把该蓝本**所有 pending+approved 提案**的 patchKeys 灌入 `t.touched`；
2. **touched 命中静默跳过**（`draftProposal` `:304-309`）：翻译物触碰已占节点 → `Debugf` 静默跳过；
3. **同标题 dedup 静默跳过**（`internal/service/rsi_translate.go:310-319`）→ `Debugf` 静默跳过。

事故链：R1 两条提案 15:14:19 起程 pending，占用 `n:t_check`、`n:t_upgrade`、`n:t_repair`；R2（15:24）/R3（15:34）全部 5 类翻译物（wrong_assignee / plan_deviation / serial_chain / split_candidate / retry_amplification）均触碰这些节点/边 → 全部被静默跳过。Debug 级日志默认不可见，daemon 全天只见 R1 两行 `auto-proposal drafted`——与附件 §5.2 观测一致，但机制不是"只处理首轮"。

### 判定

**成立（代码缺陷），但机制与附件推断不同**：生成器对每一轮都会跑，是碰撞守卫把后续轮全部静默封死。守卫本身设计合理（防止两提案改同一节点互相踩），缺陷在三点：①keys 被 pending 提案**永久占用**；②跳过**静默**（仅 Debugf，默认不可见）；③无任何出口提示人去清障。

### 修复方案（已实施并编译通过）

1. `translateCtx` 增 `owners map[string]string`（patch key→占用提案 id）与 `blocked []translationBlock{FindingCategory, ProposalTitle, Reason("touched"|"same-title"), Key, OwnerProposalID}`；`draftAutoProposals` 签名改为返回 `[]translationBlock`。
2. **keys 释放条件**：守卫查询加

   ```sql
   AND NOT (p.status='approved' AND COALESCE(p.applied_version_id,'')!=''
            AND EXISTS (SELECT 1 FROM blueprint_version av
                        WHERE av.id=p.applied_version_id AND av.status='published'))
   ```

   —— approved 且 applied draft 已 published 的提案释放 keys（改动已入权威版本，后续轮触碰同节点是合法迭代）。修复"永久占位"。
3. 被挡时 `Warnf`（命名占用提案）+ `rsi_analyze.go` 调用点把 blocks 写成 goal feed system 评论（`INSERT INTO comment`，`author_type='system'`），逐条列 category/标题/占用提案 id/节点 key，尾句"请先审批或拒绝上述待审提案，下一轮分析即可继续自动转化"；`drafted==0` 且有 blocks → `Warnf` 汇总。

修复语义：不自动清障（审批权仍在人），但阻塞**可见、可归因、可推进**。详文 [findings-P3.md](findings-P3.md)。

---

## 4. P4 审批后未 spawn 下一轮 —— 成立（纯人工断点），机制与附件推断不同

### 附件主张（§6.5）

> 11-26 无 `rsi: next round for proposal → goal (pin …)` 日志行；goal 表无 pin v2/v3 的 goal（§5.2）。11-23 健康对照证明该环节本应自动发生（§5.3）。
> 后果：学习闭环最后一跳断裂——任何已发布改进版都没有机会用同场景 goal 自证，RSI 训练域永远停在"单次观察"（§4.4 narrative 原文）。
> 待查：spawn 条件是否被当时 active 的 openviking goal 阻塞（15:24 日志显示其 parked），或与 P3 同源。

### 代码核实 —— **与附件推断不同**

附件的待查方向（"被 parked goal 阻塞 / 与 P3 同源"）**不成立：与 P3 无关，无阻塞关系**。机制现状（facts.md）：

1. **唯一链路是纯人工触发**：`StartNextRoundFromProposal`（`internal/service/rsi_next_round.go:31`）是批准→下一轮唯一链路；HTTP `POST /blueprint-proposals/{proposalId}/next-round`（`internal/server/handler/blueprint.go:269-289`，body `{"start":bool}` 默认 true → `writeJSON {goal,created}`）；按钮在 `web/components/workflows/workflow-proposals.tsx:148-153`（`p.status==='approved'` 才显示）。
2. **内部流程**：GetProposal → 必须 approved+AppliedVersionID → `sourceRef="rsi-proposal:"+p.ID` 幂等（`getBySourceRef` + `nextRoundInFlight` sync.Map 单飞）→ applied draft 未发布则 Publish → `nextRoundOrigin(p.FindingID)` → `Goal{Title:"下一轮："+p.Title, ExecutionMode:"train", BlueprintVersionID, status backlog|active}`。
3. **approve 不联动**：`ApproveProposal` 只产 draft 版本，从不自动建轮、无 nudge。11-26 无人点按钮 → v3 0 次执行，`expected_metric=wall_ms down` 无从验证。
4. **11-23 健康对照（附件 §5.3，pin `7d83f4cd`）**：10:21:25 提案 → 10:31:34 approve → 10:31:46 next round——**12 秒间隔＝人工快速点击，非自动**。

### 判定

**成立（纯人工断点）**：机制现状＝设计如此（下一轮由人发令），但闭环上没有任何引导——无自动建轮、无 nudge，人不知道要去点按钮，链路就断。附件推断的"阻塞"不存在；11-23 对照里的 12 秒也只是人工手速。

### 修复方案（已落地）

approve 时对 RSI 提案（`FindingID≠""`）自动调 `StartNextRoundFromProposal(start=false)` 建 **backlog 轮**（不自动 start，人仍是发令者）；approve 响应恒带 `next_round_goal_id`（空串=未建）+ `next_round_created`/`next_round_error`；web 端 doApprove 回调页面层 toast 引导（成功绿+goal 链接/失败 amber）；人工按钮保留（幂等）。详文 [findings-P4.md](findings-P4.md)。

---

## 5. P5 规则盲区 —— 部分成立

### 附件主张（§6.6）

> 盲区 1：R2 `dispatch_wait=13,498,787ms`（≈墙钟全长）与 `human_wait=5,170,000ms`（86m，与 gate review_duration=5160s 吻合）是三轮中最大的两块浪费，**无任何 finding 类别覆盖**（现有 5 类：wrong_assignee/serial_chain/plan_deviation/split_candidate/retry_amplification）。
> 盲区 2（阈值方向性，推断待源码确认）：serial_chain 在 R1（链占墙钟 80%）、R3（92%）触发，R2 约 46%（104.6m/225.0m）未触发——**等待越严重、发现越少**。
> 样本污染：R3 分析时刻 15:34 处于 token 配额故障期（15:08–15:20 连环失败、16:23 Packager BLOCKED），冻结图中 retry_amplification=3、"6 子任务 8 次执行"均为故障所致；建议 RSI 剔除或标注非健康执行段。

### 代码核实

1. **human_wait 规则被长墙钟稀释【确认】**：share 公式＝wait/(wall+wait)（`internal/service/workflow.go:592-595`）；阈值 `ruleHumanWaitShare=0.30` + `ruleHumanWaitFloorMS=600000`(10min)（`internal/service/rsi_analyze.go:51-52`）；规则体 `internal/service/rsi_analyze.go:761-783`（share≥0.30 && humanMS≥600000 才出 finding，severity=info）；翻译器 `translateHumanWait`（`internal/service/rsi_translate.go:889-935`，同双阈值+幂等节点检查）。R2 实测：wait=5170s、wall=13500s → **27.7%<30% 未触发**——86 分钟人工等待被长墙钟稀释，附件推断确认。
2. **dispatch_wait 无独立规则【确认】**：只有指标采集（`internal/service/workflow.go:559`）、merge_candidate 证据行（`internal/service/rsi_analyze.go:485`）、baseline 键（`internal/service/rsi_analyze.go:817`），**无独立 finding 规则/翻译器**——盲区确认。
3. **serial_chain 阈值方向【仍为推断】**（附件 §6.6 标注为推断）：R1 链占墙钟 80%、R3 92% 触发；R2 占 46%（104.6m/225.0m）未触发——"等待越严重、发现越少"，而 R2 恰是浪费最大的一轮。
4. **baseline 机制**（`internal/service/rsi_analyze.go:819-830`）：同域 ready 报告最近 20 份取中位数，<5 样本 → 空 map（本次三轮 narrative 均注明"单次观察"）。
5. **样本污染**（附件）：R3 分析时刻在 token 故障期，retry_amplification=3 系故障所致；建议 RSI 剔除/标注非健康执行段。

### 判定

**部分成立**：human_wait 稀释（公式+双阈值合取）与 dispatch_wait 盲区两项经代码确认成立；serial_chain 阈值方向在附件中本就标注为推断，本轮未复核其规则源码；样本污染为附件观察，修复方向成立。

### 修复方案（部分落地）

- ✅ dispatch_wait finding 规则（floor 5min + share≥0.15；**不注册翻译器**——lane 数是运行时配置不在蓝图 spec 里，无诚实结构变换，finding 进报告供人读）；
- ✅ human_wait 触发条件补绝对值维度（≥1h 免稀释触发；translateHumanWait 同步双触发，否则 absolute finding 会被翻译器旧条件 skip）；
- ⏸ serial_chain 阈值方向复核**未做**（附件推断，本轮未复核规则源码，不盲改）；
- ✅ RSI baseline 剔除非健康执行段（与 P7 修复联动：`goalHadFaultRuns` 过滤故障样本）。

详文 [findings-P5.md](findings-P5.md)。

---

## 6. P6 收口缺陷 —— 成立（两处：MarkDelivered 零校验 + failed 不自愈）

### 附件主张（§6.7）

> agent-browser：goal=done，但「Packager·打包」sub_goal=running、verification_result=0 条；冗余 Packager run 从 15:17 挂到 19:26 才 stopped（§3.2）。产物已实际交付（制品时间戳与 Packager run 吻合），属簿记漏水而非漏打包。
> openviking：gate_decision 19:34:02 已 approve，但「人工卡点·打包发布确认」(9d3236b9) 与「Packager·打包」(3f1df997) sub_goal 至 20:09 仍 status=failed；Verifier 复验 PASS 后交接评论确认"mention 交接未能让 failed 节点自动回到 running……需 owner retry"——approve/上游 PASS 均无法自愈 failed 节点。
> 建议：① goal 置 done 前硬校验全部 sub_goal 终态；② gate approve、上游节点 verified 时自动将下游 failed 节点复位为 running 或触发 owner 重派。

### 代码核实

**缺口一：`MarkDelivered` 零校验。** 置 done 共 5 路径（`internal/service/goal.go`）：`:1812` hootl auto-complete、`:1906` reconcile 主路径 promote、`:2478` CompleteFollowUp、`:3471` MarkDelivered(review→done)、`:2525`/`:2639` Reopen（反向）。reconcile 主路径有完整 finalization guard（`internal/service/goal.go:1790-1795`，pendingSG/pendingChanges/pendingConsults/pendingAskHuman/pendingBP 任一>0 → 不 promote）。但 `MarkDelivered`（`internal/service/goal.go:3447-3485`）**零校验**：success 分支直接 `UPDATE goal SET status='done' WHERE status='review'`；注释假设 finalization guard 保证 pending sub-goal 不存在，但 gate_decision approve→deliver 自动路径**不经过 `SubmitForReview`**（`internal/service/goal.go:1060-1091` 有人工 pendingSG 校验）→ gate 放行时 sub_goal 可仍 running。附件 agent-browser 案例：Packager sub_goal=running、verification_result=0 条、goal=done。

**缺口二：failed 下游不自愈。** `wakeDependentsTx`（`internal/service/subgoal.go:1698-1780`）先 `failGatedDependentsTx`，再 `gatedSubGoalsTx`（`internal/service/subgoal.go:1798-1812`）只查 `status='running'` 且无 run 的子目标 → **failed 节点不在唤醒集合**；gate approve/上游 verified 后无法复位 failed 节点。openviking 案例：gate 19:34 approve 后 `9d3236b9`、`3f1df997` 至 20:09 仍 failed，Verifier 复验 PASS 也无效。`ResolveBlueprintGate`（`internal/service/blueprint.go:2820-2944`）自身不级联复位 failed 下游；`ReconcileSubGoalRun` 不看 goal 终态（迟到 run 照常改写 sub_goal 状态）；cancelled 绝不复活（`CancelSubGoal` 已在取消事务里取消 queued run）。

### 判定

**成立（两处：MarkDelivered 零校验 + failed 不自愈）**。附件两个案例分别实锤两个缺口：agent-browser＝缺口一（done 但 sub_goal running/0 验证），openviking＝缺口二（gate approve 后 failed 无法复位）。

### 修复方案（已落地）

- `MarkDelivered` 补 pending sub_goal 硬校验（`deliver:guard` 拒绝，`deliver:` 前缀进 review_request 防 daemon 恢复器重放风暴）；
- `wakeDependentsTx` 尾部挂 `reviveFailedDependentsTx`：**收窄语义**——仅自动复位「带『无法满足门控』级联评论、从未有过 run」的 failed（级联阵亡），跑过又被重试链关死的 failed 留给人工 RetrySubGoal。

详文 [findings-P6.md](findings-P6.md)。

---

## 7. P7 容错缺陷 —— 成立

### 附件主张（§6.8）

> 10 次 failed run 逐字同一错误（token 额度每日刷新）；同一节点重试打满 attempt=3，两批空转：15:08–15:20（×3）与 19:43–19:54（×6，**且发生在人工已切换模型的 reopen 之后**，说明模型切换对重试批次未即时生效）。
> 建议：按错误文案/类型分类，资源配额类（429/额度类）直接 fail-fast 上报人工，不进入重试循环；此类时段产生的指标不计入 RSI 样本。

### 代码核实

1. **重试决策**（`internal/service/goal.go:1957`）：`if rc.Attempt < maxAttempts && !isImportGoal && !nonRetryable`；`maxAttempts=3`（`internal/service/goal.go:125`）；`nonRetryable=isNonRetryable(rc.Summary)`（`internal/service/goal.go:1953`，命中时 summary 加尾注"（环境/配置错误，重试无效）"）。
2. **白名单过窄（根因）**：`nonRetryableMarkers` 白名单仅 `"Model unavailable"`（`internal/service/goal.go:134-138`）——"免费 Token 额度已用完"**不在名单** → 机械重试打满 attempt=3。
3. **退避**（`retryBackoffFor` `internal/service/goal.go:157-160`）：attempt2=30s、attempt3=60s、cap 5min。
4. **事故量化**（附件 §6.8）：同一"token 额度每日刷新"错误重试 9 次跨两时段（15:08–15:20 ×3、19:43–19:54 ×6，且在人工已切模型 reopen 后——模型切换对存量重试批次未即时生效），共 10 failed run。

### 判定

**成立**。"不可重试错误被机械重试"判定正确；根因是错误分类白名单只认一种文案，quota/额度类错误全部落入机械重试。

### 修复方案（已落地）

- `nonRetryableMarkers` 扩名单 8 词（额度已用完/quota exceeded/insufficient quota/配额不足/余额不足/insufficient balance/payment required 等；**有意不收** rate limit exceeded（瞬态可 backoff 恢复）与 billing（过宽误伤））；
- RSI baseline 先收集样本后过滤 `goalHadFaultRuns`（故障期指标不入中位数池；查询失败 fail-open；防 `:memory:` 单连接库嵌套查询死锁）。

详文 [findings-P7.md](findings-P7.md)。

---

## 8. 修复落地进度与优先级

修复优先级（附件 §8 原文）：①P3 生成器 ②P4 spawn ③P2 rebase ④P1 字段级 diff ⑤P5 规则盲区 ⑥P6 状态机 ⑦P7 容错 + 存量修复（把 v2 assignee 改动合并进 published 版——历史数据需沙箱 DB 操作，本仓库修复以机制防重演为主，存量合并写入文档建议）。

| # | 修复 | 状态 |
|---|---|---|
| P3 | 碰撞守卫可见化 + keys 释放 | **已修复，回归绿**（rsi_translate.go + rsi_analyze.go；TestPhaseDAutoProposal* / TestBlueprintProposal*） |
| P4 | approve 自动建 backlog 轮 + toast 引导 | **已修复，回归绿**（handler/blueprint.go + web 三件；TestNextRound* 5 个） |
| P2 | approve auto-rebase（fail-closed） | **已修复，单测绿**（TestApproveProposalAutoRebasesSibling） |
| P1 | 版本对版本 diff 端点 + web 接入 | **已修复，单测绿**（TestBlueprintVersionDiffListsFieldChanges；web tsc EXIT:0） |
| P5 | human_wait 双触发 + dispatch_wait 规则 + baseline 剔故障样本 | **已修复**（serial_chain 方向未动：附件推断未复核，不盲改） |
| P6 | MarkDelivered 硬校验 + 级联 failed 自愈（收窄：仅救从未跑过的级联阵亡） | **已修复，单测绿**（TestCascadeFailedDependentRevivesWhenDependencyVerifies） |
| P7 | nonRetryableMarkers 扩名单 8 词 + baseline 先收集后过滤 | **已修复**（goalHadFaultRuns fail-open） |

时间顺序见 [timeline.md](timeline.md)；逐项详情见 findings-P1.md … findings-P7.md；修复执行与验证结果见 [execution-report.md](execution-report.md)。全部七项在分支 `fix/issue-31` 落地：`go build ./...` exit 0，`go test ./internal/...` 除 7 个 skill zip 环境用例（`$HOME/.flowloop/runs/skills` 无写权限，预存限制）外全绿。
