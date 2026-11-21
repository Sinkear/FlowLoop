# BUG-1 分析：交付收口撞 `SQLITE_BUSY` 无重试，一次失败永久停在 review（findings-bug1-deliver-sqlite-busy）

> **范围**：`approve → goal:approved → 异步 deliver → MarkDelivered` 这条收口链路；2026-11-21 09:06:34 opencode（`ceb804a0d2ca59a0bc1cf59cdb9c3a2b`）交付失败与 49m48s 停摆的代码机制。
> **不覆盖**：merge/push 的 Git 正确性（`deliver.go` 119-303）、`recoverPendingDelivers` 的查询语义是否正确（只讨论触发时机与幂等性边界）、事件总线本身的可靠性（观察项-5 另文）。
> **代码基线**：本仓库 `git HEAD a3d2744`。事故运行版本构建自已删除的 `/tmp/ouroflow-featnew`（`0.1.0-featnew.20261119`），源码不可得；事故行为以 `daemon.log` / DB 为准，代码位置以 HEAD 为准。
> **判定约定**：每条结论都给出「HEAD 是否已修」。工作树里有并行修复补丁（`deliver.go`/`goal.go` 未提交），**不作为已修证据**；合并状态以 [execution-report.md](execution-report.md) 为准。

## 事故摘要

| 时间（+08:00） | 事件 | 证据 |
| --- | --- | --- |
| 09:06:34 | 人工 APPROVE opencode：`ResolveReview` 写 `gate_decision`（`review_duration=48283`，即 13.4h）并发布 `goal:approved` | DB `gate_decision`；`activity_log` |
| 09:06:34 | daemon 的 deliver goroutine 调 `MarkDelivered(success=true)`，`UPDATE goal … WHERE status='review'` 返回 `database is locked (5) (SQLITE_BUSY)` | `daemon.log` 原文 |
| 09:06:34–09:56:22 | goal 停在 `review`；`review_request` 为空；无失败注记 | DB `goal.status='review'`、`review_request=''` |
| 09:07:01 / 09:09:09 / 09:17:59 | 三次重批全部被防重复保护拒绝（另见 BUG-2） | `daemon.log` ×3 |
| 09:56:20 | 运维 `kill` daemon（pid 12382） | 运维记录 |
| 09:56:22 | 看门狗拉起 pid 21302；启动补偿 `recoverPendingDelivers` 重放交付 → `goal:delivered` → done；round 4 延迟密封 | `daemon.log`：`replaying pending deliver …` |

日志原文（其 `mark delivered:` 前缀对应 `goal.go:3258` 的 `fmt.Errorf`，说明失败发生在 **UPDATE 语句**，不是 commit）：

```text
2026-11-21T09:06:34+08:00 daemon: MarkDelivered ceb804a0d2ca59a0bc1cf59cdb9c3a2b:
    mark delivered: database is locked (5) (SQLITE_BUSY)
```

## 一页结论（按影响排序）

| ID | 级别 | 结论 | HEAD 是否已修 |
| --- | --- | --- | --- |
| P0-1 | 严重 | `MarkDelivered` 从 `BeginTx` 到 `Commit` **没有任何 busy 重试**；一次 `SQLITE_BUSY` 就回滚，而批准已提交 → goal 永久停在 review | **否**（`goal.go:3233-3272`） |
| P0-2 | 严重 | 唯一补偿 `recoverPendingDelivers` **只在启动时调用一次**，主循环没有 deliver 恢复 ticker，也没有 `minAge`/in-flight 去重 → 不重启就永远不恢复 | **否**（`daemon.go:475`、`:480-486`） |
| P1-1 | 中 | 失败被 `logging.Infof` 吞掉，且**不写 `deliver:` 失败注记**；guard 唯一认的失败信号因此永远不出现 | **否**（`deliver.go:330-332`） |
| P1-2 | 中 | 仓库其实已有 `withBusyRetry`（3 次 × 100ms），但只用于 reconcile（`goal.go:1367` / `:2619`），`MarkDelivered` 没用 | **否**（`goal.go:3366-3380`） |
| P1-3 | 中 | successful UPDATE **不检查 `RowsAffected==0`**：重复/并发交付仍会 `publish goal:delivered` + `OnGoalTerminal`，每次多插一轮 `rsi_round`，污染趋势 | **否**（`goal.go:3257` 后直接往下） |
| P2 | 低 | 连接层 `busy_timeout(5000)` + WAL 已存在，所以「加超时」不是修复；5s 后仍 BUSY 时应用层必须重试，且 snapshot 升级冲突下 busy handler 根本不生效 | **否**（`store.go:47-51`；无应用层重试） |

