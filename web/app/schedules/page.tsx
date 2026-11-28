"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { CalendarClock, Plus } from "lucide-react";
import {
  useSchedules,
  useAgents,
  useSquads,
  useDomains,
  useBlueprints,
  useCreateSchedule,
  useUpdateSchedule,
  useDeleteSchedule,
  useSetScheduleEnabled,
  useGoalEvents,
  useScheduleRuns,
} from "@/lib/queries";
import {
  Button,
  PageBody,
  PageHeader,
  Empty,
  Dialog,
  Field,
  inputCls,
  ConfirmDialog,
  Badge,
  SectionCard,
  Skeleton,
  StatStrip,
} from "@/components/ui";
import { cn, relTime } from "@/lib/utils";
import { CategoryBadge } from "@/components/category-badge";
import type { Blueprint, Schedule } from "@/lib/types";

// WorkflowChip：列表行只回答两个问题——「触发时会按哪个工作流跑」和
// 「这一条迭代开不开」。固定版本号来自后端投影（blueprint_version），
// 不拿 latest_version 顶替：草稿版本号会让「v3」看起来像已发布的 v3。
// 迭代跟工作流走：自动拆解（没有固定版本）不再展示迭代开关/标记。
function WorkflowChip({ schedule, blueprints }: { schedule: Schedule; blueprints?: Blueprint[] }) {
  const review = schedule.execution_mode === "train";
  const reviewChip = review ? (
    <span
      className="rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-300"
      title="任务进入终态时分析本次执行，发现可转成该工作流的提案"
    >
      迭代 开
    </span>
  ) : (
    <span
      className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-3"
      title="不分析本次执行；任务卡上会显示「本次未开启迭代」"
    >
      迭代 关
    </span>
  );

  if (!schedule.blueprint_version_id) {
    return (
      <>
        <span
          className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-3"
          title="每次触发由执行 agent 现场拆解；没有固定工作流，发现不会转成提案"
        >
          自动拆解
        </span>
      </>
    );
  }

  // 固定版本被删除/退役时 projectPin 投影为空：显示「已删除」，不静默当成自动拆解。
  if (!schedule.blueprint_id) {
    return (
      <>
        <span
          className="rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-300"
          title="固定的工作流版本已不存在；下一次触发会退回自动拆解并记日志"
        >
          工作流已删除
        </span>
        {reviewChip}
      </>
    );
  }

  const bp = blueprints?.find((b) => b.id === schedule.blueprint_id);
  const stale = !!bp?.latest_version_id && bp.latest_version_id !== schedule.blueprint_version_id;
  return (
    <>
      <Link
        href={`/workflows/${schedule.blueprint_id}`}
        className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-2 hover:border-accent/40 hover:text-accent"
        title="固定的是保存时选中的版本；工作流发布新版本不会自动改这条任务"
      >
        {schedule.blueprint_name || "工作流"} · v{schedule.blueprint_version || "?"}
      </Link>
      {stale && (
        <span
          className="rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-300"
          title="该工作流已发布更新的版本；点「编辑」可一键固定到最新发布版本"
        >
          有新版本
        </span>
      )}
      {reviewChip}
    </>
  );
}

