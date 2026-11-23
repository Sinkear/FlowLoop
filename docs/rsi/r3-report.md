# RSI R3 报告：过程问题清单、R1→R3 演进与提案

> 合并自：`docs/rsi/analysis-r3.md`（R3 分析报告：R2 过程暴露问题清单与新提案）、`docs/rsi/round-report-r3.md`（R3 轮次演进报告，R1→R3） ｜ 合并日期：2026-11-23 ｜ 状态：历史记录（只读） ｜ 用途：R3 轮次的单文件复盘档案——按严重度归档失败/异常、保留 R1→R3 演进与效果数字、汇总三条待评审提案与下一轮候选任务；两份原文时点不同，数字不得当作同一快照。

## 0. 来源、时点与角色

| 原文件 | 产出者 / 生成器 | 时间 | 覆盖范围 | 数据源 | 并入本文件 |
| --- | --- | --- | --- | --- | --- |
| `rsi/analysis-r3.md` | optimizer（只读分析，未改业务代码） | 2026-11-11 | R2 全部失败/异常 run（8 条）+ R1 全量复核（31 条）+ 数据类问题 + 3 条新提案 + 5 条候选任务 | daemon API `http://127.0.0.1:47813`；`rsi_round` 账本（R1 `1521efe0…` / R2 `8aa88fdd…`）；`/root/.ouroflow/daemon.log`；`/proc/449/environ`；git log（`ouroflow-int-v2` 基线 `e7c2572`） | §1、§3.1–§3.4、附录 A/D |
| `rsi/round-report-r3.md` | `scripts/rsi-round-report.sh 3` | 2026-11-11 10:50 UTC | R1/R2/R3 循环演进、R3 失败/取消 run 明细、提交清单与效果数字、跨轮指标、候选任务快照 | 同 daemon API 列表端点 + 本地 git log（`ouroflow-int`） | §2、§3.5、附录 B |

- **时点差异（关键）**：round-report 生成时 R3 goal 仍为 `active`（`run 总数 42（失败/取消 25）`，训练会话 `waiting`、0 节点）；analysis-r3 是同轮分析产物。两者不是同一时刻的快照，§2.3 的 R3 行已由原文标注「session 实时（未冻结）」。
- **互补关系**：analysis-r3 负责「根因与证据链」（run id、文件行号、复现命令），round-report 负责「结构与数字」（节点/边/关键路径/墙钟/排队、commit 清单）。两文重复处已合并，冲突处并列标注（附录 D）。
- 原文均声明只读：analysis「本报告只读产出，未改动任何业务代码」；round-report「如与平台数据不一致，以 daemon API 为准」。

## 1. R3 问题清单（按严重度）

> 严重度由本次合并按「对下一轮延续性的影响」排序（S1 阻断/回归 > S2 跨轮数据可信度 > S3 流程与可运维性 > S4 历史已解决），**不是原文字段**；每条保留原文根因、证据与状态。

### 1.1 总览

| 级别 | 编号 | 问题 | 当前状态 | 主要来源 |
| --- | --- | --- | --- | --- |
| **S1** | R3-01 | 验证子进程继承 `OUROFLOW_HOME`，击穿 `t.Setenv` 隔离（R2 验证门三连败 4 条失败 run） | 已绕过（`unset` 前缀），待正式修 → 提案 P-A | analysis #1/#2/#3/#4 |
| **S1** | R3-02 | `unset OUROFLOW_HOME` 前缀导致 `go: command not found`（exit 127）三连败；验证命令 PATH 依赖宿主 shell 配置 | 已解决（`/usr/local/bin` 软链），根因待 P-A 固化 | analysis #4b/#16 |
| **S1** | R3-03 | R2 全绿 → R3 6 例失败（2 包），全部为测试环境隔离缺陷（A/A'/B），非业务缺陷 | 待修（建议随 P-A 一并覆盖测试进程环境） | `benchmark-report-r3.md`（现 `reports/benchmark-history.md` §5.5） |
| **S2** | R3-04 | `rsi_round` 轮次编号两轮均 `round_no=1`，`delta_json` 恒 `{}`，跨轮趋势无序列 | 待修 → 提案 P-B | analysis #13 |
| **S2** | R3-05 | 基准冷/温缓存口径漂移：同基线 test 23.012s → 14.5s → 11.3s（近 2 倍）、build 冷 38–44s vs 温 3.9–5.6s（约 10 倍） | 已修（P-C 口径执行，R3 起冷/温双列） | analysis #14；round-report ② 遗留表 |
| **S3** | R3-06 | R3 提交链失败：`push feat-357252be-sg-3991ce7f` exit 1（2 次）、`git add` 撞 `.gitignore` ignored path（3 次）、`load config` SQL `Scan` NULL→string（6 次） | 部分解决/待观察（原文分类：代码/流程） | round-report ② 全量表 |
| **S3** | R3-07 | `/rsi` 端点口径不一致：AGENTS.md / goal 写 `/rsi`，实测 404 | 文档待统一（候选任务 3） | analysis #15 |
| **S3** | R3-08 | owner run `b77ba4c9…` 运行 71 分钟（12:50→14:01）无产出，人工停止重发 | 已绕过（人工），待看护 → 候选任务 5 | analysis #5 |
| **S3** | R3-09 | deliver 因 SSH 私钥权限（`0750 /root/.ssh/id_ed25519`）失败，重试依赖人工 | 已解决（人工修权限），待自动重试 → 候选任务 4 | analysis #6 |
| **S4** | R3-10 | R1 批量 `Model unavailable: opencode/muse-ember-1.0`（12 条 run 同因，5–6s 退出）烧尽重试预算 | 已解决（网关配置 + opencode 2.2.5）；已由 P3 对症 | analysis #9 |
| **S4** | R3-11 | R1 `machine offline`（7 条 run）、`git: fetch: signal: killed`、双 `ouroflow connect` 实例互抢 peer 杀在途 run | 已解决（重连 / 合并单实例 / 固化为单实例纪律） | analysis #10/#11/#12；round-report ② |
| **S4** | R3-12 | R3 前置轮 `machine offline`（7 条 run，与 R1 同机报错重复出现） | 已恢复（单实例纪律 + 重连） | round-report ② |

### 1.2 S1：阻断与回归

#### R3-01 `OUROFLOW_HOME` 泄漏进验证子进程（R2 验证门三连败的根因）

R2（goal `25da0cc684817ac4d8a685180e6bbc21`，8 条 run，4 失败 1 取消）失败明细（原文表）：

