"use client";

import { useState } from "react";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { useCreateGoal, useHooks } from "@/lib/queries";
import { bindGoalHook } from "@/lib/api";
import { HOOK_EVENTS, type HookEvent } from "@/lib/types";

// 新建 Goal 的表单（对话框形态）。
// 单独抽成组件是因为两个入口都用它：任务页的「手动新建」，以及 /goals/new 里
// 管家没能创建成功时的兜底表单——兜底路径不能是死路。
export function NewGoalForm({
  agents,
  squads,
  domains,
  onClose,
  onCreated,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string }[];
  onClose: () => void;
  onCreated?: () => void;
}) {
  const createGoal = useCreateGoal();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [domainId, setDomainId] = useState("");
  const [domainErr, setDomainErr] = useState("");
  const [category, setCategory] = useState<"hitl" | "hootl">("hitl");
  const [assigneeType, setAssigneeType] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [assigneeErr, setAssigneeErr] = useState("");
  // 结束后执行（Hooks）：hookId → 事件。创建后按返回的 goal.id 逐个绑定
  // （后端 Create 不收 hooks 数组，走独立的 POST /goals/{id}/hooks 端点）。
  const { data: hookItems } = useHooks();
  const [hookSel, setHookSel] = useState<Record<string, HookEvent>>({});
  const enabledHooks = (hookItems ?? []).filter((it) => it.hook.enabled);
  const hookNameOf = (id: string) => enabledHooks.find((it) => it.hook.id === id)?.hook.name ?? id;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const body: Record<string, string> = { title, category };
    if (description) body.description = description;
    if (assigneeType && assigneeId) {
      body.assignee_type = assigneeType;
      body.assignee_id = assigneeId;
      body.status = "active";
    }
    // A chosen assignee TYPE without a chosen agent/squad would silently
    // drop the assignee and the backend rejects it as "agent goal without
    // assignee_id" — fail loudly here instead.
    if (assigneeType && !assigneeId) {
      setAssigneeErr(
        assigneeType === "agent"
          ? "请选择一个 Agent——或先到「Agent」页创建 runtime 和 agent"
          : "请选择一个 Squad——或先到「Squad」页创建小队"
      );
      return;
    }
    // v2: agent/squad-executed goals must belong to a domain (DESIGN.md §2).
    if (assigneeType === "agent" || assigneeType === "squad") {
      if (!domainId) {
        setDomainErr("请选择所属项目——agent 执行的 Goal 必须挂在一个项目上（项目提供 worktree 与验收策略）");
        return;
      }
      body.domain_id = domainId;
    }
    createGoal.mutate(body as Record<string, string> & { title: string }, {
      onSuccess: async (created) => {
        // 先把勾选的 hooks 绑上再关对话框——绑定失败不吞掉（用户会以为绑上了）。
        const entries = Object.entries(hookSel);
        const failedNames: string[] = [];
        if (entries.length) {
          const results = await Promise.allSettled(
            entries.map(([hookId, event]) => bindGoalHook(created.id, { hook_id: hookId, event }))
          );
          results.forEach((r, i) => {
            if (r.status === "rejected") failedNames.push(hookNameOf(entries[i][0]));
          });
        }
        if (failedNames.length) {
          alert(`任务已创建，但以下 hook 绑定失败：${failedNames.join("、")}——可稍后在任务详情里重新绑定`);
        }
        return onCreated ? onCreated() : onClose();
      },
    });
  };

  return (
    <Dialog
      title="新建任务"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" form="new-goal-form" disabled={createGoal.isPending}>
            {createGoal.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="new-goal-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="标题" hint="必填">
          <input value={title} onChange={(e) => setTitle(e.target.value)} className={inputCls} required placeholder="一句话说清要做什么…" />
        </Field>
        <Field label="描述">
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} rows={3} placeholder="可选：验收标准、约束条件…" />
        </Field>
        <Field label="所属项目" hint="agent/squad 执行的任务必填">
          <select value={domainId} onChange={(e) => setDomainId(e.target.value)} className={inputCls}>
            <option value="">选择…</option>
            {domains?.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </Field>
        {/* 验收分类：HITL 结束前走人工审批门禁；HOOTL 跳过复检，完成即结束。 */}
        <Field
          label="验收分类"
          hint={
            category === "hootl"
              ? "无需人工复检，任务完成即结束"
              : "结束前进入 review 阶段等待人工审批"
          }
        >
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value === "hootl" ? "hootl" : "hitl")}
            className={inputCls}
          >
            <option value="hitl">HITL · 人工验收</option>
            <option value="hootl">HOOTL · 免复检</option>
          </select>
        </Field>
        <Field label="负责人类型">
          <select
            value={assigneeType}
            onChange={(e) => {
              setAssigneeType(e.target.value);
              setAssigneeId("");
            }}
            className={inputCls}
          >
            <option value="">无（先放进待办）</option>
            <option value="agent">Agent</option>
            <option value="squad">Squad</option>
          </select>
        </Field>
        {assigneeType === "agent" && (
          <Field label="选择 Agent">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {assigneeType === "squad" && (
          <Field label="选择 Squad">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {squads?.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {/* 结束后执行（Hooks）：折叠区，只有存在启用的 hook 时才出现（spec v0.2 §5.3） */}
        {enabledHooks.length > 0 && (
          <details className="rounded-lg border border-line/60 bg-surface-0/40 px-3 py-2">
            <summary className="cursor-pointer select-none text-sm text-ink-2">
              结束后执行（Hooks）· 可选
              {Object.keys(hookSel).length > 0 && `（已选 ${Object.keys(hookSel).length}）`}
            </summary>
            <div className="mt-2 space-y-1.5">
              <p className="text-[11px] text-ink-3">
                勾选任务结束后自动执行的脚本；事件默认「任务完成」，可改。项目级的默认绑定会自动生效，无需在这里重复勾选。
              </p>
              {enabledHooks.map((it) => {
                const h = it.hook;
                const checked = h.id in hookSel;
                return (
                  <div key={h.id} className="flex flex-wrap items-center gap-2 text-sm">
                    <label className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => {
                          setHookSel((prev) => {
                            const next = { ...prev };
                            if (e.target.checked) next[h.id] = "goal_finished";
                            else delete next[h.id];
                            return next;
                          });
                        }}
                      />
                      <span className="font-mono text-ink-2">{h.name}</span>
                    </label>
                    {checked && (
                      <select
                        value={hookSel[h.id]}
                        onChange={(e) =>
                          setHookSel((prev) => ({ ...prev, [h.id]: e.target.value as HookEvent }))
                        }
                        className={`${inputCls} h-7 w-auto py-0 text-xs`}
                      >
                        {HOOK_EVENTS.map((ev) => (
                          <option key={ev.value} value={ev.value}>
                            {ev.label}
                          </option>
                        ))}
                      </select>
                    )}
                    {h.description && <span className="text-[11px] text-ink-3 truncate max-w-[16rem]">{h.description}</span>}
                  </div>
                );
              })}
            </div>
          </details>
        )}
        {(createGoal.isError || domainErr || assigneeErr) && (
          <p className="text-sm text-red-400">{domainErr || assigneeErr || String(createGoal.error)}</p>
        )}
      </form>
    </Dialog>
  );
}