**一句话**：批准是**同步提交**的，交付是**异步、无重试、错误被吞**的；两者之间没有事务，也没有补偿（除重启）。这不是「SQLite 偶尔锁一下」，而是**一次锁冲突就等价于一次静默丢交付**。

## 逐项分析

### P0-1 一次 BUSY = 永久 review【已确认·源码+日志】

`MarkDelivered` 的事务体（HEAD）：

```go
// internal/service/goal.go:3233-3272（节选）
func (s *GoalService) MarkDelivered(ctx context.Context, goalID string, success bool, note string, fixCommits []string) (*Goal, error) {
    tx, err := s.st.DB().BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    ...
    if success {
        if _, err := tx.ExecContext(ctx,
            `UPDATE goal SET status='done', handoff_note='', review_request='' WHERE id=? AND status='review'`, goalID); err != nil {
            return nil, fmt.Errorf("mark delivered: %w", err)     // ← 事故命中这里
        }
        if _, err := tx.ExecContext(ctx,
            `UPDATE run SET status='cancelled', ... WHERE goal_id=? AND status='queued'`, goalID); err != nil {
            return nil, fmt.Errorf("cancel queued runs on delivered: %w", err)
        }
        ...
    }
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    ...
}
```

失败链（每一步都在 HEAD 可核对）：

1. `ResolveReview` 先把 `gate_decision` 用**独立 `Exec`** 写入并立即提交（`goal.go:2007`），再发布 `goal:approved`（`goal.go:2101-2103`）；
2. daemon 收到事件，`go deliverGoal(...)`（`deliver.go:28-37`），scratch 域直接 `finishDeliver(success=true, "无仓库交付…")`（`deliver.go:111-113`）；
3. `finishDeliver` 调 `MarkDelivered`（`deliver.go:330`）；
4. UPDATE 撞 BUSY → 函数返回 `fmt.Errorf("mark delivered: %w", err)` → `defer tx.Rollback()`；
5. `finishDeliver` 只打 `Infof`（`deliver.go:331-332`），**不重试、不写注记、不排队**；
6. goal 留在 `review`，且 `review_request=''` → 下一次恢复只能靠重启。

注意 `MarkDelivered` 开头加载了 goal 状态却**丢弃**了：

```go
// internal/service/goal.go:3240-3248（节选）
var statusCheck string
err = tx.QueryRowContext(ctx, `SELECT status FROM goal WHERE id=?`, goalID).Scan(&statusCheck)
if errors.Is(err, sql.ErrNoRows) {
    return nil, ErrNotFound
}
if err != nil {
    return nil, fmt.Errorf("load goal: %w", err)
}
_ = statusCheck
```

也就是说，函数没有「已离开 review 就早退」的语义，唯一的幂等保护是 UPDATE 的 `WHERE status='review'`——而它并不检查影响行数（P1-3）。这解释了为什么失败后**状态机没有任何「失败」痕迹**：事务回滚把能证明失败的东西全带走了。

修复：把「BeginTx → 两条 UPDATE → Commit」整段包进 `withBusyRetry`，**每次重试重新 `BeginTx`**（不能只重试 `Exec`，否则 snapshot 冲突不会消失），并在成功后返回。

### P0-2 恢复只在启动时跑一次【已确认·源码】

```go
// internal/daemon/deliver.go:44-51（注释）
// recoverPendingDelivers re-runs the deliver step for goals whose approve
// never delivered (decision 2-9, trigger side): a crash between the approve
// and the merge/push leaves the goal in review with the latest gate_decision
// = approve and a review_request that is NOT a deliver failure ("deliver:" —
// that is a real failure awaiting the human's retry/reject, never replayed).
// The replay itself is safe: deliverGoal's merge/push idempotency skips
// already-done steps.
```

调用点只有一处：

```go
// internal/daemon/daemon.go:475
if n, err := d.recoverPendingDelivers(ctx); err != nil {
```

主循环的 ticker 列表（`daemon.go:480-486`）是：dispatch、schedule、stale、cleanup、digest、issue、runaway——**没有 deliver 恢复**，也没有 10 分钟之类的周期任务。并且查询（`deliver.go:52-60`）没有 `decided_at` 年龄条件，也没有「该 goal 是否已有一个 in-flight deliver」的去重；它之所以现在是安全的，只是因为**启动时还没有任何 deliver 在飞**。一旦把它改成 ticker，必须同时加 `minAge`（例如 `now - decided_at >= 2m`），否则会和正常交付竞态。

