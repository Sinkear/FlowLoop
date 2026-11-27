# 事实底座（Lead 核实结论，供扩写 analysis.md / findings-P*.md 使用）

核实基线：分支 fix/issue-31，基线 commit a7a60bb（PR #29 合入后）。核实对象 = 附件 sources/ 提出的 P1–P7。
背景数据：蓝本「自动升级流水线」(051dbbd8f71ef729a1050ae9aa78a099)；R1 agent-browser / R2 drawio / R3 openviking 三任务全 pinned v1=16ebee24dd5c3e64f1328d5225c038c5。

## P1 版本对比无字段级 diff —— 成立（产品能力缺口）
- 路由表 internal/server/handler/handler.go:155-176：无版本对版本 diff 端点。只有 GET /workflows/blueprints/{versionId}、GET /blueprint-versions/{versionId}/graph（单版本 graph）、GET /blueprint-proposals/{proposalId}/diff（提案 diff）。
- 服务端已有字段级 diff 能力可复用：internal/service/blueprint.go:3816-3838 BlueprintDiff{NodesChanged []DiffNode{ID,Kind,Base map[string]any,Patched map[string]any}}。
- web 现状：web/lib/types.ts:1230 BlueprintProposalDiff（提案 diff 类型）；web/components/blueprint-diff.tsx:169 ProposalDiffChanges 渲染提案 diff。版本对比只能并排拓扑图，assignee/brief 字段差异不可见。
- 附件 §6.2 诉求：节点属性级 diff（高亮 assignee/brief 变更）+ 版本列表 changelog 摘要与 diff 统计（当时 v3 changelog 为空、v2 只有一句截断描述）。
- 修复（web-fixes 队友实施中）：新增 GET /blueprint-versions/{versionId}/diff/{targetVersionId} 返回 BlueprintVersionDiff{base_version_id,target_version_id,base,patched,nodes_added/removed/changed{id,kind,base,patched},edges_added/removed,changelog_base,changelog_target}，web 版本对比 UI 接入。

## P2 同基线双提案先后批准 fork 覆盖 —— 成立（代码缺陷），已修复
- 根因（改前）：internal/service/blueprint.go ApproveProposal（原 :3737-3794）只对 p.BaseVersionID 做 patchedContent 重放（原 :3749），从不检查 base 是否已落后蓝本最新版本。同基线第二条提案 approve 时从 v1 内容 fork 出 v3，v2 改动被静默丢弃。
- 事故链：11-26 两条提案同 base v1；19:29:25 批准改派提案→draft v2(fef7788c)；19:29:31 批准计划纪律提案→draft v3(a5443d0b)（从 v1 fork，不含 v2 改动）；v3 后被发布 → published 版缺 v2 的 assignee 修正。后果量化：wrong_assignee 最忙通道 busy 占比 57%→75% 恶化到全场最高分 70.0；19:43-19:54 token 故障期 AI SHELL/Packager busy=0 全程围观，正是改派本可缓解的场景。
- 修复（已实施并编译通过）：ApproveProposal 先查蓝本最新版本（ORDER BY version DESC LIMIT 1，含 draft）；latest != base 时把补丁在最新内容上重放（auto-rebase）；重放校验失败 → NewConflictError("提案基于 v%d，但蓝本最新版本已是 v%d，补丁在最新内容上校验失败（%v）——请退回重新分析或人工合并")（fail-closed）；成功 → changelog=p.Hypothesis+"（rebase：基于 v%d，已合并到最新 v%d 上）" + Infof "blueprint: approve %q auto-rebased from v%d onto v%d"。

