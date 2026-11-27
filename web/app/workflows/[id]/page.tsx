"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { Button, ConfirmDialog, Dialog, Field, PageBody, Skeleton, inputCls } from "@/components/ui";
import {
  useBlueprint,
  useBlueprintGraph,
  useBlueprintProposals,
  useDeleteBlueprint,
  useDeleteBlueprintVersion,
  useNewBlueprintDraft,
  usePublishBlueprintVersion,
  useSquads,
} from "@/lib/queries";
import { useCreateDialog } from "@/components/create/create-dialog";
import { WorkflowHero } from "@/components/workflows/workflow-hero";
import { WorkflowGraphPanel } from "@/components/workflows/workflow-graph-panel";
import { WorkflowVersions } from "@/components/workflows/workflow-versions";
import { WorkflowProposals } from "@/components/workflows/workflow-proposals";
import { SquadReviews } from "@/components/squads/squad-reviews";
import { WorkflowAB } from "@/components/workflows/workflow-ab";
import { WorkflowJsonCard } from "@/components/workflows/workflow-json-card";
import { WorkflowSpecEditor } from "@/components/workflows/workflow-spec-editor";
import { WorkflowTabs, type WorkflowTab } from "@/components/workflows/workflow-tabs";
import { WorkflowErrorBox } from "@/components/workflows/workflow-shared";

function mutError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * 工作流详情：规范图 / 版本 / JSON 合并为一个「规范」分区，切换版本时图和原文一起变；
 * 复盘、提案、A/B 作为次级页签。
 */
