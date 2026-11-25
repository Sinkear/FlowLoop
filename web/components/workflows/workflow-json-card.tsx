"use client";

import { ChevronRight } from "lucide-react";
import { SectionCard } from "@/components/ui";
import { WorkflowCopyButton } from "@/components/workflows/workflow-shared";

/** 规范原文：给需要核对逐字段的人一个出口，不占默认视线。 */
export function WorkflowJsonCard({ content }: { content: string }) {
  return (
    <SectionCard
      title="规范 JSON"
      desc="结构编辑器背后保存的就是这份 JSON；需要精确核对字段时可以展开原文。"
      descInBody
      action={<WorkflowCopyButton text={content} label="复制 JSON" />}
      bodyClassName="p-4"
    >
      <details className="group overflow-hidden rounded-lg border border-line bg-surface-2/20">
        <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs text-ink-2 transition-colors hover:bg-surface-2/40 hover:text-ink">
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-ink-3 transition-transform group-open:rotate-90" />
          展开查看原文
        </summary>
        <pre className="max-h-[420px] overflow-auto border-t border-line bg-surface-2/30 p-4 font-mono text-[12px] leading-6 tab-size-2 text-ink-2">
          {content}
        </pre>
      </details>
    </SectionCard>
  );
}