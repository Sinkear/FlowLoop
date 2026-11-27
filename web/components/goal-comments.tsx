"use client";

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowDown,
  AtSign,
  Loader2,
  MessagesSquare,
  Send,
  X,
} from "lucide-react";
import {
  useAgents,
  useCreateGoalComment,
  useGoalComments,
  useSquads,
} from "@/lib/queries";
import { useWSEvent } from "@/lib/ws";
import { cn } from "@/lib/utils";
import { useStickToBottom } from "@/lib/use-stick-to-bottom";
import {
  authorInitials,
  agentPalette,
  ChatMessage,
  ChatSkeleton,
  DateSeparator,
  plainText,
  SystemEvent,
  truncatePlain,
  type ChatParent,
} from "@/components/goal-chat-message";
import type { Comment } from "@/lib/types";

// ── 任务对话：teamily 式多智能体对话窗口 ─────────────────────
//
// 组件自身就是一块完整面板（header / 消息区 / 固定输入区），父容器只负责
// 给高度（h-full 或 flex-1 + min-h-0）。数据仍然是 goal comments：
//   - useGoalComments 拉历史；useWSEvent("comment:created") 实时追加并去重；
//   - 人右对齐 accent 气泡，agent 左对齐 surface-2 气泡，system 居中事件条；
//   - reply / parent 引用、ask_human 徽标、Markdown、@ mention 全部保留；
//   - useStickToBottom 贴底跟随，上滚时浮出「有新消息」按钮。

const GROUP_WINDOW_MS = 3 * 60 * 1000;
const INITIAL_WINDOW = 120; // 首屏渲染的条数；更早的历史点按钮再展开
const WINDOW_STEP = 200;

