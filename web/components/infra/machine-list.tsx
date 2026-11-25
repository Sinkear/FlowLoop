"use client";

import { Monitor, Plus } from "lucide-react";
import { Button, Empty, SectionCard, Skeleton } from "@/components/ui";
import { cn, relTime } from "@/lib/utils";
import { fullTime, isMachineOnline } from "./shared";
import type { Machine } from "@/lib/types";

// 左列：机器。每行讲清「哪台机器、在不在线、多久没心跳、上面有几个席位」，
// 点一行在右侧看详情——不跳页，也不做成和运行时平级的 tab。
export function MachineList({
  machines,
  runtimeCountByMachine,
  agentCountByMachine,
  selectedId,
  onSelect,
  onAddMachine,
  isLoading,
}: {
  machines: Machine[];
  runtimeCountByMachine: Map<string, number>;
  agentCountByMachine: Map<string, number>;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onAddMachine: () => void;
  isLoading: boolean;
}) {
  return (
    <SectionCard
      title="机器"
      count={isLoading ? undefined : machines.length}
      bodyClassName="p-0"
      className="min-w-0"
    >
      {isLoading ? (
        <div className="divide-y divide-line">
          {[0, 1, 2].map((i) => (
            <div key={i} className="space-y-1.5 px-3.5 py-3">
              <Skeleton className="h-3.5 w-2/3" />
              <Skeleton className="h-3 w-1/2" />
              <Skeleton className="h-3 w-3/4" />
            </div>
          ))}
        </div>
      ) : machines.length === 0 ? (
        <Empty icon={<Monitor className="h-5 w-5" />}>
          <span className="block">
            还没有注册的机器。在主机执行 <code className="font-mono">flowloop connect</code>{" "}
            后，这台机器会自动出现在这里。
          </span>
          <span className="mt-3 block">
            <Button variant="outline" onClick={onAddMachine} className="gap-1.5">
              <Plus className="h-4 w-4" aria-hidden="true" />
              添加机器
            </Button>
          </span>
        </Empty>
      ) : (
        <ul className="divide-y divide-line">
          {machines.map((m) => {
            const online = isMachineOnline(m.status);
            const selected = m.id === selectedId;
            const runtimeCount = runtimeCountByMachine.get(m.id) ?? 0;
            const agentCount = agentCountByMachine.get(m.id) ?? 0;
            return (
              <li key={m.id}>
                <button
                  type="button"
                  onClick={() => onSelect(m.id)}
                  aria-current={selected ? "true" : undefined}
                  title={`${m.name} · ${online ? "在线" : "离线"} · 最后心跳 ${
                    m.last_seen_at ? fullTime(m.last_seen_at) : "未知"
                  }`}
                  className={cn(
                    "block w-full border-l-2 px-3 py-2.5 text-left transition-colors",
                    selected
                      ? "border-accent bg-surface-2"
                      : "border-transparent hover:bg-surface-2/50",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:ring-inset"
                  )}
                >
                  <span className="flex items-center gap-2">
                    <span
                      className={cn(
                        "h-1.5 w-1.5 shrink-0 rounded-full",
                        online ? "bg-emerald-400" : "bg-zinc-400"
                      )}
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">
                      {m.name}
                    </span>
                    <span
                      className={cn(
                        "shrink-0 text-[11px]",
                        online ? "text-emerald-300" : "text-ink-3"
                      )}
                    >
                      {online ? "在线" : "离线"}
                    </span>
                  </span>
                  <span className="mt-0.5 block truncate pl-3.5 font-mono text-[11px] text-ink-3">
                    {m.hostname || "主机名未知"}
                  </span>
                  <span className="mt-1 flex items-center justify-between gap-2 pl-3.5 text-[11px] text-ink-3">
                    <span className="truncate">
                      心跳 {m.last_seen_at ? relTime(m.last_seen_at) : "未知"}
                    </span>
                    <span className="shrink-0 font-mono tabular-nums">
                      {runtimeCount} 运行时 · {agentCount} Agent
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </SectionCard>
  );
}