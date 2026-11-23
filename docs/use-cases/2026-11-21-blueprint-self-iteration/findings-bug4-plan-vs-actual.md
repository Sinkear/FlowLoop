# BUG-4 分析：蓝本「计划」从未成为「实际执行」——咨询化执行的十一个保真缺口

（findings-bug4-plan-vs-actual.md）

> **范围**：蓝本绑定目标从「编译成工作项」到「实际执行图」的保真链路——编译产物与执行者的绑定、未编译时为什么会退化成 consult、consult 的真实语义、owner 计划外写入、spawn 弹性、reopen 手动模式、闸门表达力，以及「计划图 vs 实际图」对账的缺失。
> **不覆盖**：0 提案的翻译器链路（BUG-3，见 [findings-bug3](findings-bug3-blueprint-compile-observability.md)）；`SQLITE_BUSY` 与防重复审批（BUG-1/2）；RSI 指标口径（[analysis.md](analysis.md) §6）。
> **基线**：代码基线 `HEAD = a3d2744`（2026-11-21 15:05 +0800）；事故运行版本为更旧的 featnew 构建（`0.1.0-featnew.20261119`，源码已删除）。所有 `文件:行` 以 HEAD 为准；事故 build 的判断只到 DB / daemon.log / chat_message / 反汇编层级。
> **状态**：**未修复，未改代码**。2026-11-21 用户决定「先补文档、先分析问题给建议方案」，事故数据包（`incident-data.zip`，2026-11-21 采集）已到，本文用它把原先的「待证」项补成实证（§1），修复设计见 §5；按用户决定本轮**不动 Go/前端/协议代码**。
> **2026-11-21 实施状态（已更新）**：用户要求完整修 BUG-4（m01029）并「123 都要做」（m01435）后，工作树已实现全部绑定链与三项收尾——P0 不变量（Create/Activate/终局守卫停靠）、contract 说真话（pin + 节点>0、`PLAN NOT MATERIALIZED` 段）、dispatch 懒编译、plan-vs-actual 对账（服务 + HTTP + RSI finding + UI 卡）、consult 回答回填、cap per-blueprint、spawn children 入 ledger、reopen 显式活动、consult 工具层只读、gate timeout 默认 park、**提案→下一轮显式入口**、**gate `auto` 运行时判定**（`auto.when`/`auto.action`，sweeper 对直接上游 sealed output 求值）、**owner 写入归户**（`Blueprint-Node:` trailer → `blueprint_write` → plan-alignment 计划外写入 deviation）、**scratch `env_change` 账本**（record/list RPC + CLI + HTTP + goal 详情卡）。详见 §五 各行的「已实现」注记与 [execution-report](execution-report.md)。
>
> **2026-11-21 用户复核补充（已并入本文）**：① consult **只许提意见**，不给执行权；② g1 的 `timeout.action=rejected` **不是有意的**；③ 顺序来自 AI SHELL 的人设（`system_prompt`）与 `squad.instructions`（staged `AGENTS.md`），**蓝本从未被读取**（§1.3）；④ scratch 交付进账本的影响见 §三.12。
>
> **判定标签**
> - 【已确认·DB】事故库查询；
> - 【已确认·事件流】`chat_message` 的 `tool_calls` / `run.prompt`；
> - 【已确认·日志】`daemon.log`；
> - 【已确认·源码】HEAD 静态核对；
> - 【推断】由事实推出、缺直接观测。

---

## 一页结论（按影响排序）

| # | 级别 | 结论 | 判定 |
|---|---|---|---|
| P0-1 | **没有「pinned ⇒ 已编译」不变量** | 终局守卫只数 `blueprint_node` 中 `pending/running` 的行（`goal.go:1649` → `blueprint.go:1736-1744`）；**0 行 = 0 pending = 放行**。未编译的 pinned goal 可以一路 consult → review → done，全程无结构拦截；事故四目标 `blueprint_node` 全表 0，guard 恒报 `blueprint=0`。BUG-3 的补丁只让 `Create(active)` 也编译，没有断言「进入 active 就必有节点」 | 【已确认·源码+DB】 |
| P0-2 | **未编译时 owner 被喂「已编译」人设，且拆活被结构拒绝** | `blueprintDriven = blueprint_version_id != ""`（`team_context.go:107`），不看 ledger 是否有行；contract 说 "The work plan is COMPILED … `subgoal create` is REFUSED … Do NOT do the work items yourself"（`:304-329`）；服务层对任何 pinned goal 拒绝 `CreateSubGoal`（`subgoal.go:88-95`）。未编译时 list 为空、拆活被拒、代工被劝退 → **owner 唯一可用的编排动作只剩 comment/mention 咨询** | 【已确认·源码+DB】 |
| P0-3 | **蓝本声称的「minor/低风险 → auto 直通」在平台语义里不可表达** | gate 编译成「不发 run 的人工 holder」（`blueprint.go:1525-1545`），只能被人工释放；condition 只在**编译时**用 **goal 字段**求值（`blueprint.go:1249-1280`、`:1301-1305`），不能引用 `{{node.t1.output}}` 的风险等级；g1 的 24h 超时动作是 `rejected` | 【已确认·源码+JSON】 |
| P1-1 | **事故 build 的 consult 是事实上的执行通道** | consult run 的注入 prompt 只有 mention 唤醒行 + 派发评论（无 READ-ONLY 契约）；Upgrader 用 266 次真实工具调用、`tool=execute` 经 JEM API 直接改环境；平台未在工具层阻止。HEAD 的只读是 **prompt + detached worktree**，同样不是工具层权限 | 【已确认·事件流】/【已确认·源码（HEAD）】 |
| P1-2 | **owner 的计划外写入不入账（HEAD 风险）** | owner 的 worktree 是目标分支的可写 checkout（只有 consult/review detach）；「Do NOT do the work items yourself」只有文字；owner 的 commit 不产生 `change` 行，终局守卫的 `pendingChanges` 不数它，`deliver` 合并目标分支时照样进入 | 【已确认·源码】；事故中**未发生**（owner 各 run 仅 2–14 次调用，全是读状态/发派发评论）【已确认·事件流】 |
| P1-3 | **spawn 的 children 不在计划 ledger 里** | spawn 节点编译成一个 planner `sub_goal`，children 运行期物化成真实 `sub_goal`，**不写 `blueprint_node`**（§11.4 double bookkeeping）；schema 预留了 `node_key='<spawn>#<seq>'` 与 `parent_key`（`schema.sql:276-277`），但 Go 侧 `ParentKey` 只有结构体字段（`blueprint.go:181`），**没有任何写入点** | 【已确认·源码】 |
| P1-4 | **reopen 进入「manual follow-up mode」** | 只要 `blueprint_node` 已有行，`compileBlueprintTx` 直接 return（`blueprint.go:1203-1212`）并打 "reopened — spec rows exist, manual follow-up mode"。重开/续跑的目标不会重新编译，spec 对新增工作不再有约束 | 【已确认·源码】 |
| P1-5 | **consult 的结构化回路是空的** | 事故 `consult_request` 17 行，`response_comment_id` **全部为空**；HEAD 的 `recordConsult` 也只 INSERT 请求行（`comment.go:459-469`）。编排图能靠 requester/guest run 重建，但「这次咨询的结论」无结构化落点 | 【已确认·DB+源码】 |
| P2-1 | **有「两张图」，没有「一次对账」** | UI 把「蓝本（计划）」与「编排（实际）」并排（`web/app/goals/[id]/page.tsx:226-250`），只供人眼比较；`BuildGoalWorkflow` 从 `sub_goal`/`run`/`consult_request`/`change` 构实际图（`workflow.go:124-146`、`:326-374`），**从不读 `blueprint_node`**；RSI 冻结实际图，不产出 deviation finding | 【已确认·源码】 |
| P2-2 | **提案批准后不会自动推进下一轮**（2026-11-21 已补显式入口 `POST /blueprint-proposals/{id}/next-round`） | `ApproveProposal` 只把补丁写成 **draft** 版本（`blueprint.go:3069-3126`）；publish 是人工端点（`:939-961`）；没有任何代码在批准后自动建下一轮 training goal；emergent draft 是另一条路径（`rsi.go:800-880`，cap=`rsi.max_auto_rounds`） | 【已确认·源码】；与 `docs/RSI.md:109` 一致，产品缺口非 bug |
| P2-3 | **scratch 域的「交付」不进 change 账本** | 域「自动升级」是 scratch（无 git 仓库）：`change` 全表 0、0 goal 分支、0 merge commit；交付物 = 环境实变 + `reports/` 目录 + `manifest.json`（sha256）+ artifacts（`local_only=true`） | 【已确认·DB】 |

