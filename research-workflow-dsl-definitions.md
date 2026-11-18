# 主流 LLM Agent / 工作流平台如何定义工作流 — 调研报告

> 调研目标：回答「工作流是用代码还是结构化数据定义的？结构化表示长什么样？LLM 的弹性被放在哪一层？」，支撑把 system prompt 编排改成结构化图数据驱动的架构决策。

## 0. 一句话总结

**所有成熟平台都收敛到同一个架构：确定性控制流在外（图/代码/DSL），弹性推理在节点内部（LLM 调用被封装成带类型化输入输出的节点）。** 区别只在外层控制流用什么表示：LangGraph/Airflow/Temporal/CrewAI 用 Python 代码；Dify/n8n/Coze 用可序列化的结构化图数据（YAML/JSON）+ UI 画布。Anthropic 官方明确建议「用代码做编排，LLM 只在节点内部」。

## 1. 总览对比表

| 平台 | 结构表示 | 路由决策层 | 防乱来机制 | UI 渲染 | 序列化 | 版本管理 | HITL/断点续跑 |
|---|---|---|---|---|---|---|---|
| LangGraph | Python 代码建图 | 条件边 router 函数（可内嵌 LLM） | 图拓扑钉死路径；节点=checkpoint/retry 边界 | Studio/Mermaid/PNG 导出 | get_graph() 可导出 JSON 图结构 | checkpointer + thread_id；代码本身走 git | interrupt() + Command(resume)；静态断点 |
| Dify | YAML DSL（ReactFlow 图序列化） | if-else/code 硬编码 + question-classifier LLM 分类 | 类别集合钉死在结构里，LLM 只选 ID | 官方画布即 UI | YAML 导入导出（.ifpkg/app.yaml） | Draft→Publish→版本历史→回滚 | Human Input 节点（表单+按钮+超时分支） |
| Coze | UI 画布（闭源序列化） | Condition 硬编码 + 意图识别 LLM 分类 | 同上；异常分支+重试配置 | 官方画布 | 无公开 DSL | 版本历史/回滚；子工作流引用钉版本 | Question 节点（对话式收集+选项按钮） |
| n8n | JSON（nodes+connections） | Switch/IF 节点硬编码；Agent 节点内 LLM 决策 | 外层图路由确定；agent 弹性限制在节点内 | 官方画布 | JSON 导入导出/.n8np 包 | 版本历史（24h~企业版） | 工具级人工审批（Approve/Deny 多渠道） |
| Airflow | Python（DAG as code） | @task.branch 返回 task_id | 纯确定性；trigger rules 控制执行条件 | UI 只读渲染 DAG 图 | DAG 可 reserialize | git + DAG 版本 | 无原生 HITL；任务重试+清除重跑 |
| Temporal | 代码即流程 | 代码逻辑 | 确定性重放：Event History 为唯一真相 | 无画布 | Event History 持久化 | 代码 git；历史不可变 | 无限暂停恢复（durable execution） |
| CrewAI Flows | Python 装饰器 | @router 返回 Literal 标签 | 标签集合在类型注解里 | flow.plot() 出图 | 无画布序列化 | git | @human_feedback（LLM 归类反馈） |
| OpenAI Agents SDK | Python（无图） | LLM 决定 handoff | guardrails+tripwire；input_type 校验 | 无 | 无 | git | 无原生（靠外部编排） |
| Agno | Python steps 列表 | Condition evaluator / Router selector（可 CEL） | evaluator 钉死分支集合 | 文档流程图，无编辑器 | 无画布序列化 | git+database 持久化 run | 无原生 |

## 2. LangGraph：代码建图，图可导出

**a) 结构表示**：Python 代码用 builder 模式建图。State（TypedDict/Pydantic）+ Nodes（普通函数）+ Edges（固定/条件）。Pregel 式 super-step 执行。

