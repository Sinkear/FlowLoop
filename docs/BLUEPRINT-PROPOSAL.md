# 设计提案：工作流蓝图（Blueprint）— 把编排权威从自然语言迁到可序列化图数据

> **状态：已落地（Phase A–D + §14，2026-11）— 本文件保留为设计依据、schema 与决策存档，不再是待批提案。**
> 现行行为说明与全项目细节见 [FLOWLOOP-SPEC.html](FLOWLOOP-SPEC.html)；产品与架构决策基线见 [design/DESIGN.md](design/DESIGN.md)；协作模型见 [design/Collaboration.v2.md](design/Collaboration.v2.md)。
> 变更纪律：**先改文档、后改代码。** 行为/架构/接口/流程变更必须同步更新 FLOWLOOP-SPEC.html 对应章节；蓝图 schema 或节点语义变更还必须同步本文件。
>
> **证据基线**（本仓库 `docs/research/` 归档，三份独立调研交叉收敛）：
> - `research/research-workflow-dsl-definitions.md` — 7 平台工作流定义格式（官方文档 + Dify 源码 + 262 个真实 DSL 导出语料）
> - `research/research-llm-supervisor-orchestration.md` — LLM 编排确定性的测量证据与业界退场史
> - `research/research-archive-turn3-main-report-and-self-iteration.md` — 自迭代工作流学术/工业 10 案 + 主报告
>
> **一句话**：确定性控制流在外（结构化图数据定义），弹性推理在节点内部（LLM 被封装成带受限输出空间的节点）——这是 LangGraph / Dify / n8n / Coze / Airflow / Temporal / CrewAI 全部收敛后的唯一生产架构，Anthropic 把它写成了显式建议。

---

## 1. 问题陈述（现状三条根因，代码锚点）

### 1.1 顺序不固定 / 漏跑

- **编排权威是自然语言**：`squad.instructions`（`internal/store/schema.sql:174`，"extra briefing for leader runs"）经 `internal/daemon/team_context.go:247-249` 以 "Team playbook (written by the owner):" 注入 leader 简报。执行顺序全靠 LLM 遵从 prompt，无结构化强制。
- **DAG 门控是可选项不是规范**：执行引擎本身是确定性的——`sub_goal.depends_on`（schema.sql:302）声明后由 `wakeDependentsTx`（subgoal.go:418/656，依赖全部 verified 才释放）和 retry 拒绝（"retry refused: depends_on is not satisfied"）硬性执行。但 depends_on **是否声明、怎么声明由 leader LLM 自由发挥**（schema 注释："declared by the leader"），平台不强制。
- **完成判据缺位**：编排图是读时装配的观察图（`internal/service/workflow.go:10-19`："ASSEMBLED READ-TIME … the graph can never drift from execution state"）——它只记录已发生的事。没有规范图，就没有"全部节点应达终态"的判据，漏跑只能是"没人发现"而不是"结构性失败"。

### 1.2 管家篡位（owner 代干）

owner/steward 无"只派不干"的结构约束（worktree 真实可写），三角色（规划+调度+执行）全压在一条 prompt 上，LLM 在压力下就会自己下场。**根因不是 prompt 写得不好，而是 prompt 本质上是建议，代码层面没有任何拒绝路径**。

### 1.3 system prompt 定工作流

prompt 无法静态校验（保存时不能证明"每条分支都有出路"）、无法版本化 diff、无法序列化成优化器可操作的结构。测量证据：复杂 JSON schema 下 prompting 遵从率 <40%，受约束解码 100%（OpenAI Structured Outputs 发布文）；长上下文中部指令衰减（Lost in the Middle, 2307.03172）就是"漏跑"的机制级解释。262 个真实 Dify DSL 语料反证：生产工作流里确定性节点（code 744 / if-else 507）用量远大于 LLM 节点（llm 202）——**业界在生产中从不让自然语言承担编排权威**。

---

## 2. 设计目标与非目标

### 目标

- **G1 去掉自然语言作为编排权威**：执行顺序、分支、门控的唯一权威 = 可校验的结构化图数据（blueprint）。squad.instructions 降级为非编排性上下文简报。**"去掉"的准确含义：prompt 仍然陈述规则，但规则的执行由代码拒绝路径保证，与 LLM 是否遵从无关**（prompt states, code enforces）。
- **G2 确定性外壳 + 弹性节点**：控制流（拓扑、顺序、分支集合、门控点）由图数据钉死；LLM 弹性被封装进两个口袋——节点内部（task 的 agent 自由用工具）与受限 router（只能从注册分支里选 ID）。
- **G3 蓝图可序列化**：单一 JSON 规范 + 保存时静态校验 + 不可变版本快照 + 结构化 diff + 文件导入导出。
- **G4 RSI 节点级可度量**：规范图提供稳定分母——每个节点可归因（耗时/等待/重试/验证通过率），漏跑/乱序/越权成为可机判断言（零 LLM 成本）。
- **G5 自迭代闭环升级**：优化对象从 instructions 文本补丁升级为 **blueprint 结构化补丁**；迭代结果 = 新版本蓝图 = 前端可展示的图 diff。

### 非目标（护栏）

- **不做可视化画布编辑器**。docs/research/rsi-summary.md 风险条款（"固定画布回潮"）继续有效：MVP 前端只读渲染 + JSON DSL 导入导出；改动一律走"提案→批准→版本"通道。
- **不去掉 steward**（agent.go:23）。它保留 intake 解析与问清职责；被降级的是它的编排权。
- **不改执行引擎**。blueprint 编译到现有 sub_goal + depends_on，复用 wakeDependentsTx / retry 拒绝 / Reconcile 全链路；run/verification/change 表零改动。
- **不强制所有 goal 走蓝图**。`goal.blueprint_version_id = ''`（默认）= 现状 infer 模式，动态拆解弹性完整保留。
- **提案不自动应用**（RSI.md 限制条款保留）：结构化补丁同样人工批准 + 同题 A/B 后采纳。

---

## 3. 业界参照（收敛结论，证据见归档报告）

| 参照 | 采用的结论 |
|---|---|
| Dify YAML（ReactFlow 序列化） | nodes[] + edges[] + **分支编码在边的 sourceHandle**；类别集合钉死在节点配置；Draft→Published→History 三态；Human Input 节点（表单+按钮+超时分支）= gate 原型 |
| n8n JSON（nodes + connections） | JSON 图数据全量导入导出；LLM 弹性收敛进 Agent 节点（1.82.0 废弃全部 LLM 编排类型的用脚投票证据） |
| LangGraph | 条件边返回值约束在注册映射（Literal 标签）；interrupt + checkpointer 断点续跑 |
| Coze 意图识别节点 | 分类输出 classificationId∈1..50（LLM 只选 ID）；**必须配 fallback**；子工作流引用钉版本 |
| Temporal | 代码/图（规范）与 Event History（执行）分离重放——spec/execution 两层分离的同构原型；与决策 6-4「DB 是唯一真相」同源 |
| Airflow trigger rules | 依赖状态决定节点可运行（14 种规则收敛到本提案：on done / on failed 两类起步） |
| Anthropic《Building Effective Agents》 | "Workflows … orchestrated through **predefined code paths**"（原文）；routing 模式 = 分类器选已注册下游 |
| AFlow 2410.10762 / GPTSwarm 2402.16823 | 可优化的前提是可序列化结构（MCTS 在代码图上搜索）；agent 图 = 节点调参 + 边增删 |

---

## 4. 核心设计

### 4.1 两层模型：规范图（blueprint）与执行图（sub_goal 装配）

```
blueprint_version.content ──(编译: CompileBlueprint)──▶ sub_goal rows + depends_on
        规范图 "应然"                                        执行图 "实然"
        不可变快照                                            goal 绑定后动态产生
              │                                                    │
              └────── blueprint_node（对齐锚点账本） ─────────────┘
                     node_key ↔ sub_goal_id ↔ state
                     RSI 归因分母 + 完成判据 + 双图 diff 的 join 键
```

- **规范图**：结构化 JSON（§9 schema），版本化、不可变、可校验、可 diff——"这个流程应该长什么样"。
- **执行图**：现有实体（sub_goal/run/verification_result/change），workflow.go 读时装配完全不变——"实际发生了什么"。
- **对齐锚点**：新增 blueprint_node 表，编译时每节点一行，node_key ↔ sub_goal_id 映射 + 实例状态。规范图**不复制执行状态**（保持"图永不漂移"原则），两者通过锚点 join。

### 4.2 节点类型（六种，全部映射到既有机制，不引入新执行引擎）

