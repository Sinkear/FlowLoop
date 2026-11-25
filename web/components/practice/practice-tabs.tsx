"use client";

import { cn } from "@/lib/utils";

// 实践库的类型页签：全部 / 项目 / 小队，外加一个「工作流（即将支持）」
// 的禁用页签。禁用不是隐藏——用户会先问“为什么没有工作流”，把原因就写在
// 页签旁边比藏在 tooltip 里更诚实（WEB-IA-REDESIGN §2.6/§4.12）。
export type PracticeTab = "all" | "project" | "squad";

const TABS: { value: PracticeTab; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "project", label: "项目" },
  { value: "squad", label: "小队" },
];

// 工作流包还不能导入的原因只有一句：工作流的归属还没迁完，现在导进来会
// 建出一个没有归属的孤儿工作流。页签文字与禁用提示共用这一句。
export const WORKFLOW_DISABLED_REASON = "工作流归属迁移完成后开放导入";

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

        <span className="mx-1 h-4 w-px bg-line" aria-hidden />

        {/* 禁用页签：不可点，但保留完整可见性——用户需要知道“以后会有”。 */}
        <button
          type="button"
          role="tab"
          aria-selected={false}
          aria-disabled="true"
          disabled
          title={`暂不支持导入：${WORKFLOW_DISABLED_REASON}`}
          className="inline-flex cursor-not-allowed items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium text-ink-3 opacity-45"
        >
          工作流
          <span className="rounded border border-line px-1 py-px font-mono text-[9px] leading-none text-ink-3">
            即将支持
          </span>
        </button>
      </div>

      <p className="text-xs text-ink-3">
        工作流包暂不支持导入：{WORKFLOW_DISABLED_REASON}。
      </p>
    </div>
  );
}