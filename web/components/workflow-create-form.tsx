"use client";

import { useEffect, useMemo, useState } from "react";
import { Check } from "lucide-react";
import { BLUEPRINT_TEMPLATES } from "@/lib/blueprint-templates";
import { useAgents, useCreateBlueprint, useSquads } from "@/lib/queries";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { cn } from "@/lib/utils";
import type { Blueprint } from "@/lib/types";

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
  const [agentId, setAgentId] = useState("");

  const needsAgent = content.includes("__AGENT_ID__");
  const selectedSquad = squads?.find((squad) => squad.id === squadId);
  const error = create.error instanceof Error ? create.error.message : null;

  useEffect(() => {
    if (!squadId && squads?.length === 1) setSquadId(squads[0].id);
  }, [squadId, squads]);

  useEffect(() => {
    if (!agentId && agents && agents.length > 0) setAgentId(agents[0].id);
  }, [agentId, agents]);

  const pickTemplate = (key: string) => {
    const template = BLUEPRINT_TEMPLATES.find((item) => item.key === key);
    if (!template) return;
    setTemplateKey(key);
    setName(template.name);
    setDescription(template.description);
    setContent(template.content);
  };

  const ready =
    squadId !== "" &&
    name.trim() !== "" &&
    content.trim() !== "" &&
    (!needsAgent || agentId !== "");

  const submit = () => {
    if (!ready) return;
    const resolved = content.replaceAll("__AGENT_ID__", agentId);
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
            <Field label="所属小队" hint="必填。工作流只属于一支小队，也只能派这个队里的活">
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

          {needsAgent && (
            <Field label="执行者" hint="这套模板把任务派给具体智能体；创建后会写进规范">
              <select
                value={agentId}
                onChange={(event) => setAgentId(event.target.value)}
                className={inputCls}
              >
                <option value="">选择智能体…</option>
                {(agents ?? []).map((agent) => (
                  <option key={agent.id} value={agent.id}>{agent.name}</option>
                ))}
              </select>
            </Field>
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
            这一步只选模板，不写 JSON。模板已经过 V1–V6 校验，保存后可以直接发布。
          </div>
        </aside>
      </div>
    </Dialog>
  );
}
