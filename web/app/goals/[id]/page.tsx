"use client";

import { Suspense, useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { AlertTriangle } from "lucide-react";
import { useGoal, useAgents, useSquads, useDomains, useGoalEvents, useGoalWorkflow, useBlueprintGraph, useHeldGates, useResolveHeldGate, usePlanAlignment } from "@/lib/queries";
import { parseHandoffNote } from "@/lib/utils";
import { GoalActions } from "@/components/goal-actions";
import { GoalComments } from "@/components/goal-comments";
import { GoalContextRail, type GoalContextTab } from "@/components/goal-context-rail";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
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
//   编排图        全宽 SectionCard：1:1 读图，超宽图用「适应宽度 / 全屏」
//   主体 grid     左：GoalComments（固定高度，内部自滚）
//                 右：GoalContextRail（执行 / 计划 / 详情，子任务+运行+对账）

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
  // 计划偏差提示（警示区）与右栏「计划」tab 共用同一份对账数据（同一 query）。
  const { data: alignment } = usePlanAlignment(id);
  const [railTab, setRailTab] = useState<GoalContextTab>("exec");
  // 编排卡双视图：实时执行图 / 计划图（钉了工作流才有计划 tab）。
  const [graphTab, setGraphTab] = useState<"exec" | "plan">("exec");

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

          {/* 操作 + 执行 flow：正常态右对齐成一行；review 态的审批面板本身就是
              全宽决策区（GoalActions 内部 w-full），让它独占一行。 */}
          <div
            className={
              goal.status === "review"
                ? "flex w-full flex-wrap items-center gap-2"
                : "flex shrink-0 flex-wrap items-center justify-end gap-2"
            }
          >
            <GoalActions goal={goal} />
            <GoalTimeline goalId={id} goalStatus={goal.status} />
          </div>
        </div>
      </header>

      {/* ── 警示区（有才渲染）：持门决策 > handoff / 计划偏差 ── */}
      <GateDecisionCard goalId={id} />

      {(goal.handoff_note || hasPlanWarning) && (
        <div className="flex shrink-0 flex-col gap-2 lg:flex-row lg:flex-wrap lg:items-stretch">
          {goal.handoff_note && (() => {
            const note = parseHandoffNote(goal.handoff_note);
            return (
              <div
                className="flex min-w-0 items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.08] px-3 py-2 lg:max-w-[62%]"
                title={
                  note.kind === "reopen"
                    ? "任务从终态（完成/失败/取消）重新打开，回到进行中并安排继续执行"
                    : "换负责人时留下的交接说明"
                }
              >
                <span className="mt-0.5 shrink-0 text-[11px] font-medium text-amber-200">
                  {note.kind === "reopen" ? "重新打开" : "交接备注"}
                </span>
                <p className="line-clamp-2 min-w-0 flex-1 text-xs leading-relaxed text-amber-100">
                  {note.kind === "reopen"
                    ? note.reason || "任务重新回到进行中，智能体会继续跟进。"
                    : note.text}
                </p>
              </div>
            );
          })()}
          {hasPlanWarning && (
            <button
              type="button"
              onClick={() => setRailTab("plan")}
              className="flex min-w-0 items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[0.08] px-3 py-2 text-left text-xs text-amber-200 transition-colors hover:border-amber-500/50 hover:bg-amber-500/[0.12]"
            >
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-amber-300" />
              <span className="shrink-0 font-medium">
                {alignment?.materialized
                  ? `计划偏差 ${deviations.length} 处`
                  : "计划未物化"}
              </span>
              <span className="min-w-0 truncate text-amber-300/80">
                计划与实际不一致，点「计划」查看逐节点对账
              </span>
            </button>
          )}
        </div>
      )}

      {/* ── 编排图（全宽，实时执行 + 计划放在一起）：从右栏上移，读图不再
          挤在 400px 窄栏里。实时执行 / 计划 两个 tab 切换；没钉工作流的
          自动拆解任务只有实时图。全宽下 1:1 渲染（不 autoFit：小图会被
          fit-width 顶到 200% 上限放大成巨块），横向超宽的图用工具条
          「适应宽度 / 全屏」。 */}
      {(workflowLoading || workflowError || workflow ||
        (goal.blueprint_version_id && (bpGraphLoading || blueprintGraph))) && (
        <SectionCard
          title="编排"
          count={
            graphTab === "plan"
              ? blueprintGraph?.nodes?.length
              : workflow?.nodes?.length
          }
          desc={
            graphTab === "plan"
              ? "计划图：激活时钉住的工作流规范（应然结构）；与实时执行对照看计划 vs 实际"
              : "只读执行图：节点是子任务/验证/审查/咨询/集成，边是依赖与时序；任务终态后迭代会冻结同一张图"
          }
          descInBody
          bodyClassName="p-0"
          titleExtra={
            goal.blueprint_version_id ? (
              <Segmented
                value={graphTab}
                onChange={setGraphTab}
                options={[
                  { value: "exec", label: "实时执行" },
                  { value: "plan", label: "计划" },
                ]}
              />
            ) : undefined
          }
        >
          {graphTab === "plan" ? (
            <WorkflowGraphView
              graph={blueprintGraph ?? null}
              loading={bpGraphLoading}
              error={
                bpGraphError
                  ? bpGraphErr instanceof Error
                    ? bpGraphErr.message
                  : String(bpGraphErr)
                  : null
              }
              variant="spec"
              showMetrics={false}
              heightClass="min-h-[300px]"
            />
          ) : (
            <WorkflowGraphView
              graph={workflow}
              loading={workflowLoading}
              error={
                workflowError
                  ? workflowErr instanceof Error
                    ? workflowErr.message
                  : String(workflowErr)
                  : null
              }
              heightClass="min-h-[300px]"
            />
          )}
        </SectionCard>
      )}

      {/* ── 主体：左对话（主要宽度）+ 右上下文栏（只留一小条）。
          并排从 1024（lg）就开始：用户期望的标准版式就是「上编排、
          左会话主宽、右上下文一小栏」；再窄才退化为上下堆叠。 */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_360px] xl:grid-cols-[minmax(0,1fr)_400px] 2xl:grid-cols-[minmax(0,1fr)_440px]">
        {/* 固定尺寸的对话面板：用户要求至少和上方编排区差不多高，之后又要求
            再拉高 1/3 —— 编排卡自然高约 670px，700 → 930px；矮屏下整页滚动，
            面板自身不缩。 */}
        <div className="flex h-[930px] flex-col">
          <GoalComments goalId={id} className="min-h-0" />
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
    </PageBody>
  );
}