| 类型 | 语义 | 端口（出边 handle） | 编译映射 |
|---|---|---|---|
| `task` | 派给 agent 的执行节点；**verify 是它的属性**（对应 sub_goal 同时承担工作+质量责任，符合 domain model） | `done` / `failed` | 1:1 编译为 sub_goal（assignee、verifier 来自节点配置；重试循环是引擎内部行为，不出现在图上——LangGraph/Temporal 同样把 retry 视为引擎内部） |
| `condition` | **确定性**分支：受限操作符表达式（存在性/相等/比较/包含，参照 Dify if-else 操作符集），编译后在 Go 里求值，零 LLM | 每个声明的 case id + `else` | 求值结果记入 blueprint_node.resolved_config；按命中 case 释放下游 |
| `router` | **受限 LLM 选择分支**（见 §4.4）：给定选项、LLM 选 ID | 每个 class id + `fallback` | 释放时跑一次 processor 型路由 run（结构化输出约束枚举），决策写入 resolved_config 可审计 |
| `gate` | 决策点：表单/证据 + 决策按钮 + **可选的运行时自动判定**（`auto`）+ 超时策略（Dify Human Input 同构） | 每个 action id + 可选 `timeout`（无/无 action = park）；`auto.action` 只能取已声明 action | 复用 goal review 态机制（status='review' + review_request + human_iterations）；sweeper 每轮先按 `auto.when` 对**直接上游节点的 sealed output** 求值，命中即以 `decided_by=auto` 落决策，否则等人/超时 |
| `spawn` | 动态拆分口袋：运行时由指定 agent 拆分出**基数不定但必须登记**的子任务 | `done` / `failed`（spawn 自身在全部登记子项 settle 后 settle） | 拆分产物 = sub_goal + blueprint_node 行（node_key=`<spawn键>#<序号>`，带 parent_key）——**受控涌现：图知道范围** |
| `end` | 终点汇（显式终点让校验与渲染干净，Dify 同款） | —（汇） | 编译完成判据的一部分 |

### 4.3 边与分支编码（Dify 模式）

```json
{ "source": "n_triage", "source_handle": "has_repro", "target": "n_fix" }
```

- **可达路径集合是图数据的显式字段**：condition 用 case id、router 用 class id、gate 用 action id 填 source_handle。渲染器、校验器、执行器、优化器共用同一份真源。
- 依赖顺序不需要单独字段：**A→B 的每条路径（含经 condition/router 的）天然表达先后**；无路径相连的节点 = 可并行（复用现有 dispatch 并发）。

### 4.4 Router 语义（对齐理解：就是一个"给定选项、LLM 来选"的受约束分支）

```json
{
  "id": "n_route_fix",
  "type": "router",
  "title": "修复策略路由",
  "router": {
    "classes": [
      { "id": "local_fix",  "title": "改动小，agent 直接修", "description": "diff 预计 <200 行且不跨模块" },
      { "id": "spec_first", "title": "改动大，先出方案",     "description": "涉及接口变更或多模块" }
    ],
    "fallback": "spec_first"
  }
}
```

与"自然语言让 LLM 自己看着办"的三点本质区别：

1. **选项集合钉死在图数据里**——class id + title + description 是结构字段，不是 prompt 里的祈使句；
2. **LLM 的输出空间被压缩为"从 N 个已注册 ID 里选一个"**——结构化输出/枚举约束解码（Dify class ID / Coze classificationId / LangGraph Literal 标签三家同构，复杂 schema 下 prompting <40% vs 受约束解码 100%）；
3. **每个 class id 必须有对应 source_handle 出边，保存时校验拒绝**；未命中/超时/解析失败一律走 `fallback`（强制声明，Coze 教训）。

router 是"固定节点 + 弹性环节"诉求的业界标准答案：结构固定、选项固定、出口固定，**唯一留给 LLM 的自由度是语义判断选哪个**——且这个判断被记录（resolved_config）、可度量（fallback 率是 RSI 信号：fallback 高 = 选项描述写得差）、可优化（补丁可以改 class 描述/增删选项）。

### 4.5 可序列化与静态校验（G3，保存时拒绝而非运行时炸掉）

blueprint JSON 是唯一规范格式。**校验器是纯代码（零 LLM），可单测**。发布（draft→published）必须全部通过：

| # | 规则 | 治什么 |
|---|---|---|
| V1 | node id 唯一且匹配 `[a-z][a-z0-9_]{0,63}`；边引用的节点必须存在；source_handle 必须是源节点声明过的端口 | 结构完整性 |
| V2 | 无环（拓扑排序）；从起点可达所有节点；每个节点可达某个 end（无死路） | 治"漏跑一半的图" |
| V3 | condition 每个声明的 case、router 每个 class、gate 每个 action **都必须有出边**；router 必须声明 fallback；gate 的 timeout 可选，声明了 `action` 才要求 timeout 出边（无 action = park）；`auto.when` 必须是单条 `node.<id>.output <op> "literal"` 运行时测试，且该节点是 gate 的**直接上游**，`auto.action` 必须是已声明 action（2026-11-21 实现，见 [use-case BUG-4](use-cases/2026-11-21-blueprint-self-iteration/findings-bug4-plan-vs-actual.md)） | 治"分支悬空"与"自动判定无证据/无出口" |
| V4 | task 的 assignee 必须解析到已注册 agent/squad | 治"派给不存在的执行者" |
| V5 | 模板插值 `{{goal.*}}` / `{{node.<id>.output}}` 引用的字段必须存在于声明 | 治"参数断链" |
| V6 | content_hash（sha256）随版本快照落库，读取时校验 | 治"漂移" |

**这是图数据相对 prompt 编排的最大增量收益：prompt 无法静态校验，图可以。**

### 4.6 版本化与导入导出

- `blueprint`（命名实体）→ `blueprint_version`（不可变内容快照 + version 1..N + draft/published/retired）。
- **goal 绑定具体 version_id，引用钉版本**（Coze 子工作流模式）：确定性重跑 + 天然的同题 A/B 基础设施。
- 版本 diff = 结构化三色 diff（节点增/删/改、边增/删/改、参数变化），changelog 字段承载本版变更说明——**优化提案的 hypothesis 就落在这里**。
- JSON 文件导入导出（`flowloop blueprint export/import`），凭据类内容不入 DSL。

### 4.7 编译器：blueprint → 执行（两拍编译）

- **第一拍（goal 启动时）**：`CompileBlueprint(goal_id, version_id)` 单事务创建 blueprint_node 全量行（结构实例化）。纯代码、确定性、可重放。
- **第二拍（节点释放时）**：task 节点的 brief 是模板（`{{goal.title}}`、`{{node.n_repro.output}}`）；依赖 settle → wakeDependentsTx 释放（既有机制）→ **leader 作为"参数填充者"** 把模板解析成具体 sub_goal description + 建立 node↔sub_goal 映射。**蓝图定骨架，leader 填内容**——这就是既避开 Coze 陷阱（用户不画每个节点）、又保留 LLM 判断空间（上下文相关的实例化）的弹性设计。
- 编译产物全部走现有表：sub_goal + depends_on（由编译器从图路径推导生成，不再是 leader 的自由发挥）。

### 4.8 执行层角色重定义（G1 的强制点，全部是代码拒绝路径）

| 角色 | 降级后职责 | 结构强制（代码拒绝，与 prompt 遵从无关） |
|---|---|---|
| steward（管家） | intake 解析 + 问清需求 + 路由 goal 到合适的 blueprint | goal 绑定蓝图后：`CreateSubGoal` 对非 spawn 上下文拒绝；worktree 代干路径关闭 |
| leader（owner） | 参数填充者：节点释放时填 brief；router/gate 决策的执行者 | 不能增删节点、不能改顺序、不能绕过未 settle 的依赖（retry 拒绝已有） |
| 完成判据 | — | **Reconcile：blueprint goal 的 done 要求全部 blueprint_node ∈ {settled, skipped} 且 end 可达**。漏跑从"没人发现"变成"结构性失败"，goal 无法达成终态 |

### 4.9 弹性三口袋（回应"固定节点 + 某些环节要弹性"）

1. **节点内部弹性**：task 节点的 agent 在自己的工具面上自由发挥（现状不变）。
2. **受限 router / condition**：语义弹性收敛为"选已注册 ID"（§4.4）。
3. **spawn 动态子图**：运行时拆分，基数不定，但产物必须登记回 blueprint_node——**图知道范围**，涌现不失控。

**infer 模式完整保留为第四口袋**：不绑蓝图的 goal = 现状。且新增收敛通道：infer 执行图反复同构 → RSI finding 建议"固化 blueprint v1"（LLM+P 模式起草：LLM 只做意图→结构翻译，合法性交确定性校验器）——**从涌现到规范的单向通道**。

### 4.10 RSI 节点级可度量（G4，用户强调的核心价值）

可序列化结构给 RSI 的不是"更好的展示"，是**稳定的分母**：

- **节点级归因**：blueprint_node 是 join 锚——node_key ↔ sub_goal_id ↔ run 行 → 现有全部信号（wall time、dispatch_wait、dependency_wait、重试数、verify 结果、token 成本）无需新增埋点即变为 per-node 指标。
- **三个可机判断言**（零 LLM，确定性 checker，goal 终态时与现有图冻结一起跑）：
  - `unexecuted_node`（漏跑）：规范图节点集 − 已 settle 执行集 ≠ ∅；
  - `order_violation`（乱序）：实际派发时间序违背图路径偏序；
  - `scope_creep`（越权/篡位）：存在 sub_goal 不属于任何 blueprint_node（spawn 登记除外）。
  - 三断言与三痛点一一对应；agentevals 的 strict/superset/subset 轨迹断言是同构的现成参照。
