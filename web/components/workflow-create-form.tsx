"use client";

import { useEffect, useMemo, useState } from "react";
import { Check } from "lucide-react";
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
            保存后是草稿；发布一个版本后，才能被这个小队的任务选中。
          </span>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={submit} disabled={!ready || create.isPending}>
            {create.isPending ? "创建中…" : "创建工作流"}
          </Button>
        </>
      }
    >
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
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

          {slots.length > 0 && (
            <div className="space-y-2">
              <div>
                <p className="text-sm font-medium text-ink">节点执行者</p>
                <p className="text-[11px] leading-relaxed text-ink-3">
                  每个节点可以选不同的人，可选范围只有「{selectedSquad?.name ?? "所选小队"}」里的智能体。
                </p>
              </div>
              <div className="space-y-2">
                {slots.map((slot) => (
                  <div
                    key={slot.id}
                    className="flex flex-wrap items-center gap-3 rounded-md border border-line bg-surface-2/30 px-3 py-2"
                  >
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-[12px] text-ink">{slot.title}</p>
                      <p className="font-mono text-[10px] text-ink-3">
                        {slot.id} · {slot.kind === "spawn" ? "拆解" : "任务"}
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
                          {agent.name}{agent.id === selectedSquad?.leader_id ? "（队长）" : ""}
                        </option>
                      ))}
                    </select>
                  </div>
                ))}
              </div>
              {squadAgents.length === 0 && (
                <p className="text-[11px] leading-relaxed text-amber-400">
                  这支小队还没有可执行的智能体：先到「小队」详情里把智能体加为成员（或设置小队长）。
                </p>
              )}
            </div>
          )}

          {error && <p className="text-sm text-red-400">{error}</p>}
        </div>

        <aside className="space-y-3 rounded-lg border border-line bg-surface-2/40 p-4">
          <p className="text-[11px] font-medium uppercase tracking-wide text-ink-3">创建后</p>
          <div className="space-y-2 text-[12px] leading-relaxed text-ink-2">
            <p>工作流会挂到 <span className="text-ink">{selectedSquad?.name ?? "所选小队"}</span>，并进入详情页。</p>
            <p>先出现 V1 草稿；改动、发布和版本对比都在详情里完成。</p>
            <p>发布后，这个小队的任务默认就能选到它。</p>
          </div>
          <div className="rounded-md border border-line bg-surface-1 p-2.5 text-[11px] leading-relaxed text-ink-3">
            这一步只选模板和每个节点的执行者，不写 JSON。模板已经过 V1–V6 校验，保存后可以直接发布。
          </div>
        </aside>
      </div>
    </Dialog>
  );
}
