"use client";

import { Suspense } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { useGoal, useAgents, useSquads, useGoalEvents, useGoalWorkflow, useBlueprintGraph } from "@/lib/queries";
import { GoalActions } from "@/components/goal-actions";
import { GoalComments } from "@/components/goal-comments";
import { GoalRuns } from "@/components/goal-runs";
import { GoalSubGoals } from "@/components/goal-subgoals";
import { GoalChanges } from "@/components/goal-changes";
import { GoalHooksPanel } from "@/components/goal-hooks-panel";
import { GoalStatusBar, GoalTimeline } from "@/components/goal-timeline";
import { CategoryBadge } from "@/components/category-badge";
import { Badge, AttentionChip, PageBody, SectionCard } from "@/components/ui";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import type { Goal } from "@/lib/types";

export default function GoalDetailPage() {
  // useSearchParams 要求 Suspense 边界。
  return (
    <Suspense fallback={<div className="p-8 text-sm text-ink-3">加载中…</div>}>
      <GoalDetailPageInner />
    </Suspense>
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
  const {
    data: workflow,
    isLoading: workflowLoading,
    isError: workflowError,
    error: workflowErr,
  } = useGoalWorkflow(id);
  // 蓝本规范图（Phase A）：goal 钉了 published 版本时展示「计划态」——
  // 与下面的执行图（实际怎么跑）对照。未钉时 enabled=false 不发请求。
  const {
    data: blueprintGraph,
    isLoading: bpGraphLoading,
    isError: bpGraphError,
    error: bpGraphErr,
  } = useBlueprintGraph(goal?.blueprint_version_id ?? "");

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

  return (
    <PageBody>
      {/* Breadcrumb */}
      <div>
        {/* 从带筛选的列表进来（?status=…）时，返回列表保持筛选不丢 */}
        <Link
          href={searchParams.get("status") ? `/goals?status=${searchParams.get("status")}` : "/goals"}
          className="text-sm text-ink-3 hover:text-ink-2 hover:underline"
        >
          ← 返回列表
        </Link>
        <div className="flex items-center gap-3 mt-3">
          <h1 className="text-lg font-semibold text-ink">{goal.title}</h1>
          <Badge status={goal.status} />
          <CategoryBadge category={goal.category} />
          <AttentionChip attention={goal.attention} />
          <GoalStatusBar goalId={id} goalStatus={goal.status} />
        </div>
        <p className="text-sm text-ink-3 mt-1">
          分配给：{assigneeLabel}
        </p>
        {goal.blueprint_version_id && (
          <p className="text-xs text-ink-3 mt-1">
            蓝本：<span className="font-mono">{goal.blueprint_version_id}</span>
            （激活时按规范图编译；子任务创建被结构拒绝）
          </p>
        )}
        {goal.source_ref && (
          <p className="text-xs text-ink-3 mt-1">
            来源：<span className="font-mono">{goal.source_ref}</span>
          </p>
        )}
        {goal.description && (
          <p className="text-sm text-ink-2 mt-3 whitespace-pre-wrap">{goal.description}</p>
        )}
        {goal.handoff_note && (
          <div className="mt-3 p-3 bg-amber-500/10 border border-amber-500/30 rounded-lg text-sm text-amber-300 whitespace-pre-wrap">
            <span className="font-medium">Handoff note：</span>
            {goal.handoff_note}
          </div>
        )}
      </div>

      {/* Actions + 执行流入口 */}
      <div className="flex items-start gap-2 flex-wrap">
        <GoalActions goal={goal} />
        <GoalTimeline goalId={id} goalStatus={goal.status} />
      </div>

      {/* Comments */}
      <GoalComments goalId={id} />

      {/* Sub-goals（v2：goal 内部拆出的工作项，不是 child goal） */}
      <GoalSubGoals goalId={id} />

      {/* Changes（v2：子任务的逻辑交付物 + 修订史——owner 的集成视图） */}
      <GoalChanges goalId={id} />

      {/* 蓝本：规范图（计划态）——只在 goal 钉了蓝图时出现（Phase A） */}
      {goal.blueprint_version_id && (
        <SectionCard
          title="蓝本（计划）"
          count={blueprintGraph?.nodes?.length}
          desc="激活时钉住的规范图：执行顺序由结构保证。与下方执行图对照可看出「计划 vs 实际」"
          bodyClassName="p-0"
        >
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
          />
        </SectionCard>
      )}

      {/* 编排：live DAG（训练分析里冻结的是同一张图的终态版本） */}
      <SectionCard
        title="编排"
        count={workflow?.nodes?.length}
        desc="只读编排图：节点是子任务/验证/审查/咨询/集成，边是依赖与时序；任务终态后训练分析会冻结同一张图"
        bodyClassName="p-0"
      >
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
        />
      </SectionCard>

      {/* Hooks（v1：任务结束后的自定义脚本——生效绑定 = goal 级 + 项目继承） */}
      <GoalHooksPanel goalId={id} goalStatus={goal.status} />

      {/* Runs */}
      <GoalRuns goalId={id} />
    </PageBody>
  );
}