**一句话**：编译成功时，这套机制是**结构性**的（工作项不能自由创建、顺序由 `depends_on` 推、输入由 brief 模板注入、质量由 spec 的 verify 判、闸门由 spec 的 gate 等人工）；但**「编译成功」本身没有任何不变量保护**，而未编译时平台又用 prompt 堵死了 free-form 的替代路径——于是「执行」只能退化成咨询。事故里看起来「按蓝本执行」的链条，**其实是 AI SHELL 的 persona 与 squad instructions 在驱动**（用户复核：蓝本从未被读取），蓝本 v1 只是同一份 SOP 的结构化副本；更关键的是，**连 persona 里写死的「第 1 步建 sub_goal」都被模型静默跳过而平台放行**——顺序性完全寄存在 prompt 与模型自觉上，平台没有任何节点级绑定。

---

## 一、事故事实（数据包实证）

数据源：`/root/job-envs/sandboxes/ouroflow-rpa-1795138507/.ouroflow/ouroflow.db` 备份 `/tmp/ouroflow-db-backup-1121.db`，`daemon.log`，`chat_message` 事件流，goal 项目目录，事故二进制；事故数据包原件 `incident-data.zip`（sha256 `80bf2df6…`）为临时附件，未入库，关键事实已转录到本文。

| 项 | 数值 | 证据 |
| --- | --- | --- |
| 绑蓝本的 train goal | 4（`aada5a0c` / `2da62c29` / `b86244c3` / `ceb804a0`），均 done，pin `9ae4d32f` | 【DB】goal |
| 蓝本 / 域 | 「自动升级流水线 v2」`0fef5c50`，v1 published；域「自动升级」`73eaf8c1`（**scratch**） | 【DB】 |
| run 总数 / 按 role | **50** = owner **26** + consult **17** + review **7** | 【DB】run |
| 分目标 | minimax 15（owner 8 / consult 5 / review 2）；deepseek 13（6/4/3）；openclaw 11（6/4/1，含 11-21 01:08 唤醒 `ba2246b1`）；opencode 11（6/4/1） | 【DB】 |
| `sub_goal`（4 目标） | **0**（全库仅 1 行属旧 goal `9b581930`） | 【DB】 |
| `blueprint_node`（全表） | **0** | 【DB】 |
| `change`（全表） | **0** | 【DB】 |
| `consult_request` | 17 行；`requester_run` 全是 AI SHELL 的 owner run；`response_comment_id` **全空** | 【DB】 |
| 活动日志 | 只有 `created → commented → entered_review → review_ready`，**没有 `activated`** | 【DB】 |
| 规范图实际执行 | 无节点实例；g1 从未触发；终局 guard 恒报 `blueprint=0` | 【DB+日志】 |

> 数据包抬头写「owner 20 / consult 17 / review 6」，与分目标明细及总数 50 不一致；本表按 `run.role` 原值重计（26/17/7），分目标数字与总数自洽。

### 1.1 实际执行链（opencode `ceb804a0`，10 个 run）

```text
人工评论 @squad：升级 opencode（注意宿主机上那个不动）
  → owner 4b0f1d57（14 calls：读状态 + 发派发评论）
     └─ mention Checker → consult 413d5c47（92 calls：查新/分级/写 upgrades.json）
  → owner e41d9125（2 calls）→ mention Upgrader
     └─ consult 61c4a31f（266 calls：经 JEM API 真实升级 + 验收）
  → owner 2c9ae753（8 calls）→ mention Verifier
     └─ consult 6b17f208（65 calls：验证）
  → owner b12f7cb2（2 calls）→ mention Packager
     └─ consult e9c6bd71（149 calls：打包/manifest）
  → owner e91a4414（14 calls：总结）
  → 人工 review edb5e39f（28 calls）→ approve → deliver → done
```

AI SHELL 的派发评论原文（`10eb1092`）：

```text
[@Checker](mention://agent/f75f223f…)

来源: AI SHELL
目标: Checker
env_id: opencode
状态: DISPATCH
操作指令: 执行升级前检查。…输出 upgrades.json 交接件并回报关键信息（版本号、风险等级）。
完成后我会派发 Upgrader 执行升级。
```

daemon.log 对应行：`comment: mention dispatch goal=… → consult run … agent=… (trigger comment …)`。【已确认·日志】

### 1.2 prompt 与工具调用（回答「谁真的干了活」）

| run | 角色 | 消息 | 真实工具调用 | 内容量 |
| --- | --- | ---: | ---: | ---: |
| `4b0f1d57` | owner / AI SHELL | 23 | 14 | 2.3KB |
| `413d5c47` | consult / Checker | 141 | **92** | 121KB |
| `e41d9125` | owner | 5 | 2 | 0.4KB |
| `61c4a31f` | consult / Upgrader | 333 | **266** | 119KB |
| `2c9ae753` | owner | 12 | 8 | 1.9KB |
| `6b17f208` | consult / Verifier | 72 | **65** | 4.6KB |
| `b12f7cb2` | owner | 5 | 2 | 1KB |
| `e9c6bd71` | consult / Packager | 167 | **149** | 31KB |
| `e91a4414` | owner | 22 | 14 | 3.9KB |
| `edb5e39f` | review / Verifier | 38 | 28 | 28KB |

- **consult 的注入 prompt**（`413d5c47` 的 `run.prompt` 全文）只有 wake 行 + 派发评论正文，**没有任何平台 READ-ONLY 契约追加**；Checker 首条 thought 里的 "READ-ONLY" 是它按人设的自我推断。【已确认·事件流】
- **owner 的 `run.prompt` 为空**：worker 的 prompt 由机器侧 CLI 组装，不入库。所以「平台 contract 是否到达 owner」在库里看不到，只能看行为：owner 的 6 次 run 全部只做「读状态 + 发派发评论」，**0 次子目标创建尝试、0 次写文件类调用**。【已确认·事件流】
- **Upgrader 的真实执行**：`tool=execute` → `http://127.0.0.1:8090/api/v1/envs/opencode/exec`（JEM exec API）下发升级命令；另加载 skill `job-env-artifact-upgrader` / `job-env-upgrader-profiles`。环境实变：`opencode packageVersion 1.0.9→1.0.10`、上游 `2.0.14→2.3.2`。【已确认·事件流+DB】

