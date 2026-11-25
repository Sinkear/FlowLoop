"use client";

import { useMemo, useState } from "react";
import { GitBranchPlus, Loader2, Plus, Trash2, Undo2 } from "lucide-react";
import { Button, Dialog, Field, Segmented, inputCls } from "@/components/ui";
import { useAgents, useAllSquadRoles, useUpdateBlueprintDraft } from "@/lib/queries";
import type { BlueprintVersion } from "@/lib/types";
import { cn } from "@/lib/utils";
import { WorkflowErrorBox } from "@/components/workflows/workflow-shared";

// ── 规范的本地形状（只覆盖 V1 结构编辑器会碰的字段；其余字段原样保留） ──

type SpecTask = {
  assignee?: string;
  brief?: string;
  verify?: Record<string, unknown>;
  [extra: string]: unknown;
};

type SpecSpawn = {
  assignee?: string;
  brief?: string;
  child_type?: string;
  max_children?: number;
  [extra: string]: unknown;
};

type SpecEnd = { outcome?: string; [extra: string]: unknown };

type EditorNode = {
  id: string;
  type: string;
  title: string;
  task?: SpecTask;
  spawn?: SpecSpawn;
  end?: SpecEnd;
  condition?: Record<string, unknown>;
  router?: Record<string, unknown>;
  gate?: Record<string, unknown>;
  [extra: string]: unknown;
};

type EditorEdge = {
  source: string;
  source_handle?: string;
  target: string;
  [extra: string]: unknown;
};

type EditorGraph = {
  name?: string;
  description?: string;
  nodes: EditorNode[];
  edges: EditorEdge[];
  [extra: string]: unknown;
};

const NODE_ID_RE = /^[a-z][a-z0-9_]{0,63}$/;
const ASSIGNEE_RE = /^(agent|squad|capability):[^\s]+$/;

const NODE_TYPES: Record<string, { label: string; cls: string }> = {
  task: { label: "任务", cls: "border-sky-500/40 bg-sky-500/10 text-sky-300" },
  spawn: { label: "拆解", cls: "border-violet-500/40 bg-violet-500/10 text-violet-300" },
  end: { label: "结束", cls: "border-emerald-500/40 bg-emerald-500/10 text-emerald-300" },
  condition: { label: "条件", cls: "border-amber-500/40 bg-amber-500/10 text-amber-300" },
  router: { label: "路由", cls: "border-fuchsia-500/40 bg-fuchsia-500/10 text-fuchsia-300" },
  gate: { label: "门禁", cls: "border-orange-500/40 bg-orange-500/10 text-orange-300" },
};

const EDITABLE_TYPES = new Set(["task", "spawn", "end"]);

const HANDLE_LABELS: Record<string, string> = {
  done: "完成",
  failed: "失败",
  human: "人工",
  auto: "自动",
  timeout: "超时",
  else: "其他",
  fallback: "兜底",
};

function mutError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

function normalizeNode(raw: unknown, i: number): EditorNode {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const type = typeof r.type === "string" ? r.type : "task";
  return {
    ...r,
    id: typeof r.id === "string" ? r.id : `node_${i + 1}`,
    type,
    title: typeof r.title === "string" ? r.title : "",
  };
}

function normalizeEdge(raw: unknown): EditorEdge | null {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  if (typeof r.source !== "string" || typeof r.target !== "string") return null;
  return {
    ...r,
    source: r.source,
    target: r.target,
    ...(typeof r.source_handle === "string" ? { source_handle: r.source_handle } : {}),
  };
}

function parseSpec(text: string): { graph?: EditorGraph; error?: string } {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    return { error: `JSON 解析失败：${mutError(e)}` };
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { error: "规范必须是 JSON 对象。" };
  }
  const r = raw as Record<string, unknown>;
  if (!Array.isArray(r.nodes) || !Array.isArray(r.edges)) {
    return { error: "规范缺少 nodes[] 或 edges[]。" };
  }
  const edges: EditorEdge[] = [];
  for (let i = 0; i < r.edges.length; i++) {
    const edge = normalizeEdge(r.edges[i]);
    if (!edge) {
      return { error: `第 ${i + 1} 条连线缺少 source 或 target（两者都必须是字符串）。` };
    }
    edges.push(edge);
  }
  return {
    graph: {
      ...r,
      name: typeof r.name === "string" ? r.name : "",
      description: typeof r.description === "string" ? r.description : "",
      nodes: r.nodes.map(normalizeNode),
      edges,
    },
  };
}

