# Findings P1：版本对比无字段级 diff

> 本文件是 P1 的问题档案：问题复述、证据（[facts.md](facts.md) §P1 全部事实）、根因、影响与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §0、§6.2、§1）

附件 §0 背景：用户在 FlowLoop Web 端查看「自动升级流水线」蓝本的版本提案 1/2/3，**感觉三个版本没有区别**。经查证：三个版本拓扑（节点/连线/布局）完全相同（§2.3：9 节点 15 边零差异），差异仅存在于节点属性字段——v1→v2 全文仅 2 行 assignee（`t_upgrade`/`t_repair` 从 Upgrader 改派 AI SHELL），v1→v3 全文仅 1 处 `t_check` brief 末尾追加计划纪律文本。

附件 §6.2 原文：

> **诉求**：版本对比视图（diff 视图）应做**节点属性级 diff**——高亮 assignee 变更、brief 文本增删（类似代码 diff 的行级标注），而非仅渲染拓扑图对比。蓝本内容本身就是单一 JSON（`blueprint_version.content`），字段级 diff 在数据上完全可行。
> **附带建议**：版本列表应展示 changelog 摘要与 diff 统计（当前 v3 changelog 为空，v2 只有一句截断的负载描述）。

附件 §1 定性：产品体验级问题——"画布上'看不出区别'是必然"。

## 2. 证据（facts.md §P1 全部事实）

1. **路由表无版本对版本 diff 端点**（`internal/server/handler/handler.go:155-176`）：只有 `GET /workflows/blueprints/{versionId}`、`GET /blueprint-versions/{versionId}/graph`（单版本 graph）、`GET /blueprint-proposals/{proposalId}/diff`（提案 diff）。
2. **服务端已有字段级 diff 能力可复用**（`internal/service/blueprint.go:3816-3838`）：`BlueprintDiff{NodesChanged []DiffNode{ID,Kind,Base map[string]any,Patched map[string]any}}`。
3. **web 现状**：`web/lib/types.ts:1230` `BlueprintProposalDiff`（提案 diff 类型）；`web/components/blueprint-diff.tsx:169` `ProposalDiffChanges` 渲染提案 diff。版本对比只能并排拓扑图，assignee/brief 字段差异不可见。
4. **附件 §6.2 诉求**：节点属性级 diff（高亮 assignee/brief 变更）+ 版本列表 changelog 摘要与 diff 统计（当时 v3 changelog 为空、v2 只有一句截断描述）。
5. **修复方向**（facts.md）：新增 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}`，web 版本对比 UI 接入。

## 3. 根因

产品能力缺口，三层断点叠加：

- **路由层**：`internal/server/handler/handler.go:155-176` 的路由表里，「版本 vs 版本」的对比没有任何端点——只有单版本 graph 与提案 diff。
- **服务层**：字段级 diff 能力（`internal/service/blueprint.go:3816-3838` 的 `BlueprintDiff`/`DiffNode`，含节点属性 Base/Patched 对照）已经存在，但没有版本对比方向的调用方，能力闲置。
- **web 层**：只有提案 diff 的类型与渲染组件（`web/lib/types.ts:1230`、`web/components/blueprint-diff.tsx:169`）；版本对比视图退化为并排拓扑图，节点属性差异完全不可见。

后果闭环到数据面：本次三版本的改动本来就只落在 assignee/brief 字段里，拓扑零变化，唯一的对比视图又只画拓扑——"感觉没区别"是机制的必然结果，不是用户错觉。

## 4. 影响

- **用户侧**：已批准、已发布的版本改动对用户不可见，无法审查"这版到底改了什么"；v3 带着空 changelog 直接发布。
- **审批侧**：审批卡片无 changelog 摘要与 diff 统计，审批人只能盲批（本次两条提案 19:29:25/19:29:31 相隔 6 秒批准，与信息不足不无关系）。
- **复盘侧**：本 issue 的直接起因就是"三版本没区别"的观感；无字段级 diff 时，每次属性级改动都要靠导出 JSON 逐行比对才能定位（附件 §2.3 即是这样取证的）。

## 5. 修复方案

**后端**：新增 `GET /blueprint-versions/{versionId}/diff/{targetVersionId}`，返回

```
BlueprintVersionDiff{
  base_version_id, target_version_id, base, patched,
  nodes_added / nodes_removed / nodes_changed{id, kind, base, patched},
  edges_added / edges_removed,
  changelog_base, changelog_target
}
```

复用 `internal/service/blueprint.go:3816-3838` 的 `BlueprintDiff` 字段级能力。

**web**：版本对比 UI 接入该端点——节点属性级高亮（assignee/brief 变更）+ changelog 摘要与 diff 统计。

## 6. 修复状态

**已修复（本次 issue 处理，进行中）**：后端 diff 端点契约已定稿（见上），web-fixes 队友并行实施 web UI 接入。落地与验证结果待 execution-report.md 回填。
