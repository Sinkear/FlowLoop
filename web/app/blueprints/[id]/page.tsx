"use client";

// 蓝本详情（Phase A + UX 闭环）：版本史 + 规范图 + 版本操作。
// 规范图与执行图共用 WorkflowGraph 形状（提案 §4.12）——同一渲染器，
// 「计划长什么样」和「实际怎么跑」是同一种图。
// UX 闭环（m01274）：草稿可在线编辑/发布，published 可一键带去新建任务，
// 全程不用 curl。

import { useMemo, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { Waypoints, Loader2, Rocket, PenLine, GitBranchPlus, ArrowRight } from "lucide-react";
import { Badge, Button, Dialog, Empty, Field, PageBody, SectionCard, Skeleton, inputCls } from "@/components/ui";
import { useBlueprint, useBlueprintGraph, useNewBlueprintDraft, usePublishBlueprintVersion, useUpdateBlueprintDraft } from "@/lib/queries";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { DASH, fmtInt } from "@/components/training/format";
import { cn } from "@/lib/utils";

function fullTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString("zh-CN");
}

// 规范图指标行是执行图指标的子集（无 critical_path/speedup），单独渲染
// 四格：节点/边/任务/条件——不硬塞进执行图的 WorkflowMetricsRow。
function SpecMetrics({ metrics }: { metrics: Record<string, unknown> | null | undefined }) {
  const m = metrics ?? {};
  const items = [
    { label: "节点", value: fmtInt(typeof m.node_count === "number" ? m.node_count : null) },
    { label: "边", value: fmtInt(typeof m.edge_count === "number" ? m.edge_count : null) },
    { label: "任务节点", value: fmtInt(typeof m.task_count === "number" ? m.task_count : null) },
    { label: "条件节点", value: fmtInt(typeof m.condition_count === "number" ? m.condition_count : null) },
  ];
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {items.map((it) => (
        <div key={it.label} className="rounded-md border border-line bg-surface-2/30 px-2.5 py-2">
          <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">{it.label}</div>
          <div className="mt-1 font-mono text-sm tabular-nums text-ink-2">{it.value}</div>
        </div>
      ))}
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

export default function BlueprintDetailPage() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
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

  return (
    <PageBody>
      {/* Breadcrumb + 标题（与 goal 详情同构的手写版式） */}
      <div>
        <Link
          href="/blueprints"
          className="text-sm text-ink-3 hover:text-ink-2 hover:underline"
        >
          ← 返回列表
        </Link>
        <div className="mt-3 flex items-center gap-3">
          <h1 className="text-lg font-semibold text-ink">
            {detail?.blueprint?.name ?? "蓝本"}
          </h1>
          {selected && <Badge status={selected.status} />}
        </div>
        {detail?.blueprint?.description && (
          <p className="mt-1 whitespace-pre-wrap text-sm text-ink-3">
            {detail.blueprint.description}
          </p>
        )}
      </div>
      {isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
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
          {/* 状态引导（m01573「不明所以」）：当前版本处于什么状态、下一步该点哪。 */}
          {selected && (
            <div
              className={cn(
                "rounded-lg border px-4 py-3 text-xs leading-relaxed",
                selected.status === "published"
                  ? "border-emerald-500/30 bg-emerald-500/[0.07] text-emerald-200/90"
                  : "border-amber-500/30 bg-amber-500/[0.07] text-amber-200/90"
              )}
            >
              {selected.status === "published" ? (
                <p>
                  <span className="font-medium">v{selected.version} 已发布 —— 可以绑定任务了。</span>{" "}
                  点下方「用此版本新建任务」；任务激活时，平台按下面这张规范图把任务编译成带依赖的子任务：
                  执行顺序由代码保证，上一步没验证通过，下一步不会开始。任务详情页还能对照「计划图 vs 实际执行图」。
                </p>
              ) : (
                <p>
                  <span className="font-medium">当前选中的 v{selected.version} 是草稿 —— 还不能绑定任务。</span>{" "}
                  在下方「版本」卡片里点「编辑内容」继续改，或点「发布此版本」冻结它。
                  发布后内容不可改；后续迭代走「开新草稿」（复制最新版开 v+1，是迭代蓝本的正规通道）。
                </p>
              )}
            </div>
          )}

          {/* 版本史：版本是不可变单元，选中即查看该版本的规范图。 */}
          <SectionCard title="版本" count={versions.length}>
            {versions.length === 0 ? (
              <Empty>该蓝本还没有版本。</Empty>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {versions.map((v) => {
                  const active = v.id === effectiveVersionId;
                  return (
                    <button
                      key={v.id}
                      onClick={() => setSelectedVersionId(v.id)}
                      className={
                        "flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs transition-colors " +
                        (active
                          ? "border-accent/50 bg-accent/10 text-accent"
                          : "border-line text-ink-2 hover:bg-surface-2")
                      }
                      title={
                        (v.changelog ? v.changelog + " · " : "") +
                        (v.published_at ? "发布于 " + fullTime(v.published_at) : "未发布")
                      }
                    >
                      <span className="font-mono">v{v.version}</span>
                      <Badge status={v.status} />
                    </button>
                  );
                })}
              </div>
            )}
            {selected && (
              <div className="mt-3 space-y-1 border-t border-line pt-3 text-xs text-ink-3">
                {selected.changelog && (
                  <div>
                    <span className="text-ink-2">变更说明：</span>
                    {selected.changelog}
                  </div>
                )}
                <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px]">
                  <span>id: {selected.id}</span>
                  <span>content_hash: {selected.content_hash.slice(0, 16)}</span>
                  <span>创建于 {fullTime(selected.created_at)}</span>
                  {selected.published_at && <span>发布于 {fullTime(selected.published_at)}</span>}
                </div>
                {/* 对当前选中版本的操作：草稿可改可发布；已发布可带走建任务。 */}
                <div className="flex flex-wrap items-center gap-2 pt-2">
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

          {/* 规范图只读渲染（Phase A 验收项）：复用执行图渲染器。 */}
          <SectionCard
            title="规范图"
            desc="计划态的结构化定义：节点是任务/条件/路由/人工门/派生/终点，边编码执行顺序与分支——goal 激活时按此图编译成带依赖门控的子任务"
            bodyClassName="p-0"
          >
            {!selected ? (
              <div className="p-6">
                <Empty icon={<Waypoints className="h-8 w-8" />}>选择一个版本查看规范图。</Empty>
              </div>
            ) : (
              <>
                <div className="p-4 pb-0">
                  <SpecMetrics metrics={graph?.metrics} />
                </div>
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

          {/* 原始 JSON（只读）：结构化数据的可拷贝形态——导出/评审用。 */}
          {selected?.content && (
            <SectionCard title="规范 JSON" desc="该版本的结构化定义原文（只读；发布后不可变）">
              <details>
                <summary className="cursor-pointer select-none text-xs text-ink-3 hover:text-ink-2">
                  展开查看 / 复制 JSON
                </summary>
                <pre className="mt-2 max-h-96 overflow-auto rounded-md border border-line bg-surface-2/40 p-3 font-mono text-[11px] leading-relaxed text-ink-2">
                  {prettyContent}
                </pre>
              </details>
            </SectionCard>
          )}
        </>
      )}

      {/* ── 草稿编辑对话框 ── */}
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

      {/* ── 发布对话框 ── */}
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

      {/* ── 开新草稿对话框 ── */}
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
    </PageBody>
  );
}