**结论**：「谁写了代码」既不是 consult 越权改文件，也不是 owner 代工，而是 **consult 被当成执行者使用**——该 build 的 consult 有完整 ACP 工具集（execute/skill），平台没有在工具层限制；「只读」只存在于 HEAD 的 prompt 文案与 worktree detach。对一个交付物在远程环境的 scratch 域，detached worktree 什么也拦不住。

### 1.3 蓝本从未被读取：顺序来自两处 prompt 硬编码

2026-11-21 用户复核确认：**AI SHELL 知道 t1→t2→t3→t4 的顺序，与蓝本零关系**。注入链有两处硬编码来源：

| 来源 | 内容 | 机制 |
|---|---|---|
| ① `agent.system_prompt`（DB） | 整条 pipeline dispatch 规则：「自动升级 pipeline 有 4 个阶段，必须按序执行… Checker → Upgrader → Verifier → Packager」，连 4 个成员 agent id、mention 派发格式、wrap-up 判断规则、Verifier→Packager 交接注意事项都写死 | 机器侧以 system prompt 下发；AI SHELL 的 runtime 是 **hwcloud serve --acp**（不是 opencode，修正此前的想当然） |
| ② `squad.instructions` | 4.0 节 = 完整工作流顺序 + 交接消息模板 | 每次 run dispatch 时 CLI 执行 `stageRunProfile`，把 **persona + RunProfile + squad instructions** 三段写进 goal 目录 `AGENTS.md`（owner profile 14,746 字节；271 行起为 instructions 原文） |

**「不是读蓝本」的三条直接证据**：

1. **时序**：首 run `4b0f1d57` 的第一条 thought 在任何工具调用之前就说 "There's a pipeline: Checker → Upgrader → Verifier → Packager"——不是检索/读取出来的。
2. **零痕迹**：staged `AGENTS.md` 全文 grep `蓝本|blueprint` = **0**；蓝本节点 id（`t1/g1/t2…`）在 AGENTS.md、comment、`chat_message`、全部 50 个 run 的工具调用里**零出现**。
3. **同构来源**：蓝本 v1 spec 与 squad instructions 语义同构（同样节点、同样 ID）——**蓝本就是 instructions 的结构化副本**，运行时消费的是 instructions，不是 spec。

**最强微观证据（连人设都不被强制）**：AI SHELL 人设里 dispatch 规则第 1 步明确要求「创建 `sub_goal`（assignee=对应 agent，verifier=你自己）」，第 2 步才是 mention 派发。但 4 个 goal 的 `sub_goal` **全部 0 行**——模型只做了第 2 步，第 1 步被静默跳过，平台无任何拦截（finalization guard 对 `sub-goals=0` 照常放行）。**如果顺序真靠平台编译的依赖边保证，这种跳过在机制上不可能发生**；整个 pipeline 的顺序性完全寄存在两次 prompt 注入 + 模型自觉上。

> 重建途径（机器侧日志，供后续复查）：`cli.log` 的 `stageRunProfile: staged N bytes` 可逐 run 数；`.hwcloud/logs/hwcloud.log`（2.3MB JSON 行级）有 session id、title generation、context.recall 决策 trace，完整 prompt 原文不入库；`AGENTS.md`（persona + RunProfile + instructions 三段）+ `chat_message` + `cli.log` 三方交叉即可复原任一 run 的注入上下文。
>
> 数据包说明：用户已在本地数据包新增 §7（约 2.1 万字节）；本目录当前附件 `incident-data.zip` 仍是旧版（16,169B，无 §7），本节以用户 2026-11-21 消息为准。新包到了可逐条回填行号/字节数。

---

## 二、规范图 vs 实际图：逐节点对照

规范图（v1，7 节点 / 7 边；边：`t1→g1(done)`、`g1→t2(approved)/t5(rejected)/t5(timeout)`、`t2→t3`、`t3→t4`、`t4→t5`、`t5→end_ok`）：

| 规范节点 | 类型 / assignee | 实际发生 | 平台绑定 | 结论 |
| --- | --- | --- | --- | --- |
| **t1** Checker·查新分级 | task / agent:f75f223f | consult `413d5c47`（92 calls；产出 `upgrades.json`、check-report） | 无 | 人对了，节点不存在 |
| **g1** 分级卡点 | gate / human:owner（24h→rejected） | **未执行**。四份 Checker 报告均 `risk=normal`、`major_bump=false`，按 spec 描述应走 auto；实际既无 auto 分支，也没有节点被触发 | 无 | 卡点从未发生（不是「通过」，是「不存在」） |
| **t2** Upgrader·升级+验收 | task / agent:cbc2615f | consult `61c4a31f`（266 calls；真实升级 opencode） | 无 | 人对了，节点不存在 |
| **t3** Verifier·独立验证 | task / agent:c6f15196 | consult `6b17f208`（65 calls）+ 目标末尾 review `edb5e39f`（28 calls） | 无 | 验证被拆成 consult + 平台 review，两套语义 |
| **t4** Packager·打包 | task / agent:2b1998cf | consult `e9c6bd71`（149 calls；manifest/artifacts） | 无 | 人对了，节点不存在 |
| **t5** AI SHELL·唯一总结 | task / agent:b6e7384e | owner `e91a4414`（14 calls）发总结评论 | 无（owner 本来就不是节点执行者） | 总结发生了，但不是 t5 节点 |
| **end_ok** | end | goal 走人工审查 `approve → deliver → done` | 无 | 终局由 goal 状态机完成，非 end 节点 settle |

**判读**：顺序对、人设对、交接件对——但**不是蓝本给的**。用户 2026-11-21 复核确认（§1.3）：顺序来自 AI SHELL 的 `system_prompt` 与 `squad.instructions`（staged 进 `AGENTS.md`），运行时**从未读取蓝本**；`AGENTS.md`、评论、全部 50 个 run 的工具调用里，蓝本与节点 id 零出现。规范图与实际图之所以同构，是因为**蓝本 v1 本身就是同一份 SOP 的结构化副本**（人设/instructions 是源，蓝本是拷贝），不是平台把 spec 编译成了执行。每个箭头仍是 AI SHELL 写在评论里的约定（`状态: DISPATCH / 操作指令: … / 完成后我会派发 Upgrader`），不是 `depends_on` 边；每个执行者都是 consult，而不是编译出的 `sub_goal`。平台的 `blueprint_node` 状态机、释放条件、终局校验全程未参与。

> §6 Q1 已由用户复核关闭（人设内置，蓝本零读取）。这条把 BUG-4 的定性从「agent 不照图跑」推进到「平台从未把图交给运行时」：**执行图与规范图的相似性来自两份 prompt 的文本同构，而不是任何平台绑定**。

---

## 三、逐项分析

### 1. 编译成功时，「参照蓝本执行」是靠什么做到的（代码事实）

不是靠提示词，而是靠四层结构绑定：

