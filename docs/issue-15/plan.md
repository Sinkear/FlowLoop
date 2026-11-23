# Plan（Issue #15）：排版统一的实现方案

## 决策表

| 决策 | 选项 | 选择 | 理由 |
|------|------|------|------|
| 变更清单结构 | A 单行 flex-wrap / B 分组列表 | B | 验收要求按 节点/边 × 新增/删除/改动 分组，组内行高一致 |
| 类型列取值 | A 硬编码「节点/边」/ B 用 `nodeKindMeta`、`EDGE_KIND_LABEL` 回查 | B | 复用既有 meta，节点改动还能给出该类型的说明 |
| A/B 布局 | A 继续用 grid / B `table-fixed` + `colgroup` | B | 与 PR#14 `PlanAlignmentCard` 同款；列宽模板 + 右对齐 + 行分隔 |
| Δ 列 | A 挤在 B 值内 / B 独立列 | B | 验收要求；Δ 用 `deltaTone` 判断改善/恶化，数值用 `fmtDeltaPct` |
| `verify_pass_rate` 格式 | A 改 `format.ts` / B 页面侧适配 | B | 该键不以 `_ratio` 结尾，`fmtDeltaValue` 会落到 `fmtInt`；在页面侧补 `fmtRatio` 分支即可，不动共享模块 |
| 两栏画布 | A 改 `WorkflowGraphView` 加 prop / B 页面侧固定画布高度 | B | 组件内核不在范围；页面用固定 320px 画布高度对齐两栏 |
| 长 desc | A 全局 header 换行 / B 新增可选 `descInBody` | B | 62 处 SectionCard 共用，默认行为必须不变；由调用方按说明长度选择 |

## 改动文件

### 1. `web/components/ui.tsx` — SectionCard 增加 `descInBody`

- 默认 `false`：保持 header 内单行 `truncate`（既有 62 处调用零变更）。
- `true`：desc 移到卡片体顶部，`text-xs leading-relaxed` 整段展示，不截断。
- 同时给 header 内 desc 补 `min-w-0`，让 `truncate` 在 flex 行里真正生效（此前长 desc 会撑破 header 而不是省略）。

### 2. `web/app/blueprints/[id]/page.tsx`

**a. 数值格式（复用 `training/format.ts`）**

- 新增 `fmtABValue(metric, v)`：`_ms` → `fmtMs`、`_ratio` → `fmtRatio`、`parallel_speedup` → `fmtSpeedup`、`_rate` → `fmtRatio`、其余 `fmtInt`（均经 `fmtDeltaValue` 分派）。
- 删除 `ABRow` 内自实现的 `String(Math.round(v))`。

**b. 同题 A/B → 四列表格**

- `table-fixed` + `colgroup`：指标 108px、A/B 自适应、Δ 84px；`min-w-[560px]` 窄屏横向滚动。
- 数值列 `text-right font-mono tabular-nums`；Δ 独立列，方向图标 + `deltaTone` 语义色 + `fmtDeltaPct`；Δ 绝对值（`b−a`）放在 `title`。
- 行序固定为后端 `ABMetricKeys` 顺序（`AB_METRIC_LABELS` 键序），`deltas` 里的未知键追加在后。
- 表头 A/B 标题中文用 sans，钉住的版本哈希用 mono。

**c. 提案 diff → 结构化变更表**

- `ProposalDiffChanges`：五个分组（节点新增/节点删除/节点改动/边新增/边删除），组头「符号 + 中文组名 + 计数」，行长「类型列（中文 sans，72px 固定）+ 标识列（mono + tabular-nums）」。
- 类型列取值：新增/删除节点回查对应 graph 的 `kind` → `nodeKindMeta().label`；改动节点用 `diff.nodes_changed[].kind`；边用 `edgeKindLabel()` 解析 `src →handle→ tgt` 回查 `EDGE_KIND_LABEL`。
- 无任何变更时给空态文案（不是空白行）。

**d. 两栏画布同尺寸**

- `DiffCanvas`：边框 + 顶部图注（改动前/改动后 + 节点/边计数），两栏都固定 320px 高的画布视口（页面侧 CSS 变体，不改组件）。

**e. 字号收敛**

- 两块内只用 `text-xs` / `text-[11px]` / `text-[10px]`；移除 `text-[10.5px]`（提案行元信息）与 `text-[11.5px]`（JSON 原文 / 草稿编辑器），均归到 `text-[11px]`。

## 验证

- `npx tsc --noEmit`
- `npm run build`（Next 16，CI 同款两步）
- 检查编译产物 CSS 含固定画布高度类，确认 Tailwind 正确生成
- 无后端改动：Go 侧不受影响

## 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| A/B 窄屏挤压 | 中 | `min-w-[560px]` + 容器 `overflow-x-auto`（同 PlanAlignmentCard） |
| `descInBody` 影响其它页面 | 中 | 默认 false，仅本页两处显式开启 |
| 固定画布高度与 `max-h-[72vh]` 冲突 | 低 | 320px 小于常规视口 72vh，短视口时 `max-height` 兜底 |