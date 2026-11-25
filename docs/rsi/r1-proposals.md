# RSI R1 提案汇编（两份独立来源）

> 合并自：`docs/rsi/proposals.md`（RSI R1 基线与瓶颈提案，round-1）、`docs/reports/proposals.md`（RSI R1 优化提案，optimizer · 只读 · 复跑版） ｜ 合并日期：2026-11-23 ｜ 状态：历史记录（只读） ｜ 用途：把 R1 的**两套不同提案**原样归档——A 组面向编排结构（假依赖/会话开销/重试放大），B 组面向测试与运行时（P1/P2/P3）；每份标明来源、轮次、基线与状态，**不可混为一份**。

## 0. 两份提案的身份卡

| 维度 | **A 组**（本文件 §1） | **B 组**（本文件 §2） |
| --- | --- | --- |
| 来源文件 | `docs/rsi/proposals.md` | `docs/reports/proposals.md` |
| 原题 | RSI R1 基线与瓶颈提案（round-1） | RSI R1 优化提案（optimizer · 只读 · 复跑版） |
| 产出者 / 场景 | optimizer，RSI 训练循环的「只读分析产物」 | optimizer，「只读复跑版」；作为 R1 轮次的优化提案交付物 |
| 数据来源 | 本机 RSI 训练接口（session / workflow / rounds）+ 本 goal 评论流；session `e9f5c086…`（goal `98d60d12`，workflow 冻结于 11:08:15Z） | ① `benchmark-report.md`（当时为基线 `33f16dc` 的 R1 复跑版：冷 38.4s / 温中位 4.4s / 测试 14.5s）；② optimizer 本轮实测复测（`go test -json` 逐用例计时、平台 runs API 全量 23 条 run）；③ 行级定位均在 `33f16dc` 上核实 |
| 基线 | 指标口径：`critical_path_ms=293,501`（sg-B 单节点）；无代码 commit 基线声明 | 代码基线 `flowloop-int` HEAD `33f16dc` |
| 提案数 | 3 条（编排层） | 3 条（P1/P2/P3，测试/运行时层） |
| 原文状态 | **【待评审】/ 只读**（无批准记录） | **【待评审】**——等待人工 review，未获批准前不实施任何改动 |
| 后续状态 | 见 §3.1：合并可核对范围内未见 A 组提案的实施/复测记录 | 见 §3.2：三条均已实施（`3cf2116`/`8ca9957`/`18a8c3c`，R2 基线 `365e108`），并在 v2 以不同 SHA 重放（`e08cb72`/`e2bda1d`） |
| 共同点 | 都是只读产出，提案上限 3 条，都要求「预期收益 + 复测计划 + 风险 + 回滚」 | 同左 |

- 原文中 B 组自称「复跑版」，并提到「与首轮提案的关系：基线 HEAD 未变（33f16dc），三条提案结论不变」——说明 B 组还有一个更早的「首轮提案」版本，该版本**未入库**，仓库内只保留复跑版。A 组与之无关。
- 两组的「R1」指同一轮次名称但不同交付物：A 组分析的是**编排层**（sg-B→sg-O 依赖链），B 组分析的是**代码/测试层**（daemon 测试串行、CDN 守卫、重试风暴）。引用时必须写「A 组/B 组」或文件名，禁止只写「R1 提案」。

## 1. 提案集 A：RSI R1 基线与瓶颈提案（round-1，编排层）

> 原文声明：只读分析产物。数据源：本机 RSI 训练接口（session / workflow / rounds）+ 本 goal 评论流。指标口径：本轮 session `e9f5c086…`（goal `98d60d12`，workflow 冻结于 11:08:15Z）。

### 1.1 基线快照（R1）

| 指标 | 本轮值 | 说明 |
| --- | --- | --- |
| `critical_path_ms` | **293,501** | 关键路径 = sg-B 单节点（bench 基准任务） |
| `wall_ms` | 293,501 | 墙钟与关键路径相等 |
| `work_sum_ms` | 293,501 | 全部「工作量」都压在一条串行链上 |
| `queue_wait_ms` | 294,330 | ≈ 墙钟的 100.3%：分析节点被依赖整段阻塞 |
| `parallel_speedup` | 1.00 | 峰值并行宽度 1，单通道，无并行收益 |
| `rework_ratio` | 0 | 本轮无返工 |
| `verify_overhead_ratio` | 0（`verify_rounds=0`） | 校验开销在本轮图中不可见 |
| `consult_count` | 0 | 无咨询 |
| `human_wait` | 未采集 | 平台指标集中无该字段，属测量缺口 |
| sg-B 实际负载 | 2 个 ≤5 行文件 + `go test`（0.009s） | 真实计算 <5s，其余全是 agent 会话开销 |

