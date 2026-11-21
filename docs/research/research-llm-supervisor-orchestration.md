# LLM 管家/主管（Supervisor/Orchestrator）编排模式：工程问题与业界对策调研报告

> 调研背景：某平台用自然语言 system prompt 让"管家 Agent"按顺序调度子 agent，实际出现：执行顺序不固定、漏跑环节、管家自己把活干了不委派。本报告回答：业界公认约束手段是什么？LLM 该关在节点里还是当路由器？supervisor 模式何时仍值得保留？

## 0. 执行摘要（三问直答）

1. **"顺序固定、个别节点有弹性"落地的业界公认手段：把控制流从 LLM 收回到代码。** 具体为三层：① 外层用显式状态机/图承载步骤与顺序（代码边，非 prompt 边）；② LLM 关进节点内，只做受约束决策（JSON schema 受约束解码、受限工具面、迭代上限）；③ 用轨迹评测（trace vs 预期路径）做回归守护。没有任何主流框架靠自然语言 prompt 保证顺序。
2. **LLM 可以当"路由器"，但必须是受约束的路由器**——只能从预定义节点/handoff 工具集合中做结构化选择（LangGraph supervisor 的 handoff 工具、OpenAI Manager pattern 的 agents-as-tools 本质都是这个）；LLM 不该当"自由文本的流程控制器"。顺序可预枚举 → 顺序交给代码；顺序真的运行时不可知 → 才允许 LLM 逐步选下一节点。
3. **Supervisor 模式在"流程本身不可预知"时仍值得保留**：任务分解取决于输入内容（开放式研究、多文件代码改动）、需要运行时 re-plan、工具面大到必须分区。此时用 Magentic 式的账本（ledger）+ 停滞检测 + 轮数上限 + 计划签核来约束它。流程确定只是想省编程功夫时，业界一致答案是：别用。

---

## 1. 为什么 LLM 当编排者天然不确定

**机制层面的官方承认。** OpenAI 在 Structured Outputs 发布文中直接承认："model behavior is inherently non-deterministic—despite this model's performance improvements (93% on our benchmark), it still did not meet the reliability that developers need... So we also took a deterministic, engineering-based approach to constrain the model's outputs to achieve 100% reliability." 对复杂 JSON schema，gpt-4-0613 仅靠 prompting 遵从率 **<40%**，而 gpt-4o-2024-08-06 + Structured Outputs（受约束解码）= **100%**。机制：默认采样对每个位置上的任意 token 开放（"This flexibility is what allows models to make mistakes"）；约束法把 JSON Schema 编译成上下文无关文法（CFG），每生成一个 token 就重算一次合法 token 集合，物理上排除非法输出。见 https://openai.com/index/introducing-structured-outputs-in-the-api/
→ 推论：**"用自然语言规定执行顺序"属于 prompting 约束，是统计性偏好，不是硬约束**。同一段采样随机性意味着同一输入可产出不同调度序列。

**长上下文漂移。** 《Lost in the Middle》（Liu et al., arXiv:2307.03172）：多文档 QA 与键值检索任务中，模型性能随相关信息位置显著变化——"performance is often highest when relevant information occurs at the beginning or end of the input context, and significantly degrades when models must access relevant information in the middle of long contexts"，连显式长上下文模型也如此。对应平台现象：管家调度到第 N 个子 agent 时，"按顺序执行"的指令埋在长对话史中间，被跳过或遗忘。https://arxiv.org/abs/2307.03172

**指令遵从度本身是独立维度。** Google IFEval（arXiv:2311.07911）指出指令遵从没有标准化评测（人工贵、LLM 评有偏），于是定义 25 类"可验证指令"约 500 条 prompt 建基准——证明"prompt 说了模型就会照办"在工程上从来不成立，需要被当作单独的不确定性来源来评测。https://arxiv.org/abs/2311.07911

**错误的复合。** Anthropic《Building Effective Agents》：agent 自主性意味着错误会在多步之间复合（compounding errors），成本与延迟换取任务表现；因此要求 stopping conditions（如最大迭代数）与大量沙盒测试。https://www.anthropic.com/engineering/building-effective-agents

