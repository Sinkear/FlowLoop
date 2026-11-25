# BUG-3 分析：创建即 active 绕过蓝本编译，未编译又一路静默到「提案 0 条」

（findings-bug3-blueprint-compile-observability.md）

> **范围**：蓝本目标的创建/激活入口、`compileBlueprintTx` 的调用面、RSI 翻译器（`draftAutoProposals` / `proposeForkForChain`）的失败可见性，以及「空蓝图通过 finalization guard」的三层放行。
> **不覆盖**：RSI 报告的统计公式与阈值（见 [analysis.md](analysis.md) §6）；`GATE` 节点的审批语义；历史目标的数据修复方案（只讨论可行性）。
> **基线**：代码基线 `HEAD = a3d2744`（2026-11-21 15:05 +0800）；事故运行版本为更旧的 featnew 构建（`0.1.0-featnew.20261119`，源码 `/tmp/flowloop-featnew` 已删除，只能靠反汇编与行为取证）。所有行号以 HEAD 为准；工作树里已出现未提交的「Create 编译」补丁，**不算已修**。每条结论给出 HEAD 判定。

## 一页结论（按影响排序）

| # | 级别 | 结论 | HEAD 判定 |
|---|---|---|---|
| P0-1 | **`Create` 允许 `status=active`，却不编译蓝本** | 唯一的编译入口是 `Activate`（backlog→active 边）；`Create` 直接把 goal 写成 active，从未调用 `compileBlueprintTx`。两条创建边只覆盖了一条 | 未修（`goal.go:266-465`；编译调用仅 `:2249`） |
| P0-2 | **未编译没有任何告警，空集连续通过三层** | finalization guard 只数 `pendingBP`（0 即放行）、翻译器把空 `blueprint_node` 当「无链可译」静默返回、UI 显示空蓝图；三层都认为「正常」 | 未修（`goal.go:1643-1647`；`rsi_analyze.go:216-224`） |
| P1-1 | **翻译器 10 处静默 `return`** | 只有 4 个出口有日志（cap、调用失败、validator 拒绝、成功）；其余失败分支无任何日志，`debug` 也看不见。事故中在线实验把日志开到 debug、手动 retry，仍然零 `[rsi]` 日志 | 未修（`rsi_analyze.go` 217/223/264/290/313/321/328/342/358/381） |
| P1-2 | **即便修好编译，本次仍是 0 提案** | 翻译器只认 `category=="serial_chain"`；本次 5 条 findings 是 3×`human_wait` + 2×`lost_parallelism`，没有可翻译类型。修复编译是必要条件，不是充分条件 | 未修（`rsi_analyze.go:246-248`） |
| P2-1 | **cap 计数与注释不符** | SQL 是全局 `COUNT(DISTINCT blueprint_id)`，注释称 per-blueprint；实际限制的是「已有提案的蓝本数」，不是「每个蓝本的提案数」 | 未修（`rsi_analyze.go:229-238`；`rsi.go:687-693`） |
| P2-2 | **UI 把「未编译」画成空状态** | 蓝图详情页空节点文案暗示「还没生成」，不提示「该目标从未编译」，人无法从界面判断是设计如此还是断链 | 未修（`web/app/blueprints/[id]/page.tsx:541`） |

**一句话**：蓝图自迭代的承诺是「目标跑完 → RSI 找改进 → 翻译成新蓝本提案」；但「创建即 active」这条入口从未编译蓝本，导致节点表为空，翻译器在第一个「无节点」分支静默退出，而所有守卫/界面都把空集当成正常——整条链断在**最上游**，且断点不发声。

## 事故事实与证据分级

