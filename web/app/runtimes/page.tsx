"use client";

import { useMemo, useState } from "react";
import { RefreshCw, Terminal } from "lucide-react";
import {
  useRuntimes,
  useDeleteRuntime,
  useMachines,
  useGoalEvents,
  useAgents,
  useRuntimeModels,
  useRefreshRuntimeModels,
} from "@/lib/queries";
import {
  Button,
  Dialog,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
  StatStrip,
  Toolbar,
  inputCls,
} from "@/components/ui";
import { relTime } from "@/lib/utils";
import type { Agent, Runtime, RuntimeModel } from "@/lib/types";

// 行网格：左侧主信息 + 中间元信息列 + 右侧操作；窄屏退化为标签-值行。
// 模型列只放一个紧凑的只读入口（「N 个模型」），不再需要容纳长模型串。
const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 md:grid-cols-[minmax(0,1fr)_88px_104px_128px_52px_60px] md:items-center md:gap-3";

const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_88px_104px_128px_52px_60px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

// 运行时有三种读时状态：active=CLI 探测在；inactive=机器心跳丢失（存储仍是
// active，List 读时 join 出来的）；absent=机器最新探测里已经没有这个 CLI。
const STATUS_META: Record<string, { label: string; dot: string; text: string }> = {
  active: { label: "在线", dot: "bg-emerald-400", text: "text-emerald-300" },
  inactive: { label: "机器离线", dot: "bg-amber-400", text: "text-amber-300" },
  absent: { label: "CLI 已移除", dot: "bg-red-400", text: "text-red-300" },
};

function statusMeta(status: string) {
  return STATUS_META[status] ?? { label: status, dot: "bg-zinc-400", text: "text-ink-3" };
}

// 探测注册的运行时名叫 "<cli>@<机器名>"，前缀即 CLI 类型（claude / codex…）。
function cliType(name: string): string {
  const at = name.indexOf("@");
  return at > 0 ? name.slice(0, at) : name;
}

