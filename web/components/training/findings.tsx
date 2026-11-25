"use client";

import Link from "next/link";
import { useState } from "react";
import { ArrowDown, Check, EyeOff, GitBranch, Wand2 } from "lucide-react";
import { Button, Empty, SectionCard } from "@/components/ui";
import { useDraftProposalFromFinding, useUpdateRsiFinding } from "@/lib/queries";
import type { BlueprintProposal, RsiDetail, RsiFinding } from "@/lib/types";
import { cn } from "@/lib/utils";
import { categoryLabel, confidenceMeta, severityMeta } from "./format";

function scoreText(score: unknown): string {
  if (typeof score !== "number" || !Number.isFinite(score)) return "—";
  return score.toFixed(2);
}

// 提案状态：后端 blueprint_proposal.status 是 pending|approved|rejected，
// 未知值原样显示（不崩、不误判成"待审"）。
function proposalStatusMeta(status: string): { label: string; cls: string } {
  switch (status) {
    case "pending":
      return { label: "待审", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" };
    case "approved":
      return { label: "已批准", cls: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" };
    case "rejected":
      return { label: "已拒绝", cls: "border-line bg-surface-2 text-ink-3" };
    default:
      return { label: status || "未知", cls: "border-line bg-surface-2 text-ink-3" };
  }
}

function proposalsLink(blueprintId: string) {
  return `/workflows/${blueprintId}#wf-proposals`;
}

// 发现与提案并排（§4.9）：确认发现时右侧能直接看到“这条发现有没有变成提案”。
// 数据来源是 /rsi/sessions/{id} 的 proposals（该复盘所属工作流上的提案）——
// 没有工作流的复盘（自动拆解）没有提案，这里如实说明，不渲染假的空列表。
function ProposalPanel({ detail }: { detail: RsiDetail }) {
  const proposals = detail.proposals ?? [];
  const blueprintId = detail.session.blueprint_id;
  const findingIds = new Set((detail.findings ?? []).map((f) => f.id));

  return (
    <aside className="space-y-2.5 self-start rounded-lg border border-line bg-surface-1 p-4 lg:sticky lg:top-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium text-ink">提案</h3>
        {blueprintId && proposals.length > 0 && (
          <span className="font-mono text-[11px] tabular-nums text-ink-3">{proposals.length}</span>
        )}
      </div>

      {!blueprintId ? (
        <>
          <p className="text-xs leading-relaxed text-ink-3">
            本次任务没有固定工作流（自动拆解），发现不会转成提案。
          </p>
          <p className="text-[11px] leading-relaxed text-ink-3">
            要让这类发现变成可审的改动，先把执行固化成工作流，再用它跑任务。
          </p>
        </>
      ) : proposals.length === 0 ? (
        <>
          <p className="text-xs leading-relaxed text-ink-3">
            还没有提案。点发现右侧的「生成提案」，把一条发现变成待审的工作流改动。
          </p>
          <p className="text-[11px] leading-relaxed text-ink-3">
            提案不会自动生效：批准后才会生成新草稿版本，发布仍要人确认。
          </p>
        </>
      ) : (
        <ul className="space-y-2">
          {proposals.map((p) => (
            <ProposalRow key={p.id} proposal={p} fromThisReview={findingIds.has(p.finding_id)} />
          ))}
        </ul>
      )}

      {blueprintId && (
        <Link
          href={proposalsLink(blueprintId)}
          className="mt-1 inline-flex items-center gap-1 text-[11px] text-ink-3 hover:text-ink"
        >
          <GitBranch className="h-3 w-3" />
          查看该工作流的提案
        </Link>
      )}
    </aside>
  );
}

function ProposalRow({
  proposal,
  fromThisReview,
}: {
  proposal: BlueprintProposal;
  fromThisReview: boolean;
}) {
  const st = proposalStatusMeta(proposal.status);
  return (
    <li className="rounded-md border border-line bg-surface-2/40 px-2.5 py-2">
      <div className="flex items-start gap-1.5">
        <Link
          href={proposalsLink(proposal.blueprint_id)}
          className="min-w-0 flex-1 text-xs leading-snug text-ink-2 hover:text-ink hover:underline"
        >
          {proposal.title || "未命名提案"}
        </Link>
        <span className={cn("shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium", st.cls)}>
          {st.label}
        </span>
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-x-2 text-[10px] text-ink-3">
        <span>{proposal.created_by_id === "rsi-translator" ? "分析生成" : "人工"}</span>
        {proposal.finding_id && !fromThisReview && <span>· 其他复盘</span>}
        {fromThisReview && <span>· 本次复盘</span>}
      </div>
    </li>
  );
}

// 左列发现、右列提案；窄屏下落到发现之下，避免提案把发现挤成一列窄条。
function FindingsLayout({ detail, children }: { detail: RsiDetail; children: React.ReactNode }) {
  return (
    <div className="grid items-start gap-3 lg:grid-cols-[minmax(0,1fr)_280px]">
      <div className="min-w-0 space-y-3">{children}</div>
      <ProposalPanel detail={detail} />
    </div>
  );
}

// 处理过的 finding 保留在列表里（不回退、不隐藏），只是把操作换成状态字：
// 用户能回看自己确认/忽略过什么，比“点完就消失”更可解释。
function FindingStatusChip({ status }: { status: RsiFinding["status"] }) {
  if (status === "confirmed") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[11px] text-emerald-300">
        <Check className="h-3 w-3" /> 已确认
      </span>
    );
  }
  if (status === "dismissed") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md border border-line bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-3">
        <EyeOff className="h-3 w-3" /> 已忽略
      </span>
    );
  }
  return null;
}

