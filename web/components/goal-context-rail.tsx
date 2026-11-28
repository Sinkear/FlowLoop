"use client";

import Link from "next/link";
import { ArrowUpRight, ScrollText } from "lucide-react";
import { GoalSubGoals } from "@/components/goal-subgoals";
import { GoalRuns } from "@/components/goal-runs";
import { GoalChanges } from "@/components/goal-changes";
import { GoalHooksPanel } from "@/components/goal-hooks-panel";
import { CategoryBadge } from "@/components/category-badge";
import { fmtInt } from "@/components/training/format";
import { Badge, SectionCard, Segmented } from "@/components/ui";
import { useEnvChanges, useRsiSessions } from "@/lib/queries";
import { cn, parseHandoffNote } from "@/lib/utils";
import type { Goal } from "@/lib/types";

// ── 任务上下文右栏 ────────────────────────────────────────────────
//
// 执行图已经上移到页面顶部全宽区（见 goals/[id]/page.tsx），这里不再
// 放图；「计划 vs 实际」对账也随 2026-11-28 版式调整上移到顶部
// 「计划与执行」卡（右栏 400px 窄列 + 深层 tab 里没人找得到）。
// 这列只留「执行 / 详情」的结构化上下文：子任务、runs、变更、env、
// hooks、终态迭代。
//
// 高度：文档流页面里这一列随内容自然长高（页面整体滚动）。
export type GoalContextTab = "exec" | "details";

const TABS: { value: GoalContextTab; label: string }[] = [
  { value: "exec", label: "执行" },
  { value: "details", label: "详情" },
];

