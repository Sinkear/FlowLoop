"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { ArrowUpRight, BarChart3, Plus } from "lucide-react";
import {
  Badge,
  Button,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
  StatStrip,
  Toolbar,
} from "@/components/ui";
import {
  useDomains,
  useGoals,
  useRsiOverview,
  useRsiSessions,
  useSquads,
} from "@/lib/queries";
import type { RsiSession, RsiSessionStatus } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { TrainingTrend } from "@/components/training/trend-strip";
import {
  DASH,
  categoryLabel,
  fmtInt,
  fmtMs,
  fmtRatio,
  fmtSpeedup,
  measured,
} from "@/components/training/format";

// 状态筛选把中间态折成「进行中」：用户关心的是“还没好 / 好了 / 失败”，
// 而不是 waiting/analyzing 两段实现细节（详情页会显示精确状态）。
type StatusFilter = "all" | "active" | "ready" | "failed" | "skipped";
type RangeFilter = "all" | "7" | "30";

const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "active", label: "进行中" },
  { value: "ready", label: "已分析" },
  { value: "failed", label: "失败" },
  { value: "skipped", label: "已跳过" },
];

const RANGE_OPTIONS: { value: RangeFilter; label: string }[] = [
  { value: "30", label: "30 天" },
  { value: "7", label: "7 天" },
  { value: "all", label: "全部" },
];

const ACTIVE_STATUSES: RsiSessionStatus[] = ["waiting", "analyzing"];

// 列宽：任务名自适应，其余定宽；项目/小队用于回答“这是哪个项目、哪支队伍跑的”，
// 窄屏下只保留任务名 + 一行摘要（摘要里同样带项目/小队）。
// Tailwind 的 JIT 只扫描静态字面量，所以栅格模板必须写成完整类名，
// 不能用变量插值（否则样式不会被生成）。
const ROW_GRID =
  "grid gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 lg:grid-cols-[minmax(0,1fr)_104px_92px_84px_76px_52px_72px_64px_52px_minmax(0,128px)] lg:items-center lg:gap-3";
const HEAD_GRID =
  "hidden lg:grid lg:grid-cols-[minmax(0,1fr)_104px_92px_84px_76px_52px_72px_64px_52px_minmax(0,128px)] border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 lg:gap-3";

function fullTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString("zh-CN");
}

function matchesStatus(status: RsiSessionStatus, filter: StatusFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "active":
      return ACTIVE_STATUSES.includes(status);
    default:
      return status === filter;
  }
}

