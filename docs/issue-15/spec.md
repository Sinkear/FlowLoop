# Spec（Issue #15）：蓝本详情页「提案 diff」与「同题 A/B」排版统一

> 来源：Issue #15 需求 + Issue Agent 已发布的 Spec（comment 7099330524）。本文件是 Coding Agent 的落地口径，不改变需求边界。

## 目标

把蓝本详情页（`web/app/blueprints/[id]/page.tsx`）两块读图/读数区域统一到同一套排版语言：

1. 「优化提案 → 看 diff」：图结构对照 + 变更清单；
2. 「同题 A/B」：两个任务的执行指标对照。

统一维度：字体层级（中文一律 sans、标识用 mono）、字号刻度、行距、数字对齐与数值格式，向同页「规范图」「规范 JSON」卡片看齐。

## 根因（代码定位）

| # | 现象 | 代码位置 |
|---|------|----------|
| 1 | 变更标签、A/B 指标名与表头用 `font-mono` 渲染中文 | `page.tsx` diff 区单行 `flex flex-wrap ... font-mono text-[11px]`；A/B 区 `grid grid-cols-3 gap-2 font-mono text-[11px]` |
| 2 | `--font-mono` 是 JetBrains Mono，无 CJK 字形 → 中文回落系统字体，字宽/间距参差 | `web/app/globals.css:55` |
| 3 | 变更清单无结构：节点/边、新增/删除/改动混排在一行，行距随内容抖动 | diff 区五个 `map` 平铺 span |
| 4 | 两栏各自带工具栏/缩放，SVG 被挤、与单栏规范图卡片视觉权重不一致 | diff 区 `lg:grid-cols-2` + 两个 `WorkflowGraphView` |
| 5 | A/B 不是表格：无列宽模板、无行内边距、无分隔线，Δ 挤在 B 值单元格 | A/B 区 `grid grid-cols-3 gap-2`；`ABRow` |
| 6 | 数值格式脱离全站约定：`String(Math.round(v))` 把 0..1 的比例、1.x× 的加速、ms 都变成整数 | `ABRow` 自实现 `fmt` |
| 7 | 长说明被省略号截断 | `web/components/ui.tsx:445` `truncate text-xs` |
| 8 | 字号六档并存（含 10.5/11.5 半像素档） | 两块区域内 + 提案行元信息 |

## 验收标准（与 Issue / Spec 一致）

- [x] 提案 diff 与同题 A/B 两块内不再有中文使用 `font-mono`
- [x] 变更列表按节点/边 × 新增/删除/改动分组，组内行高一致、类型列与标识列对齐
- [x] A/B 三列 + Δ 列在任意数值长度下垂直对齐；数值右对齐并使用 `tabular-nums`
- [x] 返工率/一次通过率显示为百分比、并行加速显示 `×`、ms 类指标带单位
- [x] 卡片长说明完整可见，不被省略号截断
- [x] 两块样式与同页「规范图」「规范 JSON」卡片一致（边框/圆角/行距/色彩语义不变）
- [x] `npx tsc --noEmit` / `npm run build` 通过

## 非目标

- 不改后端 API 与数据结构（`BlueprintProposalDiff` / `ABCompareResult` 契约不动）
- 不改 `WorkflowGraphView` 渲染内核（只在页面侧协调两栏画布尺寸）
- 不改 `training/format.ts`（只 import）
- 不重构页面其它区块（页头/版本时间线的中文 mono 等不在本 Issue 范围）