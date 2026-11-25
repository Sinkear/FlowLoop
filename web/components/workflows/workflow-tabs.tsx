"use client";

import { cn } from "@/lib/utils";

export type WorkflowTab = "spec" | "versions" | "reviews" | "proposals" | "ab" | "json";

const TABS: { value: WorkflowTab; label: string }[] = [
  { value: "spec", label: "规范图" },
  { value: "versions", label: "版本" },
  { value: "reviews", label: "复盘" },
  { value: "proposals", label: "优化提案" },
  { value: "ab", label: "A/B 对比" },
  { value: "json", label: "规范 JSON" },
];

/**
 * 详情页分区：首屏只给规范图和下一步动作，版本 / 提案 / A·B 作为次级页面收起。
 * 用 tab 而不是把全部内容纵向堆叠，移动端横向滑动即可切换。
 */
export function WorkflowTabs({
  value,
  onChange,
  versionCount,
  showJson,
}: {
  value: WorkflowTab;
  onChange: (tab: WorkflowTab) => void;
  versionCount: number;
  showJson: boolean;
}) {
  const visible = TABS.filter((tab) => tab.value !== "json" || showJson);
  return (
    <div className="-mx-4 border-b border-line md:-mx-8">
      <div
        role="tablist"
        aria-label="工作流分区"
        className="flex items-center gap-1 overflow-x-auto px-4 [scrollbar-width:none] md:px-8 [&::-webkit-scrollbar]:hidden"
      >
        {visible.map((tab) => {
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
              {tab.value === "versions" && versionCount > 0 && (
                <span
                  className={cn(
                    "rounded-full px-1.5 py-0.5 font-mono text-[10px] leading-none",
                    current ? "bg-accent/15 text-accent" : "bg-surface-2 text-ink-3",
                  )}
                >
                  {versionCount}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}
