"use client";

// 全部工作流：跨小队的搜索/筛选视图（IA §4.2 + §9.2 #8）。
//
// 工作流的「家」是小队详情 → 工作流 tab；这里不是一级导航，只回答
// 「平台上都有哪些工作流、分别归谁」。列表按归属小队分组，历史桶
// （squad_id = ''）单独成组并写明只读，避免把无主工作流读成本队资产。

import { useMemo, useState } from "react";
import Link from "next/link";
import { ArrowUpRight, GitBranchPlus, PlusCircle, Search, Trash2, Waypoints } from "lucide-react";
import {
  Badge,
  Button,
  ConfirmDialog,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Skeleton,
  StatStrip,
  inputCls,
} from "@/components/ui";
import { useBlueprints, useDeleteBlueprint, useSquads } from "@/lib/queries";
import type { Blueprint } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { DASH, fmtInt } from "@/components/training/format";

const LEGACY = "__legacy__";

// 工作流的状态叙事：能不能被任务钉住（有已发布版本）与有没有未发布的改动
// 是两件正交的事，因此最多两枚章（与小队工作流列表保持一致）。
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

function WorkflowRow({ bp, onDelete }: { bp: Blueprint; onDelete: () => void }) {
  const descLine = (bp.description ?? "").split("\n")[0] ?? "";
  return (
    <li className="group flex items-start gap-1 px-4 py-3.5 transition-colors hover:bg-surface-2/50 focus-within:bg-surface-2/50">
      <Link
        href={`/workflows/${bp.id}`}
        className="flex min-w-0 flex-1 items-start gap-3 focus-visible:outline-none"
      >
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
      <button
        type="button"
        onClick={onDelete}
        title="删除工作流"
        aria-label={`删除工作流 ${bp.name}`}
        className="mt-1 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 opacity-0 transition-all hover:bg-red-500/10 hover:text-red-300 focus-visible:opacity-100 focus-visible:outline-none group-hover:opacity-100"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </button>
    </li>
  );
}

