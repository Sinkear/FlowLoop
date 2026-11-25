"use client";

import { cn } from "@/lib/utils";

// 实践库的类型页签：全部 / 项目 / 小队 / 工作流。四个页签都在 6 个以内。
// 工作流包不是“以后再说”：它从本地 JSON 导入，导入时选择归属小队，
// 先存成草稿，发布后才能派活。
export type PracticeTab = "all" | "project" | "squad" | "workflow";

const TABS: { value: PracticeTab; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "project", label: "项目" },
  { value: "squad", label: "小队" },
  { value: "workflow", label: "工作流" },
];

export function PracticeTabs({
  value,
  onChange,
  counts,
}: {
  value: PracticeTab;
  onChange: (v: PracticeTab) => void;
  counts: Record<PracticeTab, number>;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <div
        role="tablist"
        aria-label="实践包类型"
        className="flex items-center gap-0.5 rounded-md border border-line bg-surface-1 p-0.5"
      >
        {TABS.map((t) => {
          const active = value === t.value;
          return (
            <button
              key={t.value}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => onChange(t.value)}
              title={
                t.value === "workflow"
                  ? "工作流包从本地 JSON 导入，必须归属一支小队；导入后先成为草稿"
                  : undefined
              }
              className={cn(
                "inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150",
                active ? "bg-surface-3 text-ink" : "text-ink-3 hover:text-ink"
              )}
            >
              {t.label}
              <span className="font-mono text-[10px] tabular-nums text-ink-3">{counts[t.value]}</span>
            </button>
          );
        })}
      </div>

      <p className="text-xs text-ink-3">
        工作流包从本地导入，需指定归属小队；导入后先存为草稿，发布后才能派活。
      </p>
    </div>
  );
}