| 层 | 机制 | 位置 |
|---|---|---|
| 工作项物化 | 每个 task/spawn 节点 → 一个 `sub_goal`；gate → holder（无 run）；condition 在 beat 1 用 goal 字段求值并裁剪 live 图 | `blueprint.go:1191-1647` |
| 顺序 | `depends_on` = 该节点在**裁剪图**中的传递 task 祖先（spec key 映射为预分配 `sub_goal` id）；`wakeDependentsTx` 要求所有依赖 `verified` 才释放 | `blueprint.go:1352-1408`；`subgoal.go:1570` |
| 输入 | 释放时 `resolveReleasedBriefTx` 把 brief 里的 `{{node.<key>.output}}` 换成上游 `sub_goal` 最近 `role='subgoal'` run 的 `result_summary`，快照进 `blueprint_node.resolved_config.brief`；该 brief 就是下游 run 的 prompt | `blueprint.go:1767`；`daemon.go:2930-2934` |
| 质量/闸门 | task 的 `verify.mode=agent` 才生成验证 run，verdict 决定 verified / rework；gate holder 只等人工 `ResolveBlueprintGate`，超时按 spec 的 `timeout.action` | `blueprint.go:2393`、`:2494`；`subgoal.go:770-860`（conflict→rework） |

结论：**编译发生之后**，小队确实不可能「自己另起一套活」——拆活被拒、没节点就没有 run、下游拿不到上游产出就无法伪造输入、产出必须过 spec 的 verifier。这是真保证；**问题在「编译成功」没有不变量**。

### 2. 缺口 A（P0）：没有「pinned ⇒ 已编译」不变量

`compileBlueprintTx` 的唯一保证是「被调用时物化」，没有任何地方断言「pinned 的 goal 在进入执行前必须已物化」：

- `Create`（`goal.go:266-465`）在 BUG-3 补丁前完全不编译；补丁后只在 `status=active && pin!=''` 时编译。
- `Activate`（`goal.go:2205-2258`）是唯一原有入口。
- 终局守卫（`goal.go:1649`）只数 `pending/running`，0 行直接通过。
- 因此 `INSERT INTO goal … status='active', blueprint_version_id='…'` 仍可绕过所有编译（`goal.go:400`、`daemon.go:1583` 是仅有的两处 `INSERT`）。
- 事故四目标的 `blueprint_node` 0 行、guard 日志恒为 `blueprint=0`，就是这条路径的实证。【已确认·DB+日志】

[analysis.md](analysis.md) §3.2 已经指出「空集通过」是同一个语义缺口的三层投影（guard / 翻译器 / UI）。BUG-4 在这之上补一条：**缺口的后果不只是 0 提案，还让「执行」本身失去计划**——因为 owner 的 contract 和 `CreateSubGoal` 都假定计划已存在。

### 3. 缺口 B（P0）：owner 的人设与结构拒绝，把未编译目标逼成「咨询」

未编译时的实际约束组合（HEAD）：

1. `blueprintDriven=true`（`team_context.go:107`，只看 pin）；
2. owner contract（`:304-329`）明说计划已编译、`subgoal create` 会被拒、不要自己干活、用 `subgoal list` 看工作项；
3. `ouroflow subgoal list` 此时是空的（没有 `sub_goal` 行）；
4. 服务层确实拒绝 `CreateSubGoal`（`subgoal.go:88-95`），拒绝理由与「是否已编译」无关；
5. 唯一剩下的协作动作是 `goal comment` mention 一个 agent → 平台生成 `role='consult'` 的 run（`comment.go:459-469` 记录 `consult_request`；`run.go:287-294` 把「trigger comment 存在」解析为 consult）；
6. consult 在 HEAD 是 READ-ONLY 文案（`daemon.go:1554-1567`、`:1825-1829`），只产出意见/评论。

所以「都是起咨询接口来实现」不是小队偷懒，而是**平台把其他路径全堵了**：自己干被 prompt 劝阻、拆活被服务层拒绝、计划又不存在——只剩咨询。事故里 4 个 owner 共 26 个 run，**没有任何一次 `subgoal create` 尝试**；17 个 consult 全部由 owner 的 mention 评论触发，拓扑与蓝本节点语义一一对应（见 §二）。【已确认·DB+事件流】

这也解释了为什么实际图看起来像一张咨询网：`BuildGoalWorkflow` 把 `review`/`consult` 都画成节点、把 `consult_request` 画成边（`workflow.go:326-374`），咨询多的时候实际图自然「全是咨询」。

### 4. 缺口 C（P0）：闸门表达力——auto 通道在 HEAD 里无处安放

蓝本 description/brief 声称「minor/低风险 → auto 直通；major/高风险 → needs_approval 停在卡点」，但：

1. `SpecGate` 只有 `actions[]`（事故里是 approved/rejected）和 `timeout`（24h → rejected），**没有 auto 端口**；`g1` 的出边只有 `approved→t2`。
2. 编译时 gate 变成「**不发 run 的 holder**」（`blueprint.go:1525-1545`）：`sub_goal` 状态 `running`，brief 是给人看的决策卡；它只能被人工决策释放。
3. condition 节点只在**编译时**用 **goal 字段**求值（`blueprint.go:1249-1280`；注释明确 "expr may only reference goal fields"，`:1301-1305`），**不能引用 `{{node.t1.output}}`**——所以「按 Checker 的风险等级选 auto / 人工」在 Phase A 语义里写不出来。

因此：**即使 BUG-3 修好、图被完整编译，这四个目标仍会在 g1 停住等人工；没人值守时 24h 后 `timeout→rejected`，整批被拒。** 这不是事故 build 的独有缺陷，HEAD 同样成立。它解释了一个反直觉现象：蓝本把「自动分级」写进了描述，平台却只能执行「全量人工卡点」。事故里 g1 从未触发——因为图根本没编译；若编译了，它会是唯一的必经人工闸门。

**2026-11-21 用户确认：g1 的 `timeout.action=rejected` 不是有意的。** HEAD 把 `gate.timeout`（`after` + `action`）做成**必填**（`blueprint.go:444-455`，「where the flow goes when nobody decides」），超时由 `SweepBlueprintGates` 以 `decided_by=system` 执行该 action（`blueprint.go:2491-2545`）。也就是说，**spec 作者必须在「无人值守去哪」上签字**，事故蓝本签了 `rejected`，于是 24h 无人决策 = 系统自动拒绝整批。平台没有「挂起/升级（park/escalate）」这一档，作者也没有安全默认值。

**2026-11-21 已实现（工作树）**：`gate.timeout` 与 `timeout.action` 均改为可选；`after` 到点但无 action（或整段 timeout 缺失）时，`SweepBlueprintGates` 只标记 `parked_at`——不写 `gate_decision`、不释放任何分支、gate 继续 held（`internal/service/blueprint.go:443-455`、`:2491-2545`，测试 `internal/service/blueprint_park_test.go`）。显式 action 仍以 `decided_by=system` 执行，author 必须自己选。

修复方向：把超时语义拆成「停靠并通知」与「显式拒绝」两种，**默认前者**；`rejected` 只应由作者显式选择，发布时对「无人值守 → 拒绝整批」给出警示（UI 与校验都做）。

### 5. 缺口 D（P1）：consult 的语义——已决策：只许提意见

**HEAD·源码（约定层）**：

- `team_context.go:375-379`：*"You are consulted (READ-ONLY) ... Do not modify files, do not commit, do not execute the task itself — your edits are discarded by the platform."*
- `daemon.go:1825-1829`：*"READ-ONLY runs (consult/review/verify) ... produce opinions, not work"*。
- `daemon.go:1554-1567`：consult/review 使用 detached 快照 worktree，改动不回写。

