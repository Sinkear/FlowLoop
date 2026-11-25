"use client";

import { useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  ArrowLeft, ArrowRight, CalendarClock, Check, Copy, FolderGit2, GitBranch, Plus, Settings2, Users,
} from "lucide-react";
import {
  useAddProjectSquad, useConnections, useDomain, useGoals, useProjectSquads, useRemoveProjectSquad, useSchedules, useSquads,
} from "@/lib/queries";
import {
  Badge, Button, Dialog, Empty, Field, PageBody, PageHeader, SectionCard, Skeleton, StatStrip, inputCls,
} from "@/components/ui";
import { ProjectForm } from "@/components/project-form";
import { ScheduleCreateForm } from "@/components/schedule-create-form";
import { ProjectAccessTab } from "@/components/projects/project-access-tab";
import { useCreateDialog } from "@/components/create/create-dialog";
import { cn, relTime } from "@/lib/utils";
import type { Domain, Goal, Schedule, Squad } from "@/lib/types";

const TABS = ["概览", "小队", "任务", "定时任务"] as const;
type Tab = (typeof TABS)[number] | "接入";

const ACCEPTANCE_LABEL: Record<string, string> = {
  hitl: "HITL 人工验收",
  hootl: "HOOTL 自动验收",
  "": "验收级别待定",
};

