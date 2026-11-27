"use client";

import Link from "next/link";
import { useMemo, useSyncExternalStore } from "react";
import {
  ArrowRight,
  Clock3,
  FolderGit2,
  ListChecks,
  Server,
  ShieldAlert,
  Sparkles,
  TriangleAlert,
} from "lucide-react";
import { useAgents, useDomains, useGoals, useMachines, usePendingGates, useRsiSessions, useSchedules, useSquads } from "@/lib/queries";
import { useCreateDialog } from "@/components/create/create-dialog";
import { Badge, Empty, PageBody, PageHeader, SectionCard, StatStrip } from "@/components/ui";
import { STATUS_LABEL } from "@/components/ui";
import { relTime } from "@/lib/utils";
import {
  attentionKeyOfGate,
  attentionKeyOfGoal,
  attentionSeenVersion,
  markAttentionSeen,
  subscribeAttentionSeen,
  unseenAttentionCount,
} from "@/lib/attention-seen";
import type { Goal, PendingGate, RsiSession } from "@/lib/types";

function until(iso: string): string {
  if (!iso) return "未排期";
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

export default function WorkbenchPage() {
  const { open } = useCreateDialog();
  const { data: goals } = useGoals();
  const { data: pendingGates } = usePendingGates();
  const { data: domains } = useDomains();
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const { data: schedules } = useSchedules();
  const { data: sessions } = useRsiSessions();
  const { data: machines } = useMachines();

  const projects = domains ?? [];
  const allGoals = goals ?? [];
  const allSessions = sessions ?? [];

  const projectName = useMemo(() => new Map(projects.map((d) => [d.id, d.name])), [projects]);
  const ownerName = useMemo(() => {
    const map = new Map<string, string>();
    (squads ?? []).forEach((s) => map.set(`squad:${s.id}`, s.name));
    (agents ?? []).forEach((a) => map.set(`agent:${a.id}`, a.name));
    return map;
  }, [squads, agents]);
  const sessionByGoal = useMemo(() => new Map(allSessions.map((s) => [s.goal_id, s])), [allSessions]);

  const attention = allGoals.filter((g) => g.status === "review" || g.status === "failed" || g.attention);
  // 工作流人工门不改变 goal.status：只数 review/failed/attention 会让
  // 「流程停在 gate 上等人」完全隐形。人工门与待审批合并成「待我处理」。
  const heldGates = pendingGates?.gates ?? [];
  const attentionCount = attention.length + heldGates.length;
  // 红点走 app 式通知语义：徽标 = 新出现且还没看过的条数，点开（或点清单）
  // 即视为已读、红点消失；清单本身一直保留到处理完成。账本见 lib/attention-seen.ts。
  const attentionKeys = useMemo(
    () => [
      ...heldGates.map(attentionKeyOfGate),
      ...attention.map(attentionKeyOfGoal),
    ],
    [heldGates, attention]
  );
  useSyncExternalStore(subscribeAttentionSeen, attentionSeenVersion, attentionSeenVersion);
  const unseenCount = unseenAttentionCount(attentionKeys);
  const clearAttentionAlert = () => markAttentionSeen(attentionKeys);
  const active = allGoals.filter((g) => g.status === "active");
  const done = allGoals
    .filter((g) => g.status === "done")
    .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
    .slice(0, 5);
  const readyReviews = allSessions.filter((s) => s.status === "ready");
  const upcoming = (schedules ?? [])
    .filter((s) => s.enabled && s.next_run_at)
    .sort((a, b) => new Date(a.next_run_at).getTime() - new Date(b.next_run_at).getTime())
    .slice(0, 4);
  const activeSchedules = (schedules ?? []).filter((s) => s.enabled).length;

  const showOnboarding = projects.length === 0;
  const machineCount = (machines ?? []).length;

  const ownerOf = (goal: Goal) => ownerName.get(`${goal.assignee_type}:${goal.assignee_id}`) || "未指派";

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="工作台"
        desc="先看需要你拍板的事，再看正在跑的活；项目和定时任务都在下面。"
      />

      {showOnboarding ? (
        <SectionCard title="三步开始" desc="不用先配置一堆东西，先让一条任务跑起来。">
          <div className="grid gap-3 md:grid-cols-3">
            <Step
              n="1"
              title="连接运行环境"
              desc={machineCount > 0 ? `已有 ${machineCount} 台机器，运行环境就绪。` : "装好 flowloop 后，机器会出现在运行环境里。"}
              href="/infra"
              action={machineCount > 0 ? "查看运行环境" : "去连机器"}
            />
            <Step
              n="2"
              title="新建第一个项目"
              desc="挂上代码仓库或无仓库工作区；任务必须落在项目下。"
              onClick={() => open("project")}
              action="新建项目"
            />
            <Step
              n="3"
              title="新建第一条任务"
              desc="选负责人，决定用不用工作流，以及这次要不要复盘。"
              onClick={() => open("task")}
              action="新建任务"
            />
          </div>
          <div className="mt-3 rounded-md border border-line bg-surface-2/40 px-3 py-2 text-[12px] text-ink-3">
            也可以从实践库挑一套现成的项目 / 小队组合。
            <Link href="/templates" className="ml-2 inline-flex items-center gap-1 text-accent hover:text-accent-dim">
              打开实践库 <ArrowRight className="h-3 w-3" />
            </Link>
          </div>
        </SectionCard>
      ) : null}

      <StatStrip
        items={[
          {
            label: "待我处理",
            value: attentionCount,
            tone: attentionCount ? "info" : "muted",
            hint: attentionCount
              ? unseenCount
                ? `其中 ${unseenCount} 项是新提醒 —— 红点已在下方清单标题上，点击即清除`
                : "清单已看过：红点不亮，条目保留到处理完成"
              : "没有卡点",
          },
          {
            label: "进行中",
            value: active.length,
            tone: active.length ? "ok" : "muted",
            hint: active.length ? "负责人正在推进" : "现在没有在跑的任务",
          },
          {
            label: "复盘待看",
            value: readyReviews.length,
            tone: readyReviews.length ? "info" : "muted",
            hint: readyReviews.length ? "分析已完成，等你读" : "没有待看的复盘",
          },
          {
            label: "启用中的定时任务",
            value: activeSchedules,
            tone: activeSchedules ? "ok" : "muted",
            hint: upcoming[0] ? `下一次 ${until(upcoming[0].next_run_at)}` : "没有排期",
          },
        ]}
      />

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="space-y-4">
          <SectionCard
            title="待我处理"
            count={attentionCount}
            desc="工作流人工门、审批、失败和 attention 卡点集中在这里。"
            titleExtra={
              unseenCount > 0 ? (
                <button
                  type="button"
                  onClick={clearAttentionAlert}
                  title={`${unseenCount} 项新提醒 —— 点击清除红点（清单保留，逐条处理即可）`}
                  className="inline-flex h-5 min-w-5 shrink-0 items-center justify-center gap-1 rounded-full border border-red-400/40 bg-red-500/15 px-1.5 font-mono text-[10px] font-semibold leading-none text-red-300 tabular-nums transition-colors hover:bg-red-500/25"
                >
                  <span className="h-1.5 w-1.5 rounded-full bg-red-400" />
                  {unseenCount > 99 ? "99+" : unseenCount}
                </button>
              ) : null
            }
            action={
              <Link href="/goals" className="text-xs text-ink-3 hover:text-accent">
                全部任务 →
              </Link>
            }
            bodyClassName="p-0"
          >
            {attentionCount > 0 && (
              <div className="flex items-start gap-2 border-b border-line px-4 py-2 text-[11px] leading-relaxed text-ink-3">
                <span className="mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full bg-red-500/70" />
                <span>
                  {unseenCount > 0
                    ? `这批红点是“新提醒”计数：点标题上的红点或任意一条即清除；同一任务状态变化会再亮。清单保留到处理完成。`
                    : `下面 ${attentionCount} 项待处理：红点已清除，处理完一项少一项。`}
                </span>
              </div>
            )}
            {attentionCount === 0 ? (
              <Empty icon={<Sparkles className="h-5 w-5" />}>
                没有需要你处理的卡点。去项目里看看，或者给小队派一条新任务。
              </Empty>
            ) : (
              <div className="divide-y divide-line">
                {heldGates.slice(0, 4).map((gate) => (
                  <ApprovalGateRow
                    key={`${gate.goal_id}:${gate.node_key}`}
                    gate={gate}
                    project={projectName.get(gate.domain_id)}
                    onConsume={() => markAttentionSeen([attentionKeyOfGate(gate)])}
                  />
                ))}
                {attention.length > 0 && heldGates.length > 0 && (
                  <div className="px-4 py-1.5 text-[10px] uppercase tracking-[0.12em] text-ink-3">任务审批</div>
                )}
                {attention.slice(0, 6).map((goal) => (
                  <GoalRow
                    key={goal.id}
                    goal={goal}
                    project={projectName.get(goal.domain_id)}
                    owner={ownerOf(goal)}
                    session={sessionByGoal.get(goal.id)}
                    onConsume={() => markAttentionSeen([attentionKeyOfGoal(goal)])}
                  />
                ))}
              </div>
            )}
          </SectionCard>

          <SectionCard
            title="进行中"
            count={active.length}
            desc="负责人已经接活、还没到终态的任务。"
            bodyClassName="p-0"
          >
            {active.length === 0 ? (
              <Empty icon={<ListChecks className="h-5 w-5" />}>
                现在没有在跑的任务。空档期适合把重复的活配成定时任务。
              </Empty>
            ) : (
              <div className="divide-y divide-line">
                {active.slice(0, 6).map((goal) => (
                  <GoalRow
                    key={goal.id}
                    goal={goal}
                    project={projectName.get(goal.domain_id)}
                    owner={ownerOf(goal)}
                    session={sessionByGoal.get(goal.id)}
                  />
                ))}
              </div>
            )}
          </SectionCard>

          <SectionCard
            title="最近完成"
            count={done.length}
            desc="完成后复盘是否生成、有没有可看的发现。"
            bodyClassName="p-0"
          >
            {done.length === 0 ? (
              <Empty icon={<ListChecks className="h-5 w-5" />}>
                还没有完成的任务。第一条任务跑完后，这里会出现它的结果和复盘入口。
              </Empty>
            ) : (
              <div className="divide-y divide-line">
                {done.map((goal) => (
                  <GoalRow
                    key={goal.id}
                    goal={goal}
                    project={projectName.get(goal.domain_id)}
                    owner={ownerOf(goal)}
                    session={sessionByGoal.get(goal.id)}
                  />
                ))}
              </div>
            )}
          </SectionCard>
        </div>

        <div className="space-y-4">
          <SectionCard
            title="复盘待看"
            count={readyReviews.length}
            desc="分析已经跑完，读一下发现再决定改不改。"
            action={
              <Link href="/training" className="shrink-0 whitespace-nowrap text-xs text-ink-3 hover:text-accent">
                全部复盘
              </Link>
            }
            bodyClassName="p-0"
          >
            {readyReviews.length === 0 ? (
              <div className="px-4 py-5 text-[12px] text-ink-3">
                复盘只在任务开启复盘时生成；小队任务默认开启，自动拆解默认关闭。
              </div>
            ) : (
              <div className="divide-y divide-line">
                {readyReviews.slice(0, 4).map((session) => (
                  <ReviewRow key={session.id} session={session} projects={projectName} />
                ))}
              </div>
            )}
          </SectionCard>

          <SectionCard
            title="定时任务"
            count={activeSchedules}
            desc="下一次自动派活的时间。"
            action={
              <Link href="/schedules" className="text-xs text-ink-3 hover:text-accent">
                查看定时任务
              </Link>
            }
            bodyClassName="p-0"
          >
            {upcoming.length === 0 ? (
              <div className="flex items-center gap-2 px-4 py-5 text-[12px] text-ink-3">
                <Clock3 className="h-4 w-4 shrink-0" />
                没有启用中的定时任务。
              </div>
            ) : (
              <div className="divide-y divide-line">
                {upcoming.map((schedule) => (
                  <Link
                    key={schedule.id}
                    href="/schedules"
                    className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/50"
                  >
                    <Clock3 className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm text-ink">{schedule.name}</div>
                      <div className="truncate text-[11px] text-ink-3">
                        {projectName.get(schedule.domain_id) || "未知项目"} · {schedule.cron_expression} · {schedule.timezone}
                      </div>
                    </div>
                    <span className="shrink-0 font-mono text-[11px] text-ink-3">{until(schedule.next_run_at)}</span>
                  </Link>
                ))}
              </div>
            )}
          </SectionCard>

          <SectionCard
            title="我的项目"
            count={projects.length}
            desc="项目是任务的归属地。"
            action={
              <Link href="/projects" className="text-xs text-ink-3 hover:text-accent">
                查看项目
              </Link>
            }
            bodyClassName="p-0"
          >
            {projects.length === 0 ? (
              <Empty icon={<FolderGit2 className="h-5 w-5" />}>
                还没有项目。用左上角「新建」建一个，任务才有地方落地。
              </Empty>
            ) : (
              <div className="divide-y divide-line">
                {projects.slice(0, 4).map((project) => {
                  const goalCount = allGoals.filter((g) => g.domain_id === project.id).length;
                  const scheduleCount = (schedules ?? []).filter((s) => s.domain_id === project.id).length;
                  return (
                    <Link
                      key={project.id}
                      href={`/projects/${project.id}`}
                      className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/50"
                    >
                      <FolderGit2 className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm text-ink">{project.name}</div>
                        <div className="text-[11px] text-ink-3">
                          {goalCount} 个任务 · {scheduleCount} 个定时任务
                        </div>
                      </div>
                      <ArrowRight className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                    </Link>
                  );
                })}
              </div>
            )}
          </SectionCard>

          {!showOnboarding && machineCount === 0 && (
            <div className="flex items-start gap-2 rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2.5 text-[12px] text-amber-200/90">
              <Server className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>
                还没有连接机器，Agent 没有地方执行。先
                <Link href="/infra" className="mx-1 text-accent hover:underline">
                  连一台机器
                </Link>
                。
              </span>
            </div>
          )}

          <div className="flex items-start gap-2 rounded-lg border border-line bg-surface-2/30 px-3 py-2.5 text-[12px] text-ink-3">
            <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>复盘不是训练模型。它只读一次执行的实际图，指出卡点和可优化的流程。</span>
          </div>
        </div>
      </div>
    </PageBody>
  );
}