| 编号 | 事实 | 证据 | 置信度 |
|---|---|---|---|
| E1 | 翻译器**确实被调用**（不是 RSI 未触发） | 反汇编 `draftAutoProposals` 在 `Analyze` 尾部被无条件调用；`OnGoalTerminal` 正常触发（round 1–4 都在 goal done 后生成）；在线实验 retry 生成 round 5 | 已确认（行为）/ 高（反汇编） |
| E2 | `compileBlueprintTx` **唯一调用方是 `Activate`** | 反汇编 `0x50a4b0` 的 xref 只有 `0x53907c`（Activate），Activate 的 xref 只有 `0x68fcd4`（activateGoal）；HEAD 源码一致：`grep compileBlueprintTx` 只有 `goal.go:2249` + 定义 | 已确认 |
| E3 | 四个 train 目标都**没有** `blueprint_node` | DB：`blueprint_node` 全表 0 行；17 个 goal 中只有 4 个 train 目标有 `blueprint_version_id`；`blueprint_proposal` 0 行；daemon.log `compile|materialize|spawn child` 零命中；finalization guard 反复打印 `blueprint=0` | 已确认（DB/日志快照） |
| E4 | `Create` 接受 active，且**没有任何编译动作** | HEAD `goal.go:306-308` 只拒 `blocked`/`review`；`Create` 事务（389-458）无 `compileBlueprintTx`；活动日志无 `activated`；owner run 在创建后 0.05–1s 内派发 | 已确认（源码+日志） |
| E5 | 翻译器**只翻译 `serial_chain`**；`human_wait`/`lost_parallelism` 无规则 | HEAD `rsi_analyze.go:246-248`；本次 5 findings 类型分布；Web 文案暗示「策略改进」但实现只有一条规则 | 已确认（源码）/ 推断（文案意图） |

补充：事故 build 的 `compileBlueprintTx` 只被 `Activate` 调用，说明这不是「featnew 构建漏了」，而是**蓝图模块接入时只覆盖了手动启动边**。这一点与 HEAD 一致。

## 逐项分析

### 1. 「创建即 active」是怎么来的

这不是事故当时才有的设计，而是两条独立的入口边：

| 边 | 引入 | 行为 | 决策依据 |
|---|---|---|---|
| `Create`（可直接落 active） | `8c78ee9`（2026-08-14，execution-safety gate batch） | 创建时若 `status=active`，在同一事务里入队 owner 首跑（`goal.go:451-457`） | 「active-birth」决策 6-15②：创建即开工，避免「建了 backlog 却没人启动」 |
| `Activate`（backlog→active） | `69782b6`（2026-08-13） | `UPDATE … status='active' WHERE status='backlog'` → 编译 → 入队（`goal.go:2205-2258`） | 手动启动边（决策 6-14） |
| 蓝图编译 | `3699584` 等（2026-11-18，Phase A–C） | `compileBlueprintTx` **只挂在 `Activate` 上**（`:2249`） | 当时认为「启动」是唯一需要物化的时刻 |

两条边都合法，问题出在蓝图接入时**只给一条边加了编译**。HEAD 的 `Create`：

```go
// internal/service/goal.go:306-308（节选）
if status == "blocked" || status == "review" {
    return nil, NewValidationError(...)
}
...
// :400（INSERT 带 blueprint_version_id，但之后没有编译）
// :451-457（active 则同事务入队首跑）
// :458 Commit
// :463-465 publish goal:created / run 事件
```

而 `Activate` 才有：

```go
// internal/service/goal.go:2249
if err := s.compileBlueprintTx(ctx, tx, g); err != nil {
    return nil, err
}
```

`compileBlueprintTx`（`blueprint.go:1191-1212`）在 `versionID==""` 时 no-op、已有节点时跳过（reopen 手动模式），其余情况应该物化 5 个节点。但 `Create` 连这个函数都没碰。

**所以正确的修复不是「active 一律改 backlog」**——那会推翻决策 6-15②，让「创建即开工」的语义消失；正确做法是**让 `Create` 在 active + 有 pin 时也编译**，与 `Activate` 共用同一段事务内逻辑。工作树已出现这个补丁（在 `Create` 事务内调 `compileBlueprintTx`，提交后 `publishCompiledRuns`），但未提交，不能算 HEAD 已修。

### 2. 为什么「没编译」能一路走到 0 提案

三层防线全部把空集当正常：

**第一层：finalization guard。** `goal.go:1629` 取 `pendingBP`（未完成 blueprint 节点数），`:1643-1647` 的 settle 条件只要求 `pendingBP==0 && pendingSG==0`；`:1652` 的总条件里 `pendingBP>0` 才 hold。空节点表 → `pendingBP=0` → 直接过。日志模板（`:1666-1667`）确实会打印 `blueprint=%d`，但 `0` 是合法值，没人会为「0」报警。

**第二层：翻译器。** `draftAutoProposals` 的第一道门：

```go
// internal/service/rsi_analyze.go:215-224（节选）
if s.blueprintSvc == nil {
    return
}
...
versionID, err := s.blueprintSvc.GetGoalBlueprintVersionID(ctx, goalID)
if err != nil || versionID == "" {
    return
}
```