function nextNodeId(nodes: EditorNode[]): string {
  let max = 0;
  for (const n of nodes) {
    const m = /^n(\d+)$/.exec(n.id);
    if (m) max = Math.max(max, Number(m[1]));
  }
  return `n${max + 1}`;
}

function validateGraph(g: EditorGraph): string | null {
  if (!g.name || !g.name.trim()) return "规范缺少名称（graph.name）：请在顶部补一个。";
  if (g.nodes.length === 0) return "至少保留一个节点。";
  if (g.edges.length === 0) return "至少保留一条连线：否则任务编译不出执行顺序。";
  const seen = new Set<string>();
  for (const n of g.nodes) {
    if (!NODE_ID_RE.test(n.id)) {
      return `节点 id「${n.id}」不合法：需小写字母开头，仅含 a-z / 0-9 / _，最长 64 字符。`;
    }
    if (seen.has(n.id)) return `节点 id「${n.id}」重复。`;
    seen.add(n.id);
    if (!n.title.trim()) return `节点「${n.id}」缺少标题。`;
    if (n.type === "task" || n.type === "spawn") {
      const body = n.type === "task" ? n.task : n.spawn;
      const assignee = body?.assignee ?? "";
      const brief = body?.brief ?? "";
      if (!ASSIGNEE_RE.test(assignee)) {
        return `节点「${n.title || n.id}」的执行者不合法：需要 agent:<id> / squad:<id> / capability:<角色>。`;
      }
      if (!brief.trim()) return `节点「${n.title || n.id}」缺少执行说明（brief）。`;
    }
  }
  for (const e of g.edges) {
    if (!seen.has(e.source) || !seen.has(e.target)) {
      return `连线 ${e.source} → ${e.target} 指向了不存在的节点。`;
    }
  }
  return null;
}

/**
 * 结构编辑器：把“画流程”变成增删节点 + 改执行者 + 连依赖，保存仍然是写草稿 JSON。
 * 条件/路由/门禁这些 V1 不展开的节点，保留原字段并在 JSON 模式里编辑。
 */
