# 分析：创建即 active 绕过编译，翻译器十处静默退出（2026-11-21）

> **分析对象**：蓝本「自动升级流水线 v2」的四个 train 目标（deepseek / minimax / openclaw / opencode）为什么跑完、分析完，却 **0 条 `blueprint_proposal`**；以及同期 opencode 的「批准了但不 done」为什么卡死 49m48s。
> **代码基线**：本仓库 `git HEAD a3d2744`（2026-11-21 15:05:23 +0800）。事故运行版本构建自已删除的 `/tmp/agentwork-featnew`（`0.1.0-featnew.20261119`，无 vcs stamp），源码不在仓库内；凡引用事故版本行为，均标【反汇编】或【DB】。
> **配套材料**：[timeline.md](timeline.md)（时间账与口径）、[sources/蓝本自迭代断链-根因确认.md](sources/蓝本自迭代断链-根因确认.md)（原始反汇编/在线实验）、[sources/agentwork自动升级流水线v2-四任务执行记录与问题诊断.md](sources/agentwork自动升级流水线v2-四任务执行记录与问题诊断.md)（原始 DB/日志）。
> **口径修正声明**：原始文档的四个结论（未编译、静默退出、BUSY 无重试、防重复无失败态）全部成立；本文件补的是 **HEAD 行号**、**额外静默点**、**两处数字/事实修正**（§6.2、§4.4），并把「原诊断的推测」与「已确认事实」分开。
>
> **判定标签**
> - 【已确认·源码】在本仓库 HEAD 静态核对；
> - 【已确认·DB】事故沙箱 DB 实证；
> - 【已确认·反汇编】事故二进制（featnew build）符号/地址级证据；
> - 【已确认·日志】`daemon.log` 原文；
> - 【已确认·实验】11-21 11:09 在线重放；
> - 【推断】由上述事实推出、但缺直接观测。

---

## 0. 结论先行

| # | 问题 | 结论 | 判定 | 一句话证据 |
| --- | --- | --- | --- | --- |
| 1 | 4 轮训练分析成功，为什么 0 提案？ | 翻译器被调用，但 `proposeForkForChain` 查 `blueprint_node` 得 0 行，`return nil` | 【已确认·DB+反汇编+实验】 | `blueprint_node` 全表 0；round 5 重放证明链路执行；`[rsi]` 零日志 |
| 2 | 为什么 0 行？ | 4 个目标**创建即 active**，没走 `Activate`；`compileBlueprintTx` 全仓唯一调用点在 `Activate` 内 | 【已确认·源码+反汇编】 | `goal.go:266-465` 无编译；`goal.go:2249` 唯一调用；无 `activated` 活动 |
| 3 | 为什么一条日志都没有？ | 翻译器有 **10 处无日志的 `return`**，命中哪一处都无声 | 【已确认·源码】 | `rsi_analyze.go:217/223/264/290/313/321/328/342/358/381` |
| 4 | opencode 批准后为什么永远不 done？ | approve 落库成功，异步 `MarkDelivered` 的 `UPDATE` 撞 `SQLITE_BUSY`，错误被 `Infof` 吞 | 【已确认·日志+源码】 | 日志原文 `mark delivered: database is locked (5)` |
| 5 | 为什么不能重批/打回？ | 防重复保护只看「最近 decision=approve」，无时间维度、无失败态旁路 | 【已确认·源码】 | `goal.go:1967-1977` |
| 6 | 为什么重启就恢复了？ | 启动补偿 `recoverPendingDelivers` 只在启动时跑一次，平时没有 ticker | 【已确认·源码】 | `daemon.go:475`；ticker 列表无 deliver |

**一句话**：这条链路上没有任何一个「单点错误」——是「创建路径不编译」「编译后空集的守卫/翻译器视为正常」「交付失败被静默」「防重复保护把静默当成进行中」四个**默认放行**叠在一起，构成了一个从外部完全观测不到的断链。

---

## 1. 版本与基线（为什么必须重新核行号）

| 项 | 事故运行版本 | 当前仓库 HEAD |
| --- | --- | --- |
| 构建来源 | `/tmp/agentwork-featnew`（已删除） | `a3d2744`（2026-11-21 15:05） |
| 版本串 | `0.1.0-featnew.20261119`，无 vcs stamp | git describe 可用 |
| module | `github.com/eushing/agentwork` | `github.com/eushing/agentwork` |
| blueprint 模块 | 有（反汇编可见 `compileBlueprintTx` / `draftAutoProposals`） | 有（`internal/service/blueprint.go`） |
| `SetBlueprint` 接线 | 未知（原始诊断候选 A 的疑点） | **已接线**：`cmd/agentwork-daemon/main.go:154` |
| 与本目录的关系 | 事故的「事实源」 | 引用行号的「代码源」 |

两个结论：

1. **当前源码比事故版本新，但缺陷仍在**：`Create` 不编译、`MarkDelivered` 无 busy 重试、duplicate guard 无失败态、`draftAutoProposals` 静默 return——逐项在 HEAD 复核通过（见 §7 对照表）。所以本目录不是「考古已修问题」，而是「同一个缺陷在当前树上仍可复现」。
2. **原始文档里的「工作区源码」已过时**：它写于 featnew 已删除、`/root/workspace/agentwork-src` 还没有 blueprint 模块的时刻；当时的候选 A/B/C 因此无法静态排除。本文件用 HEAD 代码把「未接线」排除（`main.go:154` 已接线），把根因收敛到「未编译 + 静默」。

> 事故二进制无法在本仓库复现（源码缺失）。本文件对它的引用只到符号/地址/字符串级别；行为结论一律有 DB 或日志支撑。

---

## 2. 问题一：蓝本自迭代为什么 0 提案

### 2.1 设计中的闭环（代码事实）

```text
Create(backlog)                                    goal.go:266
  └─ Activate                                      goal.go:2205
       ├─ UPDATE status backlog→active             goal.go:2215
       ├─ compileBlueprintTx                       goal.go:2249
       │    └─ blueprint_version.content → blueprint_node 物化   blueprint.go:1191-1432
       └─ enqueue owner run                        goal.go:2238
  → 执行 / 训练
  → approve → deliver → MarkDelivered(done)        goal.go:3233
  → OnGoalTerminal                                 goal.go:3281 / rsi.go:187
  → RSI Analyze                                    rsi_analyze.go:49
  → draftAutoProposals                             rsi_analyze.go:186 → 215
  → proposeForkForChain（serial_chain）             rsi_analyze.go:259
  → CreateProposal(pending)                        rsi_analyze.go:386 / blueprint.go:2948
  → 人工批准提案 → 新 draft 版本 → 下一轮
```

关键设计意图写在 `blueprint.go:20-29`：蓝本 spec 是「执行顺序的唯一权威」，「finalization guard 数 `blueprint_node`，有未 settle 节点就到不了 done」——也就是说，**编译是这套设计的地基**，没有 `blueprint_node` 的蓝本目标在语义上等于「自由编排 + 一张没生效的 SOP」。