- **结构级指标保持**：critical_path_ms、parallel_speedup 等（RSI.md 9 类发现）在分母稳定后才有可比性——同一 blueprint_version 跨 run 对比，版本 diff 时跨版本对比。
- **优化信号升级**：路由决策分布（哪个 class 被选、fallback 率）、gate 超时率、spawn 基数方差——全部是"因为结构可序列化才存在"的指标。

### 4.11 自迭代闭环升级（G5，图驱动→图优化的完整闭环）

```
infer 运行 ──▶ 执行图同构检测 ──▶ finding: 建议固化 blueprint v1 ──▶ 人工批准
                                                                  │
   前端展示迭代后工作流图 ◀── 版本 bump + 三色 diff ◀── 结构化补丁（add/update/delete 节点/边/参数）
        │                                                              ▲
        └── 同题 A/B（v1 vs v2，比较 critical_path_ms / verify_pass_rate / cost_per_success）──┘
```

- proposal 应用目标从 instructions 文本补丁 → **blueprint 结构化补丁**（finding 指到 node_key，补丁指到 node/edge，diff 展示给用户）。
- 人工批准 + 同题 A/B 门槛不变（RSI.md P3 即此闭环的运行设施）；版本树 = 优化器的记忆（AFlow 经验树同构）。
- 远期（Phase D）：AFlow 式 MCTS 在结构化补丁空间上自动搜索，A/B 是门控。

### 4.12 前端（复用 + 两个新视图，不做编辑器）

- 复用 `web/components/training/workflow-graph.tsx`（分层拓扑布局已自研，无第三方图库依赖）。
- 新增视图 1：**规范图只读渲染**（blueprint_version.content，节点/边形状与执行图同构，渲染成本极低）。
- 新增视图 2：**双图叠加 diff**——未执行节点红框（漏跑）、图外 sub_goal 红虚线（越权）、顺序违背琥珀色（乱序）、v1↔v2 版本三色 diff。
- gate 节点在前端渲染为决策卡（表单 + 按钮流），对接既有 review 决策路径。
- **蓝本管理（2026-11-21 补）**：列表页每行与详情页页头都有删除入口 → `DELETE /blueprints/{id}`（`BlueprintService.Delete`）。同一事务内删除提案、版本、蓝本本体；仍有 `backlog|active|review` 目标钉住其任一版本时返回 409（删掉会让在跑的绑定目标失去规范权威）；终态目标保留历史 pin，不重写。UI 在确认框里原样展示服务端拒绝原因。
- **草稿版本删除（2026-11-21 补）**：详情页选中草稿版本时提供「删除草稿」→ `DELETE /blueprint-versions/{versionId}`（`BlueprintService.DeleteVersion`）。只删 `draft`：已发布版本是蓝本的权威与不可变历史，返回 409；草稿仍被任何 goal pin（含终态目标的历史 pin）或被提案的 `base_version_id`/`applied_version_id` 引用时同样 409，并在消息里带计数——先处理引用再删。删除语句带 `AND status='draft'`，与并发发布竞争时不会误删权威版本。

---

## 5. 存储设计（DDL 草案，风格随 schema.sql）

```sql
-- blueprint: a named, reusable orchestration spec (product plane).
-- The orchestration authority for goals that bind one of its versions.
CREATE TABLE IF NOT EXISTS blueprint (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    created_by_type TEXT NOT NULL DEFAULT 'human',   -- human | agent (RSI optimizer drafts count as agent)
    created_by_id   TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

-- blueprint_version: immutable content snapshot. Publish requires the
-- static validator (§4.5 V1–V6). version is 1..N per blueprint; references
-- pin an exact version (never 'latest').
CREATE TABLE IF NOT EXISTS blueprint_version (
    id            TEXT PRIMARY KEY,
    blueprint_id  TEXT NOT NULL REFERENCES blueprint(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    content       TEXT NOT NULL,              -- 规范图 JSON 全量（唯一真相）
    content_hash  TEXT NOT NULL,              -- sha256(content)
    status        TEXT NOT NULL DEFAULT 'draft',  -- draft|published|retired
    changelog     TEXT NOT NULL DEFAULT '',   -- 变更说明；优化提案的 hypothesis 落位于此
    created_at    TEXT NOT NULL,
    UNIQUE(blueprint_id, version)
);

-- goal 增列（迁移）：'' = infer 模式（现状语义完全不变）
--   blueprint_version_id TEXT NOT NULL DEFAULT ''  -- REFERENCES blueprint_version(id)

-- blueprint_node: per-goal instance ledger. The alignment anchor between the
-- spec graph (node_key) and the execution graph (sub_goal_id). RSI's
-- per-node attribution denominator + the completeness criterion input.
-- Does NOT copy execution state (state here is the node's own lifecycle).
CREATE TABLE IF NOT EXISTS blueprint_node (
    id              TEXT PRIMARY KEY,
    goal_id         TEXT NOT NULL REFERENCES goal(id) ON DELETE CASCADE,
    node_key        TEXT NOT NULL,            -- spec node id; spawn children: '<spawn_key>#<seq>'
    parent_key      TEXT NOT NULL DEFAULT '', -- set for spawn children (the spawn node's key)
    node_type       TEXT NOT NULL,            -- task|condition|router|gate|spawn|end
    sub_goal_id     TEXT NOT NULL DEFAULT '', -- '' until compiled/registered (task: at release fill)
    state           TEXT NOT NULL DEFAULT 'pending', -- pending|running|settled|skipped|failed
    resolved_config TEXT NOT NULL DEFAULT '', -- 实例化实参快照：leader 填充后的 brief / router 决策 / gate 决策
    created_at      TEXT NOT NULL,
    UNIQUE(goal_id, node_key)
);

CREATE INDEX IF NOT EXISTS idx_blueprint_node_goal ON blueprint_node(goal_id, created_at);
```

- **workflow_edge 预留表维持原样**（零读写路径）：规范边活在 blueprint_version.content 里，执行边仍由读时装配——不制造第二份边真相。
- 表数量克制：3 张新表 + 1 列。schema.sql:582-602 的 workflow_edge 与本提案不冲突（它将来若启用，服务于显式边缓存，不在本提案范围）。

---

## 6. 与既有决策的调和（升级而非推翻）

| 既有决策（docs/research/rsi-summary.md / docs/design/DESIGN.md） | 本提案的调和 |
|---|---|
| "提案改产生图的策略，不直接改图" | **"策略"从自然语言升级为结构化数据**。instructions 文本补丁无法保证下次编译出同样的图；blueprint 补丁可以（content_hash 确定性编译）。提案对象从"策略"收敛为"规范图本身"，但"改的是可复用的规范、经人工批准、A/B 复测"三条护栏原样保留 |
| Coze 固定画布回潮风险条款 | 坚持只读渲染、JSON 导入导出；且蓝图**只定骨架**（结构/顺序/分支），内容由 leader 实例化填充——不是 Coze 式"用户画每个节点" |
| RSI.md "提案不自动应用、不复测" | 保留；blueprint patch 同样人工批准 + 同题 A/B（P3 设施即为此准备） |
| 决策 6-4 Event 驱动 Reconcile | 不变。节点 settle 事件仍是 wakeup hint，blueprint_node/DB 仍是唯一真相，Reconcile 增加全节点终态判据（纯查询） |
| infer 模式（execution_mode='infer'，schema.sql:216） | 完整保留且成为收敛通道入口；蓝图 goal 可视为 'train' 的结构化加强版 |
| workflow.go "图永不漂移" 原则 | 规范图不复制执行状态，读时装配照旧；两者经 blueprint_node join |

---

## 7. 分期路线与验收标准

### Phase A — 规范化（治：顺序不固定 / 漏跑）

- 表 + 校验器（V1–V6，纯代码可单测）+ 两拍编译（先支持 task/condition/end 三种节点）+ goal.blueprint_version_id 绑定 + **全节点终态完成判据** + 前端规范图只读渲染 + `CreateSubGoal` 结构拒绝。
- **验收**：绑蓝图 goal 无法在有未 settle 节点时进 done；同一蓝图版本编译两次产生相同 sub_goal 拓扑与 depends_on；infer 模式回归零差异；执行引擎零改动。

### Phase B — 项目-小队-蓝本三层串联（治：篡位残留 + 小队内扇出无结构化替代）

用户 2026-11-18 认可并批准执行（详细设计见 §11）。三步：

