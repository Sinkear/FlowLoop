"use client";

import { useState } from "react";
import { useGoalHooks, useHooks, useBindGoalHook, useUnbindGoalHook } from "@/lib/queries";
import { Button, Empty, inputCls } from "@/components/ui";
import { ParamsEditor } from "@/components/params-editor";
import { HOOK_EVENTS, hookEventLabel, type HookEvent } from "@/lib/types";

// Goal 详情页的「结束后执行（Hooks）」面板。
// - 列出生效绑定：goal 级 + 从 domain 继承的默认绑定（scope=domain，标灰，只读）。
// - goal 非终态时可增删 goal 级绑定；终态后绑定只读（后端 409）。
// spec tmp/hooks-spec.md v0.2 §5.3。
export function GoalHooksPanel({ goalId, goalStatus }: { goalId: string; goalStatus: string }) {
  const terminal = goalStatus === "done" || goalStatus === "failed" || goalStatus === "cancelled";
  const { data: bindings, isLoading } = useGoalHooks(goalId);
  const bind = useBindGoalHook();
  const unbind = useUnbindGoalHook();
  const [showAdd, setShowAdd] = useState(false);
  const [err, setErr] = useState("");

  const goalScoped = (bindings ?? []).filter((b) => b.scope === "goal");
  const domainScoped = (bindings ?? []).filter((b) => b.scope === "domain");

  return (
    <div className="bg-surface-1 rounded-2xl border border-line/80 shadow-sm overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2.5 border-b border-line">
        <div className="text-xs font-medium text-ink-3 uppercase tracking-wide">
          结束后执行（Hooks）{bindings && bindings.length > 0 && `（${bindings.length}）`}
        </div>
        {!terminal && (
          <button
            type="button"
            onClick={() => {
              setShowAdd(!showAdd);
              setErr("");
            }}
            className="text-xs text-accent hover:underline"
          >
            {showAdd ? "收起" : "+ 添加绑定"}
          </button>
        )}
      </div>
      <div className="p-4 space-y-3">
        {showAdd && !terminal && <AddBindingForm goalId={goalId} onDone={() => setShowAdd(false)} />}

        {isLoading ? (
          <div className="text-sm text-ink-3 text-center py-4">加载中…</div>
        ) : !bindings || bindings.length === 0 ? (
          <Empty>
            尚未绑定 Hook——任务结束后不会执行任何自定义脚本。
            {!terminal && "可点右上角「添加绑定」，或到「Hooks」页先创建一个。"}
          </Empty>
        ) : (
          <div className="space-y-2">
            {goalScoped.length > 0 && (
              <div className="space-y-1.5">
                {goalScoped.map((b) => (
                  <div
                    key={b.id}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-line/60 bg-surface-0/40 px-3 py-2"
                  >
                    <span className="font-mono text-sm text-ink">{b.hook_name}</span>
                    <span className="text-xs text-accent">{hookEventLabel(b.event)}</span>
                    <span className="font-mono text-[11px] text-ink-3">#{b.seq}</span>
                    <span className="text-[11px] text-ink-3">任务级</span>
                    {b.params && Object.keys(b.params).length > 0 && (
                      <span
                        className="max-w-56 truncate font-mono text-[10px] text-ink-3"
                        title={Object.entries(b.params).map(([k, v]) => `${k}=${v}`).join("\n")}
                      >
                        {Object.entries(b.params).map(([k, v]) => `${k}=${v}`).join(" ")}
                      </span>
                    )}
                    {!terminal && (
                      <button
                        type="button"
                        onClick={() =>
                          unbind.mutate({ goalId, bindingId: b.id }, { onError: (e) => setErr(String(e)) })
                        }
                        disabled={unbind.isPending}
                        className="ml-auto text-[11px] text-ink-3 hover:text-red-400 disabled:opacity-50"
                      >
                        移除
                      </button>
                    )}
                  </div>
                ))}
              </div>
            )}
            {domainScoped.length > 0 && (
              <div className="space-y-1.5">
                {domainScoped.map((b) => (
                  <div
                    key={b.id}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-dashed border-line px-3 py-2 opacity-70"
                    title="项目级默认绑定——在本任务所属项目的编辑对话框里管理"
                  >
                    <span className="font-mono text-sm text-ink-2">{b.hook_name}</span>
                    <span className="text-xs text-accent/80">{hookEventLabel(b.event)}</span>
                    <span className="font-mono text-[11px] text-ink-3">#{b.seq}</span>
                    <span className="ml-auto text-[11px] text-ink-3">继承自项目</span>
                  </div>
                ))}
              </div>
            )}
            {terminal && (
              <p className="text-[11px] text-ink-3">任务已进入终态，绑定只读——结束后不会再新增绑定。</p>
            )}
          </div>
        )}
        {(err || bind.isError) && <p className="text-sm text-red-400">{err || String(bind.error)}</p>}
        {unbind.isError && <p className="text-sm text-red-400">{String(unbind.error)}</p>}
      </div>
    </div>
  );
}