`versionID` 在（4 个 train 目标）里非空，所以过第一关；但 `proposeForkForChain` 查节点：

```go
// internal/service/rsi_analyze.go:269-271
rows, err := s.db.QueryContext(ctx,
    `SELECT sub_goal_id, node_key FROM blueprint_node
      WHERE goal_id = ? AND COALESCE(sub_goal_id, '') != ''`, goalID)
```

0 行 → `chain` 为空 → `:263-265` `return nil, nil`，无日志。于是「有版本、无节点」这个最可疑的状态，被翻译器当成「没有可翻译的链」。

**第三层：UI。** 蓝图详情页在无节点时显示空状态文案（`page.tsx:541`），语义是「还没有内容」；人看到的是「蓝图是空的」，而不是「这个目标从未编译」。三层叠加，故障表现为「一切正常，就是没有提案」。

### 3. 十个静默退出点（HEAD）

`draftAutoProposals` 与 `proposeForkForChain` 共有 10 处无日志的 `return`：

| # | 位置 | 条件 | 后果 | 建议 |
|---|---|---|---|---|
| 1 | `rsi_analyze.go:217` | `blueprintSvc == nil` | 整个翻译器静默关闭（接线错误不可见） | `Warnf` 一次（启动期即可） |
| 2 | `:223` | 查版本失败 / `versionID==""` | 无 pin 的目标被跳过 | `Debugf`；无 pin 是正常态，降为 debug |
| 3 | `:264` | 链长 < 2 | 空蓝图在这里退出 | `Warnf`（当 version 非空但节点为 0 时） |
| 4 | `:290` | 节点未映射到子目标 | 蓝图与执行脱节 | `Warnf` 带 node_key |
| 5 | `:313` | 节点不是 task | 正常跳过 | `Debugf` |
| 6 | `:321` | 多个 end 节点 | 蓝图非法 | `Warnf` |
| 7 | `:328` | 没有 end 节点 | 蓝图非法 | `Warnf` |
| 8 | `:342` | brief 交叉引用缺失 | 数据不完整 | `Warnf` |
| 9 | `:358` | spec 邻接不符 | 蓝图/执行不一致 | `Warnf` |
| 10 | `:381` | 精确标题去重命中 | 正常去重 | `Debugf` |

有日志的只有：cap 失败/封顶（`log.Printf`，`:234`/`:236`）、`proposeForkForChain` 返回 err（`:252`）、validator 拒绝（`:392`）、成功（`:397`）。

两个细节让「静默」更彻底：

1. 这些分支连 `logging.Debugf` 都没有——不是「级别不够」，是**代码里没有语句**；
2. 仅有的 4 处日志用的是标准库 `log.Printf`，**不受** `/logs/level` 运行时开关控制（在线实验把级别调到 debug 也没用），且没有 `[rsi]` 前缀，按前缀 grep 会漏。

事故中的在线实验（`POST /rsi/sessions/07f2df9f…/retry` → round 5、`trigger=manual_retry`、零 `[rsi]` 日志）正是这个设计的直接结果：翻译器跑了，但在第 3 个出口无声返回。

### 4. E1 的边界：调用被确认，但「为什么没产出」只能靠代码推断

反汇编只能证明 `draftAutoProposals` 被 `Analyze` 调用；要证明它走到了哪个分支，需要日志或 DB 副作用——两者都没有（没有部分写入，没有 proposal）。因此：

- **已确认**：翻译器被调用；`compileBlueprintTx` 只被 `Activate` 调用；`Create` 不编译；节点表为 0；
- **推断**：翻译器在 `:264`（链长 < 2）退出。理由：`versionID` 非空（4 个目标都有），节点查询 0 行，唯一能返回 nil 且不写任何东西的路径就是这里；但**没有直接日志证据**。

这也是修复必须补日志的原因：下一次不能再靠反汇编。

### 5. 即便编译修好，本次仍然 0 提案

`rsi_analyze.go:246-248`：

```go
for _, f := range report.Findings {
    if f.Category != "serial_chain" {
        continue
    }
    ...
}
```

本次 5 条 findings 的类别：

| 类别 | 条数 | 是否有翻译规则 |
|---|---|---|
| `human_wait` | 3 | 无 |
| `lost_parallelism` | 2 | 无 |
| `serial_chain` | 0 | 有（`proposeForkForChain`） |