export function GoalComments({
  goalId,
  className,
}: {
  goalId: string;
  className?: string;
}) {
  const { data: comments, isLoading, isError } = useGoalComments(goalId);
  const createComment = useCreateGoalComment();
  const { data: agents } = useAgents();

  const [liveComments, setLiveComments] = useState<Comment[]>([]);
  const [replyTo, setReplyTo] = useState<Comment | null>(null);
  const [limit, setLimit] = useState(INITIAL_WINDOW);
  // header 右侧的小圆点：有实时事件到达时短暂 pulse（不额外发请求）。
  const [pulseSeq, setPulseSeq] = useState(0);
  const [pulsing, setPulsing] = useState(false);

  // agent id → 显示名；agents 还没加载时退回 8 位 id，别让界面闪现 32 位 hex。
  const agentName = useCallback(
    (id: string) => agents?.find((a) => a.id === id)?.name ?? (id || "未知").slice(0, 8),
    [agents]
  );

  // 切换 goal：清掉上一个任务的实时消息与回复目标，窗口重置。
  useEffect(() => {
    setLiveComments([]);
    setReplyTo(null);
    setLimit(INITIAL_WINDOW);
  }, [goalId]);

  // 实时通道：payload 就是后端 Comment 结构（对应 comment:created 事件）。
  useWSEvent("comment:created", (p) => {
    const incoming = p as unknown as Comment;
    if (!incoming || incoming.goal_id !== goalId) return;
    setLiveComments((prev) =>
      prev.some((c) => c.id === incoming.id) ? prev : [...prev, incoming]
    );
    setPulseSeq((n) => n + 1);
  });

  // 合并历史与实时消息，按时间正序；用 id 去重（refetch 与 WS 会拿到同一条）。
  const allComments = useMemo(() => {
    const base = comments ?? [];
    const seen = new Set(base.map((c) => c.id));
    const extras = liveComments.filter((c) => !seen.has(c.id));
    return [...base, ...extras].sort((a, b) => {
      const at = Date.parse(a.created_at);
      const bt = Date.parse(b.created_at);
      if (Number.isFinite(at) && Number.isFinite(bt) && at !== bt) return at - bt;
      return a.id.localeCompare(b.id);
    });
  }, [comments, liveComments]);

  // 父消息可能是被窗口裁掉的更早一条，所以索引建在「全部」上，而不是可见窗口。
  const byId = useMemo(() => new Map(allComments.map((c) => [c.id, c])), [allComments]);

  const visibleComments = useMemo(
    () => (allComments.length > limit ? allComments.slice(-limit) : allComments),
    [allComments, limit]
  );

  // ── 贴底跟随 + 「有新消息」浮按钮 ──
  // useStickToBottom 返回 callback ref；再包一层把节点交给自己的 ref，供
  // 浮按钮和「发送后跳到底」使用。两个 ref 都是稳定的，不会反复解绑重绑。
  const scrollRef = useStickToBottom<HTMLDivElement>(
    [allComments.length, goalId],
    { threshold: 72 }
  );
  const listRef = useRef<HTMLDivElement | null>(null);
  const setListRef = useCallback(
    (node: HTMLDivElement | null) => {
      scrollRef(node);
      listRef.current = node;
    },
    [scrollRef]
  );

  const [atBottom, setAtBottom] = useState(true);
  const atBottomRef = useRef(true);
  const [unread, setUnread] = useState(0);
  const prevCountRef = useRef(0);
  // 「显示更早的消息」是在列表顶部插入，插入后要把 scrollTop 补回去，
  // 否则视口会跳到新插入的旧消息上，人正在读的位置就丢了。
  const prependAnchorRef = useRef<{ scrollHeight: number; scrollTop: number } | null>(null);

  const updateAtBottom = useCallback((next: boolean) => {
    atBottomRef.current = next;
    setAtBottom(next);
  }, []);

  const jumpToBottom = useCallback(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    updateAtBottom(true);
    setUnread(0);
  }, [updateAtBottom]);

  // 面板高度变化（输入框长高、窗口缩放、移动端键盘弹出）时，如果人本来就
  // 贴在底部，就继续贴在底部——否则最后一条会被裁到视野外。
  useEffect(() => {
    const el = listRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      if (atBottomRef.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // 只在「用户正看着历史」时累计未读数；首次载入历史不算新消息。
  useEffect(() => {
    const prev = prevCountRef.current;
    prevCountRef.current = allComments.length;
    if (prev === 0) return;
    const added = allComments.length - prev;
    if (added > 0 && !atBottomRef.current) setUnread((n) => n + added);
    if (added < 0) setUnread(0);
  }, [allComments.length]);

  // 首次载入 / 切 goal：历史落地后直接到最新一条。
  useEffect(() => {
    if (isLoading) return;
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    updateAtBottom(true);
    setUnread(0);
  }, [goalId, isLoading, updateAtBottom]);

  // 展开更早消息：用插入前后的高度差把 scrollTop 补回来，保持阅读位置不动。
  useLayoutEffect(() => {
    const el = listRef.current;
    const anchor = prependAnchorRef.current;
    if (!el || !anchor) return;
    prependAnchorRef.current = null;
    const delta = el.scrollHeight - anchor.scrollHeight;
    if (delta > 0) el.scrollTop = anchor.scrollTop + delta;
  }, [limit]);

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    const near = distance <= 72;
    if (near !== atBottomRef.current) updateAtBottom(near);
    if (near && unread !== 0) setUnread(0);
  };

  // header 小圆点的 pulse：收到实时事件后亮 2.2s 再落回静态。
  useEffect(() => {
    if (pulseSeq === 0) return;
    setPulsing(true);
    const t = window.setTimeout(() => setPulsing(false), 2200);
    return () => window.clearTimeout(t);
  }, [pulseSeq]);

  // ── 作者显示名 ──
  const authorLabel = useCallback(
    (c: Comment) =>
      c.author_type === "human"
        ? "你"
        : c.author_type === "system"
          ? "系统"
          : agentName(c.author_id),
    [agentName]
  );

  const handleReply = useCallback((c: Comment) => {
    setReplyTo((prev) => (prev?.id === c.id ? null : c));
  }, []);

  const handleSubmit = useCallback(
    async (text: string) => {
      if (!text.trim()) return;
      // 用 mutateAsync：失败时让错误冒泡回输入框（保留草稿），
      // 成功后才清空并把视角带回最新一条。
      await createComment.mutateAsync({
        goalId,
        author_type: "human",
        author_id: "ui",
        content: text,
        parent_id: replyTo?.id,
      });
      setReplyTo(null);
      jumpToBottom();
    },
    [goalId, createComment, replyTo, jumpToBottom]
  );

  // ── 消息流：日期分隔 + 同作者合并（连续 3 分钟内）──
  const rows = useMemo(() => {
    const out: React.ReactNode[] = [];
    let lastDay = "";

    visibleComments.forEach((c, i) => {
      const dk = dayKeyOf(c.created_at);
      if (dk && dk !== lastDay) {
        out.push(<DateSeparator key={`day-${dk}`} label={dayLabelOf(c.created_at)} />);
        lastDay = dk;
      }

      if (c.author_type === "system") {
        out.push(<SystemEvent key={c.id} comment={c} />);
        return;
      }

      const prev = i > 0 ? visibleComments[i - 1] : null;
      const prevChat = prev && prev.author_type !== "system" ? prev : null;
      const sameAuthor =
        !!prevChat &&
        prevChat.author_type === c.author_type &&
        prevChat.author_id === c.author_id;
      const at = Date.parse(c.created_at);
      const prevAt = prevChat ? Date.parse(prevChat.created_at) : NaN;
      const closeInTime =
        Number.isFinite(at) &&
        Number.isFinite(prevAt) &&
        at - prevAt >= 0 &&
        at - prevAt < GROUP_WINDOW_MS;
      const sameDay = prevChat ? dayKeyOf(prevChat.created_at) === dk : false;

      const parentComment = c.parent_id ? byId.get(c.parent_id) : undefined;
      const parent: ChatParent | null = parentComment
        ? {
            authorName: authorLabel(parentComment),
            text: truncatePlain(plainText(parentComment.content), 96),
          }
        : null;

      out.push(
        <ChatMessage
          key={c.id}
          comment={c}
          authorName={authorLabel(c)}
          agentName={agentName}
          parent={parent}
          groupStart={!sameAuthor || !closeInTime || !sameDay}
          replyActive={replyTo?.id === c.id}
          onReply={() => handleReply(c)}
        />
      );
    });

    return out;
  }, [visibleComments, byId, authorLabel, agentName, replyTo, handleReply]);

  const hiddenCount = allComments.length - visibleComments.length;
  const pending = createComment.isPending;

  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-line bg-surface-1 shadow-sm",
        className
      )}
    >
      {/* ── header ── */}
      <header className="flex shrink-0 items-center gap-2.5 border-b border-line px-4 py-2.5">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-line bg-surface-2 text-ink-2">
          <MessagesSquare className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="truncate text-sm font-medium text-ink">任务对话</h2>
            <span className="shrink-0 rounded-full border border-line bg-surface-2 px-1.5 py-px font-mono text-[10px] text-ink-3">
              {allComments.length}
            </span>
          </div>
          <p className="mt-0.5 truncate text-[11px] text-ink-3">
            执行者与你的协作记录 · 实时
          </p>
        </div>
        <span
          className={cn(
            "ml-auto h-1.5 w-1.5 shrink-0 rounded-full transition-colors",
            pulsing ? "animate-pulse bg-accent" : "bg-accent/60"
          )}
          title={pulsing ? "正在实时同步" : "实时同步"}
        />
      </header>

      {/* ── 消息区：唯一滚动容器；浮按钮相对它定位 ── */}
      <div className="relative min-h-0 flex-1">
        <div
          ref={setListRef}
          onScroll={handleScroll}
          role="log"
          aria-label="任务对话消息"
          className="h-full overflow-y-auto overscroll-contain"
        >
          {isLoading ? (
            <div className="mx-auto w-full max-w-3xl sm:max-w-4xl px-3 sm:px-5">
              <ChatSkeleton />
            </div>
          ) : allComments.length === 0 ? (
            <div className="mx-auto flex h-full w-full max-w-3xl sm:max-w-4xl flex-col items-center justify-center px-6 py-8 text-center">
              <span className="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl border border-line bg-surface-2 text-ink-3">
                <MessagesSquare className="h-5 w-5" />
              </span>
              <p className="text-sm font-medium text-ink">对话还没开始</p>
              <p className="mt-1 max-w-sm text-xs leading-relaxed text-ink-3">
                节点完成会自动汇报到这里；你也可以在下方直接 @ 执行者追问或补充要求。
              </p>
              <p className="mt-3 flex items-center gap-1 text-[10px] text-ink-3">
                <AtSign className="h-3 w-3" />
                输入 @ 可指派 agent 或 squad 接手
              </p>
              {isError && (
                <p className="mt-2 text-[11px] text-red-400">
                  对话加载失败，稍后会自动重试。
                </p>
              )}
            </div>
          ) : (
            <div className="mx-auto w-full max-w-3xl sm:max-w-4xl px-3 py-2 sm:px-5">
              {isError && (
                <p className="mb-3 rounded-lg border border-red-500/20 bg-red-500/5 px-2.5 py-1.5 text-[11px] text-red-400">
                  对话加载失败，稍后会自动重试；已到达的实时消息仍会显示。
                </p>
              )}
              {hiddenCount > 0 && (
                <div className="mb-1 flex justify-center">
                  <button
                    type="button"
                    onClick={() => {
                      const el = listRef.current;
                      if (el) {
                        prependAnchorRef.current = {
                          scrollHeight: el.scrollHeight,
                          scrollTop: el.scrollTop,
                        };
                      }
                      setLimit((n) => n + WINDOW_STEP);
                    }}
                    className="rounded-full border border-line bg-surface-1/80 px-2.5 py-1 text-[10px] font-medium text-ink-3 transition-colors hover:border-accent/40 hover:text-accent"
                  >
                    显示更早的消息（还有 {hiddenCount} 条）
                  </button>
                </div>
              )}
              {rows}
            </div>
          )}
        </div>

        {!atBottom && (
          <button
            type="button"
            onClick={jumpToBottom}
            aria-label={unread > 0 ? `回到底部，${unread} 条新消息` : "回到最新消息"}
            className="absolute bottom-3 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-line bg-surface-1/95 px-3 py-1.5 text-[11px] font-medium text-ink-2 shadow-lg shadow-black/10 backdrop-blur transition-colors hover:border-accent/50 hover:text-accent"
          >
            <ArrowDown className="h-3 w-3" />
            {unread > 0 ? "有新消息" : "回到最新"}
            {unread > 0 && (
              <span className="rounded-full bg-accent/15 px-1 font-mono text-[10px] text-accent">
                {unread}
              </span>
            )}
          </button>
        )}
      </div>

      {/* ── 固定输入区 ── */}
      <div className="shrink-0 border-t border-line bg-surface-1 px-3 pb-3 pt-2.5 sm:px-4">
        <div className="mx-auto w-full max-w-3xl sm:max-w-4xl">
          {replyTo && (
            <div className="mb-2 flex items-center gap-2 rounded-lg border border-accent/25 bg-accent/5 px-2 py-1.5">
              <span className="shrink-0 rounded-full border border-accent/30 bg-accent/10 px-2 py-0.5 text-[10px] font-medium text-accent">
                回复 {replyTo.author_type === "agent" ? "@" : ""}
                {authorLabel(replyTo)}
              </span>
              <span className="min-w-0 flex-1 truncate text-[11px] text-ink-3">
                {truncatePlain(plainText(replyTo.content), 80)}
              </span>
              <button
                type="button"
                onClick={() => setReplyTo(null)}
                aria-label="取消回复"
                className="shrink-0 rounded p-0.5 text-ink-3 transition-colors hover:text-ink"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          )}

          {createComment.isError && (
            <p className="mb-2 text-[11px] text-red-400">
              发送失败：{errorText(createComment.error)}
            </p>
          )}

          <MentionComposer
            key={goalId}
            onSubmit={handleSubmit}
            pending={pending}
            focusKey={replyTo?.id}
          />
        </div>
      </div>
    </div>
  );
}

