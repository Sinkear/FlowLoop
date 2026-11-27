"use client";

// 工作流详情页的 diff / A-B 展示组件。
//
// 这三个组件只服务「优化提案 → 看 diff」与「同题 A/B」两块版式，抽出来是为了
// 让 page.tsx 专注在页面编排上（原文件 1300+ 行）。语义（Δ 方向、数值格式）
// 全部复用 components/training/format.ts，不在这里另起一套。

import { ArrowDownRight, ArrowUpRight, Minus } from "lucide-react";
import {
  DASH,
  EDGE_KIND_LABEL,
  asNum,
  deltaTone,
  fmtDeltaPct,
  fmtDeltaValue,
  fmtMs,
  fmtRatio,
  nodeKindMeta,
  type DeltaTone,
} from "@/components/training/format";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { cn } from "@/lib/utils";
import type {
  DiffNode,
  StructuredGraphDiff,
  WorkflowGraph,
} from "@/lib/types";

// A/B 指标的中文名（§13.1：verify_pass_rate 已落地；cost_per_success 仍诚实缺席）。
// 键顺序＝后端 ABMetricKeys 的展示顺序，页面不再依赖 deltas map 的遍历顺序。
const AB_METRIC_LABELS: Record<string, string> = {
  critical_path_ms: "关键路径",
  wall_ms: "墙钟时间",
  parallel_speedup: "并行加速",
  rework_ratio: "返工率",
  verify_pass_rate: "一次通过率",
  verify_overhead_ratio: "验证开销",
  human_wait_ms: "人工等待",
};

// 行的顺序固定按后端指标集；deltas 里出现未知键（后端新增指标）就追加在后面，
// 既不隐藏新指标，也不让 map 的遍历顺序决定表格顺序。
const AB_METRIC_ORDER = Object.keys(AB_METRIC_LABELS);

export function abMetricKeys(deltas: Record<string, number>): string[] {
  const extra = Object.keys(deltas).filter((k) => !(k in AB_METRIC_LABELS));
  return [...AB_METRIC_ORDER, ...extra];
}

// Δ 语义色：改善/恶化按指标方向判断（deltaTone），不是按数值涨跌。
const AB_TONE_TEXT: Record<DeltaTone, string> = {
  improved: "text-emerald-300",
  worsened: "text-red-300",
  same: "text-ink-3",
};

const AB_TONE_LABEL: Record<DeltaTone, string> = {
  improved: "改善",
  worsened: "恶化",
  same: "持平",
};

/**
 * 指标值 → 与训练洞察页同款格式（ms 带单位、比例出百分比、加速出 ×）。
 * format.ts 的 fmtDeltaValue 覆盖 _ms/_ratio/speedup；_rate（一次通过率）
 * 也是 0..1 比例，但它不以 _ratio 结尾，这里补一条 fmtRatio 分支，
 * 不为此改动 format.ts。
 */
function fmtABValue(metric: string, v: number | string | undefined): string {
  if (v === undefined) return DASH;
  const n = asNum(v);
  if (n === null) return typeof v === "string" && v.trim() !== "" ? v : DASH;
  if (metric.endsWith("_rate")) return fmtRatio(n);
  return fmtDeltaValue(metric, n);
}

/**
 * Δ 绝对值（b−a）：比例类指标的差是百分点而不是百分比，
 * 写成 +5.0pp 才不会被读成「涨了 5%」。
 */
function fmtDeltaAbs(metric: string, d: number): string {
  if (!Number.isFinite(d)) return DASH;
  if (metric.endsWith("_ratio") || metric.endsWith("_rate")) {
    return `${d > 0 ? "+" : d < 0 ? "−" : ""}${Math.abs(d * 100).toFixed(1)}pp`;
  }
  // fmtMs 把负值当成「未测量」返回「—」；Δ 是正负对称的差值，先取绝对值格式化
  // 再补符号，否则 ms 指标改善（差值为负）时 Δ 绝对值只剩一个破折号。
  if (metric.endsWith("_ms")) {
    return `${d > 0 ? "+" : d < 0 ? "−" : ""}${fmtMs(Math.abs(d))}`;
  }
  return fmtABValue(metric, d);
}

