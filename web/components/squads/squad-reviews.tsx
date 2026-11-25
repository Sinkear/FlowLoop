"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { Activity, ArrowRight } from "lucide-react";
import { Badge, Empty, SectionCard, Segmented, Skeleton } from "@/components/ui";
import { useGoals, useRsiSessions } from "@/lib/queries";
import type { RsiSession, RsiSessionStatus } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { DASH, fmtInt, fmtMs, fmtSpeedup, measured } from "@/components/training/format";

// 小队详情的「复盘」tab。
//
// training_session 没有 squad 字段（schema 事实），所以只能用 goal 反查：
// 本队任务（assignee_type=squad && assignee_id=本队）→ goal_id → 会话。
// 这一步在客户端 join，不新增查询接口（Phase A 不改 queries.ts）。

type StatusFilter = "all" | "active" | "ready" | "failed";

const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "active", label: "进行中" },
  { value: "ready", label: "已分析" },
  { value: "failed", label: "失败" },
];

const ACTIVE_STATUSES: RsiSessionStatus[] = ["waiting", "analyzing"];

const ROW_GRID =
  "grid gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_92px_82px_56px_80px_56px_82px] md:items-center md:gap-3";
const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_92px_82px_56px_80px_56px_82px] border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 md:gap-3";

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

function ReviewRow({ session }: { session: RsiSession }) {
  // 列值只在 ready 时是「当前结果」；其余状态后端可能残留上一轮数字。
  const ready = session.status === "ready";
  const nodeValue = ready ? fmtInt(measured(session.node_count)) : DASH;
  const cpValue = ready ? fmtMs(measured(session.critical_path_ms)) : DASH;
  const speedupValue = ready ? fmtSpeedup(measured(session.parallel_speedup)) : DASH;
  const findingValue = ready ? fmtInt(session.finding_count) : DASH;
  const time = session.started_at ? relTime(session.started_at) : DASH;

  return (
    <li className={ROW_GRID}>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5">
          <Link
            href={`/goals/${session.goal_id}`}
            className="truncate text-sm font-medium text-ink transition-colors hover:text-accent"
          >
            {session.goal_title || "（未命名任务）"}
          </Link>
          <Link
            href={`/training/${session.id}`}
            className="inline-flex shrink-0 items-center gap-0.5 text-[11px] text-accent-dim transition-colors hover:text-accent md:hidden"
          >
            复盘 <ArrowRight className="h-3 w-3" />
          </Link>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3 md:hidden">
          <Badge status={session.status} />
          <span>{time}</span>
          <span>{nodeValue} 节点</span>
          <span>{cpValue}</span>
          <span>{findingValue} 发现</span>
        </div>
      </div>

      <span className="hidden text-xs text-ink-2 md:block" title={session.started_at || undefined}>
        {time}
      </span>

      <span className="hidden md:block">
        <Badge status={session.status} />
      </span>

      <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
        {nodeValue}
      </span>

      <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
        {cpValue}
      </span>

      <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
        {findingValue}
      </span>

      <span className="hidden md:block md:text-right">
        <Link
          href={`/training/${session.id}`}
          className="inline-flex items-center gap-0.5 text-xs text-ink-2 transition-colors hover:text-accent"
        >
          复盘详情 <ArrowRight className="h-3 w-3" />
        </Link>
      </span>
    </li>
  );
}

export function SquadReviews({ squadId }: { squadId: string }) {
  const { data: goals, isLoading: goalsLoading } = useGoals();
  const { data: sessions, isLoading: sessionsLoading } = useRsiSessions();
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  const squadGoalIds = useMemo(
    () =>
      new Set(
        (goals ?? [])
          .filter((g) => g.assignee_type === "squad" && g.assignee_id === squadId)
          .map((g) => g.id),
      ),
    [goals, squadId],
  );

  const rows = useMemo(
    () =>
      (sessions ?? [])
        .filter((s) => squadGoalIds.has(s.goal_id))
        .sort((a, b) => (a.started_at < b.started_at ? 1 : a.started_at > b.started_at ? -1 : 0)),
    [sessions, squadGoalIds],
  );

  const visible = rows.filter((s) => matchesStatus(s.status, statusFilter));
  const isLoading = goalsLoading || sessionsLoading;
  const analyzed = rows.filter((s) => s.status === "ready").length;

  return (
    <SectionCard
      title="复盘"
      count={isLoading ? undefined : rows.length}
      desc={
        rows.length > 0
          ? `本队任务的复盘 · 已分析 ${analyzed} 条 · 按开始时间倒序`
          : "本队任务的复盘（仅开启复盘的任务）"
      }
      bodyClassName="p-0"
    >
      {isLoading ? (
        <div className="divide-y divide-line">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="space-y-2 px-4 py-4">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="h-3 w-2/3" />
            </div>
          ))}
        </div>
      ) : rows.length === 0 ? (
        <Empty icon={<Activity className="h-4 w-4" />}>
          这支小队还没有复盘。复盘只对开启「完成后生成复盘」的任务生成（小队 + 蓝本默认开启，
          自动拆解默认关闭）；任务进入终态后才会出现在这里，所以为空不代表任务没跑过。
          <span className="mt-4 flex flex-wrap items-center justify-center gap-2">
            <Link
              href="/training"
              className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-sm font-medium text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
            >
              去训练分析看看
            </Link>
            <Link
              href="/goals/new?form=1"
              className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-sm font-medium text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
            >
              新建训练任务
            </Link>
          </span>
        </Empty>
      ) : (
        <>
          <div className="border-b border-line px-4 py-2.5">
            <Segmented value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} />
          </div>
          {visible.length === 0 ? (
            <Empty>当前筛选条件下没有复盘；换个状态看看。</Empty>
          ) : (
            <>
              <div className={HEAD_GRID}>
                <span>任务</span>
                <span>开始</span>
                <span>状态</span>
                <span className="text-right">节点</span>
                <span className="text-right">关键路径</span>
                <span className="text-right">发现</span>
                <span className="text-right">详情</span>
              </div>
              <ul className="divide-y divide-line">
                {visible.map((s) => (
                  <ReviewRow key={s.id} session={s} />
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </SectionCard>
  );
}