**历史参照**（同 domain、同基准任务的前两次尝试）：两轮均在 sg-B 上 attempt 3 才停（2 子任务共 4 次执行），goal 级失败；结构发现同为 `retry_amplification`（score 40，「重试放大：平均每节点 2.0 次 run」）。本轮 attempt 1 即通过，说明失败非必然、可预防。

### 1.2 提案 1（预期收益最大）：解除分析与基准之间的假依赖——分析前置、提案后写

- **假设**：sg-O 的输入（RSI session/workflow/rounds 数据）在 sg-B 执行期间就已可读，真正依赖 sg-B 的只有「最终指标与结论」一小段。把 sg-O 拆成「数据收集与历史分析（可并行）+ 指标落定与提案定稿（依赖 sg-B）」，即可消除绝大部分排队等待。
- **证据**：本轮 `queue_wait_ms=294,330 ≈ wall_ms=293,501`（100.3%），`parallel_speedup=1.00`、`parallel_width_peak=1`、`lane_count=1`；实测佐证：sg-B 运行期间（11:03Z）RSI 端点已可完整拉取 session 列表与 workflow 图，仅终值指标缺失。finding 佐证：`serialization_loss_ms=0` 但整条链 100% 串行——串行不是损失在「合并」，而是损失在「起跑线」。
- **预期效果**：每轮墙钟从 ~294s 降至 ~max(sg-B 时长, 前置分析时长)+增量；sg-O 的等待段从死等 294s 变为并行消化，多轮迭代的累计节省最大（瓶颈在关键路径上的占比最高）。
- **复测计划**：R2 同基准复测；对比 `queue_wait_ms/wall_ms` 比值与 `parallel_speedup`，目标：`queue_wait` 占比 <50%、`parallel_speedup` >1.3。
- **风险**：若 sg-O 提案必须引用 sg-B 的最终验证结论，前置写稿可能返工一次；缓解：前置阶段只做数据拉取与历史轮分析，指标段落留槽位。
- **回滚**：恢复 sg-O `depends_on sg-B` 的单段派发即可，无代码改动。

### 1.3 提案 2：压缩单节点会话开销——微任务的 brief 预热与产物模板

- **假设**：293.5s 的 sg-B 里，真实计算 <5s（`go test` 0.009s），其余是会话启动、上下文装载、工具轮次与报告。基准任务规格完全确定（文件路径、内容、验证命令都给死），把「可直接落盘的产物模板 + 预检清单」写进 brief，可把执行收敛为「写文件→跑验证→报告」三轮以内。
- **证据**：`work_sum_ms == critical_path_ms == wall_ms = 293,501`，单节点独占全链；对比历史轮：一次 13ms 即失败（attempt 3）、一次 122.7s 失败（attempt 3）——时长波动 4 个数量级，说明时间花在会话与重试路径而非任务本身。
- **预期效果**：微任务节点时长从 ~5min 降到 ~2min（保守估 30%+），每轮直接缩短关键路径；对 RSI 全循环（bench→分析→实施→复测）复利。
- **复测计划**：R2 复测 sg-B `duration_ms`，目标 <210s；同时观察 `rework_ratio` 不上升。
- **风险**：brief 过度模板化可能让 agent 跳过必要的上下文判断（如 repo 结构变化）；模板需随基准任务演进而维护。
- **回滚**：从 brief 模板移除预热段即可。

### 1.4 提案 3：消除重试放大——把前置自检写进派发简报

