// 实践包导入的协议层：类型、请求，以及三份可以直接填进向导的示例。
// 这里不引入新依赖，也不用 lib/api 的内部 api<T>（它未导出），所以直接
// 用 fetch 拼 API_BASE；错误按后端的 {error, code, detail} 取 error 文案。

export const PRACTICE_API_BASE = process.env.NEXT_PUBLIC_API_URL || "/backend";

export type PracticeImportKind = "squad" | "project" | "workflow";

export interface PracticeImportAgent {
  name: string;
  final_name: string;
  renamed?: boolean;
}

export interface PracticeImportPreview {
  kind: PracticeImportKind;
  package_id?: string;
  name: string;
  original_name?: string;
  description?: string;
  version?: string;
  source: "official" | "community" | "unverified";
  source_label: string;
  source_note?: string;
  project_type?: "repo" | "scratch";
  git_url?: string;
  needs_git_url?: boolean;
  owner_squad?: string;
  owner_squad_id?: string;
  needs_squad?: boolean;
  agents?: PracticeImportAgent[];
  team_squad?: string;
  goals?: string[];
  schedules?: string[];
  workflow_node_count?: number;
  name_suffix?: boolean;
  warnings?: string[];
}

export interface PracticeApplyItem {
  kind: string;
  name: string;
  id?: string;
  action: string;
  error?: string;
}

export interface PracticeImportRef {
  kind: string;
  id: string;
  name: string;
  url: string;
}

export interface PracticeImportResult {
  preview: PracticeImportPreview;
  project?: PracticeImportRef;
  squad?: PracticeImportRef;
  workflow?: PracticeImportRef;
  agents?: PracticeImportRef[];
  goals?: PracticeImportRef[];
  schedules?: PracticeImportRef[];
  items?: PracticeApplyItem[];
  warnings?: string[];
}

export interface PracticeImportBody {
  package: Record<string, unknown>;
  name?: string;
  squad_id?: string;
  git_url?: string;
  git_credentials?: string;
  project_type?: "repo" | "scratch";
}

async function postPracticeImport<T>(path: string, body: PracticeImportBody): Promise<T> {
  const res = await fetch(`${PRACTICE_API_BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const message =
      data && typeof data === "object" && "error" in data
        ? String((data as { error?: unknown }).error ?? "")
        : "";
    throw new Error(message || `请求失败（HTTP ${res.status}）`);
  }
  return data as T;
}

export function previewPracticeImport(body: PracticeImportBody): Promise<PracticeImportPreview> {
  return postPracticeImport<PracticeImportPreview>("/templates/import/preview", body);
}

export function importPracticePackage(body: PracticeImportBody): Promise<PracticeImportResult> {
  return postPracticeImport<PracticeImportResult>("/templates/import", body);
}

export const PRACTICE_KIND_LABEL: Record<PracticeImportKind, string> = {
  squad: "小队包",
  project: "项目包",
  workflow: "工作流包",
};

export function normalizePracticeKind(kind: unknown): PracticeImportKind | null {
  if (typeof kind !== "string") return null;
  switch (kind.trim().toLowerCase()) {
    case "squad":
    case "squad-template":
    case "team":
    case "team-template":
      return "squad";
    case "project":
    case "project-template":
      return "project";
    case "workflow":
    case "workflow-template":
    case "blueprint":
    case "blueprint-template":
      return "workflow";
    default:
      return null;
  }
}

// 示例 intentionally use capability: 引用，这样只要有一支小队就能导入；
// 想展示 agent 指派时，把 capability:developer 换成 agent:<你的 Agent 名称>。
const PROJECT_SAMPLE = {
  api_version: "ouroflow/v1",
  kind: "project",
  metadata: {
    name: "数据平台",
    description: "一支小队维护一个数据分析项目：搭骨架、写采集、每天巡检。",
    version: "1.0.0",
  },
  spec: {
    domain: { type: "scratch", default_branch: "main" },
    team: {
      agents: [
        { name: "data-owner", description: "数据分析负责人", system_prompt: "你是数据项目的负责人。" },
        { name: "data-dev", description: "数据开发", system_prompt: "你负责实现数据管道。" },
      ],
      squad: {
        name: "数据小队",
        leader: "data-owner",
        instructions: "拆解任务并委派给队内成员。",
        members: [{ name: "data-dev", role: "member" }],
      },
    },
    goals: [
      { title: "搭好项目骨架", assignee: "data-owner", assignee_type: "agent", start: true },
      { title: "实现第一条采集链路", assignee: "data-dev", assignee_type: "agent" },
    ],
    schedules: [
      {
        name: "每日巡检",
        title: "检查采集链路是否正常",
        cron: "0 9 * * *",
        assignee: "data-owner",
        assignee_type: "agent",
      },
    ],
  },
};

const SQUAD_SAMPLE = {
  api_version: "ouroflow/v1",
  kind: "squad",
  metadata: {
    name: "调研小队",
    description: "一支负责收集资料、交叉核对并给出结论的小队。",
    version: "1.0.0",
  },
  spec: {
    agents: [
      { name: "scout-lead", description: "调研负责人", system_prompt: "你负责拆解调研问题并汇总结论。" },
      { name: "scout-worker", description: "调研成员", system_prompt: "你负责收集与核对资料。" },
    ],
    squad: {
      name: "调研小队",
      leader: "scout-lead",
      instructions: "把调研问题拆成可核对的小块再委派。",
      members: [{ name: "scout-worker", role: "member" }],
    },
  },
};

const WORKFLOW_SAMPLE = {
  api_version: "ouroflow/v1",
  kind: "workflow",
  metadata: {
    name: "发布流程",
    description: "先计划，再开发，最后复核；复核通过才结束。",
  },
  spec: {
    graph: {
      name: "发布流程",
      description: "一个最小可用的发布流程。",
      nodes: [
        {
          id: "plan",
          type: "task",
          title: "拆解发布",
          task: { assignee: "capability:developer", brief: "列出发布步骤与风险。" },
        },
        {
          id: "review",
          type: "task",
          title: "复核结果",
          task: { assignee: "capability:reviewer", brief: "核对步骤是否全部完成。" },
        },
        { id: "done", type: "end", title: "完成", end: { outcome: "success" } },
      ],
      edges: [
        { source: "plan", source_handle: "done", target: "review" },
        { source: "review", source_handle: "done", target: "done" },
      ],
    },
  },
};

export const PRACTICE_SAMPLES: { kind: PracticeImportKind; label: string; json: string }[] = [
  { kind: "project", label: "项目包示例", json: JSON.stringify(PROJECT_SAMPLE, null, 2) },
  { kind: "squad", label: "小队包示例", json: JSON.stringify(SQUAD_SAMPLE, null, 2) },
  { kind: "workflow", label: "工作流包示例", json: JSON.stringify(WORKFLOW_SAMPLE, null, 2) },
];