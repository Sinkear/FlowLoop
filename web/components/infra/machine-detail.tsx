"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { Bot, Boxes, Monitor, Plus, ScrollText, Terminal, Trash2 } from "lucide-react";
import { useDeleteRuntime } from "@/lib/queries";
import { Button, Empty, SectionCard } from "@/components/ui";
import { cn, relTime } from "@/lib/utils";
import { RuntimeCreateDialog } from "./runtime-create-dialog";
import { RuntimeModelsDialog } from "./runtime-models-dialog";
import { cliType, fullTime, isMachineOnline, parseCLIs, runtimeStatusMeta } from "./shared";
import type { Agent, Machine, Runtime, Squad } from "@/lib/types";

// 右列：选中机器的详情 —— 头部（状态/版本/探测到的 CLI/影响面）→ 运行时列表
// → 每个运行时绑定的 Agent。数据层级 machine → runtime → agent 就在这一列讲完。
export function MachineDetail({
  machine,
  runtimes,
  agentsByRuntime,
  squads,
}: {
  machine: Machine;
  runtimes: Runtime[];
  agentsByRuntime: Map<string, Agent[]>;
  squads: Squad[];
}) {
  const del = useDeleteRuntime();
  const [createOpen, setCreateOpen] = useState(false);
  const [modelsFor, setModelsFor] = useState<Runtime | null>(null);
  const [deleteError, setDeleteError] = useState("");

  const online = isMachineOnline(machine.status);
  const clis = parseCLIs(machine.probed_clis);

  // 影响面：机器上的运行时绑定着哪些 Agent；小队按「队长是否绑定在本机的
  // Agent 上」统计——平台没有按机器聚合小队成员的接口，逐队拉成员是 N+1，
  // 所以这里只给能一条查询算出的部分，并在 UI 上写明口径。
  const boundAgentIds = useMemo(
    () =>
      new Set(
        runtimes.flatMap((rt) => (agentsByRuntime.get(rt.id) ?? []).map((a) => a.id))
      ),
    [runtimes, agentsByRuntime]
  );
  const agentCount = useMemo(
    () => runtimes.reduce((n, rt) => n + (agentsByRuntime.get(rt.id)?.length ?? 0), 0),
    [runtimes, agentsByRuntime]
  );
  const squadCount = useMemo(
    () => squads.filter((s) => s.leader_id && boundAgentIds.has(s.leader_id)).length,
    [squads, boundAgentIds]
  );

  const deleteRuntime = (rt: Runtime) => {
    setDeleteError("");
    del.mutate(rt.id, {
      onSuccess: () => setModelsFor((cur) => (cur?.id === rt.id ? null : cur)),
      onError: (err) => setDeleteError(deleteErrorText(err)),
    });
  };

  return (
    <div className="min-w-0 space-y-4">
      <section className="overflow-hidden rounded-lg border border-line bg-surface-1">
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3.5">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={cn(
                  "h-2 w-2 shrink-0 rounded-full",
                  online ? "bg-emerald-400" : "bg-zinc-400"
                )}
                aria-hidden="true"
              />
              <h2 className="min-w-0 truncate text-lg font-semibold tracking-tight text-ink">
                {machine.name}
              </h2>
              <span
                className={cn(
                  "inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium",
                  online ? "bg-emerald-500/10 text-emerald-300" : "bg-zinc-500/10 text-ink-3"
                )}
              >
                <span
                  className={cn("h-1.5 w-1.5 rounded-full", online ? "bg-emerald-400" : "bg-zinc-400")}
                  aria-hidden="true"
                />
                {online ? "在线" : "离线"}
              </span>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-4 text-xs text-ink-3">
              <span className="truncate font-mono">{machine.hostname || "主机名未知"}</span>
              <span aria-hidden="true">·</span>
              <span>flowloop {machine.version || "版本未知"}</span>
              <span aria-hidden="true">·</span>
              <span title={fullTime(machine.last_seen_at)}>
                最后心跳 {machine.last_seen_at ? relTime(machine.last_seen_at) : "未知"}
              </span>
              <span aria-hidden="true">·</span>
              <span className="font-mono" title={machine.id}>
                ID {machine.id.slice(0, 8)}
              </span>
            </div>
          </div>

          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <Button variant="outline" onClick={() => setCreateOpen(true)} className="gap-1.5">
              <Plus className="h-4 w-4" aria-hidden="true" />
              添加运行时
            </Button>
            <Link
              href="/logs"
              className="inline-flex items-center gap-1.5 rounded-md border border-line-strong px-3 py-1.5 text-sm font-medium text-ink-2 transition-colors hover:border-accent/50 hover:bg-surface-2 hover:text-ink"
            >
              <ScrollText className="h-4 w-4" aria-hidden="true" />
              查看日志
            </Link>
          </div>
        </header>

        <div className="space-y-3 px-4 py-3">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
            <span className="text-xs text-ink-3">探测到的 CLI</span>
            {clis.length ? (
              clis.map((c) => (
                <span
                  key={c.name}
                  className="inline-flex items-center gap-1 rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[11px]"
                  title={c.skills_dir ? `技能目录：${c.skills_dir}` : undefined}
                >
                  <span className="font-mono text-ink-2">{c.name}</span>
                  {c.version && <span className="text-ink-3">{c.version}</span>}
                </span>
              ))
            ) : (
              <span className="text-xs text-ink-3">
                未探测到 agent CLI——在主机确认 CLI 已安装后重新运行 connect
              </span>
            )}
          </div>

          <div
            className={cn(
              "flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border px-3 py-2 text-sm",
              online ? "border-line bg-surface-2/40" : "border-amber-500/30 bg-amber-500/5"
            )}
          >
            <span className="text-ink-2">影响</span>
            <span className="font-mono tabular-nums text-ink">{agentCount}</span>
            <span className="text-ink-2">个 Agent</span>
            <span className="text-ink-3" aria-hidden="true">
              ·
            </span>
            <span className="font-mono tabular-nums text-ink">{squadCount}</span>
            <span className="text-ink-2">支小队</span>
            <span
              className="text-[11px] text-ink-3"
              title="平台没有「小队按机器聚合」接口；小队数按队长是否绑定在本机 Agent 上统计，普通成员的绑定未计入。"
            >
              按队长归属统计
            </span>
          </div>

          <p className="text-xs leading-relaxed text-ink-3">
            平台暂不支持远程重启机器或整机重新探测；需要重新探测某个 CLI 时，打开它的「模型」弹窗点「刷新检测」。
          </p>
        </div>
      </section>

      <SectionCard
        title="运行时"
        count={runtimes.length}
        desc="Agent 执行任务的席位"
        action={
          <Button
            variant="outline"
            onClick={() => setCreateOpen(true)}
            className="gap-1.5 !px-2.5 !py-1 text-xs"
          >
            <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            添加运行时
          </Button>
        }
        bodyClassName="p-0"
      >
        {!online && (
          <p className="border-b border-line bg-amber-500/5 px-4 py-2 text-xs leading-relaxed text-amber-300">
            机器已离线：上面所有运行时都无法领取新任务，模型目录也不能刷新；等主机重新连上后自动恢复。
          </p>
        )}

        {deleteError && (
          <p className="border-b border-line bg-red-500/5 px-4 py-2 text-xs leading-relaxed text-red-400">
            {deleteError}
          </p>
        )}

        {runtimes.length === 0 ? (
          <Empty icon={<Terminal className="h-5 w-5" />}>
            <span className="block">
              这台机器还没有运行时。机器执行 <code className="font-mono">flowloop connect</code>{" "}
              时探测到的 CLI 会自动注册；也可以手动添加一个。
            </span>
            <span className="mt-3 block">
              <Button variant="outline" onClick={() => setCreateOpen(true)} className="gap-1.5">
                <Plus className="h-4 w-4" aria-hidden="true" />
                添加运行时
              </Button>
            </span>
          </Empty>
        ) : (
          <ul className="divide-y divide-line">
            {runtimes.map((rt) => (
              <RuntimeRow
                key={rt.id}
                rt={rt}
                agents={agentsByRuntime.get(rt.id) ?? []}
                deleting={del.isPending && del.variables === rt.id}
                onOpenModels={() => setModelsFor(rt)}
                onDelete={() => deleteRuntime(rt)}
              />
            ))}
          </ul>
        )}
      </SectionCard>

      {createOpen && (
        <RuntimeCreateDialog machine={machine} onClose={() => setCreateOpen(false)} />
      )}
      {modelsFor && (
        <RuntimeModelsDialog rt={modelsFor} onClose={() => setModelsFor(null)} />
      )}
    </div>
  );
}