export default function SchedulesPage() {
  useGoalEvents();
  const { data: schedules, isLoading } = useSchedules();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();
  // 工作流列表只用于两件事：把固定版本显示成「名字 · vN」，以及判断有没有新发布
  // 版本。固定的是版本 id，名称/版本号由后端 projectPin 投影，页面上不猜。
  const { data: blueprints } = useBlueprints();
  const createSchedule = useCreateSchedule();
  const deleteSchedule = useDeleteSchedule();
  const setEnabled = useSetScheduleEnabled();
  const [showForm, setShowForm] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [detail, setDetail] = useState<Schedule | null>(null);
  const [editTarget, setEditTarget] = useState<Schedule | null>(null);

  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("new") === "1") setShowForm(true);
  }, []);

  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;
  const domainName = (did: string) => domains?.find((d) => d.id === did)?.name ?? did;
  const assigneeLabel = (s: Schedule) =>
    s.assignee_type === "squad" ? (squadName(s.assignee_id) || s.assignee_id) : (agentName(s.assignee_id) || s.assignee_id);

  const list = schedules ?? [];
  const enabledCount = list.filter((s) => s.enabled).length;
  // 最近一次触发 = 所有任务里最大的 last_run_at（'' 表示从未触发）。
  const lastRunAt = list.reduce((acc, s) => (s.last_run_at > acc ? s.last_run_at : acc), "");
  // 下次触发 = 已启用任务里最早的 next_run_at，回答「这套调度下一次动是什么时候」。
  const nextRunAt = list.reduce((acc, s) => {
    if (!s.enabled || !s.next_run_at) return acc;
    return !acc || s.next_run_at < acc ? s.next_run_at : acc;
  }, "");

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="定时任务"
        desc="按 cron 自动派生 goal：每次触发在所属项目里创建一个任务，并派给指定的 agent 或小队。"
        action={
          <Button onClick={() => setShowForm(true)} className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新建定时任务
          </Button>
        }
      />

      <StatStrip
        items={[
          {
            label: "定时任务",
            value: list.length,
            hint: list.length === 0
              ? "还没有任务"
              : enabledCount === list.length
                ? "全部启用中"
                : `${enabledCount} 个已启用`,
          },
          {
            label: "已启用",
            value: enabledCount,
            tone: enabledCount ? "ok" : "muted",
            hint: list.length ? `${list.length - enabledCount} 个已停用` : "还没有任务",
          },
          {
            label: "最近一次触发",
            value: lastRunAt ? relTime(lastRunAt) : "从未",
            tone: lastRunAt ? "info" : "muted",
            hint: lastRunAt
              ? new Date(lastRunAt).toLocaleString("zh-CN", { hour12: false })
              : "创建后由 cron 自动触发",
          },
          {
            label: "下次触发",
            value: nextRunAt ? relTime(nextRunAt) : "—",
            tone: nextRunAt ? "info" : "muted",
            hint: nextRunAt
              ? new Date(nextRunAt).toLocaleString("zh-CN", { hour12: false })
              : "没有已启用的任务",
          },
        ]}
      />

      <SectionCard
        title="任务列表"
        count={list.length}
        desc="点名称查看触发历史；右侧开关控制启停"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-3 px-4 py-3.5">
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-4 w-44" />
                  <Skeleton className="h-3 w-64" />
                </div>
                <Skeleton className="h-5 w-9 rounded-full" />
              </div>
            ))}
          </div>
        ) : list.length === 0 ? (
          <Empty icon={<CalendarClock className="h-5 w-5" />}>
            暂无定时任务。点右上角「新建定时任务」创建一条 cron 任务——它会按计划自动创建任务。
          </Empty>
        ) : (
          <div className="divide-y divide-line">
            {list.map((s: Schedule) => (
              <div key={s.id} className="flex flex-wrap items-start gap-x-4 gap-y-2 px-4 py-3">
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      onClick={() => setDetail(s)}
                      className="truncate text-sm font-medium text-ink transition-colors hover:text-accent hover:underline text-left"
                    >
                      {s.name}
                    </button>
                    <code className="rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-2">
                      {s.cron_expression}
                    </code>
                    <span className="font-mono text-[10px] text-ink-3">{s.timezone || "UTC"}</span>
                    <CategoryBadge category={s.category} className="scale-90" />
                    <WorkflowChip schedule={s} blueprints={blueprints} />
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                    <span>负责人 {assigneeLabel(s)}</span>
                    <span>项目 {domainName(s.domain_id)}</span>
                    <span>创建于 {relTime(s.created_at)}</span>
                  </div>
                </div>

                <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2">
                  <div className="min-w-[132px]">
                    <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">下次触发</div>
                    <div className="mt-0.5 text-xs text-ink-2">
                      {s.next_run_at ? relTime(s.next_run_at) : "—"}
                    </div>
                    {s.next_run_at && (
                      <div className="text-[10px] text-ink-3">
                        {new Date(s.next_run_at).toLocaleString("zh-CN", { hour12: false })}
                      </div>
                    )}
                  </div>
                  <LastRunBadge schedule={s} />
                  <Toggle
                    enabled={s.enabled}
                    pending={setEnabled.isPending && setEnabled.variables?.id === s.id}
                    onToggle={() => setEnabled.mutate({ id: s.id, enabled: !s.enabled })}
                  />
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => setEditTarget(s)}
                      className="text-xs text-ink-3 transition-colors hover:text-ink"
                    >
                      编辑
                    </button>
                    <button
                      onClick={() => setDeleteTarget(s.id)}
                      className="text-xs text-ink-3 transition-colors hover:text-red-400"
                    >
                      删除
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </SectionCard>

      {showForm && <NewScheduleForm agents={agents} squads={squads} domains={domains} blueprints={blueprints} onClose={() => setShowForm(false)} />}
      {detail && (
        <ScheduleDetail
          schedule={detail}
          agentName={agentName}
          squadName={squadName}
          domainName={domainName}
          onClose={() => setDetail(null)}
          onEdit={() => { setEditTarget(detail); setDetail(null); }}
        />
      )}
      {editTarget && (
        <EditScheduleDialog
          schedule={editTarget}
          agents={agents}
          squads={squads}
          domains={domains}
          blueprints={blueprints}
          onClose={() => setEditTarget(null)}
        />
      )}
      {deleteTarget && (
        <ConfirmDialog
          title="确认删除"
          message="确定要删除此 Schedule 吗？"
          onConfirm={() => deleteSchedule.mutate(deleteTarget, { onSuccess: () => setDeleteTarget(null) })}
          onClose={() => setDeleteTarget(null)}
          loading={deleteSchedule.isPending}
        />
      )}

      {/* 启停/删除失败必须显示——静默等于「点了没反应」。 */}
      {(setEnabled.isError || deleteSchedule.isError) && (
        <p className="text-sm text-red-400">
          {String(setEnabled.error ?? deleteSchedule.error)}
        </p>
      )}
    </PageBody>
  );
}

// LastRunBadge 显示最近一次触发的真实结果：读该任务的触发历史（最新一条），
// goal 状态即这次触发的产出状态；调度失败（dispatched 未落库 goal）显示 failed。
// 从未触发过不发请求，直接给文案。
function LastRunBadge({ schedule }: { schedule: Schedule }) {
  const { data: runs, isLoading } = useScheduleRuns(schedule.id, !!schedule.last_run_at);
  const last = runs?.[0];
  return (
    <div className="min-w-[86px]">
      <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">最近结果</div>
      <div className="mt-1">
        {!schedule.last_run_at ? (
          <span className="text-[11px] text-ink-3">未触发</span>
        ) : isLoading && !last ? (
          <span className="text-[11px] text-ink-3">读取中…</span>
        ) : last ? (
          <Badge status={last.status === "failed" ? "failed" : last.goal_status || "pending"} />
        ) : (
          <span className="text-[11px] text-ink-3">无记录</span>
        )}
      </div>
    </div>
  );
}

// Toggle 是启停开关：role=switch + aria-checked，颜色只走语义 token。
function Toggle({
  enabled,
  pending,
  onToggle,
}: {
  enabled: boolean;
  pending: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      aria-label={enabled ? "停用" : "启用"}
      title={enabled ? "点击停用" : "点击启用"}
      disabled={pending}
      onClick={onToggle}
      className={cn(
        "relative h-5 w-9 shrink-0 rounded-full border transition-colors duration-150 disabled:opacity-50",
        enabled ? "border-accent bg-accent" : "border-line-strong bg-surface-3"
      )}
    >
      <span
        className={cn(
          "absolute top-0.5 h-3.5 w-3.5 rounded-full transition-[left] duration-150",
          enabled ? "left-[18px] bg-accent-ink" : "left-0.5 bg-ink-3"
        )}
      />
    </button>
  );
}

// ScheduleDetail is the schedule's firing history view (clicking the name):
// the template config at the top, each firing below with the goal it
// produced and that goal's current status, linked to the goal detail.
function ScheduleDetail({
  schedule,
  agentName,
  squadName,
  domainName,
  onClose,
  onEdit,
}: {
  schedule: Schedule;
  agentName: (id: string) => string;
  squadName: (id: string) => string;
  domainName: (id: string) => string;
  onClose: () => void;
  onEdit: () => void;
}) {
  const { data: runs, isLoading } = useScheduleRuns(schedule.id);
  const assigneeLabel =
    schedule.assignee_type === "squad" ? (squadName(schedule.assignee_id) || schedule.assignee_id) : (agentName(schedule.assignee_id) || schedule.assignee_id);

  return (
    <Dialog
      title={`定时任务：${schedule.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onEdit}>编辑</Button>
          <Button variant="outline" onClick={onClose}>关闭</Button>
        </>
      }
    >
      <div className="space-y-3">
        {/* Template config */}
        <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-sm text-ink-2">
          <span><span className="text-ink-3 text-xs">Cron：</span><code className="text-xs bg-surface-2 px-1.5 py-0.5 rounded">{schedule.cron_expression}</code>（{schedule.timezone || "UTC"}）</span>
          <span><span className="text-ink-3 text-xs">负责人：</span>{assigneeLabel}</span>
          <span><span className="text-ink-3 text-xs">项目：</span>{domainName(schedule.domain_id)}</span>
          <span>
            <span className="text-ink-3 text-xs">状态：</span>
            <Badge status={schedule.enabled ? "running" : "cancelled"} className="scale-90" />
          </span>
          <span>
            <span className="text-ink-3 text-xs">验收分类：</span>
            <CategoryBadge category={schedule.category} />
          </span>
          <span><span className="text-ink-3 text-xs">下次触发：</span>{schedule.next_run_at ? new Date(schedule.next_run_at).toLocaleString("zh-CN") : "-"}</span>
          <span><span className="text-ink-3 text-xs">上次触发：</span>{schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString("zh-CN") : "从未"}</span>
        </div>
        {schedule.title_template && (
          <p className="text-xs text-ink-3">
            <span className="text-ink-3">标题模板：</span>{schedule.title_template}
          </p>
        )}

        {/* Firing history */}
        <div className="border-t border-line pt-2">
          <p className="text-xs font-medium text-ink-3 uppercase tracking-wide mb-2">
            触发历史{runs && runs.length > 0 && `（${runs.length}）`}
          </p>
          {isLoading ? (
            <p className="text-xs text-ink-3 py-3">加载中…</p>
          ) : !runs || runs.length === 0 ? (
            <p className="text-xs text-ink-3 py-3">还没有触发过。</p>
          ) : (
            <ul className="space-y-1 max-h-64 overflow-y-auto">
              {runs.map((r) => (
                <li key={r.id} className="flex items-center gap-2 text-xs">
                  <span className="text-ink-3 shrink-0">{new Date(r.planned_at).toLocaleString("zh-CN")}</span>
                  <Badge status={r.goal_status || "cancelled"} className="scale-90 shrink-0" />
                  <Link
                    href={`/goals/${r.goal_id}`}
                    onClick={onClose}
                    className="text-ink-2 hover:text-accent hover:underline truncate"
                  >
                    {r.goal_title || r.goal_id.slice(0, 8)}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Dialog>
  );
}

// ScheduleFormFields is the shared field set for create + edit. It receives
// the form state + setters from the parent (the parent owns submission).
function ScheduleFormFields({
  form,
  set,
  agents,
  squads,
  domains,
  blueprints,
}: {
  form: ScheduleFormState;
  set: (patch: Partial<ScheduleFormState>) => void;
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string; default_workflow_id?: string }[];
  domains?: { id: string; name: string }[];
  blueprints?: Blueprint[];
}) {
  // 可选工作流 = 本队已发布的版本（latest_version_id 就是最新发布版本的 id）。
  // 没发布过的队只给「自动拆解」，不给一个钉上去必然 400 的选项。
  const squadId = form.assignee_type === "squad" ? form.assignee_id : "";
  const options = (blueprints ?? []).filter((b) => b.squad_id === squadId && b.latest_version_id);
  // 固定的版本可能已经被删除（历史数据），或来自别的队（Phase B 之前）。
  // 这类值必须作为「当前值」出现在下拉里，否则保存时会被悄悄清空。
  const orphanPin =
    !!form.blueprint_version_id && !options.some((b) => b.latest_version_id === form.blueprint_version_id);

  return (
    <>
      <Field label="名称" hint="必填">
        <input value={form.name} onChange={(e) => set({ name: e.target.value })} className={inputCls} required placeholder="定时任务名称…" />
      </Field>
      <Field label="标题模板" hint="必填，每次触发时使用此模板创建任务标题">
        <input value={form.title_template} onChange={(e) => set({ title_template: e.target.value })} className={inputCls} required placeholder="每日站会 {{date}}" />
      </Field>
      <Field label="描述">
        <textarea value={form.description} onChange={(e) => set({ description: e.target.value })} className={inputCls} rows={2} />
      </Field>
      <Field label="负责人类型" hint="要跑工作流请选小队；单个智能体的任务不能固定工作流">
        <select
          value={form.assignee_type}
          onChange={(e) => {
            set({
              assignee_type: e.target.value,
              assignee_id: "",
              blueprint_version_id: "",
              blueprint_name: "",
              // 换负责人类型 = 工作流清空 = 自动拆解，迭代开关一并回落；
              // 否则「小队+train → 改成智能体」会把无工作流的 train 带出去。
              execution_mode: "infer",
            });
          }}
          className={inputCls}
        >
          <option value="agent">智能体</option>
          <option value="squad">小队</option>
        </select>
      </Field>
      {form.assignee_type === "agent" && (
        <Field label="选择智能体" hint="必填">
          <select value={form.assignee_id} onChange={(e) => set({ assignee_id: e.target.value })} className={inputCls} required>
            <option value="">选择…</option>
            {agents?.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
      )}
      {form.assignee_type === "squad" && (
        <Field label="选择小队" hint="必填">
          <select
            value={form.assignee_id}
            onChange={(e) => {
              const sid = e.target.value;
              // 选定小队时预选该队默认工作流（如果它有已发布版本）。
              // 这只是表单默认值：后端不做任何「保存时自动解析」，空值就是自动拆解。
              const sq = squads?.find((x) => x.id === sid);
              const def = blueprints?.find(
                (b) => b.id === (sq?.default_workflow_id ?? "") && b.latest_version_id
              );
              set({
                assignee_id: sid,
                blueprint_version_id: def?.latest_version_id ?? "",
                blueprint_name: def?.name ?? "",
                // 与新建任务同一默认：钉了工作流就默认开迭代，自动拆解默认关。
                // 用户仍可在下面的“迭代”字段手动改。
                execution_mode: def?.latest_version_id ? "train" : "infer",
              });
            }}
            className={inputCls}
            required
          >
            <option value="">选择…</option>
            {squads?.map((s) => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
        </Field>
      )}
      <Field label="所属项目" hint="必填，每次触发时在此项目的仓库上执行（验收策略 + worktree 来自该项目）">
        <select value={form.domain_id} onChange={(e) => set({ domain_id: e.target.value })} className={inputCls} required>
          <option value="">选择…</option>
          {domains?.map((d) => (
            <option key={d.id} value={d.id}>{d.name}</option>
          ))}
        </select>
      </Field>
      {form.assignee_type === "squad" ? (
        <Field
          label="工作流"
          hint="固定的是版本：触发时按这个版本编排。空 = 自动拆解（不产生提案）。发布新版本不会改这条任务"
        >
          <select
            value={form.blueprint_version_id}
            onChange={(e) => {
              const vid = e.target.value;
              const bp = options.find((b) => b.latest_version_id === vid);
              set({
                blueprint_version_id: vid,
                blueprint_name: bp?.name ?? (vid ? form.blueprint_name : ""),
                // 选择/清空工作流时同步迭代默认；手动改过迭代则以此后手动值为准。
                execution_mode: vid ? "train" : "infer",
              });
            }}
            className={inputCls}
          >
            <option value="">自动拆解（不固定工作流）</option>
            {options.map((b) => (
              <option key={b.id} value={b.latest_version_id}>
                {b.name} · v{b.latest_version}
              </option>
            ))}
            {orphanPin && (
              <option value={form.blueprint_version_id}>
                {form.blueprint_name || "已固定的工作流"}（当前值 · 版本可能已删除）
              </option>
            )}
          </select>
        </Field>
      ) : (
        <Field label="工作流" hint="单个智能体的任务不能固定工作流；要跑工作流请把负责人改成小队">
          <div className={`${inputCls} cursor-not-allowed text-ink-3`}>自动拆解</div>
        </Field>
      )}

      {/* 迭代跟工作流走：只有钉了工作流才展示开关；自动拆解没有可迭代的图。
           老数据里 agent 任务可能残留 train，这里保存时也压回 infer（见 submit）。 */}
      {form.blueprint_version_id && (
        <Field
          label="迭代"
          hint={
            form.execution_mode === "train"
              ? "任务进入终态时分析本次执行，发现可转成该工作流的提案"
              : "不分析本次执行；任务卡上显示「本次未开启迭代」"
          }
        >
          <select
            value={form.execution_mode}
            onChange={(e) => set({ execution_mode: e.target.value })}
            className={inputCls}
          >
            <option value="infer">关 · 不分析本次执行</option>
            <option value="train">开 · 分析本次执行</option>
          </select>
        </Field>
      )}
      {form.blueprint_version_id && form.execution_mode === "train" && (
        <>
          <div className="grid grid-cols-2 gap-3">
            <Field label="分析预算 (USD)" hint="上限 100；超预算即停止分析">
              <input
                type="number"
                min={0}
                max={100}
                step={0.5}
                value={form.budget_usd}
                onChange={(e) => set({ budget_usd: Number(e.target.value) || 0 })}
                className={inputCls}
              />
            </Field>
            <Field label="采样率" hint="(0,1]；1 = 每次都分析，未命中记为「跳过」">
              <input
                type="number"
                min={0}
                max={1}
                step={0.05}
                value={form.sample_rate}
                onChange={(e) => set({ sample_rate: Number(e.target.value) || 0 })}
                className={inputCls}
              />
            </Field>
          </div>
          <Field label="内容采集" hint="完整内容分析更准，但占用更多空间">
            <select
              value={form.content_capture}
              onChange={(e) => set({ content_capture: e.target.value })}
              className={inputCls}
            >
              <option value="none">不采集</option>
              <option value="redacted">脱敏摘要（默认）</option>
              <option value="full">完整内容</option>
            </select>
          </Field>
        </>
      )}
      {/* Feature B：触发出来的 Goal 继承此验收分类。 */}
      <Field
        label="验收分类"
        hint={form.category === "hootl" ? "无需人工复检，任务完成即结束" : "结束前进入 review 阶段等待人工审批"}
      >
        <select value={form.category} onChange={(e) => set({ category: e.target.value })} className={inputCls}>
          <option value="hitl">HITL · 人工验收</option>
          <option value="hootl">HOOTL · 免复检</option>
        </select>
      </Field>
      <Field label="Cron 表达式" hint="5 字段 cron: 分 时 日 月 星期（必填）">
        <input value={form.cron_expression} onChange={(e) => set({ cron_expression: e.target.value })} className={`${inputCls} font-mono`} required placeholder="0 9 * * 1-5" />
      </Field>
      <Field label="时区">
        <input value={form.timezone} onChange={(e) => set({ timezone: e.target.value })} className={inputCls} placeholder="UTC" />
      </Field>
    </>
  );
}

// blueprint_name 是表单里的回显标签（下拉里「当前值 · 可能已删除」那种），
// 不是可提交字段：后端只存版本 id，名称由 projectPin 从 pin 投影出来。
function scheduleBody(form: ScheduleFormState) {
  const { blueprint_name, ...body } = form;
  // 迭代跟工作流走：没钉工作流（自动拆解）一律压回 infer，
  // 兜住老数据残留和「小队+train → 改成智能体」这类历史状态。
  if (!body.blueprint_version_id) body.execution_mode = "infer";
  return body;
}

type ScheduleFormState = {
  name: string;
  title_template: string;
  description: string;
  assignee_type: string;
  assignee_id: string;
  domain_id: string;
  cron_expression: string;
  timezone: string;
  category: string; // hitl | hootl
  // Phase C：固定工作流版本（'' = 自动拆解）+ 迭代三件套。
  // 字段与 goal 同名同义，触发时原样写进 goal，不在这里做二次解释。
  blueprint_version_id: string;
  blueprint_name: string; // 仅用于回显（后端会投影，这里是表单内的临时标签）
  execution_mode: string; // infer | train
  content_capture: string; // none | redacted | full
  sample_rate: number;
  budget_usd: number;
};

function NewScheduleForm({
  agents,
  squads,
  domains,
  blueprints,
  onClose,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string; default_workflow_id?: string }[];
  domains?: { id: string; name: string }[];
  blueprints?: Blueprint[];
  onClose: () => void;
}) {
  const createSchedule = useCreateSchedule();
  const [form, setForm] = useState<ScheduleFormState>({
    name: "", title_template: "", description: "",
    assignee_type: "agent", assignee_id: "", domain_id: "",
    cron_expression: "", timezone: "UTC", category: "hitl",
    blueprint_version_id: "", blueprint_name: "",
    execution_mode: "infer", content_capture: "redacted",
    sample_rate: 1, budget_usd: 2,
  });
  const set = (patch: Partial<ScheduleFormState>) => setForm((f) => ({ ...f, ...patch }));

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createSchedule.mutate(scheduleBody(form), { onSuccess: onClose });
  };

  return (
    <Dialog
      title="新建定时任务"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="schedule-form" disabled={createSchedule.isPending}>
            {createSchedule.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="schedule-form" onSubmit={handleSubmit} className="space-y-4">
        <ScheduleFormFields
          form={form}
          set={set}
          agents={agents}
          squads={squads}
          domains={domains}
          blueprints={blueprints}
        />
        {createSchedule.isError && (
          <p className="text-sm text-red-400">{String(createSchedule.error)}</p>
        )}
      </form>
    </Dialog>
  );
}

// EditScheduleDialog预填当前 schedule 的值，提交调 updateSchedule。
// 改 assignee/domain 只影响后续触发——用提示说明，后端不限制。
function EditScheduleDialog({
  schedule,
  agents,
  squads,
  domains,
  blueprints,
  onClose,
}: {
  schedule: Schedule;
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string; default_workflow_id?: string }[];
  domains?: { id: string; name: string }[];
  blueprints?: Blueprint[];
  onClose: () => void;
}) {
  const updateSchedule = useUpdateSchedule();
  const [form, setForm] = useState<ScheduleFormState>({
    name: schedule.name,
    title_template: schedule.title_template,
    description: schedule.description,
    assignee_type: schedule.assignee_type || "agent",
    assignee_id: schedule.assignee_id,
    domain_id: schedule.domain_id,
    cron_expression: schedule.cron_expression,
    timezone: schedule.timezone || "UTC",
    category: schedule.category || "hitl",
    blueprint_version_id: schedule.blueprint_version_id || "",
    blueprint_name: schedule.blueprint_name || "",
    // 旧行（Phase C 之前创建）后端补的默认值就是 infer/redacted/1/2，
    // 这里仍给 fallback，避免把 '' 当成"开启"提交。
    execution_mode: schedule.execution_mode || "infer",
    content_capture: schedule.content_capture || "redacted",
    sample_rate: typeof schedule.sample_rate === "number" && schedule.sample_rate > 0 ? schedule.sample_rate : 1,
    budget_usd: typeof schedule.budget_usd === "number" ? schedule.budget_usd : 2,
  });
  const set = (patch: Partial<ScheduleFormState>) => setForm((f) => ({ ...f, ...patch }));

  // assignee/domain 与原值不同时显示"仅影响后续触发"提示。
  const assigneeChanged = form.assignee_id !== schedule.assignee_id || form.assignee_type !== schedule.assignee_type;
  const domainChanged = form.domain_id !== schedule.domain_id;
  const pinChanged = form.blueprint_version_id !== (schedule.blueprint_version_id || "");
  const reviewChanged = form.execution_mode !== (schedule.execution_mode || "infer");
  // 该工作流已发布更新的版本时，给一个「固定到最新」的入口（D3）。
  const bp = blueprints?.find((b) => b.id === schedule.blueprint_id);
  const newerPin = !!bp?.latest_version_id && bp.latest_version_id !== form.blueprint_version_id;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    // PUT 是整条替换：Phase C 字段必须一起提交，否则会被清成空/默认值。
    updateSchedule.mutate({ id: schedule.id, ...scheduleBody(form) }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title={`编辑：${schedule.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="schedule-edit-form" disabled={updateSchedule.isPending}>
            {updateSchedule.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <form id="schedule-edit-form" onSubmit={handleSubmit} className="space-y-4">
        <ScheduleFormFields
          form={form}
          set={set}
          agents={agents}
          squads={squads}
          domains={domains}
          blueprints={blueprints}
        />
        {newerPin && (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded border border-amber-500/30 bg-amber-500/10 px-3 py-2">
            <p className="text-xs text-amber-400">
              该工作流已发布<strong> v{bp?.latest_version}</strong>
              ，这条任务还固定在 v{schedule.blueprint_version || "?"}。
            </p>
            <button
              type="button"
              onClick={() =>
                set({ blueprint_version_id: bp?.latest_version_id ?? "", blueprint_name: bp?.name ?? "" })
              }
              className="text-xs font-medium text-amber-300 underline-offset-2 hover:underline"
            >
              固定到最新发布版本
            </button>
          </div>
        )}
        {(assigneeChanged || domainChanged) && (
          <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-3 py-2">
            改动负责人或项目仅影响<strong>后续触发</strong>，已派生的 goal 保持原值不变。
          </p>
        )}
        {(pinChanged || reviewChanged) && (
          <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-3 py-2">
            工作流固定版本或迭代开关只影响<strong>后续触发</strong>；已派生的 goal 保持创建时的版本与开关，迭代按当时的图对比，不会改写历史。
          </p>
        )}
        {updateSchedule.isError && (
          <p className="text-sm text-red-400">{String(updateSchedule.error)}</p>
        )}
      </form>
    </Dialog>
  );
}
