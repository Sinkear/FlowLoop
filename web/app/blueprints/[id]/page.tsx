"use client";

// 蓝本详情（Phase A + UX 闭环 + m02841 UI 优化）：版本史 + 规范图 + 版本操作。
// 规范图与执行图共用 WorkflowGraph 形状（提案 §4.12）——同一渲染器，
// 「计划长什么样」和「实际怎么跑」是同一种图。
//
// 版面顺序就是用户的心路：这是什么（页头）→ 现在卡在哪一步、下一步点什么
// （引导卡）→ 有哪几个版本（版本时间线）→ 这个版本长什么样（规范图）→
// 原文（JSON）。原来「下一步该点哪」埋在三张卡片下面，现在提到页头正下方。

import { useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import {
  ArrowRight,
  Check,
  Copy,
  Dot,
  GitBranchPlus,
  Loader2,
  PenLine,
  Rocket,
  Waypoints,
  GitPullRequestArrow,
  ThumbsUp,
  ThumbsDown,
  GitCompareArrows,
  Play,
  Trash2,
} from "lucide-react";
import { Badge, Button, ConfirmDialog, Dialog, Empty, Field, PageBody, SectionCard, Skeleton, inputCls } from "@/components/ui";
import {
  useBlueprint,
  useBlueprintGraph,
  useNewBlueprintDraft,
  usePublishBlueprintVersion,
  useUpdateBlueprintDraft,
  useBlueprintProposals,
  useBlueprintProposalDiff,
  useApproveBlueprintProposal,
  useRejectBlueprintProposal,
  useStartNextRoundFromProposal,
  useBlueprintAB,
  useDeleteBlueprint,
  useDeleteBlueprintVersion,
} from "@/lib/queries";
import type { BlueprintProposal } from "@/lib/types";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { DASH, fmtInt } from "@/components/training/format";
import { ABRow, DiffCanvas, ProposalDiffChanges, abMetricKeys } from "@/components/blueprint-diff";
import { cn } from "@/lib/utils";

function fullTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString("zh-CN");
}

// ── 进度三拍 ──
// 蓝本的生命周期只有三步：草稿 → 发布 → 绑定任务。把它画出来，用户就不必
// 从文案里推断自己站在哪。
const STEPS = [
  { key: "draft", label: "写草稿" },
  { key: "publish", label: "发布" },
  { key: "bind", label: "绑定任务" },
];

function StepTrack({ current }: { current: number }) {
  return (
    <ol className="mt-3 flex flex-wrap items-center gap-1.5 text-[11px]">
      {STEPS.map((s, i) => (
        <li key={s.key} className="flex items-center gap-1.5">
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-medium transition-colors",
              i < current
                ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
                : i === current
                  ? "border-accent/45 bg-accent/10 text-accent"
                  : "border-line text-ink-3"
            )}
          >
            {i < current ? <Check className="h-3 w-3" /> : <span className="font-mono">{i + 1}</span>}
            {s.label}
          </span>
          {i < STEPS.length - 1 && <span className="h-px w-3 bg-line-strong" aria-hidden />}
        </li>
      ))}
    </ol>
  );
}

// 规范图节点类型分布：比「节点 6 / 边 5」四个数字更能说明这份计划的结构——
// 一眼看出它有几次分叉、有没有人工门、是不是拆解型。
// 错误统一展示块：服务端校验失败时 message 里带逐条原因。
function ErrorBox({ title, message }: { title: string; message: string }) {
  return (
    <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
      <p className="mb-1 font-medium">{title}</p>
      <pre className="whitespace-pre-wrap font-mono">{message}</pre>
    </div>
  );
}

// 复制按钮：蓝本的价值一半在「拿走这段 JSON」，没有复制就得手动框选。
// clipboard API 在非安全上下文（http 非 localhost）或权限被拒时会抛错，
// 所以备一条 textarea + execCommand 的老路——蓝本详情常在内网 http 下打开。
function CopyButton({ text }: { text: string }) {
  const [state, setState] = useState<"idle" | "done" | "fail">("idle");
  async function copy() {
    // clipboard.writeText 在没有用户手势授权/权限弹窗被挂起的环境里可能
    // 既不 resolve 也不 reject——所以给它 1s，超时就落到 execCommand。
    const res = await Promise.race([
      navigator.clipboard
        .writeText(text)
        .then(() => "ok" as const)
        .catch(() => "err" as const),
      new Promise<"timeout">((r) => setTimeout(() => r("timeout"), 1000)),
    ]);
    let ok = res === "ok";
    if (!ok) {
      try {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        ok = document.execCommand("copy");
        document.body.removeChild(ta);
      } catch {
        ok = false;
      }
    }
    setState(ok ? "done" : "fail");
    setTimeout(() => setState("idle"), 1600);
  }
  return (
    <button
      type="button"
      onClick={() => void copy()}
      className="inline-flex items-center gap-1 rounded border border-line px-1.5 py-0.5 text-[11px] text-ink-3 transition-colors hover:border-accent/40 hover:text-ink-2"
      title={state === "fail" ? "浏览器拒绝了剪贴板——展开下方 JSON 手动复制" : "复制完整 JSON"}
    >
      {state === "done" ? (
        <Check className="h-3 w-3 text-emerald-300" />
      ) : (
        <Copy className="h-3 w-3" />
      )}
      {state === "done" ? "已复制" : state === "fail" ? "复制失败" : "复制"}
    </button>
  );
}