// A/B 一行：指标 / A / B / Δ。数值列右对齐 + tabular-nums——任意数值长度下
// 小数点都落在同一列，Δ 独立成列而不是挤在 B 值后面。
export function ABRow({
  metric,
  a,
  b,
  delta,
}: {
  metric: string;
  a: number | string | undefined;
  b: number | string | undefined;
  delta: number | undefined;
}) {
  const an = asNum(a);
  const bn = asNum(b);
  const hasBoth = an !== null && bn !== null;
  const tone: DeltaTone = hasBoth ? deltaTone(metric, an, bn) : "same";
  // Δ 列展示相对变化率；Δ 绝对值（b−a）放 title，避免窄列塞两行数字。
  const pct = hasBoth && an !== 0 ? fmtDeltaPct((bn - an) / an) : "";
  const up = hasBoth && bn > an;
  const Icon = tone === "same" ? Minus : up ? ArrowUpRight : ArrowDownRight;
  const deltaText =
    delta !== undefined && Number.isFinite(delta)
      ? `Δ ${fmtDeltaAbs(metric, delta)}`
      : "";
  return (
    <tr className="border-b border-line/60 last:border-b-0">
      <td className="px-3 py-2 text-[12px] text-ink-3" title={metric}>
        {AB_METRIC_LABELS[metric] ?? metric}
      </td>
      <td className="px-3 py-2 text-right font-mono text-[12px] tabular-nums text-ink-2">
        {fmtABValue(metric, a)}
      </td>
      <td className="px-3 py-2 text-right font-mono text-[12px] tabular-nums text-ink-2">
        {fmtABValue(metric, b)}
      </td>
      <td
        className={cn("px-3 py-2 text-right text-[12px]", hasBoth ? AB_TONE_TEXT[tone] : "text-ink-3")}
        title={deltaText}
      >
        {hasBoth ? (
          <span className="inline-flex items-center gap-1">
            <Icon className="h-3 w-3" aria-hidden="true" />
            {pct ? <span className="font-mono tabular-nums">{pct}</span> : AB_TONE_LABEL[tone]}
            <span className="sr-only">{AB_TONE_LABEL[tone]}</span>
          </span>
        ) : (
          DASH
        )}
      </td>
    </tr>
  );
}

// ── 提案 diff 的结构化变更表 ──
// 变更不再平铺成一行 flex-wrap 的「+ 节点 x − 边 y」：按 节点/边 × 新增/删除/改动
// 分组，组头给动作与计数，行内固定「类型列 + 标识列」，类型是中文（sans），
// 标识是节点/边的 key（mono + tabular-nums）。
type DiffChangeRow = { type: string; id: string; hint?: string };
type DiffChangeGroup = {
  key: string;
  label: string;
  sign: string;
  chip: string;
  rows: DiffChangeRow[];
};

function nodeKindLabel(nodes: WorkflowGraph["nodes"], key: string): string {
  return nodeKindMeta(nodes.find((n) => n.key === key)?.kind ?? "").label;
}

// 边 key 形如「src →handle→ tgt」（见后端 edgeKey）；用它回查边类型。
function edgeKindLabel(edges: WorkflowGraph["edges"], key: string): string {
  const [source, , target] = key.split("→").map((s) => s.trim());
  const edge = edges.find((e) => e.from === source && e.to === target);
  return edge ? EDGE_KIND_LABEL[edge.kind] ?? "边" : "边";
}