function RuntimeRow({
  rt,
  agents,
  deleting,
  onOpenModels,
  onDelete,
}: {
  rt: Runtime;
  agents: Agent[];
  deleting: boolean;
  onOpenModels: () => void;
  onDelete: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const st = runtimeStatusMeta(rt.status);
  const modelCount = rt.models?.length ?? 0;

  return (
    <li className="flex items-start justify-between gap-3 px-4 py-3 transition-colors hover:bg-surface-2/40">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", st.dot)} aria-hidden="true" />
          <span
            className="min-w-0 truncate text-sm font-medium text-ink"
            title={rt.args?.length ? `启动参数：${rt.args.join(" ")}` : "启动参数为空"}
          >
            {rt.name}
          </span>
          <span className={cn("shrink-0 text-[11px]", st.text)}>{st.label}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">{cliType(rt.name)}</span>
          <span className="shrink-0 text-[11px] text-ink-3" title={fullTime(rt.created_at)}>
            · 创建于 {relTime(rt.created_at)}
          </span>
        </div>

        <div className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1.5 pl-3.5">
          <button
            type="button"
            onClick={onOpenModels}
            aria-haspopup="dialog"
            aria-label={`${rt.name}：${
              modelCount > 0 ? `${modelCount} 个模型` : "未探测到模型"
            }，查看可用模型`}
            title="查看探测到的模型，并可「刷新检测」"
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-[11px] font-medium transition-colors",
              modelCount > 0
                ? "border-line-strong bg-surface-2 text-ink-2 hover:border-accent/50 hover:text-ink"
                : "border-dashed border-amber-500/40 text-amber-300 hover:border-amber-400"
            )}
          >
            <Boxes className="h-3 w-3" aria-hidden="true" />
            {modelCount > 0 ? `${modelCount} 个模型` : "未探测到模型"}
          </button>

          <span className="text-[11px] text-ink-3">
            Agent <span className="font-mono tabular-nums text-ink-2">{agents.length}</span>
          </span>

          {agents.length === 0 ? (
            <span className="text-[11px] text-amber-300">未被 Agent 绑定</span>
          ) : (
            <>
              {agents.slice(0, 3).map((a) => (
                <Link
                  key={a.id}
                  href="/agents"
                  title={`查看 Agent：${a.name}`}
                  className="inline-flex max-w-[150px] items-center gap-1 rounded border border-line px-1.5 py-0.5 text-[11px] text-ink-2 transition-colors hover:border-accent/50 hover:text-ink"
                >
                  <Bot className="h-3 w-3 shrink-0" aria-hidden="true" />
                  <span className="truncate">{a.name}</span>
                </Link>
              ))}
              {agents.length > 3 && (
                <span className="text-[11px] text-ink-3">+{agents.length - 3}</span>
              )}
            </>
          )}
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-2 text-xs">
        {confirming ? (
          <span className="flex items-center gap-2 whitespace-nowrap">
            <button
              type="button"
              className="font-medium text-red-400 transition-colors hover:text-red-300 disabled:opacity-50"
              onClick={() => {
                onDelete();
                setConfirming(false);
              }}
              disabled={deleting}
            >
              {deleting ? "删除中…" : "确认删除"}
            </button>
            <button
              type="button"
              className="text-ink-3 transition-colors hover:text-ink-2"
              onClick={() => setConfirming(false)}
              disabled={deleting}
            >
              取消
            </button>
          </span>
        ) : (
          <button
            type="button"
            className="inline-flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-red-400"
            onClick={() => setConfirming(true)}
            title="删除运行时"
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
            删除
          </button>
        )}
      </div>
    </li>
  );
}

