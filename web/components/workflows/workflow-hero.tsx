"use client";

import Link from "next/link";
import {
  ChevronRight,
  GitBranchPlus,
  PenLine,
  Play,
  Rocket,
  Trash2,
  Lock,
  Waypoints,
} from "lucide-react";
import { Badge, Button } from "@/components/ui";
import type { Blueprint, BlueprintVersion, Squad } from "@/lib/types";
import { fmtInt } from "@/components/training/format";
import { WorkflowStepTrack, fullTime } from "@/components/workflows/workflow-shared";

/**
 * 详情页顶部：一眼看清“这是什么工作流、归谁、当前版本能不能用、下一步做什么”。
 * 主操作按版本状态收敛为一个：发布草稿 / 用已发布版本建任务 / 先开草稿。
 */
export function WorkflowHero({
  blueprint,
  squad,
  squadsLoaded,
  selected,
  onCreateTask,
  onPublish,
  onEditStructure,
  onCreateDraft,
  onDelete,
}: {
  blueprint: Blueprint;
  squad?: Squad;
  squadsLoaded: boolean;
  selected: BlueprintVersion | null;
  onCreateTask: () => void;
  onPublish: () => void;
  onEditStructure: () => void;
  onCreateDraft: () => void;
  onDelete: () => void;
}) {
  const squadId = blueprint.squad_id;
  const legacy = !squadId;
  const published = selected?.status === "published";
  const draft = selected?.status === "draft";
  const squadName = squad?.name ?? (squadsLoaded ? `已删除小队（${squadId.slice(0, 8)}）` : "小队…");
  const stepCurrent = published ? 2 : draft ? 1 : 0;

  let primary: React.ReactNode;
  if (published) {
    primary = (
      <Button onClick={onCreateTask} disabled={legacy} title={legacy ? "历史工作流不可绑定新任务" : undefined}>
        <Play className="h-4 w-4" />
        用此版本新建任务
      </Button>
    );
  } else if (draft) {
    primary = (
      <Button onClick={onPublish}>
        <Rocket className="h-4 w-4" />
        发布此版本
      </Button>
    );
  } else {
    primary = (
      <Button onClick={onCreateDraft}>
        <GitBranchPlus className="h-4 w-4" />
        开新草稿
      </Button>
    );
  }

  const guidance = published
    ? legacy
      ? `v${selected?.version} 已发布，但这是 Phase B 之前的历史工作流：不再允许新任务绑定，旧任务会继续跑完。`
      : `v${selected?.version} 已发布。用它新建的任务会按这张规范图编译执行；要改流程请开新草稿，旧任务不受影响。`
    : draft
      ? `当前选中的 v${selected?.version} 是草稿，还不能绑定任务。改完结构后发布，新任务才能钉住它。`
      : "还没有任何版本。先开一份草稿：从模板起步、在规范图里增删节点，再发布。";

  return (
    <section className="overflow-hidden rounded-xl border border-line bg-surface-1 shadow-sm shadow-black/10">
      <div className="flex items-center gap-1.5 border-b border-line/70 bg-surface-2/40 px-5 py-2.5 text-xs text-ink-3">
        {legacy ? (
          <Link href="/workflows" className="hover:text-ink">
            全部工作流
          </Link>
        ) : (
          <>
            <Link href="/squads" className="hover:text-ink">
              小队
            </Link>
            <ChevronRight className="h-3 w-3" aria-hidden />
            <Link href={`/squads/${squadId}?tab=workflows`} className="hover:text-ink">
              {squadName}
            </Link>
          </>
        )}
        <ChevronRight className="h-3 w-3" aria-hidden />
        <span className="truncate text-ink">{blueprint.name}</span>
      </div>

      <div className="grid gap-5 px-5 py-5 xl:grid-cols-[minmax(0,1fr)_auto] xl:px-6">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-[11px]">
            <span className="text-ink-3">所属小队</span>
            {legacy ? (
              <span className="inline-flex items-center gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-amber-300">
                <Lock className="h-3 w-3" />
                历史 · 未归属小队
              </span>
            ) : (
              <Link
                href={`/squads/${squadId}`}
                className="group inline-flex items-center gap-1.5 rounded-md border border-line bg-surface-2/60 px-2 py-1 text-xs font-medium text-ink-1 transition-colors hover:border-accent/50 hover:text-accent"
              >
                <Waypoints className="h-3.5 w-3.5 text-ink-3 group-hover:text-accent" />
                {squadName}
                <ChevronRight className="h-3 w-3 text-ink-3 group-hover:text-accent" />
              </Link>
            )}
          </div>

          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
            <h1 className="text-2xl font-semibold tracking-tight text-ink">{blueprint.name}</h1>
            <Badge status={blueprint.latest_version_status || (selected ? selected.status : "draft")} />
            <span className="text-xs text-ink-3">
              {blueprint.version_count ? `${fmtInt(blueprint.version_count)} 个版本` : "尚无版本"}
            </span>
          </div>

          {blueprint.description && (
            <p className="mt-2 max-w-3xl text-sm leading-relaxed text-ink-2">{blueprint.description}</p>
          )}

          <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[11px] text-ink-3">
            {legacy ? (
              <Link
                href="/workflows"
                className="inline-flex items-center gap-1 font-medium text-ink-2 hover:text-accent"
              >
                ← 返回全部工作流
              </Link>
            ) : (
              <Link
                href={`/squads/${squadId}?tab=workflows`}
                className="inline-flex items-center gap-1 font-medium text-ink-2 hover:text-accent"
              >
                ← 返回{squadName}的工作流
              </Link>
            )}
            <span>创建于 {fullTime(blueprint.created_at)}</span>
            <span className="font-mono">{blueprint.id.slice(0, 12)}</span>
            {selected && (
              <span>
                当前查看 v{selected.version} · {selected.status === "published" ? "已发布" : "草稿"}
              </span>
            )}
          </div>

          <div className="mt-5 flex flex-wrap items-center gap-3">
            <WorkflowStepTrack current={stepCurrent} />
            <p className="max-w-2xl text-[11px] leading-relaxed text-ink-3">{guidance}</p>
          </div>
        </div>

        <div className="flex flex-wrap items-start gap-2 xl:max-w-[280px] xl:justify-end">
          {primary}
          {draft && (
            <Button variant="outline" onClick={onEditStructure}>
              <PenLine className="h-4 w-4" />
              编辑结构
            </Button>
          )}
          {selected && !draft && (
            <Button variant="outline" onClick={onCreateDraft}>
              <GitBranchPlus className="h-4 w-4" />
              开新草稿
            </Button>
          )}
          <Button
            variant="ghost"
            onClick={onDelete}
            className="text-ink-3 hover:text-red-400"
            title="删除这个工作流（含全部版本）"
          >
            <Trash2 className="h-4 w-4" />
            删除
          </Button>
        </div>
      </div>

      {legacy && (
        <div className="border-t border-amber-500/20 bg-amber-500/5 px-5 py-2 text-[11px] text-amber-300/90">
          历史桶里的工作流冻结为只读历史：不可设为小队默认，也不可被新任务选择。
        </div>
      )}
    </section>
  );
}