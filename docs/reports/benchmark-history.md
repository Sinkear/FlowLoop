# ouroflow 构建/测试基准测量历史（R1 → R3）

> 合并自：`docs/reports/benchmark-report.md`（R1 复跑修订 v2）、`docs/reports/benchmark-report-r2.md`（R2 复测）、`docs/reports/benchmark-report-r3.md`（R3 冷缓存复测）、`docs/reports/impl-notes.md`（R2 实施说明） ｜ 合并日期：2026-11-23 ｜ 状态：历史记录（只读） ｜ 用途：单文件回溯三代基线（`0bf354e` / `365e108` / `e7c2572`）的测量条件、原始数字、失败清单与结论；原文事实与措辞未改写，仅按轮次重组并标注跨文档矛盾。

## 0. 文档沿革与合并说明

| 原文件 | 原标题 / 版本 | 日期 | 基线 | 并入本文件 |
| --- | --- | --- | --- | --- |
| `reports/benchmark-report.md` | ouroflow R1 构建/测试基准测量报告（bench）· R1 复跑·冲突重做修订 v2 | 2026-11-10 | `0bf354e` | §1、§2、§3、§4、附录 A |
| `reports/impl-notes.md` | RSI R2 实施说明（builder · impl-notes）· 状态：已完成 | 2026-11-10 | `365e108`（分支 `feat-25da0cc6-sg-23d061da`） | §5.1 |
| `reports/benchmark-report-r2.md` | ouroflow R2 实施后基准复测报告（bench）· v1 | 2026-11-10 | `365e108` | §5.2–§5.6、附录 A |
| `reports/benchmark-report-r3.md` | ouroflow R3 基准复测报告（bench · 冷缓存口径 · 无依赖并行重派版）· v1 | 2026-11-11 | `e7c2572` | §1、§6、附录 A |

- 原 R1 v1 报告（基线 `33f16dc`，冷 38.4s / 温中位 4.4s / 测试 14.5s）在集成时与同队另一条同名 `benchmark-report.md` 发生路径冲突，已被 v2 修订**整体替换**，仓库内不再保留；其数字仅通过 `reports/proposals.md` 的引用与 R2 对比表间接留存，见 §2.4、§8.2。
- 每份原报告均在自身尾部声明「本报告为工作区内该文件唯一有效版本」；合并后本文件是该四条测量线的唯一归档。
- 本文件只归档测量事实与后续文档给出的判定，不重新测量、不新增数字。

## 1. 口径与基线

### 1.1 三代基线

| 基线 commit | 轮次 | 分支 / 来源 | 说明 | 与上一代的关系 |
| --- | --- | --- | --- | --- |
| `33f16dc` | R1（v1，已废弃） | `ouroflow-int` HEAD（R1 早期） | Merge feat-98d60d12 (ouroflow deliver)；R1 v1 报告与 `reports/proposals.md` 的引用基线 | — |
| `0bf354e` | R1（v2，现存 R1 报告） | `ouroflow-int` 新集成基线（"Integrate 46d681ea…"），含 optimizer 提案 `proposals.md` | 相对旧基线 `33f16dc` **仅新增文档，无代码变更** | 文档递增，代码等同 |
| `365e108` | R2 | 分支 `feat-25da0cc6-sg-23d061da` HEAD | builder 实施成果；近几代：`3cf2116` P1 并行化 + `8ca9957` P2 CDN 守卫 fail→skip + `18a8c3c` P3 不可重试快速失败 + `365e108` impl-notes docs | 相对 `0bf354e` **仅含这三条已批准提案的实施与文档**，无其他代码变更 |
| `e7c2572` | R3 | 分支 `feat-357252be-sg-b13a5581` HEAD = `origin/ouroflow-int-v2` | 近三代：`e7c2572` R1/R2 交付物迁移 + `e08cb72` P1 并行化重放 + `e2bda1d` P2/P3 语义化重放 | 相对 `365e108` 是**新基线重建（历史重放）**，非增量演进 |

### 1.2 测量口径（P-C 提案执行版）

- **冷/温双列**，跨轮对比**只采冷列**（自 R3 起）。
- 冷 = 独立 `GOCACHE`（R1 v2：`/tmp/opencode/gocache-verify`；R2：`/tmp/opencode/gocache-r2`；R3：`/tmp/r3-gocache`，测量前 `rm -rf` 清空至 0 文件，测后实测写到 2402 个文件，真冷态）。
- 温 = 标准 `GOCACHE=/root/.cache/go-build`（R3 时 2.7G 热缓存）。
- 模块缓存 `/root/go/pkg/mod`（R3 时 651M）冷/温两场景共用且均热。
- `go test ./... -count=1` 禁用测试缓存；测试语义不受 build 缓存冷/温影响，故 test 采温列 3 连跑中位。
- 计时方式：R1 v2 用 shell `time`；R2/R3 用 Python subprocess 墙钟计时（`/tmp/opencode/timeit.py`，输出 `__WALL_SECONDS__`）。
- R1 **未留冷态 test 数据**，其 build/test 值按温列口径另行标注，不参与冷列对比（R3 报告口径说明）。

### 1.3 各轮环境

| 项目 | R1（`0bf354e`） | R2（`365e108`） | R3（`e7c2572`） |
| --- | --- | --- | --- |
| go version | go1.26.4 linux/arm64 | go1.26.4 linux/arm64 | go1.26.4 linux/arm64（/usr/local/bin/go 软链，本轮已修复可直接用） |
| 内核 / 架构 | Linux 5.10.0-182.0.0.95.r3353_274.hce2.aarch64 / aarch64 | 同左 | Linux 5.10.0 · aarch64 |
| CPU | 7 vCPU | 7 vCPU | 7 vCPU · 12Gi RAM |
| git | — | — | git version 2.33.0 |
| GOCACHE（冷） | `/tmp/opencode/gocache-verify` | `/tmp/opencode/gocache-r2` | `/tmp/r3-gocache`（测量前清空） |
| GOCACHE（温） | `/root/.cache/go-build` | `/root/.cache/go-build` | `/root/.cache/go-build`（2.7G 热） |
| GOMODCACHE | `/root/go/pkg/mod` | `/root/go/pkg/mod` | `/root/go/pkg/mod`（651M，两场景共用均热） |
| 工作区 | 全新 checkout（含 v1 报告文件），标准 GOCACHE 为热缓存 | `git worktree add --detach /tmp/opencode/bench-r2 feat-25da0cc6-sg-23d061da` 只读挂载 | bench 工作区（worktree）实跑，`git diff` 为空 |
| 其他 | — | — | `OUROFLOW_HOME=/root/.ouroflow`（全局已设置，与 R3 测试失败根因直接相关） |

