"use client";

import { useState } from "react";
import { GitBranchPlus, GitCompareArrows, PenLine, Play, Rocket, Trash2 } from "lucide-react";
import { Badge, Button, Dialog, Empty, Field, SectionCard, Skeleton, inputCls } from "@/components/ui";
import { DiffCanvas, NodeFieldChanges, ProposalDiffChanges } from "@/components/blueprint-diff";
import { useBlueprintVersionDiff } from "@/lib/queries";
import type { BlueprintVersion } from "@/lib/types";
import { cn } from "@/lib/utils";
import { fullTime, VersionDot, WorkflowErrorBox } from "@/components/workflows/workflow-shared";

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
  const [compareOpen, setCompareOpen] = useState(false);

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
            <Button
              variant="ghost"
              className="ml-auto text-ink-2"
              disabled={versions.length < 2}
              title={versions.length < 2 ? "至少要有两个版本才能对比" : "选两个版本，对比结构增删改与字段差异"}
              onClick={() => setCompareOpen(true)}
            >
              <GitCompareArrows className="h-3.5 w-3.5" />
              版本对比
            </Button>
            <Button variant="ghost" onClick={onNewDraft} className="text-ink-2">
              <GitBranchPlus className="h-3.5 w-3.5" />
              开新草稿
            </Button>
          </div>
        </div>
      )}

      {compareOpen && versions.length >= 2 && (
        <VersionCompareDialog
          versions={versions}
          initialTargetId={selectedId}
          onClose={() => setCompareOpen(false)}
        />
      )}
    </SectionCard>
  );
}

// ── 版本对比（issue #31 P1）──
// 并排拓扑图只能看出「形状」变了，assignee/title/brief 这类字段差异看不见。
// 这里选 base（旧版本）/ target（新版本）两个版本，展示：变更说明对比、左右
// 两图、结构增删改（复用提案 diff 的 ProposalDiffChanges）、字段级差异
// （NodeFieldChanges：键并集逐叶比对，旧值 → 新值成对展示）。

function versionOptionLabel(v: BlueprintVersion): string {
  return `v${v.version}${v.status === "draft" ? " · 草稿" : ""}`;
}

/** target 的默认对比基准：比它旧的最近一个版本；v1 没有更旧的，就取列表里的另一个。 */
function defaultBaseId(versions: BlueprintVersion[], targetId: string): string {
  const target = versions.find((v) => v.id === targetId);
  const older = versions
    .filter((v) => target && v.version < target.version)
    .sort((a, b) => b.version - a.version)[0];
  if (older) return older.id;
  const other = versions.find((v) => v.id !== targetId);
  return other ? other.id : targetId;
}

function VersionCompareDialog({
  versions,
  initialTargetId,
  onClose,
}: {
  versions: BlueprintVersion[];
  initialTargetId: string;
  onClose: () => void;
}) {
  const [targetId, setTargetId] = useState(
    versions.some((v) => v.id === initialTargetId) ? initialTargetId : versions[0].id,
  );
  const [baseId, setBaseId] = useState(() => defaultBaseId(versions, targetId));

  const same = baseId === targetId;
  // 同一个版本没得比：传 null 让 hook 不发请求，UI 给占位说明。
  const diff = useBlueprintVersionDiff(same ? null : baseId, targetId);

  const base = versions.find((v) => v.id === baseId) ?? null;
  const target = versions.find((v) => v.id === targetId) ?? null;
  const draftMark = (v: BlueprintVersion) => (v.status === "draft" ? " · 草稿" : "");

  return (
    <Dialog
      title="版本对比"
      size="wide"
      onClose={onClose}
      footer={
        <Button variant="outline" onClick={onClose}>
          关闭
        </Button>
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="旧版本（对比基准）" hint="差异方向：旧版本 → 新版本，左侧图是它。">
          <select value={baseId} onChange={(e) => setBaseId(e.target.value)} className={inputCls}>
            {versions.map((v) => (
              <option key={v.id} value={v.id}>
                {versionOptionLabel(v)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="新版本（要看的版本）" hint="右侧图是它，差异以它为准。">
          <select value={targetId} onChange={(e) => setTargetId(e.target.value)} className={inputCls}>
            {versions.map((v) => (
              <option key={v.id} value={v.id}>
                {versionOptionLabel(v)}
              </option>
            ))}
          </select>
        </Field>
      </div>

      {same ? (
        <p className="rounded-lg border border-dashed border-line bg-surface-2/20 px-3 py-2 text-xs text-ink-3">
          旧版本和新版本选的是同一个——请换一个不同的版本。
        </p>
      ) : diff.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : diff.isError ? (
        <WorkflowErrorBox
          title="读取版本对比失败"
          message={diff.error instanceof Error ? diff.error.message : String(diff.error)}
        />
      ) : diff.data ? (
        <>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="rounded-lg border border-line px-3 py-2">
              <div className="text-[11px] text-ink-3">
                {base ? `v${base.version} 变更说明` : "旧版本变更说明"}
              </div>
              <p className="mt-1 whitespace-pre-wrap break-words text-xs leading-relaxed text-ink-2">
                {diff.data.changelog_base || "（未写变更说明）"}
              </p>
            </div>
            <div className="rounded-lg border border-line px-3 py-2">
              <div className="text-[11px] text-ink-3">
                {target ? `v${target.version} 变更说明` : "新版本变更说明"}
              </div>
              <p className="mt-1 whitespace-pre-wrap break-words text-xs leading-relaxed text-ink-2">
                {diff.data.changelog_target || "（未写变更说明）"}
              </p>
            </div>
          </div>

          <div className="grid gap-3 lg:grid-cols-2">
            <DiffCanvas
              label={base ? `旧版本 v${base.version}${draftMark(base)}` : "旧版本"}
              graph={diff.data.base}
            />
            <DiffCanvas
              label={target ? `新版本 v${target.version}${draftMark(target)}` : "新版本"}
              graph={diff.data.patched}
            />
          </div>

          <div>
            <h3 className="mb-2 text-xs font-medium text-ink-2">结构增删改</h3>
            <ProposalDiffChanges
              diff={diff.data}
              emptyText="两个版本的结构完全一致——没有增删改任何节点或边。"
            />
          </div>

          {diff.data.nodes_changed.length > 0 && (
            <div>
              <h3 className="mb-2 text-xs font-medium text-ink-2">字段级差异</h3>
              <NodeFieldChanges nodes={diff.data.nodes_changed} />
            </div>
          )}
        </>
      ) : null}
    </Dialog>
  );
}
