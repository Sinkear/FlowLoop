"use client";

import Link from "next/link";
import { Waypoints } from "lucide-react";
import { Badge, Empty, PageBody, PageHeader, SectionCard, Skeleton } from "@/components/ui";
import { useBlueprints } from "@/lib/queries";
import { relTime } from "@/lib/utils";

// 蓝本列表（Phase A，docs/BLUEPRINT-PROPOSAL.md §4.12）：结构化编排权威的
// 入口页。只读——创建/编辑走 API 或后续阶段的起草通道；这里先解决
// 「平台上有哪些工作流定义、各自长什么样」的可见性。
//
// 行布局：名称+描述自适应，其余定宽；窄屏折行。

const ROW_GRID =
  "grid gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_110px_130px_90px] md:items-center md:gap-3";
const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_110px_130px_90px] border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 md:gap-3";

function latestVersionLabel(v: string): string {
  if (!v) return "—";
  return v;
}

function BlueprintRow({
  bp,
}: {
  bp: { id: string; name: string; description: string; latest_version_id: string; created_at: string };
}) {
  // 描述只取第一行——列表页是索引，不是详情。
  const descLine = bp.description.split("\n")[0] ?? "";
  return (
    <li>
      <Link href={`/blueprints/${bp.id}`} className={ROW_GRID}>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-ink">{bp.name || "（未命名蓝本）"}</div>
          {descLine && (
            <div className="mt-0.5 truncate text-xs text-ink-3">{descLine}</div>
          )}
          <div className="mt-1 text-[11px] text-ink-3 md:hidden">
            {latestVersionLabel(bp.latest_version_id)} · 创建于 {relTime(bp.created_at)}
          </div>
        </div>
        <span className="hidden font-mono text-xs text-ink-3 md:block">
          {bp.id.slice(0, 10)}
        </span>
        <span className="hidden md:block">
          <Badge status={bp.latest_version_id ? "published" : "draft"} />
        </span>
        <span
          className="hidden text-right text-xs text-ink-3 md:block"
          title={bp.created_at || undefined}
        >
          {bp.created_at ? relTime(bp.created_at) : "—"}
        </span>
      </Link>
    </li>
  );
}

export default function BlueprintsPage() {
  const { data: blueprints, isLoading, isError, error, refetch } = useBlueprints();

  return (
    <PageBody>
      <PageHeader
        title="蓝本"
        desc="工作流的结构化定义：goal 钉住一个已发布版本，激活时由平台编译成带依赖门控的子任务——执行顺序由代码保证，不靠提示词。"
      />

      <SectionCard title="蓝本列表" count={blueprints?.length} bodyClassName="p-0">
        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
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
        ) : (blueprints ?? []).length === 0 ? (
          <div className="p-6">
            <Empty icon={<Waypoints className="h-8 w-8" />}>
              还没有蓝本。创建蓝本走 API（POST /blueprints），或等训练分析从反复同构的执行图里自动起草。
            </Empty>
          </div>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>名称</span>
              <span>ID</span>
              <span>状态</span>
              <span className="text-right">创建于</span>
            </div>
            <ul className="divide-y divide-line">
              {(blueprints ?? []).map((bp) => (
                <BlueprintRow key={bp.id} bp={bp} />
              ))}
            </ul>
          </>
        )}
      </SectionCard>
    </PageBody>
  );
}