### 1.4 各轮「只测量、不改代码」证据

- R1 v2：`git status` 仅本报告文件（新增），无任何源码修改；失败测试未修。
- R2：工作区唯一新增文件即本报告；基线以 `git worktree` 只读挂出。
- R3：`git diff` 为空；工作区唯一新增文件即本报告与 AGENTS.md 环境注入件。

## 2. 口径与 R1 多版本数字（必读）

R1 在仓库与后续文档中留下**四组互不一致的记录**，源于「不同基线 / 是否首跑含测试二进制编译 / 是否冷缓存」三个因素。合并时全部保留，不做加权或替换：

| # | 来源 | 基线 | build | test | daemon 包 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| R1-a | `benchmark-report.md`（现存 v2） | `0bf354e` | 冷 40.6s / 温 3.9s | 11.3s（单跑，exit 1） | 8.00s | 本文件 §4 的权威 R1 实跑数据 |
| R1-b | `reports/proposals.md` 引用已删除的 R1 v1 报告 | `33f16dc` | 冷 38.4s / 温中位 4.4s | 14.5s | 8.17s | 该版报告已被路径冲突替换，仓库无原文 |
| R1-c | `benchmark-report-r2.md` §4「R1 基线（任务给定）」 | 未注明 | 11.574s | 23.012s | 7.696s | 任务派发时给定值，**无对应入库报告**；R2 的 -50% 即以此为分母 |
| R1-d | `analysis-r3.md` 问题 #14 | 同基线（`33f16dc`/`0bf354e`） | 冷 38–44s vs 温 3.9–5.6s | 23.012s（首跑含测试二进制编译）→ 14.5s（sg-B2 复跑）→ 11.3s（R1 冻结口径） | — | 对前三者的自我修正说明 |

- 结论：R1 的 test 值本身不可跨来源比较（23.012s 含测试二进制编译，14.5s 与 11.3s 为温态执行；daemon 8.17 / 8.00 / 7.696 对应不同机器负载与轮次）。
- R2 报告明确采用 R1-c 作对比分母，同时补充参照 R1-a（daemon 8.00 → R2 5.9，P1 收益 -26%；R2 报告 §4 补充段）。两种对比在原文中并列存在，见 §5.4。

## 3. R1 基线（`0bf354e`，2026-11-10）

### 3.1 各命令总耗时（完整复跑）

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | 冷（独立 `GOCACHE=/tmp/opencode/gocache-verify`，模块缓存热） | 40.6 s | 0 |
| `go build ./...` | 温（标准 GOCACHE，测后复跑验证） | 3.9 s | 0 |
| `go test ./... -count=1` | 冷（禁用测试缓存，完整执行） | 11.3 s | 1（有失败用例） |

说明（原文）：v1（基线 `33f16dc`）冷构建 38.4 s、温构建中位 4.4 s、测试 14.5 s；本轮（基线 `0bf354e`）数据同量级，测试 wall 由 14.5 s 降至 11.3 s 属机器负载波动范围（Top 包 daemon 8.17s → 8.00s 基本不变）。

### 3.2 包级测试耗时 Top10（`go test ./... -count=1`，共 27 个包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | `github.com/Sinkear/ouroflow/internal/daemon` | 8.00 s | pass |
| 2 | `github.com/Sinkear/ouroflow/internal/service` | 4.61 s | **FAIL** |
| 3 | `github.com/Sinkear/ouroflow/cmd/ouroflow-cli` | 4.20 s | pass |
| 4 | `github.com/Sinkear/ouroflow/internal/notify` | 0.89 s | pass |
| 5 | `github.com/Sinkear/ouroflow/internal/issue` | 0.30 s | pass |
| 6 | `github.com/Sinkear/ouroflow/internal/backup` | 0.26 s | pass |
| 7 | `github.com/Sinkear/ouroflow/internal/server/handler` | 0.05 s | pass |
| 8 | `github.com/Sinkear/ouroflow/internal/paths` | 0.04 s | pass |
| 9 | `github.com/Sinkear/ouroflow/internal/server` | 0.03 s | pass |
| 10 | `github.com/Sinkear/ouroflow/internal/acp` | 0.03 s | pass |

其余包均 < 0.03 s；`cmd/ouroflow-daemon`、`cmd/validate-templates`、`internal/card`、`internal/events`、`internal/gitcodeapi`、`internal/store` 等无测试文件（`[no test files]`，未计入排名）。

### 3.3 失败用例清单（3 条，与 v1 一致）

均位于 `internal/service`（`codes_test.go`），父测试 `TestCodesMatchRemoteTermJSON` FAIL：

| # | 用例 | 耗时 | 失败原因（摘要） |
| --- | --- | --- | --- |
| 1 | `TestCodesMatchRemoteTermJSON` | 0.27 s | 父测试汇总失败 |
| 2 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json` | 0.26 s | 远端 CDN 词表缺少部分编码（zh-cn） |
| 3 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json` | 0.01 s | 远端 CDN 词表缺少部分编码（en-us） |

缺失编码集合（两份词表一致，由 `go test -json` 输出提取）：

```
AW.10000025  AW.10000026  AW.10000027
AW.10000028  AW.10000029  AW.10000030
```

代表性失败日志（`codes_test.go:102`）：

```
code "AW.10000025" missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json — add it via the CDN approval flow
```

失败性质判断（原文）：该测试依赖外部 CDN 资源（`res-static.hc-cdn.cn` 的 `term.json`），远端词表尚未同步仓库侧新增的 `AW.10000025`~`AW.10000030` 编码，属**外部依赖未同步**，而非本地代码缺陷；按要求不做修复，如实记录。

### 3.4 R1 汇总

- 构建：冷 40.6 s，温 3.9 s，构建全部成功。
- 测试：`go test ./... -count=1` 11.3 s；27 个包，24 包通过、1 包失败（`internal/service`）、其余无测试文件（原文口径，包数算术见 §8.2）。
- 失败用例：3 条（`TestCodesMatchRemoteTermJSON` 及 zh-cn/en-us 两个子用例），根因为远端 CDN 词表缺码（`AW.10000025~30`），未修复。
- 与 v1 差异：无实质差异；失败用例集合与根因完全一致，包级耗时排序因波动略有变化（daemon 仍为最慢包）。

