"use client";

// 扁平参数编辑器：hook 默认参数、绑定级参数共用。
// 参数是 {string: string} 的扁平 map（业界惯例：Argo arguments / K8s
// env 的简化版），注入脚本时三通道齐发——stdin payload 的 params 键、
// AGENTWORK_HOOK_PARAMS（完整 JSON）、AGENTWORK_PARAM_<KEY>（扁平 env）。
// 键规则与后端一致：^[A-Za-z_][A-Za-z0-9_-]{0,63}$（保证扁平 env 名安全）。

import { useState } from "react";
import { Plus, X } from "lucide-react";
import { inputCls } from "@/components/ui";

const KEY_RE = /^[A-Za-z_][A-Za-z0-9_-]{0,63}$/;
export const hookParamKeyValid = (k: string) => KEY_RE.test(k);

type Row = { key: string; value: string };

function toRows(params?: Record<string, string>): Row[] {
  return Object.entries(params ?? {}).map(([key, value]) => ({ key, value }));
}

// Empty rows resolve to {} (NOT undefined): the edit dialog must be able to
// explicitly CLEAR params — undefined would drop the key in JSON.stringify,
// and the backend treats an absent key as "keep current" (replace-all
// semantics), silently resurrecting the old values the user just removed.
export function rowsToParams(rows: Row[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    if (r.key.trim()) out[r.key.trim()] = r.value;
  }
  return out;
}

export function ParamsEditor({
  value,
  onChange,
  compact,
}: {
  value: Record<string, string> | undefined;
  // Receives {} when every row is removed — callers must forward that as a
  // present-but-empty object so replace-all backends can clear params.
  onChange: (v: Record<string, string>) => void;
  compact?: boolean;
}) {
  // NOTE: rows are seeded once at mount and do not follow later prop changes;
  // every current usage mounts the editor fresh inside a dialog.
  const [rows, setRows] = useState<Row[]>(toRows(value));

  const emit = (next: Row[]) => {
    setRows(next);
    onChange(rowsToParams(next));
  };

  const setRow = (i: number, patch: Partial<Row>) => {
    emit(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  };

  const badKey = rows.some((r) => r.key.trim() !== "" && !hookParamKeyValid(r.key.trim()));

  return (
    <div className="space-y-1.5">
      {rows.map((r, i) => (
        <div key={i} className="flex items-center gap-2">
          <input
            value={r.key}
            onChange={(e) => setRow(i, { key: e.target.value })}
            placeholder="参数名"
            className={`${inputCls} ${compact ? "h-8 text-xs" : ""} w-40 font-mono`}
          />
          <input
            value={r.value}
            onChange={(e) => setRow(i, { value: e.target.value })}
            placeholder="参数值"
            className={`${inputCls} ${compact ? "h-8 text-xs" : ""} flex-1 font-mono`}
          />
          <button
            type="button"
            onClick={() => emit(rows.filter((_, j) => j !== i))}
            className="shrink-0 rounded p-1 text-ink-3 transition-colors hover:text-red-400"
            aria-label="删除参数"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => emit([...rows, { key: "", value: "" }])}
          className="inline-flex items-center gap-1 text-xs text-accent transition-colors hover:text-accent/80"
        >
          <Plus className="h-3 w-3" />
          添加参数
        </button>
        {rows.length > 0 && (
          <button
            type="button"
            onClick={() => emit([])}
            className="text-xs text-ink-3 transition-colors hover:text-ink-2"
          >
            清空
          </button>
        )}
      </div>
      {badKey && (
        <p className="text-[11px] text-red-400">
          参数名需以字母或下划线开头，仅含字母/数字/下划线/连字符（最长 64）
        </p>
      )}
    </div>
  );
}
