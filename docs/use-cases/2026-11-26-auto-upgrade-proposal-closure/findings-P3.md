# Findings P3：后续轮分析零提案 —— 碰撞守卫静默封死

> 本文件是 P3 的问题档案：问题复述、证据（[facts.md](facts.md) §P3）、根因、影响、修复方案与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.4、§5.2、§1）

附件 §6.4 主张：

> R2（15:24）/R3（15:34）分析时刻无任何提案生成行（daemon 日志全天 `[rsi] auto-proposal drafted` 仅出现于 15:14:19，且两条提案的 goal 均为 `d5f0e5ba`＝R1 的 agent-browser）。R2/R3 共 6 条 findings——含全场最高分 70.0 的 wrong_assignee、全新类别 split_candidate / retry_amplification——全部未进入翻译流程。
> 待查（代码层）：生成器是否只处理 domain 首轮 terminal、或按 category 去重导致后续轮次命中同类 finding 时跳过。

## 2. 证据（facts.md §P3 全部事实）

1. **round_no 不参与任何过滤**——附件的待查方向不成立。生成器对每一轮分析都会跑（`internal/service/rsi_translate.go` 的 `draftAutoProposals` 由 `rsi_analyze.go:236` 每次分析调用）。
2. **跨轮碰撞守卫**（`internal/service/rsi_translate.go:173-194`）：把该蓝本**所有 pending+approved 提案**的 patchKeys 灌入 `t.touched`（键形 `n:<nodeID>` / `e:src\x00handle\x00tgt`），设计初衷是防两提案改同一节点互相覆盖。
3. **touched 命中静默跳过**（`draftProposal` `:304-309`）：`Debugf "touches %s already owned by another proposal"` → skip。Debug 级日志默认不可见。
4. **同标题 dedup 静默跳过**（`:310-319`）：同蓝本同标题提案已存在 → `Debugf` → skip。
5. **事故链**：R1 两条提案 15:14:19 起 pending，占用 `n:t_check`、`n:t_upgrade`、`n:t_repair`；R2（15:24）wrong_assignee、R3（15:34）全部 5 类翻译物均触碰这些节点/边 → 全部被静默跳过。daemon 全天只见 R1 两行——与附件 §5.2 观测一致。

## 3. 根因

守卫本身设计合理（批准两条触碰同节点的提案会互相覆盖），缺陷在三点：

- **keys 被 pending 提案永久占用**：提案已 approved 且其 draft 已 published 后，改动已入权威版本，后续轮触碰同节点应是合法迭代，但守卫没有"释放"概念；
- **跳过静默**：仅 Debugf，daemon 默认日志级别下不可见，运维无从发现"生成器其实在跑、只是全被挡了"；
- **无出口**：没有任何提示告诉人"去审批/拒绝 pending 提案就能解锁下一轮"。

判定：**成立（代码缺陷），但机制与附件推断不同**——不是"只处理首轮"，是碰撞守卫 + 同标题 dedup 把后续轮全部静默封死。

## 4. 影响

- R2/R3 共 6 条 findings（含最高分 70.0 wrong_assignee 与全新类别 split_candidate/retry_amplification）零产出，自动迭代停在 R1；
- 更严重的是**不可诊断**：日志层面与"没有可翻译的 finding"完全同形，非翻源码不可能定位；
- 任何"R1 产出 pending 提案后还有后续轮分析"的场景都会复现（11-26 之前应已多次发生）。

## 5. 修复方案（[execution-report.md](execution-report.md) §1.P3）

三层，不自动清障（审批权仍在人），但让阻塞**可见、可归因、可推进**：

1. **keys 释放**：守卫查询加
   ```sql
   AND NOT (p.status='approved' AND COALESCE(p.applied_version_id,'')!=''
            AND EXISTS (SELECT 1 FROM blueprint_version av
                        WHERE av.id=p.applied_version_id AND av.status='published'))
   ```
   —— approved 且 applied draft 已 published 的提案释放 keys（改动已入权威版本）。
2. **可见性**：`translateCtx` 增 `owners map[string]string`（patch key→占用提案 id）与 `blocked []translationBlock{FindingCategory, ProposalTitle, Reason("touched"|"same-title"), Key, OwnerProposalID}`；touched 命中与同标题 dedup 从 Debugf 升级为 Warnf（命名占用提案）并记入 blocks。
3. **推送**：`draftAutoProposals` 返回 `[]translationBlock`；`rsi_analyze.go` 的 `Analyze` 把 blocks 写成 goal feed system 评论（`INSERT INTO comment`，`author_type='system'`），逐条列 category/标题/占用提案 id/节点 key + 尾句"请先审批或拒绝上述待审提案，下一轮分析即可继续自动转化"。

## 6. 修复状态

**已修复（本次 issue 处理，编译通过）**。变更文件：`internal/service/rsi_translate.go`、`internal/service/rsi_analyze.go`。回归：`TestPhaseDAutoProposal*`（4）、`TestBlueprintProposal*`（3）全绿。