## 4. R2 复测（`365e108`，2026-11-10）

### 4.1 R2 实施说明（`impl-notes.md` 事实）

三条已批准提案（P1、P2、P3）全部实施，机器验证门自检通过。分支 `feat-25da0cc6-sg-23d061da`（基于 `ouroflow-int` HEAD `339f10d`），每条提案独立 commit：

| commit | 提案 | 内容 |
| --- | --- | --- |
| `3cf2116` | P1 | 并行化 `internal/daemon` 独立顶层测试 |
| `8ca9957` | P2 | CDN 漂移守卫对畸形响应 fail 改 skip |
| `18a8c3c` | P3 | 失败重试引入不可重试错误快速失败 |

**P1（并行化 `internal/daemon` 测试）**

- 改动：纯 `*_test.go`，7 个文件，+140 行。对 7 个无 `t.Setenv` 的测试文件（`intake_test.go`、`verify_test.go`、`gate_test.go`、`schedule_test.go`、`runaway_test.go`、`prompt_context_test.go`、`handoff_cancel_test.go`）的全部 **70 个顶层测试函数**加 `t.Parallel()`。
- 使用 `t.Setenv` 隔离 HOME 的 3 个文件（`deliver_test.go`、`digest_test.go`、`worktree_test.go`）保持串行（`t.Setenv` 自动禁止并行）。
- 实施前核实：7 个文件均无 `os.Setenv`/`Chdir`/固定端口/共享 HOME 依赖；`runaway_test.go` 中 `TestScratchDirNames` 只读 `runsRoot()` 路径计算；各测试均用 `:memory:` SQLite 或 `t.TempDir()`。
- 次级目标 `cmd/ouroflow-cli` **未动**：4 个测试文件中 3 个使用 `t.Setenv`（gitwork/main/probe），纯函数测试（chat/exec）合计 <0.1s，并行化收益趋近于零且违反「改动最小化」。
- 验证：`-count=1` 连跑 3 次全绿；wall 5.30s / 5.73s / 4.99s（实施前基线 8.17s，**约 -35%**）；涉及状态机/取消链的 15 个测试 `-race` 通过；`gofmt -l` 无新增格式问题（4 个文件为基线既有问题）。

**P2（CDN 漂移守卫 fail→skip）**

- 改动：`internal/service/codes_test.go`，+26/-2 行。decode 失败（截断 JSON / 网关错误页 200+HTML）由 `t.Fatalf` 改为 `t.Skipf("CDN returned malformed body — skip drift guard: %v", err)`，与既有不可达（`:89-90`）/非 200（`:93-94`）的 skip 语义对齐；新增 `OUROFLOW_SKIP_CDN_DRIFT=1` 环境开关。
- **缺码漂移同步改 skip**：实施时发现 CDN 实际返回合法 JSON 但缺 6 码（`AW.10000025~30`，两 locale 各 6 条），走缺码 `Errorf` 分支；若缺码仍 `Errorf`，`go test ./...` 无法恢复全绿，与提案 ③ 预期「1/24 包 FAIL → 0」矛盾。提案 ④ 风险条款亦明确「CDN 缺码不再使测试变红」为已接受风险，故缺码分支按提案语义改为 `t.Skipf`，skip 时输出完整缺码清单（保留可诊断性），可 `-run TestCodesMatchRemoteTermJSON` 手动复查。
- 本地不变量守卫 `TestErrorCodesExported` 保持不动。
- 验证：缺码场景输出 6 码 × 2 locale；`OUROFLOW_SKIP_CDN_DRIFT=1` 时整守卫 skip；`go test ./... -count=1` 全量无 FAIL（**13 个含测试的包全 ok**——与 §4.2 的 14 包口径差异见 §8.2）。

**P3（不可重试错误快速失败）**

- 改动：`internal/service/goal.go` +41/-4、`internal/daemon/daemon.go` +4/-1。
- 新增 `nonRetryableMarkers` 白名单 + `isNonRetryable(summary string) bool`（提案定位 `goal.go:92-104` 附近）：白名单子串匹配，起步只收 `Model unavailable` 一项（runs API 实测 12/14 条失败为同一确定性模型配置错误）；空 summary 一律可重试。
- `reconcileOnRunEndOnce` failed 分支（提案定位 `:1337`）：重试条件 `rc.Attempt < maxAttempts && !isImportGoal` 扩展为 `&& !nonRetryable`——确定性故障不烧重试预算、不追加 30s/60s backoff、不少唤醒 leader，直接走既有 goal-failed 终态分支（`:1368`，即 else 分支）。
- 终态 summary 追加「（环境/配置错误，重试无效）」。
- 同步 `internal/daemon/daemon.go:124` `maxAttempts` 注释，说明不可重试例外。
- 验证：`go build ./...` 全绿；`go vet` 全绿；`internal/service` 224 个测试通过；`internal/daemon` 全绿（CDN 守卫需 P2 的 skip 语义或环境开关）；其余错误类型的重试/退避行为完全不变（`retryBackoffFor` 未动）。

**机器验证门自检（与门完全相同的命令）**

```
go build ./...            → PASS
go test ./... -count=1    → PASS（连跑 2 次全量 0 FAIL，wall 10.9s / 9.4s）
```

**回滚**：三条提案各自独立 commit，任一 `git revert <sha>` 即可单独还原，互不影响。

### 4.2 各命令总耗时（`365e108` 上实跑）

测量方式：`git worktree add --detach /tmp/opencode/bench-r2 feat-25da0cc6-sg-23d061da` 只读挂载，未改任何代码（工作区唯一新增文件即报告）。

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | 冷（独立 `GOCACHE=/tmp/opencode/gocache-r2`，模块缓存热） | 44.417 s | 0 |
| `go build ./...` | 温（标准 GOCACHE，3 连跑） | 4.387 / 3.670 / 5.137 s（中位 **4.387 s**） | 0 |
| `go test ./... -count=1` | 3 连跑（禁用测试缓存，完整执行） | 14.399 / 11.421 / 11.559 s（中位 **11.559 s**） | **0（3 次均 0 FAIL）** |

**原文结论：`go test ./... -count=1` 三连跑全绿（14 包 ok、0 FAIL、grep "FAIL" 计数 0），P2 修复 CDN 守卫后测试套件全绿达标；本轮无任何失败用例。**