- **B-1 队内扇出结构化**：spawn 节点放行（受上限的动态子任务，引擎物化）；节点级团队简报（`sub_goal.team_squad_id`——修多小队 goal 下非 goal 小队成员拿到空白简报的 bug）。
- **B-2 小队能力化**：`capability:<role-slug>` 第三种 assignee 语法；能力=小队成员 role 的规范枚举（`LOWER(TRIM(role))`，先例 REVIEWER-only 规则 subgoal.go:109）；确定性解析规则表（见 §11.3）；蓝本与具体人档解耦（人换了流程不烂、A/B 证据可迁移）。
- **B-3 节点类型补全**：router（枚举约束选路+fallback）/ gate（复用 goal 级 review 态，Q1 定稿）随本阶段余项推进；发布门错误信息同步。
- **角色换岗**：steward/leader 从「编舞师」改为「集成者+异常处理器」（监控/集成/汇报/异常处理），infer 模式永久保留为新颖任务探索口袋。
- **验收**：capability 解析确定性（同输入必同输出，歧义 fail-closed）；spawn 子任务数 ≤ max_children、成员越界拒绝、非法 JSON spawn 失败可重试；多小队 goal 下每个节点 run 拿到自己小队的名册；router 决策全落在注册类集合内；漏跑/越权/乱序前端一眼可见。

### Phase C — 优化闭环（自迭代落地）

- proposal 类型 `blueprint_patch`（结构化补丁：add/update/delete node/edge/param）→ 校验器通过 → 人工批准（changelog=hypothesis）→ 版本 bump → 前端三色 diff → 同题 A/B（比较 critical_path_ms / verify_pass_rate / cost_per_success）→ 采纳/回滚。
- 涌现固化通道：同构检测 → 自动起草 blueprint v1（LLM+P：LLM 翻译意图，校验器保证合法性）。
- **验收**：一次完整的 finding → patch → 批准 → A/B → 采纳闭环在真实 squad 上跑通并展示给用户。

### Phase D — 自动结构搜索（远期，非 MVP）

AFlow 式 MCTS 在补丁空间搜索，A/B 门控，预算（goal.budget_usd）限位。

---

## 8. 风险与开放问题

**风险与缓解**

1. *蓝图表达力不足应对新颖任务* → infer 模式兜底（平台永不强制绑蓝图）+ spawn 口袋 + 涌现固化通道让蓝图从实践中长出来。
2. *蓝图维护成本* → 固化通道（自动起草）+ 模板库复用 + changelog 传承。
3. *双真相源漂移（spec vs 执行）* → blueprint_node 是唯一 join 锚；规范图不复制执行状态；content_hash 冻结。
4. *router 误分类* → 强制 fallback；高风险分支用 gate 兜底；fallback 率进 RSI 指标。
5. *组织性回潮（要求上画布编辑器）* → 风险条款书面保留；JSON 导入导出满足互操作。

**开放问题（已于 2026-11-17 定稿）**

- ~~Q1 gate 的编译映射~~ → **定稿：复用 goal 级 review 态**（status='review' + review_request + human_iterations 机制现成），决策以结构化评论回流，Phase B 落地。
- ~~Q2 condition 表达式语言~~ → **定稿：受限操作符集**（存在性/相等/比较/包含，序列化友好、可静态校验、零嵌入引擎依赖）。
- Q3 跨 goal 的蓝图参数化（goal 模板与蓝图的绑定面）：Phase C 再议。
- ~~Q4 涌现固化的触发条件~~ → **定稿：3 次同构**（同一 squad 的 infer 执行图连续 3 次同构即触发固化建议 finding，阈值作为 RSI 参数可调）。

---

## 9. 附录 A：蓝图 JSON Schema（草案，draft 2020-12）

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "flowloop/blueprint/v1",
  "title": "flowloop workflow blueprint",
  "type": "object",
  "required": ["name", "nodes", "edges"],
  "properties": {
    "name":        { "type": "string", "minLength": 1, "maxLength": 120 },
    "description": { "type": "string", "maxLength": 2000 },
    "nodes": { "type": "array", "minItems": 1, "items": { "$ref": "#/$defs/node" } },
    "edges": { "type": "array", "items": { "$ref": "#/$defs/edge" } }
  },
  "$defs": {
    "nodeId":  { "type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}$" },
    "portId":  { "type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}$" },
    "assignee":{ "type": "string", "pattern": "^(agent|squad):[a-z0-9_.-]+$" },
    "template":{ "type": "string", "comment": "brief 模板，插值 {{goal.<field>}} / {{node.<id>.output}}" },
    "node": {
      "type": "object",
      "required": ["id", "type", "title"],
      "properties": {
        "id":    { "$ref": "#/$defs/nodeId" },
        "type":  { "enum": ["task", "condition", "router", "gate", "spawn", "end"] },
        "title": { "type": "string", "maxLength": 200 },
        "task": {
          "type": "object",
          "required": ["assignee", "brief"],
          "properties": {
            "assignee": { "$ref": "#/$defs/assignee" },
            "brief":    { "$ref": "#/$defs/template" },
            "verify":   { "type": "object",
                          "properties": {
                            "mode":      { "enum": ["domain_default", "machine", "agent"] },
                            "verifier":  { "type": "string", "comment": "agent id when mode=agent" }
                          } }
          }
        },
        "condition": {
          "type": "object",
          "required": ["expr", "cases"],
          "properties": {
            "expr":  { "type": "string", "comment": "受限操作符表达式，字段引用 {{goal.<field>}}" },
            "cases": { "type": "array", "minItems": 1,
                       "items": { "type": "object", "required": ["id", "test"],
                                  "properties": { "id": { "$ref": "#/$defs/portId" },
                                                  "test": { "type": "string" } } } },
            "else":  { "$ref": "#/$defs/portId", "default": "else" }
          }
        },
        "router": {
          "type": "object",
          "required": ["classes", "fallback"],
          "properties": {
            "classes":  { "type": "array", "minItems": 2,
                          "items": { "type": "object", "required": ["id", "title", "description"],
                                     "properties": { "id": { "$ref": "#/$defs/portId" },
                                                     "title": { "type": "string" },
                                                     "description": { "type": "string" } } } },
            "fallback": { "$ref": "#/$defs/portId" },
            "budget":   { "type": "object", "comment": "可选：路由 run 的预算/模型约束" }
          }
        },
        "gate": {
          "type": "object",
          "required": ["prompt", "actions"],
          "properties": {
            "prompt":   { "type": "string", "comment": "给人看的证据/表单（Markdown）" },
            "actions":  { "type": "array", "minItems": 1,
                          "items": { "type": "object", "required": ["id", "label"],
                                     "properties": { "id": { "$ref": "#/$defs/portId" },
                                                     "label": { "type": "string" } } } },
            "timeout":  { "type": "object",
                          "properties": { "after": { "type": "string", "comment": "如 72h" },
                                          "action": { "$ref": "#/$defs/portId" } } },
            "auto":     { "type": "object",
                          "required": ["when", "action"],
                          "comment": "运行时自动判定：上游 sealed output 满足 when 时自动执行 action（decided_by=auto）；不满足则继续等人",
                          "properties": {
                            "when":   { "type": "string", "comment": "单条 node.<id>.output <op> \"literal\"；<id> 必须是直接上游" },
                            "action": { "$ref": "#/$defs/portId" } } }
          }
        },
        "spawn": {
          "type": "object",
          "required": ["assignee", "brief", "child_type"],
          "properties": {
            "assignee":  { "$ref": "#/$defs/assignee" },
            "brief":     { "$ref": "#/$defs/template" },
            "child_type": { "enum": ["task"] },
            "max_children": { "type": "integer", "minimum": 1 }
          }
        },
        "end": { "type": "object", "properties": { "outcome": { "enum": ["success", "failure"] } } }
      }
    },
    "edge": {
      "type": "object",
      "required": ["source", "source_handle", "target"],
      "properties": {
        "source":        { "$ref": "#/$defs/nodeId" },
        "source_handle": { "$ref": "#/$defs/portId" },
        "target":        { "$ref": "#/$defs/nodeId" }
      }
    }
  }
}
```

（JSON Schema 只覆盖 V1/V4/V5 的字段层；V2/V3 拓扑与端口完备性校验是 Go 语义校验器，参照 §4.5 清单。）

## 10. 附录 B：示例蓝图（issue 修复流程）

```json
{
  "name": "issue 修复流程",
  "description": "issue 类 goal 的标准修复编排",
  "nodes": [
    { "id": "n_repro",  "type": "task",   "title": "复现与定位",
      "task": { "assignee": "agent:coder", "brief": "复现 issue {{goal.source_ref}} 并定位根因，产出修复方案。",
                "verify": { "mode": "domain_default" } } },
    { "id": "n_route",  "type": "router", "title": "修复策略路由",
      "router": { "classes": [
          { "id": "local_fix",  "title": "小改动直接修",   "description": "diff <200 行且不跨模块" },
          { "id": "spec_first", "title": "大改动先出方案", "description": "接口变更或跨模块" } ],
        "fallback": "spec_first" } },
    { "id": "n_fix",    "type": "task",   "title": "实施修复",
      "task": { "assignee": "agent:coder", "brief": "按 {{node.n_repro.output}} 的方案实施修复。" } },
    { "id": "n_split",  "type": "spawn",  "title": "拆分测试任务",
      "spawn": { "assignee": "agent:coder", "brief": "按改动面为 {{node.n_fix.output}} 拆分必要的测试项", "child_type": "task", "max_children": 4 } },
    { "id": "n_gate",   "type": "gate",   "title": "人工把关",
      "gate": { "prompt": "复核修复 {{node.n_fix.output}}",
                "actions": [ { "id": "approve", "label": "通过" }, { "id": "reject", "label": "打回" } ],
                "timeout": { "after": "72h", "action": "reject" } } },
    { "id": "n_end_ok", "type": "end",    "title": "完成", "end": { "outcome": "success" } },
    { "id": "n_end_no", "type": "end",    "title": "失败", "end": { "outcome": "failure" } }
  ],
  "edges": [
    { "source": "n_repro", "source_handle": "done",   "target": "n_route" },
    { "source": "n_repro", "source_handle": "failed", "target": "n_end_no" },
    { "source": "n_route", "source_handle": "local_fix",  "target": "n_fix" },
    { "source": "n_route", "source_handle": "spec_first", "target": "n_gate" },
    { "source": "n_fix",   "source_handle": "done",   "target": "n_split" },
    { "source": "n_fix",   "source_handle": "failed", "target": "n_end_no" },
    { "source": "n_split", "source_handle": "done",   "target": "n_gate" },
    { "source": "n_split", "source_handle": "failed", "target": "n_end_no" },
    { "source": "n_gate",  "source_handle": "approve", "target": "n_end_ok" },
    { "source": "n_gate",  "source_handle": "reject",  "target": "n_end_no" },
    { "source": "n_gate",  "source_handle": "timeout",  "target": "n_end_no" }
  ]
}
```

编译效果：`CompileBlueprint` 建 7 行 blueprint_node；n_repro 释放时 leader 填充 brief 成具体 sub_goal；n_route 在 n_repro settle 后跑一次路由 run（只能选 local_fix/spec_first 之一，异常走 fallback）；n_split 拆的每个测试任务登记为 `n_split#1..k`；goal 只有在全部节点 settled/skipped 且 n_end_ok 可达时才能 done。同一蓝图再跑同一 goal：相同拓扑、相同 depends_on、相同判据——**可序列化 = 可重放 = 可度量 = 可优化**。

