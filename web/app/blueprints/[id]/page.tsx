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
import { useParams } from "next/navigation";
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
} from "lucide-react";
import { Badge, Button, Dialog, Empty, Field, PageBody, SectionCard, Skeleton, inputCls } from "@/components/ui";
import { useBlueprint, useBlueprintGraph, useNewBlueprintDraft, usePublishBlueprintVersion, useUpdateBlueprintDraft } from "@/lib/queries";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { DASH, NODE_KIND_META, fmtInt } from "@/components/training/format";
import { cn } from "@/lib/utils";
import type { WorkflowGraph } from "@/lib/types";

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
function SpecKindLegend({ graph }: { graph: WorkflowGraph | null | undefined }) {
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const n of graph?.nodes ?? []) m.set(n.kind, (m.get(n.kind) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [graph]);

  if (!graph || counts.length === 0) {
    return (
      <div className="border-b border-line px-4 py-2 font-mono text-[11px] text-ink-3">
        节点 {fmtInt(0)} · 边 {fmtInt(0)}
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-4 py-2">
      {counts.map(([kind, n]) => {
        const meta = NODE_KIND_META[kind];
        return (
          <span
            key={kind}
            className={cn(
              "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
              meta?.badge ?? "border-line bg-surface-2 text-ink-3"
            )}
            title={meta?.desc ?? "未知节点类型"}
          >
            {meta?.label ?? kind}
            <span className="font-mono tabular-nums opacity-80">×{n}</span>
          </span>
        );
      })}
      <span className="ml-auto font-mono text-[11px] tabular-nums text-ink-3">
        节点 {fmtInt((graph.nodes ?? []).length)} · 边 {fmtInt((graph.edges ?? []).length)}
      </span>
    </div>
  );
}

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

  const mutError = (e: unknown) => (e instanceof Error ? e.message : null);

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
                <SpecKindLegend graph={graph ?? null} />
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
                <pre className="max-h-96 overflow-auto border-t border-line bg-surface-2/40 p-3 font-mono text-[11.5px] leading-relaxed text-ink-2">
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
              className={cn(inputCls, "min-h-[380px] resize-y font-mono text-[11.5px] leading-relaxed")}
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
    </PageBody>
  );
}