### 4.3 包级测试耗时 Top10（run3，共 14 个含测试包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | `github.com/Sinkear/ouroflow/internal/daemon` | 5.911 s | pass |
| 2 | `github.com/Sinkear/ouroflow/cmd/ouroflow-cli` | 4.530 s | pass |
| 3 | `github.com/Sinkear/ouroflow/internal/service` | 4.456 s | pass（R1 唯一失败包，现已 PASS） |
| 4 | `github.com/Sinkear/ouroflow/internal/notify` | 0.925 s | pass |
| 5 | `github.com/Sinkear/ouroflow/internal/issue` | 0.419 s | pass |
| 6 | `github.com/Sinkear/ouroflow/internal/backup` | 0.297 s | pass |
| 7 | `github.com/Sinkear/ouroflow/internal/link` | 0.075 s | pass |
| 8 | `github.com/Sinkear/ouroflow/internal/server` | 0.061 s | pass |
| 9 | `github.com/Sinkear/ouroflow/internal/acp` | 0.056 s | pass |
| 10 | `github.com/Sinkear/ouroflow/internal/server/handler` | 0.049 s | pass |

- 共 27 个包：14 个含测试（全部 ok）+ 13 个无测试文件（`[no test files]`，未计入排名）。
- 三连跑 Top3 稳定性：daemon 6.218 / 5.719 / 5.911 s；service 4.619 / 4.399 / 4.456 s；cli 4.295* / 3.876 / 4.530 s（*run1 取自同命令补跑）。
- 前 3 名之外各包均 < 1 s；包结构与 R1 基线一致（实施 commit 仅触及 `internal/daemon` 与 `internal/service` 的测试文件）。

### 4.4 与 R1 基线对比（原文表，R1 取任务给定值）

R1 基线（任务给定）：build 11.574s / test 23.012s / `internal/daemon` 7.696s / 1 包失败。

| 指标 | R1 基线 | R2 实施后（`365e108`） | 变化 |
| --- | --- | --- | --- |
| `go build ./...` | 11.574 s | 温中位 **4.387 s**（冷 44.417 s 另列） | 温构建明显低于 R1（冷/温双口径均如实记录；R1 口径接近温构建） |
| `go test ./... -count=1` | 23.012 s | 中位 **11.559 s**（三连跑 14.399/11.421/11.559） | **-11.453 s（约 -50%）** |
| `internal/daemon` 包耗时 | 7.696 s | 中位 **5.911 s**（三连跑 6.218/5.719/5.911） | **-1.785 s（约 -23%）**，P1 并行化收益 |
| 失败包数 | 1（`internal/service`） | **0** | **1 → 0**，P2 生效 |
| 测试结果 | 1 包 FAIL（CDN 缺码 3 用例） | 全绿（14 包 ok、0 FAIL） | FAIL → PASS/SKIP |

补充参照（原文）：工作区内 R1 复跑报告（基线 `0bf354e`）记录 daemon 8.00s、service 4.61s FAIL、全量测试 wall 11.3s——与 R2 的 daemon 5.9s、service 4.46s PASS 相比，P1 收益（-26%）与 P2 效果（FAIL 消除）方向一致。

### 4.5 P2 行为验证（`-v` 实测证据，两次实测一致）

```
    codes_test.go:126: CDN term.json out of sync — skip drift guard — 6 code(s) missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json: AW.10000025, AW.10000026, AW.10000027, AW.10000028, AW.10000029, AW.10000030
    codes_test.go:126: CDN term.json out of sync — skip drift guard — 6 code(s) missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json: AW.10000025, ... , AW.10000030
    --- SKIP: TestCodesMatchRemoteTermJSON/...zh-cn/term.json (0.06s)
    --- SKIP: TestCodesMatchRemoteTermJSON/...en-us/term.json (0.01s)
```

- R1 中 FAIL 的 2 个子测试现均为 **SKIP**，父测试 PASS；缺码集合与 R1 完全一致（`AW.10000025~30` 共 6 码 × 2 locale），skip 时输出完整清单，可诊断性保留。

### 4.6 P1 / P3 实施核对（代码审计，不改行为）

- **P1**：`grep -c "t.Parallel()" internal/daemon/*_test.go` → gate 2 / handoff_cancel 8 / intake 30 / prompt_context 11 / runaway 3 / schedule 4 / verify 12，共 **70 处**，与 builder 报告逐文件一致；效果实测 daemon 7.696 → 5.911 s（约 -23%）。
- **P3**：`internal/service/goal.go:105-123` 含 `nonRetryableMarkers` 白名单（起步仅 `Model unavailable`）与 `isNonRetryable`；`internal/daemon/daemon.go:127` maxAttempts 注释同步。测试套件无针对 P3 的新增用例，测试耗时无对应变化，属预期。

### 4.7 R2 结论（原文四条）

1. **P1 并行化**：daemon 包 7.696s → 5.911s（约 -23%），收益真实可复现（三连跑稳定）。
2. **P2 CDN 守卫 fail→skip**：R1 唯一失败包已消除，`go test ./... -count=1` 三连跑全绿（14 包 ok、0 FAIL）。
3. **P3 不可重试快速失败**：实现到位（`service/goal.go` 白名单 + `isNonRetryable`），不改变测试行为。
4. **总体**：实施后基线 `365e108` 上，全量测试中位 11.559s（R1 23.012s，约 **-50%**），失败包 1 → 0，build 温中位 4.387s。三条已批准提案的预期效果均已在基准复测中达成。

> 上述第 4 条的 -50% 与「预期效果均达成」在后续审查中被判为口径问题，见 §8.2（保留原文结论与后续判定，二者并列）。

## 5. R3 冷缓存复测（`e7c2572`，2026-11-11）

### 5.1 各命令总耗时

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | **冷**（独立 GOCACHE，测量前清空） | **41.285 s** | 0 |
| `go build ./...` | 温（标准 GOCACHE，3 连跑） | 10.150 / 4.672 / 4.478 s（中位 **4.672 s**） | 0（3 次均 0） |
| `go test ./... -count=1` | 3 连跑（禁用测试缓存） | 16.447 / 13.173 / 11.919 s（中位 **13.173 s**） | **1（3 次均 2 包 FAIL，见 §5.4）** |

- 温 build run1（10.150s）偏高：冷测后标准缓存首次回写所致，如实记录；run2/run3 已回到 R2 温态水平（4.672/4.478 vs R2 4.387/3.670/5.137）。
- `go test` 计时含失败用例的完整执行与重试路径，中位数仍具跨轮可比性。