## 2. 业界对 Supervisor 的定位：四家框架 + supervisor vs 代码图

### 2.1 LangGraph：路由 = "选择下一个 worker 的结构化工具调用"
- 官方库 langgraph-supervisor-py 的核心机制：supervisor 节点不产出自由文本，而是通过 handoff 工具（transfer_to_xxx）选择下一个 worker——即**路由被建模为受限的工具调用，只能从预定义 worker 集合中选**。`create_supervisor([agents], model, prompt)` 返回 StateGraph，需 compile；`output_mode="full_history"|"last_message"` 控制 worker 消息写回方式；支持多级 supervisor 嵌套。https://github.com/langchain-ai/langgraph-supervisor-py
- 官方注记（重要信号）："We now recommend using the supervisor pattern directly via tools rather than this library for most use cases. The tool-calling approach gives you more control over context engineering." 迁移指南明确该库**不再积极维护**，迁移目标 = `create_agent` + 把每个 worker 包装成 `@tool`：`supervisor = create_agent(model, tools=[call_research_agent, call_math_agent], system_prompt="Route research questions to research_expert...")`。https://docs.langchain.com/oss/python/migrate/langgraph-supervisor
- 现役 LangChain 多智能体文档列出 5 模式：Subagents（主 agent 把子 agent 当工具）、Handoffs（由代码/状态触发转移）、Skills、Router（分类后路由）、**Custom workflow——"Build bespoke execution flows with LangGraph, mixing deterministic logic and agentic behavior. Embed other patterns as nodes in your workflow."**（混合模式官方句）。https://docs.langchain.com/oss/python/langchain/multi-agent
- LangGraph 教程解释为何需要 supervisor：单 agent 面对太多相似工具时决策变差（工具分区），而非"LLM 善于记住顺序"。https://docs.langchain.com/oss/python/langchain/multi-agent/subagents-personal-assistant

### 2.2 CrewAI：hierarchical process = manager LLM + 明确承认"仍按逻辑顺序"
- 默认 `Process.sequential`；显式 `Process.hierarchical` 才启用 manager（必须配 `manager_llm` 或自定义 `manager_agent`）。关键特性：Task Delegation（按角色能力分配）、Result Validation（manager 评估产出是否达标）、`planning=True`、Max Iterations、Max Requests Per Minute；且 **"Delegation is now disabled by default to give users explicit control"**。
- 最值得引用的一句："Despite being a hierarchical process, tasks follow a logical order for smooth progression, facilitated by the manager's oversight"——即便在 hierarchical 模式，CrewAI 也强调任务要按逻辑顺序推进。https://docs.crewai.com/edge/en/learn/hierarchical-process

### 2.3 AutoGen GroupChat / Magentic-One：orchestrator + 进度账本（ledger）
Magentic-One（微软，arXiv:2411.04468）的 Orchestrator 是"非确定性编排"的业界最强形态，其内部结构（MS Research 官方博文原文）：
- **双循环**："Magentic-One features an Orchestrator agent that implements two loops: an outer loop and an inner loop. The outer loop manages the task ledger (containing facts, guesses, and plan) and the inner loop manages the progress ledger (containing current progress, task assignment to agents)."
- **机制**："The Orchestrator begins by creating a plan to tackle the task, gathering needed facts and educated guesses in a Task Ledger... At each step of its plan, the Orchestrator creates a Progress Ledger where it self-reflects on task progress and checks whether the task is completed. If the task is not yet completed, it assigns one of Magentic-One other agents a subtask... If the Orchestrator finds that progress is not being made for enough steps, it can update the Task Ledger and create a new plan."（停滞检测 → 重规划）
- 论文自评："Orchestrator plans, tracks progress, and re-plans to recover from errors"，在 GAIA/AssistantBench/WebArena 与 SOTA 统计持平；模块化设计使增删 agent 无需 prompt 调优。
- 风险承认："as agentic systems grow more powerful, their risks—like taking undesirable actions or enabling malicious use-cases—can also increase"；配套 AutoGenBench："an agentic evaluation tool with built-in controls for repetition and isolation to rigorously test agentic benchmarks and tasks while minimizing undesirable side-effects"。
- URL：https://www.microsoft.com/en-us/research/articles/magentic-one-a-generalist-multi-agent-system-for-solving-complex-tasks/ ；https://arxiv.org/abs/2411.04468

