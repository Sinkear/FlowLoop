# 蓝图（blueprint）创建

用户要把一套流程存成可复用的 SOP、多步流水线或带分支/审批的流程时，才建蓝图。单个任务直接用 `create goal`。

## 先分清三个名字

- `--name`：蓝图名，Web 蓝图列表里显示。
- `--description`：蓝图说明。不传时服务端取 content 里的 `graph.description`。
- content 里的 `name` / `description`：spec 图自身的名字与说明。

三处是不同的字段，不要互相替代。

## spec graph 结构

```json
{"name": "图名", "description": "图说明", "nodes": [], "edges": []}
```

- `nodes` 每条：`{id, type, title, <type body>}`，type 只能是 `task|condition|router|gate|spawn|end`。
- `edges` 每条：`{source, source_handle, target}`。
- 最多 64 个节点。

### 节点必填项与声明端口

| type | 必填 body | 声明端口（source_handle） |
|---|---|---|
| task | `task.assignee`、`task.brief`；`verify.mode=agent` 时还要 `verify.verifier` | `done`；可选 `failed` |
| condition | `condition.expr`、`condition.cases[]`（≥1，每项 `id`+`test`） | 每个 `cases[].id` + `else` |
| router | `router.classes[]`（≥2，每项 `id`+`title`）、`router.fallback` | 每个 `classes[].id` + `fallback` |
| gate | `gate.prompt`、`gate.actions[]`（≥1，每项 `id`+`label`）、`gate.timeout{after,action}` | 每个 `actions[].id` + `timeout` |
| spawn | `spawn.assignee`、`spawn.brief`；`child_type` 只能 `task`；`max_children` 1–8 | 无强制；`done`/`failed` 可选 |
| end | `end.outcome` 只能 `success`/`failure`（可空，默认 success） | 无 |

condition 表达式是受限语法，不接脚本：

```text
goal.<field> == | != | contains | starts_with | ends_with "<literal>"
goal.<field> exists | empty
```

### 边

- `source` / `target` 必须指向存在的节点；`end` 不能有出边。
- `source_handle` 必填，且必须是源节点声明的端口：task 的 `done`/`failed`，condition 的 case id 或 `else`，router 的 class id 或 `fallback`，gate 的 action id 或 `timeout`。
- 同一个 `source + source_handle` 只能有一条出边。
- 每个强制端口都必须有出边。

## V1–V5 校验易错点

**V1 形状**

- `graph.name` 非空；`nodes`、`edges` 都非空；至少一个 `end` 节点。
- 节点 id 匹配 `^[a-z][a-z0-9_]{0,63}$`：小写字母开头，不能 `T1`、不能 `end-ok`；id 不可重复。
- 每个节点 `title` 必填，`type` 只能是六种之一。
- `task.assignee` 必须匹配 `^(agent|squad|capability):[a-z0-9_.-]+$`。裸名字不合法；`agent:` 后面要接真实 32 位 id。
- `task.brief` 非空；`verify.mode` 只能是 `domain_default|machine|agent`。

**V2 连通**

- 不能有环。
- 必须有根节点（无入边），且根只能是 task 或 spawn。
- 每个节点都要能从根到达，也要能走到某个 end。

**V3 端口**

- 强制端口缺出边直接报错；port id 也走节点 id 正则。
- `source_handle` 写了未声明的端口会报错。
- router 少于 2 个 class 不算决策，直接报错；`fallback` 必须是已声明的 class id。
- gate 缺 `actions` 或缺 `timeout` 直接报错；`timeout.after` 要能被 Go `time.ParseDuration` 解析，写 `24h`、`30m`，不要写 `1d`。
- gate 的 `timeout` 边必须和 `timeout.action` 落到同一个目标节点，不一致会报 diverges。

**V4 指派可解析**

- `agent:<id>` / `squad:<id>` 必须真实存在（创建时就查库）。
- `capability:<slug>` 只校验形状：`^[a-z][a-z0-9_-]*$`，且不能是平台保留的 `reviewer`；真正解析发生在蓝图绑定到具体任务之后，按该任务的 squad 角色匹配。
- 模板里的 `agent:__AGENT_ID__` 如果不传 `--agent`，就解析不到真实 agent，创建会失败。

