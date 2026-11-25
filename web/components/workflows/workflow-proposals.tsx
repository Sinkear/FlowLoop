"use client";

import Link from "next/link";
import { useState } from "react";
import {
  Check,
  GitCompareArrows,
  GitPullRequestArrow,
  Loader2,
  Play,
  X,
} from "lucide-react";
import { Button, Dialog, Empty, SectionCard, Skeleton } from "@/components/ui";
import {
  useApproveBlueprintProposal,
  useBlueprintProposalDiff,
  useBlueprintProposals,
  useRejectBlueprintProposal,
  useStartNextRoundFromProposal,
} from "@/lib/queries";
import type { BlueprintProposal } from "@/lib/types";
import { cn } from "@/lib/utils";
import { DiffCanvas, ProposalDiffChanges } from "@/components/blueprint-diff";
import { fmtInt } from "@/components/training/format";
import { WorkflowErrorBox, fullTime } from "@/components/workflows/workflow-shared";

const PROPOSAL_STATUS: Record<BlueprintProposal["status"], { label: string; cls: string }> = {
  pending: { label: "待批", cls: "border-amber-500/40 bg-amber-500/10 text-amber-300" },
  approved: { label: "已批准", cls: "border-emerald-500/40 bg-emerald-500/10 text-emerald-300" },
  rejected: { label: "已拒绝", cls: "border-line bg-surface-2 text-ink-3" },
};

