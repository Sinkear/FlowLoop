"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  AtSign,
  Bot,
  Circle,
  MessageSquarePlus,
  Plus,
  Send,
  Sparkles,
  Square,
  Trash2,
  UsersRound,
  X,
} from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import { Button, ConfirmDialog } from "@/components/ui";
import { StewardSetup } from "@/components/steward-setup";
import { AcpChatClient, ChatEvent, PermissionRequest } from "@/lib/acp";
import { describeStewardFailure } from "@/lib/steward";
import { StreamCards } from "@/lib/run-messages";
import { useStickToBottom } from "@/lib/use-stick-to-bottom";
import {
  deleteChatConversation,
  getChatConversation,
  listChatConversations,
  listSkills,
  saveChatConversation,
} from "@/lib/api";
import { useAgents, useRuntimes } from "@/lib/queries";
import type { Agent, ChatConversationMeta, Runtime, Skill } from "@/lib/types";

// ── 对话工作台 ───────────────────────────────────────────────
//
// 一个对话 = 服务端持久化的一段记录 + 每个参与智能体各自的 ACP 会话。
// 支持在一个对话里 @ 多个智能体：每次发送会把「共享上下文」拼进每个被
// 叫到的智能体的 prompt，所以它们看到的是同一段讨论，而不是各聊各的。
//
// 存储结构（服务端，bug 2）：
//   GET /chat/conversations       [{id,title,participants,message_count,...}]
//   GET /chat/conversations/<id>  {..meta, messages:[...]}（打开时才拉整段）
//   PUT /chat/conversations/<id>  前端去抖保存（流式回复期间不逐字写）
// localStorage 只剩两件小事：flowloop-chat-active-v1（本浏览器上次打开哪段）
// 和旧版 flowloop-chat-index-v1 / conv key —— 首次访问自动导入服务端后删掉。
// 只存文本事件；工具卡片的输入输出已在事件里，加载历史后仍能展开。

type ChatMsg = ChatEvent & {
  id: number;
  ts: number;
  agentId?: string;
  agentName?: string;
};

type ConvMeta = {
  id: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  participants: string[];
  messageCount: number;
};

const ACTIVE_KEY = "flowloop-chat-active-v1";
const LEGACY_INDEX_KEY = "flowloop-chat-index-v1";
const LEGACY_IMPORTED_KEY = "flowloop-chat-imported-v1";
const legacyConvKey = (id: string) => `flowloop-chat-conv-v1-${id}`;
const MAX_CONVS = 30;
const MAX_MSGS = 300;
const NEW_TITLE = "新对话";

function readJSON<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : null;
  } catch {
    return null;
  }
}