这三层都是**提示 / 工作树隔离**，不是工具层权限：ACP 的 `execute`、`skill`、外部 HTTP 调用仍在工具集里；对一个「交付物在远程环境」的 scratch 域（本次正是：JEM exec API 改环境），detached worktree 什么也拦不住。

**事故 build·实证**：

- consult 的 `run.prompt` 只有 mention 唤醒行 + 派发评论正文，**没有平台只读契约**；Checker 的 "READ-ONLY" 是它自己的推断。
- Upgrader `61c4a31f` 266 次真实调用，经 JEM API 直接改环境；所有者（owner）反而只编排。
- 无论契约是否存在，**平台没有在工具层阻止 consult 执行**——这是关键。

**已决策（2026-11-21 用户确认）：consult 只许提意见，不给执行权。** 由此：

1. **事故 build 中 consult 实际执行 = 该 build 的缺陷/回归**，不是产品意图——不能把「consult 当执行者」写成产品能力。
2. **HEAD 必须补工具层强制**：consult/review run 的 ACP 工具集裁成只读（禁 `execute`/写文件/带副作用的 skill/任意外部写调用），或至少对 scratch 域的远程 exec 做策略拦截。只靠 prompt 与 detached worktree 不够（后者拦不住 JEM/远程环境）。
3. **若未来确实需要「委派执行」**：新增显式 `delegate`/`act` 模式并编译成节点或账本，不复用 consult 语义；consult 的 `response_comment_id` 仍是其唯一产出通道（§9）。
4. **可观测**：consult run 若出现写类工具调用，应记 `policy_violation`（WARN + finding），不进 change、不静默放行。

HEAD 现状仍是 prompt-only（`team_context.go:375-379`、`daemon.go:1825-1829`、`:1554-1567`），见 §5 的 consult 工具白名单项。

### 6. 缺口 E（P1）：owner 的写入不进 ledger（HEAD 风险，事故未发生）

- owner 的 run 不是只读角色：只有 `consult`/`review` 走 detached 快照（`daemon.go:1554-1567`），owner 在目标分支的可写 worktree 里。
- 「Do NOT do the work items yourself」是 contract 文字，没有服务层校验。
- owner 的 commit 不产生 `change` 行；`change` 只在 `sub_goal` 通过验证时物化（`subgoal.go:576`、`:625`）。
- 终局守卫数的是 `pendingChanges`（未集成 change 数），owner 的直接 commit 不会让它 >0。
- `deliver` 合并的是目标分支，owner 的 commit 会随分支一起进入交付。

**事故实证**：owner 26 个 run 里只做「读状态 + 发派发评论」（2–14 calls），没有写文件/提交；且域是 scratch（无 git、0 change），本来也没有 commit 通道。所以事故不是这个形态，但 HEAD 的缺口真实存在——若下一个 owner 选择自己干，外部只看到「goal done + 实际图里没有对应节点」。见 §5 的 owner 归户方案。

### 7. 缺口 F（P1）：spawn 弹性不在 ledger，且 `parent_key` 是死字段

- `resolveSpawnTx`（`blueprint.go:1840-1920`）把 spawn 节点编译成 planner `sub_goal`；规划器在 `result_summary` 里给出 fenced JSON 子计划，平台物化成真实 `sub_goal`（`depends_on=[spawn]`、`created_by='blueprint'`），并把 child id 追加进 spawn 后继的 `depends_on`。
- children **不写 `blueprint_node`**（§11.4 double bookkeeping）。
- schema 注释已经设计好审计口径：`node_key='<spawn_key>#<seq>'`、`parent_key='<spawn_key>'`（`schema.sql:276-277`）；但 Go 侧 `ParentKey` 只有结构体定义（`blueprint.go:181`），`INSERT INTO blueprint_node` 的三处都没有写它，也没有任何 spawn child 的 ledger 行。
- 因此：spec 节点数 ≠ 实际工作项数，**这是设计允许的弹性**；但「哪些额外工作项来自哪个 spawn 节点」无法从 ledger 回答，只能在 `sub_goal.depends_on` 里间接看。

### 8. 缺口 G（P1）：reopen 的 manual follow-up mode

`compileBlueprintTx` 的 reopen 守卫（`blueprint.go:1203-1212`）：已有 `blueprint_node` 行就 return，并打 "reopened — spec rows exist, manual follow-up mode"。对重开/续跑的目标，这意味着：**spec 只在第一次激活时是权威**，之后新增工作不再受节点约束，也不会被守卫的 pending 计数覆盖（新工作没有 ledger 行）。如果 manual follow-up 是产品选择，需要显式状态位与 UI 提示；如果只是幂等保护的副作用，就是一个静默的保真漏洞。

### 9. 缺口 H（P1）：consult 回答没有结构化落点

事故 `consult_request` 17 行，`response_comment_id` **全部为空**；HEAD 的 `recordConsult`（`comment.go:459-469`）只 INSERT 请求行（`schema.sql:491-501` 有 `response_comment_id` 列，但没有回填代码）。实际回答是普通评论，编排图可重建，但「这次咨询的结论」无法被 RSI/UI/后续节点结构化引用。

### 10. 缺口 I（P2）：有「两张图」，没有「一次对账」

- UI：goal 详情页并排「蓝本（计划）」与「编排实际」（`web/app/goals/[id]/page.tsx:226-250`），文案要求人眼对照。
- 实际图：`BuildGoalWorkflow` 从 `sub_goal`、`run`（含 review/consult）、`consult_request`、`change` 组装（`workflow.go:124-146`、`:326-374`），**不读 `blueprint_node`**。
- 计划图：`blueprintVersionGraph` 只渲染版本 content，不带任何实例执行状态。
- RSI：训练分析冻结实际图并算 `consult_count` 等指标，但不计算「spec 节点覆盖率」「计划外工作项数」「owner 计划外 commit 数」，也不产出 finding。

结论：平台能画出两张图，但**没有把「节点 → 子任务 → run → 验证 → change → 咨询」join 成一张可判定的表**，所以「差太多」只能靠人眼和翻库发现，不能变成告警或训练输入。

### 11. 缺口 J（P2）：提案闭环后半段没有推进器

`ApproveProposal` 只产 draft 版本，publish 与建下一轮都是人工动作；emergent 路径只服务 infer 域。`docs/RSI.md:109` 明确「不自动应用、不复测」——这是人工闸门的设计选择，但产品上就是「人不推，迭代不转」。另外 `rsi_analyze.go:232` 的 cap 用全局 `COUNT(DISTINCT blueprint_id)`，与注释的 per-blueprint 语义不符（小 bug，已修）。

**2026-11-21 实现（显式入口，仍不自动跑）**：`GoalService.StartNextRoundFromProposal`（`internal/service/rsi_next_round.go`）+ `POST /blueprint-proposals/{proposalId}/next-round`。一次动作 = 发布应用版本（若仍是 draft，等价于人工确认该版本成为权威）→ 从提案 finding 反查原训练目标 → 复制 domain/assignee/验收级别/采样率/预算 → 以 `execution_mode=train`、`source_ref="rsi-proposal:<id>"` 创建下一轮并钉新版；默认 `start=true` 直接 active（当场编译 + 派发首跑），`{"start":false}` 只建 backlog。幂等：重复点击返回同一 goal（`created=false`）。蓝本页「优化提案」在 approved 行提供「开始下一轮」按钮（含 backlog 选项与结果链接）。

