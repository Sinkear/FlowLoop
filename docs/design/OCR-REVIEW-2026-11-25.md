# OCR 委派审查报告

- 日期：2026-11-25
- 分支：`feat/ui-simplify`
- 范围：`e0df96d..worktree`，OCR 识别 58 个可审查文件，+7743 / -1834
- 工具：`ocr` v1.14.2（`ocr delegate preview` + Go/SQL 规则 + 人工自审）
- 说明：托管审查 `ocr review --from e0df96d -f json` 在当前环境不可用，报 `no valid LLM endpoint configured`；因此按 OCR 的委派模式执行，结论由人工逐文件核对。

## 结论

| 级别 | 数量 | 状态 |
|---|---:|---|
| Critical | 0 | — |
| Major | 2 | 已修复 |
| Minor | 6 | 4 已修 / 2 记录为已知边界 |

## Major（已修）

### M1. 工作流结构编辑器会静默丢弃非法连线，保存时覆盖原内容

- 文件：`web/components/workflows/workflow-spec-editor.tsx:112`
- 原逻辑：`parseSpec` 对每条边执行 `normalizeEdge`，返回 `null` 就 `.filter` 掉。JSON 里只要有一条边缺少 `source`/`target`，切到结构模式或保存时会把它静默删掉，再写回版本内容。
- 修复：改为逐条解析，遇到非法边返回明确错误 `第 N 条连线缺少 source 或 target（两者都必须是字符串）。`；`switchMode('structure')` 与 `save()` 都会显示错误并阻止保存，不再静默改数据。

### M2. 定时任务选择工作流后默认不生成复盘

- 文件：`web/app/schedules/page.tsx:552`、`:587`
- 原逻辑：选择小队默认工作流 / 选择版本时只更新 `blueprint_version_id`，`execution_mode` 仍是 `infer`。Phase C 的定时任务固定了工作流，却默认不做复盘，用户很容易以为“工作流跑了但没有任何分析”。
- 修复：选择默认工作流或版本时同步 `execution_mode="train"`；清空为自动拆解时同步 `"infer"`。用户仍可在“复盘”字段手动关闭，手动值优先。

## Minor

### 已修

- `internal/service/template.go:900` `decodeStrictJSON` 用 `dec.More()` 检查尾随 JSON，顶层对象后永远为 false，导致第二个 JSON 被忽略。改为再 `Decode` 到 `io.EOF`；新增 `TestDecodeStrictJSONRejectsTrailingContent`。
- `internal/service/goal.go:399` 小队默认工作流的版本查询忽略错误。改为传播非 `sql.ErrNoRows` 错误；无 published 仍按自动拆解处理。

### 记录为已知边界（未修）

- `internal/service/schedule.go` 的 `projectPin` 投影查询忽略错误；只影响响应里的工作流名，行本身已写入。
- `internal/service/rsi_translate.go` 的 `touched` 碰撞查询未检查 `prow.Err()`；并发双击可能产生重复提案。
- API 仍允许直接 pin `squad_id=''` 的 legacy 工作流（UI 已拒绝并提示“历史工作流已冻结”）；这是兼容旧任务/测试的显式边界，不在服务层一刀切。
- `/goals/new` 在组件已挂载时只改 query 不会重新初始化类型；轮询按 `created_at` 认领，理论上可能认领并发创建的同类对象。
- 内置 schedule 的空 pin 会从 existing 继承、无法通过更新清空；内置是 agent，实际不可 pin，当前不可达。
- `normalizeNode` 对缺 id 的节点自动生成 `node_N`，不报错。

## 验证

- `go test ./...`：通过
- `go vet ./...`：无输出
- `cd web && npx tsc --noEmit`：通过
- `npm run build`：通过（30 routes）
- `git diff --check`：通过