// 模型目录弹窗：只读展示运行时探测上报的模型。优先取 GET
// /runtimes/{id}/models，请求期间用列表行里已有的 models 垫底；
// 顶部「刷新检测」可让机器立刻重新探测一次（POST .../models/refresh）。
function RuntimeModelsDialog({ rt, onClose }: { rt: Runtime; onClose: () => void }) {
  const { data, isLoading, isError } = useRuntimeModels(rt.id);
  const refresh = useRefreshRuntimeModels();
  const [query, setQuery] = useState("");
  // 手动刷新的返回值就是本次探测结果；useRefreshRuntimeModels 也会把它写
  // 回 useRuntimeModels 缓存，这里优先用 mutation data 兜住缓存回填前的一帧。
  const fresh = refresh.data?.runtime_id === rt.id ? refresh.data : undefined;
  const models: RuntimeModel[] = fresh?.models ?? data?.models ?? rt.models ?? [];
  const detectedAt = fresh?.detected_at ?? data?.detected_at ?? rt.models_at ?? "";
  const loading = isLoading && !data;
  const refreshing = refresh.isPending;
  // 409/404 是后端契约里的已知错误：直接给出可执行的中文说明，而不是把
  // 「machine offline」之类的原始 message 抛给用户。
  const refreshError = refresh.error
    ? (() => {
        const err = refresh.error as { status?: number; message?: string };
        if (err.status === 409) return "机器当前离线，无法刷新检测结果；等机器重新连上后再试。";
        if (err.status === 404) return "该运行时已不存在，可能刚被删除。";
        return err.message ? `刷新失败：${err.message}` : "刷新失败，请稍后重试。";
      })()
    : null;
  const q = query.trim().toLowerCase();
  const visible = q
    ? models.filter(
        (m) => m.id.toLowerCase().includes(q) || (m.name ?? "").toLowerCase().includes(q)
      )
    : models;

  return (
    <Dialog
      title={`${rt.name} · 可用模型`}
      onClose={onClose}
      footer={
        <Button variant="outline" onClick={onClose}>
          关闭
        </Button>
      }
    >
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 text-xs text-ink-3">
          {loading && !detectedAt ? (
            <Skeleton className="h-3 w-28" />
          ) : (
            <span>
              {detectedAt ? (
                <span title={new Date(detectedAt).toLocaleString()}>
                  最近探测：{relTime(detectedAt)}
                </span>
              ) : models.length > 0 ? (
                // 有目录但没有探测时间戳（列表行 fallback 的老数据）——别误称未探测。
                "探测时间未知"
              ) : (
                "尚未探测到模型"
              )}
              {models.length > 0 && (
                <span className="ml-2 font-mono tabular-nums">共 {models.length} 个</span>
              )}
            </span>
          )}
        </div>
        <Button
          variant="outline"
          onClick={() => refresh.mutate(rt.id)}
          disabled={refreshing}
          className="shrink-0 gap-1.5 !px-2.5 !py-1 text-xs"
          title="让机器立即重新探测一次该 CLI 的模型目录"
        >
          <RefreshCw
            className={`h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`}
            aria-hidden="true"
          />
          {refreshing ? "刷新中…" : "刷新检测"}
        </Button>
      </div>

      {refreshError && <p className="text-xs text-amber-300">{refreshError}</p>}

      {isError && (
        <p className="text-xs text-amber-300">
          探测结果读取失败{models.length ? "，以下为列表中的最近一次上报" : ""}。
        </p>
      )}

      {models.length > 8 && (
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className={`${inputCls} !py-1.5 text-xs`}
          placeholder="搜索模型名称 / ID…"
          aria-label="搜索模型"
        />
      )}

      {loading && models.length === 0 ? (
        <div className="space-y-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="rounded-lg border border-line px-3 py-2.5">
              <Skeleton className="h-3.5 w-40" />
              <Skeleton className="mt-1.5 h-3 w-56" />
            </div>
          ))}
        </div>
      ) : models.length === 0 ? (
        <div className="rounded-lg border border-dashed border-line px-4 py-8 text-center">
          <p className="text-sm text-ink-2">暂无模型目录</p>
          <p className="mx-auto mt-1 max-w-sm text-xs leading-relaxed text-ink-3">
            机器每次执行 <code className="font-mono">ouroflow connect</code>
            ，或在探测上报更新时，会自动重新探测；也可以点上方「刷新检测」立即重新探测。
          </p>
        </div>
      ) : visible.length === 0 ? (
        <p className="px-1 py-2 text-xs text-ink-3">没有匹配的模型</p>
      ) : (
        <ul className="max-h-[45vh] space-y-1 overflow-y-auto">
          {visible.map((m) => (
            <li key={m.id} className="rounded-lg border border-line bg-surface-2/40 px-3 py-2">
              <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                <span
                  className={`min-w-0 truncate text-sm text-ink ${
                    m.name ? "font-medium" : "font-mono"
                  }`}
                >
                  {m.name || m.id}
                </span>
                {m.name && m.name !== m.id && (
                  <span className="font-mono text-[11px] text-ink-3">{m.id}</span>
                )}
              </div>
              {m.description && (
                <p className="mt-0.5 text-xs leading-relaxed text-ink-3">{m.description}</p>
              )}
            </li>
          ))}
        </ul>
      )}

      <p className="text-[11px] leading-relaxed text-ink-3">
        模型由运行时探测上报，可点「刷新检测」手动重新探测；配置 Agent 时从这里选择
      </p>
    </Dialog>
  );
}