// 空态/不可用态按状态给文案：分析中、失败、跳过的原因不同，
// 不能统一说成“没有命中结构规则”（那会被误读成“分析过了但没发现问题”）。
function emptyCopy(status: string, hasReport: boolean): string {
  switch (status) {
    case "waiting":
      return "会话还在等待任务进入终态；分析开始后这里会列出命中的结构/分派/信息流瓶颈。";
    case "analyzing":
      return "正在分析当前编排图；完成后这里会列出命中的结构/分派/信息流瓶颈。分析完成前不展示旧报告里的发现。";
    case "failed":
      return "分析失败，没有产出可展示的发现。错误原因见页面顶部；修复后可点「重跑分析」重试。";
    case "skipped":
      return "本次会话被跳过（未开启复盘或采样未命中），没有执行结构分析，因此没有发现。";
    case "ready":
      return hasReport
        ? "本次分析没有命中结构规则：要么编排没有明显异常，要么样本太少不足以判定。"
        : "会话标记已分析，但报告为空，暂时没有发现可展示。";
    default:
      return "当前没有可展示的发现。";
  }
}

// 单条发现的提案动作：已有提案 → 直接给入口；没有提案 → 「生成提案」。
// 失败原因来自后端（无工作流 / 未编译 / 节点已被占用 / 校验拒绝），原样
// 显示 —— 不做乐观成功，也不把失败说成“已生成”。
function FindingProposalAction({
  finding,
  blueprintId,
  onError,
}: {
  finding: RsiFinding;
  blueprintId: string;
  onError: (msg: string) => void;
}) {
  const draft = useDraftProposalFromFinding();
  const pending = draft.isPending && draft.variables === finding.id;

  if (finding.proposal_id) {
    return (
      <Link
        href={proposalsLink(blueprintId)}
        className="inline-flex items-center gap-1 text-[11px] text-ink-3 hover:text-ink"
      >
        <GitBranch className="h-3 w-3" /> 已生成提案
      </Link>
    );
  }
  return (
    <Button
      variant="outline"
      className="h-7 px-2 text-xs hover:border-accent/50 hover:text-accent"
      disabled={pending}
      title="只对已钉版本且已编译的工作流有效"
      onClick={() => {
        onError("");
        draft.mutate(finding.id, {
          onError: (e) => onError(e instanceof Error ? e.message : String(e)),
        });
      }}
    >
      <Wand2 className="mr-1 h-3.5 w-3.5" /> {pending ? "生成中…" : "生成提案"}
    </Button>
  );
}

