"use client";

import { GitBranchPlus, PenLine, Waypoints } from "lucide-react";
import { Button, Empty } from "@/components/ui";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { DASH, fmtInt } from "@/components/training/format";
import type { BlueprintVersion, WorkflowGraph } from "@/lib/types";
import { cn } from "@/lib/utils";
import { VersionDot } from "@/components/workflows/workflow-shared";

/**
 * 首屏主角：规范图 + 版本切换。
 * 图是“计划态”的，执行期指标在这里没有意义，所以固定 variant="spec"。
 */
export function WorkflowGraphPanel({
  versions,
  selectedId,
  selected,
  onSelect,
  graph,
  loading,
  error,
  onEditStructure,
  onCreateDraft,
}: {
  versions: BlueprintVersion[];
  selectedId: string;
  selected: BlueprintVersion | null;
  onSelect: (id: string) => void;
  graph: WorkflowGraph | undefined;
  loading: boolean;
  error: string | null;
  onEditStructure: () => void;
  onCreateDraft: () => void;
}) {
  const nodeCount = graph?.nodes?.length ?? 0;
  const edgeCount = graph?.edges?.length ?? 0;
  const canEdit = selected?.status === "draft";

  return (
    <section className="overflow-hidden rounded-xl border border-line bg-surface-1 shadow-sm shadow-black/10">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line/70 px-5 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <Waypoints className="h-4 w-4 text-accent" />
          <h2 className="text-sm font-semibold text-ink">规范图</h2>
          <span className="hidden text-[11px] text-ink-3 sm:inline">任务运行时按这张图编译执行</span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {versions.length > 0 && (
            <div className="flex max-w-[46vw] flex-wrap items-center gap-1">
              {versions.map((v) => (
                <button
                  key={v.id}
                  type="button"
                  onClick={() => onSelect(v.id)}
                  title={v.changelog || `v${v.version}`}
                  className={cn(
                    "inline-flex items-center gap-1 rounded-md border px-2 py-1 text-[11px] transition-colors",
                    v.id === selectedId
                      ? "border-accent/50 bg-accent/10 text-accent"
                      : "border-line text-ink-3 hover:border-line-2 hover:text-ink-2",
                  )}
                >
                  <span
                    className={cn(
                      "h-1.5 w-1.5 rounded-full",
                      v.status === "published" ? "bg-emerald-400" : "bg-amber-400",
                    )}
                    aria-hidden
                  />
                  v{v.version}
                </button>
              ))}
            </div>
          )}
          {canEdit && (
            <Button variant="outline" onClick={onEditStructure} className="h-7 px-2.5 text-[11px]">
              <PenLine className="h-3.5 w-3.5" />
              编辑结构
            </Button>
          )}
        </div>
      </div>

      <div className="p-2">
        {versions.length === 0 ? (
          <div>
            <Empty icon={<Waypoints className="h-5 w-5" />}>
              还没有可展示的规范图——先开一份草稿，从模板起步，再在结构编辑器里增删节点。
            </Empty>
            <div className="-mt-8 flex justify-center pb-8">
              <Button variant="outline" onClick={onCreateDraft}>
                <GitBranchPlus className="h-4 w-4" />
                开新草稿
              </Button>
            </div>
          </div>
        ) : (
          <WorkflowGraphView
            graph={graph}
            loading={loading}
            error={error}
            variant="spec"
            showMetrics={false}
            heightClass="min-h-[460px]"
          />
        )}
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line/70 px-5 py-2.5 text-[11px] text-ink-3">
        <span>{graph ? `${fmtInt(nodeCount)} 个节点 · ${fmtInt(edgeCount)} 条连线` : DASH}</span>
        {selected && (
          <span>
            v{selected.version} · {selected.status === "published" ? "已发布" : "草稿"}
          </span>
        )}
        <span className="ml-auto hidden sm:inline">增删节点、改执行者、加依赖，保存后即更新草稿</span>
      </div>

      {selected && (
        <div className="flex items-center gap-1.5 border-t border-line/60 px-5 py-1.5 text-[10px] text-ink-3">
          <VersionDot status={selected.status} />
          当前查看 v{selected.version}；草稿可编辑，发布后内容不可变。
        </div>
      )}
    </section>
  );
}