// 添加绑定：hook（仅 enabled）+ 事件 + 顺序号 + 可选绑定级参数（覆盖 hook 默认）。
function AddBindingForm({ goalId, onDone }: { goalId: string; onDone: () => void }) {
  const { data: items } = useHooks();
  const bind = useBindGoalHook();
  const [hookId, setHookId] = useState("");
  const [event, setEvent] = useState<HookEvent>("goal_finished");
  const [seq, setSeq] = useState(0);
  const [params, setParams] = useState<Record<string, string> | undefined>(undefined);
  const [showParams, setShowParams] = useState(false);
  const [err, setErr] = useState("");

  const enabled = (items ?? []).filter((it) => it.hook.enabled);

  if (!enabled.length) {
    return (
      <p className="text-xs text-ink-3 rounded-lg border border-dashed border-line px-3 py-2">
        还没有启用的 Hook——先到「Hooks」页创建并启用。
      </p>
    );
  }

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-line bg-surface-0/40 p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!hookId) {
          setErr("请选择一个 Hook");
          return;
        }
        bind.mutate(
          { goalId, body: { hook_id: hookId, event, seq, params } },
          {
            onSuccess: onDone,
            onError: (e2) => setErr(String(e2)),
          }
        );
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="text-[11px] text-ink-3">Hook</span>
        <select value={hookId} onChange={(e) => setHookId(e.target.value)} className={`${inputCls} h-9 py-1`}>
          <option value="">选择…</option>
          {enabled.map((it) => (
            <option key={it.hook.id} value={it.hook.id}>
              {it.hook.name}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[11px] text-ink-3">事件</span>
        <select
          value={event}
          onChange={(e) => setEvent(e.target.value as HookEvent)}
          className={`${inputCls} h-9 py-1`}
        >
          {HOOK_EVENTS.map((ev) => (
            <option key={ev.value} value={ev.value}>
              {ev.label}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[11px] text-ink-3">顺序</span>
        <input
          type="number"
          value={seq}
          onChange={(e) => setSeq(Number(e.target.value))}
          className={`${inputCls} h-9 w-20 py-1`}
          title="同一事件内按顺序号升序执行"
        />
      </label>
      <Button type="submit" disabled={bind.isPending} className="h-9">
        {bind.isPending ? "绑定中…" : "绑定"}
      </Button>
      <button
        type="button"
        onClick={() => setShowParams(!showParams)}
        className="h-9 text-[11px] text-ink-3 transition-colors hover:text-ink-2"
        title="绑定级参数会浅合并覆盖 hook 的默认参数"
      >
        {showParams ? "收起参数" : "参数（可选）"}
      </button>
      {showParams && (
        <div className="w-full rounded-lg border border-line/60 bg-surface-0/60 p-2.5">
          <p className="mb-1.5 text-[11px] text-ink-3">绑定级参数：同名键覆盖 Hook 默认参数，仅对本绑定生效</p>
          <ParamsEditor value={params} onChange={setParams} compact />
        </div>
      )}
      {err && <p className="w-full text-sm text-red-400">{err}</p>}
    </form>
  );
}
