"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { Waypoints } from "lucide-react";
import { Badge, Empty, PageBody, SectionCard, Skeleton } from "@/components/ui";
import { useBlueprint, useBlueprintGraph } from "@/lib/queries";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { DASH, fmtInt } from "@/components/training/format";

// 蓝本详情（Phase A）：版本史 + 规范图只读渲染。
// 规范图与执行图共用 WorkflowGraph 形状（提案 §4.12）——同一渲染器，
// 「计划长什么样」和「实际怎么跑」是同一种图。

function fullTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString("zh-CN");
}

// 规范图指标行是执行图指标的子集（无 critical_path/speedup），单独渲染
// 四格：节点/边/任务/条件——不硬塞进执行图的 WorkflowMetricsRow。
function SpecMetrics({ metrics }: { metrics: Record<string, unknown> | null | undefined }) {
  const m = metrics ?? {};
  const items = [
    { label: "节点", value: fmtInt(typeof m.node_count === "number" ? m.node_count : null) },
    { label: "边", value: fmtInt(typeof m.edge_count === "number" ? m.edge_count : null) },
    { label: "任务节点", value: fmtInt(typeof m.task_count === "number" ? m.task_count : null) },
    { label: "条件节点", value: fmtInt(typeof m.condition_count === "number" ? m.condition_count : null) },
  ];
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {items.map((it) => (
        <div key={it.label} className="rounded-md border border-line bg-surface-2/30 px-2.5 py-2">
          <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">{it.label}</div>
          <div className="mt-1 font-mono text-sm tabular-nums text-ink-2">{it.value}</div>
        </div>
      ))}
    </div>
  );
}

export default function BlueprintDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const { data: detail, isLoading, isError, error, refetch } = useBlueprint(id);
  const versions = useMemo(() => detail?.versions ?? [], [detail]);

  // 默认选中最新版本（列表按新→旧排序时第一个即最新）；published 优先展示。
  const [selectedVersionId, setSelectedVersionId] = useState<string>("");
  const effectiveVersionId =
    versions.find((v) => v.id === selectedVersionId)?.id ??
    versions.find((v) => v.status === "published")?.id ??
    versions[0]?.id ??
    "";
  const selected = versions.find((v) => v.id === effectiveVersionId) ?? null;

  const {
    data: graph,
    isLoading: graphLoading,
    isError: graphError,
    error: graphErr,
  } = useBlueprintGraph(effectiveVersionId);

  const prettyContent = useMemo(() => {
    if (!selected?.content) return "";
    try {
      return JSON.stringify(JSON.parse(selected.content), null, 2);
    } catch {
      return selected.content;
    }
  }, [selected?.content]);

  return (
    <PageBody>
      {/* Breadcrumb + 标题（与 goal 详情同构的手写版式） */}
      <div>
        <Link
          href="/blueprints"
          className="text-sm text-ink-3 hover:text-ink-2 hover:underline"
        >
          ← 返回列表
        </Link>
        <div className="mt-3 flex items-center gap-3">
          <h1 className="text-lg font-semibold text-ink">
            {detail?.blueprint?.name ?? "蓝本"}
          </h1>
          {selected && <Badge status={selected.status} />}
        </div>
        {detail?.blueprint?.description && (
          <p className="mt-1 whitespace-pre-wrap text-sm text-ink-3">
            {detail.blueprint.description}
          </p>
        )}
      </div>
      {isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-72 w-full" />
        </div>
      ) : isError || !detail ? (
        <div className="p-6 text-center text-sm text-ink-3">
          <p>加载失败：{error instanceof Error ? error.message : String(error)}</p>
          <button
            className="mt-2 rounded-md border border-line px-3 py-1.5 text-xs hover:bg-surface-2"
            onClick={() => refetch()}
          >
            重试
          </button>
        </div>
      ) : (
        <>
          {/* 版本史：版本是不可变单元，选中即查看该版本的规范图。 */}
          <SectionCard title="版本" count={versions.length}>
            {versions.length === 0 ? (
              <Empty>该蓝本还没有版本。</Empty>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {versions.map((v) => {
                  const active = v.id === effectiveVersionId;
                  return (
                    <button
                      key={v.id}
                      onClick={() => setSelectedVersionId(v.id)}
                      className={
                        "flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs transition-colors " +
                        (active
                          ? "border-accent/50 bg-accent/10 text-accent"
                          : "border-line text-ink-2 hover:bg-surface-2")
                      }
                      title={
                        (v.changelog ? v.changelog + " · " : "") +
                        (v.published_at ? "发布于 " + fullTime(v.published_at) : "未发布")
                      }
                    >
                      <span className="font-mono">v{v.version}</span>
                      <Badge status={v.status} />
                    </button>
                  );
                })}
              </div>
            )}
            {selected && (
              <div className="mt-3 space-y-1 border-t border-line pt-3 text-xs text-ink-3">
                {selected.changelog && (
                  <div>
                    <span className="text-ink-2">变更说明：</span>
                    {selected.changelog}
                  </div>
                )}
                <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px]">
                  <span>id: {selected.id}</span>
                  <span>content_hash: {selected.content_hash.slice(0, 16)}</span>
                  <span>创建于 {fullTime(selected.created_at)}</span>
                  {selected.published_at && <span>发布于 {fullTime(selected.published_at)}</span>}
                </div>
              </div>
            )}
          </SectionCard>

          {/* 规范图只读渲染（Phase A 验收项）：复用执行图渲染器。 */}
          <SectionCard
            title="规范图"
            desc="计划态的结构化定义：节点是任务/条件/路由/人工门/派生/终点，边编码执行顺序与分支——goal 激活时按此图编译成带依赖门控的子任务"
            bodyClassName="p-0"
          >
            {!selected ? (
              <div className="p-6">
                <Empty icon={<Waypoints className="h-8 w-8" />}>选择一个版本查看规范图。</Empty>
              </div>
            ) : (
              <>
                <div className="p-4 pb-0">
                  <SpecMetrics metrics={graph?.metrics} />
                </div>
                <WorkflowGraphView
                  graph={graph ?? null}
                  loading={graphLoading}
                  error={graphError ? String(graphErr) : null}
                />
              </>
            )}
          </SectionCard>

          {/* 原始 JSON（只读）：结构化数据的可拷贝形态——导出/评审用。 */}
          {selected?.content && (
            <SectionCard title="规范 JSON" desc="该版本的结构化定义原文（只读；发布后不可变）">
              <details>
                <summary className="cursor-pointer select-none text-xs text-ink-3 hover:text-ink-2">
                  展开查看 / 复制 JSON
                </summary>
                <pre className="mt-2 max-h-96 overflow-auto rounded-md border border-line bg-surface-2/40 p-3 font-mono text-[11px] leading-relaxed text-ink-2">
                  {prettyContent}
                </pre>
              </details>
            </SectionCard>
          )}
        </>
      )}
    </PageBody>
  );
}
