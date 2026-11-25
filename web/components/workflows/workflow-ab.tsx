"use client";

import { useState } from "react";
import { Button, Field, inputCls, SectionCard, Skeleton } from "@/components/ui";
import { useBlueprintAB } from "@/lib/queries";
import { ABRow, abMetricKeys } from "@/components/blueprint-diff";
import { WorkflowErrorBox } from "@/components/workflows/workflow-shared";

/**
 * 同题 A/B：两个任务钉不同版本跑，比指标。
 * 属于进阶核对工具，放在次级区域，默认不展开大表。
 */
export function WorkflowAB({ blueprintId }: { blueprintId: string }) {
  const [goalA, setGoalA] = useState("");
  const [goalB, setGoalB] = useState("");
  const [showTable, setShowTable] = useState(false);
  const a = goalA.trim();
  const b = goalB.trim();
  const ab = useBlueprintAB(showTable ? blueprintId : null, showTable ? a || null : null, showTable ? b || null : null);

  return (
    <SectionCard
      title="同题 A/B"
      desc="同一个任务目标，分别钉住两个版本各跑一次，用执行指标判断改动是否真的更好。"
      descInBody
      bodyClassName="p-4"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="任务 A">
          <input
            value={goalA}
            onChange={(e) => setGoalA(e.target.value)}
            placeholder="goal id"
            className={`${inputCls} font-mono text-xs placeholder:font-sans`}
          />
        </Field>
        <Field label="任务 B">
          <input
            value={goalB}
            onChange={(e) => setGoalB(e.target.value)}
            placeholder="goal id"
            className={`${inputCls} font-mono text-xs placeholder:font-sans`}
          />
        </Field>
      </div>
      <div className="mt-4">
        <Button variant="outline" disabled={!a || !b} onClick={() => setShowTable(true)}>
          对比这两个任务
        </Button>
      </div>

      {showTable && ab.isLoading && <Skeleton className="mt-3 h-24 w-full" />}
      {showTable && ab.isError && (
        <div className="mt-3">
          <WorkflowErrorBox
            title="对比失败"
            message={ab.error instanceof Error ? ab.error.message : String(ab.error)}
          />
        </div>
      )}
      {showTable && ab.data && (
        <div className="mt-3 overflow-x-auto rounded-lg border border-line">
          <table className="w-full min-w-[560px] border-collapse text-xs">
            <colgroup>
              <col className="w-[120px]" />
              <col />
              <col />
              <col className="w-[96px]" />
            </colgroup>
            <thead>
              <tr className="border-b border-line bg-surface-2/40 text-left text-[11px] text-ink-3">
                <th className="px-3 py-2.5 font-medium">指标</th>
                <th className="px-3 py-2.5 font-medium">
                  <span className="block truncate text-xs font-medium text-ink-2">{ab.data.goal_a.goal_title || "A"}</span>
                  <span className="mt-0.5 block font-mono text-[10px]">{ab.data.goal_a.blueprint_version_id.slice(0, 8)}</span>
                </th>
                <th className="px-3 py-2.5 font-medium">
                  <span className="block truncate text-xs font-medium text-ink-2">{ab.data.goal_b.goal_title || "B"}</span>
                  <span className="mt-0.5 block font-mono text-[10px]">{ab.data.goal_b.blueprint_version_id.slice(0, 8)}</span>
                </th>
                <th className="px-3 py-2.5 text-right font-medium">Δ</th>
              </tr>
            </thead>
            <tbody>
              {abMetricKeys(ab.data.deltas).map((k) => (
                <ABRow
                  key={k}
                  metric={k}
                  a={ab.data.goal_a.metrics[k]}
                  b={ab.data.goal_b.metrics[k]}
                  delta={ab.data.deltas[k]}
                />
              ))}
            </tbody>
          </table>
          {ab.data.notes && ab.data.notes.length > 0 && (
            <ul className="space-y-1 border-t border-line/70 bg-amber-500/[0.04] px-3 py-2.5 text-[11px] leading-relaxed text-amber-300/90">
              {ab.data.notes.map((n) => (
                <li key={n}>· {n}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </SectionCard>
  );
}