所以「编译修复 → 提案出现」是错误预期。要真正闭环，必须扩充翻译规则（至少 `human_wait`：把「等待人工审批」翻译成「增加提前评审/自动预检/缩短闸门」的蓝本改动）。这是设计缺口，不是 bug；但它决定了 BUG-3 修复的验收标准不能写成「有提案」，只能写成「翻译器不再静默 + 未编译可见 + 有规则时能产出」。

### 6. cap 的次要缺陷：全局计数

```go
// internal/service/rsi_analyze.go:229-238（节选）
// 注释称：counted per blueprint via created_by_id
SELECT COUNT(DISTINCT blueprint_id) FROM blueprint_proposal
 WHERE created_by_id = 'rsi-translator' AND status IN ('pending','approved')
```

`rsi.go:687-693` 的语义是「每个蓝本每轮最多 N 条」（default 10，0 关闭）。但 SQL 没有 `WHERE blueprint_id = ?`，实际统计的是「全局已有提案的蓝本数」。后果有两个方向：一旦有 10 个不同蓝本各留下过提案，之后**任何新蓝本**的翻译都会被整体跳过（无论它自己有 0 条还是 1 条）；反过来，单个蓝本可以累积任意多条提案，per-blueprint 的 cap 永远不会触发。属于 P2，但会让「修好编译后仍不产提案」的排查更难。

### 7. 状态入口矩阵（HEAD）

| 入口 | 初始 status | 是否编译 | 依据 |
|---|---|---|---|
| `Create(status=backlog)` | backlog | 否（等 Activate） | 正常 |
| `Create(status=active)` | active | **否** | BUG-3 主因 |
| `Activate` | backlog→active | 是 | `:2249` |
| `insertFiredGoal`（定时触发） | active | 否（且不写 `blueprint_version_id`） | `daemon.go:1583`；不构成第二未编译源，但同样无 pin |
| reopen | review→active | 否（`compileBlueprintTx` 已有节点会跳过） | 手动模式 |

`INSERT INTO goal` 全仓只有 `goal.go:400` 与 `daemon.go:1583` 两处，排除了「还有第三个创建入口」的可能。

## 复现条件

| # | 条件 | 预期（当前 HEAD） |
|---|---|---|
| R1 | `POST /goals` 带 `status=active` + `blueprint_version_id=<published v1>` | 201；`goal` 立即 active；owner run 0.05–1s 内派发；`blueprint_node` 无行 |
| R2 | 跑完 → approve → `OnGoalTerminal` → RSI Analyze | 报告生成；`blueprint_proposal` 0 行；daemon.log 无 `[rsi]`、无 `compile`/`materialize` |
| R3 | 同一版本改用 `Create(status=backlog)` 再 `Activate` | `blueprint_node` 出现 5 行（t1/g1/t2/t3/t4）；最终仍可能 0 提案（无 serial_chain） |
| R4 | 把日志级别调到 debug 后 retry | 仍无翻译器日志（静默分支根本没有语句） |
| R5 | `goal status=active` 且有 pin，观察 finalization guard 日志 | `blueprint=0` 反复出现，但不 hold、不告警 |

## 影响面

| 维度 | 影响 |
|---|---|
| 产品承诺 | 「自动升级流水线 v2」核心卖点是自迭代；4 个任务、50 个 run 跑完后提案 0 条，等于闭环没合上 |
| 数据 | `blueprint_node` 0 行；`blueprint_proposal` 0 行；RSI round 1–4 的 findings 全部停留在报告里 |
| 诊断成本 | 需要反汇编 + 在线实验 + DB 取证才能定位；日志层面零线索 |
| 历史目标 | 已 `done` 的目标无法补编译（没有「重新物化」入口）；只能新建 backlog 目标验证 |
| 信任 | UI 显示空蓝图、guard 显示正常、翻译器无日志——三层都「看起来正常」，最容易得出「RSI 没跑」的错误结论 |

## 修复方案

### A. 让创建边也编译（必须）

在 `Create` 的事务内，当 `status=="active"` 且 `blueprint_version_id != ""` 时调用 `compileBlueprintTx`（与 `Activate` 共用）；提交后补 `publishCompiledRuns`。要求：

- 编译失败 → 整个创建失败（不允许「创建成功但蓝图空」）；
- 编译幂等（`compileBlueprintTx` 已有节点会跳过，reopen 安全）；
- 活动日志写 `blueprint_compiled`（节点数），失败写 `blueprint_compile_failed`。

