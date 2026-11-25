// Shared display helpers for the 运行环境 (machine → runtime → agent) page.
// Pure functions only — no hooks, no JSX — so every infra component can use them.

import type { ProbeCLI } from "@/lib/types";

// Machine.status is `connected | offline`; runtime.status is the stored
// `active | absent` plus the read-time derived `inactive` (machine offline).
export function isMachineOnline(status: string): boolean {
  return status === "connected";
}

export const RUNTIME_STATUS: Record<string, { label: string; dot: string; text: string }> = {
  active: { label: "可用", dot: "bg-emerald-400", text: "text-emerald-300" },
  inactive: { label: "机器离线", dot: "bg-amber-400", text: "text-amber-300" },
  absent: { label: "CLI 已移除", dot: "bg-red-400", text: "text-red-300" },
};

export function runtimeStatusMeta(status: string) {
  return RUNTIME_STATUS[status] ?? { label: status, dot: "bg-zinc-400", text: "text-ink-3" };
}

// parseCLIs decodes the machine's probe report (best-effort — bad JSON shows
// as "未探测" rather than crashing the page).
export function parseCLIs(raw: string): ProbeCLI[] {
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? (v as ProbeCLI[]) : [];
  } catch {
    return [];
  }
}

// Probe-registered runtimes are named "<cli>@<machine>".
export function cliType(name: string): string {
  const at = name.indexOf("@");
  return at > 0 ? name.slice(0, at) : name;
}

export function fullTime(iso: string): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

// splitArgs turns a command-line style string into the args array the machine
// uses to spawn the CLI (runtime.args). Quote-aware enough for paths with
// spaces; an unclosed quote is taken literally rather than rejected.
export function splitArgs(raw: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: '"' | "'" | null = null;
  for (const ch of raw) {
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (/\s/.test(ch)) {
      if (cur) {
        out.push(cur);
        cur = "";
      }
    } else {
      cur += ch;
    }
  }
  if (cur) out.push(cur);
  return out;
}

// parseEnv accepts one KEY=VALUE per line; throws with a line number so the
// dialog can point at the bad row instead of failing silently.
export function parseEnv(raw: string): Record<string, string> {
  const env: Record<string, string> = {};
  raw.split("\n").forEach((line, i) => {
    const t = line.trim();
    if (!t) return;
    const eq = t.indexOf("=");
    const key = eq > 0 ? t.slice(0, eq).trim() : "";
    if (!key) throw new Error(`环境变量第 ${i + 1} 行不是 KEY=VALUE 格式。`);
    env[key] = t.slice(eq + 1).trim();
  });
  return env;
}

// copyText uses the async clipboard when available and falls back to the
// textarea trick for plain-http LAN access (no secure context).
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the legacy path */
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}