## P3 后续轮零提案 —— 成立，但机制与附件推断不同，已修复
- 附件 §6.4 原推断"生成器只处理首轮"——**不成立**。真实根因：internal/service/rsi_translate.go:173-194 跨轮碰撞守卫把该蓝本所有 pending+approved 提案的 patchKeys 灌入 t.touched；draftProposal :304-309 命中 touched → Debugf 静默跳过；:310-319 同标题 dedup → Debugf 静默跳过。round_no 不参与过滤。
- 事故链：R1 两条提案 15:14:19 起程 pending，占用 n:t_check、n:t_upgrade、n:t_repair；R2(15:24)/R3(15:34) 全部 5 类翻译物（wrong_assignee/plan_deviation/serial_chain/split_candidate/retry_amplification）均触碰这些节点/边 → 全部被静默跳过；Debug 级日志默认不可见，daemon 全天只见 R1 两行"auto-proposal drafted"。
- 修复（已实施并编译通过）：
  1. translateCtx 增 owners map[string]string（patch key→占用提案 id）与 blocked []translationBlock{FindingCategory,ProposalTitle,Reason("touched"|"same-title"),Key,OwnerProposalID}；draftAutoProposals 签名改为返回 []translationBlock。
  2. **keys 释放条件**：守卫查询加 `AND NOT (p.status='approved' AND COALESCE(p.applied_version_id,'')!='' AND EXISTS (SELECT 1 FROM blueprint_version av WHERE av.id=p.applied_version_id AND av.status='published'))` —— approved 且 applied draft 已 published 的提案释放 keys（改动已入权威版本，后续轮触碰同节点是合法迭代）。修复"永久占位"。
  3. 被挡时 Warnf（命名占用提案）+ rsi_analyze.go 调用点把 blocks 写成 goal feed system 评论（INSERT INTO comment，author_type='system'），逐条列 category/标题/占用提案 id/节点 key，尾句"请先审批或拒绝上述待审提案，下一轮分析即可继续自动转化"；drafted==0 且有 blocks → Warnf 汇总。
- 修复语义：不自动清障（审批权仍在人），但阻塞可见、可归因、可推进。

## P4 审批后未 spawn 下一轮 —— 成立（纯人工断点），修复中
- 机制现状：StartNextRoundFromProposal（internal/service/rsi_next_round.go:31）是批准→下一轮唯一链路，纯人工触发：HTTP POST /blueprint-proposals/{proposalId}/next-round（internal/server/handler/blueprint.go:269-289，body {"start":bool} 默认 true → writeJSON {goal,created}）；按钮在 web/components/workflows/workflow-proposals.tsx:148-153（p.status==='approved' 才显示）。流程：GetProposal→必须 approved+AppliedVersionID→sourceRef="rsi-proposal:"+p.ID 幂等（getBySourceRef+nextRoundInFlight sync.Map 单飞）→ applied draft 未发布则 Publish→nextRoundOrigin(p.FindingID)→Goal{Title:"下一轮："+p.Title, ExecutionMode:"train", BlueprintVersionID, status backlog|active}。
- ApproveProposal 只产 draft 版本，从不自动建轮、无 nudge。11-26 无人点按钮 → v3 0 次执行，expected_metric=wall_ms down 无从验证。
- 附件 §6.5 原推断"被 parked openviking goal 阻塞"——**不成立**，与 P3 无关，无阻塞关系。
- 11-23 健康对照（§5.3，pin 7d83f4cd）：10:21:25 提案→10:31:34 approve→10:31:46 next round——12 秒间隔=人工快速点击，非自动。
- 修复方向（Lead 决策）：approve 时对 RSI 提案（FindingID≠""）自动调 StartNextRoundFromProposal(start=false) 建 backlog 轮（不自动 start，人仍是发令者）；approve 响应带 next_round_goal_id；人工按钮保留（幂等）。

## P5 规则盲区 —— 部分成立
- human_wait share 公式=wait/(wall+wait)（internal/service/workflow.go:592-595）；阈值 ruleHumanWaitShare=0.30 + ruleHumanWaitFloorMS=600000(10min)（internal/service/rsi_analyze.go:51-52）；规则体 :761-783（share≥0.30 && humanMS≥600000 才出 finding，severity=info）；翻译器 translateHumanWait（rsi_translate.go:889-935，同双阈值+幂等节点检查）。
- R2 实测：wait=5170s、wall=13500s → 27.7%<30% 未触发——86 分钟人工等待被长墙钟稀释。附件推断确认。
- dispatch_wait_ms：只有指标采集（workflow.go:559）、merge_candidate 证据行（rsi_analyze.go:485）、baseline 键（:817），**无独立 finding 规则/翻译器**——盲区确认。
- serial_chain 阈值方向（附件 §6.6，标注为推断）：R1 链占墙钟 80%、R3 92% 触发；R2 占 46%（104.6m/225.0m）未触发——"等待越严重、发现越少"。
- baseline（rsi_analyze.go:819-830）：同域 ready 报告最近 20 份取中位数，<5 样本→空 map。
- 样本污染（附件）：R3 分析时刻在 token 故障期，retry_amplification=3 系故障所致；建议 RSI 剔除/标注非健康执行段。
- 待实施修复：dispatch_wait finding 规则+翻译器；human_wait 触发条件补绝对值维度/serial_chain 方向复核。

