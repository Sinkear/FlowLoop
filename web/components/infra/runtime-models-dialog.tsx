"use client";

import { useState } from "react";
import { RefreshCw } from "lucide-react";
import { useRefreshRuntimeModels, useRuntimeModels } from "@/lib/queries";
import { Button, Dialog, Skeleton, inputCls } from "@/components/ui";
import { relTime } from "@/lib/utils";
import type { Runtime, RuntimeModel } from "@/lib/types";

// 模型目录弹窗：只读展示运行时探测上报的模型。优先取 GET
// /runtimes/{id}/models，请求期间用列表行里已有的 models 垫底；
// 顶部「刷新检测」让机器立刻重新探测一次（POST .../models/refresh）——
// 这是平台目前唯一支持的“探测”动作。
export function RuntimeModelsDialog({ rt, onClose }: { rt: Runtime; onClose: () => void }) {
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
            机器每次执行 <code className="font-mono">flowloop connect</code>
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