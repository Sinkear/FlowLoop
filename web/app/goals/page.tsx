"use client";

import { useEffect, useState, Suspense } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useGoals, useAgents, useSquads, useDomains, useCancelGoal, useReopenGoal, useDeleteGoal, useGoalEvents } from "@/lib/queries";
import { Badge, AttentionChip, ReviewPhaseBadge, PageBody, PageHeader, SectionCard, Segmented, Toolbar, Empty } from "@/components/ui";
import { GoalStats } from "@/components/goal-stats";
import { GoalBoard } from "@/components/goal-board";
import { GoalBatchBar, GoalSelectCheckbox, CheckIcon } from "@/components/goal-batch-bar";
import { CategoryBadge } from "@/components/category-badge";
import { cn } from "@/lib/utils";
import type { Goal, GoalStatus } from "@/lib/types";

// 六种状态全列出来：待办 → 进行中 → 待审批 → 已完成 / 失败 / 已取消。
// 顺序照任务的生命周期走，而不是照枚举定义排。
const STATUS_TABS: { label: string; value: GoalStatus | "all" }[] = [
  { label: "全部", value: "all" },
  { label: "待办", value: "backlog" },
  { label: "进行中", value: "active" },
  { label: "待审批", value: "review" },
  { label: "已完成", value: "done" },
  { label: "失败", value: "failed" },
  { label: "已取消", value: "cancelled" },
];

// 卡片顶部状态色条（与 Badge 同色系，一眼可扫）
const STATUS_BAR: Record<string, string> = {
  backlog: "bg-zinc-500/60",
  active: "bg-blue-400",
  review: "bg-purple-400",
  done: "bg-emerald-400",
  failed: "bg-red-400",
  cancelled: "bg-zinc-500/60",
};

// 负责人头像首字母
const initials = (name: string) => (name ? name.slice(0, 1).toUpperCase() : "?");

export default function GoalsPage() {
  // useSearchParams 要求 Suspense 边界，否则静态渲染时 CSR bailout 会让构建失败。
  return (
    <Suspense fallback={<div className="p-8 text-sm text-ink-3">加载中…</div>}>
      <GoalsPageInner />
    </Suspense>
  );
}