### 2.2 E1：翻译器确实被调用了【已确认·实验+反汇编】

- HEAD 源码：`Analyze` 尾部**无条件**调用翻译器，调用前没有 finding 数量/类型的分支：

```go
// internal/service/rsi_analyze.go:184-187
s.draftAutoProposals(ctx, goalID, graph, findings)
return nil
```

- 反汇编（事故二进制）：`Analyze` 的 Publish 与 `draftAutoProposals`（`0x5528a0`）之间**没有任何分支门控**。
- 在线实验（11-21 11:09）：`PUT /logs/level → debug`，对 opencode 的会话 `07f2df9f…` 执行 `POST /rsi/sessions/{id}/retry` → **round 5 生成**（trigger=`manual_retry`，findings 正常复算）。分析管道从头跑到尾，翻译器在链路上。
- 同一次实验，debug 级别下 **`[rsi]` 日志 0 条**：成功日志 `[rsi] auto-proposal drafted for goal %s`、失败日志、cap 日志、validator 拒绝日志，全都没出现。

→ 排除「没触发/没调用」，问题在调用之后的某个**无日志分支**。

### 2.3 E2：10 处静默退出，任何一处命中都无声【已确认·源码】

事故文档定位了 3 处（服务未接线 / 节点映射为空 / 链长<2）。在 HEAD 逐行核对后，实际是 **10 处 `return` 没有任何日志**——这也是为什么「灌 DEBUG 重放」仍然一无所获：

| # | 位置（HEAD） | 条件 | 日志 | 事故版本对应 |
| ---: | --- | --- | --- | --- |
| 1 | `internal/service/rsi_analyze.go:216-218` | `s.blueprintSvc == nil`（RSI 未接线 blueprint 服务） | 无 | nil 检查 `0x5528d0`，裸 `ret` |
| 2 | `internal/service/rsi_analyze.go:221-224` | 查 `goal.blueprint_version_id` 出错或为空（infer goal） | 无 | SQL `0x89fb4c` |
| 3 | `internal/service/rsi_analyze.go:246-248` | finding 类别不是 `serial_chain` | 无 | 仅翻译一类 finding |
| 4 | `internal/service/rsi_analyze.go:263-265` | `criticalTaskChain` 链长 < 2 | 无 | 链长比较 `0x552e54` |
| 5 | `internal/service/rsi_analyze.go:290` | 执行图节点（`sg:<id>`）在 `blueprint_node` 里没有映射 | 无 | SQL `0x8ad026` |
| 6 | `internal/service/rsi_analyze.go:313` | 映射到的 spec 节点不是 `task` 类型 | 无 | — |
| 7 | `internal/service/rsi_analyze.go:321` | 链尾分叉到多个 end | 无 | — |
| 8 | `internal/service/rsi_analyze.go:328` | 链尾没有直接接 end 节点 | 无 | — |
| 9 | `internal/service/rsi_analyze.go:342` | brief 之间互相引用（fork 会破坏数据流） | 无 | — |
| 10 | `internal/service/rsi_analyze.go:358` / `381` | spec 形状与执行图不一致 / 标题去重命中 | 无 | — |

有日志的三处（对照）：

```go
// internal/service/rsi_analyze.go:232-237（cap）
log.Printf("[rsi] auto-proposal cap check failed: %v", err)
log.Printf("[rsi] auto-iteration cap %d reached — translator proposal skipped (rsi.max_auto_rounds)", cap)
// internal/service/rsi_analyze.go:386-397（成功 / validator 拒绝）
log.Printf("[rsi] auto-proposal drafted for goal %s: %s", goalID, title)
```

注意：这里用的是标准库 `log.Printf`，不是项目 `logging` 包；`/logs/level` 调的是 `logging` 的级别。**成功路径本来会打日志，与日志级别无关**——实验零输出，只能说明「没走到成功路径」，不能说明「日志被过滤」。

### 2.4 E3：DB 实证——四个目标从未编译【已确认·DB+日志】

| 查询 | 结果 | 含义 |
| --- | --- | --- |
| `SELECT count(*) FROM blueprint_node` | **0** | 全库没有任何目标物化过节点 |
| `SELECT id, blueprint_version_id FROM goal WHERE blueprint_version_id!=''` | 4 行（正是四个 train 目标） | 所有「绑定蓝本」的目标都未编译 |
| `grep -E "compile\|materialize\|spawn child" daemon.log` | **0 命中** | 编译代码从未执行 |
| `activity_log` 按 `actor/action` 分组 | 只有 `created → commented → entered_review → review_ready`，无 `activated` | 没有走激活端点 |
| finalization guard 日志 | 反复 `… blueprint=0 — no gate yet` | 平台守卫自己一直在说「没有蓝本节点」 |

guard 的日志模板在 HEAD：

```go
// internal/service/goal.go:1667
logging.Infof("goal: %q run %s done but finalization guard holds (sub-goals=%d changes=%d consults=%d ask-human=%d blueprint=%d) — no gate yet", ...)
```

这里有个容易误读的点：`blueprint=0` 在 guard 里**既是「没有待办节点」也是「根本没有节点」**——空集通过（vacuous truth）。guard 并不会校验「绑定了蓝本的目标是否编译过」，这正是 BUG-3 最隐蔽的一层（§3.2）。

### 2.5 E4：根因最上游——创建即 active 绕过了唯一编译入口【已确认·源码+反汇编】

`Create` 允许 `active`，只拒绝 `blocked/review`：

```go
// internal/service/goal.go:283-308
if g.Status == "" {
    g.Status = "backlog"
}
...
if g.Status == "blocked" || g.Status == "review" {
    return nil, NewValidationError("cannot create a goal in blocked/review status")
}
```

创建事务末尾，如果 `status=active` 且 assignee 是 agent/squad，就在**同一事务**里派发首跑：

```go
// internal/service/goal.go:451-457
if g.Status == "active" && (g.AssigneeType == "agent" || g.AssigneeType == "squad") {
    _, ev, err := s.enqueueOwnerIntentTx(ctx, tx, g.ID, g.AssigneeType, g.AssigneeID, g.Status, "", "", "active")
    ...
}
```

但 `Create` 里**没有** `compileBlueprintTx`。编译只在 `Activate`：

```go
// internal/service/goal.go:2213-2250
res, err := tx.ExecContext(ctx,
    `UPDATE goal SET status='active' WHERE id=? AND status='backlog'`, goalID)
...
if err := s.compileBlueprintTx(ctx, tx, goalID, &compiledEvs); err != nil {
    return nil, fmt.Errorf("compile blueprint: %w", err)
}
```

全仓调用点核对（HEAD）：