function repoShort(url: string) {
  return url.replace(/^https?:\/\//i, "").replace(/\.git$/i, "");
}

export default function ProjectDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const { open } = useCreateDialog();
  const { data: project, isLoading } = useDomain(id);
  const { data: goals } = useGoals();
  const { data: schedules } = useSchedules();
  const { data: squads } = useSquads();
  const { data: squadsInProject } = useProjectSquads(id);
  const { data: connections } = useConnections();
  const [tab, setTab] = useState<Tab>("概览");
  const [editing, setEditing] = useState(false);
  const [addingSquad, setAddingSquad] = useState(false);
  const [copied, setCopied] = useState(false);

  const projectGoals = (goals ?? [])
    .filter((g) => g.domain_id === id)
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
  const projectSchedules = (schedules ?? []).filter((s) => s.domain_id === id);
  const activeGoals = projectGoals.filter((g) => g.status === "active").length;
  // Membership is the collaboration relation. A repo connection only adds an
  // optional "仓库已接入" marker; it is not required to join a project.
  const memberSquadIds = new Set((squadsInProject ?? []).map((m) => m.squad_id));
  if (project?.issue_assignee_type === "squad" && project.issue_assignee) memberSquadIds.add(project.issue_assignee);
  if (project?.pull_assignee_type === "squad" && project.pull_assignee) memberSquadIds.add(project.pull_assignee);
  const projectSquads = (squads ?? []).filter((s) => memberSquadIds.has(s.id));
  const connectedSquadIds = new Set((connections ?? []).filter((c) => c.domain_id === id).map((c) => c.squad_id));

  const copy = async () => {
    if (!project?.git_url) return;
    try {
      await navigator.clipboard.writeText(project.git_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable — URL remains selectable
    }
  };

  if (isLoading) {
    return (
      <PageBody>
        <Skeleton className="h-8 w-64" />
        <div className="mt-4 grid gap-4 md:grid-cols-2">
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      </PageBody>
    );
  }

  if (!project) {
    return (
      <PageBody>
        <Empty icon={<FolderGit2 className="h-5 w-5" />}>
          <span>项目不存在，可能已经被删除。</span>
          <span className="mt-4 block">
            <Link href="/projects">
              <Button variant="outline">返回项目列表</Button>
            </Link>
          </span>
        </Empty>
      </PageBody>
    );
  }

  return (
    <PageBody className="space-y-5">
      <div>
        <Link href="/projects" className="inline-flex items-center gap-1 text-xs text-ink-3 hover:text-accent">
          <ArrowLeft className="h-3.5 w-3.5" /> 全部项目
        </Link>
        <PageHeader
          title={project.name}
          desc={`${project.type === "scratch" ? "无仓库工作区" : repoShort(project.git_url)} · ${ACCEPTANCE_LABEL[project.acceptance_level] ?? "验收待定"}`}
          action={
            <div className="flex items-center gap-2">
              <Button variant="outline" onClick={() => setEditing(true)} className="gap-1.5">
                <Settings2 className="h-3.5 w-3.5" />
                配置
              </Button>
              <Button onClick={() => open("task", { projectId: project.id })} className="gap-1.5">
                <Plus className="h-3.5 w-3.5" />
                新建任务
              </Button>
            </div>
          }
        />
      </div>

      <StatStrip
        items={[
          { label: "任务", value: projectGoals.length, hint: `${activeGoals} 个进行中` },
          { label: "定时任务", value: projectSchedules.length, hint: projectSchedules.filter((s) => s.enabled).length + " 个启用中" },
          { label: "服务小队", value: projectSquads.length, hint: projectSquads.length ? "已加入本项目" : "还没有小队加入" },
          { label: "近 7 天完成", value: projectGoals.filter((g) => g.status === "done" && withinDays(g.created_at, 7)).length, hint: "按任务创建时间统计" },
        ]}
      />

      <div className="flex items-center gap-1 border-b border-line pb-px">
        {[...TABS, ...(project.issue_repo ? (["接入"] as const) : [])].map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setTab(item)}
            className={cn(
              "relative px-3 py-2 text-sm transition-colors",
              tab === item ? "text-ink" : "text-ink-3 hover:text-ink-2"
            )}
          >
            {item}
            {tab === item && <span className="absolute -bottom-px left-2 right-2 h-0.5 rounded-full bg-accent" />}
          </button>
        ))}
      </div>

      {tab === "概览" && (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_360px]">
          <div className="space-y-4">
            <SectionCard title="最近任务" count={projectGoals.length} action={<Link href={`/goals?project=${project.id}`} className="text-xs text-ink-3 hover:text-accent">查看全部任务</Link>} bodyClassName="p-0">
              {projectGoals.length === 0 ? (
                <Empty icon={<Check className="h-5 w-5" />}>
                  这个项目还没有任务。用右上角「新建任务」派第一条活。
                </Empty>
              ) : (
                <div className="divide-y divide-line">
                  {projectGoals.slice(0, 6).map((goal) => (
                    <Link key={goal.id} href={`/goals/${goal.id}`} className="flex items-center gap-3 px-4 py-2.5 hover:bg-surface-2/50">
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm text-ink">{goal.title}</div>
                        <div className="mt-0.5 text-[11px] text-ink-3">
                          {goal.execution_mode === "train" ? "开启复盘" : "未开复盘"}
                          {goal.blueprint_version_id ? " · 使用工作流" : " · 自动拆解"} · {relTime(goal.created_at)}
                        </div>
                      </div>
                      <Badge status={goal.status} />
                    </Link>
                  ))}
                </div>
              )}
            </SectionCard>

            <SectionCard title="定时任务" count={projectSchedules.length} action={<Link href="/schedules" className="text-xs text-ink-3 hover:text-accent">查看定时任务</Link>} bodyClassName="p-0">
              {projectSchedules.length === 0 ? (
                <div className="px-4 py-5 text-[12px] text-ink-3">没有定时任务。重复的活可以配成 cron，省掉每天手动派活。</div>
              ) : (
                <div className="divide-y divide-line">
                  {projectSchedules.map((schedule) => (
                    <Link key={schedule.id} href="/schedules" className="flex items-center gap-3 px-4 py-2.5 hover:bg-surface-2/50">
                      <CalendarClock className="h-3.5 w-3.5 text-ink-3" />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm text-ink">{schedule.name}</div>
                        <div className="mt-0.5 text-[11px] text-ink-3">{schedule.cron_expression} · {schedule.timezone}</div>
                      </div>
                      <span className={cn("text-[11px]", schedule.enabled ? "text-emerald-300" : "text-ink-3")}>
                        {schedule.enabled ? "启用" : "停用"}
                      </span>
                    </Link>
                  ))}
                </div>
              )}
            </SectionCard>
          </div>

          <div className="space-y-4">
            <SectionCard
              title="仓库与验收"
              bodyClassName="p-4"
              action={project.issue_repo ? (
                <button type="button" onClick={() => setTab("接入")} className="text-xs text-ink-3 hover:text-accent">
                  接入设置
                </button>
              ) : undefined}
            >
              <dl className="grid gap-x-6 gap-y-4 text-sm sm:grid-cols-2">
                <div>
                  <dt className="mb-1 text-[11px] text-ink-3">类型</dt>
                  <dd className="text-sm text-ink-2">{project.type === "scratch" ? "无仓库工作区" : "代码仓库"}</dd>
                </div>
                <div>
                  <dt className="mb-1 text-[11px] text-ink-3">验收</dt>
                  <dd className="text-sm text-ink-2">{ACCEPTANCE_LABEL[project.acceptance_level] ?? "待定"}</dd>
                </div>
                {project.type !== "scratch" && (
                  <div className="sm:col-span-2">
                    <dt className="mb-1.5 text-[11px] text-ink-3">仓库</dt>
                    <dd className="flex items-start gap-2 rounded-md border border-line/70 bg-surface-2/30 px-3 py-2">
                      <button
                        type="button"
                        onClick={copy}
                        title="点击复制仓库地址"
                        className="min-w-0 flex-1 break-all text-left font-mono text-[12px] leading-relaxed text-ink-2 hover:text-ink"
                      >
                        {project.git_url.replace(/^https?:\/\//i, "").replace(/\.git$/i, "")}
                      </button>
                      {copied ? <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-400" /> : <Copy className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />}
                    </dd>
                    <p className="mt-1 text-[10px] text-ink-3">点击地址可复制</p>
                  </div>
                )}
                {project.type !== "scratch" && (
                  <div>
                    <dt className="mb-1 text-[11px] text-ink-3">默认分支</dt>
                    <dd className="inline-flex items-center gap-1.5 font-mono text-[12px] text-ink-2">
                      <GitBranch className="h-3 w-3 text-ink-3" />
                      {project.default_branch || "main"}
                    </dd>
                  </div>
                )}
                <div>
                  <dt className="mb-1 text-[11px] text-ink-3">创建于</dt>
                  <dd className="text-sm text-ink-2">{relTime(project.created_at) || "—"}</dd>
                </div>
                {project.scratch_dir ? (
                  <div className="sm:col-span-2">
                    <dt className="mb-1 text-[11px] text-ink-3">项目目录</dt>
                    <dd className="break-all font-mono text-[12px] text-ink-2">{project.scratch_dir}</dd>
                  </div>
                ) : null}
              </dl>
            </SectionCard>

            <SectionCard title="服务小队" count={projectSquads.length} bodyClassName="p-0">
              {projectSquads.length === 0 ? (
                <div className="flex items-center gap-2 px-4 py-5 text-[12px] text-ink-3">
                  <Users className="h-4 w-4 shrink-0" /> 还没有小队加入这个项目。
                </div>
              ) : (
                <div className="divide-y divide-line">
                  {projectSquads.map((squad) => (
                    <Link key={squad.id} href={`/squads/${squad.id}`} className="flex items-center gap-2 px-4 py-2.5 hover:bg-surface-2/50">
                      <Users className="h-3.5 w-3.5 text-ink-3" />
                      <span className="min-w-0 flex-1 truncate text-sm text-ink">{squad.name}</span>
                      <span className={cn(
                        "shrink-0 rounded-full border px-2 py-0.5 text-[10px]",
                        connectedSquadIds.has(squad.id) ? "border-emerald-500/30 text-emerald-300" : "border-line text-ink-3",
                      )}>
                        {connectedSquadIds.has(squad.id) ? "仓库已接入" : "仅协作"}
                      </span>
                      <ArrowRight className="h-3.5 w-3.5 text-ink-3" />
                    </Link>
                  ))}
                </div>
              )}
            </SectionCard>
          </div>
        </div>
      )}

      {tab === "小队" && (
        <SquadsTab
          squads={projectSquads}
          project={project}
          connectedSquadIds={connectedSquadIds}
          canIntegrate={!!project.issue_repo}
          onAdd={() => setAddingSquad(true)}
          onOpenAccess={() => (project.issue_repo ? setTab("接入") : setEditing(true))}
        />
      )}

      {tab === "任务" && <GoalsTab projectId={project.id} goals={projectGoals} />}
      {tab === "定时任务" && <SchedulesTab projectId={project.id} schedules={projectSchedules} />}
      {tab === "接入" && project.issue_repo && <ProjectAccessTab project={project} />}

      {editing && <ProjectForm project={project} onClose={() => setEditing(false)} />}
      {addingSquad && (
        <AddSquadToProjectDialog
          project={project}
          squads={squads ?? []}
          memberSquadIds={memberSquadIds}
          onClose={() => setAddingSquad(false)}
        />
      )}
    </PageBody>
  );
}