修复：新增周期恢复（建议 10 分钟）+ `minAge` 过滤；保持查询「最新 decision 是 approve 且无 `deliver:` 注记」的语义。

### P1-1 失败被 `Infof` 吞掉，且不写失败注记【已确认·源码】

```go
// internal/daemon/deliver.go:324-333（节选）
func (d *Daemon) finishDeliver(ctx context.Context, goalID string, success bool, note string, commits ...[]string) {
    logging.Infof("daemon: deliver %s: %s", goalID, note)
    ...
    if _, err := d.goalSvc.MarkDelivered(ctx, goalID, success, note, fixCommits); err != nil {
        logging.Infof("daemon: MarkDelivered %s: %v", goalID, err)
    }
}
```

`Infof` 意味着：默认日志级别下它可能根本不出现在运维视野里；即使出现，也没有结构化的 `deliver_failed` 事件、没有告警、没有 activity 记录。

更关键的是**失败注记的写入路径走不到**：

```go
// internal/service/goal.go:3266-3271（失败分支）
} else {
    if _, err := tx.ExecContext(ctx,
        `UPDATE goal SET review_request=? WHERE id=? AND status='review'`, note, goalID); err != nil {
        return nil, fmt.Errorf("annotate deliver failure: %w", err)
    }
}
```

这条 `else` 只在 `success=false` 时执行。本次是 `success=true` 的交付，失败发生在它**内部**（UPDATE 抛错），所以 `review_request` 永远是空——而防重复保护与恢复查询都依赖 `review_request` 的 `deliver:` 前缀来识别「真失败」：

```go
// internal/service/goal.go:1943-1946
// EXCEPTION: a FAILED deliver annotates review_request ("deliver: ...")
deliverFailed := strings.HasPrefix(g.ReviewRequest, "deliver:")
```

→ BUG-1 与 BUG-2 在这里**互为放大器**：失败没留下注记，guard 就把「交付丢了」当成「交付进行中」。

修复：`finishDeliver` 在 error 分支至少做三件事：`Errorf`；尝试 `MarkDelivered(false, "deliver: "+err.Error())` 落注记（若失败再记一条）；发布 `deliver_failed` 事件（可复用现有通知链路）。

### P1-2 已有 helper，但没有用在收口上【已确认·源码】

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

HEAD 的全部调用点：

```text
internal/service/goal.go:1367:  err := withBusyRetry(func() error { ... })
internal/service/goal.go:2619:  err := withBusyRetry(func() error { ... })
```

两处都在 reconcile 路径。`MarkDelivered` 不在其中；`deliver.go` 也没有任何 busy 判断。

helper 的注释还明确约束了适用条件（`goal.go:3361-3365`）：调用方的事务必须是**条件迁移**、失败即回滚、重试从新快照开始。`MarkDelivered` 的 `UPDATE … WHERE status='review'` 正好满足，所以把整段包进去是**语义安全**的，不需要新写一个 helper。

修复：直接复用 `withBusyRetry`，把 `BeginTx` 放在闭包内部；重试次数/间隔可保持 3×100ms，或提到 5×200ms（交付是低频关键路径，多等 1s 值得）。

### P1-3 UPDATE 0 行仍发布 `goal:delivered`【已确认·源码】

`goal.go:3257` 的 UPDATE 之后没有 `RowsAffected()` 检查，直接 `cancel queued` → `Commit` → `Publish(goal:delivered)` → `OnGoalTerminal`（`goal.go:3272-3282`）。后果：

| 场景 | 现状 | 期望 |
| --- | --- | --- |
| 同一 goal 被重复交付（启动补偿 + 正常 deliver 并发、或事件重放） | 第二条 UPDATE 影响 0 行，但仍发一次 `goal:delivered`，仍调 `OnGoalTerminal` | 0 行 = 已离开 review，直接返回当前 goal，不发事件 |
| 0 行 + `OnGoalTerminal` | `rsi.go:203` 的 `UPDATE training_session … WHERE status IN ('waiting','analyzing')` 对 `analyzing` 也返回 1 行 → 再次 `go runAnalysis` | 同一 session 只允许一次自动分析 |
| 重复分析 | `Analyze` 幂等重建报告（`rsi_analyze.go:47-48` 注释），但每次插一轮 `rsi_round`（`rsi_analyze.go:168`） | 轮次不因重复事件增长 |

