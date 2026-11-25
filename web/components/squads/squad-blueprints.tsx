"use client";

import Link from "next/link";
import { ArrowUpRight, GitBranchPlus, PlusCircle, Waypoints } from "lucide-react";
import { Badge, Empty, SectionCard, Skeleton } from "@/components/ui";
import { useBlueprints } from "@/lib/queries";
import type { Blueprint } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { DASH, fmtInt } from "@/components/training/format";

// 小队详情的「工作流」tab —— Phase B：工作流只属于一支小队，这里就是本队列表。
// 跨队浏览在 /workflows（搜索视图），不再有「全局工作流」的说法。

function StateChips({ bp }: { bp: Blueprint }) {
  const hasPublished = !!bp.latest_version_id;
  const latestIsDraft = bp.latest_version_status === "draft";
  const noVersions = bp.version_count === 0;
  return (
    <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
      {noVersions ? (
        <span className="inline-flex items-center gap-1.5 rounded-md bg-zinc-500/10 px-2 py-0.5 text-xs font-medium text-ink-3">
          <span className="h-1.5 w-1.5 rounded-full bg-zinc-400" />
          尚无版本
        </span>
      ) : (
        <>
          {hasPublished ? (
            <Badge status="published" />
          ) : (
            <span className="inline-flex items-center gap-1.5 rounded-md bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-300">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
              未发布
            </span>
          )}
          {latestIsDraft && (
            <span className="inline-flex items-center gap-1.5 rounded-md bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-300">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
              v{bp.latest_version} 草稿
            </span>
          )}
        </>
      )}
    </div>
  );
}

function BlueprintRow({ bp }: { bp: Blueprint }) {
  const descLine = (bp.description ?? "").split("\n")[0] ?? "";
  return (
    <li className="group px-4 py-3.5 transition-colors hover:bg-surface-2/50 focus-within:bg-surface-2/50">
      <Link href={`/workflows/${bp.id}`} className="flex items-start gap-3 focus-visible:outline-none">
        <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3 transition-colors group-hover:text-accent">
          <Waypoints className="h-4 w-4" />
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm font-medium text-ink">
              {bp.name || "（未命名工作流）"}
            </span>
            <ArrowUpRight className="h-3.5 w-3.5 shrink-0 text-ink-3 opacity-0 transition-opacity group-hover:opacity-100" />
          </div>
          {descLine && (
            <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-ink-3">{descLine}</p>
          )}
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-ink-3">
            <span className="inline-flex items-center gap-1">
              <GitBranchPlus className="h-3 w-3" />
              {bp.version_count > 0
                ? `${fmtInt(bp.version_count)} 个版本 · 最新 v${bp.latest_version}`
                : "还没有版本"}
            </span>
            <span title={bp.created_at || undefined}>
              创建于 {bp.created_at ? relTime(bp.created_at) : DASH}
            </span>
            <span className="truncate" title={bp.id}>
              {bp.id.slice(0, 10)}
            </span>
          </div>
        </div>

        <div className="mt-0.5">
          <StateChips bp={bp} />
        </div>
      </Link>
    </li>
  );
}

export function SquadBlueprints({ squadId }: { squadId: string }) {
  const { data, isLoading, isError, error } = useBlueprints(squadId);
  const list = data ?? [];

  return (
    <SectionCard
      title="工作流"
      count={isLoading ? undefined : list.length}
      desc="这支小队的作业标准；发布版本后可被小队任务钉住"
      bodyClassName="p-0"
      action={
        <Link
          href={`/workflows/new?squad=${squadId}`}
          className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
        >
          <PlusCircle className="h-3.5 w-3.5" /> 新建工作流
        </Link>
      }
    >
      {isLoading ? (
        <div className="space-y-2 p-4">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : isError ? (
        <Empty icon={<Waypoints className="h-4 w-4" />}>
          读取工作流失败：{error instanceof Error ? error.message : String(error)}
        </Empty>
      ) : list.length === 0 ? (
        <Empty icon={<Waypoints className="h-4 w-4" />}>
          这支小队还没有工作流。
          <span className="mt-4 block">
            <Link
              href={`/workflows/new?squad=${squadId}`}
              className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
            >
              <PlusCircle className="h-3.5 w-3.5" /> 新建工作流
            </Link>
          </span>
        </Empty>
      ) : (
        <ul className="divide-y divide-line">
          {list.map((bp) => (
            <BlueprintRow key={bp.id} bp={bp} />
          ))}
        </ul>
      )}
    </SectionCard>
  );
}