- **假设**：历史两轮 sg-B 各 attempt 3（finding `retry_amplification`，evidence `runs=4 nodes=2`，score 40）源于可自愈失败模式（环境/依赖/锁竞争类），重试本身不修复原因；把前置检查（go 工具链可用、目标包路径存在、无并发写锁）加进 brief 或改为派发前校验，可将 attempt 3→1。
- **证据**：两轮 goal 级失败均为同一 finding 触发；本轮同任务 attempt 1 即 verified，证明失败非任务固有，而是环境/简报差异。
- **预期效果**：失败轮次的墙钟从 `attempt×duration` 收敛为 `1×duration`；goal 级失败（两轮皆因 sg-B 失败而失败）归零，RSI 循环不再浪费整轮。
- **复测计划**：R2/R3 观察各节点 attempt 分布与 `retry_amplification` 是否再触发；目标：平均 attempt ≤1.2。
- **风险**：清单覆盖不了新失败模式，可能给出虚假安全感；需保留失败原因回传（`cancel_reason`）以便迭代清单。
- **回滚**：从 brief 模板移除自检清单，恢复原派发文案。

### 1.5 测量缺口（非提案，供 R2 补采）

- `human_wait` 未在平台指标集中出现，人工审查段的等待无法量化；建议 R2 在 workflow 图中纳入 review/人工节点计时。
- `verify_overhead_ratio=0` 与本轮「verified with no changes」（交付物在评论流、无集成变更）一致，但校验环节的真实耗时未进图；建议校验节点计时入图后再评估。

## 2. 提案集 B：R1 优化提案（P1/P2/P3，测试/运行时层）

> 原文声明：状态**【待评审】**——等待人工 review，未获批准前不实施任何改动。约束遵守：本文件是本轮唯一产出物；未改动任何业务代码、未修复任何失败测试。

### 2.1 数据源与关系（原文）

1. `benchmark-report.md`（bench 复跑产出，基线 `flowloop-int` HEAD `33f16dc`，go1.26.4 linux/arm64，构建冷 38.4s / 温中位 4.4s，测试 14.5s）；
2. optimizer 本轮实测复测（`go test -json` 逐用例计时、平台 runs API 全量 23 条 run 记录）；
3. 行级定位均已在基线 `33f16dc` 上逐行核实。

**与首轮提案的关系**：基线 HEAD 未变（`33f16dc`），三条提案结论不变；本版以复跑基准与最新运行数据刷新依据，并再次核实了拟改动位置的行号。

### 2.2 提案 1【待评审】并行化 internal/daemon 测试，缩短最慢包的 wall time

- **① 问题依据（数据）**
  - `benchmark-report.md` §3（复跑）：`internal/daemon` 8.17s，是 27 个包中最慢的一个，占 `go test ./... -count=1` 总 wall 14.5s 的 56%。
  - 本轮实测复测：该包 wall 7.108s；逐用例计时（`go test -json`）最慢单测 `TestDeliverMergeIdentityNoGlobalGitConfig` 仅 1.04s，Top5 为 1.04/0.72/0.49/0.42/0.39s，Top8 合计仅 3.82s——即 103 个顶层测试（16 个文件）几乎全部是**串行调度**在吃掉剩余 ~4s。
  - 全仓 `t.Parallel()` 仅 10 处，`internal/daemon` 的 16 个测试文件中 **0 处**；测试相互独立性较好（仅 3 个文件用 `t.Setenv` 隔离 HOME：`deliver_test.go`、`digest_test.go`、`worktree_test.go`）。机器 7 vCPU，并行化空间明确。
- **② 拟改动位置（文件 + 行级，已在 `33f16dc` 核实）**
  - `internal/daemon/intake_test.go`、`verify_test.go`、`gate_test.go`、`schedule_test.go`、`runaway_test.go`、`prompt_context_test.go`、`handoff_cancel_test.go`（7 个文件均无 `t.Setenv`，已逐一核实）的顶层测试函数，逐一加 `t.Parallel()`（优先挑相互独立、不碰全局状态的）。
  - 对使用 `t.Setenv` 的 3 个文件（`internal/daemon/deliver_test.go`、`digest_test.go`、`worktree_test.go`）保持串行（`t.Setenv` 会自动禁止并行）或重构为显式参数注入后再并行。
  - 次级目标（同法）：`cmd/flowloop-cli`（复跑 5.15s，最慢单测约 1.1s，同样调度开销为主）。