事故里没有出现重复交付（进程重启前只有一次交付尝试），但修复 P0-2（周期恢复）后这个窗口会真实打开：恢复 ticker 可能在原 deliver 还没结束时再次投递。所以 0 行语义**必须与周期恢复一起修**，否则会把「49 分钟卡死」换成「重复交付 + 重复分析轮次」。

修复：`UPDATE` 后检查 `RowsAffected()`；0 行直接 `Commit`（空事务）并返回 `s.Get(ctx, goalID)`，不 publish、不 `OnGoalTerminal`。

### P2 `busy_timeout(5000)` 不是「已经处理了」【已确认·源码；事故 build 待核】

```go
// internal/store/store.go:47-51
// busy_timeout=5000: when two writes collide (rare under WAL), block up to
// 5s for the lock instead of returning SQLITE_BUSY immediately — without
// this, concurrent task handlers persisting messages get (5) SQLITE_BUSY.
dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
```

HEAD 自己的注释就把这个失败模式写清楚了：

```go
// internal/service/goal.go:3347-3352
// isSQLiteBusy reports whether err is a SQLITE_BUSY / SQLITE_BUSY_SNAPSHOT
// failure. WAL snapshot-upgrade races fail IMMEDIATELY with SQLITE_BUSY —
// the busy_timeout pragma does not apply to them (the tx took a read
// snapshot, another writer committed, and the write upgrade collides). The
// live failure: ReconcileGoal's attention persist hit (5) SQLITE_BUSY at
// exactly the moment the review-run enqueue committed alongside it.
```

所以 BUG-1 的准确表述不是「没有超时」，而是：

1. **连接层有 5s 等待**，但错误仍返回到应用层 → 说明等待耗尽（锁持有 >5s），或
2. 事故 build 的 DSN 未带该 PRAGMA（版本不可核），或
3. **WAL snapshot 升级冲突**：事务先 `SELECT status` 建立读快照、期间另一个写者提交，`UPDATE` 升级写锁时**立即**返回 `SQLITE_BUSY`，`busy_timeout` 对此不生效。

`MarkDelivered` 恰好是「先 SELECT、后 UPDATE」的形状（`goal.go:3240-3241` → `:3257`），与注释描述的 live failure 同构；事故日志的错误码是 `(5) SQLITE_BUSY`，而 snapshot 冲突按注释也是码 5，**单凭码值无法区分 1/2 与 3**。原始诊断称「同一秒 deepseek 也在交付」，但轨迹表显示 deepseek 的批准是 09:06:53（19 秒后），持锁者仍无直接证据（待核 T3）。

无论哪种，结论不变：**5s 是连接层的最后一道缓冲，不是应用层的正确性保证**；交付收口必须有「重开事务再试」的逻辑——而且必须是**重开事务**，因为 snapshot 冲突只有拿到新快照才会消失。

## 复现条件

| 方式 | 步骤 | 期望（修复前） | 期望（修复后） |
| --- | --- | --- | --- |
| R1 真实锁竞争 | 两个 scratch 域 goal 同时 approve，其中一个在 `MarkDelivered` 的 SELECT 后、UPDATE 前被第三个写事务占锁 >5s | 一个 goal 停在 review，日志一行 `mark delivered: database is locked (5)` | 重试后 done；只发一次 `goal:delivered` |
| R2 注入 BUSY（单测） | 用 `sqlmock`/自定义 driver 让第一条 UPDATE 返回 `sqlite3.SQLITE_BUSY`，第二次成功 | 返回 error、goal review | 第二次成功；断言 `BeginTx` 被调用 2 次 |
| R3 0 行重复交付 | 对已 done 的 goal 再调 `MarkDelivered(true)` | 0 行仍 publish + OnGoalTerminal + 可能多一轮 | 0 行静默返回；无事件、无新 round |
| R4 恢复 | 构造 `status=review` + 最新 `gate_decision=approve` + `review_request=''`，**不重启**进程 | 永远 review | 一个恢复周期内 done |

R4 在 HEAD 上必然失败（没有 ticker）；R3 需要先修 P0-2 才会被周期触发，但单测可直接调用。

## 影响

| 面 | 量化 | 说明 |
| --- | --- | --- |
| 停摆时长 | **49m48s**（09:06:34 → 09:56:22） | 恢复靠人工 kill + 看门狗，不是系统自愈 |
| 人工操作 | 4 次点击（首次 + 3 次重批）全部无效 | 详见 BUG-2 |
| 状态机 | `review` 无超时、无失败态、无 lease | 任何外部观测都看不到「交付丢了」 |
| 数据 | round 4 延迟到 09:56:22 才密封 | 分析结果本身没丢，但训练列表的 ready 时间被推迟 50 分钟 |
| 可复用性 | 同一代码路径服务所有 goal 交付 | repo 域交付还要 merge/push，失败窗口更长，风险不低于 scratch |
| 放大效应 | 与 BUG-2 叠加 → reject 也被禁 | 人类失去唯一自救手段 |