export function ProposalDiffChanges({
  diff,
  emptyText = "补丁没有增删改任何节点或边——改动后的规范图与改动前一致。",
}: {
  /** 提案 diff 与版本 diff 共用的结构化变更子集（StructuredGraphDiff）。 */
  diff: StructuredGraphDiff;
  /** 没有任何增删改时的占位文案；版本对比与提案补丁的语境不同，各自传。 */
  emptyText?: string;
}) {
  const groups: DiffChangeGroup[] = [
    {
      key: "nodes_added",
      label: "节点新增",
      sign: "+",
      chip: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
      rows: diff.nodes_added.map((k) => ({ type: nodeKindLabel(diff.patched.nodes, k), id: k })),
    },
    {
      key: "nodes_removed",
      label: "节点删除",
      sign: "−",
      chip: "border-red-500/30 bg-red-500/10 text-red-300",
      rows: diff.nodes_removed.map((k) => ({ type: nodeKindLabel(diff.base.nodes, k), id: k })),
    },
    {
      key: "nodes_changed",
      label: "节点改动",
      sign: "±",
      chip: "border-amber-500/30 bg-amber-500/10 text-amber-300",
      rows: diff.nodes_changed.map((c) => {
        const meta = nodeKindMeta(c.kind);
        return { type: meta.label, id: c.id, hint: `改动：${meta.desc}` };
      }),
    },
    {
      key: "edges_added",
      label: "边新增",
      sign: "+",
      chip: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
      rows: diff.edges_added.map((k) => ({ type: edgeKindLabel(diff.patched.edges, k), id: k })),
    },
    {
      key: "edges_removed",
      label: "边删除",
      sign: "−",
      chip: "border-red-500/30 bg-red-500/10 text-red-300",
      rows: diff.edges_removed.map((k) => ({ type: edgeKindLabel(diff.base.edges, k), id: k })),
    },
  ].filter((g) => g.rows.length > 0);

  if (groups.length === 0) {
    return (
      <p className="rounded-lg border border-dashed border-line bg-surface-2/20 px-3 py-2 text-xs text-ink-3">
        {emptyText}
      </p>
    );
  }

  return (
    <div className="overflow-hidden rounded-lg border border-line">
      {groups.map((g) => (
        <section key={g.key} className="border-b border-line last:border-b-0">
          <div className="flex items-center gap-2 bg-surface-2/40 px-3 py-1.5 text-[11px]">
            <span
              className={cn(
                "inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] leading-none",
                g.chip
              )}
            >
              {g.sign}
            </span>
            <span className="font-medium text-ink-2">{g.label}</span>
            <span className="ml-auto font-mono tabular-nums text-ink-3">{g.rows.length}</span>
          </div>
          <ul className="divide-y divide-line/60">
            {g.rows.map((r) => (
              <li key={r.id} className="flex items-baseline gap-3 px-3 py-1.5">
                <span className="w-[72px] shrink-0 truncate text-[11px] text-ink-3" title={r.hint}>
                  {r.type}
                </span>
                <span
                  className="min-w-0 flex-1 truncate font-mono text-[11px] tabular-nums text-ink-2"
                  title={r.id}
                >
                  {r.id}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

// ── nodes_changed 的字段级渲染（issue #31 P1）──
// base/patched 是整节点 map（SpecNode 序列化：顶层 id/type/title + 按 kind 一
// 段嵌套 task:{assignee,brief} / condition / router / gate / spawn / end）。
// 重点字段 assignee/title/brief 都在两层以内，所以按叶子路径展开到「两侧同
// 为对象」为止，取两侧键并集逐叶比对；两侧结构不一致（一侧对象一侧标量）
// 或三层以上的值就整体 JSON 文本比，务实够用。

const NODE_FIELD_LABELS: Record<string, string> = {
  id: "ID",
  type: "类型",
  title: "标题",
  assignee: "负责人",
  brief: "简报",
  mode: "验证方式",
  verifier: "验证人",
  outcome: "结局",
  expr: "表达式",
  else: "否则分支",
  timeout_after: "超时时长",
  timeout_action: "超时动作",
  max_children: "子项上限",
};

type FieldRow = { path: string; base: unknown; patched: unknown };

/** path 形如 "title" / "task.assignee"——叶子段查中文标签，父段留作上下文。 */
function fieldLabel(path: string): string {
  const segs = path.split(".");
  const leaf = segs[segs.length - 1];
  const label = NODE_FIELD_LABELS[leaf] ?? leaf;
  return segs.length > 1 ? `${label}（${segs.slice(0, -1).join(".")}）` : label;
}

function jsonEq(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function leafRows(path: string, b: unknown, p: unknown, out: FieldRow[]) {
  const bObj = b !== null && typeof b === "object";
  const pObj = p !== null && typeof p === "object";
  if (bObj && pObj) {
    const bRec = b as Record<string, unknown>;
    const pRec = p as Record<string, unknown>;
    for (const k of new Set([...Object.keys(bRec), ...Object.keys(pRec)])) {
      leafRows(`${path}.${k}`, bRec[k], pRec[k], out);
    }
    return;
  }
  if (!jsonEq(b, p)) out.push({ path, base: b, patched: p });
}

/** 一个节点的键并集 → { 变化叶子行, 未变叶子键 }；变化行按路径排序稳定输出。 */
function nodeFieldRows(n: DiffNode): { changed: FieldRow[]; sameKeys: string[] } {
  const changed: FieldRow[] = [];
  const same = new Set<string>();
  const bRec = n.base as Record<string, unknown>;
  const pRec = n.patched as Record<string, unknown>;
  for (const k of new Set([...Object.keys(bRec), ...Object.keys(pRec)])) {
    const before = changed.length;
    leafRows(k, bRec[k], pRec[k], changed);
    if (changed.length === before) same.add(k);
  }
  changed.sort((a, b) => a.path.localeCompare(b.path));
  return { changed, sameKeys: [...same].sort() };
}

function fmtLeaf(v: unknown): string {
  if (v === undefined) return "（无）";
  if (v === null) return "（空）";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

// 长文本（brief 等）竖排两块 pre：旧值红调、新值绿调，保留换行。短值内联一行。
const LONG_TEXT = 48;

function LeafValue({ v, tone }: { v: unknown; tone: "old" | "new" }) {
  const text = fmtLeaf(v);
  const cls =
    tone === "old"
      ? "border-red-500/25 bg-red-500/[0.06] text-red-300"
      : "border-emerald-500/25 bg-emerald-500/[0.06] text-emerald-300";
  if (text.length > LONG_TEXT || text.includes("\n")) {
    return (
      <pre
        className={cn(
          "max-h-40 overflow-auto whitespace-pre-wrap break-words rounded border px-2 py-1.5 font-mono text-[11px] leading-relaxed",
          cls
        )}
      >
        {text}
      </pre>
    );
  }
  return (
    <span className={cn("rounded border px-1.5 py-px font-mono text-[11px]", cls)}>{text}</span>
  );
}

/**
 * 字段级变更表：每个 nodes_changed 节点一张小卡（节点类型 + id + 变化数），
 * 卡内逐字段「旧值 → 新值」；未变字段收成一行灰字，证明并集遍历过而非漏看。
 */
export function NodeFieldChanges({ nodes }: { nodes: DiffNode[] }) {
  if (nodes.length === 0) return null;
  return (
    <div className="space-y-2.5">
      {nodes.map((n) => {
        const meta = nodeKindMeta(n.kind);
        const { changed, sameKeys } = nodeFieldRows(n);
        return (
          <div key={n.id} className="overflow-hidden rounded-lg border border-line">
            <div className="flex flex-wrap items-center gap-2 bg-surface-2/40 px-3 py-1.5 text-[11px]">
              <span className={cn("rounded border px-1.5 py-px text-[10px]", meta.badge)}>
                {meta.label}
              </span>
              <span className="font-mono tabular-nums text-ink-2" title={n.id}>
                {n.id}
              </span>
              <span className="ml-auto font-mono tabular-nums text-ink-3">
                {changed.length} 项变化
              </span>
            </div>
            {changed.length === 0 ? (
              <p className="px-3 py-2 text-xs text-ink-3">
                没有可比对的字段级差异（两侧仅结构不同）。
              </p>
            ) : (
              <ul className="divide-y divide-line/60">
                {changed.map((r) => (
                  <li key={r.path} className="px-3 py-2">
                    <div className="text-[11px] font-medium text-ink-2">{fieldLabel(r.path)}</div>
                    <div className="mt-1.5 grid items-start gap-1.5 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
                      <LeafValue v={r.base} tone="old" />
                      <span className="pt-0.5 text-ink-3" aria-hidden>
                        →
                      </span>
                      <LeafValue v={r.patched} tone="new" />
                    </div>
                  </li>
                ))}
              </ul>
            )}
            {sameKeys.length > 0 && (
              <div className="border-t border-line/60 px-3 py-1.5 text-[10px] text-ink-3">
                未变字段：{sameKeys.map(fieldLabel).join("、")}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// 左右两栏共用同一画布尺寸：图自身的滚动视口固定 320px 高，两栏不再因为
// 节点多少一高一低，也与下方单栏「规范图」卡片同一视觉权重。
export function DiffCanvas({ label, graph }: { label: string; graph: WorkflowGraph }) {
  return (
    <figure className="overflow-hidden rounded-lg border border-line">
      <figcaption className="border-b border-line bg-surface-2/40 px-3 py-1.5 text-[11px] font-medium text-ink-2">
        {label}
      </figcaption>
      <div className="[&_.overflow-auto]:h-[320px]">
        <WorkflowGraphView
          graph={graph}
          loading={false}
          error={null}
          variant="spec"
          showMetrics={false}
        />
      </div>
    </figure>
  );
}
