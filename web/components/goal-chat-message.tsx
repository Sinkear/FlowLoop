"use client";

import {
  CheckCircle2,
  CircleX,
  GitBranch,
  HelpCircle,
  Info,
  Reply,
  Square,
} from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import { Markdown } from "@/components/markdown";
import { Skeleton } from "@/components/ui";
import type { Comment } from "@/lib/types";

// ── goal 对话窗口的消息呈现层 ───────────────────────────────
//
// 与 goal-comments.tsx（容器/数据/输入）分开：这里只负责「一条消息长什么样」。
// 三种作者各有自己的形状：
//   human  → 右对齐 accent 气泡 + 「你」头像
//   agent  → 左对齐 surface-2 气泡 + 哈希配色头像 + 名称/agent 徽标/时间
//   system → 居中窄事件条（不是大气泡），失败/取消/门决策共用同一形状
//
// 颜色一律走语义 token（surface / ink / line / accent 及主题已重映射的
// -300 状态槽位），不写死 hex；浅色（默认 Studio Paper）与深色都可读。

// 6 个低饱和色槽位：bg 用 1/15 透明填充，字/环用主题里已按明暗重映射的 -300。
// 同一个 author_id 永远落到同一个色（哈希），跨会话稳定。
const AGENT_PALETTES = [
  "bg-emerald-500/15 text-emerald-300 ring-emerald-500/30",
  "bg-sky-500/15 text-sky-300 ring-sky-500/30",
  "bg-purple-500/15 text-purple-300 ring-purple-500/30",
  "bg-amber-500/15 text-amber-300 ring-amber-500/30",
  "bg-rose-500/15 text-rose-300 ring-rose-500/30",
  "bg-teal-500/15 text-teal-300 ring-teal-500/30",
];

export function agentPalette(seed: string): string {
  let hash = 0;
  for (let i = 0; i < seed.length; i += 1) hash = (hash * 31 + seed.charCodeAt(i)) | 0;
  return AGENT_PALETTES[Math.abs(hash) % AGENT_PALETTES.length];
}

// authorInitials：中文/日文/韩文取首字；拉丁名取前两个字母（大写）。
export function authorInitials(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";
  if (/[\u3400-\u9fff\u3040-\u30ff\uac00-\ud7af]/.test(trimmed[0])) {
    return trimmed.slice(0, 1);
  }
  const ascii = trimmed.match(/[A-Za-z0-9]+/)?.[0] ?? trimmed;
  return ascii.slice(0, 2).toUpperCase();
}

