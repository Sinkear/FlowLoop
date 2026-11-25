"use client";

import { useMemo, useState } from "react";
import { Info, Package, RefreshCw } from "lucide-react";
import {
  Button,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Skeleton,
  StatStrip,
} from "@/components/ui";
import { useTemplates, useRefreshTemplates } from "@/lib/queries";
import { relTime } from "@/lib/utils";
import { ProjectTemplateDialog } from "@/components/project-template-dialog";
import { SquadTemplateDialog } from "@/components/squad-template-dialog";
import { PracticeTabs, type PracticeTab } from "@/components/practice/practice-tabs";
import { PracticePackageCard } from "@/components/practice/practice-package-card";

// 实践库（原「模板库」，路由仍保留 /templates 以免打断书签）：
// 容器名按 D4 叫“实践库”，“最佳实践”只作为验证徽章保留给未来的官方验证包。
// 应用弹窗沿用原有项目/小队两个 dialog，本页只负责分类浏览与入口。
export default function PracticeLibraryPage() {
  const [tab, setTab] = useState<PracticeTab>("all");
  const [open, setOpen] = useState<null | "project" | "squad">(null);
  const { data: templates, isLoading, isError } = useTemplates();
  const refresh = useRefreshTemplates();

  const all = templates ?? [];
  const counts = useMemo(
    () => ({
      all: all.length,
      project: all.filter((t) => t.kind === "project-template").length,
      squad: all.filter((t) => t.kind === "squad-template").length,
    }),
    [all]
  );
  const visible = tab === "all" ? all : all.filter((t) => t.kind === `${tab}-template`);

  const errorCount = refresh.data?.errors?.length ?? 0;
  const refreshButton = (
    <Button
      variant="outline"
      onClick={() => refresh.mutate(undefined)}
      disabled={refresh.isPending}
      title="从 GitCode 模板仓库重新拉取"
      className="gap-1.5"
    >
      <RefreshCw className={`h-3.5 w-3.5 ${refresh.isPending ? "animate-spin" : ""}`} />
      {refresh.isPending ? "刷新中…" : "刷新来源仓库"}
    </Button>
  );

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="实践库"
        desc="官方来源的项目包与小队包；应用后按包内定义预填创建流程，生成的是你自己的一份副本。"
        action={refreshButton}
      />

      {/* 顶部说明：徽章分档的现状，避免把“来源可信”读成“官方验证过”。 */}
      <div className="flex items-start gap-2.5 rounded-lg border border-line bg-surface-1 px-4 py-3">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-ink-3" />
        <p className="text-xs leading-relaxed text-ink-3">
          <span className="text-ink-2">最佳实践徽章：</span>
          官方验证内容还在筛选中；当前先标记来源。现有包均来自官方 GitCode
          模板仓库，因此只标「官方起步包」，未经官方验证前不会出现「最佳实践」。
        </p>
      </div>

      <StatStrip
        items={[
          { label: "项目包", value: counts.project, hint: "来源：GitCode 模板仓库" },
          { label: "小队包", value: counts.squad, hint: "来源：GitCode 模板仓库" },
          {
            label: "上次拉取",
            value: refresh.data ? relTime(refresh.data.fetched_at) : "—",
            tone: errorCount ? "warn" : "muted",
            hint: refresh.data
              ? errorCount
                ? `${errorCount} 个错误，点「刷新来源仓库」重试`
                : `拉取到 ${refresh.data.fetched} 个包，无错误`
              : "尚未在本页刷新过",
          },
        ]}
      />

      <PracticeTabs value={tab} onChange={setTab} counts={counts} />

      <SectionCard title="实践包" count={visible.length}>
        {isLoading ? (
          <div className="grid grid-cols-1 gap-3 p-3 md:grid-cols-2">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="space-y-2.5 rounded-lg border border-line p-4">
                <div className="flex items-center gap-3">
                  <Skeleton className="h-9 w-9 rounded-md" />
                  <div className="flex-1 space-y-1.5">
                    <Skeleton className="h-4 w-40" />
                    <Skeleton className="h-3 w-24" />
                  </div>
                </div>
                <Skeleton className="h-3 w-2/3" />
                <Skeleton className="h-3 w-full" />
                <Skeleton className="h-3 w-1/2" />
              </div>
            ))}
          </div>
        ) : isError ? (
          <div>
            <Empty icon={<Package className="h-5 w-5" />}>
              实践包列表读取失败。来源仓库可能尚未拉取，或后端暂不可用——点下方「刷新来源仓库」重新拉取。
            </Empty>
            <div className="flex justify-center pb-6">{refreshButton}</div>
          </div>
        ) : all.length === 0 ? (
          <div>
            <Empty icon={<Package className="h-5 w-5" />}>
              实践库里还没有可用的包。来源仓库（GitCode）可能为空或尚未拉取——点下方「刷新来源仓库」拉取，成功后项目包与小队包会在这里列出，应用前可先展开「包含内容」核对。
            </Empty>
            <div className="flex justify-center pb-6">{refreshButton}</div>
          </div>
        ) : visible.length === 0 ? (
          <div>
            <Empty icon={<Package className="h-5 w-5" />}>
              还没有{tab === "project" ? "项目包" : "小队包"}。当前来源仓库里只有另一种类型的包，切到「全部」可以看到现有内容。
            </Empty>
            <div className="flex justify-center pb-6">
              <Button variant="outline" onClick={() => setTab("all")}>
                查看全部
              </Button>
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3 p-3 md:grid-cols-2">
            {visible.map((t) => (
              <PracticePackageCard
                key={t.id}
                template={t}
                onApply={() => setOpen(t.kind === "squad-template" ? "squad" : "project")}
              />
            ))}
          </div>
        )}
      </SectionCard>

      {open === "project" && <ProjectTemplateDialog onClose={() => setOpen(null)} />}
      {open === "squad" && <SquadTemplateDialog onClose={() => setOpen(null)} />}
    </PageBody>
  );
}