- **③ 预期收益（可量化）**：`internal/daemon` wall 8.17s → 约 3–4s（7 核并行）；全量 `go test ./... -count=1` 14.5s → 约 10–11s（**-25% 左右**）；每次 CI/开发迭代节省 3–5s，按日频迭代累计可观。
- **④ 风险与回滚**：风险——并行后共享 HOME / 临时目录 / git 全局配置的测试可能互踩产生 flaky。缓解——只对无共享可变状态的测试并行；`t.Setenv` 文件保持串行；改动后连续跑 3 次 `-count=1` 验证稳定。回滚——纯 `*_test.go` 改动，不触碰任何生产代码；`git revert` 单个 commit 即可完全还原。

### 2.3 提案 2【待评审】CDN 漂移守卫对畸形响应改 FAIL 为 SKIP，恢复测试全绿

- **① 问题依据（数据）**
  - `benchmark-report.md` §4（复跑）：`TestCodesMatchRemoteTermJSON`（`internal/service/codes_test.go:84`）父测试及 2 个子测试 FAIL，原因是远端 CDN `term.json` 缺 6 个错误码（`AW.10000025~30`，两个 locale 各 6 条）——**外部依赖未同步，本地代码无法修复**，该失败与代码质量脱钩。首轮与复跑两轮基准均复现同一失败，属固定红。
  - 代码核实（`33f16dc`）：`codes_test.go:89-90`（网络不可达 → `t.Skip`）、`:93-94`（非 200 → `t.Skipf`）已有 skip 语义；但 `:97-98` 对 **decode 失败** 是 `t.Fatalf`（必红）。CDN 返回截断 JSON、网关错误页（200 + HTML）等情况会让守卫测试直接变红，而这类瞬时/外部异常同样超出代码控制。
  - 现状影响：`go test ./...` 长期 1/24 包红，真回归的信号被固定噪音淹没（信噪比问题），也使 CI 的「全绿」判定失效。
- **② 拟改动位置（文件 + 行级，已在 `33f16dc` 核实）**
  - `internal/service/codes_test.go:96-99`：decode 失败由 `t.Fatalf` 改为 `t.Skipf("CDN returned malformed body — skip drift guard: %v", err)`，与 89-90/93-94 行既有 skip 语义对齐。
  - `internal/service/codes_test.go:100-104`（逐码断言）：可选增强——缺码信息保留 `t.Errorf`，但增加环境开关（如 `FLOWLOOP_SKIP_CDN_DRIFT=1` 时 skip 整个守卫），供无法走 CDN 审批流的开发环境使用。
  - 本地不变量守卫 `TestErrorCodesExported`（`codes_test.go:52`）保持不动。
- **③ 预期收益（可量化）**：`go test ./...` 恢复全绿（当前 1/24 包 FAIL → 0）；固定红测试噪音清零；测试信噪比恢复：今后任何 `internal/service` 的红都是真回归；`internal/service` 包（复跑 4.47s）不再因失败路径走额外的输出/重试展示开销；主要收益是可靠性而非耗时。
- **④ 风险与回滚**：风险——漂移守卫弱化，CDN 缺码不再使测试变红，跨层漂移可能被漏掉。缓解——skip 时必须输出缺码清单日志（保留可诊断性）；`TestErrorCodesExported` 仍守本地列表的唯一性/非空；需要时可用 `-run TestCodesMatchRemoteTermJSON` 手动触发检查。回滚——`git revert` 单个 commit 即可恢复原严格断言。

### 2.4 提案 3【待评审】失败重试引入「不可重试错误」快速失败，避免确定性故障烧满重试预算

- **① 问题依据（数据）**
  - 平台 runs API 实测（本轮 goal 全量 23 条 run）：**14 条 failed 中 12 条是同一确定性错误** `acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0`，且按 attempt 分布为 1/2/3 层各 5/5/4 条——3 次重试 + 30s/60s backoff 全部烧空。模型配置类错误不会随时间自愈，重试是 theater。
  - 每轮失败后 leader 被反复唤醒处理（owner run 数百秒量级），确定性故障期间这些唤醒基本是无效消耗。
  - 代码核实（`33f16dc`）：`internal/service/goal.go:92` `maxAttempts = 3`；`:96-104` `retryBackoffFor` 只按次数退避（30s/60s，封顶 5m），不看错误类型；`:1337` 的重试条件目前只有 `isImportGoal` 一个跳过先例（`:1330-1335` 注释明确「重试无收益则不重试」的既成模式）。