function Step({
  n,
  title,
  desc,
  href,
  action,
  onClick,
}: {
  n: string;
  title: string;
  desc: string;
  href?: string;
  action: string;
  onClick?: () => void;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface-1 p-3">
      <div className="mb-2 flex items-center gap-2">
        <span className="flex h-5 w-5 items-center justify-center rounded-full border border-accent/30 bg-accent/10 font-mono text-[11px] text-accent">
          {n}
        </span>
        <span className="text-sm font-medium text-ink">{title}</span>
      </div>
      <p className="min-h-10 text-[12px] leading-5 text-ink-3">{desc}</p>
      {onClick ? (
        <button
          type="button"
          onClick={onClick}
          className="mt-2 inline-flex items-center gap-1 text-[12px] text-accent hover:text-accent-dim"
        >
          {action} <ArrowRight className="h-3 w-3" />
        </button>
      ) : (
        <Link href={href ?? "/"} className="mt-2 inline-flex items-center gap-1 text-[12px] text-accent hover:text-accent-dim">
          {action} <ArrowRight className="h-3 w-3" />
        </Link>
      )}
    </div>
  );
}

function GoalRow({
  goal,
  project,
  owner,
  session,
  onConsume,
}: {
  goal: Goal;
  project?: string;
  owner: string;
  session?: RsiSession;
  onConsume?: () => void;
}) {
  return (
    <Link
      href={`/goals/${goal.id}`}
      onClick={onConsume}
      className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/50"
    >
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-ink">{goal.title}</div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-ink-3">
          <span className="truncate">{project || "未归属项目"}</span>
          <span>·</span>
          <span className="truncate">{owner}</span>
          {goal.review_phase ? (
            <>
              <span>·</span>
              <span className="text-purple-300/80">{goal.review_phase === "awaiting_approval" ? "待审批" : "审查中"}</span>
            </>
          ) : null}
        </div>
      </div>
      {session?.status === "ready" && (
        <span className="shrink-0 rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-300">
          复盘可用
        </span>
      )}
      <Badge status={goal.status} className="shrink-0" />
      <span className="hidden shrink-0 font-mono text-[10px] text-ink-3 sm:block">{relTime(goal.created_at)}</span>
    </Link>
  );
}

