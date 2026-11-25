"use client";

import { useState } from "react";
import { AlertTriangle, GitBranch, Radio, Users } from "lucide-react";
import { Button, Field, SectionCard, inputCls } from "@/components/ui";
import { WebhookPanel } from "@/components/webhook-panel";
import {
  useAgents,
  useConnections,
  useDeleteConnection,
  usePatchDomainAssignees,
  useProjectSquads,
  useSquads,
} from "@/lib/queries";
import type { Agent, Domain, Squad } from "@/lib/types";
import { cn } from "@/lib/utils";

type Picked = { type: "agent" | "squad"; id: string } | null;

function pickValue(p: Picked): string {
  return p ? `${p.type}:${p.id}` : "";
}

function parsePick(value: string): Picked {
  if (!value) return null;
  const [type, ...rest] = value.split(":");
  const id = rest.join(":");
  if ((type === "agent" || type === "squad") && id) return { type, id };
  return null;
}

function AgentSquadSelect({
  value,
  onChange,
  agents,
  squads,
}: {
  value: string;
  onChange: (value: string) => void;
  agents: Agent[];
  squads: Squad[];
}) {
  return (
    <select value={value} onChange={(event) => onChange(event.target.value)} className={inputCls}>
      <option value="">不自动处理</option>
      {agents.length > 0 && (
        <optgroup label="智能体">
          {agents.map((agent) => (
            <option key={agent.id} value={`agent:${agent.id}`}>
              {agent.name}
            </option>
          ))}
        </optgroup>
      )}
      {squads.length > 0 && (
        <optgroup label="小队">
          {squads.map((squad) => (
            <option key={squad.id} value={`squad:${squad.id}`}>
              {squad.name}
            </option>
          ))}
        </optgroup>
      )}
    </select>
  );
}