// plainText：把 markdown/mention URI 压成一行可读文本（引用块、回复 chip 用）。
// 只去掉行首的块级标记（标题/引用/列表）和强调符号 `*`、代码包裹符 `` ` ``；
// 保留词内的 `_`——`@dev_team`、`build_skill` 这类标识符被啃掉会改变语义。
export function plainText(content: string): string {
  return content
    .replace(/\[@([^\]]*)\]\(mention:[^)]*\)/g, "@$1")
    .replace(/^[ \t]*#{1,6}[ \t]+/gm, "")
    .replace(/^[ \t]*>[ \t]?/gm, "")
    .replace(/^[ \t]*[-*+][ \t]+/gm, "")
    .replace(/[*`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

export function truncatePlain(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max)}…` : text;
}

export type ChatParent = { authorName: string; text: string };

// ── 单条消息 ──
export function ChatMessage({
  comment,
  authorName,
  agentName,
  parent,
  groupStart,
  replyActive,
  onReply,
}: {
  comment: Comment;
  authorName: string;
  /** mention://agent/<id> 链接的显示名解析（与旧评论区一致）。 */
  agentName?: (id: string) => string;
  parent?: ChatParent | null;
  /** 连续同一作者的消息只在第一条显示头像/名称，后续缩进变窄。 */
  groupStart: boolean;
  replyActive: boolean;
  onReply: () => void;
}) {
  const isHuman = comment.author_type === "human";
  const isAsk = Boolean(comment.ask_human);
  const created = comment.created_at ? new Date(comment.created_at) : null;
  const validCreated = created && !Number.isNaN(created.getTime()) ? created : null;
  const time = comment.created_at ? relTime(comment.created_at) : "";
  const fullTime = validCreated ? validCreated.toLocaleString("zh-CN") : "";
  const content = isAsk ? `[@你](mention://human/ui) ${comment.content}` : comment.content;

  return (
    <div
      className={cn(
        "group relative flex items-start gap-2.5",
        isHuman ? "flex-row-reverse" : "flex-row",
        groupStart ? "mt-3.5" : "mt-1"
      )}
    >
      {groupStart ? (
        isHuman ? (
          <span
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent text-[11px] font-semibold text-accent-ink ring-1 ring-accent/30"
            title="你"
          >
            你
          </span>
        ) : (
          <span
            className={cn(
              "flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ring-1",
              agentPalette(comment.author_id || authorName)
            )}
            title={authorName}
          >
            {authorInitials(authorName)}
          </span>
        )
      ) : (
        <span className="h-8 w-8 shrink-0" aria-hidden />
      )}

      <div
        className={cn(
          // 气泡上限：恢复原来的宽度（桌面 78% / 窄屏 70%）。绝对定位的
          // hover「回复」按钮放在气泡侧边的剩余空间里，剩余列宽足够，不会
          // 溢出滚动容器、把面板顶出横向滚动条。
          "flex min-w-0 max-w-[70%] flex-col sm:max-w-[78%]",
          isHuman ? "items-end" : "items-start"
        )}
      >
        {groupStart && (
          <div
            className={cn(
              "mb-1 flex min-w-0 max-w-full items-center gap-1.5",
              isHuman && "justify-end"
            )}
          >
            <span className="truncate text-[11px] font-medium text-ink-2">{authorName}</span>
            {!isHuman && (
              <span className="shrink-0 rounded border border-line bg-surface-2 px-1 py-px text-[9px] font-medium uppercase tracking-wide text-ink-3">
                agent
              </span>
            )}
            {isAsk && (
              <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full border border-accent/40 bg-accent/10 px-1.5 py-0.5 text-[9px] font-medium text-accent">
                <HelpCircle className="h-2.5 w-2.5" /> 等你回答
              </span>
            )}
            <span className="shrink-0 text-[10px] text-ink-3" title={fullTime} suppressHydrationWarning>
              {time}
            </span>
          </div>
        )}

        <div
          className={cn(
            "relative max-w-full break-words rounded-2xl px-3.5 py-2.5 text-sm",
            isHuman
              ? "bg-accent text-accent-ink"
              : "border border-line/80 bg-surface-2 text-ink",
            isAsk && "border-accent/60 ring-1 ring-accent/25"
          )}
        >
          {parent && (
            <div
              className={cn(
                "mb-2 rounded-lg border-l-2 px-2 py-1",
                isHuman
                  ? "border-accent-ink/40 bg-black/10"
                  : "border-accent/40 bg-surface-0/50"
              )}
            >
              <p
                className={cn(
                  "text-[10px] font-medium",
                  isHuman ? "text-accent-ink/90" : "text-ink-2"
                )}
              >
                回复 {parent.authorName}
              </p>
              <p
                className={cn(
                  "mt-0.5 line-clamp-2 text-[11px] leading-snug",
                  isHuman ? "text-accent-ink/75" : "text-ink-3"
                )}
              >
                {parent.text}
              </p>
            </div>
          )}
          <Markdown
            content={content}
            agentName={agentName}
            className={cn(
              "min-w-0 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
              isHuman
                ? "text-accent-ink [&_a]:text-accent-ink [&_a]:underline [&_code]:bg-black/20 [&_code]:text-accent-ink [&_pre]:bg-black/25 [&_pre]:text-accent-ink [&_blockquote]:border-accent-ink/40 [&_blockquote]:text-accent-ink/80 [&_span]:bg-black/20 [&_span]:text-accent-ink [&_span]:ring-accent-ink/30"
                : "text-ink-2"
            )}
          />
          {/* hover 才出现的「回复」：贴着气泡外侧（agent 在右、human 在左），
              绝对定位不占布局，气泡宽度不会随 hover 抖动。 */}
          <button
            type="button"
            onClick={onReply}
            aria-label={replyActive ? "取消回复" : `回复 ${authorName}`}
            className={cn(
              "absolute top-1 z-10 inline-flex items-center gap-1 rounded-md border bg-surface-1/95 px-1 py-1 text-[10px] font-medium shadow-sm transition-colors sm:px-1.5",
              isHuman ? "right-full mr-1.5" : "left-full ml-1.5",
              replyActive
                ? "border-accent/50 text-accent opacity-100"
                : "border-line text-ink-3 opacity-0 hover:border-line-strong hover:text-ink group-hover:opacity-100 focus-visible:opacity-100"
            )}
          >
            <Reply className="h-3 w-3" />
            <span className="hidden sm:inline">{replyActive ? "取消" : "回复"}</span>
          </button>
        </div>
      </div>
    </div>
  );
}

// ── 系统事件（失败/取消/验证/门决策/节点汇报）──
// 居中窄条，不是大气泡；正文仍走 Markdown，但把标题/代码压到事件条的尺度。
function systemTone(content: string): { Icon: typeof Info; cls: string } {
  if (/❌|失败|未通过|错误/.test(content)) return { Icon: CircleX, cls: "text-red-400" };
  if (/⏹|中止|取消/.test(content)) return { Icon: Square, cls: "text-amber-400" };
  if (/✅|🔍|完成|通过/.test(content)) return { Icon: CheckCircle2, cls: "text-emerald-400" };
  if (/🚦|gate|门|决策/i.test(content)) return { Icon: GitBranch, cls: "text-sky-400" };
  return { Icon: Info, cls: "text-ink-3" };
}

export function SystemEvent({ comment }: { comment: Comment }) {
  const { Icon, cls } = systemTone(comment.content);
  const created = comment.created_at ? new Date(comment.created_at) : null;
  const validCreated = created && !Number.isNaN(created.getTime()) ? created : null;
  const time = comment.created_at ? relTime(comment.created_at) : "";
  const fullTime = validCreated ? validCreated.toLocaleString("zh-CN") : "";

  return (
    <div className="my-3 flex justify-center px-1">
      <div className="flex max-w-[92%] items-start gap-2 rounded-lg border border-line/60 bg-surface-0/40 px-2.5 py-1.5">
        <Icon className={cn("mt-[1px] h-3.5 w-3.5 shrink-0", cls)} aria-hidden />
        <Markdown
          content={comment.content}
          className="min-w-0 whitespace-pre-wrap text-[11px] leading-relaxed text-ink-3 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 [&_a]:text-accent [&_code]:text-[10px] [&_h1]:mb-0.5 [&_h1]:mt-0 [&_h1]:text-[11px] [&_h2]:mb-0.5 [&_h2]:mt-0 [&_h2]:text-[11px] [&_h3]:mb-0.5 [&_h3]:mt-0 [&_h3]:text-[11px] [&_li]:my-0 [&_ol]:my-0.5 [&_p]:my-0.5 [&_pre]:my-1 [&_strong]:font-medium [&_strong]:text-ink-2 [&_ul]:my-0.5"
        />
        {time && (
          <span
            className="mt-[1px] hidden shrink-0 text-[10px] text-ink-3/70 sm:inline"
            title={fullTime}
            suppressHydrationWarning
          >
            {time}
          </span>
        )}
      </div>
    </div>
  );
}

// ── 日期分隔：细线 + 居中日期文字 ──
export function DateSeparator({ label }: { label: string }) {
  return (
    <div className="my-4 flex items-center gap-3 first:mt-0">
      <span className="h-px flex-1 bg-line/70" />
      <span className="shrink-0 rounded-full border border-line bg-surface-1/80 px-2.5 py-0.5 text-[10px] font-medium tracking-wide text-ink-3">
        {label}
      </span>
      <span className="h-px flex-1 bg-line/70" />
    </div>
  );
}

// ── 加载骨架：交替左右两团，保持面板有「正在读历史」的呼吸感 ──
export function ChatSkeleton() {
  return (
    <div className="space-y-5 py-4">
      {[0, 1, 2, 3].map((i) => {
        const right = i % 2 === 1;
        return (
          <div
            key={i}
            className={cn("flex items-start gap-2.5", right && "flex-row-reverse")}
          >
            <Skeleton className="h-8 w-8 shrink-0 rounded-full" />
            <div className={cn("flex w-full max-w-[62%] flex-col gap-1.5", right && "items-end")}>
              <Skeleton className="h-2.5 w-20" />
              <Skeleton className={cn("h-10 w-full rounded-2xl", right && "h-8")} />
            </div>
          </div>
        );
      })}
    </div>
  );
}