function TrainingRow({
  session,
  projectName,
  squadName,
}: {
  session: RsiSession;
  projectName: string;
  squadName: string;
}) {
  const time = session.started_at ? relTime(session.started_at) : DASH;
  // 只有 ready 才有“当前报告”。waiting/skipped 的列值是 0，analyzing/failed
  // 则可能残留上一次分析的列值——都不是当前结果，统一显示「—」。
  // ready 时 finding_count=0 是真实结果，保留 0。
  const ready = session.status === "ready";
  const nodeValue = ready ? fmtInt(measured(session.node_count)) : DASH;
  const cpValue = ready ? fmtMs(measured(session.critical_path_ms)) : DASH;
  const speedupValue = ready ? fmtSpeedup(measured(session.parallel_speedup)) : DASH;
  const findingValue = ready ? fmtInt(session.finding_count) : DASH;
  const topCategory = ready ? session.top_category : "";
  return (
    <li className="relative">
      <div className={ROW_GRID}>
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            {/* 主链接：::after 撑满整行，点行内空白也能进复盘。 */}
            <Link
              href={`/training/${session.id}`}
              className="min-w-0 truncate text-sm font-medium text-ink after:absolute after:inset-0 after:content-[''] hover:text-accent-dim"
            >
              {session.goal_title || "（未命名任务）"}
            </Link>
            {session.goal_id && (
              <Link
                href={`/goals/${session.goal_id}`}
                className="relative z-10 inline-flex shrink-0 items-center gap-0.5 text-[11px] text-ink-3 transition-colors hover:text-accent"
                title="查看这次复盘对应的任务"
              >
                任务
                <ArrowUpRight className="h-3 w-3" />
              </Link>
            )}
          </div>
          {/* 窄屏摘要行：状态/时间/数量压成一行，桌面端由各列分别显示。 */}
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3 lg:hidden">
            <Badge status={session.status} />
            {projectName && <span>{projectName}</span>}
            {squadName && <span>{squadName}</span>}
            <span>{time}</span>
            <span>{nodeValue} 节点</span>
            <span>关键路径 {cpValue}</span>
            <span>{speedupValue}</span>
            <span>{findingValue} 发现</span>
            {topCategory && <span>{categoryLabel(topCategory)}</span>}
          </div>
        </div>

        <span className="hidden truncate text-xs text-ink-2 lg:block" title={projectName || undefined}>
          {projectName || DASH}
        </span>

        <span className="hidden truncate text-xs text-ink-2 lg:block" title={squadName || undefined}>
          {squadName || DASH}
        </span>

        <span className="hidden text-xs text-ink-2 lg:block" title={fullTime(session.started_at)}>
          {time}
        </span>

        <span className="hidden lg:block">
          <Badge status={session.status} />
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 lg:block">
          {nodeValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 lg:block">
          {cpValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 lg:block">
          {speedupValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 lg:block">
          {findingValue}
        </span>

        <span className="hidden min-w-0 lg:block">
          {topCategory ? (
            <>
              <span className="block truncate text-xs text-ink-2">
                {session.top_title || categoryLabel(topCategory)}
              </span>
              <span className="block font-mono text-[10px] text-ink-3">
                {categoryLabel(topCategory)}
                {session.error ? " · 有错误" : ""}
              </span>
            </>
          ) : (
            <span className="text-xs text-ink-3">{DASH}</span>
          )}
        </span>
      </div>
    </li>
  );
}

export default function TrainingPage() {
  const { data: sessions, isLoading, isError, error } = useRsiSessions();
  // 会话只带 goal_title / domain_id；项目名、小队名要 join goal + domains/squads。
  const { data: goals } = useGoals();
  const { data: domains } = useDomains();
  const { data: squads } = useSquads();
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [range, setRange] = useState<RangeFilter>("30");
  const [projectFilter, setProjectFilter] = useState("all");

  const goalById = useMemo(() => new Map((goals ?? []).map((g) => [g.id, g])), [goals]);
  const domainById = useMemo(() => new Map((domains ?? []).map((d) => [d.id, d])), [domains]);
  const squadById = useMemo(() => new Map((squads ?? []).map((s) => [s.id, s])), [squads]);

  const projectIdOf = (s: RsiSession) => goalById.get(s.goal_id)?.domain_id || s.domain_id;

  const projectOptions = useMemo(() => {
    const ids = new Set<string>();
    for (const s of sessions ?? []) {
      const pid = projectIdOf(s);
      if (pid) ids.add(pid);
    }
    return [...ids]
      .map((id) => ({ id, name: domainById.get(id)?.name || id.slice(0, 8) }))
      .sort((a, b) => a.name.localeCompare(b.name, "zh-CN"));
    // projectIdOf 只依赖 goalById / 会话自身；依赖列表已覆盖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessions, goalById, domainById]);

  // 概览跟随时间范围（契约 days 参数），状态/项目筛选只作用于列表。
  const days = range === "7" ? 7 : range === "30" ? 30 : 365;
  const {
    data: overview,
    isLoading: overviewLoading,
    isError: overviewError,
    error: overviewErr,
    refetch: refetchOverview,
  } = useRsiOverview(days);

  const visible = useMemo(() => {
    const cutoff =
      range === "all" ? null : Date.now() - Number(range) * 24 * 60 * 60 * 1000;
    return (sessions ?? [])
      .filter((s) => matchesStatus(s.status, statusFilter))
      .filter((s) => projectFilter === "all" || projectIdOf(s) === projectFilter)
      .filter((s) => {
        if (cutoff === null) return true;
        const t = Date.parse(s.started_at);
        return !Number.isNaN(t) && t >= cutoff;
      })
      .sort((a, b) => {
        const ta = Date.parse(a.started_at);
        const tb = Date.parse(b.started_at);
        return (Number.isNaN(tb) ? 0 : tb) - (Number.isNaN(ta) ? 0 : ta);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessions, statusFilter, range, projectFilter, goalById]);

  const rangeLabel = range === "7" ? "近 7 天" : range === "30" ? "近 30 天" : "全部时间";
  // overview 在没有任何会话时各项都是 0；显示「0% / 0.00×」会比「—」更像真实值。
  // 查询失败时同样不能用 0 冒充数据，全部退化为「—」并给出重试入口。
  const hasSessions = !overviewError && (overview?.sessions ?? 0) > 0;
  const successRate = hasSessions ? overview?.success_rate ?? null : null;
  const avgSpeedup = hasSessions ? overview?.avg_parallel_speedup ?? null : null;
  const avgRework = hasSessions ? overview?.avg_rework_ratio ?? null : null;
  const overviewUnavailable = overviewLoading || overviewError;

  return (
    <PageBody>
      <Link href="/" className="text-xs text-ink-3 transition-colors hover:text-ink-2">
        ← 工作台
      </Link>

      <PageHeader
        title="复盘"
        desc="复盘是一次执行的镜子，不是评分"
        action={
          <Link
            href="/goals/new"
            className="inline-flex items-center justify-center rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors duration-150 hover:bg-accent-dim"
          >
            <Plus className="mr-1 h-4 w-4" />
            新建任务
          </Link>
        }
      />

      {overviewError && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border border-red-500/25 bg-red-500/10 px-3.5 py-2 text-[11px] text-red-300">
          <span className="min-w-0 flex-1">
            复盘概览读取失败：
            {overviewErr instanceof Error ? overviewErr.message : String(overviewErr)}
          </span>
          <Button
            variant="outline"
            className="h-6 px-2 text-[11px]"
            onClick={() => void refetchOverview()}
          >
            重试
          </Button>
        </div>
      )}

      <StatStrip
        items={[
          {
            label: "复盘数",
            value: overviewUnavailable ? DASH : fmtInt(overview?.sessions ?? null),
            hint:
              range === "all"
                ? "复盘会话（不含跳过；概览上限 365 天）"
                : `${rangeLabel}的复盘会话`,
          },
          {
            label: "成功率",
            value: overviewUnavailable ? DASH : fmtRatio(successRate),
            tone:
              successRate === null
                ? "default"
                : successRate >= 0.8
                  ? "ok"
                  : successRate <= 0.5
                    ? "warn"
                    : "default",
            hint: "目标完成 / 全部会话（不含跳过）",
          },
          {
            label: "平均并行加速",
            value: overviewUnavailable ? DASH : fmtSpeedup(avgSpeedup),
            tone: avgSpeedup !== null && avgSpeedup >= 1.5 ? "ok" : avgSpeedup !== null && avgSpeedup < 1.05 ? "warn" : "default",
            hint: "总工作量 / 关键路径；1× = 完全串行",
          },
          {
            label: "平均返工率",
            value: overviewUnavailable ? DASH : fmtRatio(avgRework),
            tone: avgRework !== null && avgRework >= 0.3 ? "warn" : "default",
            hint: "验证 rejected / 总验证轮次",
          },
        ]}
      />

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-md border border-line bg-surface-2/25 px-3.5 py-2 text-[11px] text-ink-3">
        <span>
          人工等待{" "}
          <span className="font-mono text-ink-2">
            {fmtMs(overviewUnavailable ? null : overview?.human_wait_ms ?? null)}
          </span>
        </span>
        <span>
          验证开销{" "}
          <span className="font-mono text-ink-2">
            {fmtRatio(overviewUnavailable ? null : overview?.avg_verify_overhead_ratio ?? null)}
          </span>
        </span>
        <span>
          结构发现{" "}
          <span className="font-mono text-ink-2">
            {overviewUnavailable ? DASH : fmtInt(overview?.findings ?? null)}
          </span>
        </span>
        <span>
          P95 交付耗时{" "}
          <span className="font-mono text-ink-2">
            {fmtMs(hasSessions ? overview?.p95_lead_time_ms ?? null : null)}
          </span>
        </span>
      </div>

      {!overviewError && <TrainingTrend trend={overview?.trend} loading={overviewLoading} />}

      <SectionCard
        title="复盘会话"
        count={visible.length}
        desc={`${rangeLabel} · 按开始时间倒序`}
        bodyClassName="p-0"
      >
        <div className="border-b border-line px-4 py-2.5">
          <Toolbar>
            <Segmented value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} />
            <div className="ml-auto flex flex-wrap items-center gap-3">
              {projectOptions.length > 0 && (
                <label className="flex items-center gap-2 text-[11px] text-ink-3">
                  项目
                  <select
                    value={projectFilter}
                    onChange={(e) => setProjectFilter(e.target.value)}
                    className="rounded-md border border-line bg-surface-1 px-2 py-1 text-xs text-ink-2 outline-none transition-colors focus:border-accent-dim"
                  >
                    <option value="all">全部</option>
                    {projectOptions.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <div className="flex items-center gap-2">
                <span className="text-[11px] text-ink-3">时间范围</span>
                <Segmented value={range} onChange={setRange} options={RANGE_OPTIONS} />
              </div>
            </div>
          </Toolbar>
        </div>

        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="space-y-2 px-4 py-4">
                <Skeleton className="h-4 w-1/3" />
                <Skeleton className="h-3 w-2/3" />
              </div>
            ))}
          </div>
        ) : isError ? (
          <Empty icon={<BarChart3 className="h-5 w-5" />}>
            读取复盘会话失败：{error instanceof Error ? error.message : String(error)}
          </Empty>
        ) : !sessions?.length ? (
          <Empty icon={<BarChart3 className="h-5 w-5" />}>
            还没有复盘会话。任务开启复盘后，进入终态会自动分析并出现在这里。
            <Link href="/goals/new" className="mt-3 block text-xs text-accent-dim hover:text-accent">
              去新建任务 →
            </Link>
          </Empty>
        ) : !visible.length ? (
          <Empty>当前筛选条件下没有会话；换个状态、项目或时间范围看看。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>任务</span>
              <span>项目</span>
              <span>小队</span>
              <span>开始</span>
              <span>状态</span>
              <span className="text-right">节点</span>
              <span className="text-right">关键路径</span>
              <span className="text-right">加速</span>
              <span className="text-right">发现</span>
              <span>主瓶颈</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((s) => {
                const goal = goalById.get(s.goal_id);
                const pid = goal?.domain_id || s.domain_id;
                const squad =
                  goal?.assignee_type === "squad"
                    ? squadById.get(goal.assignee_id)?.name
                    : undefined;
                return (
                  <TrainingRow
                    key={s.id}
                    session={s}
                    projectName={pid ? domainById.get(pid)?.name || pid.slice(0, 8) : ""}
                    squadName={squad ?? ""}
                  />
                );
              })}
            </ul>
          </>
        )}
      </SectionCard>
    </PageBody>
  );
}