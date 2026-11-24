# Task Breakdown（Issue #15）

## 任务项

- [x] T1 `ui.tsx`：SectionCard 增加 `descInBody`（默认 false，向后兼容）+ header desc 补 `min-w-0`
- [x] T2 `page.tsx`：`fmtABValue` 复用 `training/format.ts`（ms/比例/×/rate），删除 `ABRow` 的 `Math.round`
- [x] T3 `page.tsx`：`ABRow` 改为 `<tr>`，四列（指标/A/B/Δ），数值右对齐 + `tabular-nums`
- [x] T4 `page.tsx`：A/B 区改 `table-fixed` + `colgroup` 表格，行分隔线、表头底色、窄屏横向滚动
- [x] T5 `page.tsx`：`ProposalDiffChanges` 分组变更表（节点/边 × 新增/删除/改动），类型列 + 标识列
- [x] T6 `page.tsx`：`DiffCanvas` 两栏同尺寸画布（固定 320px 视口）+ 图注计数
- [x] T7 `page.tsx`：两块去中文 mono（含 A/B 表头「钉」、Δ 语义标签、画布计数拆分 mono/sans）
- [x] T8 `page.tsx`：字号收敛——移除 `text-[10.5px]`（提案行）与 `text-[11.5px]`（JSON 原文/草稿编辑器）
- [x] T9 两块卡片启用 `descInBody`，长说明完整可见

## 验证记录

| 命令 | 结果 |
|------|------|
| `npx tsc --noEmit` | 通过 |
| `npm run build`（Next 16） | 通过，24 条路由全部产出 |
| 编译产物 CSS 检查 | `.overflow-auto{height:320px}` 已由 Tailwind 生成 |
| `grep text-[10.5px] / text-[11.5px] / grid-cols-3`（page.tsx） | 0 命中 |

## 说明

- 仓库无 JS 单测基建（`web/package.json` 无 test script，CI 只跑 `tsc --noEmit` + `build`），因此本次以 CI 同款两步作为验证门禁；本次改动无 Go 侧变更，既有 Go 测试不受影响。
- `PageBody` 其它区块（页头版本元信息、版本时间线）仍有中文 mono，属本 Issue 范围外，未改动。