### 2.4 Microsoft Agent Framework：把 5 种编排做成一等公民（4 种不含 LLM 编排）
内置编排模式：**Sequential（按定义顺序逐个执行）/ Concurrent（并行）/ Handoff（按上下文转移）/ Group Chat（共享会话）/ Magentic（manager agent 动态协调）**。Magentic 页给出约束旋钮：`MagenticWorkflowBuilder(managerAgent).AddParticipants([...]).WithMaxRounds(10).WithMaxStalls(3).WithMaxResets(2).RequirePlanSignoff(false)`——最大协调轮数、连续停滞上限（触发 replanning）、计划重置上限、人工签核计划；并适用声明"solution path is not known in advance"；官方 Note 承认"It is untested how well the Magentic orchestration will perform outside of the original Magentic-One design"。https://learn.microsoft.com/en-us/agent-framework/workflows/orchestrations/ 及 .../orchestrations/magentic

### 2.5 Supervisor 模式 vs 代码图模式（graph-in-code）
- **图模式**：LangGraph 的分支条件是普通 Python 函数（教程示例 `check_punchline` 检查 joke 里有没有 "?"/"!"，非 LLM），`workflow.add_conditional_edges("generate_joke", check_punchline, {"Fail": "improve_joke", "Pass": END})`；图在 `compile()` 前结构即固定，顺序是**代码事实**，可单测、可断言。文档立场句："Workflows have predetermined code paths and are designed to operate in a certain order. Agents are dynamic and define their own processes and tool usage." https://docs.langchain.com/oss/python/langgraph/workflows-agents
- **Supervisor 模式**：顺序是**模型每步选择的统计结果**，纵有 handoff 工具约束"只能去哪"，也约束不了"该不该现在去、会不会漏一个"。
- OpenAI 2025 指南对声明式图的批评（供平衡）："Some frameworks are declarative, requiring developers to explicitly define every branch, loop, and conditional in the workflow upfront through graphs... While beneficial for visual clarity, this approach can quickly become cumbersome and challenging as workflows grow more dynamic and complex... the Agents SDK adopts a more flexible, code-first approach."——注意这批评的是"声明式 DSL"而非"确定性控制流"；Agents SDK 本身仍是代码承载流程。
- 结论：两家之争是"DSL 图 vs 代码图"，不是"确定性 vs LLM 编排"。**确定性 vs LLM 编排之争里，没有一线框架站在"自然语言管顺序"一边。**

## 3. 具体缓解手段（重点）

### 3.1 强制结构化输出：把路由动作变成受约束选择
- 机制（OpenAI）：JSON Schema → CFG → 每步动态合法 token 集，输出 100% 合法（vs prompting <40%）。https://openai.com/index/introducing-structured-outputs-in-the-api/
- 应用到编排：路由节点输出 `{next: "worker_a" | "worker_b" | "FINISH"}` 这种枚举 schema，模型**物理上无法**发明流程外的步骤。LangGraph 的 handoff 工具、MS Agent Framework 的 manager 都是此形态。