```python
from langgraph.graph import StateGraph, START, END
from typing import Annotated
from typing_extensions import TypedDict
from operator import add

class State(TypedDict):
    messages: Annotated[list, add]  # channel + reducer
    next: str

builder = StateGraph(State)
builder.add_node("agent", agent_node)
builder.add_node("tools", tool_node)
builder.add_edge(START, "agent")
builder.add_conditional_edges("agent", route_fn, {"continue": "tools", "end": END})
builder.add_edge("tools", "agent")
app = builder.compile(checkpointer=checkpointer)  # checkpointer 可插拔
```

**b) 路由决策**：`add_conditional_edges(src, routing_fn, {map})` 的 routing_fn 是普通 Python 函数——可以是硬编码 if/else，也可以内部调 LLM 再返回节点名。路由函数返回值被约束在 map 的键集合里（或者用 Send 做 map-reduce fan-out）。节点内也可以 `Command(goto="node")` 一步完成更新+跳转，`Command[Literal["a","b"]]` 类型注解让渲染器知道可能边。

**c) 防乱来**：① 拓扑（节点/边）在 compile 前完全静态确定，LLM 无论输出什么都只能在节点内部活动；② 条件边的路由函数返回值映射到预注册的节点集合，跳不出图；③ 节点是 checkpoint/retry 边界，重执行需幂等；④ 递归上限默认 10,007 super-steps 防死循环。

**d) 渲染/序列化/版本**：`app.get_graph().draw_mermaid()` 出 Mermaid 文本（条件边虚线）；`draw_mermaid_png()`/`draw_png()` 出图；get_graph() 返回可序列化的图结构对象。LangSmith Studio 用本地 server 把图渲染成交互式 UI，可设断点单步、检查任意点状态。版本管理走 git（图是代码）。

**e) HITL**：`interrupt(payload)` 在节点内任意位置抛暂停→checkpointer 保存完整状态→无限期等待；恢复时同 thread_id invoke 且输入 `Command(resume=value)`，value 成为 interrupt() 的返回值。注意：恢复时节点从头重跑，副作用须幂等或放 interrupt 之后。`interrupt(..., response_schema=Decision)` 让 Studio 渲染类型化表单。thread_id 即持久游标。静态断点 `compile(interrupt_before=["a"])` 官方明说仅调试用。

参考：
- https://docs.langchain.com/oss/python/langgraph/graph-api
- https://docs.langchain.com/oss/python/langgraph/interrupts
- https://docs.langchain.com/oss/python/langgraph/visualize-graphs

## 3. Dify：结构化图数据驱动的最成熟范例

**a) 结构表示**：YAML DSL，graph 是 ReactFlow 序列化形状。顶层 `version/kind/app/dependencies/workflow`：

```yaml
version: "0.7.0"
kind: app
app:
  name: 我的翻译工作流
  mode: workflow          # workflow | advanced-chat | chat | completion | agent-chat | agent
dependencies:
  - type: marketplace
    value:
      marketplace_plugin_unique_identifier: langgenius/tongyi:0.1.36@sha256...
workflow:
  conversation_variables: []   # advanced-chat 的多轮记忆
  environment_variables: []     # env.XXX 常量
  features: {file_upload: ..., opening_statement: ..., ...}
  graph:
    nodes:
      - data:
          title: "开始"
          type: start
          selected: false
        id: "1770000000000"        # 字符串 ID
        position: {x: 50, y: 300}
        type: custom
        sourcePosition: right
        targetPosition: left
        width: 243
        height: 89
      - data:
          title: "LLM 翻译"
          type: llm
          model: {provider: langgenius/tongyi/tongyi, name: qwen-max, ...}
          prompt_template:
            - role: system
              text: "请翻译以下内容：{{#start.text#}}"   # 变量插值引用上游节点
        id: "1770000000001"
        position: {x: 350, y: 300}
        type: custom
      - data:
          title: "意图分类"
          type: question-classifier
          classes:
            - id: "1"      # 每类=一条输出边
              name: "技术问题"
            - id: "2"
              name: "其他"
        id: "1770000000002"
        ...
    edges:
      - data:
          sourceType: start       # 端点类型同步
          targetType: llm
          isInLoop: false
          isInIteration: false
        id: "1770000000000-source-1770000000001-target"
        source: "1770000000000"
        sourceHandle: source       # if-else 分支边 handle 是 case ID("true"/"false")
        target: "1770000000001"
        targetHandle: target
        type: custom
    viewport: {x: 0, y: 0, zoom: 0.8}
```

