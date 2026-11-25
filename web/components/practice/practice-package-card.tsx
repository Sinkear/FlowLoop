"use client";

import { useMemo, useState } from "react";
import { Boxes, ChevronDown, Loader2, ShieldCheck, Users } from "lucide-react";
import { Button } from "@/components/ui";
import { useTemplate } from "@/lib/queries";
import { cn } from "@/lib/utils";
import type {
  ProjectTemplateSpec,
  SquadTemplateSpec,
  TemplateSummary,
} from "@/lib/types";

// 包由实践库自己的来源提供（不再绑定某个代码托管站点），但后端还没有
// “已官方验证/验证时间/使用次数”这类元数据。所以这里做的是**显示层标注**，
// 而不是伪造验证：
//   官方起步包 = 来源可信的起步内容，不等于“最佳实践”。
// 等后端带回真实验证记录，再按 官方验证 / 社区分享 / 未验证 三档显示来源。
const SOURCE_LABEL = "官方来源";

// 一屏之内能看懂“这个包会创建什么”的摘要：只从已拿到的 spec 派生，
// 不做任何推断（例如不猜仓库地址、不猜验证状态）。
function summarizeSpec(kind: string, spec: Record<string, unknown>): string[] {
  const parts: string[] = [];
  if (kind === "squad-template") {
    const s = spec as unknown as SquadTemplateSpec;
    if (s.agents?.length) parts.push(`${s.agents.length} 个 Agent`);
    if (s.squad?.name) parts.push(`小队「${s.squad.name}」`);
    if (s.squad?.members?.length) parts.push(`${s.squad.members.length} 名队内成员`);
    return parts;
  }
  const s = spec as unknown as ProjectTemplateSpec;
  if (s.domain?.type === "scratch") parts.push("临时项目（不绑仓库）");
  else if (s.repo?.create) parts.push("自动创建仓库");
  else parts.push("绑定已有仓库");
  if (s.team?.agents?.length) parts.push(`${s.team.agents.length} 个 Agent`);
  if (s.team?.squad?.name) parts.push(`小队「${s.team.squad.name}」`);
  if (s.goals?.length) parts.push(`${s.goals.length} 个目标`);
  if (s.schedules?.length) parts.push(`${s.schedules.length} 个定时任务`);
  return parts;
}

export function PracticePackageCard({
  template,
  onApply,
}: {
  template: TemplateSummary;
  onApply: () => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const isSquad = template.kind === "squad-template";
  const Icon = isSquad ? Users : Boxes;

  // 内容摘要按需拉取：一次列表只拿元数据，展开哪张卡才读哪张卡的 spec，
  // 避免进页面就打 N 个详情请求。
  const detail = useTemplate(isSquad ? "squad" : "project", template.id, expanded);
  const parts = useMemo(
    () => (detail.data ? summarizeSpec(template.kind, detail.data.spec) : []),
    [detail.data, template.kind]
  );

  return (
    <article className="flex flex-col rounded-lg border border-line bg-surface-1 p-4">
      <div className="flex items-start gap-3">
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3">
          <Icon className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <h3 className="min-w-0 max-w-full truncate text-sm font-medium text-ink" title={template.name}>
              {template.name}
            </h3>
            <span className="font-mono text-[10px] text-ink-3">v{template.version}</span>
          </div>
          <p className="mt-0.5 truncate font-mono text-[10px] text-ink-3" title={template.id}>
            {template.id}
          </p>
        </div>
      </div>

      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        <span
          className={cn(
            "inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium",
            isSquad ? "bg-purple-500/10 text-purple-300" : "bg-blue-500/10 text-blue-300"
          )}
        >
          <span
            className={cn(
              "h-1.5 w-1.5 rounded-full",
              isSquad ? "bg-purple-400" : "bg-blue-400"
            )}
          />
          {isSquad ? "小队包" : "项目包"}
        </span>
        <span className="inline-flex items-center gap-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[11px] font-medium text-emerald-300">
          <ShieldCheck className="h-3 w-3" />
          官方起步包
        </span>
        {template.tags?.slice(0, 3).map((tag) => (
          <span
            key={tag}
            className="rounded border border-line px-1.5 py-0.5 font-mono text-[10px] text-ink-3"
          >
            {tag}
          </span>
        ))}
      </div>

      <p className="mt-2 line-clamp-2 min-h-[2.5rem] text-xs leading-relaxed text-ink-3">
        {template.description || "（暂无描述）"}
      </p>

      <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-[11px]">
        <div className="flex min-w-0 gap-1.5">
          <dt className="shrink-0 text-ink-3">来源</dt>
          <dd className="truncate text-ink-2" title={SOURCE_LABEL}>
            {SOURCE_LABEL}
          </dd>
        </div>
        <div className="flex min-w-0 gap-1.5">
          <dt className="shrink-0 text-ink-3">产物</dt>
          <dd className="truncate text-ink-2">
            {isSquad ? "小队与 Agent" : "项目与小队"}
          </dd>
        </div>
      </dl>

      {/* 内容摘要：默认收起，展开后才读详情。 */}
      <div className="mt-2.5">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          className="inline-flex items-center gap-1 text-[11px] text-ink-3 transition-colors hover:text-ink"
        >
          <ChevronDown
            className={cn("h-3 w-3 transition-transform duration-150", expanded && "rotate-180")}
          />
          包含内容
        </button>
        {expanded && (
          <div className="mt-1.5 rounded-md border border-line bg-surface-2/40 px-2.5 py-2">
            {detail.isPending ? (
              <span className="inline-flex items-center gap-1.5 text-[11px] text-ink-3">
                <Loader2 className="h-3 w-3 animate-spin" />
                读取包内容…
              </span>
            ) : detail.isError ? (
              <p className="text-[11px] leading-relaxed text-ink-3">
                包内容读取失败；点「应用」可在弹窗里继续预览与套用。
              </p>
            ) : parts.length ? (
              <div className="flex flex-wrap gap-1">
                {parts.map((p) => (
                  <span
                    key={p}
                    className="rounded border border-line bg-surface-1 px-1.5 py-0.5 text-[10px] text-ink-2"
                  >
                    {p}
                  </span>
                ))}
              </div>
            ) : (
              <p className="text-[11px] text-ink-3">包里只有元数据，应用时按空定义预填。</p>
            )}
          </div>
        )}
      </div>

      <div className="mt-auto flex items-center justify-between gap-2 border-t border-line pt-3">
        <span className="text-[11px] text-ink-3">应用后是你自己的副本</span>
        <Button
          variant="outline"
          className="h-7 shrink-0 px-2.5 text-xs"
          onClick={onApply}
          title="打开应用弹窗；弹窗里可预览将创建的内容"
        >
          应用
        </Button>
      </div>
    </article>
  );
}