function RuntimeRow({
  rt,
  machineName,
  agents,
  onDelete,
  onOpenModels,
}: {
  rt: Runtime;
  machineName: string;
  agents: Agent[];
  onDelete: (id: string) => void;
  onOpenModels: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const st = statusMeta(rt.status);
  const modelCount = rt.models?.length ?? 0;
  const created = new Date(rt.created_at).toLocaleString();

  return (
    <li className={`${ROW_GRID} transition-colors hover:bg-surface-2/40`}>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${st.dot}`} />
          <span className="truncate text-sm font-medium text-ink">{rt.name}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">{rt.id.slice(0, 8)}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-3.5 text-[11px] text-ink-3">
          <span className={st.text}>{st.label}</span>
          <span>·</span>
          <span title={created}>创建于 {relTime(rt.created_at)}</span>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">类型</span>
        <span className="font-mono text-ink-2">{cliType(rt.name)}</span>
      </div>

      {/* 模型列只读：完整目录收在弹窗里，行内只给数量入口（不做任何选择）。
          定宽 grid item 里用 inline-flex 的紧凑 chip，窄屏仍退化为标签-值行。 */}
      <div className="flex min-w-0 items-center justify-between gap-2 text-xs md:block md:w-full">
        <span className="shrink-0 text-[11px] text-ink-3 md:hidden">模型</span>
        <button
          type="button"
          onClick={onOpenModels}
          aria-haspopup="dialog"
          aria-label={`${rt.name}：${modelCount > 0 ? `${modelCount} 个模型` : "未探测"}，查看可用模型`}
          title={modelCount > 0 ? "查看探测到的模型" : "查看探测说明"}
          className={
            modelCount > 0
              ? "inline-flex items-center rounded-md border border-line-strong bg-surface-2 px-2 py-0.5 text-[11px] font-medium text-ink-2 transition-colors hover:border-accent/50 hover:text-ink"
              : "inline-flex items-center rounded-md border border-dashed border-line-strong px-2 py-0.5 text-[11px] font-medium text-ink-3 transition-colors hover:border-accent/50 hover:text-ink-2"
          }
        >
          {modelCount > 0 ? `${modelCount} 个模型` : "未探测"}
        </button>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">机器</span>
        <span className="truncate text-ink-2">{machineName || "—"}</span>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block md:text-right">
        <span className="text-[11px] text-ink-3 md:hidden">Agent</span>
        <span className="font-mono tabular-nums text-ink">{agents.length}</span>
      </div>

      <div className="flex items-center justify-end text-xs">
        {confirming ? (
          <span className="space-x-2 whitespace-nowrap">
            <button
              type="button"
              className="font-medium text-red-400 transition-colors hover:text-red-300"
              onClick={() => {
                onDelete(rt.id);
                setConfirming(false);
              }}
            >
              确认删除
            </button>
            <button
              type="button"
              className="text-ink-3 transition-colors hover:text-ink-2"
              onClick={() => setConfirming(false)}
            >
              取消
            </button>
          </span>
        ) : (
          <button
            type="button"
            className="text-xs text-ink-3 transition-colors hover:text-red-400"
            onClick={() => setConfirming(true)}
          >
            删除
          </button>
        )}
      </div>
    </li>
  );
}

export default function RuntimesPage() {
  useGoalEvents();
  const { data: runtimes, isLoading } = useRuntimes();
  const { data: machines } = useMachines();
  const { data: agents } = useAgents();
  const del = useDeleteRuntime();
  const [filter, setFilter] = useState<"all" | "active" | "attention">("all");
  // 当前打开模型目录弹窗的运行时；null = 关闭。
  const [modelsFor, setModelsFor] = useState<Runtime | null>(null);

  const machineName = (id: string) => machines?.find((m) => m.id === id)?.name ?? id;

  // runtime_id → 该运行时下的 agent（Agent 列的数量从这里来；模型目录走探测接口）。
  const agentsByRuntime = useMemo(() => {
    const map = new Map<string, Agent[]>();
    for (const a of agents ?? []) {
      const list = map.get(a.runtime_id) ?? [];
      list.push(a);
      map.set(a.runtime_id, list);
    }
    return map;
  }, [agents]);

  const activeCount = (runtimes ?? []).filter((r) => r.status === "active").length;
  const absentCount = (runtimes ?? []).filter((r) => r.status === "absent").length;

  // 平台没有「默认运行时」字段；agent 各自绑定 runtime_id。这里取关联 agent
  // 最多的那个当「主力」，在 hint 里说清是按绑定数推定的，不伪造设置项。
  const mainRuntime = useMemo(() => {
    let best: { rt: Runtime; count: number } | null = null;
    for (const rt of runtimes ?? []) {
      const count = agentsByRuntime.get(rt.id)?.length ?? 0;
      if (count > 0 && (!best || count > best.count)) best = { rt, count };
    }
    return best;
  }, [runtimes, agentsByRuntime]);

  const usedRuntimeCount = (runtimes ?? []).filter(
    (r) => (agentsByRuntime.get(r.id)?.length ?? 0) > 0
  ).length;

  const visible = (runtimes ?? []).filter((r) =>
    filter === "all" ? true : filter === "active" ? r.status === "active" : r.status !== "active"
  );

  return (
    <PageBody>
      <PageHeader
        title="运行时"
        desc="远端机器运行 ouroflow connect 时探测到的 agent CLI 会自动注册成运行时（CLI@机器名）；Agent 与 Run 都绑定到具体运行时。"
      />

      <StatStrip
        items={[
          {
            label: "运行时",
            value: isLoading ? "—" : (runtimes?.length ?? 0),
            hint: "探测自动注册",
          },
          {
            label: "在线",
            value: isLoading ? "—" : activeCount,
            tone: activeCount ? "ok" : "muted",
            hint: absentCount ? `${absentCount} 个 CLI 已移除` : "全部可用",
          },
          {
            label: "关联 Agent",
            value: isLoading ? "—" : (agents?.length ?? 0),
            hint: `覆盖 ${usedRuntimeCount} 个运行时`,
          },
          {
            label: "主力运行时",
            value: mainRuntime ? (
              <span className="block truncate text-sm" title={mainRuntime.rt.name}>
                {mainRuntime.rt.name}
              </span>
            ) : (
              <span className="text-sm text-ink-3">—</span>
            ),
            hint: mainRuntime ? `${mainRuntime.count} 个 agent 绑定` : "暂无 agent 绑定",
            tone: "default",
          },
        ]}
      />

      {del.isError && <p className="text-sm text-red-400">{String(del.error)}</p>}

      <SectionCard
        title="运行时"
        count={isLoading ? undefined : visible.length}
        desc={filter === "all" ? undefined : filter === "active" ? "仅在线" : "仅需处理"}
        action={
          <Toolbar>
            <Segmented
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: "全部" },
                { value: "active", label: "在线" },
                { value: "attention", label: "需处理" },
              ]}
            />
          </Toolbar>
        }
        bodyClassName="p-0"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-4 px-4 py-4">
                <Skeleton className="h-4 w-1/3" />
                <Skeleton className="h-4 w-16" />
                <Skeleton className="ml-auto h-4 w-24" />
              </div>
            ))}
          </div>
        ) : !runtimes?.length ? (
          <Empty icon={<Terminal className="h-5 w-5" />}>
            还没有运行时——在远端机器上运行 <code className="font-mono">ouroflow connect</code>
            ，探测到的 agent CLI 会自动注册到这里。
          </Empty>
        ) : !visible.length ? (
          <Empty>当前筛选下没有运行时。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>运行时</span>
              <span>类型</span>
              <span>模型</span>
              <span>机器</span>
              <span className="text-right">Agent</span>
              <span className="text-right">操作</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((rt) => (
                <RuntimeRow
                  key={rt.id}
                  rt={rt}
                  machineName={machineName(rt.machine_id)}
                  agents={agentsByRuntime.get(rt.id) ?? []}
                  onDelete={(id) => del.mutate(id)}
                  onOpenModels={() => setModelsFor(rt)}
                />
              ))}
            </ul>
          </>
        )}
      </SectionCard>

      {modelsFor && (
        <RuntimeModelsDialog rt={modelsFor} onClose={() => setModelsFor(null)} />
      )}
    </PageBody>
  );
}