| # | run id | 角色 | 状态 | 错误摘要 | 根因 | 当前状态 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `37969c7dce3cabd75e50f98dd630555d` | owner | failed（验证门） | `go test ./... -count=1` FAIL：`TestPersistentGoalWorkdir`「bare.git does not appear to be a git repository」等 | **验证子进程继承 `OUROFLOW_HOME=/root/.ouroflow`**，`paths.Home()` 优先读该变量（`internal/paths/paths.go:33-42`），`t.Setenv("HOME")` 的测试仍指向真实数据根：共享 runsRoot 里残留指向已删除临时 bare 仓的 clone → fetch 128 | **已绕过**（domain checks 改为 `unset OUROFLOW_HOME; …` 前缀），**待正式修复 → 提案 P-A** |
| 2 | `a234f3422b5b9991ec96ccc8725bd222` | sg-IMPL att1 | failed（验证门） | `TestGitWorkdirAndTransferPush` fetch 128 + `TestDeliverMergeIdentityNoGlobalGitConfig`「deliver: fetch: exit 128」+ `TestCollectDigestBatch*`「articles = 1, want 2」 | 同上泄漏：deliver/digest/gitwork 三类 `t.Setenv` 测试全部击穿到真实 `/root/.ouroflow`（digest 断言被宿主真实 `articles.json` 污染） | 同上（已绕过，待修） |
| 3 | `176d72f22468e367261a285b0e96207f` | sg-IMPL att2 | failed（验证门） | 与 #2 完全相同的三类 FAIL | 同上（失败模式重复，重试不修复原因） | 同上（已绕过，待修） |
| 4 | `d08e376d8a129de9782bfaeea2df5a7b` | sg-IMPL att3 | failed（验证门） | 同上，第三次 | 同上；**三连败烧尽该工作项重试预算**，是「验证门三连败」本体 | 同上（已绕过，待修） |
| 5 | `b77ba4c9cef45a1d9c236e03f000100f` | owner | cancelled（stopped） | 运行 71 分钟（12:50→14:01）无产出，人工停止并重发指令 | leader 轮运行卡顿（turn 未正常结束，看护未覆盖 owner 运行） | **已绕过**（人工停止 + `3cb160d91` 重发）；候选任务 5 |
| 6 | —（非 run，deliver 阶段） | — | deliver 失败一次 | `daemon.log 10:56:53`：deliver fetch 失败「UNPROTECTED PRIVATE KEY FILE! Permissions 0750 for /root/.ssh/id_ed25519」 | 容器重启重置了 SSH 私钥权限；人工批准（10:56:51）后的自动 deliver 被拒 | **已解决**（权限修正后 11:09:01 重试 → 11:09:24 merged `feat-25da0cc6` → `ouroflow-int`）；但依赖人工，候选任务 4 |
| 7 | `b319cffb1e8b40b033554096c1cf6a95` | sg-IMPL（环境修复后） | completed | — | 验证门通过（unset 前缀生效） | ✅ 已解决 |
| 8 | `a25f178a99e4747071a657c198f8f2bb` | sg-REBENCH | completed | — | R2 复测全绿（14 包 0 FAIL） | ✅ 已解决 |

- **代码层证据（P-A 提案依据）**：`runVerifiedCmd`（`internal/daemon/verify.go:171-186`）用 `exec.CommandContext` 启动验证命令时**不裁剪环境**，子进程继承 daemon 的 `OUROFLOW_HOME=/root/.ouroflow`；`paths.Home()` 优先读该变量（`internal/paths/paths.go:33-42`）。
- **影响面**：deliver/digest/gitwork/worktree 四个 `t.Setenv("HOME")` 文件隔离击穿——gitwork 撞共享 runsRoot 里的残留 clone、digest 断言撞宿主真实 `articles.json`。
- **临时绕过的脆弱性**：靠 domain checks 里每个命令手写 `unset OUROFLOW_HOME;` 前缀（本轮 domain `cd46c8f9…` checks 已如此配置）。

#### R3-02 `unset` 前缀引入 PATH 127 三连败（P-A 的姊妹问题）

R3 前置轮（goal `357252be…`）的 3 条失败 run：

| run id | 角色 | 状态 | 错误摘要 | 根因 | 当前状态 |
| --- | --- | --- | --- | --- | --- |
| `642a21d49e023135614f71ed71be52d4` | owner att1 | failed（验证门） | `unset OUROFLOW_HOME; go build ./...` → `sh: line 1: go: command not found`（exit 127），平台自动附「acceptance-policy problem」 | **unset 前缀引入新缺陷**：daemon 进程 PATH 为 `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`（`/proc/449/environ` 实测），go 实际装在 `/usr/local/go/bin`（靠启动 shell 的 profile 别名/软链生效）；unset 后 `sh -c` 仍继承 daemon 的窄 PATH，go 不在列 → 127。`daemon.log` 实录 15:22/15:45/15:48/15:52 四次 0 秒退出 | **已解决**（人工在 `/usr/local/bin` 加 go/gofmt 软链，16:01 验证门 8s+15s 通过 → run `5dc64ab5…` verified ok）；教训：**环境变量修补要连带验证 PATH 完整性**，属 P-A 姊妹问题（P-A 落地时应同时冻结 verify 子进程 PATH 语义） |
| `e83e70cf75a31c35742fc19fe2373247` | sg att1 | failed（验证门） | 同上 | 同上 | 同上 |
| `f1182ff5ffcfabd38d39f7085eba8bae` | sg att2 | failed（验证门） | 同上 | 同上 | 同上 |

#### R3-03 R2→R3 测试健康回归（6 例，2 包）

来源：`benchmark-report-r3.md` §5/§6（现 `reports/benchmark-history.md` §5.4–§5.5）。6 例失败**全部为测试环境隔离缺陷**：

- **根因 A**（#1 `TestGitWorkdirAndTransferPush`、#2 `TestPersistentGoalWorkdir`，`cmd/ouroflow-cli`）：`OUROFLOW_HOME` 全局泄漏 + 固定 domainID `dom-1` 的陈旧 bare 仓复用（`ensureBareRepo` 只检查 `HEAD` 存在即跳过 re-clone）→ fetch 128。
- **根因 A'**（#3 `TestDeliverMergeIdentityNoGlobalGitConfig`、#4 `TestParallelConsultWorktreeDetaches`，`internal/daemon`）：同源，daemon 侧 deliver/worktree 走真实 HOME 下仓库。
- **根因 B**（#5 `TestCollectDigestBatchWritesAndIsIdempotent`、#6 `TestCollectDigestBatchBadManifestKeepsMetadatalessButNoManifestEntry`）：`digestRoot()` 落在真实 `/root/.ouroflow/digest`，幂等检查命中上一轮 `g-1` 残留 → `articles = 1, want 2`；单跑亦复现。
- 结论与 P-A 关联：6 例均可通过测试内 `t.Setenv("OUROFLOW_HOME", t.TempDir())` 或 `ensureBareRepo` 增加 remote 存活校验修复；业务生产路径未被证伪。修复后 test 中位才可与 R2 11.559s 直接对比。

### 1.3 S2：跨轮数据可信度

#### R3-04 `rsi_round` 轮次编号不递增（#13）

- 现象：两轮均 `round_no=1`、`trigger=terminal`。
- 证据：`rsi_round` 两行（R1 `1521efe0374196cf889d8e53f15b01d1` / R2 `8aa88fdd89937757d9b449e11efd3e6e`，`/rsi/sessions/{id}/rounds` 实测两行 `round_no` 均 1）；`/rsi/overview` trend 两条 `round_no=1`；`delta_json` 均为 `{}`。
- 根因（原文）：`internal/service/rsi_analyze.go:129-130` 中 `round_no = MAX(round_no)+1` 按 `session_id` 取值，而每个 train goal 终态生成**新的** training_session（R1 session `371a5055…`、R2 session `f31c3a6c…`），编号永远从 1 重来。
- 状态：待修 → 提案 P-B。后续 `review-v2` 判定「不是代码 bug，是工作流设计问题」，见 `docs/rsi/int-v2-review-fix.md` §2.2。