```text
$ git grep -n "compileBlueprintTx" HEAD -- '*.go'
internal/service/goal.go:2249:  if err := s.compileBlueprintTx(ctx, tx, goalID, &compiledEvs); err != nil {
internal/service/blueprint.go:1191: func (s *GoalService) compileBlueprintTx(...)

$ git grep -n "\.Activate(" HEAD -- '*.go'
internal/server/handler/handler.go:564:  out, err := h.Goal.Activate(r.Context(), r.PathValue("id"))
```

即：**编译函数只有一个调用方 `Activate`，`Activate` 只有一个调用方「手动启动」HTTP handler**（`handler.go:563-564`）。「创建即 active」把这条路整个跳过了。

事故反汇编与此一致【已确认·反汇编】：`compileBlueprintTx 0x50a4b0` 唯一调用方 `Activate 0x53907c`；`Activate` 唯一调用方 `activateGoal 0x68fcd4`；`Create` 的调用清单里没有编译。

四个目标的活动日志与派发时序（DB）：`created` 之后 **0.05–1s** 就出现 owner run，且没有 `activated`。这就是「创建即 active = 只盖章、不编译」。

### 2.6 E5：即使编译，这 4 个目标也未必有提案【已确认·源码】

翻译器只实现一条规则：

```go
// internal/service/rsi_analyze.go:246-248
for _, f := range findings {
    if f.Category != "serial_chain" {
        continue // Phase D translates exactly one finding category
    }
```

本次 5 条 findings 是 **3 条 `human_wait` + 2 条 `lost_parallelism`**，没有 `serial_chain`。所以：

- 若目标已编译：`draftAutoProposals` 会在 246 行 `continue` 掉全部 findings → 仍然 0 提案、但这次连「节点映射」都不会查（静默点 #3）。
- `human_wait` 在 Phase D **没有任何提案模板**；Web 文案却承诺「跑过几次绑定了本蓝本的任务后，训练分析会产出结构化建议」（`web/app/blueprints/[id]/page.tsx:541`）。

→ BUG-3 的修复必须分两层：**编译（让节点存在）** 和 **规则覆盖（让 `human_wait` 可被翻译）**。只修第一层，本次四任务仍会 0 提案。

### 2.7 为什么这会误导诊断方向

| 观测 | 容易得出的错误结论 | 实际 |
| --- | --- | --- |
| 0 提案 + 0 日志 | 「翻译器没被调用 / 服务没接线」 | 调用了，只是静默 return |
| guard 反复 `blueprint=0 — no gate yet` | 「蓝本节点都完成了」 | 一个节点都没有 |
| 训练分析全部 ready、findings 正常 | 「RSI 链路健康」 | 只健康到 `rsi_report`，下游断 |
| Web 空白提案页 | 「再多跑几轮就会有」 | 跑多少轮都不会，除非编译且 finding 是 `serial_chain` |

---

## 3. 问题二：创建即 active 在 HEAD 仍合法，且「空集」在三层都被放行

### 3.1 状态入口矩阵（HEAD）

| `Create` 传入 status | 接受？ | 编译？ | 首跑？ | 结果 |
| --- | --- | --- | --- | --- |
| 省略 / `backlog` | 是（`goal.go:283-285`） | 否（等 `Activate`） | 否 | 正常路径：手动启动时编译 |
| `active` | **是**（只拒 blocked/review，`goal.go:306-308`） | **否** | **是**（`goal.go:451-457`） | 本次事故路径：跑得像蓝本，实际未编译 |
| `blocked` / `review` | 否 | — | — | 显式拒绝 |
| `done` / `failed` / `cancelled` | 是（未校验） | 否 | 否 | 不属于本问题，但入口过宽 |

另外，定时任务走 `daemon.go:1583` 的 `INSERT … status='active'`，但该 INSERT **不写 `blueprint_version_id`**，所以不构成第二个「绑定蓝本但未编译」的来源。全仓 `INSERT INTO goal` 只有这两处（`goal.go:400`、`daemon.go:1583`）。

### 3.2 「空集通过」的三层

| 层 | 代码/位置 | 空集时的行为 | 可观测性 |
| --- | --- | --- | --- |
| 平台守卫 | `goal.go:1643-1647` + `pendingBlueprintNodesTx` | `pendingBP=0` → 视为无待办，继续走 done/gate | 只在 guard 日志里出现 `blueprint=0`，无法与「已全部完成」区分 |
| 翻译器 | `rsi_analyze.go:270-290` | 0 行映射 → 整条链 `return nil` | 零日志 |
| 产品 UI | `web/app/blueprints/[id]/page.tsx:541` | 显示「还没有提案」 | 与「已编译但无 finding」无法区分 |

这不是三个独立 bug，而是**同一个语义缺口的三次投影**：系统里没有「绑定蓝本的 goal 是否已编译」这个状态位，于是三层都只能看到「节点数=0」，并按「无节点=无事」处理。

### 3.3 修复选项对比

| 方案 | 做法 | 优点 | 风险/代价 | 结论 |
| --- | --- | --- | --- | --- |
| A | `Create` 在 `status=active && blueprint_version_id!=''` 时，事务内调用 `compileBlueprintTx`，提交后 `publishCompiledRuns` | 改动小；保持「创建即可跑」的现有 API 语义；与 `Activate` 共用幂等编译 | 两个编译入口，未来新增创建路径可能再漏；编译失败会让创建失败（需要明确错误语义） | **可接受**（工作树已有此方向补丁） |
| B | 一律落 `backlog`，由 `Activate` 统一编译 | 单一入口、语义最干净；「创建即 active」不再存在 | 破坏现有「创建即跑」的调用方（UI/CLI/导入）；需要迁移与兼容层 | **推荐**（根因文档建议） |
| C | A + 编译状态位（`goal.blueprint_compiled_at` 或 `blueprint_node` 非空断言），guard/翻译器/UI 都读它 | 可观测、可断言、可告警；未来新增入口也难漏 | 需要 schema 变更/回填；历史目标无法补 | **A 落地后作为加固** |

验收不依赖选项：**任何 `blueprint_version_id != ''` 的 goal，只要 `status` 进入 active，就必须满足 `SELECT count(*) FROM blueprint_node WHERE goal_id=? > 0`**；不满足则创建/激活失败并打 WARN。这条不变量可以直接写成回归测试。

### 3.4 追加：BUG-3 的补丁只加了一条入口，不变量仍未落地（BUG-4 边界）

工作树里的「Create(active) 编译」补丁解决了 §3.1 的第二条入口，但它**没有实现 §3.3 末尾那条验收不变量**：pinned 目标进入 active/开始执行/收尾时，仍没有任何一处断言 `blueprint_node > 0`。

