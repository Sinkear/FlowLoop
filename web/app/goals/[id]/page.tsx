"use client";

import { Suspense, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import Link from "next/link";
import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { useGoal, useAgents, useSquads, useDomains, useGoalEvents, useGoalWorkflow, useBlueprintGraph, useHeldGates, useResolveHeldGate, usePlanAlignment, useEnvChanges } from "@/lib/queries";
import { GoalActions } from "@/components/goal-actions";
import { GoalComments } from "@/components/goal-comments";
import { GoalRuns } from "@/components/goal-runs";
import { GoalSubGoals } from "@/components/goal-subgoals";
import { GoalChanges } from "@/components/goal-changes";
import { GoalHooksPanel } from "@/components/goal-hooks-panel";
import { GoalStatusBar, GoalTimeline } from "@/components/goal-timeline";
import { CategoryBadge } from "@/components/category-badge";
import { Badge, AttentionChip, PageBody, SectionCard, StatStrip } from "@/components/ui";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { fmtInt, nodeKindMeta } from "@/components/training/format";
import type { Goal, HeldGate } from "@/lib/types";

// ── scratch 环境变更账本（BUG-4 P2）──
// 作业环境是 scratch 域的「交付物」：没有 git 分支/change 行。执行者用
// ouroflow env-change record 记账（前/后版本、快照、manifest），这里把它
// 列出来，让一次交付能被归因、回滚与审计。
function EnvChangesCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = useEnvChanges(goalId);
  const changes = data ?? [];
  if (isLoading || changes.length === 0) return null;
  return (
    <SectionCard
      title="环境变更账本"
      count={changes.length}
      desc="执行者对作业环境的实变记录（ouroflow env-change record）；含前/后版本、快照引用与 manifest sha256"
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
//
// 版式（2026-11-23）：统计、偏差、表格拆成三段，各段有自己的留白和描边——
// 旧版把六个统计塞进一行小字、偏差和「一致」提示各占一行、表格行距只有
// py-1，读起来像一坨；现在数字走 StatStrip，偏差走琥珀警示面板，表格
// 固定列宽 + 统一 padding，窄屏在卡片内横向滚动而不挤压列。

// run.role 的中文名（run.go 里的角色：owner/subgoal/verify/consult）。
const RUN_ROLE_LABEL: Record<string, string> = {
  owner: "主执行",
  subgoal: "子任务",
  verify: "验证",
  consult: "咨询",
  review: "审查",
};

// gate/condition/router/spawn/end 是持有者或分支点，本来就不会编译出执行
// 子任务；它们没有 sub_goal 是正常结构，不该像 task 节点一样报「未映射」。
const NODE_TYPES_WITHOUT_SUB_GOAL = new Set(["gate", "condition", "router", "spawn", "end"]);

function PlanAlignmentCard({ goalId }: { goalId: string }) {
  const { data, isLoading } = usePlanAlignment(goalId);
  if (isLoading || !data || !data.pinned) return null;
  const deviations = data.deviations ?? [];
  const commits = data.unattributed_commits ?? [];
  return (
    <SectionCard
      title="计划 vs 实际"
      count={data.planned}
      desc="逐节点对账：计划节点是否有对应的执行子任务与 run；「计划外子任务」「咨询化执行」都会列在下方"
      action={
        !data.materialized ? (
          <span className="inline-flex items-center gap-1 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-0.5 text-[11px] font-medium text-red-300">
            <AlertTriangle className="h-3 w-3" />
            计划未物化
          </span>
        ) : undefined
      }
    >
      <div className="space-y-4 p-4">
        {/* 统计单元：label + 数值 + 一行解释，不再挤成一行小字。 */}
        <StatStrip
          items={[
            { label: "计划节点", value: fmtInt(data.planned), hint: "蓝本编译产出" },
            { label: "已结算", value: fmtInt(data.settled), hint: "settled 或 skipped" },
            {
              label: "已落地",
              value: fmtInt(data.executed),
              // 拿 mapped 而不是 planned 当分母：gate/end 这类节点本来就不带
              // 执行子任务，用 planned 比会永远显示警告；executed < mapped
              // 说明节点绑定的子任务已不存在（或一任务被多节点重复引用）。
              tone: data.executed < data.mapped ? "warn" : "ok",
              hint: `已绑定子任务 ${fmtInt(data.mapped)}`,
            },
            {
              label: "计划外子任务",
              value: fmtInt(data.unplanned_sub_goals),
              tone: data.unplanned_sub_goals > 0 ? "warn" : "muted",
              hint: data.unplanned_sub_goals > 0 ? "不属于计划节点" : "没有计划外的",
            },
            { label: "咨询派发", value: fmtInt(data.consult_count), hint: "咨询不是执行" },
            {
              label: "交付提交",
              value: fmtInt(data.writes),
              tone: data.unattributed_writes > 0 ? "warn" : "default",
              hint: `归户 ${fmtInt(data.attributed_writes)} · 未归户 ${fmtInt(data.unattributed_writes)}`,
            },
          ]}
        />

        {/* 偏差：独立警示面板逐条列，不再和统计数字混在一起。 */}
        {deviations.length > 0 ? (
          <section className="rounded-lg border border-amber-500/30 bg-amber-500/[0.06]">
            <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-amber-500/20 px-3 py-2">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-amber-300" />
              <span className="text-xs font-medium text-amber-200">发现 {deviations.length} 处偏差</span>
              <span className="text-[11px] text-amber-300/70">计划与实际不一致，需要核对</span>
            </header>
            <ul className="divide-y divide-amber-500/15">
              {deviations.map((d, i) => (
                <li key={i} className="flex gap-2 px-3 py-2 text-xs leading-relaxed text-amber-100">
                  <span aria-hidden="true" className="mt-[5px] h-1 w-1 shrink-0 rounded-full bg-amber-400" />
                  <span className="min-w-0">{d}</span>
                </li>
              ))}
            </ul>
            {commits.length > 0 && (
              <div className="border-t border-amber-500/20 px-3 py-2">
                <div className="text-[10px] font-medium tracking-[0.06em] text-amber-300/80">
                  未归户的交付提交（{commits.length}）
                </div>
                <ul className="mt-1 space-y-0.5 font-mono text-[11px] leading-relaxed text-ink-3">
                  {commits.map((c, i) => (
                    <li key={i} className="break-all">
                      {c}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </section>
        ) : (
          <div className="flex items-center gap-2 rounded-lg border border-emerald-500/25 bg-emerald-500/[0.06] px-3 py-2 text-xs text-emerald-300">
            <CheckCircle2 className="h-3.5 w-3.5 shrink-0" />
            执行与计划一致，未发现偏离。
          </div>
        )}

        {/* 逐节点对账表。table-fixed + colgroup 固定列宽；宽度不够时整张表
            在卡片内横向滚动，节点标题和 runs 列表都不会被挤成竖排。 */}
        {data.nodes.length > 0 ? (
          <div className="overflow-x-auto rounded-lg border border-line">
            <table className="w-full min-w-[960px] table-fixed border-collapse text-xs">
              <colgroup>
                <col className="w-[34%]" />
                <col className="w-[104px]" />
                <col className="w-[112px]" />
                <col className="w-[144px]" />
                <col className="w-[248px]" />
              </colgroup>
              <thead className="bg-surface-2/40">
                <tr className="text-left text-[11px] text-ink-3">
                  <th className="px-3 py-2 font-medium">节点</th>
                  <th className="px-3 py-2 font-medium">类型</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                  <th className="px-3 py-2 font-medium">子任务</th>
                  {/* 末列是多行徽章，左对齐比右对齐更好扫读；右侧留 pr-5 防贴边。 */}
                  <th className="px-3 py-2 pr-5 font-medium">Runs</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line/60">
                {data.nodes.map((n) => {
                  const kind = nodeKindMeta(n.node_type);
                  const runs = n.runs ?? [];
                  return (
                    <tr key={n.node_key} className="align-top transition-colors hover:bg-surface-2/25">
                      <td className="px-3 py-2.5 align-top">
                        <div className="text-sm leading-snug text-ink" title={n.title || n.node_key}>
                          {n.title || n.node_key}
                        </div>
                        {n.title && n.title !== n.node_key && (
                          <div className="mt-0.5 truncate font-mono text-[10px] text-ink-3" title={n.node_key}>
                            {n.node_key}
                          </div>
                        )}
                      </td>
                      <td className="px-3 py-2.5 align-top">
                        <span
                          className={`inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] font-medium ${kind.badge}`}
                          title={kind.desc}
                        >
                          {kind.label}
                        </span>
                      </td>
                      <td className="px-3 py-2.5 align-top">
                        <Badge status={n.state} />
                      </td>
                      <td className="px-3 py-2.5 align-top">
                        {n.sub_goal_id ? (
                          n.sub_goal_status ? (
                            <Badge status={n.sub_goal_status} />
                          ) : (
                            <span className="text-ink-3">已映射 · 状态未知</span>
                          )
                        ) : NODE_TYPES_WITHOUT_SUB_GOAL.has(n.node_type) ? (
                          <span className="text-ink-3" title="该类型节点不承载执行子任务">
                            —
                          </span>
                        ) : (
                          <span className="font-medium text-amber-300">未映射</span>
                        )}
                      </td>
                      <td className="px-3 py-2.5 pr-5 align-top">
                        {runs.length > 0 ? (
                          <ul className="flex flex-col gap-1">
                            {runs.map((r) => (
                              <li
                                key={r.id}
                                className="flex items-center gap-1.5"
                                title={r.summary || `${r.role} · ${r.status}`}
                              >
                                <span className="w-10 shrink-0 text-[10px] text-ink-3">
                                  {RUN_ROLE_LABEL[r.role] ?? r.role}
                                </span>
                                <Badge status={r.status} className="px-1.5 py-0 text-[10px]" />
                              </li>
                            ))}
                          </ul>
                        ) : (
                          <span className="text-ink-3">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="rounded-lg border border-dashed border-line px-3 py-6 text-center text-xs text-ink-3">
            还没有可对账的计划节点。
          </p>
        )}
      </div>
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
