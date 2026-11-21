"use client";

import { useState } from "react";
import { Check, Copy, FolderGit2, Plus } from "lucide-react";
import { useDomains, useDeleteDomain } from "@/lib/queries";
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

function ProjectRow({ project: d, onEdit, onDelete }: { project: Domain; onEdit: () => void; onDelete: () => void }) {
  const [copied, setCopied] = useState(false);
  const repo = d.type !== "scratch";
  const acceptance = ACCEPTANCE_TAG[d.acceptance_level] ?? ACCEPTANCE_TAG[""];

  const copy = async () => {
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
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="truncate text-sm font-medium text-ink">{d.name}</span>
          <Tag className="border-line text-ink-2">{repo ? "代码仓库" : "无仓库工作区"}</Tag>
          <Tag className={acceptance.cls}>{acceptance.label}</Tag>
          {d.issue_assignee && <Tag className="border-sky-500/30 text-sky-300">Issue 自动</Tag>}
          {d.pull_assignee && <Tag className="border-purple-500/30 text-purple-300">PR 自动</Tag>}
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
      </div>

      <div className="flex shrink-0 items-center gap-1">
        <Button variant="outline" onClick={onEdit}>配置</Button>
        <Button variant="ghost" onClick={onDelete} className="text-red-400 hover:bg-red-500/10 hover:text-red-300">
          删除
        </Button>
      </div>
    </div>
  );
}

export default function ProjectsPage() {
  const { data: domains, isLoading } = useDomains();
  const del = useDeleteDomain();
  const [editor, setEditor] = useState<Domain | "new" | null>(null);
  const [deleting, setDeleting] = useState<Domain | null>(null);
  const [deleteError, setDeleteError] = useState("");

  const list = domains ?? [];
  const repoCount = list.filter((d) => d.type !== "scratch").length;
  const weekAgo = Date.now() - 7 * 24 * 60 * 60 * 1000;
  const newThisWeek = list.filter((d) => {
    const t = new Date(d.created_at).getTime();
    return !Number.isNaN(t) && t >= weekAgo;
  }).length;

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
        desc="项目是任务的归属地，一个项目对应一个代码仓库或无仓库工作区；新任务必须挂在项目下。"
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
            label: "代码仓库",
            value: repoCount,
            tone: repoCount ? "ok" : "muted",
            hint: repoCount ? "有共享仓库和 worktree" : "全部为无仓库工作区",
          },
          {
            label: "近 7 天新增",
            value: newThisWeek,
            tone: newThisWeek ? "info" : "muted",
            hint: newThisWeek ? "最近一周创建的项目" : "最近一周没有新项目",
          },
        ]}
      />

      <SectionCard title="项目列表" count={list.length} desc="配置里可以改仓库、验收级别和自动处理" bodyClassName="p-0">
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
            <span>还没有项目。创建一个项目并挂上代码仓库，任务就有地方落地了。</span>
            <span className="mt-4 block">
              <Button onClick={() => setEditor("new")} className="gap-1.5">
                <Plus className="h-3.5 w-3.5" />
                新建项目
              </Button>
            </span>
          </Empty>
        ) : (
          <div className="divide-y divide-line">
            {list.map((d) => (
              <ProjectRow key={d.id} project={d} onEdit={() => setEditor(d)} onDelete={() => askDelete(d)} />
            ))}
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