export default function WorkflowDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const { open } = useCreateDialog();

  const { data: detail, isLoading, isError, error, refetch } = useBlueprint(id);
  const { data: squads } = useSquads();
  const proposals = useBlueprintProposals(id);
  const proposalList = proposals.data?.proposals ?? [];
  const pendingProposalCount = proposalList.filter((proposal) => proposal.status === "pending").length;
  const versions = useMemo(
    () => [...(detail?.versions ?? [])].sort((a, b) => b.version - a.version),
    [detail?.versions],
  );
  const blueprint = detail?.blueprint;

  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [tab, setTab] = useState<WorkflowTab>("spec");
  const effectiveVersionId =
    versions.find((v) => v.id === selectedVersionId)?.id ??
    versions.find((v) => v.status === "published")?.id ??
    versions[0]?.id ??
    "";
  const selected = versions.find((v) => v.id === effectiveVersionId) ?? null;

  const graph = useBlueprintGraph(effectiveVersionId);
  const squadId = blueprint?.squad_id ?? "";
  const squad = squads?.find((s) => s.id === squadId);

  const prettyContent = useMemo(() => {
    if (!selected) return "";
    try {
      return JSON.stringify(JSON.parse(selected.content), null, 2);
    } catch {
      return selected.content;
    }
  }, [selected]);

  const newDraft = useNewBlueprintDraft(id);
  const publish = usePublishBlueprintVersion();
  const del = useDeleteBlueprint();
  const delVersion = useDeleteBlueprintVersion();

  const [editorOpen, setEditorOpen] = useState(false);
  const [publishOpen, setPublishOpen] = useState(false);
  const [publishChangelog, setPublishChangelog] = useState("");
  const [newDraftOpen, setNewDraftOpen] = useState(false);
  const [newDraftChangelog, setNewDraftChangelog] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [confirmDeleteVersionId, setConfirmDeleteVersionId] = useState<string | null>(null);
  const [deleteVersionError, setDeleteVersionError] = useState<string | null>(null);

  // 批准 RSI 提案时后端会自动创建下一轮训练 goal（backlog，未启动）——批准后
  // 会切去「规范」页签、提案组件卸载，所以这条引导 toast 由页面层持有（样式与
  // goal-board 的自研 toast 同款：项目没有 toast 库）。tone="warn" 用于后端
  // 建下一轮 goal 失败但批准本身已成功的降级提示。
  const [approveToast, setApproveToast] = useState<{
    tone: "ok" | "warn";
    text: string;
    goalId?: string;
  } | null>(null);

  useEffect(() => {
    if (!approveToast) return;
    const t = setTimeout(() => setApproveToast(null), 4200);
    return () => clearTimeout(t);
  }, [approveToast]);

  function openPublish() {
    setPublishChangelog("");
    setPublishOpen(true);
  }

  async function doPublish() {
    if (!selected) return;
    try {
      await publish.mutateAsync({ versionId: selected.id, changelog: publishChangelog });
      setPublishOpen(false);
    } catch {
      /* 错误由 ErrorBox 展示 */
    }
  }

  function openNewDraft() {
    setNewDraftChangelog("");
    setNewDraftOpen(true);
  }

  async function doNewDraft() {
    try {
      const v = await newDraft.mutateAsync(newDraftChangelog);
      setNewDraftOpen(false);
      setSelectedVersionId(v.id);
    } catch {
      /* 错误由 ErrorBox 展示 */
    }
  }

  function openDelete() {
    setDeleteError(null);
    setDeleteOpen(true);
  }

  async function confirmDelete() {
    try {
      await del.mutateAsync(id);
      router.push("/workflows");
    } catch (e) {
      setDeleteError(mutError(e));
    }
  }

  function openDeleteVersion(versionId: string) {
    setDeleteVersionError(null);
    setConfirmDeleteVersionId(versionId);
  }

  async function confirmDeleteVersion() {
    if (!confirmDeleteVersionId) return;
    try {
      await delVersion.mutateAsync(confirmDeleteVersionId);
      setSelectedVersionId("");
      setConfirmDeleteVersionId(null);
    } catch (e) {
      setDeleteVersionError(mutError(e));
    }
  }

  const graphError = graph.isError ? mutError(graph.error) : null;
  const deletingVersion = versions.find((v) => v.id === confirmDeleteVersionId) ?? null;

  return (
    <PageBody>
      {isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-40 w-full" />
          <Skeleton className="h-[480px] w-full" />
        </div>
      ) : isError || !blueprint ? (
        <div className="rounded-xl border border-line bg-surface-1 p-6">
          <WorkflowErrorBox title="读取工作流失败" message={mutError(error)} />
          <Button variant="outline" className="mt-3" onClick={() => refetch()}>
            重试
          </Button>
        </div>
      ) : (
        <>
          <WorkflowHero
            blueprint={blueprint}
            squad={squad}
            squadsLoaded={!!squads}
            selected={selected}
            onCreateTask={() => selected && open("task", { blueprintVersionId: selected.id })}
            onPublish={openPublish}
            onEditStructure={() => setEditorOpen(true)}
            onCreateDraft={openNewDraft}
            onDelete={openDelete}
          />

          <WorkflowTabs
            value={tab}
            onChange={setTab}
            proposalCount={proposalList.length}
            pendingProposalCount={pendingProposalCount}
          />

          {tab === "spec" && (
            <div className="space-y-5">
              <WorkflowGraphPanel
                versions={versions}
                selectedId={effectiveVersionId}
                selected={selected}
                onSelect={setSelectedVersionId}
                graph={graph.data}
                loading={graph.isLoading}
                error={graphError}
                onEditStructure={() => setEditorOpen(true)}
                onCreateDraft={openNewDraft}
              />
              <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_minmax(0,380px)]">
                <WorkflowVersions
                  versions={versions}
                  selectedId={effectiveVersionId}
                  selected={selected}
                  squadId={squadId}
                  onSelect={setSelectedVersionId}
                  onEditStructure={() => setEditorOpen(true)}
                  onPublish={openPublish}
                  onDeleteDraft={() => selected && openDeleteVersion(selected.id)}
                  onNewDraft={openNewDraft}
                  onCreateTask={() => selected && open("task", { blueprintVersionId: selected.id })}
                />
                {selected && <WorkflowJsonCard content={prettyContent} />}
              </div>
            </div>
          )}

          {tab === "reviews" && <SquadReviews blueprintId={id} />}

          {tab === "proposals" && (
            <WorkflowProposals
              blueprintId={id}
              onDraftCreated={(versionId) => {
                setSelectedVersionId(versionId);
                setTab("spec");
              }}
              onNextRoundGoal={(goalId, createError) =>
                createError
                  ? setApproveToast({
                      tone: "warn",
                      text: `提案已批准，但自动创建下一轮训练 goal 失败：${createError}`,
                    })
                  : setApproveToast({
                      tone: "ok",
                      text: "提案已批准，并已自动创建下一轮训练 goal（未启动）",
                      goalId,
                    })
              }
            />
          )}

          {tab === "ab" && <WorkflowAB blueprintId={id} />}
        </>
      )}


      {editorOpen && selected && selected.status === "draft" && (
        <WorkflowSpecEditor
          version={selected}
          squadId={squadId}
          onClose={() => setEditorOpen(false)}
          onSaved={() => setEditorOpen(false)}
        />
      )}

      {publishOpen && selected && (
        <Dialog
          title={`发布 v${selected.version}`}
          onClose={() => (publish.isPending ? null : setPublishOpen(false))}
          footer={
            <>
              <Button variant="outline" onClick={() => setPublishOpen(false)} disabled={publish.isPending}>
                取消
              </Button>
              <Button onClick={doPublish} disabled={publish.isPending} className="bg-emerald-600 hover:bg-emerald-500">
                {publish.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                发布
              </Button>
            </>
          }
        >
          <p className="text-sm leading-relaxed text-ink-2">
            发布后这一版内容冻结，成为可被任务钉住的快照。之后再改流程需要开新草稿。
          </p>
          <Field label="变更说明" hint="可选。会显示在版本时间线上，方便回溯这次改了什么。">
            <input
              value={publishChangelog}
              onChange={(e) => setPublishChangelog(e.target.value)}
              className={inputCls}
              placeholder="例如：把验证节点从 agent 校验改为 machine 校验"
            />
          </Field>
          {publish.isError && <WorkflowErrorBox title="发布没过校验" message={mutError(publish.error)} />}
        </Dialog>
      )}

      {newDraftOpen && (
        <Dialog
          title="开新草稿"
          onClose={() => (newDraft.isPending ? null : setNewDraftOpen(false))}
          footer={
            <>
              <Button variant="outline" onClick={() => setNewDraftOpen(false)} disabled={newDraft.isPending}>
                取消
              </Button>
              <Button onClick={doNewDraft} disabled={newDraft.isPending}>
                {newDraft.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                创建草稿
              </Button>
            </>
          }
        >
          <p className="text-sm leading-relaxed text-ink-2">
            {selected
              ? `会复制 v${selected.version} 的内容，开一份 v${versions[0] ? versions[0].version + 1 : 1} 草稿（仍是草稿，需再发布）。`
              : "会以当前最新版本为底，开一份新草稿。"}
          </p>
          <Field label="变更说明" hint="可选。描述这轮准备改什么。">
            <input
              value={newDraftChangelog}
              onChange={(e) => setNewDraftChangelog(e.target.value)}
              className={inputCls}
              placeholder="例如：缩短验证环节，增加一个失败重试分支"
            />
          </Field>
          {newDraft.isError && <WorkflowErrorBox title="创建草稿失败" message={mutError(newDraft.error)} />}
        </Dialog>
      )}

      {deleteOpen && (
        <ConfirmDialog
          title="删除工作流？"
          message={`删除「${blueprint?.name ?? ""}」及其 ${versions.length} 个版本？删除后不可恢复。${deleteError ? `（${deleteError}）` : ""}`}
          loading={del.isPending}
          onConfirm={confirmDelete}
          onClose={() => setDeleteOpen(false)}
        />
      )}

      {confirmDeleteVersionId && deletingVersion && (
        <ConfirmDialog
          title="删除草稿版本？"
          message={`删除草稿 v${deletingVersion.version}？只会删这一份草稿，已发布版本不受影响。${deleteVersionError ? `（${deleteVersionError}）` : ""}`}
          loading={delVersion.isPending}
          onConfirm={confirmDeleteVersion}
          onClose={() => setConfirmDeleteVersionId(null)}
        />
      )}

      {approveToast && (
        <div
          className={`fixed bottom-6 left-1/2 z-[70] max-w-[min(560px,90vw)] -translate-x-1/2 rounded-lg border bg-surface-1 px-3.5 py-2 text-[12px] shadow-xl shadow-black/30 page-enter ${
            approveToast.tone === "warn" ? "border-amber-500/40 text-amber-300" : "border-accent/40 text-ink"
          }`}
        >
          {approveToast.text}
          {approveToast.goalId && (
            <Link
              href={`/goals/${approveToast.goalId}`}
              className="ml-1 text-accent underline underline-offset-2 hover:text-accent-dim"
            >
              查看 goal
            </Link>
          )}
        </div>
      )}
    </PageBody>
  );
}