关键约定：**分支的可达路径编码在 edge 的 sourceHandle 里**（if-else 用 case ID、question-classifier 用 class ID、human-input 用 action ID），即「可选分支集合」是图数据的显式字段。

**节点类型全集**（1.16）：start/end/answer/llm/knowledge-retrieval/question-classifier/if-else/code/template-transform/http-request/variable-assigner/variable-aggregator/tool/parameter-extractor/iteration/document-extractor/list-operator/loop/agent/human-input/trigger-schedule/trigger-webhook 等 28 种。

**workflow vs chatflow**：同一画布+节点系统，区别在会话层——workflow 一次跑完（起点 Start/Trigger，终点 End/Output），chatflow 每条消息触发（终点必须是 Answer，支持 conversation_variables 记忆）。官方明说理由：单模型不可预测（幻觉/漏步/不一致），生产需把 AI 嵌入结构化可重复流程。

**b) 路由决策**：两层。if-else/code 节点=确定性硬编码条件（引用上游变量做比较）；question-classifier=LLM 语义分类路由——但类别集合（class ID+描述）钉死在节点配置里，LLM 只能选已有 ID，选不出新分支。

**c) 防乱来**：结构钉死一切可达路径；LLM 只在 llm/agent/question-classifier 等节点内部活动；变量通过 `{{#node_id.field#}}` 显式插值传递，无隐式上下文流。

**d) 渲染/序列化/版本**：官方画布即编辑器（ReactFlow）；YAML 全量导入导出（新版 .ifpkg=ZIP 内含 app.yaml，凭据默认滤除）；版本管理 Current Draft→Publish→Latest Version→历史版本可回滚→可导出指定版本。

**e) HITL**：Human Input 节点——表单（Markdown+变量引用+字段收集）+决策按钮（`__action_id`/`__action_value` 下游路由）+超时分支（默认 3 天），投递到 Web app 或 Email；响应后表单内容作 `__rendered_content` 变量。

**真实世界语料**（262 个公开 DSL 统计）：节点数中位数 15；类型频次 code 744/answer 561/if-else 507/llm 202/http-request 133——**条件分支和代码节点比 LLM 节点用得还多**，说明生产实践中确定性编排占主体，LLM 节点是嵌入点。

参考：
- https://docs.dify.ai/ （Workflow/Chatflow、版本控制、节点文档）
- https://github.com/langgenius/dify （api/services/app_dsl_service.py 导入导出源码）
- https://github.com/yzmw123/dify-workflow-dsl-skill （references/dsl-structure.md、real-world-yml-study.md，溯源官方源码+262 语料统计）

## 4. Coze（扣子）：闭源画布，同样的两层路由

**a) 结构表示**：UI 画布拖拽（Modern.js 前端），无公开 DSL。节点分 9 类：Basic（Start/End、LLM、Plugin、Workflow 子流程）、Logic（Variable、Code、Condition、Intent Recognition、Loop、Variable Merge、Batch）、Input&Output（Output、Input）、Database（SQL/增删改查）、Data（Variable Assign、知识库读写）、Image、Utilities（HTTP、Question、Text processing）、Conversation Management、Message Management。

**b) 路由决策**：Condition 节点=if-else 分支（多条件 AND/OR 组合，分支优先级拖拽排序）；Intent Recognition 节点=LLM 意图分类（Speed/Full 两模式，Full 最多 50 个意图+可加 system prompt 和每类输入示例；输出 classificationId（1..50，0=未命中走 fallback）+reason）。每个意图类别需要连接对应下游节点，未命中类别建议设 fallback 分支。