function GoalsPageInner() {
  useGoalEvents();
  const { data: goals, isLoading } = useGoals();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();
  const cancelGoal = useCancelGoal();
  const reopenGoal = useReopenGoal();
  const deleteGoalMut = useDeleteGoal();
  // 筛选状态放 URL（?status=review）：点卡片进详情再「返回列表」不丢筛选，
  // 刷新/分享链接也能还原。无效值回退「全部」。
  const router = useRouter();
  const searchParams = useSearchParams();
  const statusParam = searchParams.get("status");
  const filter = (
    STATUS_TABS.some((t) => t.value === statusParam) ? statusParam : "all"
  ) as GoalStatus | "all";
  const projectParam = searchParams.get("project") ?? "";
  const projectId = (domains ?? []).some((d) => d.id === projectParam) ? projectParam : "";
  const projectName = (domains ?? []).find((d) => d.id === projectId)?.name ?? "";

  const setFilter = (v: GoalStatus | "all") => {
    const params = new URLSearchParams(searchParams.toString());
    if (v === "all") params.delete("status");
    else params.set("status", v);
    const qs = params.toString();
    router.replace(qs ? `/goals?${qs}` : "/goals", { scroll: false });
  };
  const setProject = (id: string) => {
    const params = new URLSearchParams(searchParams.toString());
    if (!id) params.delete("project");
    else params.set("project", id);
    const qs = params.toString();
    router.replace(qs ? `/goals?${qs}` : "/goals", { scroll: false });
  };

  // 视图：看板（拖拽为主）/ 列表（批量与筛选为主）。选择记在 localStorage，
  // 刷新后保持；默认看板——任务面板的主入口。
  const [view, setView] = useState<"board" | "list">("board");
  useEffect(() => {
    const saved = window.localStorage.getItem("ouroflow-goals-view");
    if (saved === "list" || saved === "board") setView(saved);
  }, []);
  const pickView = (v: "board" | "list") => {
    setView(v);
    window.localStorage.setItem("ouroflow-goals-view", v);
  };

  // 已删除智能体 的 goal 仍保留记录：id 非空但查不到时显示「已删除智能体」，
  // 不再把 uuid 露给人看。agents 尚未加载时退回 id（这不是"已删除"），空 id
  // 返回 "" 让调用方的 `|| "?"` 继续兜底。
  const agentName = (aid: string) =>
    agents?.find((a) => a.id === aid)?.name ?? (aid ? (agents ? "已删除智能体" : aid) : "");
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;

  const projectGoals = goals?.filter((g) => !projectId || g.domain_id === projectId) ?? [];
  const filtered = projectGoals.filter((g) => filter === "all" || g.status === filter);

  // 多选批量操作（决策 7-6）：Set<goal id>，跨 tab 切换持久（Gmail 式），
  // 底部浮动批量条对选中项批量执行。batchBusy = 批量执行中（格子禁点）。
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [batchBusyId, setBatchBusyId] = useState<string | null>(null);
  const batchBusy = batchBusyId !== null;
  const toggleSelect = (id: string) => {
    if (batchBusy) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const allFilteredSelected = filtered.length > 0 && filtered.every((g) => selected.has(g.id));
  const selectAllFiltered = () => {
    if (batchBusy) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (allFilteredSelected) {
        for (const g of filtered) next.delete(g.id);
      } else {
        for (const g of filtered) next.add(g.id);
      }
      return next;
    });
  };

  return (
    <PageBody full={view === "board"}>
      <PageHeader
        title={projectName ? `任务 · ${projectName}` : "任务"}
        desc={projectName ? "只看这个项目下的任务。拖动卡片改状态，或者切到列表批量处理。" : "拖动卡片改状态，或者切到列表批量处理。"}
        action={
          <div className="flex items-center gap-2">
            <Segmented
              value={view}
              onChange={pickView}
              options={[
                { value: "board", label: "看板" },
                { value: "list", label: "列表" },
              ]}
            />
            <Link
              href={projectId ? `/goals/new?project=${projectId}` : "/goals/new"}
              className="inline-flex items-center justify-center rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors duration-150 hover:bg-accent-dim"
            >
              + 新建
            </Link>
          </div>
        }
      />

      <Toolbar className="mb-3 justify-between">
        <label className="flex items-center gap-2 text-xs text-ink-3">
          项目
          <select
            value={projectId}
            onChange={(e) => setProject(e.target.value)}
            className="h-8 rounded-md border border-line bg-surface-1 px-2 text-xs text-ink-2 outline-none focus:border-accent/60"
          >
            <option value="">全部项目</option>
            {(domains ?? []).map((d) => (
              <option key={d.id} value={d.id}>{d.name}</option>
            ))}
          </select>
        </label>
        {projectId && (
          <Link href={filter === "all" ? "/goals" : `/goals?status=${filter}`} className="text-xs text-ink-3 hover:text-accent">
            清除项目筛选
          </Link>
        )}
      </Toolbar>

      {/* Stats cards */}
      <GoalStats goals={projectGoals} loading={isLoading} />

      {view === "board" ? (
        isLoading ? (
          <div className="text-sm text-ink-3 py-16 text-center">加载中…</div>
        ) : (
          <GoalBoard goals={filtered} agents={agents ?? []} squads={squads ?? []} domains={domains ?? []} />
        )
      ) : (
        <>

      {/* 筛选：状态 tab + 全选（作用于当前筛选，配合多选批量条） */}
      <Toolbar className="mb-3 justify-between">
        <Segmented
          value={filter}
          onChange={setFilter}
          options={STATUS_TABS.map((t) => {
            const count = t.value === "all" ? projectGoals.length : projectGoals.filter((g) => g.status === t.value).length;
            return { value: t.value, label: count > 0 ? `${t.label} ${count}` : t.label };
          })}
        />
        {filtered.length > 0 && (
          <button
            type="button"
            disabled={batchBusy}
            onClick={selectAllFiltered}
            className="flex items-center gap-2 px-2.5 py-1.5 rounded-md text-xs font-medium text-ink-3 hover:text-accent hover:bg-accent/10 transition-colors disabled:opacity-50 disabled:hover:text-ink-3 disabled:hover:bg-transparent"
          >
            <span
              className={cn(
                "h-4 w-4 rounded border flex items-center justify-center transition-colors",
                allFilteredSelected ? "bg-accent border-accent text-accent-ink" : "bg-surface-1 border-line-strong"
              )}
            >
              {allFilteredSelected && <CheckIcon className="h-2.5 w-2.5" />}
            </span>
            {allFilteredSelected ? "取消全选" : "全选"}
          </button>
        )}
      </Toolbar>

      {/* Goals — 卡片：状态色条 + 谁在干 + 元信息（不是表格的伪卡片） */}
      <SectionCard title="任务列表" count={filtered.length}>
      {isLoading ? (
        <div className="text-sm text-ink-3 py-16 text-center">加载中…</div>
      ) : filtered.length === 0 ? (
        <Empty>
          {filter === "all" ? "还没有任务。点「新建」派第一条活。" : "没有符合当前筛选条件的任务。"}
        </Empty>
      ) : (
        <div className={cn("grid gap-3 md:grid-cols-2", batchBusy && "pointer-events-none")}>
          {filtered.map((g: Goal) => (
            <Link
              key={g.id}
              href={`/goals/${g.id}${filter === "all" ? "" : `?status=${filter}`}`}
              className={cn(
                "group relative flex bg-surface-1 rounded-lg border border-line overflow-hidden hover:border-accent/40 transition-colors duration-150",
                selected.has(g.id) && "border-accent/50 bg-accent/10",
                batchBusyId === g.id && "ring-2 ring-accent/60 border-accent/50",
                batchBusy && batchBusyId !== g.id && "opacity-60"
              )}
            >
              {/* 状态在左侧：竖向渐变条（一眼可扫） */}
              <div className={`w-1.5 shrink-0 ${STATUS_BAR[g.status] ?? "bg-zinc-500/60"}`} />
              <div className="p-4 flex-1 min-w-0">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-start gap-2.5 min-w-0">
                    <GoalSelectCheckbox
                      checked={selected.has(g.id)}
                      disabled={batchBusy}
                      onToggle={() => toggleSelect(g.id)}
                    />
                    <h3 className="font-medium text-ink group-hover:text-accent transition-colors min-w-0">
                      {g.title}
                    </h3>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <CategoryBadge category={g.category} />
                    <AttentionChip attention={g.attention} />
                    {g.status === "review" ? (
                      <ReviewPhaseBadge phase={g.review_phase} className="shrink-0" />
                    ) : (
                      <Badge status={g.status} className="shrink-0" />
                    )}
                  </div>
                </div>
                {g.status === "review" && g.review_request && (
                  <p className="mt-1.5 text-xs text-purple-300 line-clamp-1">{g.review_request}</p>
                )}
                <div className="mt-1.5 text-[11px] text-ink-3">
                  {(domains ?? []).find((d) => d.id === g.domain_id)?.name || "未归属项目"}
                </div>
                {/* 元信息：谁在干 / 负责人 · 时间 */}
                <div className="mt-3 flex items-center gap-x-4 gap-y-1 flex-wrap text-xs text-ink-3">
                  {g.current_agent_id ? (
                    <span className="flex items-center gap-1.5 font-medium text-emerald-400">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                      {agentName(g.current_agent_id)} 正在执行
                    </span>
                  ) : (
                    <span className="flex items-center gap-1.5">
                      <span className="inline-block h-4 w-4 rounded-full bg-accent/15 text-accent text-[10px] leading-4 text-center font-medium">
                        {initials(g.assignee_type === "squad" ? (squadName(g.assignee_id) || "?") : (agentName(g.assignee_id) || "?"))}
                      </span>
                      {g.assignee_type === "squad"
                        ? (squadName(g.assignee_id) || g.assignee_id)
                        : g.assignee_id
                          ? (agentName(g.assignee_id) || g.assignee_id)
                          : "未分配"}
                    </span>
                  )}
                  <span>
                    {g.created_at ? new Date(g.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : ""}
                  </span>
                  {g.status === "done" && <span className="text-emerald-400 font-medium">✓ 已完成</span>}
                  {g.status === "failed" && <span className="text-red-400 font-medium">✕ 失败</span>}
                </div>
                {/* 行内操作：取消 / 重开 / 删除。卡片整体是 Link，
                    按钮必须 preventDefault + stopPropagation 阻止跳转。
                    cancel：非终态（backlog/active/review）；reopen：终态
                    （done/failed/cancelled，人类接管路径）；delete：任意状态
                    （级联删 run/评论/子目标，confirm 后执行）。 */}
                <div className="mt-2 flex items-center gap-3 justify-end">
                  {(g.status === "active" || g.status === "backlog" || g.status === "review") && (
                    <button
                      className="text-xs text-ink-3 hover:text-amber-400 transition-colors disabled:opacity-50"
                      disabled={cancelGoal.isPending}
                      onClick={(e) => { e.preventDefault(); e.stopPropagation(); cancelGoal.mutate(g.id); }}
                    >
                      取消
                    </button>
                  )}
                  {(g.status === "done" || g.status === "failed" || g.status === "cancelled") && (
                    <button
                      className="text-xs text-ink-3 hover:text-accent transition-colors disabled:opacity-50"
                      disabled={reopenGoal.isPending}
                      onClick={(e) => { e.preventDefault(); e.stopPropagation(); reopenGoal.mutate(g.id); }}
                    >
                      重开
                    </button>
                  )}
                  <button
                    className="text-xs text-ink-3 hover:text-red-400 transition-colors disabled:opacity-50"
                    disabled={deleteGoalMut.isPending}
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      if (confirm(`删除 Goal「${g.title}」？级联删除其全部 run/评论/子目标，不可恢复。`)) deleteGoalMut.mutate(g.id);
                    }}
                  >
                    删除
                  </button>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
      </SectionCard>
        </>
      )}

      {(cancelGoal.isError || reopenGoal.isError || deleteGoalMut.isError) && (
        <p className="text-sm text-red-400 mt-2">
          {String(cancelGoal.error ?? reopenGoal.error ?? deleteGoalMut.error)}
        </p>
      )}

      {/* 多选批量操作条（决策 7-6）：选中项 > 0 时出现，浮动底部 */}
      <GoalBatchBar
        goals={goals ?? []}
        selected={selected}
        onClear={() => setSelected(new Set())}
        onSelectionChange={setSelected}
        onBusyChange={setBatchBusyId}
      />
    </PageBody>
  );
}