---

## 11. Phase B 设计定稿：项目-小队-蓝本三层串联（2026-11-18，用户已认可）

> 背景：用户提出「小队本身也要结构化——小队提供的是什么能力？怎么和蓝图结合定义真实的工作流？要把项目-小队-蓝图串起来。训练后的蓝图就是真实的工作流」。以下是对该想法的批判性定稿：三条采纳、两个前提修正（见各小节「陷阱」标注）、一个提法澄清（双簿记）。

### 11.1 三层各归其位：goal 是唯一 join point

```
项目(domain) ──提供──> 软关联：推荐小队/推荐蓝本（新建任务下拉过滤，不硬绑定）
小队(squad)  ──声明──> 能力词汇表：member.role 规范枚举（reviewer/go-dev/…）
蓝本(blueprint) ──引用──> 节点写 {"assignee": "capability:go-dev"}
                          Activate 时确定性解析（§11.3 规则表）
                          解析结果快照进 blueprint_node.resolved_config
三个概念只在 goal 创建时相交（项目 × 小队 × 蓝本），不构成硬层级。
```

- **蓝本故意不绑定项目**：蓝本是流程模板（「调研→方案→实现」这个形状），同一份可在任何项目下复用；项目归属发生在任务层（新建任务时同时选 domain 和 blueprint）。这是设计决策不是遗漏。
- **三层串联靠 capability**：goal 选定小队 → 蓝本节点的 `capability:` 引用在该小队名册内解析 → 项目提供验收策略与仓库。任何一层换人都只影响解析结果，不影响蓝本结构。

### 11.2 双簿记原则（对「训练后的蓝图就是真实的工作流」的定稿）

- ✅ **对的理解**：训练收敛后的蓝本版本 = 组织对这类任务的**最佳已知流程**——每版被同题 A/B 实证过，越来越接近现实。
- ❌ **危险的理解**：训练后蓝本变成可变的「实际流程」实体，计划与记录合并。会一次性毁掉：A/B 复测（没有 v1/v2 就没有对照）、回滚（没有 before_hash）、审计（说不清哪个版本产出了哪个结果）、guard 判据（完成度对照钉住的版本算，版本漂移守卫失效）。
- **定稿**：蓝本版本 = 计划（不可变、可版本化、可 diff）；执行图 = 记录（从 runs 读时装配、永不可编辑）；训练是连接两者的**桥**（finding → 结构化补丁 → 新版本 → 同题 A/B → 双图 diff）。计划收敛于现实，但永远不**变成**记录——菜谱越改越准，但菜谱和做菜日志不能合并成一张纸。

### 11.3 capability 解析规则表（确定性 = 同输入必同输出，歧义 fail-closed）

| 规则 | 内容 |
|---|---|
| 语法 | `capability:<role-slug>`，slug 匹配 `^[a-z][a-z0-9_-]*$`；与 `agent:`/`squad:` 并列第三种 assignee |
| 词汇表 | 即 `squad_member.role` 的 `LOWER(TRIM(role))` 规范形（先例：REVIEWER-only 规则 subgoal.go:109 已硬匹配该形式，本设计把它形式化为全平台能力词汇表，不新造表） |
| 检索范围 | goal 自己的小队（`goal.assignee_type='squad'` 且成员含该 role）；agent-assigned goal 上 `capability:` 编译报错（无处检索，fail-closed） |
| 过滤 | 排除 reviewer（REVIEWER-ONLY RULE 沿用：reviewer 只评审不执行） |
| 多命中 | 确定性选择：非终态 sub_goal 数最少者优先（负载感知），并列取 squad_member.created_at 最早——结果快照进 resolved_config，同输入必同输出 |
| 零命中 | 编译失败（goal 留 backlog，错误信息列出小队实际拥有的 role 词汇） |
| 快照 | resolved_config = `{"assignee_spec":"capability:go-dev","resolved_agent":"<id>","team_squad_id":"<squad>"}`——审计可回放「这个节点当时解析给了谁」 |

### 11.4 spawn 节点语义（队内扇出的结构化替代，登记式拆分）

- **动机**：Phase A 后 blueprint goal 内 `CreateSubGoal` 对所有人结构拒绝——队长无法把节点工作再拆给队员。这是 leader 认领制与规范图的真实残留冲突：要改的不是认领制（认领只是派发方式），而是队内扇出缺结构化通道。
- **规范**：`spawn{assignee, brief, child_type:"task", max_children}`，端口 done|failed；`max_children` 上限 8（引擎级常量）。
- **编译拍 1**：spawn 节点编译为一个 sub_goal（assignee 按上表解析，`squad:` → 队长），`team_squad_id` 记为该节点小队——队长 run 的简报带全名册。
- **运行拍（spawn 完成）**：引擎在 spawn sub_goal 的 verified 事务内解析其 run result_summary 的 JSON 数组（brief 指令要求最后一行输出 ```json [{title,brief,assignee}]```），确定性物化子任务：
  - 每项 assignee 必须是 spawn 小队的非 reviewer 成员（越界 → spawn 失败可重试）；
  - 数量 > max_children → spawn 失败可重试（fail-closed，不静默截断）；
  - 子任务 depends_on=[spawn sub_goal]，created_by_type='blueprint'（引擎物化非 agent 创建）；
  - **下游门控**：spawn 的每个规范图直接后继的 depends_on 追加全部子任务 id——下游等所有子任务 verified 才释放（复用 wakeDependentsTx 既有语义，引擎零新机制）；
  - 非法 JSON / 空数组但格式合法 → 空数组允许（队长判定无需扇出，在受控自由内）；非法 JSON → spawn 失败（重试走引擎既有 attempt 上限）。
- **计划 vs 现实**：子任务不是 blueprint_node（不在规范图）——它们出现在执行图里。规范图显示 spawn 一个节点、执行图显示实际 N 个子任务，这正是双簿记想要的可视对照。
- **guard**：子任务是普通 sub_goal，非终态即守卫挂起——引擎既有 pendingSG 计数覆盖，零新机制。

### 11.5 节点级团队简报（修多小队空白简报 bug）

- **现状 bug**：`buildTeamProfile`（team_context.go:185）只查 goal 自己的小队——多小队蓝图里 B 队成员跑节点时拿到空白简报（goal 的 squad 不是他的队）。
- **定稿**：`sub_goal.team_squad_id TEXT NOT NULL DEFAULT ''`（编译时写入：`squad:`/`capability:` 节点=该小队，`agent:` 节点与 infer goal=''）；run 的团队简报优先取 sub_goal.team_squad_id，空则回退 goal 小队（现状语义保持）。
- spawn 子任务继承 spawn 的小队——队员 run 全部看得到名册。