### 5.2 冷/温双列对照（P-C 口径核心表）

| 指标 | 冷列（独立 GOCACHE） | 温列（标准 GOCACHE） | 跨轮对比采列 |
| --- | --- | --- | --- |
| `go build ./...` | **41.285 s** | 中位 **4.672 s**（10.150/4.672/4.478） | **冷列** |
| `go test ./... -count=1` | 未单测冷态（测试三连跑均在温缓存下，中位 13.173 s） | 中位 **13.173 s**（16.447/13.173/11.919） | 温列（测试语义不受 build 缓存冷/温影响，`-count=1` 已禁测试缓存） |
| 冷/温差距 | — | build 冷态约为温态 **8.8 倍**（41.285 / 4.672） | — |

**口径说明（原文）**：R2 报告的 build 11.574s（R1 基线给定值）与 4.387s（R2 温中位）均接近**温列**；R2 冷测 44.417s 为首次冷列记录。本轮起跨轮对比统一采冷列（R1 未留冷态数据，R1 的 build/test 值按温列口径另行标注，不参与冷列对比）。

### 5.3 与 R2 基线对比表（build 采冷列 · test 采温列中位）

| 指标 | R2（`365e108`，2026-11-10） | R3（`e7c2572`，本轮） | 变化 |
| --- | --- | --- | --- |
| `go build ./...` **冷** | 44.417 s | **41.285 s** | **-3.132 s（约 -7.1%）**，冷列口径可比 |
| `go build ./...` 温中位 | 4.387 s | 4.672 s | +0.285 s（约 +6.5%），仅参照不采信 |
| `go test ./... -count=1` 中位 | 11.559 s | 13.173 s | **+1.614 s（约 +14.0%）** |
| `internal/daemon` 包耗时中位 | 5.911 s | 5.855 s（5.855/4.893/6.026） | -0.056 s（基本持平），P1 并行化收益保持 |
| `cmd/ouroflow-cli` 包耗时中位 | 4.530 s | 2.374 s（3.852/2.166/2.374） | -2.156 s（约 -47.6%，失败路径提前退出所致，见 §5.5 注） |
| `internal/service` 包耗时中位 | 4.456 s | 6.144 s（6.200/6.004/6.229） | +1.688 s（约 +37.9%） |
| 失败用例数 | 0（全绿） | **6（2 包）** | **0 → 6**，见 §5.4 |
| 失败包数 | 0 | 2（`cmd/ouroflow-cli`、`internal/daemon`） | 0 → 2 |

**结论（冷列，原文）**：新基线 `e7c2572` 冷构建 41.285s，较 R2 冷测 44.417s **约 -7%**——R2 已批准提案（P1 并行化等）的收益在新基线重建中得到保持，未因历史重放而回退。**test 中位 +14%** 的原因主要是 `internal/service` 包 +38%（新增 RSI 分析代码 `e7c2572` 的测试负载）与失败用例的重试路径。

**结论（测试健康，原文）**：R2 全绿 → R3 6 例失败。**6 例失败全部为测试环境隔离缺陷（`OUROFLOW_HOME` 全局泄漏 + 固定 domainID/goalID 跨运行残留），非业务代码功能缺陷**。

### 5.4 包级测试耗时 Top10（run3，共 30 个包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | `github.com/Sinkear/ouroflow/internal/daemon` | 6.026 s | **FAIL**（4 例，见 §5.5） |
| 2 | `github.com/Sinkear/ouroflow/cmd/ouroflow-cli` | 2.374 s | **FAIL**（2 例，见 §5.5） |
| 3 | `github.com/Sinkear/ouroflow/internal/service` | 6.229 s | pass（R2 4.456s → 6.229s，+38%，RSI 新代码测试负载） |
| 4 | `github.com/Sinkear/ouroflow/internal/notify` | 0.884 s | pass |
| 5 | `github.com/Sinkear/ouroflow/internal/issue` | 0.395 s | pass |
| 6 | `github.com/Sinkear/ouroflow/internal/backup` | 0.326 s | pass |
| 7 | `github.com/Sinkear/ouroflow/internal/server/handler` | 0.069 s | pass |
| 8 | `github.com/Sinkear/ouroflow/internal/zipx` | 0.039 s | pass |
| 9 | `github.com/Sinkear/ouroflow/internal/link` | 0.037 s | pass |
| 10 | `github.com/Sinkear/ouroflow/internal/server` | 0.030 s | pass |

- 共 30 个包：12 ok + 2 FAIL + 16 无测试文件（`[no test files]`，未计入排名）。R2 为 27 包（14 ok）；新基线包数 27 → 30（新增 `internal/paths`、`internal/proto/acpbackend`、`internal/card/feishu` 等拆分/新增包），daemon 测试部分迁移至 `cmd/ouroflow-cli`，跨轮包级对比已按同名包对齐。
- 三连跑 Top3 稳定性：daemon 5.855/4.893/6.026 s；cli 3.852/2.166/2.374 s；service 6.200/6.004/6.229 s。
- 注（原文）：cli 包中位 2.374s 较 R2 4.530s 大幅下降，是 2 个失败用例在 setup 后段提前退出（fetch 128）所致，**不是性能改进**，不采信为收益。

### 5.5 失败用例清单与根因归类（只读诊断，未改代码）

`go test ./... -count=1` 三连跑稳定复现同样 6 例（另：上轮被取消的 sg-REBENCH 运行残留数据 `/tmp/opencode/r3-test-run1.txt` 亦复现同样 6 例，交叉印证与运行时点无关）：