## 修复方案

| 步骤 | 改动 | 关键语义 |
| --- | --- | --- |
| 1 | `MarkDelivered`：整段（BeginTx→两条 UPDATE→Commit）放进 `withBusyRetry`，闭包内每次重开事务 | 重试必须从新快照开始；不能在原事务里重试 Exec |
| 2 | `UPDATE` 后检查 `RowsAffected`；0 行 → 提交空事务、返回当前 goal、**不 publish** | 幂等；防重复 `goal:delivered` / 重复 `rsi_round` |
| 3 | `finishDeliver`：error 分支 `Errorf` + 落 `deliver: …` 注记 +（可选）发布 `deliver_failed` | 让 BUG-2 的 guard 旁路有真实信号 |
| 4 | 周期恢复 ticker（10m）+ `minAge`（2m）+ 与 in-flight deliver 去重 | 不重启也能恢复；不与正常交付竞态 |
| 5 | 指标：`deliver_busy_retries_total`、`deliver_failed_total`、`deliver_recovered_total`（或等价日志字段） | 让「锁冲突频率」可观测；当前只能靠 grep 日志 |

不建议的做法：只把 `busy_timeout` 从 5000 提到 30000。它掩盖不了 snapshot 冲突（不走 busy handler），也会让请求线程多等 30s；根因是「没有重试 + 没有恢复」。

## 验收

1. **单测**：注入一次 BUSY，断言 `MarkDelivered` 最终成功、`BeginTx` 调用 ≥2 次、只发一次 `goal:delivered`。
2. **单测**：0 行路径，断言不 publish、不调 `OnGoalTerminal`、不新增 `rsi_round`。
3. **集成**：构造 R4 现场（不重启），一个恢复周期内 goal done，且 `activity_log` 有恢复记录。
4. **回归**：scratch 交付当秒完成（本次 deepseek/minimax/openclaw 的正常路径），时长不增加。
5. **故障演练**：`kill -9` daemon 在 approve 与 deliver 之间，重启后仍能恢复（现有能力，不得回归）。
6. **日志断言**：BUSY 重试成功必须留下 WARN/INFO（含重试次数），失败必须 ERROR。

## HEAD 判定表

| 结论 | HEAD 位置 | 现状 | 判定 |
| --- | --- | --- | --- |
| 交付事务无 busy 重试 | `goal.go:3233-3272` | 无 `withBusyRetry`、无 `isSQLiteBusy` 调用 | **未修** |
| helper 存在但未用 | `goal.go:3353-3380`；调用点 `:1367`、`:2619` | 仅 reconcile 使用 | **未修** |
| 失败被 `Infof` 吞 | `deliver.go:330-332` | 无 Errorf、无注记、无事件 | **未修** |
| 恢复只在启动 | `daemon.go:475`；ticker `:480-486` | 无周期恢复、无 minAge | **未修** |
| 0 行仍发事件 | `goal.go:3257` 后无 `RowsAffected` | 0 行仍 publish + OnGoalTerminal | **未修** |
| 连接层有 5s 超时 | `store.go:47-51` | busy_timeout(5000) + WAL | **已存在，但不构成修复** |
| `deliver:` 注记语义 | `goal.go:3266-3271`（仅 success=false）、`:1946`（guard 识别） | success=true 内部失败写不到 | **未修** |

## 待核

| # | 待核 | 影响 | 建议 |
| --- | --- | --- | --- |
| T3 | 09:06:34 的持锁者是谁？原始诊断称 deepseek 交付，但轨迹表显示 deepseek 批准在 09:06:53 | 决定是「锁 >5s」还是「snapshot 冲突」，以及重试是否必须重开事务（结论：必须重开，但证据强度不同） | 原 DB `run`/`activity_log` 秒级对照；或并发复现 |
| T4 | 事故 build 的 DSN 是否带 `busy_timeout(5000)` | 区分「PRAGMA 生效但超时」与「build 无 PRAGMA」 | 对二进制 `strings` 查 DSN 模板；查启动环境 |
| T5 | 事故 build 的 `deliverGoal` 是否也有 0 行路径 | 当前无证据表明发生过重复交付 | 原 DB `rsi_round` 计数（opencode 是否只有 4 轮） |