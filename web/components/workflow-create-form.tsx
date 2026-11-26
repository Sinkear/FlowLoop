"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, Plus, Trash2 } from "lucide-react";
import { BLUEPRINT_TEMPLATES } from "@/lib/blueprint-templates";
import { useAgents, useCreateBlueprint, useSquadMembers, useSquads } from "@/lib/queries";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { cn } from "@/lib/utils";
import type { Agent, Blueprint } from "@/lib/types";

// 模板里的任务/拆解节点都写成同一个占位符；创建时按节点分别替换成小队成员。
const AGENT_PLACEHOLDER = "agent:__AGENT_ID__";

type SpecNode = Record<string, unknown>;

type AgentSlot = {
  id: string;
  title: string;
  kind: "task" | "spawn";
};

function parseSpec(content: string): Record<string, unknown> | null {
  try {
    const raw: unknown = JSON.parse(content);
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
    return raw as Record<string, unknown>;
  } catch {
    return null;
  }
}

function specNodes(content: string): SpecNode[] {
  const spec = parseSpec(content);
  const nodes = spec?.nodes;
  if (!Array.isArray(nodes)) return [];
  return nodes.filter(
    (node): node is SpecNode => Boolean(node) && typeof node === "object" && !Array.isArray(node)
  );
}

function nodeBody(node: SpecNode): Record<string, unknown> | null {
  const raw = node.type === "spawn" ? node.spawn : node.task;
  return raw && typeof raw === "object" && !Array.isArray(raw)
    ? (raw as Record<string, unknown>)
    : null;
}

function agentSlots(content: string): AgentSlot[] {
  const slots: AgentSlot[] = [];
  for (const node of specNodes(content)) {
    const body = nodeBody(node);
    if (!body || body.assignee !== AGENT_PLACEHOLDER) continue;
    if (typeof node.id !== "string" || node.id === "") continue;
    slots.push({
      id: node.id,
      title: typeof node.title === "string" && node.title.trim() !== "" ? node.title : node.id,
      kind: node.type === "spawn" ? "spawn" : "task",
    });
  }
  return slots;
}

function bindNodeAgents(content: string, picks: Record<string, string>): string {
  const spec = parseSpec(content);
  if (!spec) return content;
  const nodes = Array.isArray(spec.nodes) ? spec.nodes : [];
  for (const raw of nodes) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
    const node = raw as SpecNode;
    const body = nodeBody(node);
    if (!body || body.assignee !== AGENT_PLACEHOLDER) continue;
    const picked = typeof node.id === "string" ? picks[node.id] : undefined;
    if (picked) body.assignee = `agent:${picked}`;
  }
  return JSON.stringify(spec, null, 2);
}

// 在流程末尾（end 之前）插入一个任务节点：把最后一个 task/spawn 节点的
// 唯一出边改成 source→新节点→原目标。只支持单出边的节点，分支节点留给
// 详情页的规范编辑器处理，避免一键改写把条件/路由的语义弄乱。
function addNodeAtTail(content: string): { content: string; id: string } | null {
  const spec = parseSpec(content);
  if (!spec) return null;
  const nodes = Array.isArray(spec.nodes)
    ? (spec.nodes.filter(
        (node): node is SpecNode => Boolean(node) && typeof node === "object" && !Array.isArray(node)
      ) as SpecNode[])
    : [];
  const edges = Array.isArray(spec.edges)
    ? (spec.edges.filter(
        (edge): edge is SpecNode => Boolean(edge) && typeof edge === "object" && !Array.isArray(edge)
      ) as SpecNode[])
    : [];
  if (!nodes.length || !edges.length) return null;

  let candidate: SpecNode | null = null;
  let outEdge: SpecNode | null = null;
  for (let i = nodes.length - 1; i >= 0; i -= 1) {
    const node = nodes[i];
    if (node.type !== "task" && node.type !== "spawn") continue;
    const outs = edges.filter((edge) => edge.source === node.id);
    if (outs.length === 1 && typeof outs[0].target === "string") {
      candidate = node;
      outEdge = outs[0];
      break;
    }
  }
  if (!candidate || !outEdge) return null;

  const existing = new Set(nodes.map((node) => node.id));
  let n = 1;
  while (existing.has(`extra_${n}`)) n += 1;
  const id = `extra_${n}`;
  const target = outEdge.target as string;
  outEdge.target = id;
  nodes.push({
    id,
    type: "task",
    title: `补充节点 ${n}`,
    task: { assignee: AGENT_PLACEHOLDER, brief: "补充这一步要做什么" },
  });
  edges.push({ source: id, source_handle: "done", target });
  spec.nodes = nodes;
  spec.edges = edges;
  return { content: JSON.stringify(spec, null, 2), id };
}

