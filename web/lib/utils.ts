import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// relTime renders a timestamp as a short relative string ("刚刚" / "3 分钟前"
// / "2 小时前" / "5 天前"); beyond a week it falls back to the local date.
// Empty / unparsable input returns "" / the raw string.
export function relTime(iso: string): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const seconds = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days} 天前`;
  return new Date(t).toLocaleDateString();
}

// handoff_note 语义拆分：重开时后端（internal/service/goal.go Reopen）会往
// 这个字段写 "Reopened" / "Reopened: <reason>"——那是一条状态转换记录，不是
// 真人交接。展示层分开渲染：重开 → 「重新打开」状态条（带人话解释），
// 其余 → 「交接备注」，不再拿英文原文吓用户。
export type HandoffNoteView =
  | { kind: "reopen"; reason: string }
  | { kind: "handoff"; text: string };

export function parseHandoffNote(note: string): HandoffNoteView {
  if (note === "Reopened") return { kind: "reopen", reason: "" };
  if (note.startsWith("Reopened: "))
    return { kind: "reopen", reason: note.slice("Reopened: ".length) };
  return { kind: "handoff", text: note };
}