- 终局守卫依旧只数 `pending/running`（`goal.go:1649` + `blueprint.go:1739`）：0 行 = 0 pending = 放行。
- 翻译器会因 BUG-3 的补丁打 WARN，但目标本身照样能跑完、进 review、done——**看得见的空转仍然是空转**。
- 未编译时的执行退化（owner contract 说「已 COMPILED」、`CreateSubGoal` 被拒、consult 只读）与「计划 vs 实际」对账缺口，另立 [findings-bug4](findings-bug4-plan-vs-actual.md)；事故数据包已实证：4 目标 0 个 subgoal run、17 个 consult（owner 26 / consult 17 / review 7，`change` 全 0），且事故 build 的 consult 是实际执行通道（Upgrader 266 calls 经 JEM API 改环境），不是 HEAD 文案里的只读。2026-11-21 用户复核又确认：**蓝本从未被读取**——顺序来自 AI SHELL 的 `system_prompt` 与 `squad.instructions`（staged `AGENTS.md`），`AGENTS.md`/评论/50 runs 工具调用里蓝本与节点 id 零出现；连 persona 第 1 步「建 sub_goal」也被模型静默跳过（4 goal sub_goal=0）而平台放行。用户同时决定 consult **只许提意见**、g1 的 `timeout.action=rejected` **非有意**。本节只标注边界，不重复其证据。

---

## 4. 问题三：批准 → 异步交付 → `SQLITE_BUSY` → 永久 review

### 4.1 状态机与故障点

```text
ResolveReview(approve)                          goal.go:1917
  ├─ 写 gate_decision（独立 Exec，立即提交）       goal.go:2007
  ├─ 发布 goal:approved                          goal.go:2101-2103
  └─ return（goal 仍 review）
        │
        ▼  (daemon goroutine)
onGoalApproved → go deliverGoal                  deliver.go:28-37
  ├─ scratch → finishDeliver(success=true)       deliver.go:110-113
  └─ repo   → merge/push → finishDeliver
        │
        ▼
MarkDelivered(success=true)                      goal.go:3233
  ├─ BeginTx                                     goal.go:3234
  ├─ UPDATE goal SET status='done' … WHERE status='review'   goal.go:3257
  │     └─ ✗ SQLITE_BUSY（本次）
  ├─ cancel queued runs                          goal.go:3261-3264
  ├─ Commit                                      goal.go:3272
  └─ publish goal:delivered                      goal.go:3275
        │
        ▼
finishDeliver: err != nil → logging.Infof(...)    deliver.go:330-332   ← 吞掉
```

### 4.2 代码证据

1）批准与状态翻转**不在同一事务**：

```go
// internal/service/goal.go:2007-2010 —— 独立 Exec，不在 tx 里
if _, err := s.st.DB().ExecContext(ctx,
    `INSERT INTO gate_decision (id,goal_id,run_id,gate_rule,decision,reason,decided_by,decided_at,review_duration) VALUES (?,?,?,?,?,?,?,?,?)`,
    ...); err != nil {
    return nil, fmt.Errorf("insert gate_decision: %w", err)
}
```

2）`MarkDelivered` 全流程**没有** busy 重试：

```go
// internal/service/goal.go:3233-3272（节选）
func (s *GoalService) MarkDelivered(...) (*Goal, error) {
    tx, err := s.st.DB().BeginTx(ctx, nil)
    ...
    if _, err := tx.ExecContext(ctx,
        `UPDATE goal SET status='done', handoff_note='', review_request='' WHERE id=? AND status='review'`, goalID); err != nil {
        return nil, fmt.Errorf("mark delivered: %w", err)
    }
    ...
    if err := tx.Commit(); err != nil {
        return nil, err
    }
}
```

3）仓库里已有 `withBusyRetry`，但 `MarkDelivered` 没用：

```go
// internal/service/goal.go:3353-3380
func isSQLiteBusy(err error) bool {
    var se *sqlite.Error
    if errors.As(err, &se) {
        return se.Code() == sqlite3.SQLITE_BUSY || se.Code() == sqlite3.SQLITE_BUSY_SNAPSHOT
    }
    return false
}

func withBusyRetry(fn func() error) error {
    var err error
    for attempt := 0; attempt < 3; attempt++ {
        err = fn()
        if err == nil || !isSQLiteBusy(err) {
            return err
        }
        time.Sleep(100 * time.Millisecond)
    }
    return err
}
```

调用点只有两处，都在 reconcile 家族：`goal.go:1367`、`goal.go:2619`。`MarkDelivered` 不在其中。

4）交付层吞错：

```go
// internal/daemon/deliver.go:324-333
func (d *Daemon) finishDeliver(ctx context.Context, goalID string, success bool, note string, commits ...[]string) {
    logging.Infof("daemon: deliver %s: %s", goalID, note)
    ...
    if _, err := d.goalSvc.MarkDelivered(ctx, goalID, success, note, fixCommits); err != nil {
        logging.Infof("daemon: MarkDelivered %s: %v", goalID, err)
    }
}
```

注意错误前缀：日志里的 `mark delivered:` 正是 `goal.go:3258` 对 `UPDATE` 错误的包装——说明失败发生在 **UPDATE 语句**，不是 commit。这是修复时的重要边界：重试必须覆盖「从 BeginTx 到 Commit」的整段，而不是只重试 commit。

5）SQLite 侧并非完全没有超时：

```go
// internal/store/store.go:51
dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
```

即连接层有 **5s** 的 busy 等待 + WAL（单写多读）。所以 BUG-1 不是「PRAGMA 没开」，而是：

- 锁被持有超过 5s（另有长写事务），或
- 事故 build 的 DSN/连接未带上该 PRAGMA（无法核），或
- 是 WAL snapshot 升级冲突（事务先读后写、期间别人提交；HEAD 注释 `goal.go:3347-3352` 明确说这类冲突**立即**返回 `SQLITE_BUSY`、`busy_timeout` 不生效，必须回滚重开事务；`isSQLiteBusy` 同时接受 5/517 也是为此）。

事故日志的错误码是 `(5) SQLITE_BUSY`，但 snapshot 冲突同样是码 5 且立即失败——**码值无法区分三种成因**，持锁者也没有直接证据。原始诊断称「同一秒 deepseek 也在交付」，而轨迹表显示 deepseek 的批准是 **09:06:53**（19 秒后），两者矛盾——争用者待核（§9 待核项 T3）。`MarkDelivered` 是「先 SELECT status（`goal.go:3240-3241`）、后 UPDATE（`:3257`）」的形状，与注释里那起 live failure 同构，因此 snapshot 冲突是更贴合代码的推断（但仍是推断）。无论哪种原因，**应用层重试都是必须的**，因为 5s 之后（或立即）错误仍然返回给了一个只会 `Infof` 的调用者。

### 4.3 永久卡死的机制

```text
approve 落库（gate_decision 已提交）
  → deliver 失败
    → MarkDelivered 返回 error，事务回滚
      → finishDeliver 只打 Infof，不写 review_request
        → goal 仍是 review，review_request 仍为空
          → 防重复保护看到「最新 decision=approve + runID 相同 + 无 deliver: 注记」
            → 之后所有 approve/reject 全部拒绝（goal.go:1967-1977）
              → 恢复只有启动时一次（daemon.go:475），进程不重启就永远不跑
```

