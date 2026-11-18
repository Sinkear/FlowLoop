# 设计提案：工作流蓝图（Blueprint）— 把编排权威从自然语言迁到可序列化图数据

> **状态：已批准（2026-11-17）— Phase A 启动**；Q1/Q2/Q4 开放问题按推荐定稿（见 §8）。
> 批准后动作：DESIGN.md 增补决策条目（编号顺延）→ 本提案升格为设计基线 → 启动 Phase A。
> 变更纪律遵循 DESIGN.md 头部条款：**先改文档、后改代码。实现与文档冲突时以文档为准。**
>
> **证据基线**（本仓库根目录归档，三份独立调研交叉收敛）：
> - `research-workflow-dsl-definitions.md` — 7 平台工作流定义格式（官方文档 + Dify 源码 + 262 个真实 DSL 导出语料）
> - `research-llm-supervisor-orchestration.md` — LLM 编排确定性的测量证据与业界退场史
> - `research-archive-turn3-main-report-and-self-iteration.md` — 自迭代工作流学术/工业 10 案 + 主报告
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
| `gate` | 人工卡点：表单/证据 + 决策按钮 + 超时分支（Dify Human Input 同构） | 每个 action id + `timeout` | 推荐复用 goal review 态机制（status='review' + review_request + human_iterations，schema.sql:204-208），决策以结构化评论回流；精确映射 Phase B 定稿 |
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
| V3 | condition 每个声明的 case、router 每个 class、gate 每个 action **都必须有出边**；router 必须声明 fallback；gate 必须声明 timeout 去向 | 治"分支悬空" |
| V4 | task 的 assignee 必须解析到已注册 agent/squad | 治"派给不存在的执行者" |
| V5 | 模板插值 `{{goal.*}}` / `{{node.<id>.output}}` 引用的字段必须存在于声明 | 治"参数断链" |
| V6 | content_hash（sha256）随版本快照落库，读取时校验 | 治"漂移" |

**这是图数据相对 prompt 编排的最大增量收益：prompt 无法静态校验，图可以。**

### 4.6 版本化与导入导出

- `blueprint`（命名实体）→ `blueprint_version`（不可变内容快照 + version 1..N + draft/published/retired）。
- **goal 绑定具体 version_id，引用钉版本**（Coze 子工作流模式）：确定性重跑 + 天然的同题 A/B 基础设施。
- 版本 diff = 结构化三色 diff（节点增/删/改、边增/删/改、参数变化），changelog 字段承载本版变更说明——**优化提案的 hypothesis 就落在这里**。
- JSON 文件导入导出（`agentwork blueprint export/import`），凭据类内容不入 DSL。

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

| 既有决策（docs/research/rsi-summary.md / DESIGN.md） | 本提案的调和 |
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

### Phase B — 权责隔离 + 全节点类型（治：篡位）

- steward/leader 角色契约代码化（§4.8 表全项落地）；router（枚举约束结构化输出 + fallback）/ gate（复用 review 机制，映射定稿）/ spawn（登记式拆分）三种节点；双图叠加 diff 视图。
- **验收**：blueprint goal 上 steward 无法创建图外 sub_goal（API 层拒绝）；router 决策全部落在注册类集合内；漏跑/越权/乱序在前端一眼可见。

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
  "$id": "agentwork/blueprint/v1",
  "title": "agentwork workflow blueprint",
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