### 3.2 Plan-and-execute：先把计划编译成显式结构，再由确定性执行器推进
LangGraph 官方教程（受 Plan-and-Solve, arXiv:2305.04091 启发）：
- "The core idea is to first come up with a multi-step plan, and then go through that plan one item at a time."
- 对 ReAct 的两大优势："1. Explicit long term planning (which even really strong LLMs can struggle with) 2. Ability to use smaller/weaker models for the execution step, only using larger/better models for the planning step."
- 实现：计划=结构化输出 `class Plan(BaseModel): steps: List[str]`（planner 用 temperature=0 + with_structured_output）；状态契约 `class PlanExecute(TypedDict)`；固定图 `START→planner→agent→replan→(should_end ? END : agent)`，`execute_step` 每次只取 `plan[0]` 执行（弹队首），replan 节点输出 `Union[Response, Plan]`（"Only add steps to the plan that still NEED to be done"）——**顺序推进是图代码做的，LLM 只产出/修订计划内容**。
- 教程自认局限并指出 DAG 方向："each task is still executed in sequence... You could improve on this by having each task represented as a DAG (similar to LLMCompiler)"。LLMCompiler = *An LLM Compiler for Parallel Function Calling*（arXiv:2312.04511）：LLM 一次性把任务编译成 DAG，由确定性调度器并行执行。
- 论文级形态 LLM+P（arXiv:2304.11477）：立场句 "so far, LLMs cannot reliably solve long-horizon planning problems"；机制 = LLM 只做 NL↔PDDL 翻译，规划交给经典规划器，输出**保证正确/最优**的 plan 再确定性执行。
- URL：https://github.com/langchain-ai/langgraph/blob/23961cff61a42b52525f3b20b4094d8d2fba1744/docs/docs/tutorials/plan-and-execute/plan-and-execute.ipynb ；https://arxiv.org/abs/2304.11477 ；https://arxiv.org/abs/2312.04511

### 3.3 限制编排者工具面：supervisor 只能 delegate，不能干活
- 平台痛点"管家自己把活干了"的物理解法：supervisor 的工具清单里**只有** delegate/handoff 工具，没有任何业务工具。LangChain 迁移示例：`supervisor = create_agent(model, tools=[call_research_agent, call_math_agent], ...)`——两个工具都只是委派。https://docs.langchain.com/oss/python/migrate/langgraph-supervisor
- OpenAI 指南同构：Manager pattern 中 manager 的 Orchestration 工具就是 agents themselves as tools。https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/
- Anthropic 的 ACI 原则同理（工具即接口，poka-yoke 防错）：如果不想让模型做 X，就不要把 X 做成它的工具。

### 3.4 节点 schema 化输入/输出契约
- LangGraph：`class State(TypedDict)` 类型化状态在节点间传递；plan-and-execute 用 `past_steps: Annotated[List[Tuple], operator.add]` 让"已做步骤"成为状态事实。
- Dify Agent 节点：Instruction（可 Jinja 引用上游变量）、Query、Output Variables（节点输出 schema）、Maximum Iterations。https://docs.dify.ai/en/cloud/use-dify/nodes/agent
- 价值：契约使"漏跑环节"在图上是**可观测的状态缺口**（字段没被写入），而不是"看起来完成了"。

### 3.5 Checker / evaluator 节点：验证流程是否走完
- Prompt chaining 模式中 gate 是纯代码（`check_punchline` 检查 "?"/"!"，非 LLM 判断）——Anthropic 明确建议在步骤间加 programmatic gate。
- Evaluator-optimizer 模式：生成节点 + 评审节点循环。
- CrewAI hierarchical：manager 有 Result Validation（评估产出是否达标）。
- MS Agent Framework Magentic：`RequirePlanSignoff(true)` 人工签核计划 + `WithMaxStalls(3)` 停滞检测触发重规划。
- Anthropic：agent 每步都需要 ground truth 反馈（用代码检查而非再开一个 LLM 问"你觉得对吗"）。

### 3.6 迭代/轮数上限（防失控兜底）
Dify Maximum Iterations；MS WithMaxRounds(10)；OpenAI 指南的单 agent 退出条件（final-output tool 被调 / 无工具调用直接回复 / 达最大轮数）；Anthropic stopping conditions；CrewAI Max Iterations。**所有框架都要求给 LLM 驱动循环设硬上限。**

## 4. Anthropic 与 OpenAI 的官方建议：何时固定 workflow，何时才自主 agent