#### R3-05 基准口径漂移（#14）

- 现象：同一基线（`33f16dc`/`0bf354e`）test 实测 23.012s（首跑，含测试二进制编译）→ 14.5s（sg-B2 复跑）→ 11.3s（R1 冻结口径），差近 2 倍；build 冷 38–44s vs 温 3.9–5.6s，差约 10 倍。
- 证据：run `25c28dc347ef`（23.012s）vs `3c3289b01724`（14.5s）；`reports/benchmark-report-r2.md` 冷 44.417s / 温中位 4.387s。
- 原文判定：R1→R2 报告里「测试 23.0s→11.6s、-50%」混入大量缓存口径噪音，跨轮对比不可信。
- 状态：待修 → 提案 P-C（R3 sg-REBENCH 派发词已按冷缓存口径，属约定层修复）。R3 复测（`8295f7a`）已按冷/温双列产出，跨轮对比采冷列（R2 44.417s → R3 41.285s，约 -7%）。

### 1.4 S3：流程与可运维性

#### R3-06 R3 提交链失败（round-report ② 原表，25 条中的 11 条）

| 类别 | run id（节选） | 错误摘要（原文） | 根因分类（原文） |
| --- | --- | --- | --- |
| push 失败 | `9eb08fde37a0…`（att1 09:01）、`92fc64e0ad97…`（att2 09:05） | `commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/ouroflow.git` | 代码 |
| `git add` 撞检查 | `df9dcb5fe559…`（att3 09:33）、`c42f34a7dbf3…`（att1 09:57）、`91fe6b993743…`（att4 10:46） | `commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files:` | 代码 |
| SQL 加载失败 | `d7a1a90c7ad2…`（att1）、`9decf713dee2…`（att1）、`ccedfe866595…`（att2）、`057b1a71533f…`（att2）、`303f02e65498…`（att3）、`6e8220574d9f…`（att3），均 06:35 | `load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported` | 流程 |

- 原文未给出这三类的进一步根因（仅启发式分类）；其中 `git add` 撞 `.gitignore` 与后续 `review-v2` 的 `ae12054` 修复（`check-ignore` 守卫）同源，见 `docs/rsi/int-v2-review-fix.md` §2.2。
- 另有 7 条 `machine offline`（`7a9f0ad5301c…`、`941d8a6e14da…`、`8052fa405818…`、`d2d5065cfd0e…`、`31e5666d9c3a…`、`2b88eab9a066…`、`812174d809ee…`，均 06:46，machine `9f00055a-89c8-4130-8f44-24ea2166e049`）+ 1 条 `verification failed:`（`f4c76ca53811…`，att2 06:46）+ 3 条 cancelled（`a32bea54e21e…` 07:19、`e1121ff1bf1f…` 07:21、`3a46ce6d6a04…` 07:23），全量明细见附录 B。

#### R3-07 `/rsi` 端点口径不一致（#15）

- 现象：AGENTS.md / goal 描述写 `/rsi`，实测 404（`curl /rsi` → "404 page not found"）；实际可用端点为 `/rsi/sessions`、`/rsi/sessions/{id}`、`/rsi/sessions/{id}/rounds`、`/rsi/overview`。
- 历史：R1 阻塞汇报（`3baf73cff449`）已附带发现；R3 再次踩到。
- 状态：文档/别名级待修（候选任务 3）；后续 round-report 已按真实端点实施。

#### R3-08 owner 运行 71 分钟无产出（#5）

- run `b77ba4c9cef45a1d9c236e03f000100f`，12:50→14:01，人工停止并重发（`3cb160d91`）。
- 根因（原文）：leader 轮运行卡顿，turn 未正常结束、看护未覆盖 owner 运行。候选任务 5。

#### R3-09 deliver 因 SSH 私钥权限失败（#6）

- `daemon.log 10:56:53`：`UNPROTECTED PRIVATE KEY FILE! Permissions 0750 for /root/.ssh/id_ed25519`；容器重启重置权限，人工批准（10:56:51）后的自动 deliver 被拒。
- 已解决：11:09:01 重试 → 11:09:24 merged `feat-25da0cc6` → `ouroflow-int`；期间依赖人工（候选任务 4）。

### 1.5 S4：历史失败（已解决，保留教训）

R1（goal `3ec2bfc64b364e7ee24c5f91b67e4c22`，31 条 run，15 失败 3 取消；按 API 全量复核）：

| # | run id（原文节选） | 角色 | 状态 | 错误摘要 | 根因 | 当前状态 |
| --- | --- | --- | --- | --- | --- | --- |
| 9 | `8318c5cc…`（bench att1-3，含 `634c78a8…`、`9cd850bf…`）、`899a791b…`、`e65c9c3c…`、`c9d79653…`、`9922892c…`、`75415878…`、`ceffb017…`、`4716f2f2…`、`f97a5701…`、`e068d49d…` | bench/owner | failed | `Model unavailable: opencode/muse-ember-1.0`，每次 5-6s 即退出 | 新建会话拿到默认模型不在网关清单；凡新建会话的成员任务首 prompt 必败，重试预算被确定性错误烧光（12 条 run） | **已解决**（opengw 网关配置 + opencode 2.2.5 升级）；该教训已由 P3 不可重试快速失败对症 |
| 10 | `75dbf852eaa1af4e3b2e7d63360f5f4a` | owner | failed | `machine offline — the machine must run ouroflow connect` | 连接中断 | **已解决**（重连） |
| 11 | `3dec40838070ad66c2ad1eae508ed691` | owner | failed | `git: fetch: signal: killed` | fetch 被杀（连接/资源竞争期间） | **已解决**（随 #12 修复） |
| 12 | `0ae48700b684d04e4c77395326733b33`（att2，cancelled）、`e1ca8d3faa7f87c52248dbbce7d8f008`（att1，cancelled）、`326129752dd07417876b7f7b958b580d`（att3，failed「cancelled by platform」）、`deaee148a46b066ccadfc8b79d672c7c`（failed「git: worktree add: context canceled」） | owner | cancelled/failed | 在途 run 被平台取消/打断 | **双 `ouroflow connect` 实例互抢 peer**，杀掉在途 run | **已解决**（人工合并为单实例，见 `204344e947f7` 评论；已固化为单实例纪律） |

- R1 的 12 条同因 run 与 reports 侧「23 条 run 中 14 failed、12 条同因」的早期统计口径差异见附录 D。
- 另：`rsi_round` 编号问题（#13）、基准口径（#14）、`/rsi` 404（#15）三项数据类问题已在 §1.3/§1.4 列出，原文归类为「非 run 失败，但直接伤害可感知性」。

## 2. R1→R3 演进与效果数字

### 2.1 循环演进视图（round-report §1）

每轮是一个 train 模式 goal；以下汇总自 rsi 训练会话冻结的工作流（nodes/edges/依赖）。