export function ProjectAccessTab({ project }: { project: Domain }) {
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: members } = useProjectSquads(project.id);
  const { data: connections } = useConnections();
  const patch = usePatchDomainAssignees();
  const removeConnection = useDeleteConnection();

  const [issue, setIssue] = useState(() => pickValue(parsePick(
    project.issue_assignee ? `${project.issue_assignee_type || "agent"}:${project.issue_assignee}` : ""
  )));
  const [pull, setPull] = useState(() => pickValue(parsePick(
    project.pull_assignee ? `${project.pull_assignee_type || "agent"}:${project.pull_assignee}` : ""
  )));
  const [saved, setSaved] = useState(false);

  const memberIds = new Set((members ?? []).map((member) => member.squad_id));
  const projectSquads = (squads ?? []).filter((squad) => memberIds.has(squad.id));
  const domainConnections = (connections ?? []).filter((connection) => connection.domain_id === project.id);
  const selectedIssue = parsePick(issue);
  const selectedPull = parsePick(pull);

  const save = () => {
    const body = {
      domainId: project.id,
      issue_assignee: selectedIssue?.id ?? "",
      issue_assignee_type: selectedIssue?.type ?? "agent",
      pull_assignee: selectedPull?.id ?? "",
      pull_assignee_type: selectedPull?.type ?? "agent",
    };
    patch.mutate(body, { onSuccess: () => setSaved(true) });
  };

  const migrateConnections = () => {
    if (!window.confirm("改为项目级派发会删除当前项目的小队仓库连接；仓库 webhook 需要重新指向项目接入地址。继续？")) return;
    void (async () => {
      for (const connection of domainConnections) {
        await removeConnection.mutateAsync(connection.id);
      }
      save();
    })();
  };

  return (
    <div className="space-y-4">
      <SectionCard
        title="事件派发"
        desc="仓库里的 Issue 和 PR 可以自动生成任务。这里决定生成的任务交给谁执行。"
        bodyClassName="p-4"
      >
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="Issue 执行者" hint="新建的 issue 自动派给谁；也可以不自动处理">
            <AgentSquadSelect value={issue} onChange={(value) => { setIssue(value); setSaved(false); }} agents={agents ?? []} squads={projectSquads} />
          </Field>
          <Field label="PR 执行者" hint="打开的 PR 自动派给谁；也可以不自动处理">
            <AgentSquadSelect value={pull} onChange={(value) => { setPull(value); setSaved(false); }} agents={agents ?? []} squads={projectSquads} />
          </Field>
        </div>
        <div className="mt-4 flex items-center gap-3">
          <Button onClick={save} disabled={patch.isPending || domainConnections.length > 0}>
            {patch.isPending ? "保存中…" : "保存派发规则"}
          </Button>
          {saved && <span className="text-xs text-emerald-300">已保存</span>}
          {patch.error instanceof Error && <span className="text-xs text-red-300">{patch.error.message}</span>}
        </div>
        {!selectedIssue && !selectedPull && (
          <p className="mt-2 text-[11px] leading-relaxed text-ink-3">
            两项都不选表示只记录 webhook 投递，不自动创建任务。
          </p>
        )}
        <p className="mt-2 text-[11px] leading-relaxed text-ink-3">
          只有加入本项目的小队才会出现在“小队”分组；智能体是公共资源，可以直接选中。
        </p>
        {!project.git_credentials && (
          <p className="mt-1 text-[11px] leading-relaxed text-amber-300/90">
            这个项目还没有平台令牌：webhook 仍可实时创建任务，但自动轮询不会运行；需要时在右上角「配置」里补令牌。
          </p>
        )}
      </SectionCard>

      {domainConnections.length > 0 && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4">
          <div className="flex items-start gap-2.5">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-amber-200">这个项目还有 {domainConnections.length} 条小队级仓库连接</p>
              <p className="mt-1 text-[11px] leading-relaxed text-ink-3">
                小队级连接会接管 Issue/PR 路由，项目级派发不会生效。改为项目级会删除这些连接；仓库 webhook 需要重新指向本页地址。
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <Button variant="outline" onClick={migrateConnections} disabled={removeConnection.isPending || patch.isPending}>
                  {removeConnection.isPending ? "正在迁移…" : "改为项目级派发"}
                </Button>
                <span className="font-mono text-[10px] text-ink-3">
                  {domainConnections.map((connection) => connection.repo).join(" · ")}
                </span>
              </div>
            </div>
          </div>
        </div>
      )}

      <SectionCard title="仓库接入" desc="把仓库的 Issue/PR 事件送到这个项目；不需要自动接活的项目可以忽略。" bodyClassName="p-4">
        <div className="mb-4 grid gap-3 sm:grid-cols-2">
          <div className="rounded-md border border-line bg-surface-2/30 px-3 py-2">
            <div className="mb-1 flex items-center gap-1.5 text-[11px] text-ink-3"><GitBranch className="h-3 w-3" /> 仓库</div>
            <div className="break-all font-mono text-[12px] text-ink-2">{project.issue_repo || "—"}</div>
          </div>
          <div className="rounded-md border border-line bg-surface-2/30 px-3 py-2">
            <div className="mb-1 flex items-center gap-1.5 text-[11px] text-ink-3"><Radio className="h-3 w-3" /> 平台</div>
            <div className="text-sm text-ink-2">{project.issue_provider || "github"}</div>
          </div>
        </div>
        <WebhookPanel domainId={project.id} provider={project.issue_provider || "github"} repo={project.issue_repo} />
      </SectionCard>

      <SectionCard title="派发范围" bodyClassName="p-4">
        <div className="flex items-start gap-2.5 text-[12px] leading-relaxed text-ink-3">
          <Users className="mt-0.5 h-4 w-4 shrink-0 text-ink-3" />
          <p>
            当前项目有 {projectSquads.length} 支小队、{agents?.length ?? 0} 个可选智能体。小队是路由层，事件最终仍由选定的小队 leader 或智能体执行。
          </p>
        </div>
      </SectionCard>
    </div>
  );
}