export function GoalContextRail({
  goal,
  goalId,
  acceptanceSource,
  tab,
  onTabChange,
  className,
}: {
  goal: Goal;
  goalId: string;
  /** 验收级别来源（任务级覆盖 / 与项目一致 / 平台默认…），由页面按 domain 算出。 */
  acceptanceSource: string;
  tab: GoalContextTab;
  onTabChange: (tab: GoalContextTab) => void;
  className?: string;
}) {
  return (
    <section
      aria-label="任务上下文"
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-line bg-surface-1",
        className
      )}
    >
      <header className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-2">
        <ScrollText className="h-3.5 w-3.5 shrink-0 text-ink-3" />
        <span className="shrink-0 text-[11px] font-medium tracking-wide text-ink-3">
          任务上下文
        </span>
        <div className="ml-auto">
          <Segmented value={tab} onChange={onTabChange} options={TABS} />
        </div>
      </header>

      {/* overscroll 不 contain：滚到顶/底后滚动链交给页面（同任务对话面板，
          contain 会把滚轮事件拦在栏内，整页滚不动）。 */}
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        {tab === "exec" && (
          <>
            {/* 子任务（v2：goal 内部拆出的工作项，不是 child goal） */}
            <GoalSubGoals goalId={goalId} />

            {/* Runs：谁在跑、跑了什么。执行图（编排）在页面上方的全宽区。 */}
            <GoalRuns goalId={goalId} />
          </>
        )}

        {tab === "details" && (
          <>
            {/* 描述 / 验收 / 来源 / handoff：任务的原始约定 */}
            <SectionCard title="任务说明">
              <div className="space-y-3 p-3">
                {goal.description ? (
                  <p className="whitespace-pre-wrap text-sm leading-relaxed text-ink-2">
                    {goal.description}
                  </p>
                ) : (
                  <p className="text-sm text-ink-3">没有描述。</p>
                )}

                <div className="space-y-1.5 border-t border-line pt-2.5 text-xs leading-relaxed">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="text-ink-3">验收级别</span>
                    <CategoryBadge category={goal.category} />
                    <span className="text-ink-2">当前来源：{acceptanceSource}</span>
                  </div>
                  <p className="text-[11px] text-ink-3">
                    优先级：任务级覆盖 &gt; 项目默认 &gt; 平台默认（HITL）。
                  </p>
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="text-ink-3">来源</span>
                    {goal.source_ref ? (
                      <span className="break-all font-mono text-[11px] text-ink-2">{goal.source_ref}</span>
                    ) : (
                      <span className="text-ink-2">—（平台内创建）</span>
                    )}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="text-ink-3">执行方式</span>
                    {goal.blueprint_version_id ? (
                      <span className="min-w-0 text-ink-2">
                        {goal.blueprint_id ? (
                          <Link
                            href={`/workflows/${goal.blueprint_id}`}
                            className="text-accent hover:underline"
                            title={goal.blueprint_version_id}
                          >
                            {goal.blueprint_name || goal.blueprint_id.slice(0, 8)}
                          </Link>
                        ) : (
                          <span className="text-amber-300" title={goal.blueprint_version_id}>
                            已删除的工作流
                          </span>
                        )}{" "}
                        <span className="font-mono text-[11px] text-ink-3">
                          {goal.blueprint_version ? `v${goal.blueprint_version}` : goal.blueprint_version_id.slice(0, 8)}
                        </span>
                        <span className="text-ink-3">（激活时编译成固定子任务）</span>
                      </span>
                    ) : (
                      <span className="text-ink-2">自动拆解（未使用工作流）</span>
                    )}
                  </div>
                </div>

                {goal.handoff_note && (() => {
                  const note = parseHandoffNote(goal.handoff_note);
                  // 重开备注没有实际内容（后端只写 "Reopened"），是一次性
                  // 状态翻转——页面顶部弹过 toast 就不在详情里常驻了。
                  if (note.kind === "reopen") return null;
                  return (
                    <div className="rounded-lg border border-amber-500/40 bg-amber-500/[0.12] px-3 py-2">
                      <div className="text-[11px] font-medium text-amber-200">
                        交接备注
                      </div>
                      <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-amber-100">
                        {note.text}
                      </p>
                    </div>
                  );
                })()}
              </div>
            </SectionCard>

            {/* 终态迭代：已生成 / 进行中 / 未开启三种情况都在这说清楚 */}
            <ReviewCard goal={goal} />

            {/* 变更（v2：子任务的逻辑交付物 + 修订史——owner 的集成视图） */}
            <GoalChanges goalId={goalId} />

            {/* scratch 环境变更账本（BUG-4 P2）：有记录才渲染 */}
            <EnvChangesCard goalId={goalId} />

            {/* Hooks（任务结束后的自定义脚本——生效绑定 = goal 级 + 项目继承） */}
            <GoalHooksPanel goalId={goalId} goalStatus={goal.status} />
          </>
        )}
      </div>
    </section>
  );
}

// ── scratch 环境变更账本（BUG-4 P2）──
// 作业环境是 scratch 域的「交付物」：没有 git 分支/change 行。执行者用
// flowloop env-change record 记账（前/后版本、快照、manifest），这里把它
// 列出来，让一次交付能被归因、回滚与审计。
//
// 右栏只有 ~400px：五列表格在这里必然被挤成竖排，所以窄栏用「一条环境
// 变更 = 一个紧凑块」的版式；字段（环境/版本/状态/执行者/时间/错误）一个不少。
function EnvChangesCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = useEnvChanges(goalId);
  const changes = data ?? [];
  if (isLoading || changes.length === 0) return null;
  return (
    <SectionCard
      title="环境变更账本"
      count={changes.length}
      desc="执行者对作业环境的实变记录（flowloop env-change record）；含前/后版本、快照引用与 manifest sha256"
      descInBody
    >
      <ul className="divide-y divide-line/60">
        {changes.map((c) => (
          <li key={c.id} className="px-3 py-2.5 text-xs">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="min-w-0 break-all font-mono text-ink">{c.env_id}</span>
              <span
                className={cn(
                  "shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium",
                  c.status === "succeeded"
                    ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
                    : c.status === "failed"
                      ? "border-red-500/30 bg-red-500/10 text-red-300"
                      : "border-line bg-surface-2 text-ink-2"
                )}
              >
                {c.status}
              </span>
            </div>
            <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-ink-3">
              <span className="font-mono">
                {c.before_version || "—"} → {c.after_version || "—"}
              </span>
              <span className="font-mono">{c.operator || "—"}</span>
              <span>{c.started_at ? new Date(c.started_at).toLocaleString() : "—"}</span>
            </div>
            {c.error && <p className="mt-1 break-all text-[11px] text-red-300">{c.error}</p>}
          </li>
        ))}
      </ul>
    </SectionCard>
  );
}