#### R1：RSI R1：ouroflow 自迭代首轮（基准→提案）

- goal 终态：`done` · run 总数 31（失败/取消 18）
- 训练会话 `371a50558cf9…`：status=`ready` · 3 节点 · 关键路径 45.6s · 首要发现：重试放大：平均每节点 3.5 次 run
  - 图结构：3 节点 / 3 边（agent 节点 2，集成节点 0）
    - `sg:b757e167e34` sg-B：构建/测试基准测量：failed · attempt 6 · 耗时 45.6s · 依赖 → 1 个节点
    - `sg:751a53ebdf0` sg-O：R1 优化提案（只读）：running · attempt 1 · 耗时 —
    - 依赖边：`sg:b757e167e34` → `sg:751a53ebdf0`

#### R2：RSI R2：实施已批准提案（P1+P2+P3）

- goal 终态：`done` · run 总数 8（失败/取消 5）
- 训练会话 `f31c3a6c4508…`：status=`ready` · 5 节点 · 关键路径 41.6m · 首要发现：重试放大：平均每节点 2.5 次 run
  - 图结构：5 节点 / 5 边（agent 节点 2，集成节点 2）
    - `sg:23d061daa9f` sg-IMPL：实施已批准提案 P1+P2+P3：verified · attempt 4 · 耗时 31.4m · 依赖 → 1 个节点
    - `sg:b8584aabcfb` sg-REBENCH：实施后基准复测：verified · attempt 1 · 耗时 10.2m
    - 依赖边：`sg:23d061daa9f` → `sg:b8584aabcfb`

#### R3：RSI R3：可感知性改进（演进报告机制）

- goal 终态：`active` · run 总数 42（失败/取消 25）
- 训练会话 `4f5193ccc3d0…`：status=`waiting` · 0 节点 · 关键路径 — · 首要发现：（无）
  - 图结构：14 节点 / 22 边（agent 节点 7，集成节点 3）
    - `sg:0e9ad6d5401` sg-IMPL：轮次演进报告生成器：cancelled · attempt 1 · 耗时 97.5m · 依赖 → 2 个节点
    - `sg:c1e064464d4` sg-ANALYZE：R2 问题清单与新提案：verified · attempt 3 · 耗时 55.3m · 依赖 → 1 个节点
    - `sg:3edbe2b48d3` sg-REPORT：生成首份正式演进报告：cancelled · attempt 1 · 耗时 —
    - `sg:52b00d7d4a4` sg-REBENCH：R3 基准复测：cancelled · attempt 1 · 耗时 —
    - `sg:3991ce7f016` sg-IMPL-v2：轮次演进报告生成器（重派·执行角色）：failed · attempt 6 · 耗时 70.3m
    - `sg:b13a558163d` sg-REBENCH-v2：R3 基准复测（冷缓存口径·无依赖并行）：verified · attempt 1 · 耗时 18.6m
    - `sg:49c2619baec` sg-IMPL-v3：轮次演进报告生成器（重派·复用 sg-REBENCH-v2 已合入基点）：cancelled · attempt 1 · 耗时 12.9m
    - 依赖边：`sg:0e9ad6d5401` → `sg:3edbe2b48d3`；`sg:c1e064464d4` → `sg:3edbe2b48d3`；`sg:0e9ad6d5401` → `sg:52b00d7d4a4`
- 训练会话 `079dc56e368e…`：status=`waiting` · 0 节点 · 关键路径 — · 首要发现：（无）
  - 图结构：7 节点 / 13 边（agent 节点 6，集成节点 0）
    - `sg:d1b7f848f0c` sg-IMPL：轮次演进报告生成器：failed · attempt 3 · 耗时 0.0s · 依赖 → 2 个节点
    - `sg:7d094326186` sg-ANALYZE：R2 问题清单与新提案：failed · attempt 3 · 耗时 0.0s · 依赖 → 1 个节点
    - `sg:47976318991` sg-REPORT：生成首份正式演进报告：cancelled · attempt 1 · 耗时 —
    - `sg:93ebd95d3cb` sg-REBENCH：R3 基准复测：cancelled · attempt 1 · 耗时 —
    - `sg:18e22480125` sg-IMPL（重派）：轮次演进报告生成器：cancelled · attempt 2 · 耗时 8.8m
    - `sg:2234fefdd37` sg-REBENCH（重派）：R3 基准复测：cancelled · attempt 2 · 耗时 7.6m
    - 依赖边：`sg:d1b7f848f0c` → `sg:47976318991`；`sg:7d094326186` → `sg:47976318991`；`sg:d1b7f848f0c` → `sg:93ebd95d3cb`

> 原文注：本轮（R3）在报告生成时通常仍在进行；收尾时重跑生成器即得终版。R3 有两个训练会话（`4f5193c…` 14/22 与 `079dc56…` 7/13），§2.3 的 R3 行取前者实时 metrics。

### 2.2 跨轮指标对比（round-report §4）

汇总自各轮 rsi 训练会话冻结的 metrics（`/rsi/sessions/{id}/rounds` 账本；session 未冻结时回退 `/rsi/sessions/{id}` 实时 `workflow.metrics`，并已标注）。
注意：R1/R2 的 `rsi_round` 账本 `round_no` 均为 1（提案 P-B 待修），故下表按「goal 标题里的轮次号」对齐，而非账本 `round_no`。

| 轮 | 指标来源 | 节点数 | 边数 | 关键路径 | 墙钟 | 工作总量 | 排队等待 | 并行收益 | lane 数 | lane 失衡 | 首要发现 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| R1 | rounds 账本 | 3 | 3 | 45.6s | 175.4m | 45.6s | 0.3s | 1 | 1 | 1 | 重试放大：平均每节点 3.5 次 run |
| R2 | rounds 账本 | 5 | 5 | 41.6m | 46.8m | 41.6m | 36.6m | 1 | 2 | 1.51 | 重试放大：平均每节点 2.5 次 run |
| R3 | session 实时（未冻结） | 14 | 22 | 142.1m | 197.2m | 320.5m | 1.2s | 2.26 | 3 | 2.31 | — |

解读提示（原文，含 bench R3 复测结论）：

- `重试放大`（top finding）从 R1「平均每节点 3.5 次 run」降到 R2「2.5 次」，仍是两轮共同首要发现。
- R1/R2 的 `wall_ms`/`critical_path` 差异部分来自基准任务规格不同（R1 纯基准、R2 含实施），跨轮结论看趋势不看点值。
- 基准口径（P-C）：跨轮对比 build 只采冷列——R2 基线 44.417s → R3 新基线 `e7c2572` 41.285s（约 -7%），R2 已批准提案收益在新基线重建中得到保持；温列（R2 中位 4.387s / R3 中位 4.672s）仅参照不采信。
- 测试健康：R2 全绿 → R3 复测 6 例失败（2 包），全部为测试环境隔离缺陷（`OUROFLOW_HOME` 全局泄漏击穿 `t.Setenv` 隔离 + digest 跨运行残留），非业务代码缺陷；机器验证门以 `unset OUROFLOW_HOME;` 前缀口径运行为全绿。
- R3 行标注「session 实时（未冻结）」表示该会话尚未冻结、数字取自训练页同源的实时 `workflow.metrics`，收尾冻结后重跑生成器即切换为 rounds 账本口径。
- 后续审查（`review-v2` §3.8）判定「R3 build -7% = P1 收益保持」不成立（本轮未改代码，P1 只动测试，-7% 为噪声）；两种表述并列保留，见附录 D。