**V5 模板变量**

- `{{goal.<field>}}` 只允许这些字段：`id`、`title`、`description`、`source_ref`、`category`、`domain_id`、`connection_id`、`execution_mode`、`created_by_type`。
- `{{node.<id>.output}}` 只能指向存在且 `type=task` 的节点；condition / end 不产出 output。
- gate.prompt 也参与模板检查；router 的 class 描述不参与。

## 最小可过校验的串行示例

取自平台「串行两步」模板（Web 蓝本页新建时的默认示例），占位符写作 `agent:__AGENT_ID__`：

```json
{
  "name": "串行两步",
  "description": "先调研，再基于调研结论写方案——上一步没通过验证，下一步不会开始",
  "nodes": [
    {"id": "t1", "type": "task", "title": "第一步·调研",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "围绕目标「{{goal.title}}」做调研，输出结论和关键事实"}},
    {"id": "t2", "type": "task", "title": "第二步·方案",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "基于上游结论：{{node.t1.output}}\n写出可执行的方案"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "t2"},
    {"source": "t2", "source_handle": "done", "target": "end_ok"}
  ]
}
```

存成 `serial.json` 后创建：

```bash
agentwork create blueprint --name "串行两步" --description "先调研再写方案" --content-file serial.json --agent "<agent-id>"
```

- `--agent` 会把 content 里所有 `__AGENT_ID__` 替换成裸 agent id，于是 `agent:__AGENT_ID__` 变成 `agent:<id>`，通过 V4。
- `--content` 与 `--content-file` 二选一；内容短可以直接 `--content '<JSON>'`，长内容建议写文件，避免 shell 引号出错。
- 不传 `--content` / `--content-file` 不会在本地报错，平台回 `need_fields`，`missing` 是 `名称` 或 `蓝图内容`。

创建成功后在输出里读 `entity.id`：

```json
{"status": "created", "entity": {"id": "<blueprint-id>", "name": "串行两步"}}
```

`message` 会说明这是 v1 草稿：「✅ 已创建蓝图：串行两步（…，v1 草稿）。发布之后才能在新建任务时选它；发布前还可以继续改稿。」把这句话和 `/blueprints/<id>` 一起给用户。

## 创建之后：草稿 → 发布 → 绑定

- `create blueprint` 只生成 **version=1 的草稿**，不会自动发布，也不会创建任务。
- 默认发布路径：打开 Web `/blueprints/<id>`，由人点「发布此版本」。发布后内容不可改；要改就开新草稿。
- 用户明确说「发布」时，可以代跑 `agentwork blueprint publish "<名字|id>"`。它把该蓝图的 draft 版本发布（不用记 version id）；没确认不要跑。
- 查已有蓝图：`agentwork blueprint list`。
- 绑定：只有已发布版本才有「用此版本新建任务」，跳到 `/goals/new?blueprint=<versionId>`。草稿不能绑定任务。
- 你的职责到草稿（或用户确认后的发布）为止：回报 `entity.id` 与 `/blueprints/<id>`，不代点 Web、不代绑任务。

## 常见失败

| 报错关键词 | 原因 | 处理 |
|---|---|---|
| `graph.edges must not be empty` | 只写了节点 | 补边，至少串起来 |
| `must contain at least one end node` | 没有 end | 加 `{"id":"end_ok","type":"end",...}` |
| `id must match` | id 大写或带 `-` | 改成 `t1`、`end_ok` 这类 |
| `assignee must be` | 写了裸名字 | 换 `agent:<真实 id>` 或传 `--agent` |
| `is not a declared port` | source_handle 写错 | 按节点声明的端口写 |
| `mandatory port … has no outbound edge` | 有端口没接边 | 补边或删掉多余分支 |
| `has no inbound edge` / `unreachable` | 图断开 | 检查根与边的方向 |
| `has a cycle` | 边绕回上游 | 拆环 |
| `brief refers to unknown goal field` | `{{goal.x}}` 不在白名单 | 换白名单字段 |
| `capability slug` | slug 含大写或用了 `reviewer` | 换合法 role slug |