// ── 日期分隔的标签 ──
function dayKeyOf(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

function dayLabelOf(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  const startToday = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();
  const startThat = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diffDays = Math.round((startToday - startThat) / 86400000);
  if (diffDays === 0) {
    // 「今天 14:03」：只有当天的分隔才带时间，昨天/更早只给日期。
    const hhmm = d.toLocaleTimeString("zh-CN", {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    return `今天 ${hhmm}`;
  }
  if (diffDays === 1) return "昨天";
  if (d.getFullYear() === today.getFullYear()) {
    return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
  }
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

// ── 输入框：可自动增高 + @ 结构化 mention + Enter 发送 ──
//
// 保留原实现的 mention 语义：输入框里始终是可读文本（@dev-team），提交时
// 才把 roster 里的名字换成 [@Name](mention://agent|squad/<id>) —— 与平台
// 和 agent 写出来的 URI 形状完全一致（URI 形状不变）。
function MentionComposer({
  onSubmit,
  pending,
  focusKey,
}: {
  onSubmit: (text: string) => void | Promise<void>;
  pending: boolean;
  /** 点「回复」时把焦点拉回输入框；值变化即聚焦。 */
  focusKey?: string;
}) {
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const ref = useRef<HTMLTextAreaElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const [value, setValue] = useState("");
  const [cursor, setCursor] = useState(0);
  const [at, setAt] = useState(-1); // 当前 @ 的位置（字符下标）
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState(0);

  const roster = useMemo<{ id: string; name: string; type: string }[]>(
    () => [
      ...(agents ?? []).map((a) => ({ id: a.id, name: a.name, type: "agent" })),
      ...(squads ?? []).map((s) => ({ id: s.id, name: s.name, type: "squad" })),
    ],
    [agents, squads]
  );

  const matches = useMemo(
    () =>
      at === -1
        ? []
        : roster.filter((r) => r.name.toLowerCase().includes(query.toLowerCase())),
    [at, query, roster]
  );
  const menuOpen = at !== -1 && matches.length > 0;

  // 自动增高：1 行起，最多约 6 行（leading-6 + py-2 = 40px / 160px）。
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(Math.max(el.scrollHeight, 40), 160)}px`;
  }, [value]);

  useEffect(() => {
    if (!focusKey) return;
    ref.current?.focus();
  }, [focusKey]);

  // 扫描光标前的文本：行首或空白后的最后一个 @ 且到光标间无空白，才算 mention。
  const scan = (text: string, pos: number) => {
    const before = text.slice(0, pos);
    const i = before.lastIndexOf("@");
    if (i === -1) return { at: -1, q: "" };
    if (i > 0 && !/\s/.test(before[i - 1])) return { at: -1, q: "" };
    const q = before.slice(i + 1);
    if (/\s/.test(q)) return { at: -1, q: "" };
    return { at: i, q };
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const pos = e.target.selectionStart ?? e.target.value.length;
    setValue(e.target.value);
    setCursor(pos);
    const s = scan(e.target.value, pos);
    setAt(s.at);
    setQuery(s.q);
    setIndex(0);
  };

  const insertMention = (r: { id: string; name: string; type: string }) => {
    const mention = `@${r.name} `;
    const next = value.slice(0, at) + mention + value.slice(cursor);
    setValue(next);
    setAt(-1);
    setQuery("");
    requestAnimationFrame(() => {
      const el = ref.current;
      if (el) {
        el.focus();
        const pos = at + mention.length;
        el.setSelectionRange(pos, pos);
      }
    });
  };

  // 提交时把 @name 换成结构化 URI；长名优先，避免部分匹配互相吃掉。
  const buildContent = (text: string) => {
    let out = text;
    const sorted = [...roster].sort((a, b) => b.name.length - a.name.length);
    for (const r of sorted) {
      const re = new RegExp(`@${escapeRegExp(r.name)}(?![\\w-])`, "g");
      out = out.replace(re, `[@${r.name}](mention://${r.type}/${r.id})`);
    }
    return out;
  };

  const canSend = value.trim().length > 0 && !pending;

  const submit = async () => {
    if (!canSend) return;
    const draft = value;
    try {
      await onSubmit(buildContent(draft));
    } catch {
      // 发送失败：草稿留在输入框里，错误红字由父组件显示。
      return;
    }
    // 发送期间用户可能又敲了新内容，别把它一起清掉。
    setValue((cur) => (cur === draft ? "" : cur));
    setAt(-1);
    setQuery("");
    ref.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (menuOpen) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setIndex((i) => (i + 1) % matches.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setIndex((i) => (i - 1 + matches.length) % matches.length);
        return;
      }
      if (e.key === "Tab" || e.key === "Enter") {
        // Tab 快选（Slack/GitHub 习惯）+ Enter 确认；此时 Enter 不发送。
        e.preventDefault();
        insertMention(matches[index]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setAt(-1);
        return;
      }
    }
    // Shift+Enter 换行由 textarea 默认行为处理；IME 组字中的 Enter 不发送。
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="relative">
      <div className="rounded-xl border border-line-strong bg-surface-2 transition-colors focus-within:border-accent/50 focus-within:ring-2 focus-within:ring-accent/15">
        <div className="relative">
          {/* 高亮层：与 textarea 同字号/同内边距/同换行，只给 @名字铺紫底；
              文字保持透明，正文仍由上层 textarea 显示，两层不会叠字。 */}
          <div
            ref={overlayRef}
            aria-hidden
            className="pointer-events-none absolute inset-0 overflow-hidden px-3 py-2 text-sm leading-6 whitespace-pre-wrap break-words text-transparent"
          >
            {highlightMentions(value, roster)}
          </div>
          <textarea
            ref={ref}
            value={value}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            onBlur={() => setAt(-1)}
            onScroll={(e) => {
              if (overlayRef.current) overlayRef.current.scrollTop = e.currentTarget.scrollTop;
            }}
            onFocus={(e) => {
              const pos = e.target.selectionStart ?? value.length;
              setCursor(pos);
              const s = scan(value, pos);
              setAt(s.at);
              setQuery(s.q);
            }}
            rows={1}
            placeholder="说点什么…（输入 @ 可提及 agent / squad）"
            className="relative block w-full resize-none overflow-y-auto bg-transparent px-3 py-2 text-sm leading-6 text-ink placeholder:text-ink-3 focus:outline-none"
            style={{ maxHeight: 160 }}
          />
        </div>
        <div className="flex items-center gap-2 border-t border-line/70 px-2 py-1.5">
          <span className="flex min-w-0 items-center gap-1 text-[10px] text-ink-3">
            <AtSign className="h-3 w-3 shrink-0" />
            <span className="truncate">Enter 发送 · Shift+Enter 换行 · 输入 @ 指派智能体</span>
          </span>
          <button
            type="button"
            onClick={submit}
            disabled={!canSend}
            title="发送（Enter）"
            aria-label="发送"
            className="ml-auto flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-accent text-accent-ink transition-colors hover:bg-accent-dim disabled:cursor-not-allowed disabled:opacity-40"
          >
            {pending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Send className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
      </div>

      {menuOpen && (
        <div
          role="listbox"
          aria-label="选择要提及的对象"
          className="absolute bottom-full left-0 right-0 z-30 mb-1.5 max-h-64 overflow-y-auto rounded-xl border border-line bg-surface-1 p-1 shadow-xl shadow-black/30"
        >
          <div className="px-2 py-1 text-[10px] text-ink-3">
            ↑↓ 选择 · Enter/Tab 确认 · Esc 取消
          </div>
          {matches.map((r, i) => (
            <button
              key={`${r.type}-${r.id}`}
              type="button"
              role="option"
              aria-selected={i === index}
              onMouseEnter={() => setIndex(i)}
              onMouseDown={(e) => {
                e.preventDefault(); // 保住 textarea 焦点
                insertMention(r);
              }}
              className={cn(
                "flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-sm transition-colors",
                i === index ? "bg-accent/10 text-accent" : "text-ink-2 hover:bg-surface-2"
              )}
            >
              <span
                className={cn(
                  "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[9px] font-semibold ring-1",
                  r.type === "squad"
                    ? "bg-purple-500/15 text-purple-300 ring-purple-500/30"
                    : agentPalette(r.id)
                )}
              >
                {r.type === "squad" ? "队" : authorInitials(r.name)}
              </span>
              <span className="min-w-0 flex-1 truncate">{r.name}</span>
              <span className="ml-auto shrink-0 text-[10px] uppercase text-ink-3">
                {r.type}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// escapeRegExp：roster 名字进正则前先转义。
function escapeRegExp(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// highlightMentions：把输入框里的 roster @名字铺一层紫色背景。
// 与 textarea 字符数完全一致（不做替换、不加字），只加背景——padding /
// 文字色都会让两层错位或叠字（正文由 textarea 自己显示）。
function highlightMentions(
  text: string,
  roster: { name: string }[]
): React.ReactNode[] {
  if (!text) return [];
  const names = [...roster.map((r) => r.name)].sort((a, b) => b.length - a.length);
  if (names.length === 0) return [text];
  const re = new RegExp(`@(${names.map(escapeRegExp).join("|")})(?![\\w-])`, "g");
  const parts = text.split(re);
  // split 带捕获组：偶数下标是普通文本，奇数下标是匹配到的名字。
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      <span key={i} className="rounded-sm bg-purple-400/25">
        @{part}
      </span>
    ) : (
      part
    )
  );
}