function GoalsTab({ projectId, goals }: { projectId: string; goals: Goal[] }) {
  const { open } = useCreateDialog();
  return (
    <SectionCard
      title="任务"
      count={goals.length}
      desc="按项目过滤后的任务列表；状态和负责人沿用任务板。"
      action={<Link href={`/goals?project=${projectId}`} className="text-xs text-ink-3 hover:text-accent">查看全部任务</Link>}
      bodyClassName="p-0"
    >
      {goals.length === 0 ? (
        <Empty icon={<Check className="h-5 w-5" />}>
          这个项目还没有任务。用右上角「新建任务」派第一条活。
        </Empty>
      ) : (
        <div className="divide-y divide-line">
          {goals.map((goal) => (
            <Link key={goal.id} href={`/goals/${goal.id}`} className="flex items-center gap-3 px-4 py-3 hover:bg-surface-2/50">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-ink">{goal.title}</div>
                <div className="mt-0.5 text-[11px] text-ink-3">
                  {goal.execution_mode === "train" ? "开启复盘" : "未开复盘"} · {goal.blueprint_version_id ? "使用工作流" : "自动拆解"} · {relTime(goal.created_at)}
                </div>
              </div>
              <Badge status={goal.status} />
            </Link>
          ))}
        </div>
      )}
    </SectionCard>
  );
}