**Anthropic《Building Effective Agents》**（2024-12，https://www.anthropic.com/engineering/building-effective-agents ）：
- 定义："Workflows are systems where LLMs and tools are orchestrated through predefined code paths. Agents are systems where LLMs dynamically direct their own processes and tool usage."
- 选型结论："workflows offer predictability and consistency for well-defined tasks, whereas agents are the better option when flexibility and model-driven decision-making are needed at scale. For many applications... optimizing single LLM calls with retrieval and in-context examples is usually enough."
- 关键限定：orchestrator-workers 模式的适用场景是 **"无法预知子任务"**（如复杂编码改动改哪些文件事先不知道）——即连"中央 LLM 编排"都被归类为 workflow 的一种，前提是子任务集合动态。
- 三原则：保持简单（复杂度按需增加）；透明性（显示 agent 的 planning steps）；精心设计 ACI。

**OpenAI《A practical guide to building agents》**（2025，https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/ ，PDF https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf ）：
- 该上 agent 的三场景：复杂决策（nuanced judgment/exceptions）、规则集难维护、大量非结构化数据。原文："Before committing... validate that your use case can meet these criteria clearly. **Otherwise, a deterministic solution may suffice.**"
- Manager pattern（agents as tools）"ideal for workflows where you only want one agent to control workflow execution"；何时拆多 agent："maximize a single agent's capabilities first"，先单 agent 后多 agent（"starting with a single agent and evolving to multi-agent systems only when needed"）。
- Guardrails 六类中 rules-based protections 明确包含"regex/黑名单等确定性措施"。

**共识**：可预测性需求 → 确定性 workflow；把 LLM 编排当成"按需支付的昂贵选项"，而不是默认架构。

## 5. 混合模式：确定性步骤编排 + 弹性节点（重点：Temporal）

低代码平台已集体收敛到同一形态——**画布连线 = 确定性控制流；LLM/Agent = 节点**：

| 平台 | 机制证据 |
|---|---|
| **Dify** | Agent Node 是 Chatflow/Workflow 画布内的节点："enables autonomous tool invocation... LLMs can dynamically select and execute tools at runtime"，内置 Function Calling / ReAct 策略，参数含 Instruction、Maximum Iterations、Output Variables。固定图负责步骤顺序，弹性被关在节点内。https://docs.dify.ai/en/cloud/use-dify/nodes/agent |
| **n8n** | "The AI Agent node lets you build an AI agent... Connect a chat model and one or more tools, and the agent decides which tools to call"——Agent 是 root node（chat model/tools 是其子节点），外部 workflow 由画布固定；曾提供 Conversational/OpenAI Functions/**Plan and Execute Agent**/ReAct/SQL 等多种 LLM 编排类型，**1.82.0 起全部废弃，收敛为单一 Tools Agent**——历史演变的直接证据：LLM 控制流程的类型被淘汰，留下"固定流程+工具型 agent 节点"。https://docs.n8n.io/integrations/builtin/cluster-nodes/root-nodes/n8n-nodes-langchain.agent/ |
| **Flowise Agentflow V2** | "The visual connections between nodes on the canvas **explicitly define the workflow's path and control sequence**"，"V2 architecture implements a comprehensive node-dependency and execution queue system that **precisely respects these defined pathways**"；supervisor 被降维成图内一种节点（"A Supervisor agent can formulate and delegate tasks to multiple Worker agents"）。https://docs.flowiseai.com/using-flowise/agentflowv2 |
| **LangFlow** | "Langflow's Agent component is critical for building agent flows"——Agent 是 flow 里的一个组件，流程由组件连线构成。https://docs.langflow.org/agents |
| **LangGraph** | Custom workflow："mixing deterministic logic and agentic behavior. Embed other patterns as nodes in your workflow." https://docs.langchain.com/oss/python/langchain/multi-agent |

