"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { AlertTriangle, RotateCcw, X } from "lucide-react";
import { useGoal, useAgents, useSquads, useDomains, useGoalEvents, useGoalWorkflow, useBlueprintGraph, useHeldGates, useResolveHeldGate, usePlanAlignment } from "@/lib/queries";
import { parseHandoffNote } from "@/lib/utils";
import { GoalActions, ReviewPanel } from "@/components/goal-actions";
import { GoalComments } from "@/components/goal-comments";
import { GoalContextRail, type GoalContextTab } from "@/components/goal-context-rail";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { PlanAlignmentCard } from "@/components/plan-alignment-card";
import { GoalStatusBar, GoalTimeline } from "@/components/goal-timeline";
import { CategoryBadge } from "@/components/category-badge";
import { Badge, AttentionChip, SectionCard, PageBody, Segmented } from "@/components/ui";
import type { Goal, HeldGate } from "@/lib/types";

// ── 任务页 = 文档流：编排图全宽在前，对话面板固定高 ─────────────
//
// 一屏式「左对话 + 右上下文」工作台实践下来，编排图挤在 400px 窄栏里
// 没法读。这版把图从右栏拿出来、全宽放在对话上方，页面恢复普通滚动
// 文档（不再是定高一屏）；对话面板保留固定高度、内部自己滚，行内容
// 宽度回到消息列自己的 max-w。
//
// 版面（整页可滚）：
//   顶栏          返回 / 标题 + badges + 持有者状态 / 元信息 / 操作 + 执行 flow
//   警示区        持门决策卡、handoff、计划偏差（有才渲染）
//   计划与执行    全宽 SectionCard：执行图/规范图（上下双图）与「计划 vs
//                 实际」对账表 tab 切换（对账明细原在右栏"计划"tab，
//                 2026-11-28 用户反馈后并入此卡同级 tab）
//   主体 grid     左：GoalComments（固定高度，内部自滚）
//                 右：GoalContextRail（执行 / 详情，子任务+运行）

export default function GoalDetailPage() {
  // useSearchParams 要求 Suspense 边界。
  return (
    <Suspense fallback={<div className="p-8 text-sm text-ink-3">加载中…</div>}>
      <GoalDetailPageInner />
    </Suspense>
  );
}

// ── 工作流人工门决策卡（B-3，§4.2）──
// 工作流里的 gate 节点持门等待时（state=running，held_since 已盖戳），
// 这张卡就是人拍板的入口。每个 action 是一个按钮；拍板后：
// 服务端落 chosen action + 剪掉未选分支 + 释放选中分支（执行图立刻变化）。
// 超时无人拍板时 sweeper 自动执行 timeout_action（decided_by=system）。
function GateDecisionCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = useHeldGates(goalId);
  const resolve = useResolveHeldGate(goalId);
  const [reason, setReason] = useState("");
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const gates = data?.gates ?? [];
  if (isLoading || gates.length === 0) return null;

  const decide = (g: HeldGate, action: string) => {
    setBusyKey(g.node_key + ":" + action);
    setErr(null);
    resolve.mutate(
      { nodeKey: g.node_key, action, reason: reason.trim() || undefined },
      {
        onError: (e) => setErr(e instanceof Error ? e.message : String(e)),
        onSettled: () => setBusyKey(null),
      }
    );
  };

  return (
    <div id="held-gates">
    <SectionCard
      title="等待你的决策"
      count={gates.length}
      desc="工作流人工门：执行到这里停下来等人拍板。选一个动作放行对应分支；声明了自动判定的门会先按上游产出自动决策，超时未决策则按超时策略处理"
    >
      <div className="flex flex-col gap-4 p-3">
        {gates.map((g) => (
          <div key={g.node_key} className="p-3 rounded-lg bg-amber-500/5 border border-amber-500/30">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm font-medium text-ink">{g.title}</span>
              <span className="font-mono text-xs text-ink-3">{g.node_key}</span>
              {g.held_since && (
                <span className="text-xs text-ink-3">
                  等待中（自 {new Date(g.held_since).toLocaleString()}）
                </span>
              )}
            </div>
            <p className="mt-2 text-sm text-ink-2 whitespace-pre-wrap">{g.prompt}</p>
            {g.timeout_after && (
              <p className="mt-1 text-xs text-ink-3">
                超过 {g.timeout_after} 无人决策 → {" "}
                {g.timeout_action ? `自动「${g.timeout_action}」` : "仅标记挂起（park）"}
              </p>
            )}
            {g.auto_when && (
              <p className="mt-1 text-xs text-emerald-300/90 font-mono">
                自动判定：{g.auto_when} → 「{g.auto_action}」
              </p>
            )}
            <div className="mt-3 flex items-center gap-2 flex-wrap">
              {g.actions.map((a) => (
                <button
                  key={a.id}
                  disabled={!!busyKey}
                  onClick={() => decide(g, a.id)}
                  className={`px-3 py-1.5 text-sm rounded-md border transition ${
                    a.id === g.timeout_action
                      ? "border-amber-500/40 bg-amber-500/10 text-amber-200 hover:bg-amber-500/20"
                      : "border-emerald-500/40 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20"
                  } disabled:opacity-50`}
                >
                  {busyKey === g.node_key + ":" + a.id ? "提交中…" : a.label}
                </button>
              ))}
            </div>
          </div>
        ))}
        <input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="决策理由（可选，会记录进决策快照）"
          className="w-full px-3 py-2 text-sm rounded-md bg-surface-2 border border-line text-ink placeholder:text-ink-3"
        />
        {err && (
          <p className="text-sm text-rose-400 whitespace-pre-wrap">{err}</p>
        )}
        {resolve.isPending && <p className="text-xs text-ink-3">正在落库决策…</p>}
      </div>
    </SectionCard>
    </div>
  );
}