不建议「active 一律改 backlog」：与决策 6-15② 冲突，且会让「创建即开工」的用户语义消失。

### B. 加「未编译」断言（必须）

1. **启动/激活时**：有 pin 但 `blueprint_node` 为 0 → `Warnf`（每个 goal 一次）；
2. **finalization guard**：不能简单要求节点 >0（reopen/手动模式合法），但可以要求「编译标记」——编译成功时写一条 activity/字段；guard 见到 pin 却无编译标记就 hold 并 WARN；
3. **翻译器入口**：`versionID != "" && nodeCount == 0` → `Warnf("blueprint %s has version but 0 nodes — was it compiled?")`，再 return。

### C. 补日志（必须）

- 10 个静默点按上表分级补 `logging.Debugf`/`Warnf`，统一 `[rsi]` 前缀，走项目 logger（受 `/logs/level` 控制）；
- cap 与 validator 的 `log.Printf` 一并迁移；
- 成功路径日志加 `goal_id`、`version_id`、`findings`、`proposals` 计数。

### D. 规则扩容（产品决策）

至少把 `human_wait` 翻译成「提前评审/自动预检」类改动；否则修完编译仍然 0 提案。建议单独立项，不与 BUG-3 混在一个 PR。

### E. cap 修正

SQL 加 `AND blueprint_id = ?`，或把语义改成「全局提案总数上限」并改注释/常量名。二选一，必须与 `rsi.go` 的注释一致。

### F. UI

蓝图空状态区分「未编译」（有 pin、无节点 → 警告 + 入口）与「已编译但为空」；详情页显示编译时间/节点数/版本。

## 验收

| # | 场景 | 预期 |
|---|---|---|
| A1 | 新建 active + pin 目标 | 事务提交后 `blueprint_node` = 5；活动日志有 `blueprint_compiled` |
| A2 | 新建 active 目标但编译失败（注入） | 创建失败；无半成品 goal；日志 ERROR |
| A3 | 新建 active 无 pin | 正常创建，不编译（`versionID==""` no-op），无 WARN |
| A4 | 未编译的旧目标跑完 | finalization guard hold 或至少 WARN；翻译器 WARN 一次；不产生「正常」假象 |
| A5 | 有 pin + 节点 + 一条 `serial_chain` finding | `blueprint_proposal` 1 行；日志 `[rsi] auto-proposal drafted` |
| A6 | 有 pin + 节点 + 只有 `human_wait` | 0 提案（规则未扩容时），但日志说明「skipped: no translation rule for human_wait」 |
| A7 | 11 个蓝本各 1 条提案（cap 测试） | per-blueprint 语义下每蓝本仍可继续；全局语义下按配置整体封顶，日志说明 |

## HEAD 判定汇总

| 结论 | HEAD 状态 | 依据 |
|---|---|---|
| Create 不编译 | 未修（工作树有未提交补丁） | `goal.go:266-465`；`compileBlueprintTx` 调用仅 `:2249` |
| 未编译无告警/空集通过 | 未修 | `goal.go:1643-1647`；`rsi_analyze.go:264` |
| 翻译器 10 静默点 | 未修 | 行号表见 §3 |
| 仅 serial_chain | 未修 | `rsi_analyze.go:246-248` |
| cap 全局计数 | 未修 | `rsi_analyze.go:232` vs 注释 |
| UI 空状态误导 | 未修 | `page.tsx:541` |

## 待核

| # | 问题 | 影响 |
|---|---|---|
| T9 | featnew build 的 `Create` 是否确实没有编译（反汇编 xref 已支持，但没有源码级证据） | 影响「事故 build 与 HEAD 同缺陷」的措辞强度；结论不变 |
| T10 | 事故蓝本 v1 是否确实 5 节点、4 个 train 任务是否都 pin 同一版本 | DB 只能看到 4 个 goal 有 pin；节点表已空，无法回查模板 |
| T11 | 历史 done 目标是否需要「补编译 + 回溯提案」 | 决定是否要做数据修复工具；技术上可离线物化，但提案是否有时效性需产品判断 |

## 相关

- 根因全景：[analysis.md](analysis.md) §2–§3
- BUG-1/2：[findings-bug1-deliver-sqlite-busy.md](findings-bug1-deliver-sqlite-busy.md)、[findings-bug2-duplicate-approval.md](findings-bug2-duplicate-approval.md)
- 事故时间线：[timeline.md](timeline.md)