function mutError(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * 优化提案（Phase C）：复盘产出结构化补丁，人工批准后落成草稿。
 * 这是次级信息——默认折叠成一行行提案，需要时再展开三色 diff。
 */
export function WorkflowProposals({
  blueprintId,
  onDraftCreated,
}: {
  blueprintId: string;
  onDraftCreated: (versionId: string) => void;
}) {
  const proposals = useBlueprintProposals(blueprintId);
  const [diffId, setDiffId] = useState<string | null>(null);
  const [approveId, setApproveId] = useState<string | null>(null);
  const [nextRoundId, setNextRoundId] = useState<string | null>(null);
  const [nextRoundResult, setNextRoundResult] = useState<{ id: string; title: string; status: string } | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const diff = useBlueprintProposalDiff(diffId);
  const approve = useApproveBlueprintProposal(blueprintId);
  const reject = useRejectBlueprintProposal(blueprintId);
  const nextRound = useStartNextRoundFromProposal(blueprintId);

  const list = proposals.data?.proposals ?? [];

  async function doApprove() {
    if (!approveId) return;
    setActionError(null);
    try {
      const r = await approve.mutateAsync(approveId);
      setApproveId(null);
      onDraftCreated(r.draft.id);
    } catch (e) {
      setActionError(mutError(e));
    }
  }

  async function doNextRound(start: boolean) {
    if (!nextRoundId) return;
    setActionError(null);
    try {
      const r = await nextRound.mutateAsync({ proposalId: nextRoundId, start });
      setNextRoundId(null);
      setNextRoundResult({ id: r.goal.id, title: r.goal.title, status: r.goal.status });
    } catch (e) {
      setActionError(mutError(e));
    }
  }

  return (
    <SectionCard
      title="优化提案"
      count={list.length}
      desc="复盘把“这张图哪里该改”算成结构化补丁：批准 = 落一份新草稿，拒绝 = 只留记录。批准不会自动发布。"
      bodyClassName="p-0"
    >
      {nextRoundResult && (
        <div className="border-b border-emerald-500/20 bg-emerald-500/[0.06] px-5 py-2.5 text-xs text-emerald-300">
          已创建下一轮：
          <Link href={`/goals/${nextRoundResult.id}`} className="mx-1 underline underline-offset-2 hover:text-emerald-200">
            {nextRoundResult.title}
          </Link>
          （{nextRoundResult.status === "backlog" ? "backlog，待启动" : "已开跑"}）
        </div>
      )}

      {proposals.isLoading ? (
        <div className="p-5">
          <Skeleton className="h-24 w-full" />
        </div>
      ) : proposals.isError ? (
        <div className="p-5">
          <WorkflowErrorBox title="读取提案失败" message={mutError(proposals.error)} />
        </div>
      ) : list.length === 0 ? (
        <Empty icon={<GitPullRequestArrow className="h-5 w-5" />}>
          还没有提案。任务执行完、复盘发现可复用的改法后，会在这里生成待批补丁。
        </Empty>
      ) : (
        <ul className="divide-y divide-line/60">
          {list.map((p) => {
            const st = PROPOSAL_STATUS[p.status];
            return (
              <li key={p.id} className="px-5 py-3.5">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={cn("rounded border px-1.5 py-px text-[10px]", st.cls)}>{st.label}</span>
                  <span className="min-w-0 flex-1 text-sm font-medium text-ink">{p.title}</span>
                  <span className="text-[11px] text-ink-3">{fullTime(p.created_at)}</span>
                </div>
                {p.hypothesis && (
                  <p className="mt-1.5 line-clamp-2 text-xs leading-relaxed text-ink-2">{p.hypothesis}</p>
                )}
                <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[10px] text-ink-3">
                  <span>{fmtInt(p.patches.length)} 个补丁 op</span>
                  <span>基于 {p.base_version_id.slice(0, 8)}</span>
                  {p.expected_metric && (
                    <span>
                      预期 {p.expected_metric} {p.expected_direction === "up" ? "↑" : p.expected_direction === "down" ? "↓" : "—"}
                    </span>
                  )}
                  {p.applied_version_id && <span>已生成草稿 {p.applied_version_id.slice(0, 8)}</span>}
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <Button
                    variant="outline"
                    className="h-7 px-2.5 text-[11px]"
                    onClick={() => setDiffId(diffId === p.id ? null : p.id)}
                  >
                    <GitCompareArrows className="h-3.5 w-3.5" />
                    {diffId === p.id ? "收起 diff" : "看 diff"}
                  </Button>
                  {p.status === "approved" && (
                    <Button className="h-7 px-2.5 text-[11px]" onClick={() => setNextRoundId(p.id)}>
                      <Play className="h-3.5 w-3.5" />
                      开始下一轮
                    </Button>
                  )}
                  {p.status === "pending" && (
                    <>
                      <Button
                        variant="outline"
                        className="h-7 px-2.5 text-[11px]"
                        disabled={reject.isPending}
                        onClick={() =>
                          reject.mutate(p.id, { onError: (e) => setActionError(mutError(e)) })
                        }
                      >
                        <X className="h-3.5 w-3.5" />
                        拒绝
                      </Button>
                      <Button
                        className="h-7 px-2.5 text-[11px]"
                        disabled={approve.isPending}
                        onClick={() => {
                          setActionError(null);
                          setApproveId(p.id);
                        }}
                      >
                        <Check className="h-3.5 w-3.5" />
                        批准
                      </Button>
                    </>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {diffId && (
        <div className="border-t border-line/70 bg-surface-2/20 p-4">
          {diff.isLoading ? (
            <Skeleton className="h-64 w-full" />
          ) : diff.isError ? (
            <WorkflowErrorBox title="读取 diff 失败" message={mutError(diff.error)} />
          ) : diff.data ? (
            <>
              <div className="grid gap-3 lg:grid-cols-2">
                <DiffCanvas label="改动前" graph={diff.data.base} />
                <DiffCanvas label="改动后" graph={diff.data.patched} />
              </div>
              <div className="mt-3">
                <ProposalDiffChanges diff={diff.data} />
              </div>
            </>
          ) : null}
        </div>
      )}

      {approveId && (
        <Dialog
          title="批准这个提案？"
          onClose={() => (approve.isPending ? null : setApproveId(null))}
          footer={
            <>
              <Button variant="outline" onClick={() => setApproveId(null)} disabled={approve.isPending}>
                取消
              </Button>
              <Button onClick={doApprove} disabled={approve.isPending}>
                {approve.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
                批准并开新草稿
              </Button>
            </>
          }
        >
          <p className="text-sm leading-relaxed text-ink-2">
            批准会把补丁应用到基于版本 <code className="font-mono text-ink-3">{approveId.slice(0, 8)}</code>{" "}
            的新草稿上。草稿需要手动发布才会被任务钉住。
          </p>
          {actionError && <WorkflowErrorBox title="批准失败" message={actionError} />}
        </Dialog>
      )}

      {nextRoundId && (
        <Dialog
          title="开始下一轮复盘？"
          onClose={() => (nextRound.isPending ? null : setNextRoundId(null))}
          footer={
            <>
              <Button variant="outline" onClick={() => setNextRoundId(null)} disabled={nextRound.isPending}>
                取消
              </Button>
              <Button variant="outline" onClick={() => doNextRound(false)} disabled={nextRound.isPending}>
                只建 backlog
              </Button>
              <Button onClick={() => doNextRound(true)} disabled={nextRound.isPending}>
                {nextRound.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
                发布并立即开始
              </Button>
            </>
          }
        >
          <p className="text-sm leading-relaxed text-ink-2">
            “开始下一轮”会：发布提案应用版本 → 复制原训练目标 → 把新任务钉到新版本。
            {` `}不想立刻占资源，就只落 backlog，之后手动启动。
          </p>
          {actionError && <WorkflowErrorBox title="创建下一轮失败" message={actionError} />}
        </Dialog>
      )}

      {actionError && !approveId && !nextRoundId && (
        <div className="px-5 pb-3">
          <WorkflowErrorBox title="操作失败" message={actionError} />
        </div>
      )}
    </SectionCard>
  );
}