**c) 防乱来**：意图集合钉死在节点配置；LLM 只输出 classificationId，可选路径=已画出的边。节点级异常处理三选一：中断流程/返回预设内容（isSuccess+errorBody）/走异常分支（可配超时 60s 内、重试一次、异常分支子工作流）。

**d) 渲染/序列化/版本**：画布即 UI；无公开导出格式；资源库工作流有版本历史（submit/publish 两类记录、可回滚 Load to draft、不可编辑历史版本）；**子工作流引用钉具体版本**——App 里的 Workflow 节点绑定某版本后不自动升级，升级须手动且全量（生产隔离设计）。

**e) HITL**：Question 节点（对话式）——Reply directly（自然语言回答+字段提取，缺字段会追问）或 Reply based on options（选项按钮，每选项接不同下游分支，建议配 fallback）；输出 USER_RESPONSE 变量。仅同步模式可用。

**Loop 节点**（体现子图嵌套）：三种模式（数组遍历/指定次数/无限循环+break 节点），生成独立 loop body 子画布（内部顺序执行，无需 start/end 节点），内置变量 item/index，Set Variable/Stop Loop 只能用在循环体内。

参考：
- https://www.coze.com/open/docs/guides/workflow （overview）
- https://www.coze.com/open/docs/guides/condition_node
- https://www.coze.com/open/docs/guides/intent_recognition_node
- https://www.coze.com/open/docs/guides/loop_node
- https://www.coze.com/open/docs/guides/question_node
- https://www.coze.com/open/docs/guides/workflow_version

## 5. n8n：JSON 图 + 弹性推理嵌在节点内

**a) 结构表示**：workflow JSON = `nodes` 数组 + `connections` 对象（以源节点 name 为键）：

```json
{
  "name": "Demo workflow",
  "nodes": [
    {"parameters": {}, "id": "74003dcd-...", "name": "Chat Trigger",
     "type": "@n8n/n8n-nodes-langchain.chatTrigger",
     "typeVersion": 1, "position": [660, 340]},
    {"parameters": {}, "id": "ce8c3da4-...", "name": "Basic LLM Chain",
     "type": "@n8n/n8n-nodes-langchain.chainLlm", "typeVersion": 1.7,
     "position": [880, 340]}
  ],
  "connections": {
    "Chat Trigger": {
      "main": [[{"node": "Basic LLM Chain", "type": "main", "index": 0}]]
    },
    "Ollama Chat Model": {
      "ai_languageModel": [[{"node": "Basic LLM Chain", "type": "ai_languageModel", "index": 0}]]
    }
  },
  "settings": {"executionOrder": "v1"}
}
```

连接数组层级：`源节点名[连接类型][输出端口][输出索引]{node, type, index}`。**两种连接槽位泾渭分明**：`main` 是外层确定性数据流；`ai_languageModel`/`ai_tool`/`ai_memory` 等专用槽把子节点挂到 root 节点上（cluster nodes），配置的是节点内部能力而非图拓扑。

**b) 路由决策**：外层=Switch/IF 节点硬编码条件（官方 agents-vs-chains 示例：Chat Trigger→Switch 按 query 里 "agent"/"chain" 关键词分流→分别到 AI Agent 节点或 Basic LLM Chain 节点）。Agent 节点内部=LLM 自主决定调用哪些工具（LangChain Tools Agent 语义，n8n 1.82.0 起统一为 Tools Agent），agent 在节点内会多轮跑（setup→调工具→评估→答复）。

**c) 防乱来**：图拓扑和连线确定性；agent 的弹性被限制在节点边界内（工具集合=挂上来的 ai_tool 子节点）；节点天然 items-in/items-out 循环；执行顺序 v1：分支整支跑完再下一支，按画布位置排序，可预测。