| # | 用例 | 包 | 错误摘要 | 根因归类 |
| --- | --- | --- | --- | --- |
| 1 | `TestGitWorkdirAndTransferPush` | `cmd/ouroflow-cli` | `gitwork_test.go:59: workdir: fetch: exit status 128: '/tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git' does not appear to be a git repository` | **A：`OUROFLOW_HOME` 泄漏 + 陈旧缓存复用** |
| 2 | `TestPersistentGoalWorkdir` | `cmd/ouroflow-cli` | 同上（同一陈旧 `bare.git` 路径） | A |
| 3 | `TestDeliverMergeIdentityNoGlobalGitConfig` | `internal/daemon` | `deliver_test.go:114: goal status = "review", want done. review_request="deliver: fetch: exit status 128: '/tmp/TestDeliverMergeIdentity.../002/remote.git' does not appear..."` | **A'：同源（daemon 侧 deliver 走真实 HOME 下仓库）** |
| 4 | `TestParallelConsultWorktreeDetaches` | `internal/daemon` | `worktree_test.go:39: owner worktree: git fetch: exit status 128: '/tmp/TestParallelConsultWorktreeDetaches.../002/src' does not appear...` | A' |
| 5 | `TestCollectDigestBatchWritesAndIsIdempotent` | `internal/daemon` | `digest_test.go:206: articles = 1, want 2`（日志：`goal g-1 batch already collected — skipping append`） | **B：`digestRoot()` 落在真实守护进程数据目录，跨运行幂等残留** |
| 6 | `TestCollectDigestBatchBadManifestKeepsMetadatalessButNoManifestEntry` | `internal/daemon` | `digest_test.go:386: articles = 1, want 2 (manifest.json excluded)`（同上 "already collected"） | B |

#### 根因 A（#1/#2，实测证据链）

1. `cmd/ouroflow-cli` 测试 `t.Setenv("HOME", t.TempDir())` 只改 HOME；`internal/paths.Home()`（`paths.go:36-46`）**优先读 `OUROFLOW_HOME`**（本环境全局设置为 `/root/.ouroflow`，且 `paths.go:15` 注释明确 "OUROFLOW_HOME is read on every call"）。
2. `ensureBareRepo`（`cmd/ouroflow-cli/gitwork.go:38-62`）用固定 domainID `dom-1` 计算 `paths.RepoDir("dom-1")` = `/root/.ouroflow/repos/dom-1`，**只检查 `HEAD` 存在即跳过 re-clone**（`gitwork.go:44` "Existence alone is not cloned" 注释所防的恰是半写目录，但未防陈旧 remote）。
3. 实测 `/root/.ouroflow/repos/dom-1`：HEAD 存在（2026-11-10 20:49 上轮测试遗留）、`git remote -v` 指向 `file:///tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git`（该目录已删除）→ fetch 128。
4. 对照实验：手动在同一环境建 `bare.git` + clone + push + `git ls-remote file://…` 全部正常（git 2.33.0 无问题）→ **排除 git 工具链问题**；失败仅出现在复用陈旧 `/root/.ouroflow/repos/dom-1` 时。

#### 根因 B（#5/#6，实测证据链）

1. digest 测试同样 `t.Setenv("HOME", t.TempDir())`，但 `digestRoot()` = `paths.Digest()` = `filepath.Join(Home(), "digest")` = **真实 `/root/.ouroflow/digest`**（`OUROFLOW_HOME` 优先）。
2. 实测 `/root/.ouroflow/digest/g-1/`（只剩 `1.md`）与 `articles.json`（create 时间戳 `2026-11-11T08:45:30Z` = 本轮第一次三连跑时刻）跨测试运行共享；`collectDigestBatch` 的幂等检查（`digest.go:193-199` "batch already collected — skipping append"）命中上一轮 `g-1` 残留 → 第二次起不再追加 → `articles = 1, want 2`。
3. 单跑复现验证：`go test ./internal/daemon -count=1 -run 'TestCollectDigestBatchWritesAndIsIdempotent'` 单独执行亦 FAIL（日志同样 "already collected"）→ **稳定复现，非并行污染**。

#### 归类小结（原文）

- **非业务缺陷**：6 例全部可通过「测试内 `t.Setenv("OUROFLOW_HOME", t.TempDir())`（或 `ensureBareRepo` 增加 remote 存活校验）」修复，业务代码（deliver/worktree/digest 的生产路径）本身逻辑未被证伪。
- **与 optimizer P-A 提案的关系**：根因 A 是 P-A「验证子进程环境净化」的**姊妹问题**——P-A 防 daemon 验证子进程继承 `OUROFLOW_HOME`，本组失败防 **go test 子进程**继承 `OUROFLOW_HOME` 后 paths 层误用；修复方向一致（环境隔离层），建议 P-A 实施时一并覆盖测试进程环境。
- **对基准连续性的影响**：R2 全绿口径在新基线上被测试隔离缺陷打破；建议后续轮次将「测试环境隔离修复」作为高优任务，修复后 §5.4 失败项应归零，届时 test 中位数才可与 R2 11.559s 直接对比。

### 5.6 R3 结论（原文五条）

1. **冷列（跨轮采信）**：新基线 `e7c2572` 冷构建 **41.285s**，较 R2 冷测 44.417s **约 -7%**，R2 提案收益在新基线重建中保持。
2. **温列（参照）**：温 build 中位 4.672s（R2 4.387s，+6.5%）；test 中位 13.173s（R2 11.559s，+14.0%，主因 service 包 +38% RSI 新代码负载与失败重试路径）。
3. **测试健康**：6 例失败（2 包）**全部为测试环境隔离缺陷**（`OUROFLOW_HOME` 全局泄漏 × 固定 ID 跨运行残留），根因已实测定位（含陈旧 remote 路径、跨运行共享目录、单跑复现三重证据），非业务代码缺陷；已与 P-A 提案建立关联。
4. **基准连续性**：R1（`0bf354e`）→ R2（`365e108`）→ R3（`e7c2572`）三代基线数据齐备；自本轮起冷/温双列口径落地，跨轮对比只采冷列。
5. 本轮未改任何代码（`git diff` 为空），全部测量在本 bench 工作区只读完成。

## 6. 横向对比表（跨轮一页视图）

> 列内数值均取自各轮原文；**不同列/不同口径不可相减**（R1 无冷态 test；test 首跑含编译）。空值表示该轮原文未测。

| 指标 | R1-a（`0bf354e`，现存报告） | R1-c（任务给定，R2 引用） | R2（`365e108`） | R3（`e7c2572`） |
| --- | --- | --- | --- | --- |
| `go build` 冷 | 40.6 s | — | 44.417 s | **41.285 s** |
| `go build` 温 | 3.9 s（单跑） | 11.574 s（口径未注明，原文称接近温列） | 中位 4.387 s（4.387/3.670/5.137） | 中位 4.672 s（10.150/4.672/4.478） |
| `go test ./... -count=1` | 11.3 s（单跑） | 23.012 s | 中位 **11.559 s**（14.399/11.421/11.559） | 中位 13.173 s（16.447/13.173/11.919） |
| `internal/daemon` | 8.00 s | 7.696 s | 中位 5.911 s（6.218/5.719/5.911） | 中位 5.855 s（run3 6.026 s） |
| `internal/service` | 4.61 s **FAIL** | 1 包失败 | 4.456 s pass | 中位 6.144 s（run3 6.229 s）pass |
| `cmd/ouroflow-cli` | 4.20 s | — | 4.530 s | 中位 2.374 s（失败提前退出，不采信） |
| 失败用例 / 失败包 | 3 / 1 | 未细分 / 1 | 0 / 0 | **6 / 2** |
| 包总数（含测试包） | 27（24 ok + 1 FAIL，原文） | — | 27（14 测试 + 13 无测试） | 30（12 ok + 2 FAIL + 16 无测试） |
| 退出码 | 1 | 未注明 | 0（3 次） | 1（3 次） |

