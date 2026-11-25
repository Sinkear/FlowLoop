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
} from "@/components/ui";
import { useTemplates, useRefreshTemplates } from "@/lib/queries";
import { ProjectTemplateDialog } from "@/components/project-template-dialog";
import { SquadTemplateDialog } from "@/components/squad-template-dialog";
import { PracticeTabs, type PracticeTab } from "@/components/practice/practice-tabs";
import { PracticePackageCard } from "@/components/practice/practice-package-card";
import { PracticeExamples } from "@/components/practice/practice-examples";

// 实践库（原「模板库」，路由仍保留 /templates 以免打断书签）：
// 容器名按 D4 叫“实践库”，“最佳实践”只作为验证徽章保留给未来的官方验证包。
// 实践库已不再依赖 GitCode 模板仓库：没接入来源时后端返回空列表（不是 400），
// 所以这里的“空”是一种正常状态，不该出现红色报错或“刷新来源仓库”的主按钮。
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
  const hasTemplates = all.length > 0;

  // 只有确实读到包时才提供刷新入口：来源未接入时刷新必定失败，按钮只会
  // 制造一次无意义的报错。刷新失败也只写成卡片内的弱化说明，不是页面错误。
  const refreshButton = (
    <Button
      variant="ghost"
      onClick={() => refresh.mutate(undefined)}
      disabled={refresh.isPending}
      title="重新读取实践库来源（不会自动执行）"
      className="h-7 gap-1.5 px-2 text-xs"
    >
      <RefreshCw className={`h-3.5 w-3.5 ${refresh.isPending ? "animate-spin" : ""}`} />
      {refresh.isPending ? "刷新中…" : "刷新"}
    </Button>
  );

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="实践库"
        desc="项目包与小队包的应用入口；应用后会按包内定义预填创建流程，生成的是属于你自己的一份副本。"
      />

      {/* 顶部说明：来源未接入是常态，先把“页面不会自动拉取远端内容”讲清楚。 */}
      <div className="flex items-start gap-2.5 rounded-lg border border-line bg-surface-1 px-4 py-3">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-ink-3" />
        <p className="text-xs leading-relaxed text-ink-3">
          实践库只存放项目包与小队包的定义。接入来源前这里保持为空，页面不会自动拉取远端内容；
          应用任何包后生成的都是你自己的副本，不会改动来源包。
        </p>
      </div>

      <PracticeTabs value={tab} onChange={setTab} counts={counts} />

      <SectionCard
        title="实践包"
        count={visible.length}
        action={hasTemplates ? refreshButton : undefined}
      >
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
        ) : !hasTemplates ? (
          <Empty icon={<Package className="h-5 w-5" />}>
            <span className="mb-1.5 block text-sm font-medium text-ink">实践库还没有接入来源</span>
            接入来源后，项目包与小队包可以在这里导入，应用后会生成属于你自己的一份副本。
            <span className="mt-4 flex justify-center">
              <Button variant="outline" disabled title="来源接入后开放">
                导入实践包（即将开放）
              </Button>
            </span>
            {isError && (
              <span className="mt-3 block text-[11px] text-ink-3">
                暂时读不到实践包列表；这不影响继续新建项目与小队，稍后可再回来看看。
              </span>
            )}
          </Empty>
        ) : visible.length === 0 ? (
          <div>
            <Empty icon={<Package className="h-5 w-5" />}>
              还没有{tab === "project" ? "项目包" : "小队包"}。切换到「全部」可以看看现有的另一种类型。
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

        {refresh.isError && (
          <p className="border-t border-line px-4 py-2 text-[11px] text-ink-3">
            刷新失败：实践库来源暂不可用，稍后再试即可。
          </p>
        )}
      </SectionCard>

      {/* 空库时给一组静态示例，说明“包会长什么样”；示例不可导入，也不冒称可安装。 */}
      {!isLoading && !hasTemplates && <PracticeExamples />}

      {open === "project" && <ProjectTemplateDialog onClose={() => setOpen(null)} />}
      {open === "squad" && <SquadTemplateDialog onClose={() => setOpen(null)} />}
    </PageBody>
  );
}