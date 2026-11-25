"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";
import { DASH } from "@/components/training/format";

/** 时间统一用本地化字符串；空值显示占位符。 */
export function fullTime(iso: string | null | undefined): string {
  if (!iso) return DASH;
  return new Date(iso).toLocaleString("zh-CN");
}

/** 错误提示：保留后端校验原文，方便动画里直接改 spec。 */
export function WorkflowErrorBox({ title, message }: { title: string; message: string }) {
  return (
    <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2">
      <div className="text-xs font-medium text-amber-300">{title}</div>
      <pre className="mt-1 whitespace-pre-wrap font-mono text-[11px] leading-relaxed text-amber-200/90">
        {message}
      </pre>
    </div>
  );
}

/** 复制按钮：优先 Clipboard API，失败回退 textarea + execCommand。 */
export function WorkflowCopyButton({ text, label = "复制" }: { text: string; label?: string }) {
  const [state, setState] = useState<"idle" | "done" | "fail">("idle");

  async function copy() {
    try {
      await Promise.race([
        navigator.clipboard.writeText(text),
        new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), 1000)),
      ]);
      setState("done");
    } catch {
      try {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
        setState("done");
      } catch {
        setState("fail");
      }
    }
    setTimeout(() => setState("idle"), 1600);
  }

  return (
    <button
      type="button"
      onClick={copy}
      className="inline-flex items-center gap-1 text-[11px] text-ink-3 hover:text-ink transition-colors"
      title="复制到剪贴板"
    >
      {state === "done" ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
      {state === "done" ? "已复制" : state === "fail" ? "复制失败" : label}
    </button>
  );
}

export const WORKFLOW_STEPS = [
  { key: "draft", label: "写草稿" },
  { key: "publish", label: "发布" },
  { key: "bind", label: "绑定任务" },
] as const;

/** 三步进度：让用户知道当前版本卡在哪一步。 */
export function WorkflowStepTrack({ current }: { current: number }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {WORKFLOW_STEPS.map((step, i) => {
        const done = i < current;
        const active = i === current;
        return (
          <div key={step.key} className="flex items-center gap-2">
            {i > 0 && (
              <span
                className={cn("h-px w-6", done || active ? "bg-emerald-500/50" : "bg-line")}
                aria-hidden
              />
            )}
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px]",
                done && "border-emerald-500/40 bg-emerald-500/10 text-emerald-300",
                active && "border-accent/50 bg-accent/10 text-accent",
                !done && !active && "border-line text-ink-3",
              )}
            >
              {done ? (
                <Check className="h-3 w-3" />
              ) : (
                <span className={cn("h-1.5 w-1.5 rounded-full", active ? "bg-accent" : "bg-ink-3/50")} />
              )}
              {step.label}
            </span>
          </div>
        );
      })}
    </div>
  );
}

/** 版本状态点：草稿琥珀，已发布翠绿。 */
export function VersionDot({ status }: { status: string }) {
  const published = status === "published";
  return (
    <span
      className={cn(
        "mt-1.5 h-2 w-2 shrink-0 rounded-full ring-4",
        published ? "bg-emerald-400 ring-emerald-500/10" : "bg-amber-400 ring-amber-500/10",
      )}
      aria-hidden
    />
  );
}