"use client";

import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { fmtInt, nodeKindMeta } from "@/components/training/format";
import { Badge, SectionCard, StatStrip } from "@/components/ui";
import { usePlanAlignment } from "@/lib/queries";

// ── 计划 vs 实际对账（BUG-4 / 2026-11-28 版式调整）──
// blueprint_node ↔ sub_goal ↔ run ↔ consult_request 逐节点 join 出来，
// 偏离（未物化/计划外子任务/节点无执行/咨询化执行）直接列成文字。
//
// 归属变迁：原先住在右栏「计划」tab 里，用户看不到（对照右栏 400px 窄
// 列 + 深层 tab，对账表 min-w-[960px] 几乎必然横向滚动）。现在移到任务
// 页顶部「计划与执行」卡，与规范图/执行图同级 tab（framed=false 时嵌在
// 大卡的 body 里，卡片自己不带框）。
//
// 版式（2026-11-23）：统计、偏差、表格拆成三段，各段有自己的留白和描边。
// 统计走 StatStrip（自动折行），对账表 min-w-[960px] 在卡片内横向滚动，
// 列不被挤成竖排。

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

export function PlanAlignmentCard({
  goalId,
  framed = true,
}: {
  goalId: string;
  /** false = 嵌进「计划与执行」卡的 body（外层已有框和标题），不重复画 SectionCard。 */
  framed?: boolean;
}) {
  const { data, isLoading } = usePlanAlignment(goalId);
  if (isLoading || !data || !data.pinned) return null;
  const deviations = data.deviations ?? [];
  const commits = data.unattributed_commits ?? [];

  const body = (
    <div className="space-y-4 p-4">
      {/* 统计单元：label + 数值 + 一行解释，不再挤成一行小字。 */}
      <StatStrip
        items={[
          { label: "计划节点", value: fmtInt(data.planned), hint: "工作流编译产出" },
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
  );

  if (!framed) return body;

  return (
    <SectionCard
      title="计划 vs 实际"
      count={data.planned}
      desc="逐节点对账：计划节点是否有对应的执行子任务与 run；「计划外子任务」「咨询化执行」都会列在下方"
      descInBody
      action={
        !data.materialized ? (
          <span className="inline-flex items-center gap-1 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-0.5 text-[11px] font-medium text-red-300">
            <AlertTriangle className="h-3 w-3" />
            计划未物化
          </span>
        ) : undefined
      }
    >
      {body}
    </SectionCard>
  );
}
