"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ArrowRight, ShieldAlert, X } from "lucide-react";
import { useGoals, usePendingGates } from "@/lib/queries";

// 全局待审核提醒：人工门不改变 goal.status，只在 goal 详情页里可见——
// 人不在那个页面时流程就静默停着。这里在应用壳层轮询，一旦出现新的
// 人工门/待审批，就在右下角弹一张卡片（不依赖浏览器通知权限）。
// 初始加载时已有的条目算「已看过」，不打扰；只提示本次会话里新出现的。
export function PendingApprovalsToast() {
  const { data: gatesData } = usePendingGates();
  const { data: goals } = useGoals();

  const items = useMemo(() => {
    const out: { key: string; label: string }[] = [];
    for (const g of gatesData?.gates ?? []) {
      out.push({ key: `gate:${g.goal_id}:${g.node_key}`, label: g.title || g.node_key });
    }
    for (const g of goals ?? []) {
      if (g.status === "review") out.push({ key: `review:${g.id}`, label: g.title });
    }
    return out;
  }, [gatesData, goals]);

  const seenRef = useRef<Set<string> | null>(null);
  const [fresh, setFresh] = useState<string[]>([]);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!gatesData || !goals) return;
    const keys = items.map((i) => i.key);
    if (seenRef.current === null) {
      seenRef.current = new Set(keys);
      return;
    }
    const unseen = keys.filter((k) => !seenRef.current!.has(k));
    keys.forEach((k) => seenRef.current!.add(k));
    if (unseen.length > 0) {
      setFresh(items.filter((i) => unseen.includes(i.key)).map((i) => i.label).slice(0, 3));
      setOpen(true);
    }
  }, [items, gatesData, goals]);

  useEffect(() => {
    if (items.length === 0) setOpen(false);
  }, [items.length]);

  useEffect(() => {
    if (!open) return;
    const timer = window.setTimeout(() => setOpen(false), 12_000);
    return () => window.clearTimeout(timer);
  }, [open, fresh]);

  if (!open || items.length === 0) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-4 right-4 z-[60] w-[min(92vw,360px)] rounded-lg border border-amber-500/40 bg-surface-1 p-3 shadow-2xl"
    >
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-400">
          <ShieldAlert className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-ink">
            有 {items.length} 项等你审核
          </div>
          <div className="mt-1 space-y-0.5 text-[12px] leading-5 text-ink-3">
            {fresh.length > 0 ? (
              <div className="truncate text-amber-200/90">新增：{fresh.join("、")}</div>
            ) : (
              <div>工作流人工门和待审批任务集中在这里。</div>
            )}
            <div>尽快拍板，别让流程停着。</div>
          </div>
          <div className="mt-2 flex items-center gap-3">
            <Link
              href="/"
              onClick={() => setOpen(false)}
              className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:text-accent-dim"
            >
              去工作台处理 <ArrowRight className="h-3 w-3" />
            </Link>
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="text-[12px] text-ink-3 hover:text-ink"
            >
              稍后
            </button>
          </div>
        </div>
        <button
          type="button"
          aria-label="关闭提醒"
          onClick={() => setOpen(false)}
          className="rounded p-1 text-ink-3 hover:bg-surface-2 hover:text-ink"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}