### 11.6 leader 换岗与 infer 永久保留

- leader/steward 从「编舞师」（自由拆解）换岗为「集成者+异常处理器」：监控（subgoal list）、集成（change integrate）、评论汇报、--ask 问用户、异常处理（spawn 计划本身也是一种受控的创作）。
- **infer 模式永久保留**：新颖任务的探索口袋；同构信号（3 次同构，Q4 定稿）是 leader 发现的顺序变成结构化蓝本的通道——**别删 leader，给 leader 换岗**。

### 11.7 分期与验收

| 步 | 内容 | 验收 |
|---|---|---|
| B-1 | spawn 节点 + team_squad_id 简报 | spawn 物化 ≤ max_children、成员越界拒、非法 JSON 失败可重试、下游等全部子任务；多小队 goal 每节点拿到自己队名册（回归测试锁定） |
| B-2 | capability assignee + role 词汇表形式化 | §11.3 规则表逐条可单测；蓝本换人不动结构 |
| B-2.1 | 角色词汇表注册表（本节 §11.8） | 注册过词汇表的小队成员 role 必须来自注册表；未注册小队自由文本兼容；蓝本新建页可见各队词汇（口径对齐） |
| B-3 | router / gate 节点 | router 决策全落在注册类集合内 + fallback；gate 复用 goal review 态（Q1 定稿） |

**风险**：spawn JSON 是新的自然语言边界——缓解：成员/上限/依赖全引擎校验，LLM 只决定「拆几件、谁干、干什么」三件事；capability 负载感知选择依赖 mutable 状态，但快照进 resolved_config 保证同次编译可回放。

### 11.8 角色词汇表注册表：技能入词、口径对齐、role 即岗位（2026-11-18 定稿，用户认可）

**背景（用户指出的问题）**：A、B 两位成员 role 同为 implementer，但 A 挂专有技能 skill-a、B 挂设计技能 skill-b——蓝本要专有功能时 capability 解析只看 role+负载，会「谁空闲选谁」，意图没有任何通道表达。而 `agent.skills`（schema agent 表 :132）数据明明就在表里，匹配时被无视。

**定稿：技能差异编码进角色词（词汇表单轴封闭）**——不引入第二个匹配轴（不做 `capability:implementer+skill-a` 语法）：

- 小队的意义就是角色差异化：差异间协同、各自擅长。**技能差异大到影响派工 = 它们是不同岗位**，必须在词汇表里分成不同的词（如 `proprietary-implementer` vs `design-implementer`）；技能差异不影响派工的，不该进蓝本。角色词即岗位，skill 收窄为「这个人身上的工具箱配置」（挂在该 agent 上，不再参与编译期匹配）。
- 为什么拒绝双轴：role 是 squad_member 的静态声明，skill 是 agent 上的动态配置（随时挂/摘）——两轴交集会漂移（今天 skill-a 只有 A 挂着，明天 B 也挂上，同一份蓝本解析结果默默变了），resolved_config 的 A/B 对比失去控制变量；半命中态（role 中但技能不中）让 fail-closed 的「缺什么」说不清。**词汇表的价值在封闭、可枚举、无歧义——单轴是底线。**

**角色注册表（参考 skill 的管理方式：命名 + description）**：

- 新表 `squad_role`（id, squad_id, name, description, created_at, UNIQUE(squad_id,name)）——**每小队一份词汇表**（capability 检索域本来就是 goal 自己的小队，§11.3）。
- name 即 capability 引用的 slug（`^[a-z][a-z0-9_-]*$` 规范形）；description 是岗位说明书：这个角色干什么、隐含什么技能/经验、适合被派什么活——给蓝本作者和小队 owner 看的**口径对齐文案**，也喂 spawn 规划者。
- 保留词：`reviewer`（REVIEWER-ONLY RULE 结构性保留，始终合法）、`member`（legacy 缺省词）。注册过词汇表的小队，成员 role 必须是保留词或注册词（**AddMember fail-closed**）；从未注册词汇表的小队保持自由文本（模板 apply / team import / 既有小队零破坏——迁移路径：想强一致就先注册词汇表）。
- 口径对齐的强制面：小队详情页可维护注册表；蓝本新建页展示各小队已注册的词汇（作者写 `capability:` 时有词可查，不再靠脑内对齐）。§11.3 的「零命中错误列出小队实际拥有的 role 词汇」与此同一动机：错误信息即对齐界面。

**system prompt 与迭代故事（用户定稿方向）**：两层分离——

| 层 | 放哪 | 内容 | 迭代通道 |
|---|---|---|---|
| 岗位 | squad_role.description | 这个角色在本小队干什么、隐含什么技能 | W-RSI 提案可产 role 描述补丁（提案→人工批准→版本） |
| 人设 | agent.system_prompt | agent 全局行为风格 | W-RSI 提案已含 prompt_version 回滚目标——system prompt 在迭代中可修改，走同一提案闸门 |

岗位描述**不替代** system prompt（一个是岗位说明书，一个是人格底座）；两者都可被 RSI 提案修改，但都必须过人工批准——这正是 §11.2 双簿记+人在环的既有闸门，不新增直通道。

---

## 12. Phase C 设计定稿：优化闭环（2026-11-18 定稿，用户已批准执行）

> 验收目标（§7-C）：一次完整的 **finding → patch → 批准 → A/B → 采纳** 闭环可跑通并展示给用户。四样东西一个都不能少：可度量的规范、结构化提案、人工批准闸门、可对比的复测。

### 12.0 Q3 定稿（跨 goal 的蓝图参数化）

绑定面维持 Phase A 现状：**goal 字段白名单（`{{goal.*}}`）+ 上游节点输出（`{{node.<id>.output}}`）**，编译拍 1 / 释放拍 2 解析。不做跨 goal 全局参数表——那是 Phase D 预算/参数化的一部分。理由：当前所有真实绑定诉求（标题/描述/类别/来源引用）都被白名单覆盖；参数表一旦引入就要面对参数漂移、缺省继承、版本间参数变更语义三座山，MVP 不接。

### 12.1 结构化补丁提案（`blueprint_proposal` 表）

```
finding/人 ──▶ 补丁提案（add/update/delete node/edge）──▶ 校验器双检 ──▶ 人工批准
                                                                       │
    新版本草稿（changelog = hypothesis） ◀── apply 到 base 版本内容 ◀────┘
```

- 新表 `blueprint_proposal`（id, blueprint_id, base_version_id, title, hypothesis, expected_metric, expected_direction, patches_json, finding_id?, status pending|approved|rejected, applied_version_id, decided_*, created_*）。
- **patches_json = 结构化操作数组**，五种 op：`add_node`（完整 SpecNode）/ `update_node`（同 id 同 type 全量替换 body）/ `delete_node`（级联删相连边）/ `add_edge` / `delete_edge`。
- **校验器双检**：创建时 apply→V1–V6 全量校验（不合法当场拒）；批准时**重新** apply+校验（base 内容不可变 + patches 不可变 ⇒ 确定性，双检防的是库被旁路改动的漂移，同 V6 content_hash 精神）。
- **批准 = 开新草稿**（changelog=hypothesis，applied_version_id 记录），**不自动发布**——发布仍是独立人工动作（W-RSI「提案不自动应用」原则原样保留）。绑定新任务的采纳决策因此也永远是人的决策。
- finding_id 可选字段：W-RSI finding（suggestion 文本）→ 人把建议结构化成补丁 → 提案闭环。finding→patch 的翻译由人完成（LLM+P 分工：人/LLM 提议，校验器执法）；Phase D 才让机器自动翻译。
- **显式「下一轮」入口**（2026-11-21）：批准只开草稿这一事实会让 RSI 停在「人不推、迭代不转」。`POST /blueprint-proposals/{proposalId}/next-round` 把后续两步合成一次**人工触发**的动作：发布 applied 版本（若仍 draft）→ 由 finding→report→training_session 反查原训练目标 → 继承 domain/assignee/验收/采样/预算，建 `execution_mode=train`、`source_ref="rsi-proposal:<id>"` 的新目标并钉新版；`{"start":false}` 只建 backlog，默认直接 active。幂等（重复调用返回同一 goal）。批准本身仍不发布、不建目标。

### 12.2 三色 diff（提案视图）

`ProposalDiff`：apply patches 得 patched 图，与 base 图做结构化 diff——nodes_added / nodes_removed / nodes_changed（按 id 对齐，含字段级前后值）/ edges_added / edges_removed。前端：左右两张规范图并排 + 彩色变更清单（+绿 / −红 / ~琥珀）。规范图零改动复用（§4.12 一渲染器两图味）。

### 12.3 同题 A/B 对照（复测面）