## 7. 结论（按轮归档）

- **R1**：构建冷 40.6s / 温 3.9s；测试 11.3s 但有 3 条失败用例，根因是外部 CDN 词表缺 6 码；代码零改动。
- **R2**：三条已批准提案实施后，`go test ./... -count=1` 三连跑全绿（0 FAIL），daemon 包 5.911s（对比任务给定 R1 基线 -23%），build 温中位 4.387s；原文判定「预期效果均达成」。
- **R3**：新基线重建后冷构建 41.285s（对比 R2 冷测 -7.1%），温列 build +6.5%、test 中位 +14.0%；测试由全绿退回 6 例失败，全部定位为测试环境隔离缺陷（A/A'/B），非业务代码缺陷。
- **跨轮可信度**：自 R3 起冷/温双列分离，跨轮 build 只比冷列；test 只比温列中位。R1/R2 的部分结论建立在跨口径相减之上，使用时须带 §2 的口径表。

## 8. 局限、存疑与后续审查

### 8.1 测量局限（原文归纳）

1. **R1 无冷态 test 数据**，R1 的 build/test 只能按温列标注，不能参与冷列跨轮对比。
2. **机器负载波动**：R1 测试 wall 14.5 → 11.3s 被原文归为负载波动（daemon 8.17 → 8.00s 基本不变）；单跑值与中位值不可混用。
3. **外部依赖**：R1/R2 的 CDN 失败源于 `res-static.hc-cdn.cn` 词表未同步，非仓库代码可控；R2 后缺码改 SKIP，红信号消失（可诊断性保留在 skip 日志）。
4. **测试环境泄漏**：R3 的 6 例失败由 `OUROFLOW_HOME=/root/.ouroflow` 全局变量 + 固定 domainID/goalID 残留造成，任何未设该变量的机器不复现；因此 R3 的 test 中位不可与 R2 直接等同比较。
5. **口径变更本身改变可比性**：R2 冷测 44.417s 是首次冷列记录，与 R1 无冷列基线之间只能单向参照。
6. **未做重复测量统计**：冷构建每轮仅 1 次，无 min/max；温 build/test 为 3 连跑中位。

### 8.2 已知矛盾与存疑（合并时发现，保留原文并标注）

1. **R1 四组数字并存**：见 §2。`benchmark-report.md`（40.6/3.9/11.3，daemon 8.00）、已删除的 R1 v1（38.4/4.4/14.5，daemon 8.17）、R2 报告引用的任务给定值（11.574/23.012，daemon 7.696）、`analysis-r3` 的 23.012→14.5→11.3 演进说明。四者基线/口径/是否含测试编译均不同，**无法调和为单一 R1 基线**；原文各自的对比结论因此分母不同。
2. **R1 包数算术不一致**：R1 报告称「27 个包，24 包通过、1 包失败（internal/service）、其余无测试文件」，但同文又列出 6 个无测试包，24+1+6=31 ≠ 27；Top10 之外还有若干 <0.03s 的包。R2 报告记 27 包 = 14 测试 + 13 无测试（14+13=27，自洽）。R1 汇总句的「24 包通过」疑为笔误或统计口径不同，原文保留。
3. **R2 测试包数 14 vs 13**：`benchmark-report-r2.md` 记「14 个含测试包（全部 ok）」「14 包 ok、0 FAIL」；`impl-notes.md` P2 验证记「13 个含测试的包全 ok」。相差 1 个包，原文未说明，保留两处。
4. **R3 service 中位 6.144s 与其三连跑值不符**：原文列出 6.200/6.004/6.229，其中位应为 6.200；报告的 6.144s 无法由所列三值复算（run3 值为 6.229s，§5.4）。保留原值并标注。
5. **R3 冷构建 -7% 的归因**：R3 报告将其解读为「R2 提案收益在新基线重建中保持」；但同一报告写明本轮未改代码、P1 只动测试，后续审查据此判定该 -7% 属噪声（见下条）。两种表述并列保留。
6. **后续审查对 R2/R3 收益结论的判定**（来源：`docs/rsi/review-v2.md` §3.8，2026-11-14）：
   - R2「23.012→11.559s（-50%）」**不成立**：23.012s 是首跑含测试二进制编译，11.559s 是温 build cache；同机冷/温 test 26.15 vs 7.36s（3.6×），不能跨列相减。
   - R3「cold build 41.285 vs 44.417（-7.1%），P1 收益保持」**不成立**：R3 自述本轮未改代码，P1 只动测试，不可能影响 build，-7% 是噪声。
   - `analysis-r3` #14（承认 23.012 含编译、冷/温 build 38–44 vs 3.9–5.6s）被该审查评为「本轮最诚实的自我修正」。
   - 该审查建议的公平协议：固定 commit/机器/`GOMAXPROCS`/module cache；冷温分列、同列跨轮比较、绝不跨列相减；冷跑用全新 GOCACHE；每格 ≥3 次取中位并给 min/max；A/B 交错；编译与执行分开；记录负载与测试数。
7. **P1 收益幅度的口径分歧**：R2 实测 -23%（7.696→5.911）；`review-v2` 在 16 核 amd64 上受控 A/B 得 daemon 中位 3.615→3.44s（-4.8%）、全量配对仅省 0.56s，判「全量收益不可归因」，并要求删掉“-35%/-25%”口径。该结论属机器差异与口径问题，见 `docs/rsi/int-v2-review-fix.md`。

## 附录 A：复现命令与原始输出（各轮节选）

### A.1 R1（`0bf354e`）

