"use client";

import { useId, useMemo, useRef, useState } from "react";
import {
  Bot,
  CircleSlash,
  Eye,
  ForkKnife,
  GitBranch,
  GitMerge,
  ListChecks,
  Maximize2,
  MessageSquare,
  ShieldCheck,
  Split,
  Target,
  Waypoints,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { WorkflowEdge, WorkflowGraph, WorkflowNode } from "@/lib/types";
import { cn } from "@/lib/utils";
import {
  EDGE_KIND_LABEL,
  DASH,
  fmtInt,
  fmtMs,
  fmtRatio,
  metricNum,
  nodeKindMeta,
} from "./format";

// 只读 DAG 的分层布局。不引第三方图库：
// 横向按拓扑层排列，纵向同层横排；坐标是固定的 CSS 像素，连线用 SVG 画。
// 卡片尺寸（m04217 视觉统一）：比旧版高一档，为了装下「状态章 + 两行标题 +
// 元数据脚注」，和蓝本规范图的节点卡共用同一套版式。
const NODE_W = 212;
const NODE_H = 94;
const GAP_X = 66;
const GAP_Y = 18;
const COL_W = NODE_W + GAP_X;
const ROW_H = NODE_H + GAP_Y;

const KIND_ICON: Record<string, LucideIcon> = {
  goal: Target,
  agent: Bot,
  verify: ShieldCheck,
  review: Eye,
  consult: MessageSquare,
  integrate: GitMerge,
  // 蓝本规范图节点（Phase A）；未知 kind 已有 fallback（Waypoints）。
  task: ListChecks,
  condition: Split,
  router: ForkKnife,
  gate: CircleSlash,
  spawn: GitBranch,
  end: Target,
};

interface LaidOut {
  node: WorkflowNode;
  layer: number;
  top: number;
  x: number;
}

interface Layout {
  width: number;
  height: number;
  nodes: LaidOut[];
  byKey: Map<string, LaidOut>;
  maxLayer: number;
  maxRow: number;
}

function sortNodes(a: WorkflowNode, b: WorkflowNode): number {
  const ta = Number.isFinite(a.started_at_ms) ? a.started_at_ms : 0;
  const tb = Number.isFinite(b.started_at_ms) ? b.started_at_ms : 0;
  if (ta !== tb) return ta - tb;
  return a.key.localeCompare(b.key);
}

/**
 * 拓扑分层：所有边都参与（不只是 depends_on/seq），create 边把 agent 放到
 * goal 下一层，verified_by/consult/integrated 把 verify 等节点放到其来源之后。
 * 层号取最长路径（Kahn），这样依赖链不会“抄近道”排到同一层。
 * 图里若有时序环（理论上不应有），剩余节点追加到最深层，页面不退化成空白。
 */
function computeLayout(graph: WorkflowGraph): Layout {
  const nodes = (graph.nodes ?? []).filter((n) => n && typeof n.key === "string");
  const byKeyRaw = new Map(nodes.map((n) => [n.key, n]));
  const edges = (graph.edges ?? []).filter(
    (e) => byKeyRaw.has(e.from) && byKeyRaw.has(e.to) && e.from !== e.to
  );

  const indeg = new Map<string, number>();
  const out = new Map<string, string[]>();
  for (const n of nodes) {
    indeg.set(n.key, 0);
    out.set(n.key, []);
  }
  for (const e of edges) {
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
    out.get(e.from)?.push(e.to);
  }

  const layer = new Map<string, number>();
  const queue: string[] = [];
  // 根节点必须显式落 0 层（m04217 修）：旧版只 push 不 set，于是根节点的
  // layer 一直是 undefined，走到下面的「环保护」分支被当成未处理节点塞到
  // 最深层——goal 永远被画在最右边，图读起来是「执行 → 执行 → 目标」。
  for (const n of nodes) {
    if ((indeg.get(n.key) ?? 0) === 0) {
      layer.set(n.key, 0);
      queue.push(n.key);
    }
  }
  // 队列按开始时间取，保证同层输出稳定。
  queue.sort((a, b) => sortNodes(byKeyRaw.get(a)!, byKeyRaw.get(b)!));

  let head = 0;
  while (head < queue.length) {
    const key = queue[head++];
    const base = layer.get(key) ?? 0;
    for (const to of out.get(key) ?? []) {
      layer.set(to, Math.max(layer.get(to) ?? 0, base + 1));
      indeg.set(to, (indeg.get(to) ?? 1) - 1);
      if ((indeg.get(to) ?? 0) === 0) queue.push(to);
    }
  }

  // 环保护：没被拓扑排序覆盖的节点接到当前最深层后面。
  let maxLayer = 0;
  for (const v of layer.values()) maxLayer = Math.max(maxLayer, v);
  for (const n of nodes) {
    if (!layer.has(n.key)) {
      layer.set(n.key, maxLayer + 1);
      maxLayer += 1;
    }
  }

  // review 节点在图上没有结构边（后端只建 agent/verify/consult/integrate 的边）。
  // 纯按拓扑会把它们扔到和 goal 同一列，看起来像“审查先于执行”。把它们放到
  // 当前最右列：不改后端图，只修正展示层对孤立节点的理解。
  const linked = new Set<string>();
  for (const e of edges) {
    linked.add(e.from);
    linked.add(e.to);
  }
  const lastLayer = Math.max(1, maxLayer);
  for (const n of nodes) {
    if (!linked.has(n.key) && n.kind !== "goal") layer.set(n.key, lastLayer);
  }
  // 深度按「实际落到第几列」重算：单节点图（只有 goal）不该因为
  // lastLayer 的下限 1 而显示成深度 2。
  maxLayer = 0;
  for (const v of layer.values()) maxLayer = Math.max(maxLayer, v);

  const groups = new Map<number, WorkflowNode[]>();
  for (const n of nodes) {
    const l = layer.get(n.key) ?? 0;
    if (!groups.has(l)) groups.set(l, []);
    groups.get(l)!.push(n);
  }
  for (const arr of groups.values()) arr.sort(sortNodes);

  const maxRow = Math.max(1, ...Array.from(groups.values(), (a) => a.length));
  const height = maxRow * ROW_H - GAP_Y;
  const width = (maxLayer + 1) * COL_W - GAP_X;

  const laidOut: LaidOut[] = [];
  for (const [l, arr] of groups) {
    const offset = ((maxRow - arr.length) * ROW_H) / 2;
    arr.forEach((node, i) => {
      laidOut.push({
        node,
        layer: l,
        top: offset + i * ROW_H,
        x: l * COL_W,
      });
    });
  }

  return {
    width,
    height,
    nodes: laidOut,
    byKey: new Map(laidOut.map((n) => [n.node.key, n])),
    maxLayer,
    maxRow,
  };
}

// 状态语义（m04217）：旧版只有一个 1.5px 小圆点，颜色还漏掉了后端真实存在的
// verified / running / completed / cancelled 四种状态——它们全落进了灰色兜底，
// 「已验证」和「待执行」在图上是同一个灰点。这里把状态升级成「左侧色条 + 中文
// 状态章」的组合，并补齐词表（取自 ui.tsx 的 STATUS_LABEL，平台内一套说法）。
interface StatusMeta {
  label: string;
  /** 左侧色条 */
  bar: string;
  /** 状态章底色 + 文字 */
  chip: string;
  /** 章内圆点 */
  dot: string;
}

const STATUS_META: Record<string, StatusMeta> = {
  done: { label: "已完成", bar: "bg-emerald-400", chip: "bg-emerald-500/10 text-emerald-300", dot: "bg-emerald-400" },
  verified: { label: "已验证", bar: "bg-emerald-400", chip: "bg-emerald-500/10 text-emerald-300", dot: "bg-emerald-400" },
  completed: { label: "已完成", bar: "bg-emerald-400", chip: "bg-emerald-500/10 text-emerald-300", dot: "bg-emerald-400" },
  approved: { label: "已通过", bar: "bg-emerald-400", chip: "bg-emerald-500/10 text-emerald-300", dot: "bg-emerald-400" },
  ready: { label: "就绪", bar: "bg-emerald-400", chip: "bg-emerald-500/10 text-emerald-300", dot: "bg-emerald-400" },
  running: { label: "执行中", bar: "bg-blue-400", chip: "bg-blue-500/10 text-blue-300", dot: "bg-blue-400" },
  active: { label: "进行中", bar: "bg-blue-400", chip: "bg-blue-500/10 text-blue-300", dot: "bg-blue-400" },
  review: { label: "待审批", bar: "bg-purple-400", chip: "bg-purple-500/10 text-purple-300", dot: "bg-purple-400" },
  reviewing: { label: "审查中", bar: "bg-purple-400", chip: "bg-purple-500/10 text-purple-300", dot: "bg-purple-400" },
  failed: { label: "失败", bar: "bg-red-400", chip: "bg-red-500/10 text-red-300", dot: "bg-red-400" },
  error: { label: "出错", bar: "bg-red-400", chip: "bg-red-500/10 text-red-300", dot: "bg-red-400" },
  rejected: { label: "已拒绝", bar: "bg-red-400", chip: "bg-red-500/10 text-red-300", dot: "bg-red-400" },
  blocked: { label: "阻塞", bar: "bg-amber-400", chip: "bg-amber-500/10 text-amber-300", dot: "bg-amber-400" },
  pending: { label: "待执行", bar: "bg-ink-3/50", chip: "bg-surface-2 text-ink-3", dot: "bg-ink-3" },
  queued: { label: "排队中", bar: "bg-ink-3/50", chip: "bg-surface-2 text-ink-3", dot: "bg-ink-3" },
  cancelled: { label: "已取消", bar: "bg-zinc-400", chip: "bg-zinc-500/10 text-ink-3", dot: "bg-zinc-400" },
  // 蓝本规范图节点：非执行态，用中性青区分「这是规范，不是执行记录」。
  spec: { label: "计划", bar: "bg-teal-400", chip: "bg-teal-500/10 text-teal-300", dot: "bg-teal-400" },
};

function nodeStatusMeta(status: string): StatusMeta {
  if (STATUS_META[status]) return STATUS_META[status];
  return {
    label: status || "未开始",
    bar: "bg-ink-3/40",
    chip: "bg-surface-2 text-ink-3",
    dot: "bg-ink-3",
  };
}

function edgePath(from: LaidOut, to: LaidOut): string {
  const sx = from.x + NODE_W;
  const sy = from.top + NODE_H / 2;
  const tx = to.x;
  const ty = to.top + NODE_H / 2;
  const dx = Math.max(24, (tx - sx) / 2);
  return `M ${sx} ${sy} C ${sx + dx} ${sy}, ${tx - dx} ${ty}, ${tx} ${ty}`;
}

function NodeCard({
  laid,
  critical,
  criticalPath,
}: {
  laid: LaidOut;
  critical: boolean;
  criticalPath: boolean;
}) {
  const { node } = laid;
  const meta = nodeKindMeta(node.kind);
  const st = nodeStatusMeta(node.status);
  const Icon = KIND_ICON[node.kind] ?? Waypoints;
  const isSpecNode = node.status === "spec";
  const duration = fmtMs(Number.isFinite(node.duration_ms) ? node.duration_ms : null);
  const tooltip = [
    `${node.label || node.key}（${meta.label} · ${st.label}）`,
    node.status ? `状态：${node.status}` : "",
    isSpecNode
      ? "计划态节点：任务激活后才会执行"
      : `耗时：${duration}`,
    node.assignee_id ? `执行者：${node.assignee_id}` : "",
    node.attempt > 0 ? `尝试：${node.attempt}` : "",
    node.reworks > 0 ? `返工：${node.reworks}` : "",
    Number.isFinite(node.critical_share) && node.critical_share > 0
      ? `关键路径占比：${fmtRatio(node.critical_share, 0)}`
      : "",
    node.key,
  ]
    .filter(Boolean)
    .join("\n");

  // 版式与蓝本规范图节点卡统一（m04217）：左侧状态色条 → 类型章 + 状态章 →
  // 两行标题（不再 truncate 成一行，长标题在图上可读）→ 脚注元数据。
  return (
    <div
      className={cn(
        "group absolute overflow-hidden rounded-lg border bg-surface-1 shadow-sm transition-shadow duration-150 hover:z-10 hover:shadow-md",
        meta.ring,
        critical && "border-accent/60 bg-accent/[0.05] shadow-accent/10",
        !critical && criticalPath && "border-accent/40"
      )}
      style={{ left: laid.x, top: laid.top, width: NODE_W, height: NODE_H }}
      title={tooltip}
    >
      <span className={cn("absolute inset-y-0 left-0 w-[3px]", st.bar)} aria-hidden="true" />
      <div className="flex h-full flex-col px-2.5 py-2 pl-[13px]">
        <div className="flex items-center gap-1.5">
          <span
            className={cn(
              "inline-flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
              meta.badge
            )}
          >
            <Icon className="h-3 w-3" />
            {meta.label}
          </span>
          <span
            className={cn(
              "ml-auto inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[9.5px] font-medium",
              st.chip
            )}
          >
            <span className={cn("h-1.5 w-1.5 rounded-full", st.dot)} />
            {st.label}
          </span>
        </div>
        <div
          className="mt-1 line-clamp-2 text-[11.5px] font-medium leading-[1.35] text-ink"
          title={node.label}
        >
          {node.label || node.key}
        </div>
        <div className="mt-auto flex items-center gap-2 overflow-hidden text-[10px] text-ink-3">
          {!isSpecNode && duration !== DASH && (
            <span className="shrink-0 font-mono tabular-nums">{duration}</span>
          )}
          {node.attempt > 1 && <span className="shrink-0">尝试 {node.attempt}</span>}
          {node.reworks > 0 && (
            <span className="shrink-0 font-medium text-amber-300">返工 {node.reworks}</span>
          )}
          {isSpecNode && (
            <span className="truncate font-mono" title={node.key}>
              {node.key}
            </span>
          )}
          {/* 执行者 id 是哈希，画在卡上只是噪声（tooltip 里有）——脚注留给
              耗时/尝试/返工这些能读懂的数。 */}
          {critical && (
            <span className="ml-auto shrink-0 font-mono text-accent">
              关键 {fmtRatio(node.critical_share, 0)}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

function EdgeLine({
  edge,
  from,
  to,
  critical,
  markerBase,
  markerCritical,
  markerSpec,
}: {
  edge: WorkflowEdge;
  from: LaidOut;
  to: LaidOut;
  critical: boolean;
  markerBase: string;
  markerCritical: string;
  markerSpec: string;
}) {
  const inferredSeq = edge.kind === "seq" && edge.source === "inferred";
  const isSpec = edge.kind === "spec";
  // 颜色走主题令牌（m04217）：旧版写死 #71717a / #22c55e，浅色主题下灰线在
  // 纸面上偏脏、绿线和 accent 也不同源。现在依赖/创建/规范三类边各有强度。
  const stroke = critical
    ? "var(--color-accent)"
    : isSpec
      ? "var(--color-teal-400)"
      : "var(--color-ink-3)";
  const strokeOpacity = critical ? 0.95 : isSpec ? 0.65 : edge.kind === "created" ? 0.32 : 0.5;
  const strokeWidth = critical ? 2.1 : isSpec ? 1.4 : 1.1;
  return (
    <path
      d={edgePath(from, to)}
      fill="none"
      stroke={stroke}
      strokeWidth={strokeWidth}
      strokeOpacity={strokeOpacity}
      strokeLinecap="round"
      strokeDasharray={inferredSeq ? "4 4" : edge.source === "inferred" ? "2 3" : undefined}
      markerEnd={`url(#${critical ? markerCritical : isSpec ? markerSpec : markerBase})`}
    />
  );
}

// 编排指标行（m04217）：和蓝本列表的 StatStrip 一个版式——小号大写标签 +
// 等宽大数字，值才是主角。旧版 14px 数值和标签几乎同权重，八格读起来像表格。
// tone 只在本省得的指标上标警（返工率 > 0、分派失衡 > 1.3×），其余保持中性。
export function WorkflowMetricsRow({
  metrics,
}: {
  metrics: Record<string, unknown> | null | undefined;
}) {
  const m = metrics ?? {};
  const speedup = metricNum(m, "parallel_speedup");
  const rework = metricNum(m, "rework_ratio");
  const imbalance = metricNum(m, "lane_imbalance");
  const unrun = metricNum(m, "unrun_sub_goals") ?? 0;
  const capped = m["queue_wait_capped"] === true;
  const overlap = metricNum(m, "queue_wait_overlap_ms");
  const queueHint =
    "派发延迟：开始执行 − 就绪时间（累计口径，按编排跨度封顶）" +
    (unrun > 0 ? `（${unrun} 个未开始执行的子目标不计入）` : "") +
    (capped ? `（并行重叠已收敛到编排跨度，超出 ${fmtMs(overlap)} 记入 queue_wait_overlap_ms）` : "");
  const items: { label: string; value: string; tone?: "accent" | "warn"; hint?: string }[] = [
    {
      label: "并行加速",
      value: fmtSpeedupSafe(speedup),
      tone: "accent",
      hint: "串行耗时才并行加速之比",
    },
    { label: "关键路径", value: fmtMs(metricNum(m, "critical_path_ms")), hint: "决定墙钟的那条链" },
    { label: "串行损失", value: fmtMs(metricNum(m, "serialization_loss_ms")), hint: "本可并行却被串起来的等待" },
    { label: "验证开销", value: fmtRatio(metricNum(m, "verify_overhead_ratio")), hint: "验证占执行时间比例" },
    {
      label: "返工率",
      value: fmtRatio(rework),
      tone: rework !== null && rework > 0 ? "warn" : undefined,
      hint: "返工次数 / 任务数",
    },
    { label: "咨询次数", value: fmtInt(metricNum(m, "consult_count")), hint: "小队内跨 agent 咨询" },
    { label: "排队等待", value: fmtMs(metricNum(m, "queue_wait_ms")), hint: queueHint },
    {
      label: "分派失衡",
      value: fmtLaneImbalance(imbalance),
      tone: imbalance !== null && imbalance > 1.3 ? "warn" : undefined,
      hint: "最忙 lane / 平均 lane",
    },
    {
      label: "依赖门控",
      value: fmtMs(metricNum(m, "dependency_wait_ms")),
      hint: "等待前置依赖完成（累计口径，可超过墙钟；基线与口径修正见 #13）",
    },
  ];

  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {items.map((it) => (
        <div
          key={it.label}
          className="rounded-md border border-line bg-surface-1 px-2.5 py-2"
          title={it.hint}
        >
          <div className="text-[10px] font-medium tracking-[0.06em] text-ink-3">
            {it.label}
          </div>
          <div
            className={cn(
              "mt-0.5 font-mono text-[17px] leading-tight tabular-nums",
              it.tone === "accent"
                ? "text-accent"
                : it.tone === "warn"
                  ? "text-amber-300"
                  : "text-ink-2"
            )}
          >
            {it.value}
          </div>
        </div>
      ))}
    </div>
  );
}

function fmtSpeedupSafe(v: number | null): string {
  if (v === null) return DASH;
  return `${v.toFixed(2)}×`;
}

function fmtLaneImbalance(v: number | null): string {
  if (v === null) return DASH;
  // lane_imbalance = 最忙 lane / 平均 lane，>=1 才有意义；不是 0..1 比例。
  return `${v.toFixed(2)}×`;
}

// 节点类型图例（m04217）：按 kind 计数渲染彩色章（类型章与节点卡同一套
// NODE_KIND_META.badge），右侧是结构计数。蓝本规范图与执行编排图共用这一份，
// 免得两个页面各写一份、颜色和文案慢慢漂开。
export function WorkflowKindLegend({
  graph,
  suffix,
}: {
  graph: WorkflowGraph | null | undefined;
  /** 追加在「节点 N · 边 M」后面的结构量，如「深度 3」。 */
  suffix?: string;
}) {
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const n of graph?.nodes ?? []) m.set(n.kind, (m.get(n.kind) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [graph]);
  const nodes = graph?.nodes?.length ?? 0;
  const edges = graph?.edges?.length ?? 0;

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line bg-surface-2/30 px-4 py-2">
      {counts.map(([kind, n]) => {
        const meta = nodeKindMeta(kind);
        return (
          <span
            key={kind}
            className={cn(
              "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
              meta.badge
            )}
            title={meta.desc}
          >
            {meta.label}
            <span className="font-mono tabular-nums opacity-80">×{n}</span>
          </span>
        );
      })}
      <span className="ml-auto font-mono text-[11px] tabular-nums text-ink-3">
        节点 {fmtInt(nodes)} · 边 {fmtInt(edges)}
        {suffix ? ` · ${suffix}` : ""}
      </span>
    </div>
  );
}

export function WorkflowGraphView({
  graph,
  loading,
  error,
  metrics,
  analyzing = false,
  heightClass = "min-h-[260px]",
  showMetrics = true,
  variant = "execution",
}: {
  graph: WorkflowGraph | null | undefined;
  loading?: boolean;
  error?: string | null;
  /** 图不可用时（分析失败/无图）仍可展示报告里的 metrics。 */
  metrics?: Record<string, unknown> | null;
  /** 分析进行中：此时图是实时图，分析完成后才冻结，文案不能写“已冻结”。 */
  analyzing?: boolean;
  heightClass?: string;
  showMetrics?: boolean;
  /**
   * spec = 蓝本规范图（计划态，未执行）：隐藏执行期概念——冻结横幅、
   * 深度、生成时间、节点耗时、执行指标行；图例只列图上真实出现的边类型。
   * 同一渲染器两种“味”（提案 §4.12），不是两套组件。
   */
  variant?: "execution" | "spec";
}) {
  const spec = variant === "spec";
  const uid = useId().replace(/[:]/g, "");
  const markerBase = `wf-arrow-${uid}`;
  const markerCritical = `wf-arrow-c-${uid}`;
  const markerSpec = `wf-arrow-s-${uid}`;
  // 30 节点链宽度可达 7.5k px，直接铺开会把整页撑爆：限制容器高度并允许双向滚动，
  // 再提供缩放/适应宽度，默认 1×。
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [zoom, setZoom] = useState(1);
  const MIN_ZOOM = 0.15;
  const MAX_ZOOM = 2;

  const layout = useMemo(() => (graph && (graph.nodes?.length ?? 0) > 0 ? computeLayout(graph) : null), [graph]);
  const graphMetrics = graph?.metrics ?? metrics ?? null;
  const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
  const zoomBy = (delta: number) => setZoom((z) => clampZoom(Number((z + delta).toFixed(2))));
  const fitWidth = () => {
    const el = scrollRef.current;
    if (!el || !layout?.width) return;
    // 留出左右 padding（px-4 = 16px × 2）和一点滚动条余量。
    setZoom(clampZoom(Number(((el.clientWidth - 40) / layout.width).toFixed(2))));
  };

  if (loading) {
    return (
      <div className={cn("space-y-3 p-4", heightClass)}>
        <div className="flex gap-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-[82px] flex-1 animate-pulse rounded-lg bg-surface-2" />
          ))}
        </div>
        <div className="h-16 animate-pulse rounded-md bg-surface-2/60" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-3 p-4">
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
          {spec ? "规范图读取失败" : "编排图读取失败"}：{error}
        </p>
        {showMetrics && <WorkflowMetricsRow metrics={graphMetrics} />}
      </div>
    );
  }

  if (!layout) {
    return (
      <div className="space-y-3 p-4">
        <div
          className={cn(
            "flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-line px-4 py-10 text-center",
            heightClass
          )}
        >
          <Waypoints className="h-5 w-5 text-ink-3" />
          <p className="text-sm text-ink-2">还没有可展示的编排图</p>
          <p className="max-w-md text-xs leading-relaxed text-ink-3">
            训练会话进入分析、任务进入终态并完成分析后，这里会展示冻结的编排 DAG；
            分析失败或任务尚未拆解时为空。
          </p>
        </div>
        {showMetrics && <WorkflowMetricsRow metrics={graphMetrics} />}
      </div>
    );
  }

  const cpSet = new Set(graph?.critical_path ?? []);
  const nodesByKey = layout.byKey;
  // 图例只列图上真实出现的边类型：规范图只有 spec 边，执行图也免受七类
  // 无关键目的干扰（执行图早期常只有 created/depends_on 两三种边）。
  const legendKinds = new Set<string>((graph?.edges ?? []).map((e) => e.kind));
  return (
    <div className="space-y-0">
      {/* 工具条（m04217）：第一行只说「这是什么图 + 缩放」，节点类型图例
          单独一行带计数——和蓝本规范图同一套层级，读者先认图再认结构。 */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-4 py-2 text-[11px] text-ink-3">
        <span className="inline-flex items-center gap-1 text-ink-2">
          <Waypoints className="h-3.5 w-3.5" />
          {spec
            ? "计划图 · 未执行"
            : analyzing
              ? graph?.frozen
                ? "上次分析快照（分析中）"
                : "实时编排图（分析中）"
              : graph?.frozen
                ? "已冻结编排图"
                : "实时编排图"}
        </span>
        {spec && (
          <span className="text-ink-3">
            任务激活时按此图编译成带依赖的子任务，这里只是计划
          </span>
        )}
        {analyzing && !spec && (
          <span className="text-ink-3">
            {graph?.frozen
              ? "重跑中，完成后更新为本次快照"
              : "分析完成后才会冻结为只读快照"}
          </span>
        )}
        {!spec && graph?.generated_at && <span>生成于 {graph.generated_at}</span>}
        {cpSet.size > 0 && (
          <span className="inline-flex items-center gap-1 text-accent">
            <span className="h-1.5 w-3.5 rounded-sm bg-accent" />
            关键路径
          </span>
        )}
        <div className="ml-auto inline-flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => zoomBy(-0.25)}
            disabled={zoom <= MIN_ZOOM}
            title="缩小"
            aria-label="缩小编排图"
            className="inline-flex h-6 w-6 items-center justify-center rounded border border-line text-ink-3 transition-colors hover:text-ink-2 disabled:opacity-40"
          >
            <ZoomOut className="h-3.5 w-3.5" />
          </button>
          <span className="w-10 text-center font-mono tabular-nums text-ink-2">
            {Math.round(zoom * 100)}%
          </span>
          <button
            type="button"
            onClick={() => zoomBy(0.25)}
            disabled={zoom >= MAX_ZOOM}
            title="放大"
            aria-label="放大编排图"
            className="inline-flex h-6 w-6 items-center justify-center rounded border border-line text-ink-3 transition-colors hover:text-ink-2 disabled:opacity-40"
          >
            <ZoomIn className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={fitWidth}
            title="缩放到容器宽度"
            className="inline-flex h-6 items-center gap-1 rounded border border-line px-1.5 text-ink-3 transition-colors hover:text-ink-2"
          >
            <Maximize2 className="h-3 w-3" />
            适应宽度
          </button>
          <button
            type="button"
            onClick={() => setZoom(1)}
            title="恢复 1:1"
            className="h-6 rounded border border-line px-1.5 text-ink-3 transition-colors hover:text-ink-2"
          >
            1:1
          </button>
        </div>
      </div>

      <WorkflowKindLegend
        graph={graph}
        suffix={spec ? undefined : `深度 ${fmtInt(layout.maxLayer + 1)}`}
      />

      <div
        ref={scrollRef}
        className="max-h-[72vh] max-w-full overflow-auto px-4 py-3"
        style={{
          // 细点阵底（图的样子，不是空白画布）：深浅主题都用 --color-line，
          // 因此不需要两套样式。
          backgroundImage: "radial-gradient(var(--color-line) 1px, transparent 1px)",
          backgroundSize: "16px 16px",
        }}
      >
        <div
          className="relative"
          style={{ width: layout.width * zoom, height: layout.height * zoom }}
        >
          <div
            className="relative"
            style={{
              width: layout.width,
              height: layout.height,
              transform: `scale(${zoom})`,
              transformOrigin: "top left",
            }}
          >
          <svg
            width={layout.width}
            height={layout.height}
            className="pointer-events-none absolute inset-0"
            aria-hidden="true"
          >
            <defs>
              <marker
                id={markerBase}
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M 0 0 L 8 4 L 0 8 z" style={{ fill: "var(--color-ink-3)" }} />
              </marker>
              <marker
                id={markerCritical}
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M 0 0 L 8 4 L 0 8 z" style={{ fill: "var(--color-accent)" }} />
              </marker>
              <marker
                id={markerSpec}
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M 0 0 L 8 4 L 0 8 z" style={{ fill: "var(--color-teal-400)" }} />
              </marker>
            </defs>
            {(graph?.edges ?? []).map((e, i) => {
              const from = nodesByKey.get(e.from);
              const to = nodesByKey.get(e.to);
              if (!from || !to) return null;
              const critical = criticalEdge(e, from, to, cpSet);
              return (
                <EdgeLine
                  key={`${e.from}-${e.to}-${e.kind}-${i}`}
                  edge={e}
                  from={from}
                  to={to}
                  critical={critical}
                  markerBase={markerBase}
                  markerCritical={markerCritical}
                  markerSpec={markerSpec}
                />
              );
            })}
          </svg>

          {layout.nodes.map((laid) => {
            const share = Number.isFinite(laid.node.critical_share)
              ? laid.node.critical_share
              : 0;
            return (
              <NodeCard
                key={laid.node.key}
                laid={laid}
                critical={cpSet.has(laid.node.key) || share > 0.5}
                criticalPath={cpSet.has(laid.node.key)}
              />
            );
          })}
          </div>
        </div>
      </div>

      {cpSet.size > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-t border-line px-4 py-2 text-[10px] text-ink-3">
          <span className="shrink-0">关键路径：</span>
          {graph!.critical_path.map((key, i) => {
            const n = nodesByKey.get(key)?.node;
            return (
              <span key={`${key}-${i}`} className="inline-flex items-center gap-1.5">
                {i > 0 && <span className="text-ink-3">→</span>}
                <span className="rounded border border-accent/25 bg-accent/[0.06] px-1.5 py-0.5 text-accent">
                  {n?.label || key}
                </span>
              </span>
            );
          })}
        </div>
      )}

      {showMetrics && (
        <div className="border-t border-line px-4 py-3">
          <WorkflowMetricsRow metrics={graphMetrics} />
        </div>
      )}

      {legendKinds.size > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line px-4 py-2 text-[10px] text-ink-3">
          {Object.entries(EDGE_KIND_LABEL)
            .filter(([kind]) => legendKinds.has(kind))
            .map(([kind, label]) => (
              <span key={kind}>
                <span className="font-mono">{kind}</span> {label}
              </span>
            ))}
        </div>
      )}
    </div>
  );
}

function criticalEdge(
  edge: WorkflowEdge,
  from: LaidOut,
  to: LaidOut,
  cpSet: Set<string>
): boolean {
  if (cpSet.has(edge.from) && cpSet.has(edge.to)) return true;
  const fa = Number.isFinite(from.node.critical_share) ? from.node.critical_share : 0;
  const ta = Number.isFinite(to.node.critical_share) ? to.node.critical_share : 0;
  return fa > 0 && ta > 0;
}