"use client";

import { Boxes, Users } from "lucide-react";
import { SectionCard } from "@/components/ui";
import { cn } from "@/lib/utils";
import {
  PRACTICE_SOURCE_META,
  PracticeSourceBadge,
  type PracticeSource,
} from "@/components/practice/practice-source-badge";

// 静态示例：实践库还没有接入来源时，用它们说明“包长什么样、来源徽章怎么读”。
// 这里刻意不提供「应用」入口，也不写版本号/下载量——示例不是可导入的包，
// 不能让界面暗示这些具体内容现在就能安装。
type PracticeExample = {
  id: string;
  kind: "project" | "squad";
  name: string;
  description: string;
  source: PracticeSource;
  features: string[];
};

const EXAMPLES: PracticeExample[] = [
  {
    id: "example-project-web",
    kind: "project",
    name: "Web 服务项目包",
    description: "绑定仓库、组一支小队，并按包内定义预置目标与定时任务。",
    source: "official",
    features: ["仓库与小队", "目标与定时任务"],
  },
  {
    id: "example-squad-data",
    kind: "squad",
    name: "数据分析小队包",
    description: "预置若干 Agent 与队内分工，导入后直接是一支可派活的小队。",
    source: "community",
    features: ["Agent 与分工", "小队成员"],
  },
  {
    id: "example-project-script",
    kind: "project",
    name: "临时脚本项目包",
    description: "不绑仓库的一次性项目，适合脚本、实验和临时验证。",
    source: "unverified",
    features: ["不绑仓库", "快速起步"],
  },
];

const SOURCE_ORDER: PracticeSource[] = ["official", "community", "unverified"];

function ExampleCard({ example }: { example: PracticeExample }) {
  const isSquad = example.kind === "squad";
  const Icon = isSquad ? Users : Boxes;
  return (
    <article className="relative flex flex-col rounded-lg border border-dashed border-line bg-surface-1 p-4">
      <span className="absolute right-3 top-3 rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-3">
        示例
      </span>

      <div className="flex items-start gap-3">
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3">
          <Icon className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <h3 className="truncate pr-12 text-sm font-medium text-ink" title={example.name}>
            {example.name}
          </h3>
          <span
            className={cn(
              "mt-1 inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium",
              isSquad ? "bg-purple-500/10 text-purple-300" : "bg-blue-500/10 text-blue-300"
            )}
          >
            <span
              className={cn("h-1.5 w-1.5 rounded-full", isSquad ? "bg-purple-400" : "bg-blue-400")}
            />
            {isSquad ? "小队包" : "项目包"}
          </span>
        </div>
      </div>

      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        <PracticeSourceBadge source={example.source} />
      </div>

      <p className="mt-2 line-clamp-2 min-h-[2.5rem] text-xs leading-relaxed text-ink-3">
        {example.description}
      </p>

      <div className="mt-2 flex flex-wrap gap-1">
        {example.features.map((f) => (
          <span
            key={f}
            className="rounded border border-line bg-surface-1 px-1.5 py-0.5 text-[10px] text-ink-2"
          >
            {f}
          </span>
        ))}
      </div>

      <div className="mt-auto pt-4">
        <div className="border-t border-line pt-3">
          <span className="text-[11px] text-ink-3">示例卡片 · 仅供预览，不可导入</span>
        </div>
      </div>
    </article>
  );
}

export function PracticeExamples() {
  return (
    <SectionCard
      title="实践包示例"
      count={EXAMPLES.length}
      desc="以下均为展示样式，不是可导入的包；接入来源后，真实包会替换这里并标上来源徽章。"
      descInBody
    >
      <div className="grid grid-cols-1 gap-3 p-3 md:grid-cols-2 xl:grid-cols-3">
        {EXAMPLES.map((example) => (
          <ExampleCard key={example.id} example={example} />
        ))}
      </div>

      {/* 徽章词汇先立在这里：以后真实包接入时，来源一栏直接复用同一套读法。 */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line px-4 py-3">
        <span className="text-[11px] text-ink-3">来源徽章：</span>
        {SOURCE_ORDER.map((source) => (
          <span key={source} className="inline-flex items-center gap-1.5">
            <PracticeSourceBadge source={source} />
            <span className="text-[11px] text-ink-3">{PRACTICE_SOURCE_META[source].desc}</span>
          </span>
        ))}
      </div>
    </SectionCard>
  );
}