// 删除刚加的节点：把它的入边接回它的出边目标，避免图断链。
function removeNode(content: string, id: string): string {
  const spec = parseSpec(content);
  if (!spec) return content;
  const nodes = Array.isArray(spec.nodes) ? spec.nodes : [];
  const edges = Array.isArray(spec.edges) ? spec.edges : [];
  const keptNodes = nodes.filter(
    (node) => !(node && typeof node === "object" && !Array.isArray(node) && (node as SpecNode).id === id)
  );
  const outEdge = edges.find(
    (edge): edge is SpecNode =>
      Boolean(edge) && typeof edge === "object" && !Array.isArray(edge) && (edge as SpecNode).source === id
  );
  const nextTarget = outEdge && typeof outEdge.target === "string" ? outEdge.target : null;
  const keptEdges: unknown[] = [];
  for (const raw of edges) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
    const edge = raw as SpecNode;
    if (edge.source === id) continue;
    if (edge.target === id && nextTarget) edge.target = nextTarget;
    keptEdges.push(edge);
  }
  spec.nodes = keptNodes;
  spec.edges = keptEdges;
  return JSON.stringify(spec, null, 2);
}

function nodeBrief(content: string, id: string): string {
  const spec = parseSpec(content);
  if (!spec || !Array.isArray(spec.nodes)) return "";
  for (const raw of spec.nodes) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
    const node = raw as SpecNode;
    if (node.id !== id) continue;
    const body = nodeBody(node);
    return typeof body?.brief === "string" ? body.brief : "";
  }
  return "";
}

// 新增节点后允许改标题和简报；模板自带的节点不在这里改，避免动到校验过的文案。
function patchCustomNode(content: string, id: string, patch: { title?: string; brief?: string }): string {
  const spec = parseSpec(content);
  if (!spec || !Array.isArray(spec.nodes)) return content;
  for (const raw of spec.nodes) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
    const node = raw as SpecNode;
    if (node.id !== id) continue;
    if (patch.title !== undefined) node.title = patch.title;
    if (patch.brief !== undefined) {
      const body = nodeBody(node);
      if (body) body.brief = patch.brief;
    }
  }
  return JSON.stringify(spec, null, 2);
}