### 12. 缺口 K（P2）：scratch 域的交付不进 `change` 账本

事故域「自动升级」`73eaf8c1` 是 **scratch 型**（无 git 仓库）：`change` 全表 0 行、0 个 goal 分支、0 个 merge commit；「交付」= 环境实变（opencode `1.0.9→1.0.10`、上游 `2.0.14→2.3.2`）+ `reports/` 目录（`upgrades.json`、check/accept/verifier/pack report）+ `manifest.json`（sha256）+ artifacts（`local_only=true`、`obs_uploaded=false`）。【已确认·DB+目录】

后果：① 变更不可回滚（只有 snapshot，没有变更账本）；② 无法把「哪个节点/run 改了哪个环境」归因（RSI 的 `change` 维度对 scratch 域恒为空）；③ 与 BUG-4 的「计划 vs 实际」对账互补——`change` 是代码域的账，scratch 域没有对应物。**这是设计缺口，不是本次事故的 bug**；建议为 scratch 域提供 artifact/execution 账本（§5 的 P2）。

**scratch 交付进账本的影响（2026-11-21 用户提问）**：

| 做 | 得到什么 | 代价 / 风险 |
|---|---|---|
| **归因** | 每条环境实变挂到 `goal_id / run_id / node_key / env_id`，「谁在哪个节点把 opencode 从 1.0.9 升到 1.0.10」可查；RSI 的 `change` 维度对 scratch 域不再恒空 | 需要机器侧（JEM exec 处）与 daemon 两侧写入点；跨机器可信度、幂等与乱序要处理 |
| **回滚** | before/after 版本 + snapshot 指针 + manifest sha256 入账，可一键回退；事故里只有 snapshot、没有「回滚哪一批」的记录 | 账本记的是「平台发起的变更」，挡不住环境被人/其他流水线改（漂移）；回滚脚本仍需按环境类型适配 |
| **审计/合规** | 生产环境升级有 operator、时间、工单、审批人；`decided_by=system` 的 timeout 也有据可查 | **不能存凭据/token**；保留期与体积；失败/中断会留半条记录，需要 status 状态机 |
| **成本与质量指标** | 每节点耗时可进 RSI；rework/回退次数可量化（本次 Verifier→Upgrader 回退逻辑在 prompt 里，账本可验证是否真发生） | 需要与 RSI 指标口径对齐，否则两套账 |
| **去重/幂等** | 同 env 同目标版本不重复升级；「已升级但账本没写」可对账发现 | 仅覆盖平台发起的变更；人工变更仍需环境侧事实源 |

**建议（P2，最小版）**：新增 `env_change`（或 `env_artifact`）表，字段 `goal_id, run_id, node_key, env_id, before_version, after_version, snapshot_ref, manifest_sha256, status(started/succeeded/failed/rolled_back), operator, started_at, finished_at, error`；由机器侧 exec 包装器在**执行前后**各写一条（先 `started` 再落 `succeeded/failed`），daemon 只做查询与展示。**不要塞进 `change` 表**：`change` 的语义是代码 diff/merge（有分支、commit、patch），scratch 域没有这些，混用会污染 RSI 的 change 指标。

**直接回答「要不要」**：要，但它是 P2 的可观测性建设，不是 BUG-4 的前置。没有它，事故这类「consult 执行了 4 个环境升级」永远无法从平台侧证明谁改了什么、也无法回滚一批；有了它，RSI 才能对 scratch 域产出和代码域同等级别的 finding。若短期只做一件事，先做**归因 + started/failed 两态**（不做回滚自动化），成本最低、收益最直接。

---

## 四、影响面

| 面 | 事实 | 代价 | 证据 |
|---|---|---|---|
| 计划保真 | 编译发生则强绑定；未编译则零保证，且被引导到咨询 | 「蓝本执行」名存实亡；外部无法从结果区分「按图跑」与「自由编排」 | §二/§三；【DB】 |
| 闸门 | auto 直通不可表达；所有 gate 型蓝本必然全量人工，超时 24h 还会 rejected | 「自动分级」只是文案；无人值守的升级会被整批拒绝 | §三.4 |
| 诊断 | 空 ledger 在 guard/翻译器/UI 三处都被当「正常」 | 事故只能靠翻 DB 发现（BUG-3 已补部分日志，但 guard 仍无「pinned 但 0 行」告警） | [analysis.md](analysis.md) §3.2 |
| 训练输入 | RSI 冻结的是实际图（咨询/审查边全在内），不是「相对蓝本的偏离」 | 学到的是「这次怎么咨询的」，不是「蓝本哪一步设计得不对」；`human_wait` 等指标也被计划外编排污染 | §三.10；`workflow.go:592-680` |
| 成本 | 17 个 consult 是完整 agent run（事故 build 里还真实执行），但不产 change、不归因到 spec 节点 | 事故 50 runs 的墙钟里含大量咨询/派发；无法算「每个 spec 节点的成本」 | 【DB+事件流】；[timeline.md](timeline.md) |
| 审计 | scratch 域交付物 = 环境实变 + 目录产物，`change` 0 行 | 影子执行天然不可审计；合规/回滚都缺账本 | §三.11；【DB】 |
| 修复判断 | 「蓝本没约束住」与「蓝本没编译」在外部表现相同 | 容易把 P0-1 误判成「agent 不听话」，去改 prompt/角色，而不是补不变量 | §三 |

---

## 五、修复方案（未实施，供决策）

> 用户 2026-11-21 决定「先不改代码，只把 BUG-4 写进 use-case」；事故数据包已到，以下是更新后的设计。

### P0：补不变量（先决条件，任何方案都要有）

新增 `assertPlanMaterialized(ctx, tx, goalID)`：

```
pin := SELECT blueprint_version_id FROM goal WHERE id=?
if pin == '' { return nil }                      // infer goal 不受约束
n := SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?
if n == 0 { return ValidationError("goal is pinned to a blueprint but its plan was never materialized — activate it (backlog→active) before running") }
```

调用点（建议全加，成本低）：

1. `Create` 事务内（`status=active && pin!=''` 时，编译后立即断言；或编译失败即失败——BUG-3 已让编译进事务）；
2. `Activate` 事务内（编译后断言）；
3. daemon 启动 owner/subgoal run 前（claim 时 `pin!=''` 且 0 行 → 拒绝该 run 并置 attention，而不是让它跑）；
4. 终局守卫（`pendingBP==0` 且 `pin!=''` 且 0 行 → hold + Warn，而不是放行）。

**2026-11-21 已实现（工作树）**：`planMaterialization` + `assertPlanMaterializedTx`（`internal/service/blueprint.go`，`planMaterialization` 复用 DB/tx）接入 `Create`（`internal/service/goal.go:466-479`）与 `Activate`（`internal/service/goal.go:2316-2321`）——pin 而 0 行即回滚，错误文案指明「activate the pinned goal so the spec compiles」。终局守卫在 `internal/service/goal.go:1649` 后检测「pin 且 0 行」：置 `status='review'`、写 `plan_unmaterialized` activity、发 `goal:reviewing`、`Warnf` 说明执行已偏离，不再静默放行。**claim 级拒绝未做**：已入队 run 拒绝会卡在 queued，故以守卫兜底（测试 `TestFinalizationGuardParksUnmaterializedPlan`）。懒编译未做（保持显式激活语义）。

