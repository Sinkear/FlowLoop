"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowUpRight, GitBranchPlus, PlusCircle, Trash2, Waypoints } from "lucide-react";
import { Badge, Button, ConfirmDialog, Empty, PageBody, PageHeader, SectionCard, Skeleton, StatStrip } from "@/components/ui";
import { useBlueprints, useDeleteBlueprint } from "@/lib/queries";
import { relTime } from "@/lib/utils";
import type { Blueprint } from "@/lib/types";
import { DASH, fmtInt } from "@/components/training/format";

// 蓝本列表（Phase A + UX 闭环，docs/BLUEPRINT-PROPOSAL.md §4.12）：结构化
// 编排权威的入口页。创建走 /blueprints/new（模板起步）；这里解决「平台上
// 有哪些工作流定义、各自长什么样、现在处于什么状态」的可见性。
//
// 布局取舍（m02841 UI 优化）：
// - 去掉了原来的「ID / 状态 / 创建于」四列表格。ID 对人没有意义，状态用
//   表格里的一个 badge 也说不清——「已发布 v1，但最新是 v3 草稿」才是用户
//   真正要判断的事，那需要两枚章。
// - 一行 = 一张条目卡：左边图形标记 + 名称/摘要/事实行，右边状态章。
//   扫描时先看状态章（右对齐成列），再看名字，最后按需读摘要。

// 一枚蓝本的「状态叙事」：它能不能被绑定、有没有未发布的改动。
// 这两件事正交，所以要两枚章而不是一枚。
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

function BlueprintRow({ bp, onDelete }: { bp: Blueprint; onDelete: () => void }) {
  // 摘要只取第一行——列表页是索引，不是详情。
  const descLine = (bp.description ?? "").split("\n")[0] ?? "";
  return (
    <li className="group flex items-start gap-1 px-4 py-3.5 transition-colors hover:bg-surface-2/50 focus-within:bg-surface-2/50">
      <Link
        href={`/blueprints/${bp.id}`}
        className="flex min-w-0 flex-1 items-start gap-3 focus-visible:outline-none"
      >
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
          {/* 事实行：版本数 / 最新版本号 / 创建时间 / 短 id。
              等宽字体 + 同一档灰度，读起来是「元数据带」而不是正文。 */}
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-ink-3">
            <span className="inline-flex items-center gap-1">
              <GitBranchPlus className="h-3 w-3" />
              {bp.version_count > 0
                ? `${fmtInt(bp.version_count)} 个版本 · 最新 v${bp.latest_version}`
                : "还没有版本"}
            </span>
            <span title={bp.created_at || undefined}>创建于 {bp.created_at ? relTime(bp.created_at) : DASH}</span>
            <span className="truncate" title={bp.id}>
              {bp.id.slice(0, 10)}
            </span>
          </div>
        </div>

        <div className="mt-0.5 flex flex-col items-end gap-1.5">
          <StateChips bp={bp} />
        </div>
      </Link>
      <button
        type="button"
        onClick={onDelete}
        title="删除蓝本"
        aria-label={`删除蓝本 ${bp.name}`}
        className="mt-1 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-300 focus-visible:opacity-100 focus-visible:outline-none group-hover:opacity-100"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </button>
    </li>
  );
}

export default function BlueprintsPage() {
  const { data: blueprints, isLoading, isError, error, refetch } = useBlueprints();
  const list = blueprints ?? [];

  // 删除：只允许删无活跃目标钉住的蓝本；被拒绝时把服务端原因原样放进确认框。
  const del = useDeleteBlueprint();
  const [deleting, setDeleting] = useState<Blueprint | null>(null);
  const [deleteError, setDeleteError] = useState("");
  async function confirmDelete() {
    if (!deleting) return;
    try {
      await del.mutateAsync(deleting.id);
      setDeleting(null);
      setDeleteError("");
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : String(e));
    }
  }

  // 概览条：只放三个判断工作流健康度真正用得上的数——有多少蓝本、有多少
  // 真的能绑定任务、有多少还停在草稿。
  const stats = {
    total: list.length,
    bindable: list.filter((b) => !!b.latest_version_id).length,
    drafts: list.filter((b) => b.latest_version_status === "draft").length,
  };

  return (
    <PageBody>
      <PageHeader
        title="蓝本"
        desc="工作流的结构化定义：任务钉住一个已发布版本，激活时由平台编译成带依赖门控的子任务——执行顺序由代码保证，不靠提示词。"
        action={
          <Link
            href="/blueprints/new"
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
          >
            <PlusCircle className="h-3.5 w-3.5" /> 新建蓝图
          </Link>
        }
      />

      {!isLoading && !isError && stats.total > 0 && (
        <StatStrip
          items={[
            { label: "蓝本", value: fmtInt(stats.total) },
            { label: "可绑定任务", value: fmtInt(stats.bindable), tone: "ok", hint: "至少有一个已发布版本" },
            {
              label: "停在草稿",
              value: fmtInt(stats.drafts),
              tone: stats.drafts > 0 ? "warn" : "muted",
              hint: stats.drafts > 0 ? "最新版本尚未发布" : "没有未发布的改动",
            },
          ]}
        />
      )}

      <SectionCard title="全部蓝本" count={list.length} bodyClassName="p-0">
        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : isError ? (
          <div className="p-6 text-center text-sm text-ink-3">
            <p>加载失败：{error instanceof Error ? error.message : String(error)}</p>
            <button
              className="mt-2 rounded-md border border-line px-3 py-1.5 text-xs hover:bg-surface-2"
              onClick={() => refetch()}
            >
              重试
            </button>
          </div>
        ) : list.length === 0 ? (
          <div className="p-6">
            <Empty icon={<Waypoints className="h-8 w-8" />}>
              还没有蓝本。蓝本就是「先做 A、再做 B、两路并行后汇合」这样的执行计划——写成结构化的节点和依赖，由平台保证按计划跑，不会漏步骤、不会乱序。
              <Link
                href="/blueprints/new?template=serial"
                className="mt-4 inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
              >
                <PlusCircle className="h-3.5 w-3.5" /> 从模板新建第一份
              </Link>
            </Empty>
          </div>
        ) : (
          <ul className="divide-y divide-line">
            {list.map((bp) => (
              <BlueprintRow key={bp.id} bp={bp} onDelete={() => { setDeleteError(""); setDeleting(bp); }} />
            ))}
          </ul>
        )}
      </SectionCard>

      {deleting && (
        <ConfirmDialog
          title="删除蓝本"
          message={`删除蓝本「${deleting.name}」会同时删除它的 ${deleting.version_count} 个版本与全部提案，不可恢复。仍有未结束的目标钉住它的某个版本时会被拒绝——先结束/取消那些目标。${deleteError ? ` 删除失败：${deleteError}` : ""}`}
          loading={del.isPending}
          onConfirm={() => void confirmDelete()}
          onClose={() => {
            setDeleting(null);
            setDeleteError("");
          }}
        />
      )}
    </PageBody>
  );
}