**Temporal：确定性 workflow + 非确定性 activity 的彻底分离（最工业级的"确定性编排+弹性节点"）**
- **Workflow 代码必须确定性**（官方原因原文）："Because the Workflow is re-executed to rebuild its state: It has to make the same decisions when given the same history. It shouldn't depend on any values not recorded in the history which would be different between runs." 直接调 Date.now()/random/网络会在 replay 时走出不同路径，"the Workflow could take a different path and fail to match the recorded history"。https://docs.temporal.io/workflows
- **LLM 调用被官方归类为必须放 Activity 的外部非确定操作**："Activities handle everything that interacts with the outside world, like: API calls, Database queries, **LLM invocations**, File I/O"。官方教程章节标题即立场："Building Temporal Activities to execute non-deterministic agent code"——"it's safer to always call them in an Activity. When an Activity fails, it's automatically retried by default." https://learn.temporal.io/tutorials/ai/durable-ai-agent/
- **Event History**："When a Workflow calls an Activity, the Activity runs once, its result is recorded in the Event History. During replay, that result is reused, not recomputed."——崩溃后从事件历史重建状态，不重跑已执行的 LLM 调用（durable execution）。
- 对应平台痛点的映射：**顺序 = Workflow 代码**（可单测、可 replay 验证、崩溃可恢复）；**弹性 = Activity**（LLM 在节点内随便非确定，失败自动重试，绝不影响外层顺序）。这就是"顺序固定、个别节点有弹性"的教科书式架构。

## 6. 测试/评测：用轨迹断言守护编排正确性

LangChain 官方 Agent Evals（agentevals 包）：把 agent 执行轨迹（消息+工具调用序列）与手写 reference_trajectory 直接对比——**"执行轨迹 vs 预期 DAG"已有现成实现**，且四种模式与平台三个痛点精确对应：

| 模式 | 语义 | 对应平台痛点 |
|---|---|---|
| `strict` | "Exact match of message structure and tool calls **in the same order**"（用例原话："policy lookup before authorization"） | **顺序错** |
| `superset` | "Agent calls at least the reference tools (extras allowed)"——"Verifying minimum required actions are taken" | **漏环节** |
| `subset` | "Agent calls only tools from reference (no extras)"——"Ensuring agent doesn't exceed expected scope" | **自作主张多干/越权** |
| `unordered` | 同样工具调用、任意顺序 | 顺序无关的信息收集 |

- Trajectory match 是 deterministic、fast、cost-free 的回归测试；另一轴 LLM-as-judge 用于无严格预期的定性评估。
- 定义原文："evals measure how well your agent performs by assessing its execution trajectory... useful for catching regressions when you change prompts, tools, or models."
- Magentic-One 的 AutoGenBench 补充运行时评测纪律："built-in controls for repetition and isolation... minimizing undesirable side-effects"（agent 行为有副作用，评测需隔离+重复采样）。
- URL：https://docs.langchain.com/oss/python/langchain/test/evals ；https://github.com/langchain-ai/agentevals ；LangSmith trace 可视化与监控见 https://docs.langchain.com/langsmith/trace-with-langgraph

**结论：编排正确性不靠 prompt 遵从，靠轨迹断言的回归测试。** strict/superset/subset 恰好各治"乱序/漏跑/多跑"。

## 7. 核心三问总结论 + 对该平台的落地建议

**Q1：顺序固定、个别节点弹性，业界公认的约束手段？**
不是更好的 prompt，而是**架构分工**：外层控制流用确定性状态机/图（代码边、可枚举步骤、程序化 gate、可单测），LLM 关进节点内（受约束解码 + 受限工具面 + 迭代上限）。Anthropic 与 OpenAI 都把确定性 workflow 列为可预测场景的首选（"Otherwise, a deterministic solution may suffice"）；五家低代码平台全部实现了"画布连线定顺序 + Agent 节点装弹性"；Temporal 把同一思想做到极致（workflow 确定性代码 + LLM 全部在 activity + Event History 回放）。

**Q2：LLM 该关在节点里，还是可以当路由器？**
- 默认答案：**关在节点里**。需要"选路"的少数位置（分类、决定循环退出）可以放 LLM，但必须是**受约束路由器**：输出枚举 schema（{next ∈ 预定义节点集合}）或预定义 handoff 工具调用，禁止自由文本决策；并在其上叠 checker gate 与轮数上限。
- 判据：步骤序列能否在写代码时枚举？能 → LLM 无权决定顺序（最多做分支分类）；不能（真正运行时才知）→ 才允许 plan-and-execute / supervisor 式 LLM 逐步选路，且必须配 ledger/停滞检测/上限。

