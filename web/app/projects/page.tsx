"use client";

import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ArrowRight, Check, Copy, FolderGit2, Plus } from "lucide-react";
import { useConnections, useDeleteDomain, useDomains, useGoals, useSchedules, useSquads } from "@/lib/queries";
import {
  Button, ConfirmDialog, Empty, PageBody, PageHeader, SectionCard, Skeleton, StatStrip,
} from "@/components/ui";
import { ProjectForm } from "@/components/project-form";
import { cn, relTime } from "@/lib/utils";
import type { Domain } from "@/lib/types";

const ACCEPTANCE_TAG: Record<string, { label: string; cls: string }> = {
  hitl: { label: "HITL 人工验收", cls: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" },
  hootl: { label: "HOOTL 自动验收", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  "": { label: "待定", cls: "border-line bg-surface-2 text-ink-3" },
};

function Tag({ className, children }: { className: string; children: React.ReactNode }) {
  return (
    <span className={cn("inline-flex shrink-0 items-center rounded border px-1.5 py-0.5 font-mono text-[10px]", className)}>
      {children}
    </span>
  );
}

function repoShort(url: string): string {
  return url.replace(/^https?:\/\//i, "").replace(/\.git$/i, "");
}

function ProjectRow({
  project: d,
  goalCount,
  scheduleCount,
  squadCount,
  onEdit,
  onDelete,
}: {
  project: Domain;
  goalCount: number;
  scheduleCount: number;
  squadCount: number;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const repo = d.type !== "scratch";
  const acceptance = ACCEPTANCE_TAG[d.acceptance_level] ?? ACCEPTANCE_TAG[""];

  const copy = async (event: React.MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
    if (!d.git_url) return;
    try {
      await navigator.clipboard.writeText(d.git_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure origin) — the URL stays selectable
    }
  };

  return (
    <div className="flex flex-col gap-3 px-4 py-3.5 transition-colors hover:bg-surface-2/40 md:flex-row md:items-center md:gap-4">
      <Link href={`/projects/${d.id}`} className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="truncate text-sm font-medium text-ink">{d.name}</span>
          <Tag className="border-line text-ink-2">{repo ? "代码仓库" : "无仓库工作区"}</Tag>
          <Tag className={acceptance.cls}>{acceptance.label}</Tag>
          <Tag className="border-line bg-surface-2 text-ink-3">{goalCount} 任务</Tag>
          <Tag className="border-line bg-surface-2 text-ink-3">{scheduleCount} 定时</Tag>
          <Tag className="border-line bg-surface-2 text-ink-3">{squadCount} 小队</Tag>
        </div>

        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3">
          {repo ? (
            <button
              type="button"
              onClick={copy}
              title={`${d.git_url}\n点击复制`}
              className="group inline-flex min-w-0 max-w-full items-center gap-1 font-mono text-ink-3 transition-colors hover:text-ink-2"
            >
              <span className="truncate">{repoShort(d.git_url)}</span>
              {copied ? <Check className="h-3 w-3 shrink-0 text-emerald-400" /> : <Copy className="h-3 w-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />}
            </button>
          ) : (
            <span className="font-mono">无仓库 · 项目目录模式</span>
          )}
          <span>·</span>
          {repo && (
            <>
              <span className="font-mono">{d.default_branch || "main"} 分支</span>
              <span>·</span>
            </>
          )}
          <span title={d.created_at ? new Date(d.created_at).toLocaleString() : ""}>创建于 {relTime(d.created_at) || "—"}</span>
        </div>
      </Link>

      <div className="flex shrink-0 items-center gap-1">
        <Link href={`/projects/${d.id}`} className="inline-flex items-center gap-1 px-2 py-1.5 text-sm text-ink-2 hover:text-ink">
          打开 <ArrowRight className="h-3.5 w-3.5" />
        </Link>
        <Button variant="outline" onClick={onEdit}>配置</Button>
        <Button variant="ghost" onClick={onDelete} className="text-red-400 hover:bg-red-500/10 hover:text-red-300">
          删除
        </Button>
      </div>
    </div>
  );
}

function ProjectsInner() {
  const search = useSearchParams();
  const { data: domains, isLoading } = useDomains();
  const { data: goals } = useGoals();
  const { data: schedules } = useSchedules();
  const { data: connections } = useConnections();
  const { data: squads } = useSquads();
  const del = useDeleteDomain();
  const [editor, setEditor] = useState<Domain | "new" | null>(null);
  const [deleting, setDeleting] = useState<Domain | null>(null);
  const [deleteError, setDeleteError] = useState("");

  useEffect(() => {
    if (search.get("new") === "1") setEditor("new");
  }, [search]);

  const params = useSearchParams();
  useEffect(() => {
    if (params.get("new") === "1") setEditor("new");
  }, [params]);

  const list = domains ?? [];
  const repoCount = list.filter((d) => d.type !== "scratch").length;
  const weekAgo = Date.now() - 7 * 24 * 60 * 60 * 1000;
  const newThisWeek = list.filter((d) => {
    const t = new Date(d.created_at).getTime();
    return !Number.isNaN(t) && t >= weekAgo;
  }).length;
  const activeGoals = (goals ?? []).filter((g) => g.status === "active").length;

  const squadIdsFor = (d: Domain): Set<string> => {
    const ids = new Set((connections ?? []).filter((c) => c.domain_id === d.id).map((c) => c.squad_id));
    if (d.issue_assignee_type === "squad" && d.issue_assignee) ids.add(d.issue_assignee);
    if (d.pull_assignee_type === "squad" && d.pull_assignee) ids.add(d.pull_assignee);
    return new Set([...ids].filter((id) => (squads ?? []).some((s) => s.id === id)));
  };

  const askDelete = (d: Domain) => {
    setDeleteError("");
    setDeleting(d);
  };
  const confirmDelete = () => {
    if (!deleting) return;
    setDeleteError("");
    del.mutate(deleting.id, {
      onSuccess: () => setDeleting(null),
      onError: (err) => setDeleteError(String(err)),
    });
  };

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="项目"
        desc="任务、定时任务和小队服务都挂在项目下。先选项目，再看它下面发生了什么。"
        action={
          <Button onClick={() => setEditor("new")} className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新建项目
          </Button>
        }
      />

      <StatStrip
        items={[
          {
            label: "项目数",
            value: list.length,
            hint: list.length === 0 ? "还没有项目" : `${repoCount} 个代码仓库 · ${list.length - repoCount} 个无仓库`,
          },
          {
            label: "进行中任务",
            value: activeGoals,
            tone: activeGoals ? "ok" : "muted",
            hint: activeGoals ? "跨所有项目正在跑" : "现在没有在跑的任务",
          },
          {
            label: "近 7 天新增",
            value: newThisWeek,
            tone: newThisWeek ? "info" : "muted",
            hint: newThisWeek ? "最近一周创建的项目" : "最近一周没有新项目",
          },
        ]}
      />

      <SectionCard title="项目列表" count={list.length} desc="打开项目可以看到它下面的任务、定时任务和服务小队" bodyClassName="p-0">
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="space-y-2 px-4 py-4">
                <Skeleton className="h-4 w-52" />
                <Skeleton className="h-3 w-80 max-w-full" />
              </div>
            ))}
          </div>
        ) : list.length === 0 ? (
          <Empty icon={<FolderGit2 className="h-5 w-5" />}>
            还没有项目。用右上角「新建项目」创建一个并挂上代码仓库，任务就有地方落地了。
          </Empty>
        ) : (
          <div className="divide-y divide-line">
            {list.map((d) => {
              const squadCount = squadIdsFor(d).size;
              const goalCount = (goals ?? []).filter((g) => g.domain_id === d.id).length;
              const scheduleCount = (schedules ?? []).filter((s) => s.domain_id === d.id).length;
              return (
                <ProjectRow
                  key={d.id}
                  project={d}
                  goalCount={goalCount}
                  scheduleCount={scheduleCount}
                  squadCount={squadCount}
                  onEdit={() => setEditor(d)}
                  onDelete={() => askDelete(d)}
                />
              );
            })}
          </div>
        )}
      </SectionCard>

      {editor && (
        <ProjectForm project={editor === "new" ? undefined : editor} onClose={() => setEditor(null)} />
      )}

      {deleting && (
        <ConfirmDialog
          title="删除项目"
          message={`删除项目「${deleting.name}」不可恢复。项目下还有任务或定时任务时会被拒绝——先清理再删。${deleteError ? ` 删除失败：${deleteError}` : ""}`}
          loading={del.isPending}
          onConfirm={confirmDelete}
          onClose={() => {
            setDeleting(null);
            setDeleteError("");
          }}
        />
      )}
    </PageBody>
  );
}

export default function ProjectsPage() {
  return (
    <Suspense fallback={<PageBody><Skeleton className="h-40 w-full" /></PageBody>}>
      <ProjectsInner />
    </Suspense>
  );
}