function ApprovalGateRow({
  gate,
  project,
  onConsume,
}: {
  gate: PendingGate;
  project?: string;
  onConsume?: () => void;
}) {
  const waited = gate.held_since ? relTime(gate.held_since) : "";
  return (
    <Link
      href={`/goals/${gate.goal_id}#held-gates`}
      onClick={onConsume}
      className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/50"
    >
      <ShieldAlert className="h-4 w-4 shrink-0 text-amber-400" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-ink">
          {gate.title || gate.node_key}
          <span className="ml-1.5 text-xs font-normal text-ink-3">· 等你拍板</span>
        </div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-ink-3">
          <span className="truncate">{project || "未归属项目"}</span>
          <span>·</span>
          <span className="truncate">{gate.goal_title || "任务"}</span>
          {waited ? (
            <>
              <span>·</span>
              <span className="text-amber-300/90">已等 {waited}</span>
            </>
          ) : null}
        </div>
      </div>
      <span className="shrink-0 text-[11px] text-ink-3">去拍板 →</span>
    </Link>
  );
}

function ReviewRow({ session, projects }: { session: RsiSession; projects: Map<string, string> }) {
  return (
    <Link href={`/training/${session.id}`} className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-2/50">
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-ink">{session.goal_title}</div>
        <div className="mt-0.5 truncate text-[11px] text-ink-3">
          {projects.get(session.domain_id) || "未知项目"} · {session.finding_count} 个发现 ·{" "}
          {STATUS_LABEL[session.status] ?? session.status}
        </div>
      </div>
      <ArrowRight className="h-3.5 w-3.5 shrink-0 text-ink-3" />
    </Link>
  );
}