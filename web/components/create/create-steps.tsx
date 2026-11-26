"use client";

import Link from "next/link";
import { ArrowRight, Check } from "lucide-react";
import { cn } from "@/lib/utils";
import type { CreateKind } from "@/lib/task-template";

export type CreateStep = {
  key: string;
  label: string;
  desc: string;
  done: boolean;
  count?: number;
  /** 未完成时点击切换到的创建类型；没有 kind 就跳 href。 */
  kind?: CreateKind;
  href: string;
};

/**
 * 新建页顶部的“推荐创建顺序”：第一次使用时按 01→06 把最小闭环跑通。
 * 已有对象自动打勾；未完成项在当前页切换类型，不跳走。
 */
export function CreateSteps({
  steps,
  currentKind,
  disabled,
  onPick,
}: {
  steps: CreateStep[];
  currentKind: CreateKind;
  disabled?: boolean;
  onPick: (kind: CreateKind) => void;
}) {
  return (
    <section className="overflow-hidden rounded-xl border border-line bg-surface-1 shadow-sm shadow-black/10">
      <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-line/70 bg-surface-2/30 px-5 py-3">
        <div>
          <h2 className="text-sm font-semibold text-ink">推荐创建顺序</h2>
          <p className="mt-0.5 text-[11px] text-ink-3">
            第一次使用，按 01 → 06 建出最小闭环；已有对象会自动打勾。
          </p>
        </div>
        <span className="text-[11px] text-ink-3">项目 → 运行环境 → 智能体 → 小队 → 工作流 → 任务</span>
      </div>

      <ol className="grid grid-cols-1 divide-y divide-line sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-6">
        {steps.map((step, i) => {
          const current = step.kind === currentKind;
          const body = (
            <>
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-[11px] font-semibold tabular-nums",
                    step.done
                      ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-300"
                      : current
                        ? "border-accent/50 bg-accent/10 text-accent"
                        : "border-line bg-surface-2 text-ink-3",
                  )}
                >
                  {step.done ? <Check className="h-3.5 w-3.5" /> : String(i + 1).padStart(2, "0")}
                </span>
                <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{step.label}</span>
                {typeof step.count === "number" && step.count > 0 ? (
                  <span className="shrink-0 text-[11px] tabular-nums text-ink-3">{step.count}</span>
                ) : null}
              </div>
              <p className="mt-1.5 text-[11px] leading-relaxed text-ink-3">{step.desc}</p>
              <span className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-ink-3">
                {step.done ? "查看" : step.kind ? "去创建" : "去设置"}
                <ArrowRight className="h-3 w-3" />
              </span>
            </>
          );

          return (
            <li key={step.key} className="min-w-0">
              {step.done ? (
                <Link
                  href={step.href}
                  className="flex h-full flex-col px-4 py-3 transition-colors hover:bg-surface-2/50"
                >
                  {body}
                </Link>
              ) : step.kind ? (
                <button
                  type="button"
                  disabled={disabled}
                  onClick={() => onPick(step.kind!)}
                  className={cn(
                    "flex h-full w-full flex-col px-4 py-3 text-left transition-colors",
                    disabled ? "cursor-not-allowed opacity-60" : "hover:bg-surface-2/50",
                    current && "bg-accent/[0.06]",
                  )}
                >
                  {body}
                </button>
              ) : (
                <Link
                  href={step.href}
                  className="flex h-full flex-col px-4 py-3 transition-colors hover:bg-surface-2/50"
                >
                  {body}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}