对比「设计的失败路径」：如果 `MarkDelivered(success=false)` 被执行，它会把失败原因写进 `review_request`（`goal.go:3266-3271`）；guard 看到 `deliver:` 前缀会放行重批/打回（`goal.go:1946`）。**本次失败发生在 `success=true` 的 `MarkDelivered` 内部，根本没有走到 `success=false` 分支**——于是系统失去了唯一能标记失败的机制。

### 4.4 自愈与时间账

启动补偿的判定条件（`deliver.go:51-60`）：

```sql
SELECT g.id FROM goal g
 WHERE g.status='review'
   AND g.review_request NOT LIKE 'deliver:%'
   AND EXISTS (
     SELECT 1 FROM gate_decision d
     WHERE d.goal_id = g.id AND d.decision='approve'
       AND d.decided_at = (SELECT MAX(decided_at) FROM gate_decision WHERE goal_id = g.id)
   )
```

它只在启动时调用一次（`daemon.go:475`），主循环的 ticker 列表（`daemon.go:480-486`：dispatch / schedule / stale / cleanup / digest / issue / runaway）**没有 deliver 恢复**。所以本次靠 `kill 12382 → 看门狗 09:56:22 拉起 21302 → 补偿自愈`。

精确时长：09:06:34 → 09:56:22 = **49m48s**。「约 50 分钟」的所有原始表述都成立；「opencode 16h29m」则是重复计入（见 [timeline.md](timeline.md) §0）。

### 4.5 修复方案（BUG-1）

| 步骤 | 改动 | 说明 |
| --- | --- | --- |
| 1 | `MarkDelivered` 的整段（BeginTx→SELECT→UPDATE→cancel queued→Commit）包进 `withBusyRetry`，**每次重试重新 BeginTx** | 现有 helper 的注释明确要求「调用方事务必须是条件迁移、失败即回滚」；`MarkDelivered` 正是这种（`UPDATE … WHERE status='review'`），语义安全。必须重开事务，否则 snapshot 冲突无法消失。 |
| 2 | UPDATE 的 `RowsAffected()==0` 视为「已交付/已离开 review」，**不要**再 publish `goal:delivered` 或 `OnGoalTerminal` | HEAD 当前不检查 0 行（`goal.go:3257` 后直接往下），重复/并发交付会重复发事件。`OnGoalTerminal` 对 `status='analyzing'` 也会再次放行（`rsi.go:203` 的 `IN ('waiting','analyzing')`），会重复 `go runAnalysis`；`Analyze` 虽幂等重建报告，但每跑一次就多插一轮 `rsi_round`（`rsi_analyze.go:168`），重复事件会污染轮次趋势——所以事件层必须去重。 |
| 3 | `finishDeliver` 在 `MarkDelivered` 返回 error 时：`Errorf` 记录，并尝试 `MarkDelivered(false, "deliver: …")` 落失败注记；仍失败则至少写一条 `deliver_failed` 活动/日志 | 让 guard 的 `deliverFailed` 旁路有东西可读；避免「静默失败」。 |
| 4 | `recoverPendingDelivers` 增加周期 ticker（如 10 分钟），并带 `minAge`（如 2 分钟）避免与正常交付竞态 | 消除对重启的依赖；`minAge` 用 `gate_decision.decided_at` 过滤。 |

验收（必须自动化）：

- 注入 BUSY 的测试：第一次 `MarkDelivered` 返回 BUSY，重试后 done；断言只发一次 `goal:delivered`。
- 0 行路径：模拟重复交付，断言不重复发事件、不重复 `OnGoalTerminal`。
- 恢复路径：构造「approve 已落库 + status=review + 无 deliver:」，不重启进程，等待一个 ticker 周期后 done。
- 回归：deepseek/minimax/openclaw 的正常路径（scratch 当秒交付）时长不变。

---

## 5. 问题四：防重复审批保护没有「失败态」和「时间」两个维度

### 5.1 代码

```go
// internal/service/goal.go:1931-1977（节选）
if g.Status != "review" {
    logging.Warnf("review: goal %q decision=%s REJECTED (goal is %s, not review)", ...)
    return nil, NewCodedError(CodeGoalNotInReview, "goal is not in review")
}
// Duplicate-decision guard: the deliver step runs ASYNC after an approve,
// and the goal stays in review until it finishes — a second decision in
// that window would race the merge. Both directions are guarded:
//   - re-approve: the human clicking again (the page shows no feedback
//     while deliver runs) would pile up gate_decision rows.
//   - reject after approve: the goal would go back to active + a new run
//     while the deliver may already have pushed …
// EXCEPTION: a FAILED deliver annotates review_request ("deliver: ...")
deliverFailed := strings.HasPrefix(g.ReviewRequest, "deliver:")
handoffLoopPark := strings.HasPrefix(g.ReviewRequest, "handoff_loop:")
authorizePark := strings.HasPrefix(g.ReviewRequest, "authorize:")
if decision == "approve" || decision == "reject" || decision == "redirect" {
    var lastDecision, lastRunID string
    err := s.st.DB().QueryRowContext(ctx,
        `SELECT decision, run_id FROM gate_decision WHERE goal_id=? ORDER BY decided_at DESC LIMIT 1`, goalID).
        Scan(&lastDecision, &lastRunID)          // ← 没有取 decided_at
    if err == nil && !deliverFailed && !handoffLoopPark && !authorizePark && lastRunID == runID {
        if decision == "approve" && lastDecision == "approve" {
            logging.Warnf("review: goal %q decision=%s REJECTED (already approved — deliver in flight or its result pending)", ...)
            return nil, NewValidationError("goal already approved — waiting for the deliver step (or check the deliver result)")
        }
        if (decision == "reject" || decision == "redirect") && lastDecision == "approve" {
            logging.Warnf("review: goal %q decision=%s REJECTED (being delivered — available after the deliver finishes or fails)", ...)
            return nil, NewValidationError("goal is being delivered — reject is available after the deliver finishes or fails")
        }
    }
}
```

### 5.2 三个结构性缺陷

| # | 缺陷 | 后果 |
| --- | --- | --- |
| 1 | **把「approve 已落库」等同于「交付进行中」** | 交付已经失败但没写 `deliver:` 注记时（BUG-1 的形态），保护会无限期拒绝 |
| 2 | **查询不取 `decided_at`，没有时间维度** | 13.4 小时前的 approve 与 1 秒前的 approve 无法区分；只要进程不重启，永远拒绝 |
| 3 | **拒绝只返回字符串，无状态码/无失败态** | 前端只能显示 "goal already approved"；用户无法知道该等、该重试、还是该找运维（设计缺陷-4/O-2） |

