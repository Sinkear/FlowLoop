// 蓝本模板（Phase A 起步，Phase B 扩充）：新蓝图页的起步预设。
// 每份 content 都是能直接通过 V1–V6 校验的完整规范 JSON——用户点一下
// 就有一个能保存、能发布、能绑定任务的起点，而不是面对一片空白 JSON。
//
// 约定：占位符 __AGENT_ID__ 会在提交时被表单里选中的智能体 id 替换
// （agent:<32位id>）；{{goal.title}} / {{node.<id>.output}} 由平台在
// 编译/释放时解析（拍1/拍2）。字段必须走 goal 白名单（V5）。
// Phase B 语义（§11）：
//   - capability:<role-slug> 把节点派给「任务自己的小队」里角色匹配的
//     成员（谁空闲给谁；reviewer 永不执行；角色词来自小队成员的 role）；
//   - spawn 节点 = 拆解扇出：规划者完成时输出子项清单（JSON 契约），
//     平台把它物化成真实子任务，全部子项完成后下游才释放。

export interface BlueprintTemplate {
  key: string;
  title: string;
  desc: string;
  name: string;
  description: string;
  content: string;
}

const SERIAL = `{
  "name": "串行两步",
  "description": "先调研，再基于调研结论写方案——上一步没通过验证，下一步不会开始",
  "nodes": [
    {"id": "t1", "type": "task", "title": "第一步·调研",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "围绕目标「{{goal.title}}」做调研，输出结论和关键事实"}},
    {"id": "t2", "type": "task", "title": "第二步·方案",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "基于上游结论：{{node.t1.output}}\\n写出可执行的方案"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "t2"},
    {"source": "t2", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const PARALLEL = `{
  "name": "并行分头+汇合",
  "description": "两路互不依赖的任务同时开跑，全部通过后由第三个节点汇合",
  "nodes": [
    {"id": "t1", "type": "task", "title": "路线A",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "围绕目标「{{goal.title}}」完成路线A的工作"}},
    {"id": "t2", "type": "task", "title": "路线B",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "围绕目标「{{goal.title}}」完成路线B的工作（与路线A互不依赖）"}},
    {"id": "t3", "type": "task", "title": "汇合·整合",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "整合两路产出：\\n路线A：{{node.t1.output}}\\n路线B：{{node.t2.output}}"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "t1", "source_handle": "done", "target": "t3"},
    {"source": "t2", "source_handle": "done", "target": "t3"},
    {"source": "t3", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const CONDITIONAL = `{
  "name": "按类别分流",
  "description": "激活时按任务类别走不同路径：hitl 走人工路径，其余走自动路径——条件只求值一次",
  "nodes": [
    {"id": "c1", "type": "condition", "title": "类别判断",
     "condition": {"expr": "goal.category == \\"hitl\\"", "cases": [{"id": "human", "test": "goal.category == \\"hitl\\""}], "else": "auto"}},
    {"id": "t_human", "type": "task", "title": "人工路径",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "此任务类别需要人工检查点：{{goal.title}}"}},
    {"id": "t_auto", "type": "task", "title": "自动路径",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "直接执行：{{goal.title}}"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "c1", "source_handle": "human", "target": "t_human"},
    {"source": "c1", "source_handle": "auto", "target": "t_auto"},
    {"source": "t_human", "source_handle": "done", "target": "end_ok"},
    {"source": "t_auto", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const SPAWN = `{
  "name": "拆解扇出",
  "description": "规划者先把任务拆成若干子项（上限4项），平台把子项物化成真实子任务并行执行，全部通过后整合收尾",
  "nodes": [
    {"id": "n_plan", "type": "spawn", "title": "拆解规划",
     "spawn": {"assignee": "agent:__AGENT_ID__", "brief": "把目标「{{goal.title}}」拆成可并行的子项，每个子项给出标题和简报", "max_children": 4}},
    {"id": "n_after", "type": "task", "title": "整合收尾",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "全部子项完成后：整合各子项产出，给出目标「{{goal.title}}」的最终结论"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n_plan", "source_handle": "done", "target": "n_after"},
    {"source": "n_after", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const CAPABILITY = `{
  "name": "按角色派工",
  "description": "节点派给小队里角色词匹配的成员（负载最轻者优先）。绑定此蓝图的任务必须是【小队任务】。角色词应体现岗位差异（技能不同=不同岗位词，如 design-implementer / proprietary-implementer），在小队详情页的角色词汇表里注册维护",
  "nodes": [
    {"id": "n1", "type": "task", "title": "执行工作",
     "task": {"assignee": "capability:implementer", "brief": "围绕目标「{{goal.title}}」完成本角色职责内的工作"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n1", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const ROUTER = `{
  "name": "智能路由",
  "description": "先分析改动，再由路由节点从注册类里选一个（LLM 受限选择）：小改动走快速路径，大改动走周全路径。解析失败自动落 fallback（周全），永远不会死锁",
  "nodes": [
    {"id": "n_analyze", "type": "task", "title": "改动分析",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "分析目标「{{goal.title}}」的改动规模与风险，输出分析结论（路由节点会据此分类）"}},
    {"id": "n_route", "type": "router", "title": "按规模路由",
     "router": {"classes": [
        {"id": "quick", "title": "小改动", "description": "改动小、风险低，可直接动手"},
        {"id": "thorough", "title": "大改动", "description": "改动大或有风险，需要先出方案再动手"}
      ], "fallback": "thorough"}},
    {"id": "t_quick", "type": "task", "title": "快速执行",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "直接动手完成：{{goal.title}}\\n（分析结论：{{node.n_analyze.output}}）"}},
    {"id": "t_thorough", "type": "task", "title": "周全执行",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "先写方案再执行：{{goal.title}}\\n（分析结论：{{node.n_analyze.output}}）"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n_analyze", "source_handle": "done", "target": "n_route"},
    {"source": "n_route", "source_handle": "quick", "target": "t_quick"},
    {"source": "n_route", "source_handle": "thorough", "target": "t_thorough"},
    {"source": "t_quick", "source_handle": "done", "target": "end_ok"},
    {"source": "t_thorough", "source_handle": "done", "target": "end_ok"}
  ]
}`;

const GATE = `{
  "name": "人工门审批",
  "description": "执行到关键点停下来等人工拍板：放行走「执行」分支，驳回报废走「返工」分支。24 小时无人决策自动驳回",
  "nodes": [
    {"id": "n_proposal", "type": "task", "title": "产出方案",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "围绕目标「{{goal.title}}」产出供人工审查的方案"}},
    {"id": "n_gate", "type": "gate", "title": "人工审查",
     "gate": {"prompt": "审查以下方案后拍板：\\n{{node.n_proposal.output}}", "actions": [
        {"id": "approve", "label": "放行"},
        {"id": "reject", "label": "驳回"}
      ], "timeout": {"after": "24h", "action": "reject"}}},
    {"id": "t_ship", "type": "task", "title": "执行落地",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "方案已获人工放行，按方案执行：{{goal.title}}"}},
    {"id": "t_rework", "type": "task", "title": "返工修改",
     "task": {"assignee": "agent:__AGENT_ID__", "brief": "方案被驳回，按驳回意见返工：{{goal.title}}"}},
    {"id": "end_ok", "type": "end", "title": "完成", "end": {"outcome": "success"}},
    {"id": "end_no", "type": "end", "title": "返工完成", "end": {"outcome": "success"}}
  ],
  "edges": [
    {"source": "n_proposal", "source_handle": "done", "target": "n_gate"},
    {"source": "n_gate", "source_handle": "approve", "target": "t_ship"},
    {"source": "n_gate", "source_handle": "reject", "target": "t_rework"},
    {"source": "n_gate", "source_handle": "timeout", "target": "t_rework"},
    {"source": "t_ship", "source_handle": "done", "target": "end_ok"},
    {"source": "t_rework", "source_handle": "done", "target": "end_no"}
  ]
}`;

export const BLUEPRINT_TEMPLATES: BlueprintTemplate[] = [
  {
    key: "serial",
    title: "串行流水线",
    desc: "调研 → 方案。上一步没验证通过，下一步不会开始。",
    name: "串行两步",
    description: "先调研，再基于调研结论写方案——上一步没通过验证，下一步不会开始",
    content: SERIAL,
  },
  {
    key: "parallel",
    title: "并行分头+汇合",
    desc: "两路同时开跑，全部通过后汇合。适合可拆分的独立工作。",
    name: "并行分头+汇合",
    description: "两路互不依赖的任务同时开跑，全部通过后由第三个节点汇合",
    content: PARALLEL,
  },
  {
    key: "conditional",
    title: "条件分支",
    desc: "按任务类别在激活时选路径：hitl 走人工，其余走自动。",
    name: "按类别分流",
    description: "激活时按任务类别走不同路径：hitl 走人工路径，其余走自动路径——条件只求值一次",
    content: CONDITIONAL,
  },
  {
    key: "spawn",
    title: "拆解扇出",
    desc: "规划者拆子项，平台物化成真实子任务并行跑，全部通过后整合。",
    name: "拆解扇出",
    description: "规划者先把任务拆成若干子项（上限4项），平台把子项物化成真实子任务并行执行，全部通过后整合收尾",
    content: SPAWN,
  },
  {
    key: "capability",
    title: "按角色派工",
    desc: "派给小队里角色词匹配的成员（负载最轻者优先）。要求任务绑小队、成员带角色词。技能差异请编码进角色词。",
    name: "按角色派工",
    description: "节点派给小队里角色为 implementer 的成员（谁空闲给谁）。绑定此蓝图的任务必须是【小队任务】，且小队成员的 role 里有该角色词",
    content: CAPABILITY,
  },
  {
    key: "router",
    title: "智能路由",
    desc: "LLM 从注册类里选一条路（可选小改动/大改动）。解析失败自动落 fallback，永不死锁。",
    name: "智能路由",
    description: "先分析改动，再由路由节点从注册类里选一个（LLM 受限选择）：小改动走快速路径，大改动走周全路径。解析失败自动落 fallback（周全），永远不会死锁",
    content: ROUTER,
  },
  {
    key: "gate",
    title: "人工门审批",
    desc: "关键点停下来等人工拍板：放行/驳回。超时（24h）自动驳回。",
    name: "人工门审批",
    description: "执行到关键点停下来等人工拍板：放行走「执行」分支，驳回报废走「返工」分支。24 小时无人决策自动驳回",
    content: GATE,
  },
];