- **② 拟改动位置（文件 + 行级，已在 `33f16dc` 核实）**
  - `internal/service/goal.go:1337`：重试条件处增加错误分类判定——对 summary 匹配高置信度不可重试特征（如 `Model unavailable`、鉴权/配置类错误）的失败，跳过重试直接走 `:1368` 的 goal-failed 终态分支，summary 注明「环境/配置错误，重试无效」。
  - `internal/service/goal.go:92-104`：新增 `isNonRetryable(summary string) bool` 判定函数（白名单子串匹配，先只收 `Model unavailable` 一项）。
  - `internal/daemon/daemon.go:124-126`：同步 `maxAttempts` 处的注释，说明不可重试错误例外。
- **③ 预期收益（可量化）**：确定性故障下消除无效重试——本轮 12 条确定性失败中约 7 条属重试链上的必然失败（attempt 2/3 层），可省去（每次省 5–16s run 时间 + 一次 30–60s backoff 占位）；每次此类故障少唤醒 leader 1–2 次（每次 owner run 数百秒量级 agent 时间），token 与时间成本显著下降；故障语义更准确：从「任务失败」（触发失败卡片、消耗重试预算）纠正为「环境不可用」，人工介入定位更快。
- **④ 风险与回滚**：风险——错误分类误判，把本可自愈的瞬时故障（如网关短暂抖动报同款错误）提前判死，损失一次本可成功的重试。缓解——白名单只收高置信度子串并从最窄集合起步（先仅 `Model unavailable`）；退避机制与其余错误类型的重试行为完全不变；判定命中时在 summary 里显式标注原因，便于人工复核误判。回滚——`git revert` 单个 commit，行为退回现有「3 次全量重试」逻辑。

### 2.5 附：提案与基准数据的对应关系（原文表）

| 提案 | 依据数据 | 类型 |
| --- | --- | --- |
| 1 | daemon 包 8.17s / 总 14.5s；本轮实测 7.108s、Top8 单测仅 3.82s；`t.Parallel` 0 处 | 测试性能 |
| 2 | `TestCodesMatchRemoteTermJSON` 父+2 子测试 FAIL（外部 CDN 缺 6 码，两轮复现）；`codes_test.go:97-98` `Fatalf` | 测试可靠性 |
| 3 | 平台 runs API：23 条 run 中 14 failed，其中 12 条同一确定性错误（attempt 1/2/3 = 5/5/4）；`goal.go:92/96-104/1337` 重试机制 | 平台运行时效率 |

## 3. 状态与后续核对（合并时补充，只引用已入库文档）

### 3.1 A 组（编排层）

- 原文状态为只读/待评审；本轮合并可核对到的 R1→R3 文档（`docs/rsi/r3-report.md`、`docs/reports/benchmark-history.md`、`docs/rsi/int-v2-review-fix.md`）中，**未见针对 A 组三条提案的实施、批准或复测记录**。因此按原文保持「未实施」。
- 关联但不可混同：`review-v2` 对「R1 `queue_wait≈wall` 证明 sg-O 被依赖阻塞」的判定是「结论可能对，但证据是 metric artifact——`queue_wait` 定义就含依赖等待」（见 `docs/rsi/int-v2-review-fix.md` §2.4.8）。这不否定 A 组提案，但说明其证据链需要按新的指标口径（dependency_wait / dispatch_wait 拆分）重算。
- A 组的 `critical_path_ms=293,501` 与 round-report R1 的「关键路径 45.6s」不是同一快照，见 §4.1。

### 3.2 B 组（P1/P2/P3）

