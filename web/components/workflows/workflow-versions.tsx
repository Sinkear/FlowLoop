"use client";

import { GitBranchPlus, PenLine, Play, Rocket, Trash2 } from "lucide-react";
import { Badge, Button, Empty, SectionCard } from "@/components/ui";
import type { BlueprintVersion } from "@/lib/types";
import { cn } from "@/lib/utils";
import { fullTime, VersionDot } from "@/components/workflows/workflow-shared";

/** 版本时间线（次级信息）：默认只读浏览，操作收敛到底部一行。 */
export function WorkflowVersions({
  versions,
  selectedId,
  selected,
  squadId,
  onSelect,
  onEditStructure,
  onPublish,
  onDeleteDraft,
  onNewDraft,
  onCreateTask,
}: {
  versions: BlueprintVersion[];
  selectedId: string;
  selected: BlueprintVersion | null;
  squadId: string;
  onSelect: (id: string) => void;
  onEditStructure: () => void;
  onPublish: () => void;
  onDeleteDraft: () => void;
  onNewDraft: () => void;
  onCreateTask: () => void;
}) {
  return (
    <SectionCard
      title="版本"
      count={versions.length}
      desc="版本不可变：草稿可以随意改，发布后内容冻结。任务钉住的是发布快照。"
      bodyClassName="p-0"
    >
      {versions.length === 0 ? (
        <Empty>
          该工作流还没有版本。开一份草稿就能开始画流程。
        </Empty>
      ) : (
        <ol className="divide-y divide-line/60">
          {versions.map((v) => {
            const active = v.id === selectedId;
            return (
              <li key={v.id} className="relative">
                {active && <span className="absolute inset-y-0 left-0 w-0.5 bg-accent" aria-hidden />}
                <button
                  type="button"
                  onClick={() => onSelect(v.id)}
                  className={cn(
                    "flex w-full items-start gap-3 px-5 py-3 text-left transition-colors",
                    active ? "bg-accent/[0.06]" : "hover:bg-surface-2/50",
                  )}
                >
                  <VersionDot status={v.status} />
                  <span className="w-10 shrink-0 pt-0.5 font-mono text-xs text-ink-2">v{v.version}</span>
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-2">
                      <Badge status={v.status} />
                      {active && (
                        <span className="rounded border border-accent/30 bg-accent/10 px-1 py-px text-[10px] text-accent">
                          正在查看
                        </span>
                      )}
                      <span className="text-[11px] text-ink-3">
                        {v.status === "published"
                          ? `发布于 ${fullTime(v.published_at)}`
                          : `创建于 ${fullTime(v.created_at)}`}
                      </span>
                    </span>
                    <span className="mt-1 block truncate text-xs text-ink-2">
                      {v.changelog || "（未写变更说明）"}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ol>
      )}

      {selected && (
        <div className="border-t border-line/70 px-5 py-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[10px] text-ink-3">
            <span>{selected.id.slice(0, 12)}</span>
            <span>hash {selected.content_hash.slice(0, 10)}</span>
            <span>{selected.content.length.toLocaleString("zh-CN")} 字符</span>
          </div>
          <div className="mt-2 flex flex-wrap gap-2">
            {selected.status === "draft" ? (
              <>
                <Button variant="outline" onClick={onEditStructure}>
                  <PenLine className="h-3.5 w-3.5" />
                  编辑结构
                </Button>
                <Button variant="outline" onClick={onPublish} className="border-emerald-500/40 text-emerald-300 hover:bg-emerald-500/10">
                  <Rocket className="h-3.5 w-3.5" />
                  发布此版本
                </Button>
                <Button variant="ghost" onClick={onDeleteDraft} className="text-ink-3 hover:text-red-400">
                  <Trash2 className="h-3.5 w-3.5" />
                  删除草稿
                </Button>
              </>
            ) : (
              squadId && (
                <Button variant="outline" onClick={onCreateTask} className="border-emerald-500/40 text-emerald-300 hover:bg-emerald-500/10">
                  <Play className="h-3.5 w-3.5" />
                  用此版本新建任务
                </Button>
              )
            )}
            <Button variant="ghost" onClick={onNewDraft} className="ml-auto text-ink-2">
              <GitBranchPlus className="h-3.5 w-3.5" />
              开新草稿
            </Button>
          </div>
        </div>
      )}
    </SectionCard>
  );
}