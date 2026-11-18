// 蓝本模板（Phase A）：新蓝图页的起步预设。
// 每份 content 都是能直接通过 V1–V6 校验的完整规范 JSON——用户点一下
// 就有一个能保存、能发布、能绑定任务的起点，而不是面对一片空白 JSON。
//
// 约定：占位符 __AGENT_ID__ 会在提交时被表单里选中的智能体 id 替换
// （agent:<32位id>）；{{goal.title}} / {{node.<id>.output}} 由平台在
// 编译/释放时解析（拍1/拍2）。字段必须走 goal 白名单（V5）。

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
];