export default function WorkflowsPage() {
  const { data: blueprints, isLoading, isError, error, refetch } = useBlueprints();
  const { data: squads } = useSquads();
  const list = blueprints ?? [];

  const [query, setQuery] = useState("");
  const [squadFilter, setSquadFilter] = useState<string>("all");

  // 删除：仍被未结束目标钉住时服务端拒绝，原因原样放进确认框。
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

  const squadName = useMemo(() => {
    const map = new Map<string, string>();
    (squads ?? []).forEach((s) => map.set(s.id, s.name));
    return map;
  }, [squads]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return list.filter((bp) => {
      if (squadFilter === LEGACY) {
        if (bp.squad_id !== "") return false;
      } else if (squadFilter !== "all" && bp.squad_id !== squadFilter) {
        return false;
      }
      if (!q) return true;
      const owner = bp.squad_id ? squadName.get(bp.squad_id) ?? "" : "历史 未归属";
      return [bp.name, bp.description ?? "", bp.id, owner].some((v) =>
        v.toLowerCase().includes(q)
      );
    });
  }, [list, query, squadFilter, squadName]);

  // 分组：按小队顺序；历史桶永远排最后。当前筛选到某队时也只出那一组。
  const groups = useMemo(() => {
    const byOwner = new Map<string, Blueprint[]>();
    filtered.forEach((bp) => {
      const key = bp.squad_id || LEGACY;
      const arr = byOwner.get(key) ?? [];
      arr.push(bp);
      byOwner.set(key, arr);
    });
    const owned = (squads ?? [])
      .map((s) => ({ key: s.id, name: s.name, items: byOwner.get(s.id) ?? [] }))
      .filter((g) => g.items.length > 0);
    // 归属小队已被删除时，仍按 squad_id 成组并如实标注，不让工作流凭空消失；
    // 小队列表还在加载时先写「小队 …」，不误报成已删除。
    byOwner.forEach((items, key) => {
      if (key === LEGACY) return;
      if (owned.some((g) => g.key === key)) return;
      owned.push({
        key,
        name:
          squads === undefined
            ? `小队 ${key.slice(0, 8)}…`
            : `已删除小队（${key.slice(0, 8)}）`,
        items,
      });
    });
    const legacy = byOwner.get(LEGACY) ?? [];
    return { owned, legacy };
  }, [filtered, squads]);

  const stats = {
    total: list.length,
    bindable: list.filter((b) => !!b.latest_version_id).length,
    legacy: list.filter((b) => b.squad_id === "").length,
    squads: new Set(list.filter((b) => b.squad_id).map((b) => b.squad_id)).size,
  };

  return (
    <PageBody>
      <PageHeader
        title="全部工作流"
        desc="工作流归属于一支小队：任务只能钉住本队已发布的版本，跨队复用请复制一份。这里是跨队的搜索视图。"
        action={
          <Link
            href="/workflows/new"
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
          >
            <PlusCircle className="h-3.5 w-3.5" /> 新建工作流
          </Link>
        }
      />

      {!isLoading && !isError && stats.total > 0 && (
        <StatStrip
          items={[
            { label: "工作流", value: fmtInt(stats.total), hint: `${fmtInt(stats.squads)} 支小队` },
            {
              label: "可绑定任务",
              value: fmtInt(stats.bindable),
              tone: "ok",
              hint: "至少有一个已发布版本",
            },
            {
              label: "历史 · 未归属",
              value: fmtInt(stats.legacy),
              hint: "Phase B 之前的无主工作流，只读",
            },
          ]}
        />
      )}

      <SectionCard bodyClassName="p-0">
        <div className="flex flex-col gap-2 border-b border-line p-3 sm:flex-row sm:items-center">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-3" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className={`${inputCls} pl-8`}
              placeholder="搜工作流名称、描述、id 或小队…"
              aria-label="搜索工作流"
            />
          </div>
          <label className="flex shrink-0 items-center gap-2 text-xs text-ink-3">
            归属
            <select
              value={squadFilter}
              onChange={(e) => setSquadFilter(e.target.value)}
              className={`${inputCls} h-8 py-0`}
              aria-label="按小队筛选"
            >
              <option value="all">全部小队</option>
              {(squads ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
              <option value={LEGACY}>历史 · 未归属</option>
            </select>
          </label>
        </div>

        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : isError ? (
          <div className="p-6 text-center text-sm text-ink-3">
            <p>加载失败：{error instanceof Error ? error.message : String(error)}</p>
            <Button variant="outline" className="mt-2" onClick={() => refetch()}>
              重试
            </Button>
          </div>
        ) : filtered.length === 0 ? (
          <div className="p-6">
            <Empty icon={<Waypoints className="h-8 w-8" />}>
              {stats.total === 0
                ? "还没有工作流。工作流隶属于小队，先选一支小队，再从模板起步。"
                : "没有匹配的工作流。换个关键词或把「归属」切回全部小队。"}
              <span className="mt-4 flex justify-center">
                {stats.total === 0 ? (
                  <Link
                    href="/squads"
                    className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-sm font-medium text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
                  >
                    去选一支小队
                  </Link>
                ) : (
                  <Button
                    variant="outline"
                    onClick={() => {
                      setQuery("");
                      setSquadFilter("all");
                    }}
                  >
                    清除筛选
                  </Button>
                )}
              </span>
            </Empty>
          </div>
        ) : (
          <div className="space-y-5 p-4">
            {groups.owned.map((group) => (
              <section key={group.key} className="rounded-lg border border-line">
                <div className="flex flex-wrap items-center gap-2 border-b border-line bg-surface-2/40 px-4 py-2.5">
                  <Waypoints className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                  <Link
                    href={`/squads/${group.key}`}
                    className="min-w-0 truncate text-sm font-medium text-ink transition-colors hover:text-accent"
                  >
                    {group.name}
                  </Link>
                  <span className="font-mono text-[11px] text-ink-3">
                    {fmtInt(group.items.length)} 条
                  </span>
                  <span className="ml-auto text-[11px] text-ink-3">
                    <Link href={`/squads/${group.key}`} className="hover:text-accent">
                      查看小队 →
                    </Link>
                  </span>
                </div>
                <ul className="divide-y divide-line">
                  {group.items.map((bp) => (
                    <WorkflowRow
                      key={bp.id}
                      bp={bp}
                      onDelete={() => {
                        setDeleteError("");
                        setDeleting(bp);
                      }}
                    />
                  ))}
                </ul>
              </section>
            ))}

            {groups.legacy.length > 0 && (
              <section className="rounded-lg border border-line">
                <div className="flex flex-wrap items-center gap-2 border-b border-line bg-surface-2/40 px-4 py-2.5">
                  <Waypoints className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                  <span className="text-sm font-medium text-ink">历史 · 未归属小队</span>
                  <span className="font-mono text-[11px] text-ink-3">
                    {fmtInt(groups.legacy.length)} 条
                  </span>
                </div>
                <p className="border-b border-line px-4 py-2 text-[11px] leading-relaxed text-ink-3">
                  Phase B 之前创建的工作流没有归属小队，冻结为只读历史：旧任务继续跑完，新任务不可选择。
                </p>
                <ul className="divide-y divide-line">
                  {groups.legacy.map((bp) => (
                    <WorkflowRow
                      key={bp.id}
                      bp={bp}
                      onDelete={() => {
                        setDeleteError("");
                        setDeleting(bp);
                      }}
                    />
                  ))}
                </ul>
              </section>
            )}
          </div>
        )}
      </SectionCard>

      {deleting && (
        <ConfirmDialog
          title="删除工作流"
          message={`删除工作流「${deleting.name}」会同时删除它的 ${deleting.version_count} 个版本与全部提案，不可恢复。仍有未结束的目标钉住它的某个版本时会被拒绝——先结束/取消那些目标。${deleteError ? ` 删除失败：${deleteError}` : ""}`}
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
