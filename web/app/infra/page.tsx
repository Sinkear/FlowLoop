"use client";

import { useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { useAgents, useMachines, useRuntimes, useSquads } from "@/lib/queries";
import { Button, PageBody, PageHeader, Skeleton, StatStrip } from "@/components/ui";
import { AddMachineDialog } from "@/components/infra/add-machine-dialog";
import { MachineList } from "@/components/infra/machine-list";
import { MachineDetail, MachineDetailPlaceholder } from "@/components/infra/machine-detail";
import { isMachineOnline } from "@/components/infra/shared";
import type { Agent, Runtime } from "@/lib/types";

// /infra = 机器 → 运行时 → Agent 的三层归属，一页讲完：
// 顶部健康摘要（哪台离线、哪个席位没模型/没 Agent）→ 左列选机器 → 右列看详情。
export default function InfraPage() {
  const machinesQ = useMachines();
  const runtimesQ = useRuntimes();
  const agentsQ = useAgents();
  const squadsQ = useSquads();

  const machines = useMemo(() => machinesQ.data ?? [], [machinesQ.data]);
  const runtimes = useMemo(() => runtimesQ.data ?? [], [runtimesQ.data]);
  const agents = useMemo(() => agentsQ.data ?? [], [agentsQ.data]);
  const squads = useMemo(() => squadsQ.data ?? [], [squadsQ.data]);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);

  // 离线机器排最前（出了事的先看到），其余按最后心跳倒序。
  const sortedMachines = useMemo(
    () =>
      [...machines].sort((a, b) => {
        const oa = isMachineOnline(a.status) ? 1 : 0;
        const ob = isMachineOnline(b.status) ? 1 : 0;
        if (oa !== ob) return oa - ob;
        return (b.last_seen_at || "").localeCompare(a.last_seen_at || "");
      }),
    [machines]
  );

  // 选择用「显式 id，找不到就落回第一台」的派生值，不在 effect 里改 state，
  // 这样刷新导致排序变化时不会跳到别的机器。
  const selected = sortedMachines.find((m) => m.id === selectedId) ?? sortedMachines[0] ?? null;

  const runtimesByMachine = useMemo(() => {
    const map = new Map<string, Runtime[]>();
    for (const rt of runtimes) {
      const list = map.get(rt.machine_id);
      if (list) list.push(rt);
      else map.set(rt.machine_id, [rt]);
    }
    for (const list of map.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return map;
  }, [runtimes]);

  const machineIdByRuntime = useMemo(
    () => new Map(runtimes.map((rt) => [rt.id, rt.machine_id])),
    [runtimes]
  );

  const agentsByRuntime = useMemo(() => {
    const map = new Map<string, Agent[]>();
    for (const a of agents) {
      if (!a.runtime_id) continue;
      const list = map.get(a.runtime_id);
      if (list) list.push(a);
      else map.set(a.runtime_id, [a]);
    }
    for (const list of map.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return map;
  }, [agents]);

  const runtimeCountByMachine = useMemo(() => {
    const map = new Map<string, number>();
    for (const [mid, list] of runtimesByMachine) map.set(mid, list.length);
    return map;
  }, [runtimesByMachine]);

  const agentCountByMachine = useMemo(() => {
    const map = new Map<string, number>();
    for (const a of agents) {
      const mid = a.runtime_id ? machineIdByRuntime.get(a.runtime_id) : undefined;
      if (mid) map.set(mid, (map.get(mid) ?? 0) + 1);
    }
    return map;
  }, [agents, machineIdByRuntime]);

  const offlineCount = machines.filter((m) => !isMachineOnline(m.status)).length;
  const noModelRuntimes = runtimes.filter((rt) => (rt.models?.length ?? 0) === 0);
  // Catalog detection only reads the CLI config; a runtime can advertise a
  // model list while its provider endpoint is down. The refresh-time prompt
  // probe records that separately, so surface it as its own failure stat.
  const failedProbeRuntimes = runtimes.filter((rt) => rt.probe_status === "failed");
  const unboundRuntimes = runtimes.filter((rt) => (agentsByRuntime.get(rt.id)?.length ?? 0) === 0);

  const isLoading = machinesQ.isLoading || runtimesQ.isLoading || agentsQ.isLoading;
  const selectedRuntimes = selected ? (runtimesByMachine.get(selected.id) ?? []) : [];

  return (
    <PageBody>
      <PageHeader
        title="运行环境"
        desc="机器、运行时和 Agent 的归属都在这一页；哪台机器离线、哪些 Agent 会受影响，一眼能看到。"
        action={
          <Button onClick={() => setAddOpen(true)} className="gap-1.5">
            <Plus className="h-4 w-4" aria-hidden="true" />
            添加机器
          </Button>
        }
      />

      <StatStrip
        items={[
          {
            label: "机器",
            value: machines.length,
            hint: `${machines.length - offlineCount} 台在线`,
            tone: "default",
          },
          {
            label: "离线机器",
            value: offlineCount,
            hint: offlineCount > 0 ? "上面的 Agent 无法领取新任务" : "全部在线",
            tone: offlineCount > 0 ? "err" : "ok",
          },
          {
            label: "模型不可用",
            value: failedProbeRuntimes.length,
            hint:
              failedProbeRuntimes.length > 0
                ? `${(failedProbeRuntimes[0].probe_error || "模型端点不可达").slice(0, 42)}…`
                : noModelRuntimes.length > 0
                  ? "有运行时还没有模型目录，点「刷新检测」"
                  : "模型目录与连通性正常",
            tone: failedProbeRuntimes.length > 0 ? "err" : noModelRuntimes.length > 0 ? "warn" : "ok",
          },
          {
            label: "未绑定 Agent",
            value: unboundRuntimes.length,
            hint: unboundRuntimes.length > 0 ? "有运行时但还没有 Agent 使用" : "运行时都有 Agent",
            tone: unboundRuntimes.length > 0 ? "warn" : "ok",
          },
        ]}
      />

      <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(260px,320px)_minmax(0,1fr)]">
        <MachineList
          machines={sortedMachines}
          runtimeCountByMachine={runtimeCountByMachine}
          agentCountByMachine={agentCountByMachine}
          selectedId={selected?.id ?? null}
          onSelect={setSelectedId}
          onAddMachine={() => setAddOpen(true)}
          isLoading={isLoading}
        />

        {selected ? (
          <MachineDetail
            key={selected.id}
            machine={selected}
            runtimes={selectedRuntimes}
            agentsByRuntime={agentsByRuntime}
            squads={squads}
          />
        ) : isLoading ? (
          <DetailSkeleton />
        ) : (
          <MachineDetailPlaceholder onAddMachine={() => setAddOpen(true)} />
        )}
      </div>

      {addOpen && <AddMachineDialog onClose={() => setAddOpen(false)} />}
    </PageBody>
  );
}

function DetailSkeleton() {
  return (
    <div className="min-w-0 space-y-4" aria-hidden="true">
      <div className="space-y-3 rounded-lg border border-line bg-surface-1 p-4">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-3 w-64" />
        <Skeleton className="h-10 w-full" />
      </div>
      <div className="space-y-3 rounded-lg border border-line bg-surface-1 p-4">
        <Skeleton className="h-4 w-24" />
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-12 w-full" />
        ))}
      </div>
    </div>
  );
}