- **已实施（R2）**：三条提案分别在 `feat-25da0cc6-sg-23d061da` 落地，commit `3cf2116`（P1）、`8ca9957`（P2）、`18a8c3c`（P3），随后 `365e108`（impl-notes docs）。实施细节与验证见 `docs/reports/benchmark-history.md` §4.1；复测结论：`go test ./... -count=1` 三连跑全绿（14 包 0 FAIL）、daemon 中位 5.911s。
- **v2 重放**：`flowloop-int-v2` 用不同 SHA 重放同一组提案——`e08cb72`（P1）、`e2bda1d`（P2/P3 语义化重放）；新基线 `e7c2572`。见 `docs/reports/benchmark-history.md` §1.1。
- **后续审查对 B 组效果的修正**（来源 `docs/rsi/int-v2-review-fix.md`）：
  - P1：受控 A/B 实测 daemon 中位 3.615→3.44s（-4.8%），全量配对仅省 0.56s、不可归因；原文「-35% / 全量 -25%」在 16 核机器不可复现。
  - P2：实施方向被判定「生效但方向反了」——四条路径全 SKIP 后守卫失去 FAIL 路径；后续 `2aaa78b` 改为「缺码默认 FAIL + 到期 allowlist + env=1 才 skip」。
  - P3：14 例表驱动分类符合预期，但仓库当时未提交测试；后续补测试并收窄匹配。
- **状态表述**：B 组原文的「【待评审】」是**历史状态**，不是当前状态；当前状态为「已实施并经两轮基线复测 + v2 审查修订」。

## 4. 合并时发现的矛盾与存疑（保留原文，不做改写）

1. **两套「R1 基线」数值不可混用**：
   - A 组：`critical_path_ms=293,501`、`wall_ms=293,501`、`queue_wait_ms=294,330`、3 节点/3 边，session `e9f5c086…`（goal `98d60d12`）。
   - round-report R1：关键路径 45.6s、墙钟 175.4m、排队 0.3s、3 节点/3 边，session `371a5055…`（goal `3ec2bfc6…`）。
   - 两者都自称 R1，数值差一个数量级（293.5s vs 45.6s）；应为 R1 内不同 session/时点。引用时必须带 session/goal id，禁止当作同一基线。
2. **B 组引用的 R1 报告版本已不存在**：B 组数据源写「`benchmark-report.md` … 基线 `33f16dc`，冷 38.4s / 温中位 4.4s / 测试 14.5s」；该版报告在集成时因同名路径冲突被 `0bf354e` 的 v2 修订整体替换（现存 `0bf354e`：冷 40.6s / 温 3.9s / 测试 11.3s），仓库已无 `33f16dc` 版原文。B 组的数字按原文保留，但不能用现存 `benchmark-history.md` §3 的数字去「校正」它。
3. **B 组 P2 的诊断与后续实测不符**：B 组称红因是 `:97-98` 的 decode 失败 `t.Fatalf`；`review-v2` 实测 before 树（`112d50f`）的红因是 `codes_test.go:102` **缺码断言**（CDN 返回合法 JSON、缺 `AW.10000025`），decode 路径未被触发。原文照录，后续判定见 `docs/rsi/int-v2-review-fix.md` §2.4.2。
4. **B 组 P2 的包数口径**：B 组称「1/24 包红」，现存的 R1 v2 报告记 27 个包、R2 记 27 包（14 测试 + 13 无测试），R3 记 30 包。24 的来源应为更早的包统计口径，保留原文。
5. **B 组 P3 的分母已过期**：B 组记「23 条 run / 14 failed / 12 同因」；`analysis-r3` 复核后为 R1 31 run / 15 failed / 3 cancelled（12/15）。原文保留，见 `docs/rsi/r3-report.md` 附录 D-2。
6. **A 组与 B 组的「提案 1/2/3」编号重名**：两份都各有「提案 1/2/3」，但内容完全不同；本文件用「A 组 / B 组」前缀区分，后续引用不得只写编号。
7. **A 组指标的后续可用性**：`queue_wait` 在 v2 修复（`2a4babe`）后已拆分为 `dependency_wait_ms` + `dispatch_wait_ms`，`queue_wait_ms` 仅保留兼容别名；A 组基于旧 `queue_wait` 的 100.3% 论证需按新口径重算（见 `docs/rsi/int-v2-review-fix.md` §3.3）。

---

*原文落款（A 组）：本报告只读产出……（原文无尾注；文件头声明「只读分析产物」）。*
*原文落款（B 组）：三条提案均【待评审】，未获批准前不实施任何改动；本文为工作区内该主题唯一提案文件。*