**懒编译**作为更平滑的替代：同一检查点若 0 行，在事务内调用 `compileBlueprintTx`（reopen guard 已幂等），成功继续、失败停靠并报错。建议 **懒编译默认开启 + 断言兜底**。

**2026-11-21 已实现（工作树，补充）**：`GoalService.EnsurePlanMaterialized`（`internal/service/blueprint.go`，独立事务：检查→`compileBlueprintTx`→断言→commit→`publishCompiledRuns`）；daemon 在派发 owner/subgoal run 前调用（`internal/daemon/daemon.go` dispatch 分支），失败 `failRun("blueprint plan could not be materialized: …")`；`team_context.go` 的 `blueprintDriven` 改为「pin 且 ledger>0」，未物化时注入 `PLAN NOT MATERIALIZED` 段。测试 `TestEnsurePlanMaterializedIsLazyAndIdempotent`、`TestFinalizationGuardParksUnmaterializedPlan`。

若产品需要「自由编排但仍想钉蓝本」的目标，加显式 `goal.plan_mode='manual'`（或 activity）并在 UI 标「计划未物化」，让偏离可见；**不要用「0 行就默认 manual」**，那等于保留现状。

### P0：让 owner 的 contract 说真话

`team_context.go:107` 把 `blueprintVersionID != ""` 改为「pin 且 `blueprint_node` 行数 > 0」；未物化时 contract 应是：

> The goal is pinned to a blueprint but its plan is NOT materialized. Do not start work and do not route around it with consults — park the goal and ask the human to activate/compile it (or switch it to manual mode).

同时在 owner 简报里注入蓝本名称/描述/节点清单（id/type/assignee/title），并加一句「若 `subgoal list` 为空，先报告异常，不要自创 SOP」——避免「读得到蓝本但绑不上」时靠人设猜。

> §1.3 的实证已经把这条从「建议」升级为「必需」：事故里连人设第 1 步（`create sub_goal`）都被模型静默跳过，平台照常放行。**凡是要保证的执行约束，都不能只写在 prompt 里**——prompt 只能引导，断言/工具白名单/终局校验才是机制。

### P1：闸门与 consult 语义（需要产品决策）

| 项 | 方案 | 取舍 |
| --- | --- | --- |
| gate auto | **已实现（2026-11-21 工作树）**：gate 增加 `auto: {when, action}`；`when` 是单条 `node.<id>.output <op> "literal"`，`<id>` 必须是 gate 的**直接上游**（校验 V3 同批强制）；`action` 必须是已声明动作。sweeper 每轮对上游 sealed `run.result_summary` 求值：命中即 `ResolveBlueprintGate(..., decided_by=auto)`，未命中回落到人工/超时。发布/编译 stamping `auto_when`/`auto_action`；决策卡展示规则。测试 `blueprint_auto_test.go`（命中/未命中/四类校验失败） | 多个上游证据仍应写成 condition 节点（auto 刻意只支持单子句）；schema/编译器/UI/测试均已接入；事故 t1→g1 的「低风险直通」现在可用一条 auto 规则表达 |
| timeout 语义 | **已决策并实现（2026-11-21 工作树）**：事故 `timeout.action=rejected` 非有意。`gate.timeout`/`action` 改为可选，缺省语义=park（到点只记 `parked_at`，不决策、不释放分支）；`rejected` 仅由作者显式选择；显式 system 决策保留 | 影响所有 gate 型蓝本；`blueprint.go:443-455`、`:2491-2545`；测试 `TestBlueprintGateWithoutTimeoutHolds`/`TestBlueprintGateTimeoutWithoutActionParks` |
| consult 工具策略 | **已决策并实现（2026-11-21 工作树）**：consult 只许提意见 → 机器侧 `autopermit` 对 `role=consult` 强制只读：拒绝 fs 写、terminal、`edit|delete|move|execute` 权限请求（记 `policy_violation` 日志），只放行 read/search；`team_context` consult 契约同步声明「平台在工具层强制」 | 会改变现有「@成员 干活」的习惯；若确需执行，另立显式 `delegate` 并编译成节点。`cmd/ouroflow-cli/autopermit.go`、`exec.go`；测试 `autopermit_test.go` |
| consult 回填 | guest run 完成时把回答评论 id 写回 `response_comment_id`；workflow/RSI 使用该字段展示「咨询结论」 | 纯增量字段，无兼容问题 |

### P1：plan-vs-actual 对账

新增只读端点 `GET /goals/{id}/plan-alignment`（Web 与 RSI 共用）：

```
per blueprint_node: node_key, node_type, state, sub_goal_id,
  sub_goal.status, latest role='subgoal' run (id/status/result_summary),
  verification_result verdict, change (id/status),
  consult edges originating from that sub_goal's runs
summary: covered / uncovered / extra / settled_by_prune / failed
extra: sub_goal rows with no blueprint_node mapping (spawn children / manual)
       owner-branch commits not reachable from any change head
```

RSI 侧据此产出 `plan_deviation` finding（例如「3/5 节点无执行记录」「12 个 consult run、0 个 change 归因到节点」），并纳入训练报告，而不是只冻结实际图。事故数据正好能生成第一份 deviation 报告：spec 7 节点，实际 0 个 sub_goal / 17 个 consult / 0 change。

**2026-11-21 已实现（工作树）**：`internal/service/plan_alignment.go` 的 `PlanAlignment`（join `blueprint_node↔sub_goal↔run↔consult_request`，输出 planned/settled/mapped/executed/unplanned/consult/deviations）；HTTP `GET /goals/{id}/plan-alignment`；RSI `planAlignmentFindings`（category `plan_deviation`，high/medium，L1 置信度，`rsi_analyze.go`）；goal 详情页「计划 vs 实际」卡（`web/app/goals/[id]/page.tsx`）。测试 `plan_alignment_test.go` 两个对齐用例 + `TestConsultResponseBackfill`。

### P1：owner 写入归户

三选一（按侵入性排序）：
1. deliver 前检查：目标分支上存在「不属于任何 change head 的 commit」→ 拒绝交付并列出 commit；
2. 要求 owner commit 带 trailer `Blueprint-Node: <node_key>`，对账时归户，缺失即 deviation；
3. 禁止 owner run 写目标分支（worktree 只读），owner 只能 integrate——最硬，但会改变现有 owner 工作流。

建议 1/2 先做（可观测），3 作为产品选项。

**2026-11-21 已实现（工作树）**：② trailer 路线。`internal/service/blueprint_write.go` 解析 deliver 的 fixLog（NUL 分隔的 `<sha> <subject>\0<body>`），提取 `Blueprint-Node: <node_key>`（标签大小写不敏感、键严格小写、CRLF 容忍、复合 body 取最后一条），校验节点确在 ledger 中；`RecordBlueprintWrites` 按 `(goal_id,commit_sha)` upsert；`internal/daemon/deliver.go` 在 merge+push 成功后调用（失败只记 Errorf，不阻断交付）。`plan_alignment` 增 `writes/attributed_writes/unattributed_writes/unattributed_commits`，未归户提交即「计划外写入」deviation。测试 `blueprint_write_test.go`（trailer 11 子例 + 账本计数/deviation）。①（交付前硬拒绝）未做：先以可观测 deviation 暴露，避免改变现有 owner 工作流。

### P1：spawn children 进 ledger

