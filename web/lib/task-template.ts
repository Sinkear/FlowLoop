// 「新建任务」的默认输出句模板。
//
// 为什么要模板：大多数任务描述只写了“做什么”，没写“交付什么”。agent 于是
// 交回一段自由发挥的总结，验收时还得再问一遍。把输出要求写成一句固定的话，
// 用户按一次 Tab 就能带上，比记格式或翻文档轻。
//
// 只插入一次：已经包含「输出要求：」就不再重复插（Tab 恢复成普通的焦点切换）。
export const OUTPUT_MARKER = "输出要求：";

export const OUTPUT_TEMPLATE =
  "输出要求：给出简洁的改动说明（改了什么、为什么），列出涉及的文件，并用一句话说明如何验证。";

export function appendOutputTemplate(current: string): string {
  if (current.includes(OUTPUT_MARKER)) return current;
  const trimmed = current.trimEnd();
  return trimmed ? `${trimmed}\n\n${OUTPUT_TEMPLATE}` : OUTPUT_TEMPLATE;
}
// ── 通用创建入口的实践模板 ───────────────────────────────────────────────
//
// 2026-11-21：「新建」页从“只建任务”扩成“任务 / 项目 / 小队 / 智能体 / 工作流 / 定时任务”
// 通用入口。Tab 不再补一句抽象的「输出要求」，而是补一条能直接照着改的
// 实践表达：把该填的字段（仓库、验收级别、负责人、运行时、卡点）摆出来，
// 用户只需替换「XX」和尖括号里的值。
//
// 和输出要求一样只插一次：文本里已经出现任意一种模板标记，Tab 就恢复成
// 普通焦点切换，避免用户在模板之间来回切换时被追加两段。

export type CreateKind = "goal" | "project" | "squad" | "agent" | "workflow" | "schedule";

export const CREATE_TEMPLATES: Record<CreateKind, string> = {
  goal: "创建一个任务：<要做什么>。\n负责人：<agent 或小队>；项目：<项目名>；验收级别：HITL 人工验收 / HOOTL 自动验收。",
  project:
    "创建一个项目：名称「XX」，仓库地址 <https://…>，默认分支 main，验收级别 HITL / HOOTL / 暂不设置。",
  squad: "创建一个小队：名称「XX」，负责人 <智能体名>，成员 <…>，负责 <…>。",
  agent:
    "创建智能体：名称「XX」，使用运行时 <运行时名>，加入小队 <小队名>（可选），职责 <…>。",
  workflow:
    "根据「XX 场景」创建一个工作流：名称「XX」，所属小队 <小队名>，步骤 <1… 2…>，需要人工确认的卡点 <…>。",
  schedule:
    "创建一个定时任务：名称「XX」，项目 <项目名>，cron <0 9 * * *>，负责人 <agent 或小队>，完成后迭代工作流。",
};

// 标记取模板里唯一、且不会在日常描述中出现的短句。
export const TEMPLATE_MARKERS = [
  "创建一个任务：",
  "创建一个项目：",
  "创建一个小队：",
  "创建一个 agent：",
  "创建一个工作流：",
  "创建一个定时任务：",
];

export function hasCreateTemplate(text: string): boolean {
  return TEMPLATE_MARKERS.some((m) => text.includes(m));
}

export function appendCreateTemplate(current: string, kind: CreateKind): string {
  if (hasCreateTemplate(current)) return current;
  const tpl = CREATE_TEMPLATES[kind];
  const trimmed = current.trimEnd();
  return trimmed ? `${trimmed}\n\n${tpl}` : tpl;
}
