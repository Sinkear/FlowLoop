"use client";

import { useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  ArrowLeft, ArrowRight, CalendarClock, Check, Copy, FolderGit2, GitBranch, Plus, Settings2, Users,
} from "lucide-react";
import {
  useConnections, useDomain, useGoals, useSchedules, useSquads,
} from "@/lib/queries";
import {
  Badge, Button, Empty, PageBody, PageHeader, SectionCard, Skeleton, StatStrip,
} from "@/components/ui";
import { ProjectForm } from "@/components/project-form";
import { cn, relTime } from "@/lib/utils";
import type { Goal, Schedule, Squad } from "@/lib/types";

const TABS = ["概览", "任务", "定时任务", "小队", "设置"] as const;
type Tab = (typeof TABS)[number];

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
  const { data: project, isLoading } = useDomain(id);
  const { data: goals } = useGoals();
  const { data: schedules } = useSchedules();
  const { data: squads } = useSquads();
  const { data: connections } = useConnections();
  const [tab, setTab] = useState<Tab>("概览");
  const [editing, setEditing] = useState(false);
  const [copied, setCopied] = useState(false);

  const projectGoals = (goals ?? [])
    .filter((g) => g.domain_id === id)
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
  const projectSchedules = (schedules ?? []).filter((s) => s.domain_id === id);
  const activeGoals = projectGoals.filter((g) => g.status === "active").length;
  const squadIds = new Set((connections ?? []).filter((c) => c.domain_id === id).map((c) => c.squad_id));
  if (project?.issue_assignee_type === "squad" && project.issue_assignee) squadIds.add(project.issue_assignee);
  if (project?.pull_assignee_type === "squad" && project.pull_assignee) squadIds.add(project.pull_assignee);
  const projectSquads = (squads ?? []).filter((s) => squadIds.has(s.id));

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
              <Link
                href={`/goals/new?project=${project.id}`}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
              >
                <Plus className="h-3.5 w-3.5" />
                新建任务
              </Link>
            </div>
          }
        />
      </div>

      <StatStrip
        items={[
          { label: "任务", value: projectGoals.length, hint: `${activeGoals} 个进行中` },
          { label: "定时任务", value: projectSchedules.length, hint: projectSchedules.filter((s) => s.enabled).length + " 个启用中" },
          { label: "服务小队", value: projectSquads.length, hint: projectSquads.length ? "已接入本项目" : "还没有小队接入" },
          { label: "近 7 天完成", value: projectGoals.filter((g) => g.status === "done" && withinDays(g.created_at, 7)).length, hint: "按任务创建时间统计" },
        ]}
      />

      <div className="flex items-center gap-1 border-b border-line pb-px">
        {TABS.map((item) => (
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
            <SectionCard title="最近任务" count={projectGoals.length} action={<Link href={`/goals?project=${project.id}`} className="text-xs text-ink-3 hover:text-accent">任务板</Link>} bodyClassName="p-0">
              {projectGoals.length === 0 ? (
                <Empty icon={<Check className="h-5 w-5" />}>
                  <span>这个项目还没有任务。派第一条活，让它跑起来。</span>
                  <span className="mt-4 block">
                    <Link href={`/goals/new?project=${project.id}`}>
                      <Button className="gap-1.5"><Plus className="h-3.5 w-3.5" />新建任务</Button>
                    </Link>
                  </span>
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

            <SectionCard title="定时任务" count={projectSchedules.length} action={<Link href="/schedules" className="text-xs text-ink-3 hover:text-accent">管理</Link>} bodyClassName="p-0">
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
            <SectionCard title="仓库与验收">
              <dl className="space-y-3 text-sm">
                <Row label="类型" value={project.type === "scratch" ? "无仓库工作区" : "代码仓库"} />
                {project.type !== "scratch" && (
                  <>
                    <div>
                      <dt className="mb-1 text-[11px] text-ink-3">仓库</dt>
                      <dd className="flex items-center gap-1.5">
                        <button type="button" onClick={copy} className="min-w-0 truncate font-mono text-[12px] text-ink-2 hover:text-ink">
                          {repoShort(project.git_url)}
                        </button>
                        {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3 text-ink-3" />}
                      </dd>
                    </div>
                    <Row label="默认分支" value={project.default_branch || "main"} icon={<GitBranch className="h-3 w-3" />} />
                  </>
                )}
                <Row label="验收" value={ACCEPTANCE_LABEL[project.acceptance_level] ?? "待定"} />
                <Row label="创建于" value={relTime(project.created_at) || "—"} />
                {project.scratch_dir ? <Row label="项目目录" value={project.scratch_dir} mono /> : null}
              </dl>
            </SectionCard>

            <SectionCard title="服务小队" count={projectSquads.length} bodyClassName="p-0">
              {projectSquads.length === 0 ? (
                <div className="flex items-center gap-2 px-4 py-5 text-[12px] text-ink-3">
                  <Users className="h-4 w-4 shrink-0" /> 还没有小队接入这个项目。
                </div>
              ) : (
                <div className="divide-y divide-line">
                  {projectSquads.map((squad) => (
                    <Link key={squad.id} href={`/squads/${squad.id}`} className="flex items-center gap-2 px-4 py-2.5 hover:bg-surface-2/50">
                      <Users className="h-3.5 w-3.5 text-ink-3" />
                      <span className="min-w-0 flex-1 truncate text-sm text-ink">{squad.name}</span>
                      <ArrowRight className="h-3.5 w-3.5 text-ink-3" />
                    </Link>
                  ))}
                </div>
              )}
            </SectionCard>
          </div>
        </div>
      )}

      {tab === "任务" && <GoalsTab projectId={project.id} goals={projectGoals} />}
      {tab === "定时任务" && <SchedulesTab schedules={projectSchedules} />}
      {tab === "小队" && <SquadsTab squads={projectSquads} />}
      {tab === "设置" && (
        <SectionCard title="项目设置" desc="仓库、验收级别、Issue / PR 自动处理都在这里配置。">
          <div className="flex flex-wrap items-center gap-3">
            <Button onClick={() => setEditing(true)} className="gap-1.5">
              <Settings2 className="h-3.5 w-3.5" /> 编辑项目配置
            </Button>
            <span className="text-[12px] text-ink-3">删除项目请回项目列表操作；有任务或定时任务时会被拒绝。</span>
          </div>
        </SectionCard>
      )}

      {editing && <ProjectForm project={project} onClose={() => setEditing(false)} />}
    </PageBody>
  );
}

function GoalsTab({ projectId, goals }: { projectId: string; goals: Goal[] }) {
  return (
    <SectionCard
      title="任务"
      count={goals.length}
      desc="按项目过滤后的任务列表；状态和负责人沿用任务板。"
      action={<Link href={`/goals?project=${projectId}`} className="text-xs text-ink-3 hover:text-accent">在任务板打开</Link>}
      bodyClassName="p-0"
    >
      {goals.length === 0 ? (
        <Empty icon={<Check className="h-5 w-5" />}>
          <span>这个项目还没有任务。</span>
          <span className="mt-4 block">
            <Link href={`/goals/new?project=${projectId}`}>
              <Button className="gap-1.5"><Plus className="h-3.5 w-3.5" />新建任务</Button>
            </Link>
          </span>
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

function SchedulesTab({ schedules }: { schedules: Schedule[] }) {
  return (
    <SectionCard title="定时任务" count={schedules.length} action={<Link href="/schedules" className="text-xs text-ink-3 hover:text-accent">管理定时任务</Link>} bodyClassName="p-0">
      {schedules.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-ink-3">没有定时任务。到定时任务页新建，选择这个项目即可。</div>
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
  );
}

function SquadsTab({ squads }: { squads: Squad[] }) {
  return (
    <SectionCard title="服务小队" count={squads.length} desc="通过仓库接入或 Issue / PR 自动处理绑定到本项目的小队。" bodyClassName="p-0">
      {squads.length === 0 ? (
        <div className="px-4 py-6 text-[12px] text-ink-3">还没有小队接入。到小队详情里配置仓库连接，或让小隊在任务里被选为负责人。</div>
      ) : (
        <div className="divide-y divide-line">
          {squads.map((squad) => (
            <Link key={squad.id} href={`/squads/${squad.id}`} className="flex items-center gap-3 px-4 py-3 hover:bg-surface-2/50">
              <Users className="h-4 w-4 text-ink-3" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-ink">{squad.name}</div>
                <div className="mt-0.5 line-clamp-1 text-[11px] text-ink-3">{squad.description || "没有描述"}</div>
              </div>
              <ArrowRight className="h-3.5 w-3.5 text-ink-3" />
            </Link>
          ))}
        </div>
      )}
    </SectionCard>
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