function GoalDetailPageInner() {
  useGoalEvents();
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const id = params.id;
  const { data: goal, isLoading } = useGoal(id);
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();
  const {
    data: workflow,
    isLoading: workflowLoading,
    isError: workflowError,
    error: workflowErr,
  } = useGoalWorkflow(id);
  // 工作流规范图（Phase A）：goal 钉了 published 版本时展示「计划态」——
  // 与执行图（实际怎么跑）对照。未钉时 enabled=false 不发请求。
  const {
    data: blueprintGraph,
    isLoading: bpGraphLoading,
    isError: bpGraphError,
    error: bpGraphErr,
  } = useBlueprintGraph(goal?.blueprint_version_id ?? "");
  // 计划偏差提示（警示区）与「计划 vs 实际」tab 共用同一份对账数据
  // （同一 query）。三态解构齐全：tab 内的加载/出错兜底（MINOR-6）要用。
  const {
    data: alignment,
    isLoading: alignmentLoading,
    isError: alignmentError,
  } = usePlanAlignment(id);
  const [railTab, setRailTab] = useState<GoalContextTab>("exec");
  // 计划与执行卡视图（2026-11-28 用户反馈重构）：
  //   graphs   = 执行图与规范图（计划）上下同框，1:1 同缩放直接对比；
  //   align    = 计划 vs 实际逐节点对账表（从右栏移上来）。
  const [planView, setPlanView] = useState<"graphs" | "align">("graphs");

  // 重开提示 = 一次性 toast，页面上不常驻（用户反馈）。handoff_note 是
  // "Reopened"/"Reopened: X" 时弹一下告诉人，6 秒自己走；同一会话对同一条
  // 备注只弹一次。真交接备注（有人留下的内容）不弹，留在右栏「详情」里。
  //
  // 盖戳时机很关键：必须在 toast 真正展示完成后（自动消失/手动关闭）才写
  // sessionStorage「已看过」。如果在弹之前就写，StrictMode/Suspense 的
  // 卸载重挂会让第二挂载周期看到标记直接 return——提示被整个吞掉
  // （实测复现）。重挂载场景里两次 setReopenToast 只有存活周期的那次会
  // 渲染出来，用户始终只看到一条。
  const [reopenToast, setReopenToast] = useState<string | null>(null);
  const reopenSeenKeyRef = useRef<string | null>(null);
  const consumeReopenToast = () => {
    const key = reopenSeenKeyRef.current;
    if (!key) return;
    try {
      sessionStorage.setItem(key, "1");
    } catch {
      // 隐私模式禁写 sessionStorage：下次进页会再弹一次，可接受
    }
  };
  useEffect(() => {
    if (!goal?.handoff_note) return;
    const note = parseHandoffNote(goal.handoff_note);
    if (note.kind !== "reopen") return;
    const key = `flowloop-reopen-toast:${id}:${goal.handoff_note}`;
    try {
      if (sessionStorage.getItem(key)) return;
    } catch {
      // 读失败按未看过处理
    }
    reopenSeenKeyRef.current = key;
    setReopenToast(note.reason || "任务重新回到进行中，智能体会继续跟进。");
  }, [goal?.handoff_note, id]);
  useEffect(() => {
    if (!reopenToast) return;
    const t = setTimeout(() => {
      setReopenToast(null);
      consumeReopenToast();
    }, 6000);
    return () => clearTimeout(t);
  }, [reopenToast]);

  // 终态任务的首屏重点是「这一轮到底做成了什么」——迭代卡在「详情」tab 里，
  // 默认停在「执行」会把它藏起来。只在任务首次进入终态时切一次，之后人手动
  // 切回「执行」不再被顶走。
  const terminalStatus = goal?.status;
  useEffect(() => {
    if (terminalStatus === "done" || terminalStatus === "failed" || terminalStatus === "cancelled") {
      setRailTab((current) => (current === "exec" ? "details" : current));
    }
  }, [terminalStatus]);

  if (isLoading) return <div className="p-8 text-sm text-ink-3">加载中…</div>;
  if (!goal) return <div className="p-8 text-sm text-ink-3">找不到 Goal。</div>;

  // 已删除 agent 的 goal 记录仍在：id 非空但查不到时显示「已删除 agent」，
  // 不再把 uuid 露给人看。agents 尚未加载时退回 id（这不是"已删除"）。
  const agentName = (aid: string) =>
    agents?.find((a) => a.id === aid)?.name ?? (aid ? (agents ? "已删除 agent" : aid) : "");
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;
  const assigneeLabel = goal.assignee_type === "squad"
    ? (squadName(goal.assignee_id) || goal.assignee_id || "-")
    : goal.assignee_id
      ? (agentName(goal.assignee_id) || goal.assignee_id)
      : "-";
  const domain = domains?.find((d) => d.id === goal.domain_id);
  const projectName = domain?.name || (goal.domain_id ? "未知项目" : "—");
  // 验收级别的生效来源（任务级覆盖 > 项目默认 > 平台默认），详情 tab 里展示。
  const acceptanceSource = !domain
    ? "平台默认"
    : domain.acceptance_level === goal.category
      ? "与项目一致"
      : domain.acceptance_level
        ? "任务级覆盖"
        : "项目未设置 → 平台默认";

  const deviations = alignment?.deviations ?? [];
  const hasPlanWarning =
    !!goal.blueprint_version_id && !!alignment?.pinned && (!alignment.materialized || deviations.length > 0);

  // 计划与执行卡的副标题与两图 error 文案：先算后用（MINOR-7：三元
  // 嵌套读不出层次，提到 JSX 之前的具名 const / helper）。
  const planDesc =
    planView === "align"
      ? goal.blueprint_version_id
        ? "逐节点对账：计划节点是否有对应的执行子任务与 run；计划外子任务/咨询化执行都会列出来"
        : "自动拆解任务没有钉住的工作流，暂无逐节点计划对账——下方说明了怎么才有"
      : goal.blueprint_version_id
        ? "上：实时执行图（实际怎么跑）；下：计划图（钉住的工作流规范，应然结构）——上下对照看计划 vs 实际"
        : "只读执行图：节点是子任务/验证/审查/咨询/集成，边是依赖与时序；任务终态后迭代会冻结同一张图";
  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

  return (
    <PageBody className="space-y-2.5 px-4 py-4 sm:px-6">
      {/* ── 顶栏（shrink-0）：返回 / 标题 + 状态 / 元信息 / 操作 ── */}
      <header className="shrink-0 space-y-1.5">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0 flex-1 space-y-1">
            {/* 从带筛选的列表进来（?status=…）时，返回列表保持筛选不丢 */}
            <Link
              href={searchParams.get("status") ? `/goals?status=${searchParams.get("status")}` : "/goals"}
              className="inline-flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-ink-2 hover:underline"
            >
              ← 任务列表
            </Link>

            <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5">
              <h1 className="min-w-0 truncate text-lg font-semibold text-ink" title={goal.title}>
                {goal.title}
              </h1>
              <Badge status={goal.status} />
              <CategoryBadge category={goal.category} />
              <AttentionChip attention={goal.attention} />
              <GoalStatusBar goalId={id} goalStatus={goal.status} />
            </div>

            {/* 问题描述（"必要描述"）：mention 语法压平成 @名字，多行折叠为一段，
                最多两行——完整的原始描述在「详情」tab 里。 */}
            {(() => {
              const desc = (goal.description || "")
                .replace(/\[@([^\]]+)\]\(mention:\/\/[^)]*\)/g, "@$1")
                .replace(/[ \t]*\n[ \t]*/g, " ")
                .trim();
              if (!desc) return null;
              return (
                <p
                  className="line-clamp-2 min-w-0 text-xs leading-relaxed text-ink-3"
                  title={desc}
                >
                  {desc}
                </p>
              );
            })()}

            {/* 元信息行：项目 / 分配给 / 工作流 / 来源（紧凑，窄屏可换行） */}
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-3">
              <span className="inline-flex min-w-0 items-center gap-1">
                项目
                {goal.domain_id ? (
                  <Link
                    href={`/projects/${goal.domain_id}`}
                    className="min-w-0 truncate text-ink-2 transition-colors hover:text-accent"
                    title={domain?.name || goal.domain_id}
                  >
                    {projectName}
                  </Link>
                ) : (
                  <span className="text-ink-2">—</span>
                )}
              </span>
              <span className="min-w-0 truncate">
                分配给：<span className="text-ink-2">{assigneeLabel}</span>
                {goal.assignee_type === "squad" ? "（小队）" : goal.assignee_type === "agent" ? "（智能体）" : ""}
              </span>
              <span className="inline-flex min-w-0 items-center gap-1">
                工作流
                {goal.blueprint_version_id ? (
                  <>
                    {goal.blueprint_id ? (
                      <Link
                        href={`/workflows/${goal.blueprint_id}`}
                        className="min-w-0 truncate text-ink-2 transition-colors hover:text-accent"
                        title={goal.blueprint_version_id}
                      >
                        {goal.blueprint_name || goal.blueprint_id.slice(0, 8)}
                      </Link>
                    ) : (
                      // 固定版本被删除：不假装任务“自动拆解”，直说工作流已不存在。
                      <span className="text-amber-300" title={goal.blueprint_version_id}>
                        已删除
                      </span>
                    )}
                    <span className="shrink-0 font-mono text-ink-3" title={goal.blueprint_version_id}>
                      {goal.blueprint_version ? `v${goal.blueprint_version}` : goal.blueprint_version_id.slice(0, 8)}
                    </span>
                  </>
                ) : (
                  <span className="text-ink-2">自动拆解</span>
                )}
              </span>
              {goal.source_ref && (
                <span className="inline-flex min-w-0 items-center gap-1">
                  来源
                  <span className="max-w-[280px] truncate font-mono text-ink-2" title={goal.source_ref}>
                    {goal.source_ref}
                  </span>
                </span>
              )}
            </div>
          </div>

          {/* 操作 + 执行 flow：右对齐一行，允许换行。
              注意绝不能 w-full——父级是 lg:flex-row，w-full 会把左边
              min-w-0 flex-1 的标题块挤到 0 宽，标题/徽章/元信息全部
              垂直堆叠溢出，看起来就像"乱码"（2026-11-28 用户反馈）。 */}
          <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 lg:max-w-[46%]">
            <GoalActions goal={goal} />
            <GoalTimeline goalId={id} goalStatus={goal.status} />
          </div>
        </div>
      </header>

      {/* ── 警示区（有才渲染）：持门决策 > 计划偏差 ──
          重开不再放这里：handoff_note=Reopened 是一次性状态翻转，改为
          进页/出现时弹 toast（见页面末尾 reopenToast）。 */}
      <GateDecisionCard goalId={id} />

      {hasPlanWarning && (
        <button
          type="button"
          onClick={() => setPlanView("align")}
          className="flex shrink-0 min-w-0 items-center gap-2 rounded-lg border border-amber-500/40 bg-amber-500/[0.12] px-3 py-2 text-left text-xs text-amber-200 transition-colors hover:border-amber-500/60 hover:bg-amber-500/[0.18]"
        >
          <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-amber-300" />
          <span className="shrink-0 font-medium">
            {alignment?.materialized
              ? `计划偏差 ${deviations.length} 处`
              : "计划未物化"}
          </span>
          <span className="min-w-0 truncate text-amber-300/80">
            计划与实际不一致，点「计划 vs 实际」查看逐节点对账
          </span>
        </button>
      )}

      {/* ── 计划与执行（全宽，2026-11-28 用户反馈重构）──
          原名「编排」。用户的版式期望：
          ① 规范图（计划）和执行图上下排版，方便逐节点对比；
          ② 计划 vs 实际（逐节点对账）不再藏在右栏 tab 里，和两图同级
             tab 可见；
          ③ 节点缩小一档（NODE_W/H 已调小），框整体更高（执行图窗格
             min-h 300→380px）。
          自动拆解任务没有规范图/对账表时，tab 仍然显示，
          用空态说明"为什么没有、怎么才有"。 */}
      {(workflowLoading || workflowError || workflow ||
        (goal.blueprint_version_id && (bpGraphLoading || blueprintGraph))) && (
        <SectionCard
          title="计划与执行"
          count={
            planView === "align"
              ? alignment?.planned
              : workflow?.nodes?.length
          }
          desc={planDesc}
          descInBody
          bodyClassName="p-0"
          titleExtra={
            <Segmented
              value={planView}
              onChange={setPlanView}
              options={[
                { value: "graphs", label: "执行图 · 计划图" },
                { value: "align", label: "计划 vs 实际" },
              ]}
            />
          }
        >
          {planView === "align" ? (
            goal.blueprint_version_id ? (
              // 对账数据三态兜底（MINOR-6）：PlanAlignmentCard 只在
              // data 就绪且已钉住时渲染；加载中/出错时若不在这里
              // 接住，tab 会是一块无解释的空白。
              !alignment && alignmentLoading ? (
                <div className="flex items-center justify-center px-4 py-10 text-xs text-ink-3">
                  对账数据加载中…
                </div>
              ) : alignmentError ? (
                <div className="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
                  <p className="text-xs font-medium text-red-400">对账数据加载失败</p>
                  <p className="text-[11px] text-ink-3">稍后重试；也可以刷新页面重新拉取。</p>
                </div>
              ) : (
                <PlanAlignmentCard goalId={id} />
              )
            ) : (
              <div className="flex flex-col items-center justify-center gap-2.5 px-4 py-10 text-center">
                <span className="flex h-10 w-10 items-center justify-center rounded-xl border border-line bg-surface-2 text-ink-3">
                  <AlertTriangle className="h-4 w-4" />
                </span>
                <p className="text-xs font-medium text-ink-2">没有逐节点对账表</p>
                <p className="max-w-md text-[11px] leading-relaxed text-ink-3">
                  「计划 vs 实际」把钉住的工作流逐节点与实际执行对账（计划节点 ↔
                  子任务 ↔ runs、计划外子任务、偏差清单）。本任务是
                  <strong className="text-ink-2">自动拆解</strong>：创建时没有指定工作流，由
                  Agent 现场编排，没有计划基线可对。创建任务时选择（或事后给任务钉住）工作流后，这里会展示逐节点对账表。
                </p>
              </div>
            )
          ) : (
            <div className="divide-y divide-line">
              <WorkflowGraphView
                graph={workflow}
                loading={workflowLoading}
                error={workflowError ? errText(workflowErr) : null}
                heightClass="min-h-[380px]"
              />
              {goal.blueprint_version_id ? (
                <WorkflowGraphView
                  graph={blueprintGraph ?? null}
                  loading={bpGraphLoading}
                  error={bpGraphError ? errText(bpGraphErr) : null}
                  variant="spec"
                  showMetrics={false}
                  heightClass="min-h-[320px]"
                />
              ) : (
                <div className="flex flex-col items-center justify-center gap-2.5 px-4 py-10 text-center">
                  <span className="flex h-10 w-10 items-center justify-center rounded-xl border border-line bg-surface-2 text-ink-3">
                    <AlertTriangle className="h-4 w-4" />
                  </span>
                  <p className="text-xs font-medium text-ink-2">没有计划图（规范图）</p>
                  <p className="max-w-md text-[11px] leading-relaxed text-ink-3">
                    计划图是钉住的工作流规范（应然结构），与上方
                    <strong className="text-ink-2">实时执行图</strong>
                    上下对照就是逐节点的计划 vs 实际。本任务是自动拆解：Agent
                    现场编排、无预先计划。创建任务时选择（或事后钉住）工作流后，这里会与执行图并排显示。
                  </p>
                </div>
              )}
            </div>
          )}
        </SectionCard>
      )}

      {/* ── 主体：左对话（主要宽度）+ 右上下文栏（只留一小条）。
          并排从 1024（lg）就开始：用户期望的标准版式就是「上编排、
          左会话主宽、右上下文一小栏」；再窄才退化为上下堆叠。
          review 态：审批卡作为 reviewSlot 传进 GoalComments，渲染在
          消息流末尾——审批是对话的一部分（agent 汇报 → 人拍板），
          在对话内呈现，不再悬在对话面板外。 */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_360px] xl:grid-cols-[minmax(0,1fr)_400px] 2xl:grid-cols-[minmax(0,1fr)_440px]">
        {/* 固定尺寸的对话面板：用户要求至少和上方编排区差不多高，之后又要求
            再拉高 1/3 —— 编排卡自然高约 670px，700 → 930px；矮屏下整页滚动，
            面板自身不缩。 */}
        <div className="flex h-[930px] flex-col">
          <GoalComments
            goalId={id}
            className="min-h-0"
            reviewSlot={
              goal.status === "review" ? <ReviewPanel goal={goal} /> : undefined
            }
          />
        </div>
        {/* 并排时右栏与对话面板等高，内容在栏内自滚，
            不再垂成 2000px 的长条把页面拖长；堆叠态（<lg）恢复自然高度。 */}
        <GoalContextRail
          goal={goal}
          goalId={id}
          acceptanceSource={acceptanceSource}
          tab={railTab}
          onTabChange={setRailTab}
          className="lg:h-[930px]"
        />
      </div>

      {/* 重开 toast：portal 到 body——页面级 page-enter 等祖先若带 transform，
          fixed 会被劫持成 containing block（全屏浮层踩过同一坑）。 */}
      {reopenToast &&
        createPortal(
          <div
            role="status"
            aria-live="polite"
            className="fixed bottom-4 right-4 z-[60] w-[min(92vw,360px)] rounded-lg border border-emerald-500/40 bg-surface-1 p-3 shadow-2xl"
          >
            <div className="flex items-start gap-2.5">
              <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-400">
                <RotateCcw className="h-4 w-4" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium text-ink">任务已重新打开</div>
                <p className="mt-1 text-[12px] leading-5 text-ink-3">{reopenToast}</p>
              </div>
              <button
                type="button"
                aria-label="关闭提醒"
                onClick={() => {
                  consumeReopenToast();
                  setReopenToast(null);
                }}
                className="rounded p-1 text-ink-3 hover:bg-surface-2 hover:text-ink"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>,
          document.body
        )}
    </PageBody>
  );
}