```bash
# 环境
$ go version && uname -m
go version go1.26.4 linux/arm64
aarch64

# 冷构建（独立 GOCACHE，基线 0bf354e）
$ { time env GOCACHE=/tmp/opencode/gocache-verify go build ./... ; } 2>&1
real    0m40.627s
user    2m17.678s
sys     0m23.475s

# 温构建复跑验证
$ { time go build ./... ; } 2>&1
real    0m3.889s

# 测试（-count=1 禁用缓存，JSON 输出解析包级耗时与失败用例）
$ { time go test ./... -count=1 -json > /tmp/opencode/test-r1v2.json ; } 2>&1
real    0m11.279s
（exit=1，internal/service 失败）

# 失败用例（-json 事件流提取）
FAIL 0.27s internal/service :: TestCodesMatchRemoteTermJSON
FAIL 0.26s internal/service :: TestCodesMatchRemoteTermJSON/...zh-cn/term.json
FAIL 0.01s internal/service :: TestCodesMatchRemoteTermJSON/...en-us/term.json
missing codes: AW.10000025 ~ AW.10000030

# 代码零改动：git status 仅本报告文件（新增），无任何源码修改
```

### A.2 R2（`365e108`）

```bash
# 基线挂载（只读，不改代码）
git worktree add --detach /tmp/opencode/bench-r2 feat-25da0cc6-sg-23d061da
cd /tmp/opencode/bench-r2 && git log --oneline -1
# → 365e108 docs: R2 实施说明 impl-notes.md（P1+P2+P3 实施与验证记录）

# 1) go build ./... 计时
python3 /tmp/opencode/timeit.py go build ./...                    # → 4.387 / 3.670 / 5.137 s, exit 0
python3 /tmp/opencode/timeit.py env GOCACHE=/tmp/opencode/gocache-r2 go build ./...
# → 44.417 s, exit 0

# 2) go test ./... -count=1 计时
python3 /tmp/opencode/timeit.py go test ./... -count=1
# → 14.399 / 11.421 / 11.559 s, 均 exit 0；grep -c FAIL = 0

# 3) P2 skip 证据
go test ./internal/service -count=1 -v | grep -E "SKIP:|missing from"
# → --- SKIP: TestCodesMatchRemoteTermJSON/...zh-cn/term.json (0.06s) / en-us (0.01s)；缺码 AW.10000025~30 × 2 locale

# 4) 包级耗时提取
grep -E "^ok" /tmp/opencode/test-r2-run3.txt | sort -k2
# → daemon 5.911s / ouroflow-cli 4.530s / service 4.456s / notify 0.925s / issue 0.419s / backup 0.297s / link 0.075s / server 0.061s / acp 0.056s / handler 0.049s ...

# 5) P1 核对
grep -c "t.Parallel()" internal/daemon/*_test.go   # → 2+8+30+11+3+4+12 = 70
```

### A.3 R3（`e7c2572`）

```bash
# 基线确认（只读）
cd /root/.ouroflow/runs/goal/357252be…/sg/…/6d704bc6f96289e41e5fa2d06ee4131c
git log --oneline -1        # → e7c2572 docs: R1/R2 自迭代交付物迁移至新基线
git diff --stat             # → 空（未改任何代码）

# 1) 冷构建计时（GOCACHE 先清空至 0 文件）
rm -rf /tmp/r3-gocache && find /tmp/r3-gocache -type f | wc -l   # → 0
python3 /tmp/opencode/timeit.py env GOCACHE=/tmp/r3-gocache go build ./...
# → __WALL_SECONDS__=41.285 / __EXIT_CODE__=0；测后 find … | wc -l → 2402（真冷态）

# 2) 温构建三连跑
for i in 1 2 3; do python3 /tmp/opencode/timeit.py go build ./…; done
# → 10.150 / 4.672 / 4.478 s，均 exit 0

# 3) 测试三连跑
for i in 1 2 3; do python3 /tmp/opencode/timeit.py go test ./... -count=1; done
# → 16.447 / 13.173 / 11.919 s，均 exit 1；grep -c "^FAIL" 各 5 行（2 包 + 3 汇总行）

# 4) 失败用例与包级耗时提取
grep -E "^--- FAIL" /tmp/opencode/r3b-test-run3.txt
# → TestGitWorkdirAndTransferPush / TestPersistentGoalWorkdir / TestDeliverMergeIdentityNoGlobalGitConfig
#    / TestCollectDigestBatchWritesAndIsIdempotent / TestCollectDigestBatchBadManifest… / TestParallelConsultWorktreeDetaches
grep -E "^ok " /tmp/opencode/r3b-test-run3.txt | sort -k3 -rn | head -10

# 5) 根因 A 证据
git -C /root/.ouroflow/repos/dom-1 remote -v
# → origin file:///tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git（已删除）
env | grep ^OUROFLOW_HOME   # → /root/.ouroflow（全局泄漏）
# 对照：手动 bare.git + clone + push + ls-remote file:// 全部 rc=0（git 2.33.0 无问题）

# 6) 根因 B 证据（单跑复现）
go test ./internal/daemon -count=1 -run 'TestCollectDigestBatchWritesAndIsIdempotent' -v
# → INFO digest: goal g-1 batch already collected — skipping append；--- FAIL（稳定复现）
ls /root/.ouroflow/digest/g-1/   # → 1.md（跨运行共享目录残留）

# 7) 交叉印证（上轮被取消 sg-REBENCH 的残留数据，非正式采信）
grep -E "^--- FAIL" /tmp/opencode/r3-test-run1.txt   # → 同样 6 例
```

## 附录 B：事实来源索引（便于回溯）

| 事实块 | 原始出处 |
| --- | --- |
| R1 全部命令/包级/失败明细/验证输出 | `benchmark-report.md`（v2，`0bf354e`） |
| R1 v1 数字（38.4/4.4/14.5、daemon 8.17） | `reports/proposals.md` 数据源第一条（引用已删除的 R1 v1 报告） |
| R1 任务给定数字（11.574/23.012/7.696） | `benchmark-report-r2.md` §4 首行 |
| R2 P1/P2/P3 改动细节、行数、验证 | `impl-notes.md` |
| R2 命令/包级/对比/P2 证据/P1 审计 | `benchmark-report-r2.md` |
| R3 冷温双列/包级/6 例失败/根因 A·A'·B | `benchmark-report-r3.md` |
| 对 R2/R3 收益结论的否定与公平协议 | `docs/rsi/review-v2.md` §3.8（现见 `docs/rsi/int-v2-review-fix.md`） |
| R1 口径漂移的自我修正（#14） | `docs/rsi/analysis-r3.md`（现见 `docs/rsi/r3-report.md`） |