# Findings P2：同基线双提案先后批准，fork 覆盖丢弃前批改动

> 本文件是 P2 的问题档案：问题复述、证据（[facts.md](facts.md) §P2 全部事实）、根因、影响、修复方案与存量修复建议，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.3、§1、§2.3）

附件 §6.3 原文：

> **现象**：两条提案 base_version 均为 v1、间隔 6 秒先后 approve。第一条落地为 draft v2 后，第二条**仍基于 v1** 生成 v3 并发布，v2 的改派改动被静默丢弃（§2.3 diff 证实 v3 不含 assignee 变更）。
> **诉求**：approve 第二条提案时检测 `base_version_id` 是否落后于该蓝本最新版本（含 draft）：**要么提示冲突让人工合并，要么自动 rebase 到最新版本后再生成新 draft**。至少在 changelog/审批卡片上标注"本提案不含 v2 的改动，v2 将被覆盖"。

数据面（附件 §2.1/§2.3）：两提案同 base v1；19:29:25 改派提案 approved → draft v2 `fef7788c59fc4301097cac5a3da890cd`（changelog 一句截断的负载描述）；19:29:31 计划纪律提案 approved → draft v3 `a5443d0bc750813230425288d64a7744`（从 v1 fork）；v2→v3 diff = brief 追加 + 两处 assignee 改回 Upgrader，**即 v3 完全不含 v2 的改派改动**。v2 从未发布，v3 其后被 published。

## 2. 证据（facts.md §P2 全部事实）

1. **根因（改前）**：`internal/service/blueprint.go` `ApproveProposal`（原 `:3737-3794`）只对 `p.BaseVersionID` 做 patchedContent 重放（原 `:3749`），从不检查 base 是否已落后蓝本最新版本。同基线第二条提案 approve 时从 v1 内容 fork 出 v3，v2 改动被静默丢弃。
2. **事故链**：11-26 两条提案同 base v1；19:29:25 批准改派提案 → draft v2 `fef7788c`；19:29:31 批准计划纪律提案 → draft v3 `a5443d0b`（从 v1 fork，不含 v2 改动）；v3 后被发布 → published 版缺 v2 的 assignee 修正。
3. **后果量化**：wrong_assignee 最忙通道 busy 占比 57%→75% 恶化到全场最高分 70.0；19:43–19:54 token 故障期 AI SHELL/Packager busy=0 全程围观，正是改派本可缓解的场景。
4. **修复（已实施并编译通过）**：`ApproveProposal` 先查蓝本最新版本（`ORDER BY version DESC LIMIT 1`，含 draft）；latest != base 时把补丁在最新内容上重放（auto-rebase）；重放校验失败 → `NewConflictError("提案基于 v%d，但蓝本最新版本已是 v%d，补丁在最新内容上校验失败（%v）——请退回重新分析或人工合并")`（fail-closed）；成功 → changelog=`p.Hypothesis+"（rebase：基于 v%d，已合并到最新 v%d 上）"` + `Infof "blueprint: approve %q auto-rebased from v%d onto v%d"`。

## 3. 根因

`ApproveProposal` 的重放基座选错了：**以提案自带 `p.BaseVersionID` 的内容为底板重放补丁，而不是蓝本当前最新版本**。提案 pending 期间蓝本可能已被其他提案推进（同基线并发提案、或多提案陆续批准），base 内容早已过时；approve 时不查"base 是否落后"，就直接从旧 base fork 出新 draft——旧 base 与新 draft 之间的所有改动（本例中 v2 的 assignee 修正）被静默挤出发布线。

判定：**成立（代码缺陷）**——不是并发冲突未防护，而是 approve 路径根本没有"最新版本"这个概念。

## 4. 影响

- **直接后果**：v2（已批准的改派提案落地版）从未发布；published 的 v3 缺失 assignee 修正，且 changelog 为空，用户无从知道丢过什么。
- **指标恶化**：wrong_assignee 逐轮走高（64.4 → 68.0 → 70.0 全场最高），最忙通道 busy 占比 57%→75%；其唯一解药恰在被覆盖的 v2 里。
- **故障期放大**：19:43–19:54 token 故障期 AI SHELL/Packager busy=0 全程围观——改派本可缓解的场景无改派可用。
- **复现面**：任何"多条提案基于同一 base 先后批准"的场景都会复现，且无任何告警。

## 5. 修复方案（已实施并编译通过）

`ApproveProposal` 增加自动 rebase，fail-closed：

1. approve 时先查蓝本最新版本（`ORDER BY version DESC LIMIT 1`，**含 draft**）；
2. latest != base → 把提案补丁在最新内容上重放（auto-rebase）；
3. 重放校验失败 → `NewConflictError("提案基于 v%d，但蓝本最新版本已是 v%d，补丁在最新内容上校验失败（%v）——请退回重新分析或人工合并")`——不静默丢改动；
4. 重放成功 → changelog 标注 `p.Hypothesis+"（rebase：基于 v%d，已合并到最新 v%d 上）"` + `Infof "blueprint: approve %q auto-rebased from v%d onto v%d"`，落盘可审计。

附件建议的"冲突提示或自动 rebase"二选一，修复取自动 rebase；rebase 失败时以 ConflictError 覆盖"提示冲突让人工合并"分支。

## 6. 存量修复建议（附件 §8 存量行）

把 v2 的 assignee 改动合并进当前 published 版（v3），使已批准优化生效。历史数据需沙箱 DB 操作，本仓库修复以机制防重演为主，存量合并以文档建议为准，不在本次代码修复范围。

## 7. 修复状态

**已修复（本次 issue 处理，已编译通过）**：auto-rebase + fail-closed ConflictError + changelog/日志标注均已实施。落地与验证结果待 execution-report.md 回填。