export default function BlueprintDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const router = useRouter();
  const { data: detail, isLoading, isError, error, refetch } = useBlueprint(id);
  const versions = useMemo(() => detail?.versions ?? [], [detail]);

  // 默认选中最新版本（列表按新→旧排序时第一个即最新）；published 优先展示。
  const [selectedVersionId, setSelectedVersionId] = useState<string>("");
  const effectiveVersionId =
    versions.find((v) => v.id === selectedVersionId)?.id ??
    versions.find((v) => v.status === "published")?.id ??
    versions[0]?.id ??
    "";
  const selected = versions.find((v) => v.id === effectiveVersionId) ?? null;

  const {
    data: graph,
    isLoading: graphLoading,
    isError: graphError,
    error: graphErr,
  } = useBlueprintGraph(effectiveVersionId);

  const prettyContent = useMemo(() => {
    if (!selected?.content) return "";
    try {
      return JSON.stringify(JSON.parse(selected.content), null, 2);
    } catch {
      return selected.content;
    }
  }, [selected?.content]);

  // ── 版本操作（UX 闭环） ──
  const newDraft = useNewBlueprintDraft(id);
  const updateDraft = useUpdateBlueprintDraft();
  const publish = usePublishBlueprintVersion();

  const [editOpen, setEditOpen] = useState(false);
  const [editContent, setEditContent] = useState("");
  const [publishOpen, setPublishOpen] = useState(false);
  const [publishChangelog, setPublishChangelog] = useState("");
  const [newDraftOpen, setNewDraftOpen] = useState(false);
  const [newDraftChangelog, setNewDraftChangelog] = useState("");

  // ── 提案（Phase C §12）：列表 + diff 预览 + 批准/拒绝 ──
  const proposals = useBlueprintProposals(id);
  const [diffProposalId, setDiffProposalId] = useState<string | null>(null);
  const proposalDiff = useBlueprintProposalDiff(diffProposalId);
  const approveProposal = useApproveBlueprintProposal(id);
  const rejectProposal = useRejectBlueprintProposal(id);
  const startNextRound = useStartNextRoundFromProposal(id);
  const [confirmApproveId, setConfirmApproveId] = useState<string | null>(null);
  const [confirmNextRoundId, setConfirmNextRoundId] = useState<string | null>(null);
  const [nextRoundResult, setNextRoundResult] = useState<{ id: string; title: string; status: string } | null>(null);

  async function doNextRound(proposalId: string, start: boolean) {
    try {
      const { goal } = await startNextRound.mutateAsync({ proposalId, start });
      setConfirmNextRoundId(null);
      setNextRoundResult({ id: goal.id, title: goal.title, status: goal.status });
    } catch {
      // 错误在对话框里展示（startNextRound.error）
    }
  }

  async function doApprove(proposalId: string) {
    try {
      const { draft } = await approveProposal.mutateAsync(proposalId);
      setConfirmApproveId(null);
      setDiffProposalId(null);
      if (draft?.id) setSelectedVersionId(draft.id); // 批准产出新草稿，直接切过去
    } catch {
      // 错误在对话框里展示（approveProposal.error）
    }
  }

  // ── 同题 A/B（§12.3）：两个 goal id 一比 ──
  const [abGoalA, setAbGoalA] = useState("");
  const [abGoalB, setAbGoalB] = useState("");
  const ab = useBlueprintAB(id, abGoalA.trim() || null, abGoalB.trim() || null);

  const mutError = (e: unknown) => (e instanceof Error ? e.message : null);

  // ── 删除本蓝本（连带版本/提案）：被活跃目标 pin 住时服务端 409 ──
  const del = useDeleteBlueprint();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  async function confirmDelete() {
    try {
      await del.mutateAsync(id);
      router.push("/blueprints");
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : String(e));
    }
  }

  // ── 删除草稿版本（乱填草稿的清理口）：已发布/被引用的由服务端 409 拒绝 ──
  const delVersion = useDeleteBlueprintVersion();
  const [confirmDeleteVersionId, setConfirmDeleteVersionId] = useState<string | null>(null);
  const [deleteVersionError, setDeleteVersionError] = useState("");
  async function confirmDeleteVersion() {
    if (!confirmDeleteVersionId) return;
    try {
      await delVersion.mutateAsync(confirmDeleteVersionId);
      setConfirmDeleteVersionId(null);
      setDeleteVersionError("");
    } catch (e) {
      setDeleteVersionError(e instanceof Error ? e.message : String(e));
    }
  }

  function openEdit() {
    if (!selected) return;
    setEditContent(prettyContent || selected.content);
    setEditOpen(true);
  }

  async function saveEdit() {
    if (!selected) return;
    try {
      await updateDraft.mutateAsync({ versionId: selected.id, content: editContent });
      setEditOpen(false);
    } catch {
      // 错误在对话框内展示（updateDraft.error）
    }
  }

  async function doPublish() {
    if (!selected) return;
    try {
      await publish.mutateAsync({ versionId: selected.id, changelog: publishChangelog });
      setPublishOpen(false);
      setPublishChangelog("");
    } catch {
      // 错误在对话框内展示（publish.error）
    }
  }

  async function doNewDraft() {
    try {
      const v = await newDraft.mutateAsync(newDraftChangelog);
      setNewDraftOpen(false);
      setNewDraftChangelog("");
      setSelectedVersionId(v.id); // 直接切到新草稿，用户下一步通常就是改它
    } catch {
      // 错误在对话框内展示（newDraft.error）
    }
  }

  const isPublished = selected?.status === "published";
  const stepCurrent = isPublished ? 2 : 1;

  return (
    <PageBody>
      {/* ── 页头：它是什么 ── */}
      <div>
        <Link
          href="/blueprints"
          className="text-sm text-ink-3 transition-colors hover:text-ink hover:underline"
        >
          ← 返回蓝本列表
        </Link>
        <div className="mt-3 flex flex-wrap items-center gap-2.5">
          <h1 className="text-2xl font-semibold tracking-tight text-ink">
            {detail?.blueprint?.name ?? "蓝本"}
          </h1>
          {selected && <Badge status={selected.status} />}
          {detail?.blueprint && (
            <Button
              variant="outline"
              className="ml-auto gap-1.5 border-red-500/30 text-red-300 hover:bg-red-500/10"
              onClick={() => {
                setDeleteError("");
                setDeleteOpen(true);
              }}
            >
              <Trash2 className="h-3.5 w-3.5" /> 删除蓝本
            </Button>
          )}
        </div>
        {detail?.blueprint?.description && (
          <p className="mt-1.5 max-w-3xl whitespace-pre-wrap text-sm leading-relaxed text-ink-3">
            {detail.blueprint.description}
          </p>
        )}
        {detail?.blueprint && (
          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-ink-3">
            <span>{fmtInt(versions.length)} 个版本</span>
            <Dot className="h-3 w-3 opacity-40" />
            <span>创建于 {fullTime(detail.blueprint.created_at)}</span>
            <Dot className="h-3 w-3 opacity-40" />
            <span className="truncate" title={detail.blueprint.id}>
              id {detail.blueprint.id.slice(0, 12)}
            </span>
          </div>
        )}
      </div>

      {isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-72 w-full" />
        </div>
      ) : isError || !detail ? (
        <div className="p-6 text-center text-sm text-ink-3">
          <p>加载失败：{error instanceof Error ? error.message : String(error)}</p>
          <button
            className="mt-2 rounded-md border border-line px-3 py-1.5 text-xs hover:bg-surface-2"
            onClick={() => refetch()}
          >
            重试
          </button>
        </div>
      ) : (
        <>
          {/* ── 引导卡：现在卡在哪一步、下一步点什么 ──
              状态色用 -300 槽位（浅色主题会重映射成深色可读值），
              不再用 -200 的浅薄荷色——那在浅色主题下几乎看不见。 */}
          {selected && (
            <div
              className={cn(
                "rounded-lg border px-4 py-3.5",
                isPublished
                  ? "border-emerald-500/30 bg-emerald-500/[0.06]"
                  : "border-amber-500/30 bg-amber-500/[0.06]"
              )}
            >
              <div className="flex flex-wrap items-start gap-3">
                <span
                  className={cn(
                    "mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md",
                    isPublished
                      ? "bg-emerald-500/15 text-emerald-300"
                      : "bg-amber-500/15 text-amber-300"
                  )}
                >
                  {isPublished ? <Check className="h-4 w-4" /> : <PenLine className="h-4 w-4" />}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-ink">
                    {isPublished
                      ? `v${selected.version} 已发布 —— 可以绑定任务了`
                      : `当前选中的 v${selected.version} 是草稿 —— 还不能绑定任务`}
                  </p>
                  <p className="mt-1 text-xs leading-relaxed text-ink-2">
                    {isPublished
                      ? "任务激活时，平台按下面这张规范图把任务编译成带依赖的子任务：执行顺序由代码保证，上一步没验证通过，下一步不会开始。任务详情页还能对照「计划图 vs 实际执行图」。"
                      : "在下方「版本」里点「编辑内容」继续改，或点「发布此版本」冻结它。发布后内容不可改；后续迭代走「开新草稿」（复制最新版开 v+1，是迭代蓝本的正规通道）。"}
                  </p>
                  <StepTrack current={stepCurrent} />
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2">
                  {isPublished ? (
                    <Link
                      href={`/goals/new?blueprint=${selected.id}`}
                      className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors hover:bg-accent-dim"
                    >
                      用此版本新建任务 <ArrowRight className="h-3.5 w-3.5" />
                    </Link>
                  ) : (
                    <>
                      <Button
                        onClick={() => {
                          setPublishChangelog("");
                          setPublishOpen(true);
                        }}
                        className="gap-1.5"
                      >
                        <Rocket className="h-3.5 w-3.5" /> 发布此版本
                      </Button>
                      <Button variant="outline" onClick={openEdit} className="gap-1.5">
                        <PenLine className="h-3.5 w-3.5" /> 编辑内容
                      </Button>
                    </>
                  )}
                </div>
              </div>
            </div>
          )}

          {/* ── 版本时间线：版本是不可变单元，选中即查看该版本的规范图 ── */}
          <SectionCard
            title="版本"
            count={versions.length}
            desc="版本不可变——选中哪个，下面就渲染哪个版本的规范图"
          >
            {versions.length === 0 ? (
              <div className="p-6">
                <Empty>该蓝本还没有版本。</Empty>
              </div>
            ) : (
              <ol className="divide-y divide-line">
                {versions.map((v) => {
                  const active = v.id === effectiveVersionId;
                  const published = v.status === "published";
                  return (
                    <li key={v.id} className="relative">
                      {active && (
                        <span className="absolute inset-y-0 left-0 w-0.5 bg-accent" aria-hidden />
                      )}
                      <button
                        type="button"
                        onClick={() => setSelectedVersionId(v.id)}
                        aria-current={active ? "true" : undefined}
                        className={cn(
                          "flex w-full items-start gap-3 px-4 py-3 text-left transition-colors",
                          active ? "bg-accent/[0.05]" : "hover:bg-surface-2/40"
                        )}
                      >
                        <span
                          className={cn(
                            "mt-1.5 h-2 w-2 shrink-0 rounded-full",
                            published ? "bg-emerald-400" : "bg-amber-400",
                            active && "ring-2 ring-accent/25"
                          )}
                        />
                        <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-center gap-2">
                            <span
                              className={cn(
                                "font-mono text-sm",
                                active ? "font-semibold text-ink" : "text-ink"
                              )}
                            >
                              v{v.version}
                            </span>
                            <Badge status={v.status} />
                            {active && (
                              <span className="rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                                正在查看
                              </span>
                            )}
                            <span className="ml-auto font-mono text-[11px] text-ink-3">
                              {v.published_at
                                ? `发布于 ${fullTime(v.published_at)}`
                                : `创建于 ${fullTime(v.created_at)}`}
                            </span>
                          </span>
                          <span className="mt-0.5 line-clamp-2 block text-xs leading-relaxed text-ink-2">
                            {v.changelog || "（未写变更说明）"}
                          </span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ol>
            )}

            {selected && (
              <div className="border-t border-line px-4 py-3">
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[11px] text-ink-3">
                  <span className="truncate" title={selected.id}>
                    id {selected.id.slice(0, 12)}
                  </span>
                  <span title="内容哈希（读取时校验，保证版本不可变）">
                    hash {selected.content_hash.slice(0, 12)}
                  </span>
                  <span>{prettyContent ? `${fmtInt(prettyContent.length)} 字符` : ""}</span>
                </div>
                {/* 对当前选中版本的操作：草稿可改可发布；已发布可带走建任务。 */}
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  {selected.status === "draft" && (
                    <>
                      <Button variant="outline" onClick={openEdit} className="gap-1.5">
                        <PenLine className="h-3.5 w-3.5" /> 编辑内容
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => {
                          setPublishChangelog("");
                          setPublishOpen(true);
                        }}
                        className="gap-1.5 border-emerald-500/40 text-emerald-300 hover:bg-emerald-500/10"
                      >
                        <Rocket className="h-3.5 w-3.5" /> 发布此版本
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => {
                          setDeleteVersionError("");
                          setConfirmDeleteVersionId(selected.id);
                        }}
                        className="gap-1.5 border-red-500/40 text-red-300 hover:bg-red-500/10"
                        title="删除这个草稿版本（已发布版本不可删；被引用时会拒绝）"
                      >
                        <Trash2 className="h-3.5 w-3.5" /> 删除草稿
                      </Button>
                    </>
                  )}
                  {selected.status === "published" && (
                    <Link
                      href={`/goals/new?blueprint=${selected.id}`}
                      className="inline-flex items-center gap-1.5 rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-1.5 text-xs font-medium text-emerald-300 transition-colors hover:bg-emerald-500/20"
                    >
                      <ArrowRight className="h-3.5 w-3.5" /> 用此版本新建任务
                    </Link>
                  )}
                  <Button
                    variant="outline"
                    onClick={() => {
                      setNewDraftChangelog("");
                      setNewDraftOpen(true);
                    }}
                    className="gap-1.5"
                    title="复制当前最新版本开一个新草稿（已发布版本不可改，改动走这里）"
                  >
                    <GitBranchPlus className="h-3.5 w-3.5" /> 开新草稿
                  </Button>
                </div>
              </div>
            )}
          </SectionCard>

          {/* ── 提案（Phase C §12.1）：结构化补丁 → 人工批准 → 新草稿 ── */}
          <SectionCard
            title="优化提案"
            count={proposals.data?.proposals?.length ?? 0}
            desc="结构化补丁提案：批准 = 以假设为说明开一个新草稿（不自动发布）；拒绝 = 一锤定音。提案可由 RSI finding 翻译而来，也接受手工创建"
          >
            {nextRoundResult && (
              <div className="mx-4 mt-3 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-200">
                已创建下一轮：
                <Link href={`/goals/${nextRoundResult.id}`} className="font-medium underline underline-offset-2">
                  {nextRoundResult.title}
                </Link>
                <span className="text-emerald-300/70">（{nextRoundResult.status === "active" ? "已开跑" : "backlog，待启动"}）</span>
              </div>
            )}
            {proposals.isLoading ? (
              <div className="p-4"><Skeleton className="h-16 w-full" /></div>
            ) : (proposals.data?.proposals?.length ?? 0) === 0 ? (
              <div className="p-6">
                <Empty icon={<GitPullRequestArrow className="h-8 w-8" />}>
                  还没有提案。跑过几次绑定了本蓝本的任务后，训练分析会产出结构化建议；也可以走 API（POST /blueprints/「id」/proposals）手工提交补丁。
                </Empty>
              </div>
            ) : (
              <ol className="divide-y divide-line">
                {proposals.data!.proposals.map((pr: BlueprintProposal) => (
                  <li key={pr.id} className="px-4 py-3">
                    <div className="flex flex-wrap items-start gap-2">
                      <span
                        className={cn(
                          "mt-0.5 inline-flex shrink-0 items-center rounded px-1.5 py-0.5 text-[10px] font-semibold",
                          pr.status === "pending" && "bg-amber-500/15 text-amber-300",
                          pr.status === "approved" && "bg-emerald-500/15 text-emerald-300",
                          pr.status === "rejected" && "bg-zinc-500/15 text-zinc-400"
                        )}
                      >
                        {pr.status === "pending" ? "待批" : pr.status === "approved" ? "已批准" : "已拒绝"}
                      </span>
                      <div className="min-w-0 flex-1">
                        <p className="text-sm font-medium text-ink">{pr.title}</p>
                        <p className="mt-0.5 line-clamp-2 text-xs text-ink-3">{pr.hypothesis || "（未写假设）"}</p>
                        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-ink-3">
                          <span>
                            <span className="font-mono tabular-nums">{pr.patches.length}</span> 个补丁 op
                          </span>
                          <span>
                            基于 <span className="font-mono">{pr.base_version_id.slice(0, 8)}</span>
                          </span>
                          {pr.expected_metric && (
                            <span>
                              预期 <span className="font-mono">{pr.expected_metric}</span>{" "}
                              {pr.expected_direction === "down" ? "↓" : "↑"}
                            </span>
                          )}
                          {pr.applied_version_id && (
                            <span>
                              已生成草稿{" "}
                              <span className="font-mono">{pr.applied_version_id.slice(0, 8)}</span>
                            </span>
                          )}
                        </p>
                      </div>
                      <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                        <Button
                          variant="outline"
                          className="gap-1 text-[11px]"
                          onClick={() => setDiffProposalId(diffProposalId === pr.id ? null : pr.id)}
                        >
                          <GitCompareArrows className="h-3 w-3" />
                          {diffProposalId === pr.id ? "收起 diff" : "看 diff"}
                        </Button>
                        {pr.status === "approved" && (
                          <Button
                            className="gap-1 text-[11px]"
                            onClick={() => setConfirmNextRoundId(pr.id)}
                          >
                            <Play className="h-3 w-3" /> 开始下一轮
                          </Button>
                        )}
                        {pr.status === "pending" && (
                          <>
                            <Button
                              variant="outline"
                              className="gap-1 text-[11px]"
                              disabled={rejectProposal.isPending}
                              onClick={() => rejectProposal.mutate(pr.id)}
                            >
                              <ThumbsDown className="h-3 w-3" /> 拒绝
                            </Button>
                            <Button
                              className="gap-1 text-[11px]"
                              onClick={() => setConfirmApproveId(pr.id)}
                            >
                              <ThumbsUp className="h-3 w-3" /> 批准
                            </Button>
                          </>
                        )}
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </SectionCard>

          {/* ── 提案 diff：三色对照（新增=emerald、删除=red、改动=amber）── */}
          {diffProposalId && (
            <SectionCard
              title="提案 diff"
              desc="左：改动前规范图 · 右：改动后规范图（补丁应用后仍过 V1–V6 校验才会出现）"
              descInBody
            >
              {proposalDiff.isLoading ? (
                <div className="p-4"><Skeleton className="h-64 w-full" /></div>
              ) : proposalDiff.isError ? (
                <div className="p-4">
                  <ErrorBox title="diff 读取失败：" message={String(proposalDiff.error)} />
                </div>
              ) : proposalDiff.data ? (
                <div className="space-y-3 p-4">
                  <div className="grid items-start gap-3 lg:grid-cols-2">
                    <DiffCanvas label="改动前" graph={proposalDiff.data.base} />
                    <DiffCanvas label="改动后" graph={proposalDiff.data.patched} />
                  </div>
                  <ProposalDiffChanges diff={proposalDiff.data} />
                </div>
              ) : null}
            </SectionCard>
          )}

          {/* ── 同题 A/B（§12.3）：两个 goal 的执行指标对比 ── */}
          <SectionCard
            title="同题 A/B"
            desc="输入两个用过本蓝本的任务 id：并排比较执行指标（墙钟/关键路径/并行加速/返工/一次通过率/人工等待）；成本指标仍诚实缺席（run 无 token 采集）"
            descInBody
          >
            <div className="space-y-3 p-4">
              <div className="flex flex-wrap items-end gap-2">
                <div className="min-w-56 flex-1">
                  <Field label="任务 A（goal id）">
                    <input className={cn(inputCls, "font-mono text-xs placeholder:font-sans")} value={abGoalA} onChange={(e) => setAbGoalA(e.target.value)} placeholder="32 位任务 id" />
                  </Field>
                </div>
                <div className="min-w-56 flex-1">
                  <Field label="任务 B（goal id）">
                    <input className={cn(inputCls, "font-mono text-xs placeholder:font-sans")} value={abGoalB} onChange={(e) => setAbGoalB(e.target.value)} placeholder="32 位任务 id" />
                  </Field>
                </div>
              </div>
              {ab.isError && (
                <ErrorBox title="对比读取失败：" message={String(ab.error)} />
              )}
              {ab.data && (
                <div className="space-y-2">
                  <div className="overflow-x-auto rounded-lg border border-line">
                    <table className="w-full min-w-[560px] table-fixed border-collapse text-xs">
                      <colgroup>
                        <col className="w-[108px]" />
                        <col />
                        <col />
                        <col className="w-[84px]" />
                      </colgroup>
                      <thead className="bg-surface-2/40">
                        <tr className="text-[11px] text-ink-3">
                          <th className="px-3 py-2 text-left font-medium">指标</th>
                          <th className="px-3 py-2 text-left font-medium" title={ab.data.goal_a.goal_id}>
                            <span className="flex min-w-0 items-baseline gap-1.5">
                              <span className="shrink-0">A</span>
                              <span className="min-w-0 truncate text-ink-2">
                                {ab.data.goal_a.goal_title || ab.data.goal_a.goal_id.slice(0, 8)}
                              </span>
                              {ab.data.goal_a.blueprint_version_id && (
                                <span className="shrink-0 whitespace-nowrap text-[10px]">
                                  钉{" "}
                                  <span className="font-mono tabular-nums">
                                    {ab.data.goal_a.blueprint_version_id.slice(0, 8)}
                                  </span>
                                </span>
                              )}
                            </span>
                          </th>
                          <th className="px-3 py-2 text-left font-medium" title={ab.data.goal_b.goal_id}>
                            <span className="flex min-w-0 items-baseline gap-1.5">
                              <span className="shrink-0">B</span>
                              <span className="min-w-0 truncate text-ink-2">
                                {ab.data.goal_b.goal_title || ab.data.goal_b.goal_id.slice(0, 8)}
                              </span>
                              {ab.data.goal_b.blueprint_version_id && (
                                <span className="shrink-0 whitespace-nowrap text-[10px]">
                                  钉{" "}
                                  <span className="font-mono tabular-nums">
                                    {ab.data.goal_b.blueprint_version_id.slice(0, 8)}
                                  </span>
                                </span>
                              )}
                            </span>
                          </th>
                          <th className="px-3 py-2 text-right font-medium" title="B 相对 A 的变化（改善/恶化按指标方向判断）">Δ</th>
                        </tr>
                      </thead>
                      <tbody>
                        {abMetricKeys(ab.data.deltas).map((k) => (
                          <ABRow
                            key={k}
                            metric={k}
                            a={ab.data!.goal_a.metrics[k]}
                            b={ab.data!.goal_b.metrics[k]}
                            delta={ab.data!.deltas[k]}
                          />
                        ))}
                      </tbody>
                    </table>
                  </div>
                  {(ab.data.notes?.length ?? 0) > 0 && (
                    <ul className="space-y-0.5 text-[11px] text-amber-300/90">
                      {ab.data!.notes!.map((n, i) => (
                        <li key={i}>· {n}</li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          </SectionCard>

          {/* ── 规范图（Phase A 验收项）：复用执行图渲染器 ── */}
          <SectionCard
            title="规范图"
            desc="计划态的结构化定义：节点是任务/条件/路由/人工门/派生/终点，边编码执行顺序与分支"
            bodyClassName="p-0"
          >
            {!selected ? (
              <div className="p-6">
                <Empty icon={<Waypoints className="h-8 w-8" />}>选择一个版本查看规范图。</Empty>
              </div>
            ) : (
              <>
                <WorkflowGraphView
                  graph={graph ?? null}
                  loading={graphLoading}
                  error={graphError ? String(graphErr) : null}
                  variant="spec"
                  showMetrics={false}
                />
              </>
            )}
          </SectionCard>

          {/* ── 原始 JSON（只读）：可拷贝形态，导出/评审用 ── */}
          {selected?.content && (
            <SectionCard
              title="规范 JSON"
              desc="该版本的结构化定义原文（只读；发布后不可变）"
              action={<CopyButton text={prettyContent} />}
            >
              <details className="group">
                <summary className="cursor-pointer select-none px-4 py-2.5 text-xs text-ink-3 transition-colors hover:text-ink-2">
                  展开查看原文
                </summary>
                <pre className="max-h-96 overflow-auto border-t border-line bg-surface-2/40 p-3 font-mono text-[11px] leading-relaxed text-ink-2">
                  {prettyContent}
                </pre>
              </details>
            </SectionCard>
          )}
        </>
      )}

      {/* ── 草稿编辑对话框 ──
          Dialog 是「挂载即渲染」组件，显隐必须靠条件挂载（仓库惯例
          {open && <Dialog/>}）——无条件挂载会让三个弹窗全开、叠层挡死整
          页，且 onClose 只改 state 不影响渲染，点了没反应（m02616 bug）。 */}
      {editOpen && (
        <Dialog
          title={`编辑草稿 v${selected?.version ?? ""}`}
          onClose={() => setEditOpen(false)}
          wide
          footer={
            <>
              <Button variant="outline" onClick={() => setEditOpen(false)}>取消</Button>
              <Button onClick={() => void saveEdit()} disabled={updateDraft.isPending} className="gap-1.5">
                {updateDraft.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                保存草稿
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <p className="text-xs text-ink-3">
              保存时服务端会重新跑 V1–V6 校验。草稿可以反复改；「发布」之后这个版本就冻结了，后续改动请开新草稿。
            </p>
            <textarea
              className={cn(inputCls, "min-h-[380px] resize-y font-mono text-[11px] leading-relaxed")}
              spellCheck={false}
              value={editContent}
              onChange={(e) => setEditContent(e.target.value)}
            />
            {mutError(updateDraft.error) && (
              <ErrorBox title="保存没过校验：" message={mutError(updateDraft.error)!} />
            )}
          </div>
        </Dialog>
      )}

      {/* ── 发布对话框 ── */}
      {publishOpen && (
        <Dialog
          title={`发布 v${selected?.version ?? ""}`}
          onClose={() => setPublishOpen(false)}
          footer={
            <>
              <Button variant="outline" onClick={() => setPublishOpen(false)}>取消</Button>
              <Button
                onClick={() => void doPublish()}
                disabled={publish.isPending}
                className="gap-1.5 border-emerald-500/40 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20"
              >
                {publish.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                发布
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <p className="text-xs text-ink-3">
              发布 = 冻结这个版本并允许绑定任务。发布后内容不可改（校验不过会留在草稿态）。
            </p>
            <Field label="变更说明" hint="可选；写清楚这次改了什么，之后看版本史一眼就懂">
              <input
                className={inputCls}
                value={publishChangelog}
                onChange={(e) => setPublishChangelog(e.target.value)}
                placeholder="例如：把验证方式从 machine 改成 agent 复核"
              />
            </Field>
            {mutError(publish.error) && (
              <ErrorBox title="发布没过校验：" message={mutError(publish.error)!} />
            )}
          </div>
        </Dialog>
      )}

      {/* ── 开新草稿对话框 ── */}
      {newDraftOpen && (
        <Dialog
          title="开新草稿"
          onClose={() => setNewDraftOpen(false)}
          footer={
            <>
              <Button variant="outline" onClick={() => setNewDraftOpen(false)}>取消</Button>
              <Button onClick={() => void doNewDraft()} disabled={newDraft.isPending} className="gap-1.5">
                {newDraft.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                创建草稿
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <p className="text-xs text-ink-3">
              会复制当前最新版本的内容开一个 v+1 草稿，原版本不动。这是迭代蓝图的正规通道：改一版 → 发布 → 绑定新任务对比效果。
            </p>
            <Field label="变更说明" hint="这版打算改什么">
              <input
                className={inputCls}
                value={newDraftChangelog}
                onChange={(e) => setNewDraftChangelog(e.target.value)}
                placeholder="例如：并行化调研和写作，缩短关键路径"
              />
            </Field>
            {mutError(newDraft.error) && (
              <ErrorBox title="创建草稿失败：" message={mutError(newDraft.error)!} />
            )}
          </div>
        </Dialog>
      )}
      {/* ── 提案 → 下一轮（RSI 闭环）── */}
      {confirmNextRoundId && (
        <Dialog
          title="开始下一轮训练？"
          onClose={() => setConfirmNextRoundId(null)}
          footer={
            <>
              <Button variant="outline" onClick={() => setConfirmNextRoundId(null)}>取消</Button>
              <Button
                variant="outline"
                disabled={startNextRound.isPending}
                onClick={() => void doNextRound(confirmNextRoundId, false)}
              >
                只建 backlog
              </Button>
              <Button
                disabled={startNextRound.isPending}
                className="gap-1.5 border-emerald-500/40 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20"
                onClick={() => void doNextRound(confirmNextRoundId, true)}
              >
                {startNextRound.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                发布并立即开始
              </Button>
            </>
          }
        >
          <div className="space-y-3 text-xs leading-relaxed text-ink-2">
            <p>
              这一步把 RSI 串成闭环：<strong className="text-ink">发布</strong>提案的应用版本（若仍是草稿）→
              复制原训练目标的域、小队/执行者、验收级别、采样率与预算 →
              <strong className="text-ink">钉住新版本</strong>创建下一轮（execution_mode=train）。
            </p>
            <p className="text-ink-3">
              「立即开始」会当场创建 active 目标并派发 owner run（产生真实执行成本）；「只建 backlog」等你之后手动启动。
              两种都会编译规范图，下一轮的执行会与原目标同口径对比。
            </p>
            {mutError(startNextRound.error) && (
              <ErrorBox title="创建下一轮失败：" message={mutError(startNextRound.error)!} />
            )}
          </div>
        </Dialog>
      )}
      {/* ── 批准提案确认（Phase C §12.1）── */}
      {confirmApproveId && (
        <Dialog
          title="批准这个提案？"
          onClose={() => setConfirmApproveId(null)}
          footer={
            <>
              <Button variant="outline" onClick={() => setConfirmApproveId(null)}>取消</Button>
              <Button
                onClick={() => void doApprove(confirmApproveId)}
                disabled={approveProposal.isPending}
                className="gap-1.5 border-emerald-500/40 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20"
              >
                {approveProposal.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                批准并开新草稿
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <p className="text-xs leading-relaxed text-ink-2">
              批准 = 把补丁应用到基准版本，重新过一遍 V1–V6 校验，然后以提案的假设为变更说明开一个
              <strong className="text-ink">新草稿</strong>。草稿不会自动发布——要不要生效、什么时候生效，仍由你点「发布」决定。
            </p>
            {mutError(approveProposal.error) && (
              <ErrorBox title="批准失败：" message={mutError(approveProposal.error)!} />
            )}
          </div>
        </Dialog>
      )}
      {confirmDeleteVersionId && (
        <ConfirmDialog
          title="删除草稿版本"
          message={`确定删除这个草稿版本吗？${
            versions.find((v) => v.id === confirmDeleteVersionId)?.version
              ? `（v${versions.find((v) => v.id === confirmDeleteVersionId)!.version}）`
              : ""
          }内容与变更说明会一并消失，不可恢复。已发布版本是蓝本的权威与历史，不会被删除；被目标钉住或被提案引用的草稿也会被服务端拒绝并说明原因。${deleteVersionError ? ` 删除失败：${deleteVersionError}` : ""}`}
          loading={delVersion.isPending}
          onConfirm={() => void confirmDeleteVersion()}
          onClose={() => {
            setConfirmDeleteVersionId(null);
            setDeleteVersionError("");
          }}
        />
      )}
      {deleteOpen && detail?.blueprint && (
        <ConfirmDialog
          title="删除蓝本"
          message={`删除蓝本「${detail.blueprint.name}」会同时删除它的 ${versions.length} 个版本与全部提案，不可恢复。仍有未结束的目标钉住它的某个版本时会被拒绝——先结束/取消那些目标。${deleteError ? ` 删除失败：${deleteError}` : ""}`}
          loading={del.isPending}
          onConfirm={() => void confirmDelete()}
          onClose={() => {
            setDeleteOpen(false);
            setDeleteError("");
          }}
        />
      )}
    </PageBody>
  );
}