## P6 收口缺陷 —— 成立（两处：MarkDelivered 零校验 + failed 不自愈）
- 置 done 共 5 路径（internal/service/goal.go）：:1812 hootl auto-complete、:1906 reconcile 主路径 promote、:2478 CompleteFollowUp、:3471 MarkDelivered(review→done)、:2525/:2639 Reopen（反向）。reconcile 主路径有完整 finalization guard（:1790-1795，pendingSG/pendingChanges/pendingConsults/pendingAskHuman/pendingBP 任一>0 → 不 promote）。
- **MarkDelivered（goal.go:3447-3485）零校验**：success 分支直接 `UPDATE goal SET status='done' WHERE status='review'`；注释假设 finalization guard 保证 pending sub-goal 不存在，但 gate_decision approve→deliver 自动路径不经过 SubmitForReview（:1060-1091 有人工 pendingSG 校验）→ gate 放行时 sub_goal 可仍 running。附件 agent-browser 案例：Packager sub_goal=running、verification_result=0 条、goal=done。
- **failed 下游不自愈**：wakeDependentsTx（internal/service/subgoal.go:1698-1780）先 failGatedDependentsTx，再 gatedSubGoalsTx（:1798-1812）只查 status='running' 且无 run 的子目标 → failed 节点不在唤醒集合；gate approve/上游 verified 后无法复位 failed 节点。openviking 案例：gate 19:34 approve 后 9d3236b9、3f1df997 至 20:09 仍 failed，Verifier 复验 PASS 也无效。ResolveBlueprintGate（blueprint.go:2820-2944）自身不级联复位 failed 下游。ReconcileSubGoalRun 不看 goal 终态（迟到 run 照常改写 sub_goal 状态），cancelled 绝不复活（CancelSubGoal 已在取消事务里取消 queued run）。
- 待实施修复：MarkDelivered 补 pending sub_goal 硬校验；wakeDependentsTx 把"deps 全 verified 且自身 failed"的节点纳入复位。

## P7 容错缺陷 —— 成立
- run 重试决策 internal/service/goal.go:1957 `if rc.Attempt < maxAttempts && !isImportGoal && !nonRetryable`；maxAttempts=3（goal.go:125）；nonRetryable=isNonRetryable(rc.Summary)（:1953，命中时 summary 加尾注"（环境/配置错误，重试无效）"）；**nonRetryableMarkers 白名单仅 "Model unavailable"**（goal.go:134-138）——"免费 Token 额度已用完"不在名单 → 机械重试打满 attempt=3。retryBackoffFor（:157-160）：attempt2=30s、attempt3=60s、cap 5min。
- 附件 §6.8：同一"token 额度每日刷新"错误重试 9 次跨两时段（15:08-15:20 ×3、19:43-19:54 ×6，且在人工已切模型 reopen 后——模型切换对存量重试批次未即时生效），共 10 failed run。
- 待实施修复：nonRetryableMarkers 扩名单（quota/额度/plan/rate limit 类）；RSI 分析剔除/标注故障执行段。

## daemon 日志关键行（附件 §5.2/§5.3）
- 11-26：15:14:19 两行提案生成（均 goal d5f0e5ba=R1 agent-browser）；19:29:25 approved→draft v2(fef7788c，从未发布)；19:29:31 approved→draft v3(a5443d0b，后 published)；全天无 "rsi: next round for proposal" 行。
- 11-23 对照：10:21:25 提案→10:31:34 approve→10:31:46 next round。

## 修复优先级（附件 §8 原文）
①P3 生成器 ②P4 spawn ③P2 rebase ④P1 字段级 diff ⑤P5 规则盲区 ⑥P6 状态机 ⑦P7 容错 + 存量修复（把 v2 assignee 改动合并进 published 版——历史数据需沙箱 DB 操作，本仓库修复以机制防重演为主，存量合并写入文档建议）。