按 schema 既有设计写入：物化 child 时 `INSERT blueprint_node(node_key='<spawn>#<seq>', parent_key='<spawn>', node_type='task', sub_goal_id=child, state='pending')`，并把 child 的完成/验证回写到该行。这样「弹性」仍在（规划器仍可决定子计划），但**额外工作项可归因、可计数、可对账**。

**2026-11-21 已实现（工作树）**：`resolveSpawnTx` 在 `insertSubGoalTx` 成功后 `INSERT OR IGNORE INTO blueprint_node(node_key='<spawn>#<seq>', parent_key=<spawn>, node_type='task', sub_goal_id=child, state='running')`；child 的 settle 沿用现有 `sub_goal_id` 映射回写；`TestBlueprintSpawn*` 全绿。

### P2：闭环与账本

- **自迭代推进（2026-11-21 已实现）**：`POST /blueprint-proposals/{id}/next-round` 把「发布应用版本 → 克隆原训练目标 → 钉新版建下一轮」合成一次显式动作；UI 在 approved 提案行提供「开始下一轮」（可选择立即 active 或只建 backlog）。仍然由人触发，不在批准时自动 publish/自动跑，遵守 `docs/RSI.md:109` 的人工确认原则。
- **cap 口径**：`rsi_analyze.go:232` 改为 per-blueprint 计数。**2026-11-21 已实现（工作树）**：解析 version→blueprint_id 后 `COUNT(*) WHERE blueprint_id=?`。
- **scratch 账本（2026-11-21 已实现，工作树）**：新增 `env_change` 表（goal/run/node、env_id、before/after 版本、snapshot_ref、manifest_sha256、status=started|succeeded|failed|rolled_back、operator、started/finished、error，索引 goal+started、env+started）；`GoalService.RecordEnvChange/ListEnvChanges`（校验终态、sha256 64 hex、时间序）；RPC `env_change.record|list`（身份取自 run token）；CLI `ouroflow env-change record|list`；HTTP `GET /goals/{id}/env-changes`；goal 详情页「环境变更账本」卡；scratch 域 run 的 contract 增加「改环境必须记账」段。测试 `env_change_test.go`（往返/排序/9 类校验）。**未做**：由机器自动截获（当前靠执行者显式 record）、artifact 内容上传、按账本自动回滚。
- **UI**：蓝本详情页显示「最近一轮 spec vs 实际」覆盖率与未绑定原因（未编译/已停靠）。**2026-11-21 已实现（工作树）**：goal 详情页新增「计划 vs 实际」卡（planned/executed/consult 计数 + 偏离列表 + 逐节点表）。

### 验收不变量（回归测试）

**任何 `blueprint_version_id != ''` 的 goal，只要 `status` 进入 active，就必须满足 `SELECT count(*) FROM blueprint_node WHERE goal_id=? > 0`**；不满足则创建/激活/启动失败并打 WARN。事故四目标在新代码上重放，必须在「创建/首次 owner run」处被拦下，而不是走到 done。

---

## 六、已补证与仍待核

数据包已把原「待补证清单」的前 5 项补成实证：

| 原待证项 | 结论 | 证据 |
| --- | --- | --- |
| 4 个 goal 的 run 角色分布 | owner 26 / consult 17 / review 7，**0 subgoal run**；所有 consult/review 都有 trigger_comment | 【DB】 |
| `consult_request` 全图 | 17 行，requester 全是 AI SHELL owner run，target 与 spec 节点 assignee 一一对应；response_comment 全空 | 【DB】 |
| `change` 行 | 全表 0；域是 scratch（无 git） | 【DB】 |
| git log / 谁真的写代码 | **consult 通过 JEM API 真实执行**（Upgrader 266 calls）；owner 只编排；无 goal 分支/merge commit | 【事件流】 |
| 蓝本 v1 content | 7 节点/7 边；g1 无 auto 端口；Checker 判 risk=normal 但 g1 从未执行 | 【DB】 |
| 事故 build 的 consult 行为 | 观测到的注入 prompt 无只读契约；工具层未阻止 execute；与 HEAD 的 prompt 约定不同 | 【事件流+HEAD 源码】 |
| owner prompt 是否同 HEAD | owner `run.prompt` 为空（机器侧组装），无法直接比对；行为上 0 次 create/写 | 【事件流】 |
| RSI cap / infer shape | `rsi.max_auto_rounds` 未配置走默认，封顶计数 0；infer_shape 3 行全属 verify-domain，自动升级域 0 行 | 【DB】 |

仍待核 / 已闭合：

| # | 事项 | 结论 | 依据 / 建议核法 |
| --- | --- | --- | --- |
| Q1 | AI SHELL 如何得知 t1→t2→t3→t4 的顺序 | **已闭合（2026-11-21 用户复核）**：人设内置，蓝本从未被读取。顺序来自 `agent.system_prompt` 的 pipeline dispatch 规则 + `squad.instructions`（经 `stageRunProfile` 写入 `AGENTS.md`）；蓝本 v1 与 instructions 同构，是同一 SOP 的结构化副本 | §1.3 三条证据；`AGENTS.md` grep 蓝本 = 0；50 runs 工具调用零节点 id |
| Q2 | featnew build 的 consult 工具策略（是否曾裁剪） | 仍待核：观测到该 build consult 有完整工具集且无人拦截；无法判定是「回归」还是「该版本本就如此」 | 反汇编 daemon 工具裁剪逻辑，或查该 build 的 ACP 会话配置 |
| Q3 | 产品语义：consult 是否允许执行 | **已闭合**：只许提意见，不给执行权。事故 build 的 consult 执行属于缺陷 | 用户确认（m00683）；HEAD 需工具层白名单（§5） |
| Q4 | g1 的 `timeout.action=rejected` 是否有意 | **已闭合**：非有意；HEAD 必填 timeout 且由系统自动执行，缺乏安全默认值 | 用户确认；`blueprint.go:444-455`、`:2491-2545` |
| Q5 | scratch 交付应进 `change` 还是另立 artifact 账本 | 已给影响分析（缺口 K）：建议另立 `env_change` 账本，不混入 `change`；待架构决策 | §三.12 影响表；最小版 = 归因 + started/failed 两态 |

---

## 七、与 BUG-3 的边界

| 问题 | BUG-3 | BUG-4 |
|---|---|---|
| 编译入口 | `Create(active)` 不编译；静默退出 | 编译发生后的保真；未编译时的路径选择（consult 空转） |
| 观测 | 翻译器 10 处静默；Analyze 无 WARN | guard 对空 ledger 无告警；prompt 与事实不符；无 plan-vs-actual 指标 |
| 闸门/consult | 不涉及 | auto 不可表达；consult 只读约定 vs 实际执行 |
| 修复状态 | 工作树已实现（未提交） | **未实现**（本文只设计） |
| 共用 | 都以 `blueprint_node` 为锚；BUG-4 的 P0 不变量会让 BUG-3 的「0 提案」在源头变成显式错误 | |

> 结论：BUG-3 修完，事故的「0 提案」会变成「有日志/有 WARN」；但如果不做 BUG-4 的 P0 不变量，下一个「创建即 active」的目标仍然可以在**从未编译**的情况下跑完，只是这次能看见——**看得见的空转仍然是空转**。反过来，只做 BUG-4 的不变量而不修 BUG-3，pinned 目标会被拦下、不再产出提案——两者必须一起修。