// 后端删运行时有两类拒绝：仍被 Agent 引用（AW.10000004）与运行时已不存在。
// 前者给出下一步，而不是把英文 message 抛给用户。
function deleteErrorText(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string };
  if (e.code === "AW.10000004" || e.status === 409) {
    return "这个运行时仍被 Agent 绑定。先到「智能体」页删除或改绑这些 Agent，再回来删运行时。";
  }
  if (e.status === 404) return "这个运行时已不存在，可能刚被删除。";
  return e.message ? `删除失败：${e.message}` : "删除失败，请重试。";
}

// 没有可展示的机器时，右列也给出下一步，不留白板。
export function MachineDetailPlaceholder({ onAddMachine }: { onAddMachine: () => void }) {
  return (
    <SectionCard title="机器详情" className="min-w-0">
      <Empty icon={<Monitor className="h-5 w-5" />}>
        <span className="block">
          先在左侧选择一台机器，这里会显示它的运行时、模型目录和绑定的 Agent。
          如果列表还是空的，说明还没有主机通过 <code className="font-mono">flowloop connect</code> 接入。
        </span>
        <span className="mt-3 block">
          <Button variant="outline" onClick={onAddMachine} className="gap-1.5">
            <Plus className="h-4 w-4" aria-hidden="true" />
            添加机器
          </Button>
        </span>
      </Empty>
    </SectionCard>
  );
}