**d) 渲染/序列化/版本**：官方画布即编辑器；JSON 全量导入导出（Download/Import from URL/File/复制粘贴）；.n8np CLI 包（含 folders/依赖）；版本历史保存/恢复（普通 24h，Cloud Pro 5 天，全历史企业版）；executions 运行记录可拷回编辑器 debug。

**e) HITL**：工具级人工审批——workflow 暂停→审批请求发 Slack/Email/Teams 等 9 渠道→人类看工具名+参数 Approve（按 AI 输入执行）或 Deny（AI 收到拒绝信息）；审批渠道可与主交互渠道不同。

参考：
- https://docs.n8n.io/build/understand-workflows/workflow-components/
- https://docs.n8n.io/build/integrate-ai/understand-ai-components/agents-vs-chains
- https://docs.n8n.io/build/integrate-ai/human-in-the-loop-for-tools
- https://docs.n8n.io/build/flow-logic/understand-execution-order/
- https://github.com/n8n-io/self-hosted-ai-starter-kit （真实 workflow JSON 样本）

## 6. 传统引擎对照：Airflow（DAG as code）与 Temporal（durable execution）

### Airflow
**a)** Python 代码：`with DAG(dag_id=..., schedule="@daily")` 或 `@dag` 装饰器；依赖 `first >> [second, third]`/`chain()`/`cross_downstream()`；从 Python 文件加载（只取顶层 DAG 对象）。
**b)** 分支：`@task.branch` 函数返回下游 task_id（或 None 跳过全部），纯确定性代码；trigger rules（all_success/one_failed/none_failed 等 14 种）控制任务在什么上游状态下运行。
**c)** 全确定性，无 LLM 概念——是「把 LLM 当 API 调用塞进 Task」的最朴素模式。
**d)** UI 只读渲染 DAG 图；DAG 可 reserialize；版本走 git。
**e)** 无 HITL；失败任务可 clear 重跑；`depends_on_past`。
```python
@task.branch(task_id="branch_task")
def branch_func(ti=None):
    xcom_value = int(ti.xcom_pull(task_ids="start_task"))
    if xcom_value >= 5: return "continue_task"
    elif xcom_value >= 3: return "stop_task"
    else: return None
start_op >> branch_op >> [continue_op, stop_op]
```

### Temporal
**a)** 代码即流程：Workflow Definition 是普通 Go/Java/TS/Python 代码，Workflow Type 是注册名，Workflow Execution 是运行实例。
**b)** 路由=代码逻辑本身。
**c) 确定性重放（核心机制）**：Event History 是一切已发生事情的有序日志（source of truth）。恢复时不恢复内存快照，而是**从头重跑 Workflow 代码+重放 Event History 引导回原状态**——所以 Workflow 代码必须确定性（同 history 同决策）：禁 Date.now()/随机数/直接网络调用（有 replay-safe API 替代）；一切副作用（API 调用/数据库/LLM 调用/文件 IO）放 Activity，Activity 结果记录进 history，重放时复用不重算。
**d)** 无画布；Event History 持久化；版本走 git（+PatchVersion API 处理在途执行）。
**e)** 可运行数年、崩溃自动恢复——「断点续跑」是执行模型本身；Signal/Update 支持外部交互。
**对 LLM 平台的意义**：Temporal 文档明确把 "LLM invocations" 列为 Activity 的典型用途——长耗时、不可靠的 LLM 调用适合 Activity 化，确定性流程代码负责编排。LangGraph 的 checkpointer+重放 与 Temporal 的 Event History+重放是同构思想。

参考：
- https://airflow.apache.org/docs/apache-airflow/stable/core-concepts/dags.html
- https://docs.temporal.io/workflows

## 7. 代码定义派（CrewAI Flows / OpenAI Agents SDK / Agno）