机制上，guard 依赖的唯一失败信号是 `review_request` 的 `deliver:` 前缀，而这个前缀由 **`MarkDelivered(success=false)`** 写入。BUG-1 的失败发生在 `success=true` 的调用内部：`UPDATE` 失败 → 返回 error → 事务回滚 → `finishDeliver` 吞错。**能标记失败的路径没被走到，guard 要检测的失败因此永远不可见**——两个 bug 互为放大器。

### 5.3 事故现场

| 时间 | 点击 | 结果 |
| --- | --- | --- |
| 09:06:34 | 首次 approve | 成功落库；交付 BUSY 失败 |
| 09:07:01 | 第 2 次 approve | `REJECTED (already approved — deliver in flight or its result pending)` |
| 09:09:09 | 第 3 次 approve | 同上 |
| 09:17:59 | 第 4 次 approve | 同上（距首次 11m25s；判据仍是「最近一次 decision=approve」） |

期间 **reject 同样被拒**（`goal.go:1975-1977`）——用户既不能重批也不能打回，只能等或重启。

### 5.4 修复方案（BUG-2）

| 步骤 | 改动 | 说明 |
| --- | --- | --- |
| 1 | guard 查询同时取 `decided_at`；若 `decision=approve && 同一 runID && 无 deliver: 注记 && now-decided_at >= grace`（建议 5 分钟，与 `deliverWaitForRuns` 对齐）→ **不拒绝**，而是**重发 `goal:approved`**（重试交付），返回当前 goal（仍 review） | 重发是幂等的（`deliverGoal` 的 merge/push 幂等，scratch 直通 `MarkDelivered`）；不写第二条 `gate_decision`，保持审计干净 |
| 2 | grace 内仍拒绝，但错误信息区分「交付中（请稍候）」与「交付失败（可重试/打回）」；有 `deliver:` 前缀时走现有例外 | 解决 O-2；前端可按错误码渲染 |
| 3 | `reject` 在「approve 超过 grace 且无失败注记」时**仍拒绝**（交付可能真的还在跑，打回会与 merge 竞态）；一旦 `deliver:` 注记出现，按现有例外放行 | 保守但正确；把「允许打回」绑定到「明确的失败证据」，而不是时间 |
| 4 | 交付失败时主动写 `deliver:` 注记（BUG-1 步骤 3）+ 通知（现有 feishu/webhook 链路） | 让 guard 的旁路真正可用 |

为什么 grace 用「分钟」而不是「秒」：repo 域交付要 merge + push，`deliverWaitForRuns` 允许等 5 分钟（`deliver.go:41`）；grace 必须大于「最慢的正常交付」才安全。推荐 2 分钟只适用于 scratch/幂等重发，repo 域建议 5 分钟或按 domain 类型区分。

验收矩阵：

| 场景 | 期望 |
| --- | --- |
| 交付进行中（< grace）重批 | 拒绝，文案="交付中，请稍候" |
| 交付失败但无注记（BUG-1 形态，> grace） | 重发 `goal:approved`，不新增 `gate_decision`，交付完成后 done |
| 交付失败且有 `deliver:` 注记 | approve 重试交付；reject 打回 agent（现有例外） |
| 同一秒连点两次 | 第一次正常；第二次按 grace 拒绝，不产生第二条 decision |
| 重复 `goal:approved` 事件 | 只产生一次 done 事件（依赖 BUG-1 的 0 行修复） |

---

## 6. 问题五：`human_wait` 口径——事故 build 与 HEAD 不一致

### 6.1 两套计算路径（HEAD）

| 路径 | 代码 | 口径 |
| --- | --- | --- |
| 单 goal（RSI 报告 / workflow 图） | `internal/service/workflow.go:409-413` | `SELECT SUM(review_duration)*1000 FROM gate_decision WHERE goal_id=?`；`share = wait/(wall+wait)`。这解释了 82%/37%/4.2%/91%。 |
| 全局 overview | `internal/service/rsi.go:512` | `SELECT SUM(review_duration)*1000 FROM gate_decision` + 可选时间过滤；**全库**，不按 goal 分。 |
| 时长来源 | `internal/service/goal.go:1990-2005` | 从最近一次 `entered_review / requested_review / parked_review / review_ready` 起算到本次决策。 |

### 6.2 冲突事实

| 事实 | 数值 | 来源 |
| --- | --- | --- |
| 事故 build 的 openclaw `human_wait` | **362s（4.2%）** | DB/RSI 报告 |
| openclaw 真实人工等待 | ≈12.6h（20:35:39 → 09:08 之后） | 活动日志 |
| HEAD 上 authorize approve 的路径 | 先写 `gate_decision`（`goal.go:2007`），`duration` 从 `entered_review` 起算（`goal.go:1996`），因此 **12.6h 会被计入** | 源码 |
| HEAD 上 authorize approve 的状态翻转 | 同步 `review→active` + 唤醒 owner（`goal.go:2048-2096`），**不发布** `goal:approved`、不走 deliver | 源码 |

两个可能解释（都未证实，需 featnew 源码或原 DB 复核）：

1. 事故 build 的 `ResolveReview` 对 `authorize:` park 不写 `gate_decision`（或 `review_duration=0`）；HEAD 后来统一成「所有决策都记一行」。
2. 人工的第一次「批准/恢复」走的不是 `ResolveReview`，而是一个不落 `gate_decision` 的唤醒端点（例如直接恢复 run），所以 12.6h 没进入任何 decision 行。

无论哪种，**「事故 build 的 4.2%」不能直接用来评价 HEAD 的指标**；而「人类等待被低估」这一结论本身（O-3）在两种解释下都成立——区别是：HEAD 可能已经把授权等待算进去了，只是当时的数据没有。

### 6.3 影响与修复

- 对蓝本优化的影响：openclaw 从「最顺、0 findings」变成「84% 人工等待」，会直接改变 RSI 的优先级；四任务整体等待从 28.9h 变成 ≈41.5h。
- 修复建议：把「授权暂停 / handoff 暂停 / ask-human 暂停」与「评审闸门」拆成 `human_wait_by_kind`（或 `park_kind` 维度），UI 同时显示「评审等待」与「其他人工等待」；不要用一个 `SUM` 掩盖语义差异。
- 保留原口径：`/rsi/overview` 的 `p95_lead_time_ms ≈ 15.65h` 与各任务端到端时长相符（O-4），说明端到端口径是准的，失真的只是 `human_wait` 的分解。

---

## 7. 修复对照表（HEAD → 方案）

