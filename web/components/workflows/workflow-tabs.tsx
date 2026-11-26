"use client";

import { cn } from "@/lib/utils";

export type WorkflowTab = "spec" | "reviews" | "proposals" | "ab";

const TABS: { value: WorkflowTab; label: string }[] = [
  { value: "spec", label: "规范" },
  { value: "reviews", label: "复盘" },
  { value: "proposals", label: "优化提案" },
  { value: "ab", label: "A/B 对比" },
];

/**
 * 详情页分区：首屏只给规范图和下一步动作，版本 / 提案 / A·B 作为次级页面收起。
 * 用 tab 而不是把全部内容纵向堆叠，移动端横向滑动即可切换。
 */
export function WorkflowTabs({
  value,
  onChange,
  proposalCount,
  pendingProposalCount,
}: {
  value: WorkflowTab;
  onChange: (tab: WorkflowTab) => void;
  proposalCount: number;
  pendingProposalCount: number;
}) {
  return (
    <div className="-mx-4 border-b border-line md:-mx-8">
      <div
        role="tablist"
        aria-label="工作流分区"
        className="flex items-center gap-1 overflow-x-auto px-4 [scrollbar-width:none] md:px-8 [&::-webkit-scrollbar]:hidden"
      >
        {TABS.map((tab) => {
          const current = tab.value === value;
          return (
            <button
              key={tab.value}
              type="button"
              role="tab"
              aria-selected={current}
              onClick={() => onChange(tab.value)}
              className={cn(
                "relative inline-flex shrink-0 items-center gap-1.5 border-b-2 px-2.5 pb-2 pt-1 text-xs transition-colors",
                current
                  ? "border-accent text-ink-1"
                  : "border-transparent text-ink-3 hover:border-line hover:text-ink-2",
              )}
            >
              {tab.label}
              {tab.value === "proposals" && proposalCount > 0 && (
                <span
                  title={`${pendingProposalCount} 条待批，共 ${proposalCount} 条提案`}
                  className={cn(
                    "rounded-full px-1.5 py-0.5 font-mono text-[10px] leading-none",
                    current
                      ? "bg-accent/15 text-accent"
                      : pendingProposalCount > 0
                        ? "bg-amber-500/15 text-amber-300"
                        : "bg-surface-2 text-ink-3",
                  )}
                >
                  {proposalCount}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}