### 2.3 R3 权威基准数字（引自 `reports/benchmark-history.md` §5）

| 指标 | 值 |
| --- | --- |
| 基线 | `e7c2572`（`feat-357252be-sg-b13a5581` HEAD = `origin/ouroflow-int-v2`；近三代 `e7c2572` + `e08cb72` P1 重放 + `e2bda1d` P2/P3 重放） |
| `go build ./...` 冷 | **41.285 s**（R2 冷测 44.417s，-7.1%） |
| `go build ./...` 温中位 | 4.672 s（10.150/4.672/4.478；R2 4.387s，+6.5%） |
| `go test ./... -count=1` 中位 | 13.173 s（16.447/13.173/11.919；R2 11.559s，+14.0%） |
| `internal/daemon` 中位 | 5.855 s（run3 6.026 s，FAIL） |
| `cmd/ouroflow-cli` 中位 | 2.374 s（失败提前退出，不采信为收益） |
| `internal/service` 中位 | 6.144 s（原文；其三连跑 6.200/6.004/6.229，run3 6.229 s pass） |
| 失败 | 6 例 / 2 包（`cmd/ouroflow-cli` 2、`internal/daemon` 4），全部环境隔离缺陷 |
| 包数 | 30（12 ok + 2 FAIL + 16 无测试文件） |

### 2.4 改进点与提交清单（round-report §3）

改进点 = 本轮实施并合入集成分支的已批准提案；commit 链接指向仓库 commit 页，效果数字来自实施前后的基准复测。

**报告所列「本轮（R3）实施提交」（15 条，原文顺序；范围混杂问题见附录 D-4）：**