// 终态迭代卡（Phase A + C）：迭代会话只在「训练」模式任务激活时由
// RSI EnsureSession 创建（internal/service/rsi.go:154）。因此终态却没有
// 会话 = 本次未开启迭代，而不是“分析失败”。提案有两条生成路径（Phase C）：
// 分析完成时自动转换一部分，迭代页里也能对单条发现点「生成提案」。这里只说
// 时机与归属，不编造提案条数——条数在迭代页按真实数据渲染。
const REVIEW_TERMINAL = new Set(["done", "failed", "cancelled"]);

function ReviewCard({ goal }: { goal: Goal }) {
  const { data: sessions, isLoading } = useRsiSessions();
  // 一个 goal 只会有一个会话（EnsureSession 幂等），find 足够；轮询仍由 query 负责。
  const session = sessions?.find((s) => s.goal_id === goal.id);
  const terminal = REVIEW_TERMINAL.has(goal.status);
  // 会话列表还没回来时不能断言「本次未开启迭代」——那会把加载中误报成没有迭代。
  if (!session && (isLoading || !sessions)) return null;
  if (!session && !terminal) return null;

  const noBlueprint = !goal.blueprint_version_id;
  const view = session ? (
    <Link
      href={`/training/${session.id}`}
      className="inline-flex items-center gap-0.5 text-xs text-ink-2 transition-colors hover:text-accent"
    >
      查看迭代
      <ArrowUpRight className="h-3 w-3" />
    </Link>
  ) : null;

  return (
    <SectionCard title="迭代" action={view}>
      {session ? (
        <div className="space-y-1.5 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge status={session.status} />
            {session.status === "ready" ? (
              <span className="text-sm text-ink-2">
                迭代已生成：
                <span className="font-mono tabular-nums text-ink">{fmtInt(session.finding_count)}</span>{" "}
                个发现
              </span>
            ) : session.status === "analyzing" ? (
              <span className="text-sm text-ink-2">
                分析中：迭代正在分析编排图，页面会自动刷新；完成后可查看发现。
              </span>
            ) : session.status === "waiting" ? (
              <span className="text-sm text-ink-2">
                等待中：任务进入终态后才会冻结编排图并开始分析。
              </span>
            ) : session.status === "failed" ? (
              <span className="text-sm text-ink-2">
                迭代分析失败{session.error ? `：${session.error}` : "，没有可展示的发现"}。
              </span>
            ) : (
              <span className="text-sm text-ink-2">
                本次迭代被跳过（未开启迭代或采样未命中），没有分析报告。
              </span>
            )}
          </div>
          {session.status === "ready" && !noBlueprint && (
            <p className="text-xs leading-relaxed text-ink-3">
              提案有两处来源：分析完成时会自动转换一部分结构类发现；迭代页里也能对单条发现点「
              生成提案」，再由人工在
              <Link href={`/workflows/${session.blueprint_id}#wf-proposals`} className="text-accent hover:underline">
                工作流
              </Link>
              的提案区审阅。
            </p>
          )}
        </div>
      ) : (
        <div className="space-y-1.5 p-3">
          <p className="text-sm text-ink-2">
            本次未开启迭代。适合一次性/探索性任务；不会产生优化提案。
          </p>
          <p className="text-xs leading-relaxed text-ink-3">
            迭代只在勾选「完成后迭代工作流」的任务上开启；这次执行没有迭代会话，因此也不会产生提案。
          </p>
        </div>
      )}
      {noBlueprint && (
        <p className="px-3 pb-3 text-xs leading-relaxed text-ink-3">
          本次没有使用工作流（自动拆解），发现不能转成提案；可在工作流页把这次执行固化成模板。
        </p>
      )}
    </SectionCard>
  );
}