# Findings P4：审批后未 spawn 下一轮 —— 纯人工断点无引导

> 本文件是 P4 的问题档案：问题复述、证据（[facts.md](facts.md) §P4）、根因、影响、修复方案与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.5、§5.2、§5.3、§1）

附件 §6.5 主张：

> 11-26 无 `rsi: next round for proposal → goal (pin …)` 日志行；goal 表无 pin v2/v3 的 goal（§5.2）。11-23 健康对照证明该环节本应自动发生（§5.3）。
> 后果：学习闭环最后一跳断裂——任何已发布改进版都没有机会用同场景 goal 自证，RSI 训练域永远停在"单次观察"。
> 待查：spawn 条件是否被当时 active 的 openviking goal 阻塞（15:24 日志显示其 parked），或与 P3 同源。

## 2. 证据（facts.md §P4 全部事实）

1. **唯一链路是纯人工触发**：`StartNextRoundFromProposal`（`internal/service/rsi_next_round.go:31`）是批准→下一轮的唯一链路；HTTP `POST /blueprint-proposals/{proposalId}/next-round`（`internal/server/handler/blueprint.go:269-289`，body `{"start":bool}` 默认 true）；按钮在 `web/components/workflows/workflow-proposals.tsx:148-153`（`p.status==='approved'` 才显示）。
2. **内部流程**：GetProposal → 必须 approved+AppliedVersionID → `sourceRef="rsi-proposal:"+p.ID` 幂等（getBySourceRef + nextRoundInFlight 单飞）→ applied draft 未发布则 Publish → `nextRoundOrigin(p.FindingID)` 取原始训练 goal 的域/通道/预算 → `Goal{Title:"下一轮："+p.Title, ExecutionMode:"train", BlueprintVersionID, status backlog|active}`。
3. **approve 不联动**：`ApproveProposal` 只产 draft 版本（代码注释自述 "still human-triggered — RSI never starts work by itself"），无自动建轮、无 nudge。
4. **11-23 对照（附件 §5.3，pin `7d83f4cd`）**：10:31:34 approve → 10:31:46 next round——12 秒间隔＝人工快速点击，非自动。

## 3. 根因

**附件推断"被 parked goal 阻塞 / 与 P3 同源"不成立**：与 P3 无关、无任何阻塞关系。机制现状＝设计如此（下一轮由人发令，防止训练自我繁殖），但闭环断点处**没有任何引导**——无自动建轮、无提醒，人不知道要去点按钮，链路就断。11-26 无人点按钮 → v2/v3 均 0 次执行，`expected_metric=wall_ms down` 无从验证。

判定：**成立（纯人工断点缺引导）**。

## 4. 影响

- 已发布的 v3 改进零执行，学习闭环最后一跳断裂，RSI 训练域停在"单次观察"；
- 11-26 全天人工注意力都在故障处理上，无人记得"批准后还有个下一轮按钮"——任何一天忘点都会复现；
- 与 P2 复合放大：v2 的改派修正被 v3 覆盖（P2），而 v3 自身又从未被执行（P4）——两次机会同时归零。

## 5. 修复方案（[execution-report.md](execution-report.md) §1.P4）

approve handler 对 RSI 提案（`p.FindingID != ""` 且 Goal 服务已接线）自动调 `StartNextRoundFromProposal(start=false)`：

- 建 **backlog** 轮——不自动 start，人仍是执行发令者（与代码既有设计哲学一致）；
- approve 响应恒带 `next_round_goal_id`（空串=未建：非 RSI 提案/服务未接线），另有 `next_round_created`/`next_round_error`；创建失败不 fail 响应（批准已成功，人可再点幂等按钮）；
- web：doApprove 读响应回调页面层 toast（成功=ok 色+goal 链接；失败=amber 警示），SectionCard 与确认框文案改为"批准 = 落一份新草稿，并自动创建下一轮训练 goal（backlog，未启动，需人工启动）"；
- 人工"查看/开始下一轮"按钮保留（幂等：source_ref 相同返回同一 goal，created=false，不重复建）。

## 6. 修复状态

**已修复（本次 issue 处理，编译通过）**。变更文件：`internal/server/handler/blueprint.go`、`web/components/workflows/workflow-proposals.tsx`、`web/app/workflows/[id]/page.tsx`。回归：`TestNextRound*`（5）全绿；web 7 文件 tsc EXIT:0。