`GET /workflows/blueprints/{id}/ab?goal_a&goal_b`：两个 goal 的执行图 metrics 并排 + delta 列。比较项用**平台已实测的指标**：critical_path_ms / wall_ms / parallel_speedup / rework_ratio / verify_overhead_ratio / human_wait_ms。verify_pass_rate 与 cost_per_success 留给 Phase D（需要验证轮次语义规整 + 预算埋点，当前平台没有这两个数——不造数）。goal 级对照是 A/B 的最小诚实实现：同一 issue 的配对复测（RSI.md P3 的 same_issue_ab）在预算埋点后升级。

### 12.4 涌现固化通道（Q4 定稿的落地：3 次同构）

```
infer goal 终态(done) ──▶ 执行图 shape 签名 ──▶ infer_shape 入账（按 domain）
                                │
              同域连续 3 次同签名 ──▶ 自动起草新蓝本 v1 草稿（确定性翻译，不发布）
```

- **签名**（v1 启发式，诚实标注）：节点 kind 多重集 + 规范化边三元组 (from_kind|to_kind|edge_kind) 多重集，排序后 SHA256。不含 agent 身份（同构=结构同构，人换了不影响）；不做到图的同构 canonization（NP 难方向），多重集签名是便宜的第一道滤网——误报（结构碰巧同）只会多起草一份草稿，草稿本身零风险（不发布不绑定）。
- **触发**：goal:finished(status=done) 且 goal.blueprint_version_id=''（infer 模式）；同 domain 最近 3 条 infer_shape 签名全等 → 起草。
- **翻译（确定性，无 LLM）**：sub_goal → task 节点（id=sgN、title、assignee=agent:<实际执行者>、brief=终态 description、verifier 带 verify 块）；depends_on → done 边；追加共享 end_ok。Create 走既有校验门——非法即弃（fail-closed，只记日志）。
- **幂等**：起草名 = `固化：<签名前8位>·<domain名>`，blueprint.name UNIQUE 撞名即跳过（同形状重复触发不双开）。
- 产物是普通草稿：列表可见、description 注明「3 次同构自动起草」、发布权在人。阈值 3 硬编码 const（Q4 说可调——RSI 参数化随 Phase D 配置面，v1 不做配置项）。

### 12.5 验收（Phase C 完成判据）

1. 补丁提案：非法补丁创建被拒；合法补丁批准后生成带 patched 内容的新草稿（changelog=hypothesis）、重复批准冲突。
2. diff：ProposalDiff 的 add/remove/change 清单与构造的补丁一致。
3. A/B：两 goal 对照端点返回双方 metrics + delta。
4. 固化：同域 3 次同构 infer goal 触发自动起草（确定性翻译通过校验）；形状不同不触发；重复触发不双开。
5. 全量回归 + 浏览器实测（提案批准→diff 视图→A/B 卡→固化草稿出现在列表）。

## 13. Phase D 设计定稿：自动化与度量补全（2026-11-18 定稿，用户已批准执行）

### 13.1 verify_pass_rate 落地（第 2 层指标补全）

`verify_pass_rate` = verified 工作项中**首轮验证通过**的占比。数据源是执行图
组装时已拉取的 verification_result 派生量（agent 节点 Reworks==0 即首轮过），
零新埋点。落点两处：执行图 metrics（`workflow.go`）与 `ABMetricKeys`
（rsi.go，A/B 对照第 7 个比较项，位于 rework_ratio 之后）。
`cost_per_success` 诚实缺席：run 表无 token/成本列，平台不采集模型侧成本——
不造数（沿用「no fake numbers」原则），留待真正接入计费信号。

### 13.2 确定性 finding→补丁翻译器（LLM+P 的 P 侧自动化第一步）

Phase C 里 finding→patch 是人工翻译。Phase D 把其中**可确定性翻译**的类别自动化：
`internal/service/rsi_translate.go` 注册了 11 个 finding→补丁翻译器，与
`rsi_analyze.go` 产出的 11 类 finding 一一对应。翻译规则全部确定性、零 LLM：
先用执行图定位热点，再回蓝本 spec 找对应的 task/gate 节点，生成
`add_node` / `update_node` / `delete_node` / `add_edge` / `delete_edge` 补丁。

共享前提（任一不满足就静默跳过，不会乱补）：

- 前提：goal 钉了蓝本版本（infer goal 无 spec 可补丁，跳过），且蓝图已编译
  （`blueprint_node` 有记录）。
- 映射：执行节点 `sg:id` → `blueprint_node.node_key`；非 spec 任务（spawn 子项、
  consult 等）没有蓝图节点可补，直接放弃。
- 形状检查：补丁前先在 spec 上确认目标节点类型与端口合法（spec 是真相，
  执行图只是线索）；链→fork 时末成员的 done 边必须直达唯一的 end 节点。
- 数据流检查：被拆分/替换/删除的 task，若其 brief（或 gate prompt）被另一个
  节点的 `{{node.X.output}}` 引用，翻译器放弃该条——fork 会断引用。链→fork
  另外保守检查链外节点对非末成员的引用（V1 的单端口出边使这种引用在合法
  spec 里几乎不可达，但一旦存在就是静默断流，宁可放弃）。
- 团队边界：`split_candidate` 只翻译 assignee 为 `squad:`/`capability:` 的
  task。compile 只为这两类挂 `team_squad_id`；`agent:` 归属的 spawn 子项只能
  派给规划者自己，而执行器按 agent 单飞，拆出来的子项会被串行化——只多一层
  规划开销，没有并行收益，所以不生成。
- 验收契约：task 带自定义 verify（`mode` 为 `machine`/`agent`，非
  `domain_default`）时不做 task→spawn 拆分、也不作为 merge 的被删末梢——
  spawn 没有 verify 体，V4 只为 `type=task` 配 verifier，强转会静默丢掉验收人。
- 执行者边界：merge 只吸收与 keeper 同 assignee 的末梢（被删 brief 由 keeper
  的执行者承接，跨执行者等于悄悄改派，与 analyzer 的 suggestion 相悖）。
- 安全闸门：翻译产物仍走 CreateProposal 的完整 V1–V6 校验（fail-closed），
  提案状态 pending——**翻译器永不发布，人工仍是闸门**（§12.1 不变）。
- 防重：同一轮分析每类最多起草 1 条、整轮最多 4 条。同一轮内按补丁效果签名
  （`patches` JSON）去重；跨轮按提案 title 去重（`rejected` 也算——人工否决
  过的同一条确定性转化不会每轮重来；想重来请改 title 或删旧提案）。
- 预算：`rsi.max_auto_rounds` 实际计的是**每个蓝本累计 pending/approved 自动
  提案数**（不是分析轮次；默认 10，每轮最多 4 条），翻译器计数与人工提案/发布
  完全分开。

翻译器一览（`findingTranslators`）：

| finding 类别 | 触发 | 确定性补丁 | 预期指标 |
|---|---|---|---|
| `serial_chain` | 关键路径 ≥4 串行 task | 删 k−1 条链边，成员全部直连共享 end（链→fork，all-or-nothing） | wall_ms ↓ |
| `lost_parallelism` | 可并行的串行段 | 同上；同轮已有 serial_chain 时让位，避免双开 | parallel_speedup ↑ |
| `split_candidate` | agent busy 占比 ≥50%，且 assignee 为 `squad:`/`capability:` | task→spawn（同 id、同 assignee/brief、max_children=2），重连所有出入边 | agent_busy 占比 ↓ |
| `merge_candidate` | ≥3 个同时收尾的 task 末梢；被合并叶时长和 ≤ min(5min, 10% wall) | 最多 2 个最小末梢并入最重的 keeper（同 assignee 且同 verifier）：入边改指、brief 追加「合并交付」，删除末梢 | wall_ms ↓ |
| `human_wait` | 人工等待占比 ≥30% 且 ≥10min | 成功 end 前插「人工审核证据包」task（4 项证据），原 done 入边改指到它 | human_wait_ms ↓ |
| `wrong_assignee` | 最忙 lane ≥5min 且 ≥2× 其余中位数 | 把最忙 lane 上最多 2 个 task 改派给最闲 lane | wall_ms ↓ |
| `brief_quality` | 受影响任务 ≥2 且被拒 ≥3（或单任务被拒 ≥2） | task brief 追加「验收标准」块（幂等 marker） | rework_ratio ↓ |
| `retry_amplification` | agent 节点 attempt ≥3（=2 次重试） | brief 追加「执行前置检查」块 | wall_ms ↓ |
| `consult_repeat` | 同一子项 ≥2 次咨询 | brief 追加「派发前确认清单」块 | wall_ms ↓ |
| `ping_pong` | 同一对 agent 双向各 ≥2 次咨询 | 相关 task brief 追加「一次性问题清单」块 | wall_ms ↓ |
| `plan_deviation` | 计划/执行偏离 | 入口 task（无入边）brief 追加「计划节点纪律」块 | wall_ms ↓ |