export function WorkflowSpecEditor({
  version,
  squadId,
  onClose,
  onSaved,
}: {
  version: BlueprintVersion;
  squadId: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const initial = useMemo(() => parseSpec(version.content), [version.content]);
  const [graph, setGraph] = useState<EditorGraph | null>(initial.graph ?? null);
  const [jsonText, setJsonText] = useState(() =>
    initial.graph ? pretty(initial.graph) : version.content,
  );
  const [mode, setMode] = useState<"structure" | "json">(initial.graph ? "structure" : "json");
  const [parseError, setParseError] = useState<string | null>(initial.error ?? null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);

  const agents = useAgents();
  const roles = useAllSquadRoles();
  const update = useUpdateBlueprintDraft();

  const squadRoles = (roles.data ?? []).filter((r) => r.squad_id === squadId);
  const assigneeChoices = [
    ...(agents.data ?? []).map((a) => ({ value: `agent:${a.id}`, label: `${a.name}（执行者）` })),
    ...squadRoles.map((r) => ({ value: `capability:${r.name}`, label: `${r.name}（角色）` })),
    ...(squadId ? [{ value: `squad:${squadId}`, label: "整支小队（并行拆解）" }] : []),
  ];
  const defaultAssignee =
    assigneeChoices.find((c) => c.value.startsWith("agent:"))?.value ??
    assigneeChoices.find((c) => c.value.startsWith("capability:"))?.value ??
    "";

  const nodes = graph?.nodes ?? [];

  function commit(next: EditorGraph) {
    setGraph(next);
    setJsonText(pretty(next));
    setDirty(true);
    setSaveError(null);
  }

  function patchGraph(patch: Partial<EditorGraph>) {
    if (!graph) return;
    commit({ ...graph, ...patch });
  }

  function patchNode(id: string, patch: Partial<EditorNode>) {
    if (!graph) return;
    commit({ ...graph, nodes: graph.nodes.map((n) => (n.id === id ? { ...n, ...patch } : n)) });
  }

  function patchBody(id: string, kind: "task" | "spawn", patch: Record<string, unknown>) {
    if (!graph) return;
    commit({
      ...graph,
      nodes: graph.nodes.map((n) =>
        n.id === id ? { ...n, [kind]: { ...(n[kind] ?? {}), ...patch } } : n,
      ),
    });
  }

  function removeNode(id: string) {
    if (!graph) return;
    commit({
      ...graph,
      nodes: graph.nodes.filter((n) => n.id !== id),
      edges: graph.edges.filter((e) => e.source !== id && e.target !== id),
    });
  }

  function addNode(type: "task" | "spawn" | "end") {
    if (!graph) return;
    const id = nextNodeId(graph.nodes);
    const node: EditorNode =
      type === "task"
        ? {
            id,
            type,
            title: "新任务节点",
            task: { assignee: defaultAssignee, brief: "围绕目标完成这一小步。", verify: { mode: "domain_default" } },
          }
        : type === "spawn"
          ? {
              id,
              type,
              title: "拆解节点",
              spawn: { assignee: defaultAssignee, brief: "把目标拆成可直接执行的子任务。", child_type: "task" },
            }
          : { id, type, title: "完成", end: { outcome: "success" } };
    commit({ ...graph, nodes: [...graph.nodes, node] });
  }

  function addDependency(targetId: string, sourceId: string) {
    if (!graph || !sourceId) return;
    if (graph.edges.some((e) => e.source === sourceId && e.target === targetId)) return;
    commit({
      ...graph,
      edges: [...graph.edges, { source: sourceId, source_handle: "done", target: targetId }],
    });
  }

  function removeEdge(source: string, handle: string | undefined, target: string) {
    if (!graph) return;
    commit({
      ...graph,
      edges: graph.edges.filter(
        (e) => !(e.source === source && (e.source_handle ?? "") === (handle ?? "") && e.target === target),
      ),
    });
  }

  function switchMode(next: "structure" | "json") {
    if (next === mode) return;
    if (next === "json") {
      if (graph) setJsonText(pretty(graph));
      setParseError(null);
      setMode("json");
      return;
    }
    const parsed = parseSpec(jsonText);
    if (!parsed.graph) {
      setParseError(parsed.error ?? "JSON 解析失败。");
      return;
    }
    setGraph(parsed.graph);
    setParseError(null);
    setMode("structure");
  }

  function reset() {
    setGraph(initial.graph ?? null);
    setJsonText(initial.graph ? pretty(initial.graph) : version.content);
    setParseError(initial.error ?? null);
    setSaveError(null);
    setDirty(false);
    setMode(initial.graph ? "structure" : "json");
  }

  async function save() {
    setSaveError(null);
    let candidate = graph;
    if (mode === "json") {
      const parsed = parseSpec(jsonText);
      if (!parsed.graph) {
        setParseError(parsed.error ?? "JSON 解析失败。");
        return;
      }
      candidate = parsed.graph;
    }
    if (!candidate) return;
    const invalid = validateGraph(candidate);
    if (invalid) {
      setSaveError(invalid);
      return;
    }
    try {
      await update.mutateAsync({ versionId: version.id, content: `${pretty(candidate)}\n` });
      onSaved();
    } catch (e) {
      setSaveError(mutError(e));
    }
  }

  const nodeById = new Map(nodes.map((n) => [n.id, n] as const));
  const incoming = (id: string) => (graph?.edges ?? []).filter((e) => e.target === id);

  return (
    <Dialog
      title={`编辑结构 · v${version.version}`}
      onClose={update.isPending ? () => {} : onClose}
      size="wide"
      footer={
        <>
          <div className="mr-auto flex items-center gap-2">
            {dirty && <span className="text-[11px] text-amber-300">有未保存改动</span>}
            {nodes.length > 0 && <span className="text-[11px] text-ink-3">{nodes.length} 个节点</span>}
          </div>
          <Button variant="ghost" onClick={reset} disabled={update.isPending || !dirty}>
            <Undo2 className="h-4 w-4" />
            还原
          </Button>
          <Button variant="outline" onClick={onClose} disabled={update.isPending}>
            取消
          </Button>
          <Button onClick={save} disabled={update.isPending || !dirty}>
            {update.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            保存草稿
          </Button>
        </>
      }
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-ink-3">
          编辑会直接覆盖当前草稿的内容；发布后的版本不可改。
        </p>
        <Segmented
          value={mode}
          onChange={(v) => switchMode(v as "structure" | "json")}
          options={[
            { value: "structure", label: "结构" },
            { value: "json", label: "JSON" },
          ]}
        />
      </div>

      {parseError && <WorkflowErrorBox title="JSON 还没解析通过" message={parseError} />}
      {saveError && <WorkflowErrorBox title="保存没过校验" message={saveError} />}

      {mode === "json" ? (
        <div>
          <textarea
            value={jsonText}
            onChange={(e) => {
              setJsonText(e.target.value);
              setDirty(true);
              setSaveError(null);
            }}
            spellCheck={false}
            className={`${inputCls} min-h-[420px] font-mono text-xs leading-relaxed`}
          />
          <p className="mt-1 text-[11px] text-ink-3">
            切回「结构」会做一次 JSON 解析；V1 结构编辑器不展开的字段（condition / router / gate / verify）会原样保留。
          </p>
        </div>
      ) : graph ? (
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)]">
            <Field label="规范名称">
              <input
                value={graph.name ?? ""}
                onChange={(e) => patchGraph({ name: e.target.value })}
                className={inputCls}
                placeholder="例如：修复线上缺陷"
              />
            </Field>
            <Field label="说明（可选）">
              <input
                value={graph.description ?? ""}
                onChange={(e) => patchGraph({ description: e.target.value })}
                className={inputCls}
                placeholder="一句话说明这张图解决什么问题"
              />
            </Field>
          </div>

          <div className="flex flex-wrap items-center gap-2 border-b border-line/70 pb-2">
            <span className="text-xs font-medium text-ink-2">节点</span>
            <span className="text-[11px] text-ink-3">连线由每个节点的「前置依赖」决定</span>
            <div className="ml-auto flex flex-wrap gap-1.5">
              <Button variant="outline" className="h-7 px-2.5 text-[11px]" onClick={() => addNode("task")}>
                <Plus className="h-3.5 w-3.5" />
                任务
              </Button>
              <Button variant="outline" className="h-7 px-2.5 text-[11px]" onClick={() => addNode("spawn")}>
                <GitBranchPlus className="h-3.5 w-3.5" />
                拆解
              </Button>
              <Button variant="outline" className="h-7 px-2.5 text-[11px]" onClick={() => addNode("end")}>
                <Plus className="h-3.5 w-3.5" />
                结束
              </Button>
            </div>
          </div>

          <datalist id="wf-editor-assignees">
            {assigneeChoices.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </datalist>

          <div className="space-y-3">
            {nodes.map((n) => {
              const meta = NODE_TYPES[n.type] ?? { label: n.type, cls: "border-line bg-surface-2 text-ink-3" };
              const editable = EDITABLE_TYPES.has(n.type);
              const body = n.type === "task" ? n.task : n.type === "spawn" ? n.spawn : undefined;
              const ups = incoming(n.id);
              return (
                <div key={n.id} className="overflow-hidden rounded-lg border border-line bg-surface-1">
                  <div className="flex flex-wrap items-center gap-2 border-b border-line/70 bg-surface-2/30 px-3 py-2">
                    <span className={cn("rounded border px-1.5 py-px text-[10px]", meta.cls)}>{meta.label}</span>
                    {editable ? (
                      <input
                        value={n.title}
                        onChange={(e) => patchNode(n.id, { title: e.target.value })}
                        className="min-w-[140px] flex-1 rounded border border-transparent bg-transparent px-1 py-1 text-sm font-medium text-ink outline-none transition-colors hover:border-line focus:border-accent/50 focus:bg-surface-1"
                        placeholder="节点标题"
                      />
                    ) : (
                      <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{n.title || "（无标题）"}</span>
                    )}
                    <span className="font-mono text-[10px] text-ink-3">{n.id}</span>
                    <button
                      type="button"
                      onClick={() => removeNode(n.id)}
                      className="rounded p-1 text-ink-3 transition-colors hover:bg-red-500/10 hover:text-red-400"
                      title="删除这个节点（同时删掉相关连线）"
                      aria-label={`删除节点 ${n.title || n.id}`}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>

                  {!editable ? (
                    <p className="px-3 py-2.5 text-[11px] text-ink-3">
                      条件 / 路由 / 门禁节点的分支配置暂不展开；切到 JSON 模式可编辑 cases、classes、actions。
                    </p>
                  ) : n.type === "end" ? (
                    <div className="flex flex-wrap items-center gap-3 px-3 py-2.5">
                      <Field label="结束结果">
                        <select
                          value={n.end?.outcome ?? "success"}
                          onChange={(e) => patchNode(n.id, { end: { ...(n.end ?? {}), outcome: e.target.value } })}
                          className={`${inputCls} h-8 w-32 text-xs`}
                        >
                          <option value="success">success（成功）</option>
                          <option value="failure">failure（失败）</option>
                        </select>
                      </Field>
                      <p className="text-[11px] text-ink-3">任务跑到这个节点时按结果收尾。</p>
                    </div>
                  ) : (
                    <div className="space-y-3 p-3">
                      <Field label="执行者" hint="agent:<id> / squad:<id> / capability:<角色>">
                        <input
                          list="wf-editor-assignees"
                          value={body?.assignee ?? ""}
                          onChange={(e) =>
                            patchBody(n.id, n.type as "task" | "spawn", { assignee: e.target.value })
                          }
                          className={`${inputCls} h-8 font-mono text-xs`}
                          placeholder={defaultAssignee || "agent:<id>"}
                        />
                      </Field>
                      {assigneeChoices.length > 0 && (
                        <div className="flex flex-wrap gap-1">
                          {assigneeChoices.map((choice) => {
                            const current = (body?.assignee ?? "") === choice.value;
                            return (
                              <button
                                key={choice.value}
                                type="button"
                                onClick={() => patchBody(n.id, n.type as "task" | "spawn", { assignee: choice.value })}
                                className={cn(
                                  "rounded border px-1.5 py-0.5 text-[10px] transition-colors",
                                  current
                                    ? "border-accent/50 bg-accent/10 text-accent"
                                    : "border-line bg-surface-2/60 text-ink-3 hover:text-ink-2",
                                )}
                              >
                                {choice.label}
                              </button>
                            );
                          })}
                        </div>
                      )}
                      {body && "assignee" in body && body.assignee && !ASSIGNEE_RE.test(body.assignee) && (
                        <p className="text-[11px] text-amber-300">
                          这个值不符合执行者格式，保存会被后端拒绝。
                        </p>
                      )}
                      <Field label={n.type === "spawn" ? "拆解说明" : "任务说明"}>
                        <textarea
                          value={body?.brief ?? ""}
                          onChange={(e) => patchBody(n.id, n.type as "task" | "spawn", { brief: e.target.value })}
                          rows={2}
                          className={`${inputCls} text-xs`}
                          placeholder={n.type === "spawn" ? "怎么拆、拆成什么样" : "这一步要做什么、验收标准是什么"}
                        />
                      </Field>
                    </div>
                  )}

                  <div className="flex flex-wrap items-center gap-2 border-t border-line/60 px-3 py-2">
                    <span className="text-[10px] uppercase tracking-wide text-ink-3">前置依赖</span>
                    {ups.length === 0 ? (
                      <span className="text-[11px] text-ink-3">入口节点（没有前置）</span>
                    ) : (
                      ups.map((e) => (
                        <span
                          key={`${e.source}|${e.source_handle ?? ""}|${e.target}`}
                          className="inline-flex items-center gap-1 rounded border border-line bg-surface-2/60 px-1.5 py-px text-[10px] text-ink-2"
                        >
                          {nodeById.get(e.source)?.title || e.source}
                          <span className="text-ink-3">
                            ·{HANDLE_LABELS[e.source_handle ?? ""] ?? e.source_handle ?? "完成"}
                          </span>
                          <button
                            type="button"
                            onClick={() => removeEdge(e.source, e.source_handle, e.target)}
                            className="ml-0.5 text-ink-3 hover:text-red-400"
                            aria-label="删除这条依赖"
                          >
                            ×
                          </button>
                        </span>
                      ))
                    )}
                    {nodes.length > 1 && (
                      <select
                        value=""
                        onChange={(e) => addDependency(n.id, e.target.value)}
                        className="ml-auto h-6 rounded border border-line bg-surface-1 px-1 text-[10px] text-ink-2"
                        aria-label={`为 ${n.title || n.id} 添加前置依赖`}
                      >
                        <option value="">+ 添加前置节点…</option>
                        {nodes
                          .filter(
                            (other) =>
                              (other.type === "task" || other.type === "spawn") &&
                              other.id !== n.id &&
                              !ups.some((e) => e.source === other.id),
                          )
                          .map((other) => (
                            <option key={other.id} value={other.id}>
                              {other.title || other.id}
                            </option>
                          ))}
                      </select>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          {nodes.length === 0 && (
            <p className="rounded-lg border border-dashed border-line px-4 py-8 text-center text-xs text-ink-3">
              图里已经没有节点。点上面的「任务 / 拆解 / 结束」开始搭，或在 JSON 模式粘贴一份完整规范。
            </p>
          )}
        </div>
      ) : (
        <p className="text-xs text-ink-3">先切到 JSON 模式修好解析问题，再回到结构编辑。</p>
      )}
    </Dialog>
  );
}