"use client";

import { ShieldCheck, ShieldQuestion, Users, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

// 实践包的来源分档只有三档，全站用同一套词汇：官方验证 / 社区分享 / 未验证。
// 后端目前还没有真实验证记录，所以这里只定义“怎么显示”，不负责判定来源；
// 等接入来源并带回来源元数据后，由数据决定挂哪一档。
export type PracticeSource = "official" | "community" | "unverified";

export const PRACTICE_SOURCE_META: Record<
  PracticeSource,
  { label: string; desc: string; icon: LucideIcon; tone: string }
> = {
  official: {
    label: "官方验证",
    desc: "内容与来源都经过官方审核",
    icon: ShieldCheck,
    tone: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
  },
  community: {
    label: "社区分享",
    desc: "社区成员提交，未经官方审核",
    icon: Users,
    tone: "border-sky-500/30 bg-sky-500/10 text-sky-300",
  },
  unverified: {
    label: "未验证",
    desc: "来源未经审核，导入前请自行核对",
    icon: ShieldQuestion,
    tone: "border-line bg-surface-2 text-ink-3",
  },
};

export function PracticeSourceBadge({
  source,
  className,
}: {
  source: PracticeSource;
  className?: string;
}) {
  const meta = PRACTICE_SOURCE_META[source];
  const Icon = meta.icon;
  return (
    <span
      title={meta.desc}
      className={cn(
        "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium",
        meta.tone,
        className
      )}
    >
      <Icon className="h-3 w-3" />
      {meta.label}
    </span>
  );
}