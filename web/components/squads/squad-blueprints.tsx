"use client";

import Link from "next/link";
import { ArrowUpRight, GitBranchPlus, PlusCircle, Waypoints } from "lucide-react";
import { Badge, Empty, SectionCard, Skeleton } from "@/components/ui";
import { useBlueprints } from "@/lib/queries";
import type { Blueprint } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { DASH, fmtInt } from "@/components/training/format";

// 小队详情的「工作流」tab —— Phase A 的诚实版。
//
// Phase A 还没有 blueprint.squad_id，blueprint 仍是全局实体。所以这里展示
// 全局蓝本，并在最上方写明归属迁移中；绝不把「全局蓝本」说成「本队工作流」。
// 下一阶段（B）加上 squad_id 后才换成真正的本队列表。

const NEW_BLUEPRINT_HREF = "/blueprints/new?source=squad";

// 蓝本的状态叙事：能不能被任务钉住（有已发布版本）与有没有未发布的改动
// 是两件正交的事，因此最多两枚章（与 /blueprints 列表页保持一致）。
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
      <Link href={`/blueprints/${bp.id}`} className="flex items-start gap-3 focus-visible:outline-none">
        <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3 transition-colors group-hover:text-accent">
          <Waypoints className="h-4 w-4" />
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm font-medium text-ink">
              {bp.name || "（未命名蓝本）"}
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

export function SquadBlueprints() {
  const { data, isLoading, isError, error } = useBlueprints();
  const list = data ?? [];

  return (
    <div className="space-y-6">
      {/* 迁移说明必须是常驻文案，不能只在空态出现：用户看到全局列表时
          就得知道这些蓝本不属于本队。 */}
      <div className="flex items-start gap-2 rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2.5 text-[12px] leading-relaxed text-amber-200/90">
        <Waypoints className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <span>
          工作流归属迁移中：当前展示全局蓝本，下一阶段只显示本队工作流。
          <Link href="/blueprints" className="ml-1 text-accent hover:text-accent-dim">
            查看蓝本库 →
          </Link>
        </span>
      </div>

      <SectionCard
        title="工作流"
        count={isLoading ? undefined : list.length}
        desc="全局蓝本（迁移期）；发布版本后可被任务钉住"
        bodyClassName="p-0"
        action={
          <Link
            href={NEW_BLUEPRINT_HREF}
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
          >
            <PlusCircle className="h-3.5 w-3.5" /> 新建蓝本
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
            读取蓝本失败：{error instanceof Error ? error.message : String(error)}
          </Empty>
        ) : list.length === 0 ? (
          <Empty icon={<Waypoints className="h-4 w-4" />}>
            全局还没有任何蓝本。蓝本是结构化的执行计划（节点、依赖、角色），发布后可被任务钉住。
            迁移期内新建的蓝本仍属于全局，不会自动归属这支小队。
            <span className="mt-4 flex flex-wrap items-center justify-center gap-2">
              <Link
                href={NEW_BLUEPRINT_HREF}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
              >
                <PlusCircle className="h-3.5 w-3.5" /> 新建蓝本
              </Link>
              <Link
                href="/blueprints"
                className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-sm font-medium text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
              >
                查看蓝本库
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
    </div>
  );
}