- [`81e5e94`](https://github.com/Sinkear/ouroflow/commit/81e5e94) ouroflow run a44eecac7bfa28f6996ab8a9372ba367
- [`90bcc13`](https://github.com/Sinkear/ouroflow/commit/90bcc13) docs(rsi): 对齐最新集成基点 dfcd444 重新生成 R3 演进报告（R3-1 rework）
- [`3ac0b1f`](https://github.com/Sinkear/ouroflow/commit/3ac0b1f) fix(rsi): 协作约定迁出根级 AGENTS.md，根治集成冲突（R3-1 修订）
- [`a47f403`](https://github.com/Sinkear/ouroflow/commit/a47f403) docs(rsi): 对齐新集成基点——报告吸收 bench R3 复测权威数字（解决集成冲突）
- [`c3af19e`](https://github.com/Sinkear/ouroflow/commit/c3af19e) docs(rsi): R3 轮次演进报告回填本工作项 commit 链接
- [`d5b4651`](https://github.com/Sinkear/ouroflow/commit/d5b4651) feat(rsi): 轮次演进报告生成器 + R3 首份演进报告 + AGENTS.md 收尾约定（R3-1）
- [`dfcd444`](https://github.com/Sinkear/ouroflow/commit/dfcd444) Integrate 12e9a4dd631b729c2734f3cf0f174ec9
- [`8295f7a`](https://github.com/Sinkear/ouroflow/commit/8295f7a) bench: R3 基准复测报告（冷缓存口径·冷/温双列·6 例测试隔离缺陷根因归类）
- [`0bb81bb`](https://github.com/Sinkear/ouroflow/commit/0bb81bb) Integrate 2c5f00af0966f59d50db0616a16f412c
- [`d420946`](https://github.com/Sinkear/ouroflow/commit/d420946) docs(rsi): R3 分析复核——补充 R3 前置轮 PATH 127 三连败（642a21d4/e83e70cf/f1182ff5）为问题 #4b/#16，P-A 增补 verify 子进程 PATH 语义探测，P-B 补 prevMetrics 跨轮读取行号修正
- [`c3126ff`](https://github.com/Sinkear/ouroflow/commit/c3126ff) docs(rsi): R3 分析复核——按 API 全量修正 R1 头部统计（15 failed/3 cancelled）并补齐 634c78a8/9cd850bf/326129752 三条遗漏 run
- [`eab6b4a`](https://github.com/Sinkear/ouroflow/commit/eab6b4a) docs(rsi): R3 分析报告——R2 过程暴露问题清单 + 3 条新提案 + 下一轮候选任务
- [`ff797d2`](https://github.com/Sinkear/ouroflow/commit/ff797d2) docs(rsi): R3 分析——R2 全量失败 run 清单与三份新提案（OUROFLOW_HOME 泄漏 / rsi_round 编号 / bench 口径）
- [`e7c2572`](https://github.com/Sinkear/ouroflow/commit/e7c2572) docs: R1/R2 自迭代交付物迁移至新基线（proposals/benchmark/impl-notes）
- [`e08cb72`](https://github.com/Sinkear/ouroflow/commit/e08cb72) test(P1): 并行化 internal/daemon 独立顶层测试（R2 提案 1 在新基线重放）

**集成分支 `ouroflow-int` 近期提交（含历轮改进，12 条）：**

- [`33f16dc`](https://github.com/Sinkear/ouroflow/commit/33f16dc) Merge feat-98d60d12 (ouroflow deliver)
- [`752f21f`](https://github.com/Sinkear/ouroflow/commit/752f21f) ouroflow run 16d4e354c224d8d3488c20792f637697
- [`3aecbef`](https://github.com/Sinkear/ouroflow/commit/3aecbef) Merge pull request #2 from Sinkear/fix/bugs
- [`35b1518`](https://github.com/Sinkear/ouroflow/commit/35b1518) webhook ui，前端bug，总览页修复
- [`11cd194`](https://github.com/Sinkear/ouroflow/commit/11cd194) Merge pull request #1 from Sinkear/feat/template-export
- [`c23a837`](https://github.com/Sinkear/ouroflow/commit/c23a837) update artifact-swap skill
- [`29c82a9`](https://github.com/Sinkear/ouroflow/commit/29c82a9) 新增ouroflow更新部署的skill
- [`e270d36`](https://github.com/Sinkear/ouroflow/commit/e270d36) 增加模板导入导出的能力，实现自替代包优化
- [`ff84b05`](https://github.com/Sinkear/ouroflow/commit/ff84b05) chore(license): 衍生说明移到 NOTICE，LICENSE 保持标准 MIT 两行版权
- [`7ed6a91`](https://github.com/Sinkear/ouroflow/commit/7ed6a91) chore(license): 版权行合并到标准 MIT 头部，便于 GitHub 识别 MIT
- [`de0753f`](https://github.com/Sinkear/ouroflow/commit/de0753f) chore: 独立发行版基线（CI、上游补丁同步脚本、MIT 衍生声明）
- [`051d73a`](https://github.com/Sinkear/ouroflow/commit/051d73a) chore: 合并上游 master(82b0b68) 作为独立主线起点

**历轮改进点与效果数字（原文表；来源：`docs/rsi/proposals.md`、`reports/benchmark-report-r2.md`、`reports/benchmark-report-r3.md`、`reports/impl-notes.md`；口径冲突时以最新复测报告为准）：**

| 轮 | 提案 | 改进 | commit | 效果数字（before → after） |
| --- | --- | --- | --- | --- |
| R2 | P1 | 并行化 internal/daemon 独立顶层测试（70 个测试函数加 t.Parallel） | [`e08cb72`](https://github.com/Sinkear/ouroflow/commit/e08cb72) | internal/daemon 包测试中位 5.911s（R2 复测基准，P1 后保持；R3 复测 5.855s 持平）；详见 `reports/benchmark-history.md` §4.3、§5.3 |
| R2 | P2 | CDN 漂移守卫对畸形响应 fail 改 skip | [`8ca9957`](https://github.com/Sinkear/ouroflow/commit/8ca9957) | 14 包 0 FAIL（修复前该守卫对畸形响应直接 FAIL） |
| R2 | P3 | 失败重试引入不可重试错误快速失败 | [`18a8c3c`](https://github.com/Sinkear/ouroflow/commit/18a8c3c) | 确定性错误（如模型不可用）不再烧尽重试预算（R1 曾 12 条 run 同因全败） |
| R3 | R3-1 | 轮次演进报告生成器 + AGENTS.md 收尾约定 | 本工作项 commit（合入后由下轮报告回填链接） | 可感知性 5 缺口 → 5 节报告覆盖；每轮收尾强制产出 |

## 3. 提案与待办

> 三条新提案（P-A/P-B/P-C）均【待评审】，等待人工批准后方可由 builder 实施（原文状态，截至合并日未见实施记录）。圆点内「用户可感知说明」为原文保留。

### 3.1 提案 P-A【待评审】验证子进程环境净化（`OUROFLOW_HOME` 泄漏的正式修复）

- **问题依据**：R2 验证门三连败（run `a234f342…`/`176d72f2…`/`d08e376d…` + owner `37969c7d…`，共 4 条失败 run）全部同一根因：`runVerifiedCmd`（`internal/daemon/verify.go:171-186`）用 `exec.CommandContext` 启动验证命令时**不裁剪环境**，子进程继承 daemon 的 `OUROFLOW_HOME=/root/.ouroflow`；而 `paths.Home()` 优先读该变量（`internal/paths/paths.go:33-42`），导致所有 `t.Setenv("HOME")` 的测试（deliver/digest/gitwork/worktree 四个文件）隔离击穿——gitwork 撞上共享 runsRoot 里的残留 clone、digest 断言撞上宿主真实 `articles.json`。当前靠 domain checks 里每个命令手写 `unset OUROFLOW_HOME;` 前缀绕过（本轮 domain `cd46c8f9…` checks 已如此配置），属「靠每个 verify 命令记得 unset」的脆弱约定。
- **拟改动位置**：`internal/daemon/verify.go:171-186` `runVerifiedCmd`——构造 `c := exec.CommandContext(...)` 后新增 `c.Env = sanitizeVerifyEnv()`；新函数过滤 `os.Environ()` 中的 `OUROFLOW_HOME` 与 `OUROFLOW_DB` 两项（只剥离这两个改写状态根的变量，保留其余环境，避免误伤需要调 CLI 的 verify 命令）；同函数需一并固化 PATH 语义——若继承的 PATH 不含常用工具链目录（如 `/usr/local/go/bin`），在报告输出一行提示（探测 `go`/`node` 等是否存在，仅提示不改行为，避免 R3 前置轮 `642a21d4` 式 0 秒静默 127）。同步在 `internal/daemon/verify_test.go` 加回归用例：预设 `OUROFLOW_HOME` 后跑 `sh -c 'echo $OUROFLOW_HOME'` 断言输出为空。
- **预期收益**：该类验证门失败从 R2 的 4/8 条 run（50%）降为 0；domain checks 可去掉 unset 前缀（恢复策略原文 `go build ./...`）；任何域名的 verify 不再因宿主状态泄漏误红。可量化：验证门同类失败 4 → 0。
- **风险与回滚**：极低——仅影响 verify 子进程环境，不碰 agent/CLI 通道；若某域 verify 确需读宿主状态，可在 `checks.Setup` 显式 `export OUROFLOW_HOME=…` 找回。回滚 = revert 该单 commit（domain 里的 unset 前缀是幂等 no-op，回滚后行为不变）。
- **用户可感知说明**：交付前的自动质量检查不再因为「测试偷看了平台自己的数据」而莫名变红，任务一次通过率明显提高，你不用再反复点批准重试。
- **R3 复测补充**：bench R3 确认同一泄漏还击穿 go test 子进程的 `t.Setenv` 隔离（6 例失败根因 A/A'/B），P-A 实施时应一并覆盖测试进程环境。

### 3.2 提案 P-B【待评审】`rsi_round` 轮次编号按 domain 递增

- **问题依据**：`rsi_round` 账本两行 `round_no` 均为 1（R1 `1521efe0…` / R2 `8aa88fdd…`，`trigger` 均为 `terminal`），`/rsi/overview` trend 无法区分轮次，跨轮 `delta_json` 恒为空 `{}`——「跨轮指标对比」与演进报告第五节失去数据基础。根因：`internal/service/rsi_analyze.go:129-130` 中 `round_no = MAX(round_no)+1` 按 `session_id` 取值，而每个 train goal 终态生成**新的** training_session（R1 session `371a5055…`、R2 session `f31c3a6c…`），编号永远从 1 重来。
- **拟改动位置**：`internal/service/rsi_analyze.go:129-137`——MAX 查询（`:130`）改为按 domain 取：`SELECT COALESCE(MAX(r.round_no),0) FROM rsi_round r JOIN goal g ON g.id=r.goal_id WHERE g.domain_id=?`（`domain_id` 从本次分析的目标行读取；domain 为空或无历史时回退现有 session 级查询）；`prevMetrics` 读取（`:136-137`）同步按新 `prevNo` 定位上一轮行（跨 session 取上一轮 metrics，delta 才有真实基数）；`trigger` 判定（`:133-134`）同步改为 domain 级 `prevNo==0`。schema 不动：唯一索引 `uq_rsi_round_session(session_id, round_no)`（`internal/store/store.go:148`）仍成立。
- **预期收益**：R3 起轮次编号 1→2→3 连续；洞察页趋势与每轮演进报告【跨轮指标对比】首次具备可比序列；`delta_json` 开始产出真实轮间差值（如「重试放大 3.5→2.5」将直接呈现为轮间 delta 而非手工查表）。
- **风险与回滚**：低——只影响新写入行的编号；已存两行 `round_no=1` 保持原样（历史读取按 `created_at` 排序仍正确，前端无破坏）。回滚 = revert 单 commit，编号回到 session 级。
- **用户可感知说明**：训练分析页的「轮次趋势」从此能看到 1→2→3 的连续编号和每轮之间的真实变化，而不是每轮都叫「第 1 轮」。

### 3.3 提案 P-C【待评审】bench 基准统一冷缓存口径

- **问题依据**：同一基线（`ouroflow-int` HEAD `33f16dc`）上，`go test ./... -count=1` 首跑实测 23.012s（run `25c28dc347ef`，含全部测试二进制编译），复跑降到 14.5s（run `3c3289b01724`）；`go build ./...` 冷 38-44s vs 温 3.9-5.6s 差约 10 倍（`reports/benchmark-report-r2.md`）。R1→R2 报告里「测试 23.0s→11.6s、-50%」的结论混入了大量缓存口径噪音（`docs/rsi-round-evolution-report.md` 问题 #8 已自认），跨轮对比不可信。
- **拟改动位置**：纯约定层，不改业务代码——① `docs/rsi/self-iter-playbook.md`「每轮操作」小节固化口径：build 必测冷（独立 GOCACHE，如 `GOCACHE=$(mktemp -d) go build ./...`）+ 温中位各一列；test 以温 3 连跑中位为准并注明首跑编译已完成的口径；② 所有基准报告必须同时记录冷/温两列与 GOCACHE 路径；③ R3 的 sg-REBENCH 派发词已按此口径执行（`GOCACHE=/tmp/r3-gocache` 冷构建），本轮即为首个统一口径样本。下一轮可再落 `scripts/rsi-bench.sh` 统一计时（builder 实施）。
- **预期收益**：跨轮 build 对比噪音从 ±50%（23.0 vs 14.5 同基线）收敛到冷/温分列后各自 ±10% 内；演进报告【跨轮指标对比】的百分比首次可信，人工批准提案时有真实数字依据。
- **风险与回滚**：无代码风险，仅测量与文档约定；口径写错回滚 = 改回 playbook 文档即可。
- **用户可感知说明**：每轮报告里「这轮比上轮快了百分之多少」从此是真实改进，不再混入缓存冷热的假象，你看报告做批准决策更有依据。

### 3.4 下一轮候选任务清单（按优先级，供人工挑选成为 R4 任务）

1. **实施 P-A：验证子进程环境净化**（高优）——依据：R2 验证门三连败 4 条失败 run 同根因，且 R3 前置轮 unset 前缀又引入 PATH 三连败（#4b），验证子进程环境已是两轮最高频失败源；收益：同类验证门失败 4+3 → 0，domain checks 可简化；改动集中在 `verify.go` 一个函数 + 1 个回归测试，工作量最小。
2. **实施 P-B：`rsi_round` 按 domain 递增**（中优）——依据：两轮 `round_no` 均为 1、delta 恒空；收益：R4 报告【跨轮指标对比】首次有真序列；改动一条 SQL + trigger 判定 + prevMetrics 跨轮读取，随带回归验证 `/rsi/overview` trend。
3. **`/rsi` 端点口径修正**（低成本高一致性）——依据：AGENTS.md/playbook/goal 描述写的 `/rsi` 实测 404（R1 `3baf73cff449` 已附带发现，R3 再次踩到）；改法二选一：补文档为 `/rsi/sessions` 等真实端点，或 handler 加 1 行 `GET /rsi` 别名指向 overview。
4. **deliver 失败自动重试/退避**（中优）——依据：R2 人工批准后 deliver 因 SSH 权限失败一次（`daemon.log 2026-11-11 10:56:53`），直到 11:09:01 才重试成功，期间依赖人工推进；改法：`deliverGoal` 外层加一次带延迟的自动重试（可重试错误白名单复用 P3 的 `isNonRetryable` 思路，SSH 权限类修正后可重试）。
5. **owner/leader 运行卡顿看护**（中优）——依据：R2 owner run `b77ba4c9…` 运行 71 分钟（12:50:33→14:01:25）无产出被人工停止；改法：把 watchdog idle-notify/最大时长看护明确覆盖 owner 运行（目前主要命中 worker），超时先通知人工而非静默挂起。

### 3.5 R3 收尾遗留问题状态（round-report §2 表）

承接 analysis-r3 的问题清单，本轮收尾时状态如下（原文，由本节人工/后续轮更新结论列）：

| 问题 | 上轮状态 | 本轮收尾状态 |
| --- | --- | --- |
| `OUROFLOW_HOME` 泄漏进验证子进程（R2 验证门三连败根因） | 待修（提案 P-A） | 待评审；bench R3 复测确认同一泄漏还击穿 go test 子进程的 `t.Setenv` 隔离（6 例失败根因 A/A'/B），P-A 实施时应一并覆盖测试进程环境 |
| `rsi_round` 轮次编号不按 domain 递增（两轮均 `round_no=1`） | 待修（提案 P-B） | 待评审 |
| 基准冷/温缓存口径漂移（跨轮对比混入缓存噪音） | 待修（提案 P-C） | P-C 口径已执行：R3 复测（`8295f7a`）按冷/温双列产出，跨轮对比采冷列（R2 44.417s → R3 41.285s，约 -7%） |
| `/rsi` 端点口径不一致（文档写 `/rsi`，实测 404） | 待修（候选任务 3） | 本报告已按真实端点实施，文档口径待统一 |
| 验证命令 PATH 依赖宿主 shell 配置（go 软链缺失三连败） | 已解决（`/usr/local/bin` 软链） | 已解决，P-A 落地时固化 PATH 语义 |
| deliver 因 SSH 私钥权限失败、依赖人工重试 | 已解决（人工修权限） | 待修（候选任务 4：deliver 自动重试） |
| owner 运行 71 分钟无产出无看护 | 已绕过（人工停止） | 待修（候选任务 5：owner 看护） |

## 4. 附录

### 附录 A：数据来源与端点（原文）

- **daemon API**：`http://127.0.0.1:47813` 的 `/goals`、`/goals/{id}/runs`、`/goals/{id}/comments`、`/rsi/sessions`、`/rsi/sessions/{id}`、`/rsi/sessions/{id}/rounds`、`/rsi/overview`。
- **端点纠正**：goal 描述与 AGENTS.md 中写的 `/rsi` 实测 404；实际可用端点为上述 4 个 `/rsi/...` 端点（已列入问题 #15）。`/runs/{id}` 单查端点亦为 404（`self-iter-playbook.md` §6 已注明）。
- **账本**：`rsi_round` R1 `1521efe0374196cf889d8e53f15b01d1` / R2 `8aa88fdd89937757d9b449e11efd3e6e`。
- **goal id**：R1 `3ec2bfc64b364e7ee24c5f91b67e4c22`（31 run）；R2 `25da0cc684817ac4d8a685180e6bbc21`（8 run）；R3 `357252be…`（round-report 生成时 42 run）。
- **训练会话**：R1 `371a50558cf9…`（3 节点/3 边，ready）；R2 `f31c3a6c4508…`（5 节点/5 边，ready）；R3 `4f5193ccc3d0…`（14 节点/22 边，waiting）与 `079dc56e368e…`（7 节点/13 边，waiting）。
- **日志/环境**：`/root/.ouroflow/daemon.log`；daemon 进程环境 `/proc/449/environ`；git log（`ouroflow-int-v2` 基线 `e7c2572`）。
- **round-report 生成器**：`scripts/rsi-round-report.sh 3`，生成时间 `2026-11-11 10:50 UTC`；固定五节【① 本轮概览】【② 失败与取消】【③ 提交清单】【④ 指标】【⑤ 候选项】。

### 附录 B：R3 失败/取消 run 全量明细（round-report §2 原表，25 条）

| run id | 状态 | 角色 | attempt | 时间（2026-11-11） | 错误摘要 | 根因分类 |
| --- | --- | --- | --- | --- | --- | --- |
| `642a21d49e02…` | failed | worker | 1 | 07:31 | verification failed: | 环境 |
| `e83e70cf75a3…` | failed | worker | 1 | 07:31 | verification failed: | 环境 |
| `f1182ff5ffcf…` | failed | worker | 2 | 07:48 | verification failed: | 环境 |
| `9eb08fde37a0…` | failed | worker | 1 | 09:01 | commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/ouroflow.git | 代码 |
| `92fc64e0ad97…` | failed | worker | 2 | 09:05 | commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/ouroflow.git | 代码 |
| `df9dcb5fe559…` | failed | worker | 3 | 09:33 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `c42f34a7dbf3…` | failed | worker | 1 | 09:57 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `91fe6b993743…` | failed | worker | 4 | 10:46 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `7a9f0ad5301c…` | failed | worker | 1 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `941d8a6e14da…` | failed | worker | 1 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `8052fa405818…` | failed | worker | 1 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `d2d5065cfd0e…` | failed | worker | 2 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `31e5666d9c3a…` | failed | worker | 2 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `2b88eab9a066…` | failed | worker | 3 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `812174d809ee…` | failed | worker | 3 | 06:46 | machine offline — the machine must run `ouroflow connect` (machine 9f00055a-…) | 环境 |
| `f4c76ca53811…` | failed | worker | 2 | 06:46 | verification failed: | 环境 |
| `a32bea54e21e…` | cancelled | worker | 1 | 07:19 | (被取消，无错误摘要) | 流程 |
| `e1121ff1bf1f…` | cancelled | worker | 1 | 07:21 | (被取消，无错误摘要) | 流程 |
| `3a46ce6d6a04…` | cancelled | worker | 3 | 07:23 | (被取消，无错误摘要) | 流程 |
| `d7a1a90c7ad2…` | failed | worker | 1 | 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `9decf713dee2…` | failed | worker | 1 | 06:35 | 同上 | 流程 |
| `ccedfe866595…` | failed | worker | 2 | 06:35 | 同上 | 流程 |
| `057b1a71533f…` | failed | worker | 2 | 06:35 | 同上 | 流程 |
| `303f02e65498…` | failed | worker | 3 | 06:35 | 同上 | 流程 |
| `6e8220574d9f…` | failed | worker | 3 | 06:35 | 同上 | 流程 |

- 原文注：根因分类为生成器启发式（环境/代码/流程），供人工复核。

### 附录 C：R1/R2 失败 run 明细位置

- R1 汇总（31 run / 15 failed / 3 cancelled）与同类 run 清单见 §1.5。
- R2 全量 8-run 清单见 §1.2 R3-01 表（4 失败 + 1 取消 + 3 completed；另 deliver 阶段失败 1 次为表 #6）。
- R3 全量 25 条见附录 B。

### 附录 D：合并时发现的矛盾、口径差异与存疑（保留原文，不做裁定）

1. **「R1 基线」存在两套互不相同的指标快照**：
   - `docs/rsi/proposals.md`（round-1，session `e9f5c086…`，goal `98d60d12`，workflow 冻结于 11:08:15Z）：`critical_path_ms=293,501`、`wall_ms=293,501`、`work_sum_ms=293,501`、`queue_wait_ms=294,330`（≈墙钟 100.3%）、`parallel_speedup=1.00`、`rework_ratio=0`、`verify_overhead_ratio=0`、3 节点/3 边。
   - round-report R1（session `371a5055…`，goal `3ec2bfc6…`）：关键路径 45.6s、墙钟 175.4m、排队 0.3s、`parallel_speedup=1`、3 节点/3 边、首要发现「重试放大 3.5 次/节点」。
   - 两者都自称 R1，但 session/goal/数值完全不同；应为 R1 内部不同时点或不同 session 的快照。**不可当作同一基线混用**；引用时必须带 session id。
2. **R1 失败统计分母不一致**：`reports/proposals.md` 记「平台 runs API 全量 23 条 run：14 failed 中 12 条同因」；analysis-r3 按 API 全量复核后为「31 条 run，15 失败 3 取消」；`review-v2` §3.3 亦指出「12/14 分母已过期，仓库后审计是 31 run / 15 failed / 3 cancelled（12/15）」。本报告采用 analysis 的 15 failed 口径（§1.5），并保留 23/14 的早期记录于 `docs/rsi/r1-proposals.md`。
3. **R3 指标为未冻结的实时值**：round-report 生成时 R3 goal 仍 `active`、两个训练会话均 `waiting`、0 节点；§2.2 的 R3 行（14 节点/22 边/CP 142.1m/墙钟 197.2m）来自实时 `workflow.metrics`。收尾冻结后重跑生成器会切换为 rounds 账本口径，届时数字可能变化。
4. **round-report §3「本轮（R3）实施提交」范围混杂**：列表含 R1/R2 迁移与重放提交（`e7c2572`、`e08cb72`）、bench 报告（`8295f7a`）、集成提交（`dfcd444`、`0bb81bb`）与 R3 文档/生成器提交。原文按「本轮相关提交」列出，不是严格的 R3 增量 commit 集。
5. **R3 前置轮失败的记录粒度不同**：round-report 附录 B 记为 07:31/07:31/07:48 三条 `verification failed:`（无正文）；analysis R3-02 记为 `go: command not found` exit 127，并引 `daemon.log` 15:22/15:45/15:48/15:52 四次 0 秒退出。同一组 run 的详细诊断只在 analysis 侧，且「4 次日志退出 vs 3 条 run」数量不一致（可能含更早一次尝试）；两说并列保留。
6. **R3 test 中位 6.144s 复算不符**：§2.3 保留原文 `internal/service` 中位 6.144s，但其列出的三连跑值为 6.200/6.004/6.229（中位应为 6.200），run3 为 6.229s。原值无法由所列样本复算，标注待核。
7. **P-C 的效果与 review-v2 判定冲突**：round-report/analysis 记「R3 冷构建 41.285s，较 R2 44.417s 约 -7%，R2 提案收益保持」；`review-v2` §3.8 判「R3 未改代码、P1 只动测试，不可能影响 build，-7% 是噪声」。两者并列，不合并为单一结论。
8. **`/rsi` 端点与 `runs/{id}`**：round-report 自述已按真实端点实施，但 analysis #15 的文档统一仍未完成；playbook §6 已把真实端点写入约定。文档层修复状态需以仓库最新文档为准。

---

*原文落款（analysis-r3）：本报告只读产出，未改动任何业务代码；三条提案均待人工评审批准后方可由 builder 实施。*
*原文落款（round-report-r3）：本报告由 `scripts/rsi-round-report.sh` 自动生成；如与平台数据不一致，以 daemon API 为准。生成时间：2026-11-11 10:50 UTC。*