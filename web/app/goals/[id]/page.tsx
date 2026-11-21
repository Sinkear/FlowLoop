"use client";

import { Suspense, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { useGoal, useAgents, useSquads, useDomains, useGoalEvents, useGoalWorkflow, useBlueprintGraph, useHeldGates, useResolveHeldGate, usePlanAlignment, useEnvChanges } from "@/lib/queries";
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
import type { Goal, HeldGate } from "@/lib/types";

// ── scratch 环境变更账本（BUG-4 P2）──
// 作业环境是 scratch 域的「交付物」：没有 git 分支/change 行。执行者用
// agentwork env-change record 记账（前/后版本、快照、manifest），这里把它
// 列出来，让一次交付能被归因、回滚与审计。
function EnvChangesCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = useEnvChanges(goalId);
  const changes = data ?? [];
  if (isLoading || changes.length === 0) return null;
  return (
    <SectionCard
      title="环境变更账本"
      count={changes.length}
      desc="执行者对作业环境的实变记录（agentwork env-change record）；含前/后版本、快照引用与 manifest sha256"
    >
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="text-left text-ink-3 border-b border-line">
              <th className="py-1 pr-3 font-medium">环境</th>
              <th className="py-1 pr-3 font-medium">版本</th>
              <th className="py-1 pr-3 font-medium">状态</th>
              <th className="py-1 pr-3 font-medium">执行者</th>
              <th className="py-1 font-medium">开始</th>
            </tr>
          </thead>
          <tbody>
            {changes.map((c) => (
              <tr key={c.id} className="border-b border-line/50 align-top">
                <td className="py-1 pr-3 font-mono text-ink">{c.env_id}</td>
                <td className="py-1 pr-3 text-ink-2">
                  {c.before_version || "—"} → {c.after_version || "—"}
                </td>
                <td className="py-1 pr-3">
                  <span className={c.status === "succeeded" ? "text-emerald-400" : c.status === "failed" ? "text-red-300" : "text-ink-2"}>
                    {c.status}
                  </span>
                </td>
                <td className="py-1 pr-3 font-mono text-ink-2">{c.operator || "—"}</td>
                <td className="py-1 text-ink-3">{c.started_at ? new Date(c.started_at).toLocaleString() : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {changes.some((c) => c.error) && (
        <p className="mt-2 text-xs text-red-300">
          {changes.filter((c) => c.error).map((c) => `${c.env_id}: ${c.error}`).join("；")}
        </p>
      )}
    </SectionCard>
  );
}

// ── 计划 vs 实际对账（BUG-4）──
// 以前「蓝本（计划）」与「编排（实际）」并排，靠人眼比较；这张卡把
// blueprint_node ↔ sub_goal ↔ run ↔ consult_request 逐节点 join 出来，
// 偏离（未物化/计划外子任务/节点无执行/咨询化执行）直接列成文字。
function PlanAlignmentCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = usePlanAlignment(goalId);
  if (isLoading || !data || !data.pinned) return null;
  const deviations = data.deviations ?? [];
  return (
    <SectionCard
      title="计划 vs 实际"
      count={data.planned}
      desc="逐节点对账：计划节点是否有对应的执行子任务与 run；「计划外子任务」「咨询化执行」都会列在下方"
    >
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-ink-2 mb-3">
        <span>计划节点 <span className="font-mono text-ink">{data.planned}</span></span>
        <span>已结算 <span className="font-mono text-ink">{data.settled}</span></span>
        <span>已落地 <span className="font-mono text-ink">{data.executed}</span></span>
        <span>计划外子任务 <span className="font-mono text-ink">{data.unplanned_sub_goals}</span></span>
        <span>咨询派发 <span className="font-mono text-ink">{data.consult_count}</span></span>
        {data.writes > 0 && (
          <span>
            交付提交 <span className="font-mono text-ink">{data.writes}</span>（归户{" "}
            <span className="font-mono text-ink">{data.attributed_writes}</span>）
          </span>
        )}
        {!data.materialized && (
          <span className="px-2 py-0.5 text-xs rounded-md bg-red-500/10 text-red-300">计划未物化</span>
        )}
      </div>
      {deviations.length > 0 ? (
        <ul className="mb-3 space-y-1">
          {deviations.map((d, i) => (
            <li key={i} className="text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded px-2 py-1">
              {d}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-emerald-400 mb-3">执行与计划一致（无偏离）。</p>
      )}
      {(data.unattributed_commits ?? []).length > 0 && (
        <ul className="mb-3 space-y-1">
          {data.unattributed_commits.map((c, i) => (
            <li key={i} className="text-xs text-ink-3 font-mono">
              {c}
            </li>
          ))}
        </ul>
      )}
      {data.nodes.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-ink-3 border-b border-line">
                <th className="py-1 pr-3 font-medium">节点</th>
                <th className="py-1 pr-3 font-medium">类型</th>
                <th className="py-1 pr-3 font-medium">状态</th>
                <th className="py-1 pr-3 font-medium">子任务</th>
                <th className="py-1 font-medium">Runs</th>
              </tr>
            </thead>
            <tbody>
              {data.nodes.map((n) => (
                <tr key={n.node_key} className="border-b border-line/50">
                  <td className="py-1 pr-3 font-mono text-ink">{n.node_key}</td>
                  <td className="py-1 pr-3 text-ink-2">{n.node_type}</td>
                  <td className="py-1 pr-3 text-ink-2">{n.state}</td>
                  <td className="py-1 pr-3 text-ink-2">
                    {n.sub_goal_id ? (n.sub_goal_status || "已映射") : <span className="text-amber-300">无</span>}
                  </td>
                  <td className="py-1 text-ink-2">{n.runs?.length ?? 0}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </SectionCard>
  );
}

export default function GoalDetailPage() {
  // useSearchParams 要求 Suspense 边界。
  return (
    <Suspense fallback={<div className="p-8 text-sm text-ink-3">加载中…</div>}>
      <GoalDetailPageInner />
    </Suspense>
  );
}

// ── 蓝本人工门决策卡（B-3，§4.2）──
// 蓝本里的 gate 节点持门等待时（state=running，held_since 已盖戳），
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
    <SectionCard
      title="等待你的决策"
      count={gates.length}
      desc="蓝本人工门：执行到这里停下来等人拍板。选一个动作放行对应分支；声明了自动判定的门会先按上游产出自动决策，超时未决策则按超时策略处理"
    >
      <div className="flex flex-col gap-4">
        {gates.map((g) => (
          <div key={g.node_key} className="p-4 rounded-lg bg-amber-500/5 border border-amber-500/30">
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
          className="w-full px-3 py-2 text-sm rounded-md bg-bg-2 border border-line text-ink placeholder:text-ink-4"
        />
        {err && (
          <p className="text-sm text-rose-400 whitespace-pre-wrap">{err}</p>
        )}
        {resolve.isPending && <p className="text-xs text-ink-3">正在落库决策…</p>}
      </div>
    </SectionCard>
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
        <p className="text-xs text-ink-3 mt-1">
          验收级别优先级：任务级覆盖 &gt; 项目默认 &gt; 平台默认（HITL）。
          {(() => {
            const domain = domains?.find((d) => d.id === goal.domain_id);
            const source = !domain
              ? "平台默认"
              : domain.acceptance_level === goal.category
                ? "与项目一致"
                : domain.acceptance_level
                  ? "任务级覆盖"
                  : "项目未设置 → 平台默认";
            return <span className="ml-1 text-ink-2">当前：{source}</span>;
          })()}
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

      {/* 蓝本人工门决策卡（B-3）：有持门的 gate 时出现，没有就不占地方 */}
      <GateDecisionCard goalId={id} />

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

      {/* 计划 vs 实际（BUG-4）：蓝本目标的逐节点对账，偏离直接可见 */}
      {goal.blueprint_version_id && <PlanAlignmentCard goalId={id} />}

      {/* scratch 环境变更账本（BUG-4 P2）：有记录才渲染 */}
      <EnvChangesCard goalId={id} />

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