export function TrainingFindings({ detail }: { detail: RsiDetail }) {
  const update = useUpdateRsiFinding();
  const [err, setErr] = useState("");

  // score 降序（undefined/NaN 沉底），同分按 id 稳定排序，避免每次渲染抖动。
  const findings = [...(detail.findings ?? [])].sort((a, b) => {
    const sa = typeof a.score === "number" && Number.isFinite(a.score) ? a.score : -Infinity;
    const sb = typeof b.score === "number" && Number.isFinite(b.score) ? b.score : -Infinity;
    if (sb !== sa) return sb - sa;
    return a.id.localeCompare(b.id);
  });

  const status = detail.session.status;
  const hasReport = status === "ready" && detail.report != null;

  // 只有 ready 才有“当前报告”：analyzing 的旧报告、failed 的半成品都不展示。
  if (!hasReport) {
    return (
      <FindingsLayout detail={detail}>
        <SectionCard title="发现">
          <Empty icon={<EyeOff className="h-5 w-5" />}>{emptyCopy(status, hasReport)}</Empty>
        </SectionCard>
      </FindingsLayout>
    );
  }

  const act = (f: RsiFinding, status: "confirmed" | "dismissed") => {
    setErr("");
    update.mutate(
      { id: f.id, status },
      { onError: (e) => setErr(e instanceof Error ? e.message : String(e)) }
    );
  };

  if (findings.length === 0) {
    return (
      <FindingsLayout detail={detail}>
        <SectionCard title="发现">
          <Empty icon={<EyeOff className="h-5 w-5" />}>{emptyCopy(status, hasReport)}</Empty>
        </SectionCard>
      </FindingsLayout>
    );
  }

  return (
    <FindingsLayout detail={detail}>
      <div className="space-y-3">
        <div className="flex items-center gap-2 text-[11px] text-ink-3">
          <ArrowDown className="h-3.5 w-3.5" />
          共 {findings.length} 条发现，按 score 降序（高分 = 更值得先看）
        </div>

        {err && (
          <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            操作失败：{err}
          </p>
        )}

        {findings.map((f) => {
          const sev = severityMeta(f.severity);
          const conf = confidenceMeta(f.confidence);
          const pending = update.isPending && update.variables?.id === f.id;
          // 后端 evidence_json 是 []string：目前是 "metric=value" 测量串，
          // 也允许以后塞 span/run id；原样展示，不做二次解释。
          const evidence = Array.isArray(f.evidence)
            ? f.evidence.filter((x): x is string => typeof x === "string" && x.length > 0)
            : [];
          return (
            <article
              key={f.id}
              className={cn(
                "overflow-hidden rounded-lg border border-line bg-surface-1",
                f.status === "dismissed" && "opacity-60"
              )}
            >
              <header className="flex items-start gap-3 px-4 py-3">
                <span
                  className={cn(
                    "mt-0.5 shrink-0 rounded border px-1.5 py-0.5 text-[11px] font-medium",
                    sev.cls
                  )}
                >
                  {sev.label}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <h3 className="text-sm font-medium text-ink">{f.title || "未命名发现"}</h3>
                    <FindingStatusChip status={f.status} />
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                    <span>{categoryLabel(f.category)}</span>
                    <span className="inline-flex items-center gap-1">
                      置信度
                      <span
                        className="rounded border border-line bg-surface-2 px-1 py-0.5 font-mono text-[10px] text-ink-2"
                        title={conf.desc}
                      >
                        {conf.label}
                      </span>
                    </span>
                    <span title={evidence.join("\n")}>证据 {evidence.length} 条</span>
                    <span className="font-mono tabular-nums">score {scoreText(f.score)}</span>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  {f.status === "open" ? (
                    <>
                      <Button
                        variant="outline"
                        className="h-7 px-2 text-xs hover:border-emerald-500/40 hover:text-emerald-300"
                        disabled={pending}
                        onClick={() => act(f, "confirmed")}
                      >
                        <Check className="mr-1 h-3.5 w-3.5" /> 确认
                      </Button>
                      <Button
                        variant="ghost"
                        className="h-7 px-2 text-xs hover:text-red-300"
                        disabled={pending}
                        onClick={() => act(f, "dismissed")}
                      >
                        <EyeOff className="mr-1 h-3.5 w-3.5" /> 忽略
                      </Button>
                    </>
                  ) : null}
                </div>
              </header>

              <div className="space-y-2.5 border-t border-line px-4 py-3">
                <div>
                  <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                    判据
                  </div>
                  <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-ink-2">
                    {f.explanation || "—"}
                  </p>
                  {evidence.length > 0 && (
                    <p className="mt-1.5 truncate font-mono text-[10px] text-ink-3" title={evidence.join("\n")}>
                      证据：{evidence.slice(0, 4).join(" · ")}
                      {evidence.length > 4 ? ` · +${evidence.length - 4}` : ""}
                    </p>
                  )}
                </div>
                {f.suggestion && (
                  <div className="rounded-md border-l-2 border-accent/50 bg-surface-2/40 px-3 py-2">
                    <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                      建议
                    </div>
                    <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-ink-2">
                      {f.suggestion}
                    </p>
                  </div>
                )}
                {detail.session.blueprint_id && f.status !== "dismissed" && (
                  <div className="flex justify-end">
                    <FindingProposalAction
                      finding={f}
                      blueprintId={detail.session.blueprint_id}
                      onError={setErr}
                    />
                  </div>
                )}
              </div>
            </article>
          );
        })}
      </div>
    </FindingsLayout>
  );
}