### CrewAI Flows：事件驱动装饰器
`@start()` 入口、`@listen(方法或标签)`、`@router` 条件路由（返回 `Literal["success","failed"]`，标签集合在类型注解里）、`@and_`/`@or_` 多事件汇合。State 可用 Pydantic 结构化。`flow.plot()` 可自动出图（依赖 router 标签）。`@human_feedback(message=..., emit=["approved","rejected","needs_revision"], llm=...)` 暂停收集反馈、LLM 归类到预定义 outcome。持久化支持 Fork 快照。
```python
class MyFlow(Flow[MyState]):
    @start()
    def generate_content(self): ...
    @router(generate_content)
    def check(self) -> Literal["success", "failed"]:
        return "success" if self.state.success_flag else "failed"
    @listen("success")
    def publish(self): ...
```
参考：https://docs.crewai.com/concepts/flows

### OpenAI Agents SDK：无图，交接链
核心=Agent（LLM+instructions+tools+handoffs+guardrails，内置 loop 跑到最终输出）。**Handoffs 呈现为 LLM 的工具**（`transfer_to_<agent_name>`），路由决策完全在模型；`input_type=PydanticModel` 让交接时模型必须提供符合 schema 的元数据（本地校验）；input/output/tool 三层 guardrails，tripwire_triggered=True 抛异常拦截。没有图结构、没有序列化、没有 UI——适合简单交接链，复杂编排需外层框架（或官方推荐直接用代码）。
参考：https://openai.github.io/openai-agents-python/

### Agno Workflows：Python steps + 控制容器
`Workflow(steps=[...])`；Step 恰好包一个 executor（Agent/Team/Function/Workflow 嵌套）；容器 Condition（evaluator=Python 函数/布尔/CEL 表达式字符串，steps/else_steps）/ Router（selector 返回 step 名或列表，choices 钉死候选）/ Parallel / Loop（end_condition+max_iterations+forward_iteration_output）。定位：控制流可重复，agent 输出可变；配 database 持久化 run。
参考：https://docs.agno.com/workflows/overview

## 8. Anthropic《Building Effective Agents》：分类学基础

**Workflow vs Agent 的定义**（原文）："Workflows are systems where LLMs and tools are orchestrated through **predefined code paths**. Agents are systems where LLMs **dynamically direct their own processes** and tool usage, maintaining control over how they accomplish tasks."

五种 workflow 模式：
1. **Prompt chaining**：任务拆成固定串，中间加 programmatic checks/gate（如 Moderation→Generate→Check→Revise）。
2. **Routing**：先分类输入再转专门下游（分类器可以是 LLM 或传统模型）——即 Dify question-classifier / Coze intent recognition / n8n Switch 的理论原型。
3. **Parallelization**：sectioning（拆独立子任务并行）+ voting（同任务多次跑聚合）。
4. **Orchestrator-workers**：中央 LLM 动态拆任务、委派 worker、再合成——子任务不预定义（这是 agent 味最重的 workflow）。
5. **Evaluator-optimizer**：生成+评审循环。

Agents = "LLMs using tools based on environmental feedback in a loop"，可设检查点或遇阻塞暂停找人类，终止=完成或最大迭代数。

**核心建议**：从最简方案起步；可预测性要求高用 workflow，灵活性要求高才上 agent；生产中「减少抽象层、用基本组件构建」；三原则=保持简单、显式展示 agent 的规划步骤（透明）、精心打磨 agent-computer interface（工具文档+测试）。

参考：https://www.anthropic.com/research/building-effective-agents

## 9. 架构决策支撑：把 system prompt 编排改成结构化图数据驱动