function SchedulesTab({ projectId, schedules }: { projectId: string; schedules: Schedule[] }) {
  const [creating, setCreating] = useState(false);
  return (
    <>
    <SectionCard
      title="定时任务"
      count={schedules.length}
      action={
        <Button variant="outline" onClick={() => setCreating(true)} className="shrink-0 gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          新建定时任务
        </Button>
      }
      bodyClassName="p-0"
    >
      {schedules.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-ink-3">这个项目还没有定时任务。点右上角「新建定时任务」，项目会自动选中。</div>
      ) : (
        <div className="divide-y divide-line">
          {schedules.map((schedule) => (
            <div key={schedule.id} className="flex items-center gap-3 px-4 py-3">
              <CalendarClock className="h-3.5 w-3.5 text-ink-3" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-ink">{schedule.name}</div>
                <div className="mt-0.5 text-[11px] text-ink-3">
                  {schedule.cron_expression} · {schedule.timezone} · 下一次 {schedule.next_run_at ? untilTime(schedule.next_run_at) : "未排期"}
                </div>
              </div>
              <Badge status={schedule.enabled ? "active" : "cancelled"} />
            </div>
          ))}
        </div>
      )}
    </SectionCard>
    {creating && <ScheduleCreateForm initialProjectId={projectId} onClose={() => setCreating(false)} />}
    </>
  );
}

function SquadsTab({
  squads,
  project,
  connectedSquadIds,
  canIntegrate,
  onAdd,
  onOpenAccess,
}: {
  squads: Squad[];
  project: Domain;
  connectedSquadIds: Set<string>;
  canIntegrate: boolean;
  onAdd: () => void;
  onOpenAccess: () => void;
}) {
  const remove = useRemoveProjectSquad();
  const leave = (squad: Squad) => {
    if (!window.confirm(`把「${squad.name}」移出本项目？已有仓库连接时需要先在项目的「接入」页解除。`)) return;
    remove.mutate({ domainId: project.id, squadId: squad.id });
  };
  return (
    <SectionCard
      title="小队"
      count={squads.length}
      desc="加入项目的小队可以在这个项目下接任务。仓库事件接入是可选的，只影响 Issue/PR 自动派活。"
      action={
        <Button variant="outline" onClick={onAdd} className="gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          加入小队
        </Button>
      }
      bodyClassName="p-0"
    >
      {squads.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-ink-3">
          还没有小队加入本项目。点右上角「加入小队」把已有小队接进来；不需要配置仓库。
        </div>
      ) : (
        <div className="divide-y divide-line">
          {squads.map((squad) => {
            const connected = connectedSquadIds.has(squad.id);
            return (
              <div key={squad.id} className="flex items-center gap-3 px-4 py-3 hover:bg-surface-2/50">
                <Link href={`/squads/${squad.id}`} className="min-w-0 flex flex-1 items-center gap-3">
                  <Users className="h-4 w-4 text-ink-3" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm text-ink">{squad.name}</div>
                    <div className="mt-0.5 line-clamp-1 text-[11px] text-ink-3">{squad.description || "没有描述"}</div>
                  </div>
                </Link>
                <span className={cn(
                  "shrink-0 rounded-full border px-2 py-0.5 text-[10px]",
                  connected ? "border-emerald-500/30 text-emerald-300" : "border-line text-ink-3",
                )}>
                  {connected ? "仓库已接入" : "仅协作"}
                </span>
                <button
                  type="button"
                  onClick={onOpenAccess}
                  className="shrink-0 text-[11px] text-ink-3 hover:text-accent"
                >
                  {!canIntegrate ? "配置仓库" : connected ? "管理接入" : "配置接入"}
                </button>
                <button
                  type="button"
                  onClick={() => leave(squad)}
                  disabled={remove.isPending && remove.variables?.squadId === squad.id}
                  className="shrink-0 text-[11px] text-ink-3 transition-colors hover:text-red-300 disabled:opacity-40"
                >
                  移出
                </button>
              </div>
            );
          })}
        </div>
      )}
      {remove.isError && (
        <p className="border-t border-line px-4 py-2 text-[11px] text-red-300">{String(remove.error)}</p>
      )}
      {project.type === "scratch" && (
        <div className="border-t border-line px-4 py-2.5 text-[11px] text-ink-3">
          这是无仓库项目；小队加入与代码仓库无关。
        </div>
      )}
    </SectionCard>
  );
}

