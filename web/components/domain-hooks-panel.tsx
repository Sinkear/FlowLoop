"use client";

import { useState } from "react";
import { Button, Skeleton, inputCls } from "@/components/ui";
import { ParamsEditor } from "@/components/params-editor";
import {
  useDomainHooks,
  useBindDomainHook,
  useUnbindDomainHook,
  useHooks,
} from "@/lib/queries";
import { cn } from "@/lib/utils";
import { HOOK_EVENTS, hookEventLabel, type HookEvent } from "@/lib/types";

const SECTION_LABEL = "font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

// DomainHooksPanel — 项目级默认 Hook 绑定（spec tmp/hooks-spec.md v0.2 §5.3）。
// 对本项目所有任务生效：任务自己的绑定按 (hook, event) 覆盖同组合的项目级
// 绑定；同事件内项目级先于任务级执行。
// 嵌在编辑 domain 的 <form> 里：不用嵌套 <form>（HTML 不允许），所有按钮
// type="button"，输入框 Enter 一律拦下自己提交，避免提交父表单关对话框。
export function DomainHooksPanel({ domainId }: { domainId: string }) {
  const { data: bindings, isLoading } = useDomainHooks(domainId);
  const { data: items } = useHooks();
  const bind = useBindDomainHook();
  const unbind = useUnbindDomainHook();
  const [showAdd, setShowAdd] = useState(false);
  const [hookId, setHookId] = useState("");
  const [event, setEvent] = useState<HookEvent>("goal_finished");
  const [seq, setSeq] = useState(0);
  const [params, setParams] = useState<Record<string, string> | undefined>(undefined);
  const [showParams, setShowParams] = useState(false);
  const [err, setErr] = useState("");

  const enabled = (items ?? []).filter((it) => it.hook.enabled);

  const submit = () => {
    if (!hookId) {
      setErr("请选择一个 Hook");
      return;
    }
    setErr("");
    bind.mutate(
      { domainId, body: { hook_id: hookId, event, seq, params } },
      {
        onSuccess: () => {
          setShowAdd(false);
          setHookId("");
          setSeq(0);
          setParams(undefined);
          setShowParams(false);
        },
        onError: (e) => setErr(String(e)),
      }
    );
  };

  return (
    <div className="rounded-lg border border-line bg-surface-1 p-4 space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className={SECTION_LABEL}>默认 HOOKS</span>
        <span className="text-[11px] text-ink-3">对本项目所有任务生效</span>
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            setShowAdd((v) => !v);
            setErr("");
          }}
          className="ml-auto h-6 px-1.5 text-xs"
        >
          {showAdd ? "收起" : "+ 添加"}
        </Button>
      </div>

      {showAdd && (
        <div className="flex flex-wrap items-end gap-2 rounded border border-line/60 bg-surface-0/40 p-2.5">
          {enabled.length === 0 ? (
            <p className="text-xs text-ink-3">还没有启用的 Hook——先到「Hooks」页创建并启用。</p>
          ) : (
            <>
              <label className="flex flex-col gap-1">
                <span className="text-[11px] text-ink-3">Hook</span>
                <select
                  value={hookId}
                  onChange={(e) => setHookId(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      submit();
                    }
                  }}
                  className={cn(inputCls, "h-8 py-0")}
                >
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
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      submit();
                    }
                  }}
                  className={cn(inputCls, "h-8 py-0")}
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
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      submit();
                    }
                  }}
                  className={cn(inputCls, "h-8 w-20 py-0")}
                  title="同一事件内按顺序号升序执行"
                />
              </label>
              <Button type="button" onClick={submit} disabled={bind.isPending} className="h-8 px-2.5 text-xs">
                {bind.isPending ? "绑定中…" : "绑定"}
              </Button>
              <button
                type="button"
                onClick={() => setShowParams((v) => !v)}
                className="h-8 text-[11px] text-ink-3 transition-colors hover:text-ink-2"
                title="绑定级参数会浅合并覆盖 hook 的默认参数"
              >
                {showParams ? "收起参数" : "参数（可选）"}
              </button>
              {showParams && (
                <div className="w-full rounded border border-line/60 bg-surface-0/60 p-2">
                  <p className="mb-1 text-[11px] text-ink-3">绑定级参数：同名键覆盖 Hook 默认参数，仅对本绑定生效</p>
                  <ParamsEditor value={params} onChange={setParams} compact />
                </div>
              )}
            </>
          )}
          {err && <p className="w-full text-xs text-red-400">{err}</p>}
        </div>
      )}

      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-6 w-2/3" />
          <Skeleton className="h-6 w-1/2" />
        </div>
      ) : !bindings || bindings.length === 0 ? (
        <p className="text-xs text-ink-3">没有项目级默认绑定——本项目任务结束后只执行任务自己勾选的 Hook。</p>
      ) : (
        <div className="divide-y divide-line rounded border border-line">
          {bindings.map((b) => (
            <div key={b.id} className="flex flex-wrap items-center gap-2 px-2.5 py-1.5">
              <span className="font-mono text-xs text-ink-2">{b.hook_name}</span>
              <span className="text-[11px] text-accent">{hookEventLabel(b.event)}</span>
              <span className="font-mono text-[10px] text-ink-3">#{b.seq}</span>
              {b.params && Object.keys(b.params).length > 0 && (
                <span
                  className="max-w-56 truncate font-mono text-[10px] text-ink-3"
                  title={Object.entries(b.params).map(([k, v]) => `${k}=${v}`).join("\n")}
                >
                  {Object.entries(b.params).map(([k, v]) => `${k}=${v}`).join(" ")}
                </span>
              )}
              <button
                type="button"
                onClick={() => unbind.mutate({ domainId, bindingId: b.id }, { onError: (e) => setErr(String(e)) })}
                disabled={unbind.isPending}
                className="ml-auto text-[11px] text-ink-3 hover:text-red-400 disabled:opacity-50"
              >
                移除
              </button>
            </div>
          ))}
        </div>
      )}
      {bind.isError && <p className="text-xs text-red-400">{String(bind.error)}</p>}
      {unbind.isError && <p className="text-xs text-red-400">{String(unbind.error)}</p>}
    </div>
  );
}