**规则撞脚与去重**：同一轮里，每个 draft 会记录自己触碰的节点/边身份
（`n:<id>`、`e:<src>\x00<handle>\x00<tgt>`），后一个 draft 与已触碰集合相交即跳过；
启动时还会把同一 blueprint 上 `pending/approved` 提案的 `patches_json` 触碰集并入，
避免与在线提案重复改同一节点。结构类（fork/split/merge/human pack）先于简报类
（brief/retry/consult/ping/plan_deviation）。已知限制：同一 base 的两个提案
“批准 P1 → 再批准 P2 并发布”会丢 P1 的改动（ApproveProposal 不做 base 新鲜度检查），
操作上应按“批准 → 发布 → 再批准下一条”串行进行。

brief 加固块都用固定标题做幂等 marker，重复分析不会重复追加。`human_wait`
不猜 gate 上游的输出结论（gate auto 规则需要真实 result_summary），而是用
证据包把人工检查提前到交付前，仍不自动改 gate 动作。证据包提案用固定 title，
跨轮去重由 title 完成；另外 spec 里已有 `人工审核证据包`/`rsi_review_pack*`
节点时直接跳过（下一轮 goal 钉到含证据包的新版本时生效；同一 goal 的
`blueprint_version_id` 是写入即固定，不会换 base）。

挂点：Analyze 提交后 `draftAutoProposals`（log-and-continue，翻译失败不影响
分析本身）。测试在 `internal/service/rsi_translate_test.go`：每类翻译器都
断言产物能通过 ApproveProposal 的完整校验，并覆盖效果去重、注册表完整性、
跨执行者 merge 与 `agent:` split 的跳过路径。

已知边界（既有链路限制，不是本次翻译器引入；换自动提案池后暴露面变大，按
现状记录）：

- `finding_id` 生命周期：每轮 Analyze 会按 session 重建 `rsi_report`/`rsi_finding`，
  旧提案的 `finding_id` 随之失效；由 finding 反推下一轮的 `nextRoundOrigin`
  要求该 finding 仍在最新一轮。批准/发布要在 finding 被下一轮覆盖前完成
  （Phase C 的 serial_chain 提案同样如此）。
- 同 base 多提案：一次分析最多产出 4 条，全部以同一钉住的 base 版本校验。
  ApproveProposal 只校验记录里的 base、不检查 base 是否已过期，因此请逐条
  「批准 → 发布 → 再批准下一条」；同时批准两条再分别发布，后一条草稿会丢掉
  前一条的改动。
- cap 语义：见上文「预算」——按提案数计的自动化闸门，不按分析轮次计。

### 13.3 冗余缩减与前端精简（2026-11-18 用户指令）

- `web/lib/api.ts`：删除 7 个全前端零引用的函数（getRuntime/getAgent/
  getSquad/getSchedule/listBlueprintVersions/createBlueprintProposal/
  listHookEvents）。
- 「项目仓库」页（domains）：**不是死模块**——domain 是 goal.domain_id 外
  键目标、验收策略 Checks 是 verify 机制依据、webhook secret 是 issue
  webhook 签名基础，删概念会断 goal/verify/RSI 三条主链。精简方向=页面拆
  薄：卡点统计与全局 webhook secret 两个低频管理面板收进折叠（默认收起，
  首屏只剩仓库列表）；编译确认卡/进度流保持原位（那是冻结验收策略的人工闸门）。
- `workflow_edge` 预留空表维持不动（§5 已声明，零读写路径）。

### 13.4 Phase D 边界（不做清单）

- 不做 MCTS/AFlow 式自动搜索（远期 Phase E 话题）；
- 不做 finding→patch 的 LLM 翻译；确定性翻译已覆盖 11 类 finding 中可安全
  机械化的情形，需要语义重排/语义判断的补丁仍走人工翻译通道（LLM 侧未来再做）；
- 不做 cost_per_success（无数据源不造数）；
- 不删任何「感觉没用」但实际在主链上的模块（domains/templates/skills/
  IM 均核实为活功能——分别是 goal 归属/环境冷启动/config.push 推送目标/
  飞书通知通道）。

## 14. 收敛补强与迭代护栏（2026-11-18 用户定稿）

用户问题（m04460/m04472）："涌现固化在复杂场景的收敛是不是太困难了？
效率可提升但准确性谁验收？给一个默认最高迭代轮次配置。"

### 14.1 双层签名收敛（L0 骨架 / L1 精确）

问题确认：L1 全量签名要求两次执行的**全部**节点与边同构——多一轮
consult、一次 review、一次返工都会破坏同构。复杂场景凑齐「同域连续 3
次全量同构」确实难——收敛困难是真实的，不是错觉。

定稿（不放宽零风险原则，而是给收敛加一层镜头）：

| 层 | 内容 | 触发动作 |
|---|---|---|
| L1 精确 | 现有全量签名（节点 kind 多重集 + 边三元组多重集） | 3 次同构 → 「固化」草稿（同 §12.4） |
| L0 骨架 | 剥离运行间噪声：verify/review/consult 节点、seq（时序推断）/verified_by/consult 边、返工轮归一（Reworks/Attempt 不入签名——本来就只签 kind 不签数值） | 3 次骨架同构且 L1 不同 → 「近似固化」草稿 |

要点：
- 近似固化草稿**命名键 = L0 骨架签名**（不是 L1），同域后续 near-miss
  三元组继续 dedup 进同一份草稿，不堆积；
- 近似固化同样**只起草、不发布**——复杂场景收敛了，人工闸门没松；
- 仍不引入 LLM 判断「形状相似否」——两层都是确定性哈希，可解释可复现；
- 诚实标注保留：L0/L1 都是多重集启发式，不是图 canonization；误命中最坏
  结果 = 多一份等人审的草稿，零风险（§12.4 原则不变）。

### 14.2 自动迭代轮次上限（rsi.max_auto_rounds）

用户定稿："给一个默认最高迭代的轮次配置。"

- 配置键 `app_settings` 表 `rsi.max_auto_rounds`（JSON 字符串值），
  **默认 10**；`0` = 关闭自动起草；非法值回落默认；
- 计数口径：**按域计**——涌现固化以 `infer_shape.drafted=1` 行数计，
  翻译器提案以 `blueprint_proposal.created_by_id='rsi-translator'` 的
  distinct blueprint 数计；
- **分析永不被封**（可见性不许倒退）：cap 只封「优化工件的自动产出」，
  即自动草稿与自动提案的产生；`rsi_finding` 的发现、报告、metrics 照常；
- **人工动作永不在 cap 内**：人工创建的提案、批准、发布、A/B 全部不受
  限制——cap 只防"机器自己刷自己"，不防人；
- 到达上限只记日志跳过，不报错、不断流（安全网语义，非错误）。

### 14.3 评测集（做 ① 的方案，暂缓边界）

用户追问"如果要做 1 的话你会怎么做"。方案（未实施，属 Phase E）：

1. `domain` 加评测集声明：`eval_goals JSON`（一组历史 goal id，人工或
   半自动挑选的"同题集"）；

2. A/B 端点扩展：`GET /workflows/blueprints/{id}/ab?mode=eval` —— 不再
   现场挑两个 goal，而是**对评测集里每个 goal 用两个版本各跑一次**
   （复测 = 重新执行，非历史对比；这需要执行预算与并发调度，是 Phase E
   的主体工作量）；
3. 指标聚合从"单对 delta"升级为"集合均值 delta + 显著性符号检验"
   （复用 §12.5 的 min_pairs 语义：至少 5 对才有结论）；
4. 验收：评测集跑 v2 全面 ≥ v1（效率指标改善且质量指标不掉）才允许
   采纳建议（仍人工发布）。

成本诚实说明：重跑评测集 = 真实执行 = 花钱花时间。这正是 Phase D 没做
它的原因——「没有评测集就不叫自迭代，只能叫生成」，但补齐它需要执行
预算规划，不适合塞进本轮。

### 14.4 best checkpoint（同问，暂缓边界）

用户追问"是不是把 best checkpoint 能力也给加上"。结论：**版本链已经
是 checkpoint，缺的是"标注哪个最好"**。方案（Phase E）：

- 不新建机制：blueprint_version 的链本身就是全量 checkpoint（不可变、
  可回滚——goal 钉版本即回滚）；
- 补一列标注：`blueprint.best_version_id`（人工设置；A/B 卡上"设为最
  佳"按钮；评测集达标后建议设置）；
- 展示层：列表页/详情页徽章「最佳 v3」；新建任务绑定蓝本时默认选中
  best 版本（仍可改选）；
- 语义边界：best 是**标注不是开关**——不自动切流量、不自动发布，它只
  是回答"这么多版本里该用哪个"的导航位。AFlow 的 archive 教训（调研
  结论 b5）：**checkpoint 的价值在检索与复用，不在存储**——没有 best
  标注的版本链等于没有索引的仓库。

暂缓理由同 14.3：这两项都是真功能不是幻灯片，塞进 Phase D 尾巴会牺牲
验收质量；作为 Phase E 的第一批候选，与评测集同属"让 A/B 结论从人工挑
题升级为机器可执行"的主题。