function AddSquadToProjectDialog({
  project,
  squads,
  memberSquadIds,
  onClose,
}: {
  project: Domain;
  squads: Squad[];
  memberSquadIds: Set<string>;
  onClose: () => void;
}) {
  const add = useAddProjectSquad();
  const eligible = squads.filter((squad) => !memberSquadIds.has(squad.id));
  const [squadId, setSquadId] = useState("");
  const [error, setError] = useState("");
  const selected = squadId || eligible[0]?.id || "";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!selected || add.isPending) return;
    setError("");
    add.mutate(
      { domainId: project.id, squadId: selected },
      {
        onSuccess: onClose,
        onError: (err) => setError(err instanceof Error ? err.message : String(err)),
      },
    );
  };

  return (
    <Dialog
      title="加入小队"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="add-squad-to-project" disabled={!selected || add.isPending}>
            {add.isPending ? "加入中…" : "加入项目"}
          </Button>
        </>
      }
    >
      <form id="add-squad-to-project" onSubmit={submit} className="space-y-4">
        <Field label="选择小队" hint="已有小队加入后，就能在这个项目下接任务。">
          {eligible.length === 0 ? (
            <div className="rounded-md border border-line bg-surface-2/40 px-3 py-2 text-xs text-ink-3">
              所有小队都已经加入本项目。可以先去小队页新建一支。
            </div>
          ) : (
            <select value={selected} onChange={(e) => setSquadId(e.target.value)} className={inputCls}>
              {eligible.map((squad) => (
                <option key={squad.id} value={squad.id}>{squad.name}</option>
              ))}
            </select>
          )}
        </Field>
        <p className="text-[11px] leading-relaxed text-ink-3">
          加入项目不需要配置仓库，也不会自动订阅 Issue/PR。需要自动接活时，再到小队「接入」里绑定仓库事件。
        </p>
        {project.issue_repo && (
          <p className="text-[11px] leading-relaxed text-ink-3">
            当前项目已配置仓库 <span className="font-mono">{project.issue_repo}</span>；Issue/PR 派发在项目的「接入」页配置。
          </p>
        )}
        {error && <p className="text-xs text-red-300">{error}</p>}
      </form>
    </Dialog>
  );
}

function Row({ label, value, mono, icon }: { label: string; value: string; mono?: boolean; icon?: React.ReactNode }) {
  return (
    <div>
      <dt className="mb-0.5 text-[11px] text-ink-3">{label}</dt>
      <dd className={cn("flex items-center gap-1.5 text-sm text-ink-2", mono && "break-all font-mono text-[12px]")}>
        {icon}
        {value || "—"}
      </dd>
    </div>
  );
}

function withinDays(iso: string, days: number) {
  const t = new Date(iso).getTime();
  return !Number.isNaN(t) && Date.now() - t <= days * 24 * 60 * 60 * 1000;
}

function untilTime(iso: string) {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = t - Date.now();
  if (diff <= 0) return "即将触发";
  const minutes = Math.round(diff / 60000);
  if (minutes < 60) return `${minutes} 分钟后`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时后`;
  return `${Math.round(hours / 24)} 天后`;
}