function newID(): string {
  return `c-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

// 旧版 localStorage 索引/记录：只用于一次性导入服务端。
function readLegacyIndex(): ConvMeta[] {
  const list = readJSON<ConvMeta[]>(LEGACY_INDEX_KEY);
  if (!Array.isArray(list)) return [];
  return list.filter((m) => m && typeof m.id === "string").slice(0, MAX_CONVS);
}

function readLegacyConv(id: string): ChatMsg[] {
  const data = readJSON<{ messages?: ChatMsg[] }>(legacyConvKey(id));
  if (!data || !Array.isArray(data.messages)) return [];
  return data.messages.filter((m) => m && typeof m.text === "string");
}

function clearLegacyHistory(index: ConvMeta[]) {
  try {
    for (const m of index) localStorage.removeItem(legacyConvKey(m.id));
    localStorage.removeItem(LEGACY_INDEX_KEY);
  } catch {
    // 清理失败不影响服务端记录；下次启动会因 imported 标记跳过。
  }
}

// 旧版管家历史迁移：只迁移一次，迁完删旧 key，避免两套记录同时存在。
function migrateLegacy(): { meta: ConvMeta; messages: ChatMsg[] } | null {
  for (const key of ["flowloop-chat-history-steward", "flowloop-chat-history"]) {
    const old = readJSON<ChatEvent[]>(key);
    if (!Array.isArray(old) || !old.length) continue;
    const now = Date.now();
    const messages: ChatMsg[] = old
      .filter((m) => m && typeof m.text === "string")
      .map((m, i) => ({ ...m, id: i + 1, ts: now }));
    if (!messages.length) continue;
    const firstUser = messages.find((m) => m.kind === "user");
    const meta: ConvMeta = {
      id: newID(),
      title: (firstUser?.text ?? "以前的对话").replace(/\s+/g, " ").slice(0, 40),
      createdAt: now,
      updatedAt: now,
      participants: [],
      messageCount: messages.length,
    };
    try {
      localStorage.removeItem(key);
    } catch {}
    return { meta, messages };
  }
  return null;
}

// 服务端元数据 → 组件内的 camelCase 形状（时间保留 unix ms，比较/显示都用它）。
function toMeta(m: ChatConversationMeta): ConvMeta {
  return {
    id: m.id,
    title: m.title,
    createdAt: m.created_at,
    updatedAt: m.updated_at,
    participants: m.participants ?? [],
    messageCount: m.message_count,
  };
}

// 把一段对话写进服务端。失败静默：对话仍在本页可用，下一次保存会重试。
async function persistConversation(meta: ConvMeta, messages: ChatMsg[]): Promise<boolean> {
  try {
    await saveChatConversation(meta.id, {
      title: meta.title,
      participants: meta.participants,
      messages: messages.slice(-MAX_MSGS),
      created_at: meta.createdAt,
      updated_at: meta.updatedAt,
    });
    return true;
  } catch {
    return false;
  }
}

// 首次访问：把旧版本地记录整体搬到服务端；成功后才删本地副本并记标记。
async function importLegacyHistoryOnce() {
  try {
    if (localStorage.getItem(LEGACY_IMPORTED_KEY)) return;
  } catch {
    return; // SSR/隐私模式：没有旧记录可导，直接跳过。
  }
  const index = readLegacyIndex();
  const legacy = migrateLegacy();
  const items: { meta: ConvMeta; messages: ChatMsg[] }[] = [];
  for (const m of index) items.push({ meta: m, messages: readLegacyConv(m.id) });
  if (legacy) items.push(legacy);
  for (const it of items) {
    const ok = await persistConversation(it.meta, it.messages);
    if (!ok) return; // 服务端不可用：保留本地数据，下次启动重试（按 id upsert，不会重复）。
  }
  clearLegacyHistory(index);
  try {
    localStorage.setItem(LEGACY_IMPORTED_KEY, "1");
  } catch {}
}

const STARTERS = [
  "帮我创建一个任务：把 webhook 投递失败重试写成文档",
  "现在有哪些任务卡在待审批？",
  "看下最近一次 agent 执行失败的原因",
];

// 从 @ 光标位置解析出待补全的 query；只认行首或空白后的 @，
// 避免把邮箱、代码里的 @ 也弹成选择框。
function mentionAtCaret(value: string, caret: number): { start: number; query: string } | null {
  const upto = value.slice(0, caret);
  const m = upto.match(/(^|[\s(（])@([^\s@，。,.!?：:；;]{0,32})$/);
  if (!m) return null;
  return { start: upto.length - m[2].length - 1, query: m[2] };
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function mentionedAgents(text: string, agents: Agent[]): Agent[] {
  return agents.filter((a) => a.name && new RegExp(`@${escapeRe(a.name)}(?![\\w\\u4e00-\\u9fa5])`).test(text));
}

function truncate(s: string, n: number): string {
  const one = s.replace(/\s+/g, " ").trim();
  return one.length > n ? `${one.slice(0, n)}…` : one;
}

const MAX_CONTEXT_MSGS = 8;
const MAX_CONTEXT_CHARS = 420;

export function ChatWorkbench({ className }: { className?: string }) {
  const qc = useQueryClient();
  const { data: agentList } = useAgents();
  const { data: runtimeList } = useRuntimes();
  const agents = useMemo(() => agentList ?? [], [agentList]);
  const runtimes = runtimeList ?? [];

  const runtimeOf = useCallback(
    (agent: Agent): Runtime | undefined => runtimes.find((r) => r.id === agent.runtime_id),
    [runtimes]
  );
  const isOnline = useCallback(
    (agent: Agent) => runtimeOf(agent)?.status === "active",
    [runtimeOf]
  );

  const [index, setIndex] = useState<ConvMeta[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [messages, setMessages] = useState<ChatMsg[]>([]);
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const [hydrated, setHydrated] = useState(false);
  const [busyAgents, setBusyAgents] = useState<Set<string>>(new Set());
  const [connState, setConnState] = useState<Record<string, "connecting" | "ready" | "error">>({});
  const [permQueue, setPermQueue] = useState<
    { agent: Agent; req: PermissionRequest; respond: (o: { optionId?: string; cancelled?: boolean }) => void }[]
  >([]);
  const [mention, setMention] = useState<{ start: number; query: string } | null>(null);
  const [mentionIndex, setMentionIndex] = useState(0);
  const [pendingDelete, setPendingDelete] = useState<ConvMeta | null>(null);
  const [confirmClear, setConfirmClear] = useState(false);

  // 技能 chip：跟旧版一致，解释当前参与者的技能（读不到就只显示 id）。
  const [skillMeta, setSkillMeta] = useState<Record<string, Skill>>({});
  const [openSkill, setOpenSkill] = useState<string | null>(null);

  const msgID = useRef(1);
  // 服务端拉取期间禁止把尚未加载的空消息写回去（否则会清空服务端记录）。
  const loadingConv = useRef(false);
  // 正文已从服务端载入的对话 id：只有这些才有资格回写，避免把未加载的
  // 空 messages 覆盖掉服务端已有记录。
  const loadedIds = useRef<Set<string>>(new Set());
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const conns = useRef<
    Map<string, { client: AcpChatClient; promise: Promise<{ client: AcpChatClient; sessionId: string }> }>
  >(new Map());
  const busyRef = useRef(false);
  useEffect(() => { busyRef.current = busyAgents.size > 0; }, [busyAgents]);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const skillsBarRef = useRef<HTMLDivElement>(null);
  const scrollRef = useStickToBottom<HTMLDivElement>([messages, busyAgents.size]);

  const activeMeta = index.find((m) => m.id === activeId) ?? null;
  // 保存/切换回调里读最新值，避免把它们塞进依赖导致 effect 反复触发。
  const activeMetaRef = useRef<ConvMeta | null>(null);
  activeMetaRef.current = activeMeta;
  const activeIdRef = useRef<string | null>(null);
  activeIdRef.current = activeId;
  const indexRef = useRef<ConvMeta[]>([]);
  indexRef.current = index;
  const messagesRef = useRef<ChatMsg[]>([]);
  messagesRef.current = messages;
  const participants = useMemo(() => {
    const ids = activeMeta?.participants ?? [];
    return ids.map((id) => agents.find((a) => a.id === id)).filter((a): a is Agent => Boolean(a));
  }, [activeMeta, agents]);
  const participantIds = participants.map((a) => a.id);
  const busyCount = busyAgents.size;

  const patchMeta = useCallback((id: string, patch: (m: ConvMeta) => ConvMeta) => {
    const cur = id === activeMetaRef.current?.id
      ? activeMetaRef.current
      : indexRef.current.find((m) => m.id === id);
    if (!cur) return;
    const next = patch(cur);
    setIndex((prev) => prev.map((m) => (m.id === id ? next : m)));
    // 参与者的变化也要落库；只对当前打开的一段有完整消息可写。
    if (id === activeIdRef.current) void persistConversation(next, messagesRef.current);
  }, []);

  // ── 初始化：先从服务端拉列表（顺带一次性导入旧 localStorage 记录），
  //    再打开上次那段（或最新一段）。没有对话时等 agent 列表到了再建。 ──
  useEffect(() => {
    let cancelled = false;
    loadingConv.current = true;
    (async () => {
      await importLegacyHistoryOnce();
      let list: ConvMeta[] = [];
      try {
        const res = await listChatConversations();
        list = (res.conversations ?? []).map(toMeta).slice(0, MAX_CONVS);
      } catch {
        // 服务端不可用：保持空历史，不清任何本地数据（下次启动重试导入）。
      }
      if (cancelled) return;
      setIndex(list);
      const last = (() => {
        try { return localStorage.getItem(ACTIVE_KEY); } catch { return null; }
      })();
      const chosen = list.find((m) => m.id === last) ?? list[0] ?? null;
      if (chosen) {
        setActiveId(chosen.id);
        try {
          const conv = await getChatConversation(chosen.id);
          if (cancelled) return;
          const msgs = ((conv.messages ?? []) as ChatMsg[]).filter((m) => m && typeof m.text === "string");
          setMessages(msgs);
          loadedIds.current.add(chosen.id);
          msgID.current = Math.max(0, ...msgs.map((m) => m.id)) + 1;
        } catch {
          // 列表有元数据但正文拉取失败：先留空，稍后切走再切回来会重试。
        }
      }
      loadingConv.current = false;
      setHydrated(true);
    })();
    return () => { cancelled = true; };
  }, []);

  // 技能库只拉一次。
  const skillFetched = useRef(false);
  useEffect(() => {
    if (skillFetched.current) return;
    skillFetched.current = true;
    listSkills()
      .then((list) => {
        const map: Record<string, Skill> = {};
        for (const s of list) map[s.id] = s;
        setSkillMeta(map);
      })
      .catch(() => {});
  }, []);

  const createConversation = useCallback(
    (participantList: string[], title = NEW_TITLE): ConvMeta => {
      // 切到新对话前先把上一段未去抖的修改写掉，否则最后几条消息会丢。
      if (saveTimer.current) {
        clearTimeout(saveTimer.current);
        saveTimer.current = null;
      }
      const prevMeta = activeMetaRef.current;
      if (prevMeta && loadedIds.current.has(prevMeta.id)) {
        void persistConversation(prevMeta, messagesRef.current);
      }
      const now = Date.now();
      const meta: ConvMeta = {
        id: newID(),
        title,
        createdAt: now,
        updatedAt: now,
        participants: participantList,
        messageCount: 0,
      };
      setIndex((prev) => [meta, ...prev].slice(0, MAX_CONVS));
      // 先落一行空对话：刷新页面/换浏览器后它也要在列表里。
      void persistConversation(meta, []);
      loadedIds.current.add(meta.id);
      loadingConv.current = false;
      setActiveId(meta.id);
      try { localStorage.setItem(ACTIVE_KEY, meta.id); } catch {}
      setMessages([]);
      msgID.current = 1;
      setInput("");
      setError("");
      setBusyAgents(new Set());
      return meta;
    },
    []
  );

  // 首次拿到 agent 列表：没有对话就建一段，参与者默认管家（否则第一个在线 agent）。
  useEffect(() => {
    if (!hydrated || !agents.length || index.length) return;
    const primary = agents.find((a) => a.type === "steward") ?? agents.find(isOnline) ?? agents[0];
    createConversation(primary ? [primary.id] : []);
  }, [hydrated, agents, index.length, createConversation, isOnline]);

  // 迁移来的对话没有参与者：补上默认对象。
  useEffect(() => {
    if (!hydrated || !activeMeta || activeMeta.participants.length || !agents.length) return;
    const primary = agents.find((a) => a.type === "steward") ?? agents.find(isOnline) ?? agents[0];
    if (!primary) return;
    patchMeta(activeMeta.id, (m) => ({ ...m, participants: [primary.id] }));
  }, [hydrated, activeMeta, agents, patchMeta, isOnline]);

  useEffect(() => {
    try { if (activeId) localStorage.setItem(ACTIVE_KEY, activeId); } catch {}
  }, [activeId]);

  // 持久化当前对话到服务端。流式回复期间每条事件都会改 messages，所以
  // 去抖 900ms：一次回复结束后只写一次，避免把存库打成每秒几十次。
  useEffect(() => {
    if (!hydrated || !activeId || loadingConv.current) return;
    const meta = activeMetaRef.current;
    if (!meta) return;
    // 正文尚未载入的对话不能回写（会把服务端记录清空）。
    if (!loadedIds.current.has(activeId)) return;
    const firstUser = messages.find((m) => m.kind === "user" && m.text.trim());
    const next: ConvMeta = {
      ...meta,
      messageCount: messages.length,
      updatedAt: Date.now(),
      title: meta.title === NEW_TITLE && firstUser ? truncate(firstUser.text, 40) : meta.title,
    };
    setIndex((prev) => prev.map((m) => (m.id === activeId ? next : m)));
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      saveTimer.current = null;
      const cur = activeMetaRef.current;
      if (cur && cur.id === activeIdRef.current) void persistConversation(cur, messagesRef.current);
    }, 900);
  }, [messages, activeId, hydrated]);

  // 卸载/切页：把最后一次未去抖到期的修改补写一次。
  useEffect(() => () => {
    if (saveTimer.current) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    const cur = activeMetaRef.current;
    if (cur && messagesRef.current.length) void persistConversation(cur, messagesRef.current);
  }, []);

  // 切对话 / 退出：取消在途回复并断开所有 ACP 连接，记录保留在 localStorage。
  const disconnectAll = useCallback(() => {
    for (const [, conn] of conns.current) {
      try { conn.client.close(); } catch {}
    }
    conns.current.clear();
    setConnState({});
    setBusyAgents(new Set());
  }, []);

  useEffect(() => () => {
    for (const [, conn] of conns.current) {
      try { conn.client.close(); } catch {}
    }
  }, []);

  // 打开某一段：正文按需从服务端拉（列表里只有元数据）。
  const loadConversation = useCallback((id: string) => {
    loadingConv.current = true;
    setActiveId(id);
    setMessages([]);
    msgID.current = 1;
    setInput("");
    setError("");
    setPermQueue([]);
    setOpenSkill(null);
    getChatConversation(id)
      .then((conv) => {
        if (activeIdRef.current !== id) return;
        const msgs = ((conv.messages ?? []) as ChatMsg[]).filter((m) => m && typeof m.text === "string");
        setMessages(msgs);
        loadedIds.current.add(id);
        msgID.current = Math.max(0, ...msgs.map((m) => m.id)) + 1;
      })
      .catch(() => {})
      .finally(() => {
        if (activeIdRef.current === id) loadingConv.current = false;
      });
  }, []);

  // 切走前先把上一段未到期的修改写掉，避免丢最后一轮。
  const flushPendingSave = useCallback(() => {
    if (saveTimer.current) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    const cur = activeMetaRef.current;
    if (cur) void persistConversation(cur, messagesRef.current);
  }, []);

  const openConversation = useCallback(
    (id: string) => {
      if (id === activeIdRef.current) return;
      flushPendingSave();
      disconnectAll();
      loadConversation(id);
    },
    [disconnectAll, flushPendingSave, loadConversation]
  );

  const deleteConversation = useCallback(
    (id: string) => {
      void deleteChatConversation(id).catch(() => {});
      loadedIds.current.delete(id);
      setIndex((prev) => prev.filter((m) => m.id !== id));
      if (id === activeIdRef.current) {
        disconnectAll();
        if (saveTimer.current) {
          clearTimeout(saveTimer.current);
          saveTimer.current = null;
        }
        const rest = indexRef.current.filter((m) => m.id !== id);
        const nextConv = rest[0];
        if (nextConv) {
          loadConversation(nextConv.id);
        } else {
          loadingConv.current = false;
          setActiveId(null);
          setMessages([]);
          const primary = agents.find((a) => a.type === "steward") ?? agents.find(isOnline) ?? agents[0];
          createConversation(primary ? [primary.id] : []);
        }
      }
    },
    [disconnectAll, loadConversation, agents, isOnline, createConversation]
  );

  const handleNewChat = useCallback(() => {
    const primary = participants[0] ?? agents.find((a) => a.type === "steward") ?? agents.find(isOnline) ?? agents[0];
    if (activeMeta && messages.length === 0) {
      // 空对话不叠加：就地重置，避免历史栏里堆一串“新对话”。
      setInput("");
      setError("");
      if (!activeMeta.participants.length && primary) {
        patchMeta(activeMeta.id, (m) => ({ ...m, participants: [primary.id] }));
      }
      inputRef.current?.focus();
      return;
    }
    disconnectAll();
    createConversation(primary ? [primary.id] : []);
    inputRef.current?.focus();
  }, [participants, agents, isOnline, activeMeta, messages.length, patchMeta, disconnectAll, createConversation]);

  // ── ACP：每个参与者一条连接 + 一个 CLI 侧会话 ──
  const appendEvent = useCallback((agent: Agent, ev: ChatEvent) => {
    if (ev.kind === "user") return; // 用户消息在发送时入列，不让回显重复
    msgID.current += 1;
    const next: ChatMsg = {
      ...ev,
      id: msgID.current,
      ts: Date.now(),
      agentId: agent.id,
      agentName: agent.name,
    };
    setMessages((prev) => {
      const last = prev[prev.length - 1];
      if (last && last.kind === next.kind && last.agentId === agent.id && (ev.kind === "agent" || ev.kind === "thought")) {
        return [...prev.slice(0, -1), { ...last, text: last.text + ev.text }];
      }
      if (ev.kind === "tool" && ev.isResult && ev.callId) {
        const idx = prev.findLastIndex(
          (m) => m.kind === "tool" && m.callId === ev.callId && m.agentId === agent.id && !m.isResult
        );
        if (idx >= 0) {
          const copy = [...prev];
          copy[idx] = { ...copy[idx], output: ev.output, isResult: true };
          return copy;
        }
      }
      return [...prev, next];
    });
  }, []);

  const ensureSession = useCallback(
    (agent: Agent): Promise<{ client: AcpChatClient; sessionId: string }> => {
      const existing = conns.current.get(agent.id);
      if (existing) return existing.promise;

      setConnState((s) => ({ ...s, [agent.id]: "connecting" }));
      const client = new AcpChatClient({
        onEvent: (ev) => appendEvent(agent, ev),
        onPermissionRequest: (req, respond) =>
          setPermQueue((q) => [...q, { agent, req, respond }]),
        onClose: (info) => {
          const cur = conns.current.get(agent.id);
          if (!cur || cur.client !== client) return;
          conns.current.delete(agent.id);
          setConnState((s) => ({ ...s, [agent.id]: "error" }));
          setBusyAgents((prev) => {
            if (!prev.has(agent.id)) return prev;
            const next = new Set(prev);
            next.delete(agent.id);
            return next;
          });
          if (busyRef.current) {
            setError(`${agent.name} 连接已断开${info ? `（${info.code}${info.reason ? ` ${info.reason}` : ""}）` : ""}`);
          }
        },
      });
      const host = window.location.host;
      const promise = (async () => {
        await client.connect(
          `ws${window.location.protocol === "https:" ? "s" : ""}://${host}/backend/agents/${agent.id}/acp`
        );
        const sessionId = await client.newSession();
        // 智能体配了模型就尝试切到该模型；CLI 不支持 session/set_model 时
        // 保持它自己的默认模型，不影响这一轮对话。
        if (agent.model) {
          try {
            await client.setModel(sessionId, agent.model);
          } catch {
            /* 不支持模型切换的 CLI：忽略 */
          }
        }
        setConnState((s) => ({ ...s, [agent.id]: "ready" }));
        return { client, sessionId };
      })();
      conns.current.set(agent.id, { client, promise });
      promise.catch(async (e: Error) => {
        const cur = conns.current.get(agent.id);
        if (cur?.client !== client) return;
        conns.current.delete(agent.id);
        setConnState((s) => ({ ...s, [agent.id]: "error" }));
        let why = e.message;
        if (agent.type === "steward") {
          const hint = await describeStewardFailure(agent.id).catch(() => "");
          if (hint) why = `${why}　${hint}`;
        }
        setError(`${agent.name} 连接失败：${why}`);
      });
      return promise;
    },
    [appendEvent]
  );

  const stopAll = useCallback(() => {
    for (const id of busyAgents) {
      const conn = conns.current.get(id);
      if (!conn) continue;
      void conn.promise
        .then(({ client, sessionId }) => client.cancel(sessionId))
        .catch(() => {});
    }
  }, [busyAgents]);

  // ── 发送：解析 @ 目标 → 共享上下文 → 并发投递 ──
  const send = useCallback(
    async (textOverride?: string) => {
      const text = (textOverride ?? input).trim();
      if (!text || busyCount) return;
      if (!agents.length) return;

      const online = (a: Agent) => isOnline(a);
      const mentioned = mentionedAgents(text, agents);
      const unavailable = mentioned.filter((a) => !online(a));
      let targets = mentioned.filter(online);
      if (!targets.length) {
        const fallback =
          participants.find(online) ??
          agents.find((a) => a.type === "steward" && online(a)) ??
          agents.find(online);
        if (fallback) targets = [fallback];
      }
      if (!targets.length) {
        setError(unavailable.length ? `${unavailable.map((a) => a.name).join("、")} 的运行时不在线` : "没有在线智能体");
        return;
      }

      setError(
        unavailable.length ? `${unavailable.map((a) => a.name).join("、")} 不在线，本轮只发给在线智能体` : ""
      );

      // 新叫到的智能体加入本对话参与者，后续轮次默认仍由被 @ 的人回答。
      const nextParticipants = Array.from(new Set([...participantIds, ...targets.map((a) => a.id)]));
      if (activeId) patchMeta(activeId, (m) => ({ ...m, participants: nextParticipants }));

      const prior = messages;
      msgID.current += 1;
      const userMsg: ChatMsg = { kind: "user", text, id: msgID.current, ts: Date.now() };
      setMessages((prev) => [...prev, userMsg]);
      setInput("");
      setMention(null);
      setBusyAgents(new Set(targets.map((a) => a.id)));

      const roster = nextParticipants
        .map((id) => agents.find((a) => a.id === id))
        .filter((a): a is Agent => Boolean(a));
      const context = prior
        .filter((m) => (m.kind === "user" || m.kind === "agent") && m.text.trim())
        .slice(-MAX_CONTEXT_MSGS)
        .map((m) => `${m.kind === "user" ? "用户" : `@${m.agentName ?? "agent"}`}：${truncate(m.text, MAX_CONTEXT_CHARS)}`)
        .join("\n");

      const promptFor = (agent: Agent) =>
        [
          "【共享对话上下文】",
          `参与者：${roster.map((a) => `@${a.name}`).join("、") || `@${agent.name}`}`,
          roster.length > 1 ? "这是一个多智能体对话。请只以你自己的身份回应，不要替其他智能体或用户发言。" : "",
          context ? `最近对话：\n${context}` : "",
          "【本轮用户消息】",
          text,
        ]
          .filter(Boolean)
          .join("\n\n");

      await Promise.all(
        targets.map(async (agent) => {
          try {
            const { client, sessionId } = await ensureSession(agent);
            await client.prompt(sessionId, promptFor(agent));
          } catch (e) {
            if (conns.current.get(agent.id)) {
              setError(`${agent.name} 发送失败：${(e as Error).message}`);
            }
          } finally {
            setBusyAgents((prev) => {
              if (!prev.has(agent.id)) return prev;
              const next = new Set(prev);
              next.delete(agent.id);
              return next;
            });
          }
        })
      );
    },
    [
      input,
      busyCount,
      agents,
      isOnline,
      participants,
      participantIds,
      activeId,
      patchMeta,
      messages,
      ensureSession,
    ]
  );

  // 参与者增删：右侧面板点击即可加入/移出；加入时顺手把 @name 填进输入框。
  const toggleParticipant = useCallback(
    (agent: Agent) => {
      if (!activeMeta) return;
      const has = participantIds.includes(agent.id);
      if (has) {
        if (busyAgents.has(agent.id)) {
          const conn = conns.current.get(agent.id);
          if (conn) void conn.promise.then(({ client, sessionId }) => client.cancel(sessionId)).catch(() => {});
        }
        patchMeta(activeMeta.id, (m) => ({ ...m, participants: m.participants.filter((id) => id !== agent.id) }));
        return;
      }
      patchMeta(activeMeta.id, (m) => ({ ...m, participants: [...m.participants, agent.id] }));
      setInput((cur) => (cur.trim() ? `${cur.trimEnd()} @${agent.name} ` : `@${agent.name} `));
      inputRef.current?.focus();
    },
    [activeMeta, participantIds, busyAgents, patchMeta]
  );

  // 技能元数据 → 参与者集合去重后的 chip。
  const loadedSkills = useMemo(() => {
    const ids = Array.from(new Set(participants.flatMap((a) => a.skills ?? [])));
    return ids.map((id) => {
      const meta = skillMeta[id];
      return {
        id,
        label: meta ? meta.title || meta.name : id.slice(0, 8),
        description: meta?.description ?? "",
        builtin: Boolean(meta?.builtin),
      };
    });
  }, [participants, skillMeta]);

  useEffect(() => {
    if (!openSkill) return;
    const onDown = (e: MouseEvent) => {
      if (skillsBarRef.current && !skillsBarRef.current.contains(e.target as Node)) setOpenSkill(null);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [openSkill]);

  // 输入框 @ 补全：随输入实时过滤，↑↓ / Enter / Tab 都可选。
  const mentionCandidates = useMemo(() => {
    if (!mention) return [];
    const q = mention.query.toLowerCase();
    return agents
      .filter((a) => !q || a.name.toLowerCase().includes(q) || (a.model ?? "").toLowerCase().includes(q))
      .sort((a, b) => Number(isOnline(b)) - Number(isOnline(a)) || a.name.localeCompare(b.name))
      .slice(0, 8);
  }, [mention, agents, isOnline]);

  useEffect(() => { setMentionIndex(0); }, [mention?.query]);

  const refreshMention = (value: string, caret: number) => {
    setMention(mentionAtCaret(value, caret));
  };

  const chooseMention = (agent: Agent) => {
    const el = inputRef.current;
    if (!el || !mention) return;
    const caret = el.selectionStart ?? input.length;
    const before = input.slice(0, mention.start);
    const after = input.slice(caret);
    const next = `${before}@${agent.name} ${after}`;
    setInput(next);
    setMention(null);
    if (activeMeta && !participantIds.includes(agent.id)) {
      patchMeta(activeMeta.id, (m) => ({ ...m, participants: [...m.participants, agent.id] }));
    }
    requestAnimationFrame(() => {
      el.focus();
      const pos = before.length + agent.name.length + 2;
      el.setSelectionRange(pos, pos);
    });
  };

  const currentPerm = permQueue[0] ?? null;
  const anyBusy = busyCount > 0;
  const onlineCount = agents.filter(isOnline).length;

  const connLabel = (agent: Agent) => {
    const st = connState[agent.id];
    if (st === "ready") return "已连接";
    if (st === "connecting") return "连接中…";
    if (st === "error") return "连接失败";
    return isOnline(agent) ? "待连接" : "离线";
  };

  return (
    <div className={cn("flex min-h-0 flex-1 gap-3", className)}>
      {/* 左：本机对话历史。每条只留“一行标题 + 一行元信息”，不抢主栏注意力。 */}
      <aside className="hidden w-[218px] shrink-0 flex-col overflow-hidden rounded-lg border border-line bg-surface-1 lg:flex">
        <div className="flex items-center justify-between border-b border-line px-3 py-2">
          <span className="text-[11px] font-medium tracking-wide text-ink-3">历史对话</span>
          <button
            type="button"
            onClick={handleNewChat}
            title="新建对话"
            aria-label="新建对话"
            className="flex h-7 w-7 items-center justify-center rounded-md border border-line text-ink-2 transition-colors hover:border-accent/40 hover:text-accent"
          >
            <MessageSquarePlus className="h-3.5 w-3.5" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {index.length === 0 && <p className="px-2 py-3 text-[11px] text-ink-3">还没有对话。</p>}
          <div className="flex flex-col gap-0.5">
            {index.map((m) => {
              const active = m.id === activeId;
              const group = m.participants
                .map((id) => agents.find((a) => a.id === id)?.name)
                .filter(Boolean)
                .slice(0, 3);
              return (
                <div
                  key={m.id}
                  className={cn(
                    "group relative rounded-md transition-colors",
                    active ? "bg-surface-3" : "hover:bg-surface-2/70"
                  )}
                >
                  <button
                    type="button"
                    onClick={() => openConversation(m.id)}
                    className="block w-full px-2 py-2 pr-7 text-left"
                    aria-current={active ? "true" : undefined}
                  >
                    <span className="flex min-w-0 items-center gap-1.5">
                      {active && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />}
                      <span className={cn("min-w-0 flex-1 truncate text-xs", active ? "font-medium text-ink" : "text-ink-2")}>
                        {m.title || NEW_TITLE}
                      </span>
                    </span>
                    <span className="mt-0.5 block truncate text-[10px] text-ink-3">
                      {group.length ? group.map((n) => `@${n}`).join(" ") : "无参与者"}
                      {" · "}
                      {m.messageCount} 条 · {relTime(new Date(m.updatedAt).toISOString())}
                    </span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setPendingDelete(m)}
                    title="删除这段对话"
                    aria-label={`删除对话 ${m.title}`}
                    className="absolute right-1 top-1.5 rounded p-1 text-ink-3/70 transition-colors hover:bg-surface-0 hover:text-red-400 focus-visible:text-red-400"
                  >
                    <Trash2 className="h-3 w-3" />
                  </button>
                </div>
              );
            })}
          </div>
        </div>
        <p className="shrink-0 border-t border-line px-3 py-2 text-[10px] text-ink-3">
          记录保存在服务端 · 换浏览器也能看到 · 最近 {MAX_CONVS} 段
        </p>
      </aside>

      {/* 中：对话主体 */}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-lg border border-line bg-surface-1">
        <header className="flex items-center gap-2 border-b border-line px-4 py-2.5">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", anyBusy ? "bg-accent animate-pulse" : "bg-accent/60")} />
          <span className="min-w-0 truncate text-sm font-medium text-ink">{activeMeta?.title || NEW_TITLE}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">
            ACP · {participants.length} 个智能体
          </span>
          <div className="ml-auto flex shrink-0 items-center gap-0.5">
            <button
              type="button"
              onClick={handleNewChat}
              className="flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-ink-3 transition-colors hover:bg-surface-2 hover:text-ink"
              title="新建一段对话（历史里空的对话会被替换）"
            >
              <MessageSquarePlus className="h-3 w-3" /> 新建
            </button>
            {messages.length > 0 && (
              <button
                type="button"
                onClick={() => setConfirmClear(true)}
                className="flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400"
                title="清空这段对话的显示记录（不影响各 CLI 侧会话）"
              >
                <Trash2 className="h-3 w-3" /> 清空
              </button>
            )}
          </div>
        </header>

        <div ref={scrollRef} className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4">
          {!agents.length && (
            <StewardSetup
              className="mx-auto my-8 max-w-md"
              onCreated={() => qc.invalidateQueries({ queryKey: ["agents"] })}
            />
          )}

          {agents.length > 0 && messages.length === 0 && (
            <div className="py-6">
              <p className="text-sm text-ink-2">
                一个对话框，可以叫上多个智能体一起讨论。输入 <kbd className="rounded border border-line bg-surface-2 px-1 font-mono text-[11px]">@</kbd> 选择对象；
                不 @ 时默认由管家回应。
              </p>
              <div className="mt-3 flex flex-col items-start gap-1.5">
                {STARTERS.map((s) => (
                  <button
                    key={s}
                    type="button"
                    onClick={() => void send(s)}
                    disabled={anyBusy || !agents.length}
                    className="rounded-md border border-line bg-surface-2 px-2.5 py-1.5 text-left text-xs text-ink-2 transition-colors hover:border-accent/40 hover:text-ink disabled:opacity-50"
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>
          )}

          {messages.map((m, i) => {
            const prev = messages[i - 1];
            if (m.kind === "thought") {
              return (
                <div key={m.id}>
                  <AgentLine agentName={m.agentName} show={!prev || prev.kind === "thought" ? false : true} />
                  <StreamCards items={[{ kind: "thought", key: String(m.id), content: m.text, isStreaming: anyBusy && i === messages.length - 1 }]} />
                </div>
              );
            }
            if (m.kind === "tool") {
              return (
                <div key={m.id}>
                  <AgentLine agentName={m.agentName} show={!prev || prev.kind === "thought" || (prev.kind === "tool" && prev.agentId !== m.agentId)} />
                  <StreamCards
                    items={[{
                      kind: "tool",
                      key: m.callId ?? String(m.id),
                      toolName: m.toolName ?? "tool",
                      input: "",
                      output: m.output ?? "",
                      hasResult: !!m.isResult,
                    }]}
                  />
                </div>
              );
            }
            if (m.kind === "user") {
              return (
                <div key={m.id} className="flex justify-end">
                  <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-lg bg-accent px-3 py-2 text-sm text-accent-ink">
                    {m.text}
                  </div>
                </div>
              );
            }
            return (
              <div key={m.id} className="flex flex-col items-start">
                <AgentLine agentName={m.agentName} show={!prev || prev.kind !== "agent" || prev.agentId !== m.agentId} />
                <div className="max-w-[88%] whitespace-pre-wrap break-words rounded-lg bg-surface-2 px-3 py-2 text-sm text-ink">
                  {m.text}
                </div>
              </div>
            );
          })}

          {anyBusy && (
            <p className="text-xs text-ink-3">
              正在回复：{Array.from(busyAgents).map((id) => agents.find((a) => a.id === id)?.name).filter(Boolean).join("、")}…
            </p>
          )}
          {error && <p className="text-xs text-red-400">{error}</p>}
        </div>

        {/* 参与者栏：当前这段对话里被叫到的人，一眼能看出“谁在场”。 */}
        {participants.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-line bg-surface-0/40 px-4 py-1.5">
            <span className="flex items-center gap-1 text-[11px] text-ink-3">
              <UsersRound className="h-3 w-3" /> 参与者
            </span>
            {participants.map((a) => (
              <span
                key={a.id}
                className={cn(
                  "flex max-w-[180px] items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px]",
                  isOnline(a) ? "border-line bg-surface-2 text-ink-2" : "border-line bg-surface-2/50 text-ink-3"
                )}
                title={`${a.name} · ${a.model || "运行时默认"} · ${connLabel(a)}`}
              >
                <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", isOnline(a) ? "bg-accent" : "bg-zinc-500")} />
                <span className="truncate">@{a.name}</span>
                <button
                  type="button"
                  onClick={() => toggleParticipant(a)}
                  className="ml-0.5 rounded p-0.5 text-ink-3 transition-colors hover:text-red-400"
                  aria-label={`把 ${a.name} 移出本轮对话`}
                >
                  <X className="h-2.5 w-2.5" />
                </button>
              </span>
            ))}
          </div>
        )}

        {/* 已加载技能：保留旧版的功能栏，让“为什么它会这样做”可见。 */}
        {loadedSkills.length > 0 && (
          <div ref={skillsBarRef} className="flex items-center gap-2 border-t border-line bg-surface-0/40 px-4 py-1.5">
            <span className="shrink-0 text-[11px] text-ink-3">已加载技能</span>
            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
              {loadedSkills.map((sk) => (
                <div key={sk.id} className="relative">
                  <button
                    type="button"
                    onClick={() => setOpenSkill((cur) => (cur === sk.id ? null : sk.id))}
                    aria-expanded={openSkill === sk.id}
                    title={sk.description || sk.label}
                    className={cn(
                      "flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] transition-colors",
                      openSkill === sk.id
                        ? "border-accent/50 bg-accent/10 text-accent"
                        : "border-line bg-surface-2 text-ink-2 hover:border-accent/40 hover:text-ink"
                    )}
                  >
                    <Sparkles className="h-3 w-3 shrink-0" />
                    <span className="max-w-[160px] truncate">{sk.label}</span>
                  </button>
                  {openSkill === sk.id && (
                    <div className="absolute bottom-full left-0 z-30 mb-1.5 w-64 rounded-lg border border-line bg-surface-1 p-3 shadow-xl shadow-black/30">
                      <div className="flex items-center gap-1.5">
                        <Sparkles className="h-3.5 w-3.5 shrink-0 text-accent" />
                        <span className="min-w-0 flex-1 truncate text-xs font-medium text-ink">{sk.label}</span>
                        {sk.builtin && (
                          <span className="shrink-0 rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                            内置
                          </span>
                        )}
                      </div>
                      <p className="mt-1.5 text-[11px] leading-relaxed text-ink-2">{sk.description || "（无描述）"}</p>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 输入区：@ 补全浮层贴在上方，键盘全程可操作。 */}
        <div className="relative border-t border-line bg-surface-0/40 px-4 py-3">
          {mention && (
            <div
              role="listbox"
              aria-label="选择智能体"
              className="absolute bottom-full left-4 right-4 z-40 mb-1 max-h-64 overflow-y-auto rounded-lg border border-line bg-surface-1 p-1 shadow-xl shadow-black/40"
            >
              <div className="px-2 py-1 text-[10px] text-ink-3">↑↓ 选择 · Enter 确认 · Esc 取消</div>
              {mentionCandidates.length === 0 && (
                <div className="px-2 py-2 text-xs text-ink-3">没有匹配的智能体</div>
              )}
              {mentionCandidates.map((a, i) => {
                const online = isOnline(a);
                return (
                  <button
                    key={a.id}
                    type="button"
                    role="option"
                    aria-selected={i === mentionIndex}
                    onMouseEnter={() => setMentionIndex(i)}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => chooseMention(a)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors",
                      i === mentionIndex ? "bg-accent/10" : "hover:bg-surface-2"
                    )}
                  >
                    <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", online ? "bg-accent" : "bg-zinc-500")} />
                    <span className="min-w-0 flex-1 truncate text-xs font-medium text-ink">{a.name}</span>
                    <span className="max-w-[110px] shrink-0 truncate font-mono text-[10px] text-ink-3">
                      {a.model || "运行时默认"}
                    </span>
                    {!online && <span className="shrink-0 text-[10px] text-amber-400">离线</span>}
                  </button>
                );
              })}
            </div>
          )}
          <div className="flex items-end gap-2">
            <textarea
              ref={inputRef}
              rows={2}
              className="min-h-[46px] flex-1 resize-none rounded-md border border-line-strong bg-surface-2 px-3 py-2 text-sm text-ink outline-none transition-colors placeholder:text-ink-3 focus:border-accent-dim focus:ring-2 focus:ring-accent/15 disabled:opacity-50"
              placeholder={agents.length ? "说点什么…  输入 @ 叫上多个智能体，Enter 发送，Shift+Enter 换行" : "还没有可对话的智能体"}
              value={input}
              onChange={(e) => {
                const el = e.currentTarget;
                setInput(el.value);
                refreshMention(el.value, el.selectionStart ?? el.value.length);
              }}
              onClick={(e) => refreshMention(e.currentTarget.value, e.currentTarget.selectionStart ?? 0)}
              onBlur={() => setMention(null)}
              onKeyDown={(e) => {
                if (mention && mentionCandidates.length) {
                  if (e.key === "ArrowDown") {
                    e.preventDefault();
                    setMentionIndex((i) => (i + 1) % mentionCandidates.length);
                    return;
                  }
                  if (e.key === "ArrowUp") {
                    e.preventDefault();
                    setMentionIndex((i) => (i - 1 + mentionCandidates.length) % mentionCandidates.length);
                    return;
                  }
                  if (e.key === "Enter" || e.key === "Tab") {
                    e.preventDefault();
                    chooseMention(mentionCandidates[mentionIndex] ?? mentionCandidates[0]);
                    return;
                  }
                  if (e.key === "Escape") {
                    e.preventDefault();
                    setMention(null);
                    return;
                  }
                }
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  void send();
                }
              }}
              disabled={!agents.length}
            />
            {anyBusy ? (
              <Button variant="danger" onClick={stopAll} className="gap-1.5" title="中断这一轮所有回复，会话与记录保留">
                <Square className="h-3.5 w-3.5 fill-current" /> 停止
              </Button>
            ) : (
              <Button onClick={() => void send()} disabled={!input.trim() || !agents.length} className="gap-1.5">
                <Send className="h-3.5 w-3.5" /> 发送
              </Button>
            )}
          </div>
          <div className="mt-1.5 flex items-center gap-2 text-[10px] text-ink-3">
            <AtSign className="h-3 w-3" />
            <span>{participants.length > 1 ? "多智能体共享同一段讨论上下文" : "叫上更多智能体即可开始多人讨论"}</span>
            {participants[0] && (
              <span className="ml-auto font-mono">
                {participants[0].name} · {participants[0].model || "运行时默认"}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 右：在线智能体。点一下加入/移出当前对话。 */}
      <aside className="hidden w-[246px] shrink-0 flex-col overflow-hidden rounded-lg border border-line bg-surface-1 min-[1200px]:flex">
        <div className="flex items-center justify-between border-b border-line px-3 py-2">
          <span className="flex items-center gap-1.5 text-[11px] font-medium tracking-wide text-ink-3">
            <Bot className="h-3.5 w-3.5" /> 智能体
          </span>
          <span className="font-mono text-[10px] text-ink-3">{onlineCount}/{agents.length} 在线</span>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {agents.length === 0 && <p className="px-2 py-3 text-[11px] text-ink-3">还没有智能体。</p>}
          {agents.map((a) => {
            const online = isOnline(a);
            const joined = participantIds.includes(a.id);
            return (
              <button
                key={a.id}
                type="button"
                onClick={() => toggleParticipant(a)}
                title={online ? `${joined ? "移出" : "加入"}对话 · ${connLabel(a)}` : "运行时离线，暂不可对话"}
                aria-pressed={joined}
                className={cn(
                  "flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors",
                  joined ? "bg-surface-3" : "hover:bg-surface-2/70"
                )}
              >
                <span className="relative mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line bg-surface-2 font-mono text-[10px] text-ink-2">
                  {a.name.slice(0, 1).toUpperCase()}
                  <span
                    className={cn(
                      "absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-2 ring-surface-1",
                      online ? "bg-accent" : "bg-zinc-500"
                    )}
                  />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1">
                    <span className={cn("min-w-0 flex-1 truncate text-xs", joined ? "font-medium text-ink" : "text-ink-2")}>
                      {a.name}
                    </span>
                    {a.type === "steward" && (
                      <span className="shrink-0 rounded border border-accent/30 px-1 text-[9px] text-accent">管家</span>
                    )}
                    {!online && <span className="shrink-0 text-[9px] text-amber-400">离线</span>}
                  </span>
                  <span className="mt-0.5 block truncate font-mono text-[10px] text-ink-3">
                    {a.model || "运行时默认"}
                  </span>
                </span>
                {joined ? (
                  <span className="mt-0.5 shrink-0 text-[10px] text-accent">已加入</span>
                ) : (
                  <Plus className="mt-0.5 h-3 w-3 shrink-0 text-ink-3" />
                )}
              </button>
            );
          })}
        </div>
        <p className="shrink-0 border-t border-line px-3 py-2 text-[10px] leading-relaxed text-ink-3">
          点击加入讨论；加入后输入框会自动补一个 @。离线智能体需要先让机器上线。
        </p>
      </aside>

      {pendingDelete && (
        <ConfirmDialog
          title="删除这段对话？"
          message={`「${pendingDelete.title || NEW_TITLE}」的记录只存在本机，删除后无法恢复。`}
          onConfirm={() => {
            deleteConversation(pendingDelete.id);
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}

      {confirmClear && (
        <ConfirmDialog
          title="清空这段对话？"
          message="会清掉服务端保存的显示记录（所有浏览器都看不到了）；各 CLI 侧的真实会话不受影响，但聊天记录无法恢复。"
          onConfirm={() => {
            setMessages([]);
            setError("");
            setConfirmClear(false);
          }}
          onClose={() => setConfirmClear(false)}
        />
      )}

      {currentPerm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <div className="w-96 rounded-xl border border-line bg-surface-1 p-4 shadow-xl shadow-black/30">
            <p className="text-sm font-medium text-ink">{currentPerm.agent.name} 请求权限</p>
            <p className="mt-1 text-xs text-ink-3">
              {currentPerm.req.toolCall?.title || "tool"}（{currentPerm.req.toolCall?.kind || "unknown"}）
            </p>
            {permQueue.length > 1 && (
              <p className="mt-1 text-[10px] text-ink-3">还有 {permQueue.length - 1} 个待处理请求</p>
            )}
            <div className="mt-3 flex justify-end gap-2">
              {(currentPerm.req.options ?? []).filter((o) => o.kind?.startsWith("allow")).map((o) => (
                <Button key={o.optionId} onClick={() => { currentPerm.respond({ optionId: o.optionId }); setPermQueue((q) => q.slice(1)); }}>
                  {o.name || "允许"}
                </Button>
              ))}
              {(currentPerm.req.options ?? []).filter((o) => o.kind?.startsWith("reject")).map((o) => (
                <Button key={o.optionId} variant="outline" onClick={() => { currentPerm.respond({ optionId: o.optionId }); setPermQueue((q) => q.slice(1)); }}>
                  {o.name || "拒绝"}
                </Button>
              ))}
              <Button variant="outline" onClick={() => { currentPerm.respond({ cancelled: true }); setPermQueue((q) => q.slice(1)); }}>
                取消
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function AgentLine({ agentName, show }: { agentName?: string; show: boolean }) {
  if (!show || !agentName) return null;
  return (
    <p className="mb-1 flex items-center gap-1 text-[11px] text-ink-3">
      <Circle className="h-1.5 w-1.5 fill-current text-accent" /> @{agentName}
    </p>
  );
}