| ID | HEAD 事实 | 缺陷判定 | 修复 | 回归/验收 |
| --- | --- | --- | --- | --- |
| BUG-1 | `MarkDelivered` 无 busy retry（`goal.go:3233-3272`）；`finishDeliver` 吞错（`deliver.go:330-332`）；恢复只在启动（`daemon.go:475`） | **存在** | 整段 `withBusyRetry` + 重开事务；0 行视为已完成；失败落 `deliver:` 注记；周期恢复 ticker | 注入 BUSY；不重启恢复；事件只发一次 |
| BUG-2 | guard 不取 `decided_at`、无失败态、reject 也被禁（`goal.go:1956-1977`） | **存在** | 时间维度 + grace 重发 `goal:approved`；分态文案；reject 仍保守 | §5.4 矩阵 |
| BUG-3 | `Create` 不编译（`goal.go:266-465`）；编译唯一入口 `Activate`（`goal.go:2249`）；翻译器 10 处静默 return（`rsi_analyze.go:217-381`）；未编译无 WARN | **存在** | 创建即 active 时编译（或一律 backlog）；翻译器补 Debugf/Warnf；`human_wait` 模板；「绑定蓝本但 0 节点」的 WARN | 新目标走 backlog→激活，`blueprint_node>0`，approve 后出现 `[rsi] auto-proposal drafted` |
| BUG-4 | 无「pinned ⇒ 已编译」不变量（`goal.go:1649` + `blueprint.go:1736-1744`）；未编译时 contract 说「已编译」（`team_context.go:107`、`:304-329`）；pinned 目标拆活被拒（`subgoal.go:88-95`）；consult 在 HEAD 仅 prompt/worktree 约定（`daemon.go:1554-1567`、`:1828`），事故 build 却被当作执行通道；**蓝本从未被读取**（顺序来自 `agent.system_prompt` + `squad.instructions`，persona 第 1 步建 sub_goal 被模型跳过）；gate 的 auto 直通不可表达（`blueprint.go:1525-1545`，condition 只读 goal 字段 `:1301-1305`；`timeout` 必填 `:444-455`，用户确认事故 `rejected` 非有意）；spawn children 不入 ledger；无 plan-vs-actual 对账 | **已修复（工作树，未提交）**：不变量（Create/Activate/终局守卫）、contract 说真话、dispatch 懒编译、对账端点+RSI finding+UI 卡、consult 工具层只读、gate timeout 默认 park、gate `auto` 运行时判定、owner 写入归户（`Blueprint-Node:` trailer）、spawn children 入 ledger、提案→下一轮显式入口、scratch `env_change` 账本；事故数据已实证 26/17/7 runs、0 subgoal run、0 change、17 consult response 全空 | 不变量 + contract 说真话 + 对账端点/finding + owner 写入归户 + spawn 进 ledger + gate auto + timeout 默认 park + consult 工具层只读白名单（用户已决策 consult 只许提意见；实现见 findings-bug4 §5） | 见 [findings-bug4](findings-bug4-plan-vs-actual.md) §5–§6 与 [execution-report](execution-report.md) |
| 设计缺陷-4 | 源码注释自认 "the page shows no feedback"（`goal.go:1938`） | 存在 | goal 卡片「交付中」过渡态（监听 `goal_delivered`/`deliver_failed`） | UI 手测 |
| 观察项-5 | `event seq gap … lost on the wire` | 未复现 | machine 事件协议侧重号/乱序排查 | 复现日志 |

> 工作树里的并行修复（未提交）已朝 BUG-1/2/3 方向改动：`deliver.go` 增加带 `minAge` 的恢复与周期 ticker、`Errorf`；`goal.go` 在 `Create` 中对 active 目标编译、guard 用 grace 重发、`MarkDelivered` 用 `withBusyRetry`。**这些改动不是本目录的基线**，最终以 [execution-report.md](execution-report.md) 为准；行号合并后会漂移。

---

## 8. 遗留与取舍

1. **历史目标无法补编译**：编译发生在激活时，四个目标已 done；要验证「编译后会不会出提案」，必须新建目标走「backlog → 手动启动」。这不是修复缺陷，是数据事实。
2. **修 BUG-3 的编译层，本次四任务仍会 0 提案**：它们的 findings 是 3×`human_wait` + 2×`lost_parallelism`，翻译器只认 `serial_chain`。所以「自迭代闭环恢复」需要同时做「规则扩容」（至少 `human_wait`），否则修完仍然静默（只是这次会在 246 行 `continue` 掉）。
3. **guard 的「空集通过」是一个设计取舍**：没绑定蓝本的 goal 本来就不该有节点；用 `blueprint_node` 数量做断言会误伤 infer goal。安全的做法是**只在 `blueprint_version_id != ''` 时断言**（方案 C 的状态位/断言），而不是全局要求节点 > 0。
4. **`human_wait` 的语义边界**：评审闸门等待与授权/唤醒等待是两种人工成本，混在一个 SUM 里会让「优化蓝本」与「优化交互」两个方向无法区分。建议拆维度，而不是简单改分母。
5. **事故 build 不可复现**：featnew 源码缺失，BUG-1 的「持锁者」、O-3 的口径差异都只能靠原 DB 复算或找回源码；本仓库能确认的是**当前 HEAD 的同构缺陷**与其修复点。
6. **不修的部分**：`event seq gap`（观察项-5）与「批准后 UI 无过渡态」（设计缺陷-4）不在本次代码修复范围；前者需协议侧证据，后者是前端改动，已在 README 修复清单里保留。
7. **BUG-4 未修（2026-11-21 用户决定）**：用户观察到「真实执行图完全不是按规范图来执行，都是起咨询接口」，要求先把缺口写清楚再动代码。事故数据包（incident-data.zip）已到，findings-bug4 §1 已用其补齐实证（4 目标 50 runs = owner 26 / consult 17 / review 7，0 subgoal run，0 change；17 consult_request response 全空；g1 从未触发；Upgrader 经 JEM API 真实执行），但本轮**仍不改编译/守卫/contract**。注意 §3.4 的「不变量」与 BUG-4 的 P0 修复是同一件事——BUG-3 的补丁若只停在入口，验收项仍不完整。
8. **蓝本零读取与「prompt 不可靠」的结论（2026-11-21 用户复核）**：顺序来自 AI SHELL 的 `system_prompt` + `squad.instructions`（staged `AGENTS.md`），蓝本 v1 只是同一 SOP 的结构化副本；`AGENTS.md`/评论/50 runs 工具调用里蓝本与节点 id 零出现。更强的一点：persona 明确要求「第 1 步 create sub_goal」，4 个 goal 仍全 0 行，平台无拦截。**因此任何执行保证都不能依赖 prompt**——这直接支撑 BUG-4 的 P0 断言与 consult 工具层白名单。用户已确认 consult 只许提意见、g1 `rejected` 非有意；scratch 交付进账本的影响与建议见 findings-bug4 §三.12。

---

## 9. 附录

### A. 代码位置速查（HEAD a3d2744）