export function WorkflowCreateForm({
  squadId: fixedSquadId,
  initialTemplate,
  onClose,
  onCreated,
  embedded,
}: {
  squadId?: string;
  initialTemplate?: string;
  onClose: () => void;
  onCreated?: (workflow: Blueprint) => void;
  embedded?: boolean;
}) {
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const create = useCreateBlueprint();

  const initial = useMemo(
    () => BLUEPRINT_TEMPLATES.find((template) => template.key === initialTemplate),
    [initialTemplate]
  );
  const [squadId, setSquadId] = useState(fixedSquadId ?? "");
  const [templateKey, setTemplateKey] = useState(initial?.key ?? "");
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [content, setContent] = useState(initial?.content ?? "");
  // node id → agent id：同一份规范里每个节点可以派给不同的人。
  const [agentByNode, setAgentByNode] = useState<Record<string, string>>({});
  // 本轮新建时手动加进来的节点（只有这些允许删除 / 改标题与简报）。
  const [addedIds, setAddedIds] = useState<string[]>([]);
  const [addNote, setAddNote] = useState("");

  const { data: members } = useSquadMembers(squadId);
  const selectedSquad = squads?.find((squad) => squad.id === squadId);
  const slots = useMemo(() => agentSlots(content), [content]);

  // 可选执行者 = 小队成员里的智能体 ∪ 小队长（创建小队时队长不一定写进成员表）。
  const squadAgents = useMemo(() => {
    const ids = (members ?? [])
      .filter((member) => member.member_type === "agent")
      .map((member) => member.member_id);
    const leader = selectedSquad?.leader_id;
    if (leader && !ids.includes(leader)) ids.unshift(leader);
    const byId = new Map((agents ?? []).map((agent) => [agent.id, agent]));
    return ids
      .map((id) => byId.get(id))
      .filter((agent): agent is Agent => Boolean(agent))
      .sort((a, b) => (a.id === leader ? -1 : 0) - (b.id === leader ? -1 : 0));
  }, [agents, members, selectedSquad?.leader_id]);

  const error = create.error instanceof Error ? create.error.message : null;

  useEffect(() => {
    if (!squadId && squads?.length === 1) setSquadId(squads[0].id);
  }, [squadId, squads]);

  // 换小队 / 换模板后，把节点选择收敛到当前小队的成员：默认选第一个，
  // 已经选过且仍然合法的保留。
  useEffect(() => {
    const ids = squadAgents.map((agent) => agent.id);
    setAgentByNode((prev) => {
      const next: Record<string, string> = {};
      let changed = false;
      for (const slot of slots) {
        const current = prev[slot.id];
        const picked = current && ids.includes(current) ? current : (ids[0] ?? "");
        if (picked) next[slot.id] = picked;
        if (picked !== (current ?? "")) changed = true;
      }
      if (Object.keys(prev).length !== Object.keys(next).length) changed = true;
      return changed ? next : prev;
    });
  }, [slots, squadAgents]);

  const allNodesAssigned = slots.every((slot) => Boolean(agentByNode[slot.id]));
  const ready =
    squadId !== "" &&
    name.trim() !== "" &&
    content.trim() !== "" &&
    allNodesAssigned &&
    // 有占位符却解析不出节点时不允许提交，避免把 __AGENT_ID__ 存进规范。
    !(content.includes("__AGENT_ID__") && slots.length === 0);

  const pickTemplate = (key: string) => {
    const template = BLUEPRINT_TEMPLATES.find((item) => item.key === key);
    if (!template) return;
    setTemplateKey(key);
    setName(template.name);
    setDescription(template.description);
    setContent(template.content);
    setAgentByNode({});
    setAddedIds([]);
    setAddNote("");
  };

  const appendNode = () => {
    const added = addNodeAtTail(content);
    if (!added) {
      setAddNote("这个模板的分支结构不能一键加节点；可以先保存成草稿，在详情页的规范编辑器里改。");
      return;
    }
    setAddNote("");
    setContent(added.content);
    setAddedIds((prev) => [...prev, added.id]);
  };

  const submit = () => {
    if (!ready) return;
    const resolved = bindNodeAgents(content, agentByNode);
    create.mutate(
      { name: name.trim(), description: description.trim(), content: resolved, squad_id: squadId },
      { onSuccess: (workflow) => onCreated?.(workflow) }
    );
  };

  return (
    <Dialog
      title="新建工作流"
      size="wide"
      onClose={onClose}
      inline={embedded}
      footer={
        <>
          <span className="mr-auto text-[11px] leading-relaxed text-ink-3">
            保存后是 V1 草稿；发布后本小队任务即可选到。
          </span>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={submit} disabled={!ready || create.isPending}>
            {create.isPending ? "创建中…" : "创建工作流"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="所属小队" hint="必填。工作流只属于一支小队，节点执行者也只从这支小队里选">
            <select
              value={squadId}
              onChange={(event) => setSquadId(event.target.value)}
              className={inputCls}
              disabled={Boolean(fixedSquadId)}
            >
              <option value="">选择小队…</option>
              {(squads ?? []).map((squad) => (
                <option key={squad.id} value={squad.id}>{squad.name}</option>
              ))}
            </select>
          </Field>
          <Field label="名称" hint="在同一支小队内唯一">
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              className={inputCls}
              placeholder="例如：调研到上线"
            />
          </Field>
        </div>

        <Field label="描述" hint="可选，说明这条流程适合什么任务">
          <input
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            className={inputCls}
            placeholder="一句话说明它的用途…"
          />
        </Field>

        <div className="space-y-2">
          <p className="text-sm font-medium text-ink">从模板起步</p>
          <div className="grid gap-2 sm:grid-cols-2">
            {BLUEPRINT_TEMPLATES.map((template) => (
              <button
                key={template.key}
                type="button"
                onClick={() => pickTemplate(template.key)}
                className={cn(
                  "relative rounded-lg border p-3 text-left transition-colors",
                  templateKey === template.key
                    ? "border-accent/60 bg-accent/[0.06]"
                    : "border-line bg-surface-2/30 hover:border-accent/35 hover:bg-surface-2/60"
                )}
              >
                <span className="flex items-center justify-between gap-2">
                  <span className="text-[13px] font-medium text-ink">{template.title}</span>
                  {templateKey === template.key && (
                    <Check className="h-3.5 w-3.5 shrink-0 text-accent" />
                  )}
                </span>
                <span className="mt-1 block text-[11px] leading-relaxed text-ink-3">
                  {template.desc}
                </span>
              </button>
            ))}
          </div>
        </div>

        <div className="space-y-2">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-sm font-medium text-ink">流程节点</p>
              <p className="text-[11px] leading-relaxed text-ink-3">
                模板只是起点：每个节点可以换执行者，也可以在末尾再加一步。执行者只从「{selectedSquad?.name ?? "所选小队"}」里选。
              </p>
            </div>
            <button
              type="button"
              onClick={appendNode}
              disabled={!content}
              className="inline-flex shrink-0 items-center gap-1 rounded-md border border-line px-2.5 py-1 text-xs text-ink-2 transition-colors hover:border-accent/50 hover:text-ink disabled:opacity-50"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              加节点
            </button>
          </div>

          {slots.length > 0 ? (
            <div className="space-y-2">
              {slots.map((slot) => {
                const custom = addedIds.includes(slot.id);
                return (
                  <div
                    key={slot.id}
                    className="rounded-md border border-line bg-surface-2/30 px-3 py-2"
                  >
                    <div className="flex flex-wrap items-center gap-3">
                      <div className="min-w-0 flex-1">
                        {custom ? (
                          <input
                            value={slot.title}
                            onChange={(event) =>
                              setContent((prev) =>
                                patchCustomNode(prev, slot.id, { title: event.target.value })
                              )
                            }
                            className="w-full rounded border border-transparent bg-transparent px-1 py-0.5 text-[12px] text-ink outline-none transition-colors hover:border-line focus:border-accent/50"
                            placeholder="节点标题"
                          />
                        ) : (
                          <p className="truncate text-[12px] text-ink">{slot.title}</p>
                        )}
                        <p className="font-mono text-[10px] text-ink-3">
                          {slot.id} · {slot.kind === "spawn" ? "拆解" : "任务"}
                          {custom ? " · 自定义" : ""}
                        </p>
                      </div>
                      <select
                        value={agentByNode[slot.id] ?? ""}
                        onChange={(event) =>
                          setAgentByNode((prev) => ({ ...prev, [slot.id]: event.target.value }))
                        }
                        className={cn(inputCls, "w-full max-w-[220px]")}
                        disabled={squadAgents.length === 0}
                      >
                        <option value="">选择智能体…</option>
                        {squadAgents.map((agent) => (
                          <option key={agent.id} value={agent.id}>
                            {agent.name}
                            {agent.id === selectedSquad?.leader_id ? "（队长）" : ""}
                          </option>
                        ))}
                      </select>
                      {custom && (
                        <button
                          type="button"
                          title="删除这个节点"
                          onClick={() => {
                            setContent((prev) => removeNode(prev, slot.id));
                            setAddedIds((prev) => prev.filter((id) => id !== slot.id));
                          }}
                          className="rounded-md p-1.5 text-ink-3 transition-colors hover:bg-red-500/10 hover:text-red-300"
                        >
                          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                        </button>
                      )}
                    </div>
                    {custom && (
                      <textarea
                        value={nodeBrief(content, slot.id)}
                        onChange={(event) =>
                          setContent((prev) =>
                            patchCustomNode(prev, slot.id, { brief: event.target.value })
                          )
                        }
                        rows={2}
                        className="mt-2 w-full rounded-md border border-line bg-surface-1 px-2.5 py-1.5 text-[11px] leading-relaxed text-ink-2 outline-none focus:border-accent/50"
                        placeholder="这一步要做什么（可引用 {{goal.title}} / {{node.<id>.output}}）"
                      />
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="rounded-md border border-dashed border-line px-3 py-2 text-[11px] leading-relaxed text-ink-3">
              {content
                ? "这份模板没有按智能体派工的节点（capability 模板按角色派工）。可以先加一个节点，或保存后在详情页调整。"
                : "先选一个模板，节点会显示在这里。"}
            </p>
          )}

          {slots.length > 0 && squadAgents.length === 0 && (
            <p className="text-[11px] leading-relaxed text-amber-400">
              这支小队还没有可执行的智能体：先到「小队」详情里把智能体加为成员（或设置小队长）。
            </p>
          )}
          {addNote && <p className="text-[11px] leading-relaxed text-amber-400">{addNote}</p>}
        </div>
        {error && <p className="text-sm text-red-400">{error}</p>}
      </div>
    </Dialog>
  );
}