### 9.1 行业共识
1. **确定性外壳 + 弹性内核**是唯一被所有平台验证过的生产架构：Dify/n8n/Coze 三个商业平台全是「图数据定义控制流，LLM 封装成节点」；LangGraph 虽用代码建图但拓扑静态、路由函数约束返回集合；Anthropic 把它写成显式建议。
2. **路由决策分两层**且两层都该有：硬编码层（if-else/Switch/Condition，处理已知模式）+ LLM 分类层（question-classifier/intent recognition，处理语义未知但类别已知的模式）。后者把 LLM 的输出空间压缩为「从 N 个已注册分支里选一个 ID」，这是防乱来的关键技巧——所有平台的 LLM 路由节点都这么设计（classificationId、class ID、Literal 标签）。
3. **结构化图数据（而非代码）是平台化/产品化的门槛**：Dify YAML、n8n JSON 证明了图数据可序列化存储、可 UI 渲染、可版本管理、可导入导出、可静态校验（「workflow 必须有 end 节点」「每个 intent 类别必须连下游」这类约束可以在保存时校验，而不是运行时炸掉）。代码建图（LangGraph/Airflow/CrewAI）获得表达力但失去画布编辑、非工程师协作、运行时校验。
4. **分支编码在边数据里**：Dify 用 sourceHandle=case/class/action ID 表达「哪个分支从哪条边出去」，n8n 用 connections 的输出端口数组——两者都把可达路径集合做成图数据的显式字段，渲染器、校验器、执行器共用同一份真源。
5. **HITL 是图的一等公民**：LangGraph interrupt+checkpointer（状态快照+恢复游标）、Dify Human Input 节点（表单+按钮 ID+超时分支）、n8n 工具审批、Coze Question 节点——共同模式是「暂停点=图节点，恢复=结构化响应路由到预定义分支」。
6. **断点续跑=持久化执行状态**：Temporal Event History 重放 / LangGraph checkpoint 线程 / Dify 版本快照，本质都是「把执行状态做成可恢复数据」。
7. **子图嵌套与封装复用**：Dify workflow 工具（语料里 69 个 workflow-provider vs 21 个 builtin）、Coze Workflow 节点（钉版本引用）、Agno Workflow 嵌套 Step——子工作流=子图，是控制复杂度的通用手段。
8. **版本管理三态**：Draft（编辑态）→ Published（运行态）→ History（回滚），且**引用钉版本**（Coze 子工作流不自动升级）是生产隔离的正确做法。

### 9.2 对「prompt 编排 → 图数据驱动」的具体建议
- **Schema 参照**：nodes[]（id/type/title/position/参数化 config）+ edges[]（source/target/sourceHandle 表分支）+ 顶层 metadata（version/mode/features）——Dify 的结构最完整，可直接借鉴（它是 ReactFlow 序列化，自家 UI 生态现成）。
- **LLM 节点参数化**：model provider、prompt template（带变量插值占位符）、输入输出 schema（类型化），对应 Dify llm 节点 + LangGraph interrupt response_schema 的思路。
- **路由节点模式**：`router: {type: "llm-classifier", classes: [{id, title, description}], fallback: "default_branch"}` + 每个分支 ID 对应一条出边——把「类别集合」与「边的 sourceHandle」绑定校验。
- **执行语义**：super-step/分步执行（LangGraph）或事件驱动（CrewAI）二选一；节点=重试/checkpoint 边界；幂等要求写进规范。
- **校验器**：图保存时校验结构约束（终点必达、分支全连接、变量引用存在、类型匹配），这是图数据相对 prompt 编排的最大增量收益——prompt 无法静态校验，图可以。
- **弹性边界**：需要 LLM 自由发挥的环节封装成 agent 型节点（内部 loop+工具），控制流永远不让 LLM 碰——这既是 Anthropic 的建议，也是 Dify（agent 节点嵌入 workflow 图）和 n8n（AI Agent cluster 节点嵌 main 流）的实际做法。

### 9.3 方法论备注
本次调研基于各平台官方文档全文（LangGraph docs、docs.dify.ai、docs.n8n.io、www.coze.com/open/docs、Airflow 3.4 docs、docs.temporal.io、docs.crewai.com、openai.github.io/openai-agents-python、docs.agno.com、anthropic.com）+ Dify 源码（app_dsl_service.py）+ 262 个真实 Dify DSL 语料统计。未使用二手综述，所有 schema 片段均溯源至官方文档或真实导出文件。