| 主题 | 位置 |
| --- | --- |
| `Create` 入口 / 默认 backlog / 状态校验 | `internal/service/goal.go:266` / `:283-285` / `:306-308` |
| 创建事务 / INSERT（含 `blueprint_version_id`） / active 首跑 / commit / publish | `internal/service/goal.go:389` / `:400-402` / `:451-457` / `:458` / `:463-465` |
| `Activate` / backlog→active / 编译 / 提交 / 发布 | `internal/service/goal.go:2205` / `:2215` / `:2249` / `:2252` / `:2255-2258` |
| `compileBlueprintTx` / 空 pin 跳过 / 已有节点跳过 | `internal/service/blueprint.go:1191` / `:1201-1202` / `:1203-1212` |
| `MarkDelivered` / BeginTx / UPDATE done / cancel queued / 失败注记 / commit / publish / RSI hook | `internal/service/goal.go:3233` / `:3234` / `:3257` / `:3261-3264` / `:3266-3271` / `:3272` / `:3275` / `:3281-3282` |
| busy 判定 / 重试 helper / 现有调用点 | `internal/service/goal.go:3353` / `:3366` / `:1367`、`:2619` |
| `ResolveReview` / 状态前置 / guard 注释 / 三类 park 例外 / 查询 / 条件 / 拒绝文案 | `internal/service/goal.go:1917` / `:1931` / `:1935-1955` / `:1946`、`:1949`、`:1955` / `:1956-1960` / `:1967` / `:1972-1977` |
| `review_duration` 起点 / `gate_decision` 写入 / `goal:approved` | `internal/service/goal.go:1990-2005` / `:2007` / `:2101-2103` |
| authorize approve 同步释放 | `internal/service/goal.go:2048-2096` |
| 交付订阅 / scratch 直通 / 吞错 / 启动恢复 | `internal/daemon/deliver.go:28-37` / `:110-113` / `:330-332` / `:51-82` |
| 启动恢复调用 / 主循环 ticker | `internal/daemon/daemon.go:475` / `:480-486` |
| 翻译器调用 / 函数 / 静默点 / 仅 serial_chain / 链 / 节点映射 / CreateProposal / 成功日志 | `internal/service/rsi_analyze.go:186` / `:215` / `:217-381`（10 处） / `:246-248` / `:259` / `:270-290` / `:386` / `:397` |
| RSI 接线 / overview human_wait / per-goal human_wait | `cmd/agentwork-daemon/main.go:154` / `internal/service/rsi.go:512` / `internal/service/workflow.go:409-413` |
| SQLite DSN（WAL + busy_timeout=5000） | `internal/store/store.go:51` |
| guard 日志模板 / 空集条件 | `internal/service/goal.go:1667` / `:1643-1647` |
| 提案空状态文案 | `web/app/blueprints/[id]/page.tsx:541` |
| BUG-4：蓝本节点「释放」/ 依赖 gate / brief 解析 | `internal/service/subgoal.go:1570` / `internal/service/blueprint.go:1767` |
| BUG-4：owner contract 的「已编译」判定 / 原文 | `internal/daemon/team_context.go:107` / `:304-329` |
| BUG-4：pinned goal 拒绝拆活 | `internal/service/subgoal.go:88-95` |
| BUG-4：consult/review 只读 worktree / 只读角色 | `internal/daemon/daemon.go:1554-1567` / `:1828` |
| BUG-4：spawn 弹性物化 / children 非 ledger 行 | `internal/service/blueprint.go:1840-1920` / `goal.go:1651-1655` |
| BUG-4：实际图构建（含 consult/review 节点与边） | `internal/service/workflow.go:124-146`、`:326-374` |
| BUG-4：计划/实际并排 UI | `web/app/goals/[id]/page.tsx:226-250` |
| BUG-4：spawn children 的 schema 预留（`parent_key`/`node_key`） | `internal/store/schema.sql:273-284` |
| 表结构 | `internal/store/schema.sql:273`（blueprint_node）、`:295`（blueprint_proposal）、`:509`（gate_decision）、`:679`（training_session）、`:722`（rsi_report）、`:763`（rsi_round） |

### B. 证据清单

| 证据 | 内容 | 位置 |
| --- | --- | --- |
| DB-1 | `SELECT count(*) FROM blueprint_node` → 0 | 事故沙箱 DB（原始文档 §五） |
| DB-2 | 17 goals 中仅 4 个 train 目标 `blueprint_version_id` 非空 | 同上 |
| DB-3 | 4 个目标的 `activity_log` 无 `activated` | 同上 |
| DB-4 | `gate_decision`：opencode approve `review_duration=48283`（09:06:34） | 同上 |
| LOG-1 | `MarkDelivered ceb804a0…: mark delivered: database is locked (5) (SQLITE_BUSY)` | daemon.log |
| LOG-2 | 三条 `decision=approve REJECTED (already approved — deliver in flight or its result pending)` | daemon.log 09:07:01 / 09:09:09 / 09:17:59 |
| LOG-3 | `replaying pending deliver for ceb804a0…` + `无仓库交付——产物在项目目录` | daemon.log 09:56:22 |
| EXP-1 | debug 级别 + retry → round 5（trigger=manual_retry），`[rsi]` 零日志 | 11-21 11:09 在线实验 |
| DIS-1 | `draftAutoProposals 0x5528a0`、`proposeForkForChain 0x552e10`、`compileBlueprintTx 0x50a4b0`、`Activate 0x53907c`、`activateGoal 0x68fcd4`，及其调用关系 | 事故二进制反汇编 |
| DIS-2 | nil 检查 `0x5528d0`、链长比较 `0x552e54`、节点 SQL `0x8ad026` | 同上 |

### C. 待核项

| # | 待核 | 为什么重要 | 建议核法 |
| --- | --- | --- | --- |
| T1 | 事故 build 的 `authorize` 决策是否落 `gate_decision`（O-3 的 362s vs HEAD 会算 12.6h） | 决定 §6 采用哪种口径解释 | 找回 featnew 源码；或对原 DB 执行 `SELECT decision,decided_at,review_duration FROM gate_decision WHERE goal_id='b86244c3…'` |
| T2 | 轨迹表 run 数与 RSI run 数差额（12/14/10 vs 13/15/11） | 只影响明细完整性，不影响总账 | 对原 DB 跑 `SELECT id,role,started_at,finished_at FROM run WHERE goal_id=?`，补列缺失 run |
| T3 | BUG-1 的持锁者（原始诊断称「同秒 deepseek 交付」，轨迹表显示 deepseek 批准在 19 秒后） | 决定是「锁超 5s」还是「snapshot 冲突」，影响重试是否必须重开事务 | 原 DB 的 `run`/`activity_log` 秒级对照；或复现两次并发交付 |
| T4 | 事故 build 是否带 `busy_timeout(5000)` DSN | 区分「PRAGMA 生效但仍超时」与「build 无 PRAGMA」 | `strings` 二进制查 DSN 模板，或查事故进程启动环境 |
| T5 | opencode 真实创建→done 是 15h39m 还是 16h29m | 影响「四任务总历时」叙事 | 以 DB `created_at`/`delivered_at` 复算（本目录取 15h39m，理由见 timeline §0） |