**Q3：supervisor 模式何时仍值得保留？**
- 任务分解不可预知（Anthropic 对 orchestrator-workers 的限定："hard to predict in advance"，如多文件代码改动）；需要运行时根据反馈 re-plan（Magentic-One 的 task ledger + progress ledger 双循环）；工具面过大必须分区（LangChain：相似工具过多使单 agent 决策退化）。
- 即便保留，也要加 Magentic 式约束：进度账本使状态显式化、停滞检测（WithMaxStalls）、轮数上限（WithMaxRounds）、计划人工签核（RequirePlanSignoff）、只 delegate 不干活的工具面。

**对该平台的具体建议（按优先级）**：
1. 把"步骤顺序"从 system prompt 中移出，固化为显式步骤列表/状态机（哪怕先是最简单的数组遍历 + 每步调对应子 agent）；顺序可枚举的环节一律不让 LLM 参与决策。
2. 管家 agent 改造为受约束路由器（如确需 LLM 判断）：输出 JSON schema 枚举 `{next_step}`；其工具面只剩 delegate 工具——物理上杜绝"自己把活干了"。
3. 弹性环节（内容生成、模糊判断）才放 ReAct 型 agent 节点，配 Maximum Iterations。
4. 每个节点带显式 IO 契约；流程末尾加纯代码 completeness checker（断言所有必经节点产物存在）。
5. 建轨迹回归评测：superset 防漏跑、subset 防越权多跑、strict 防乱序（agentevals 现成可用）；用 LangSmith trace 观测每次调度偏离。

---

## 附：核心引用 URL 清单
- Anthropic《Building Effective Agents》: https://www.anthropic.com/engineering/building-effective-agents
- OpenAI《A practical guide to building agents》: https://openai.com/business/guides-and-resources/a-practical-guide-to-building-ai-agents/ （PDF: https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf）
- OpenAI Structured Outputs: https://openai.com/index/introducing-structured-outputs-in-the-api/
- LangGraph supervisor 库: https://github.com/langchain-ai/langgraph-supervisor-py ；迁移指南: https://docs.langchain.com/oss/python/migrate/langgraph-supervisor ；多智能体概览: https://docs.langchain.com/oss/python/langchain/multi-agent ；workflows-agents: https://docs.langchain.com/oss/python/langgraph/workflows-agents
- LangGraph plan-and-execute 教程: https://github.com/langchain-ai/langgraph/blob/23961cff61a42b52525f3b20b4094d8d2fba1744/docs/docs/tutorials/plan-and-execute/plan-and-execute.ipynb
- CrewAI hierarchical process: https://docs.crewai.com/edge/en/learn/hierarchical-process
- MS Agent Framework 编排: https://learn.microsoft.com/en-us/agent-framework/workflows/orchestrations/ ；Magentic: https://learn.microsoft.com/en-us/agent-framework/workflows/orchestrations/magentic
- Magentic-One 博文: https://www.microsoft.com/en-us/research/articles/magentic-one-a-generalist-multi-agent-system-for-solving-complex-tasks/ ；论文: https://arxiv.org/abs/2411.04468
- Lost in the Middle: https://arxiv.org/abs/2307.03172 ；IFEval: https://arxiv.org/abs/2311.07911 ；LLM+P: https://arxiv.org/abs/2304.11477 ；Plan-and-Solve: https://arxiv.org/abs/2305.04091 ；LLMCompiler: https://arxiv.org/abs/2312.04511
- Temporal workflows（确定性）: https://docs.temporal.io/workflows ；Durable AI Agent 教程: https://learn.temporal.io/tutorials/ai/durable-ai-agent/
- Dify Agent 节点: https://docs.dify.ai/en/cloud/use-dify/nodes/agent ；n8n AI Agent: https://docs.n8n.io/integrations/builtin/cluster-nodes/root-nodes/n8n-nodes-langchain.agent/ ；Flowise Agentflow V2: https://docs.flowiseai.com/using-flowise/agentflowv2 ；LangFlow agents: https://docs.langflow.org/agents
- Agent Evals 轨迹评测: https://docs.langchain.com/oss/python/langchain/test/evals ；agentevals: https://github.com/langchain-ai/agentevals
