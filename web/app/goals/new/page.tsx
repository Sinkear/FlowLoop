"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowRight, Check, Loader2, PenLine, Send, Sparkles, Square, Trash2 } from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, inputCls } from "@/components/ui";
import { NewGoalForm } from "@/components/new-goal-form";
import {
  ask,
  clearPerm,
  getNewGoalServerVersion,
  getNewGoalState,
  getNewGoalVersion,
  reset,
  resumeIfNeeded,
  retry,
  stop,
  subscribeNewGoal,
} from "@/lib/new-goal-chat";
import { useAgents, useSquads, useDomains } from "@/lib/queries";
import { cn } from "@/lib/utils";

// 「新建任务」页：一句话描述 → 管家用 agentwork CLI 建任务 → 落库后自动切回任务视图。
// 为什么是独立页面而不是弹窗/参数：
//   1) /goals?new=1 要先把整个任务列表拉起来才弹窗，点侧栏会卡一下，而且 URL 没变化时
//      二次点击没有任何反馈（用户反馈的“点两次不生效”）。
//   2) 建任务是要写一段话的，独立页面能把“描述—管家反馈—追问”这条线放得下。
// 兜底：管家不可用（没有在线机器）时，同一页里能直接手动填表，不是死路。
//
// 生命周期（2026-11-16 修复后的架构）：对话连接、记录、建任务轮询全部住在
// 模块级 store（web/lib/new-goal-chat.ts）里，本组件只是一个「视图」——
// 切到别的页面（app router 会卸载本组件）不会断开连接、不会丢记录、不会
// 掐死管家正在跑的建任务命令；回到本页直接续上现场。浏览器刷新/关闭则靠
// store 的流程快照 + CLI 侧的会话持久化接回原对话（见 store 头注释）。

const EXAMPLES = [
  "把 webhook 投递失败的重试逻辑写成一份文档，放到 webhook 面板旁边",
  "给任务看板加一个按负责人筛选的入口",
  "整理最近三次 agent 执行失败的原因，汇总成一条评论",
];

export default function NewGoalPage() {
  const router = useRouter();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();

  useSyncExternalStore(subscribeNewGoal, getNewGoalVersion, getNewGoalServerVersion);
  const st = getNewGoalState();
  const perm = st.perm;
  const busy = st.busy;

  const [text, setText] = useState("");
  const [followUp, setFollowUp] = useState("");
  const [manual, setManual] = useState(false);
  // 半死连接提醒：连接在、但机器一直不回应时，别让用户盯着一个无声的转圈。
  const [slow, setSlow] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  // 上次刷新/关闭页面留下的未完成流程（快照在 store 里）→ 接回对话并续跑
  // 「建好了没」的轮询。StrictMode 双挂载安全：store 自己有握手去重。
  useEffect(() => {
    void resumeIfNeeded();
  }, []);

  // 自动滚到最新一条。
  useEffect(() => {
    scrollRef.current?.scrollTo(0, scrollRef.current.scrollHeight);
  }, [st.lines, busy]);

  // 守护：连接在、但 60s 没有任何进展 —— 提示可能是半死连接，别静默转圈。
  useEffect(() => {
    if (st.phase !== "running" && st.phase !== "connecting") {
      setSlow(false);
      return;
    }
    const timer = window.setTimeout(() => setSlow(true), 60_000);
    return () => window.clearTimeout(timer);
  }, [st.phase]);

  // 建成功 → 切回任务视图。phase=created 可能发生在用户不在本页的时候
  // （切去了别的页面，轮询在 store 里继续跑）——回到本页时补上这次跳转，
  // 然后 reset 让 store 干净地迎接下一段流程。
  useEffect(() => {
    if (st.phase !== "created" || !st.createdGoal) return;
    const goalId = st.createdGoal.id;
    const timer = window.setTimeout(() => {
      reset();
      router.push(`/goals/${goalId}`);
    }, 900);
    return () => window.clearTimeout(timer);
  }, [st.phase, st.createdGoal, router]);

  return (
    <PageBody>
      <PageHeader
        title="新建任务"
        desc="一句话说清楚要做什么，管家会拆解成任务建进平台；建好之后自动回到任务视图。"
        action={
          <Link href="/goals" className="text-sm text-ink-3 transition-colors hover:text-ink">
            回到任务 <ArrowRight className="ml-0.5 inline h-3.5 w-3.5" />
          </Link>
        }
      />

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          {st.phase === "idle" ? (
            <SectionCard
              title="要做什么？"
              desc="写目标就行，不用写实现步骤——管家会按平台的规矩补负责人和项目。"
            >
              <div className="space-y-3 p-4">
                <textarea
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault();
                      void ask(text, true);
                    }
                  }}
                  rows={5}
                  placeholder="例如：把 webhook 投递失败的重试逻辑写成文档，放到 webhook 面板旁边"
                  className={cn(inputCls, "resize-none leading-relaxed")}
                />
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    onClick={() => void ask(text, true)}
                    disabled={!text.trim()}
                    className="gap-1.5"
                  >
                    <Sparkles className="h-3.5 w-3.5" /> 交给管家创建
                  </Button>
                  <Button variant="outline" onClick={() => setManual(true)} className="gap-1.5">
                    <PenLine className="h-3.5 w-3.5" /> 手动填写
                  </Button>
                  <span className="text-[11px] text-ink-3">⌘/Ctrl + Enter 发送</span>
                </div>
              </div>
            </SectionCard>
          ) : (
            <SectionCard
              title="管家"
              count={st.lines.filter((l) => l.role !== "system").length}
              desc={
                st.phase === "connecting"
                  ? "正在连接管家…"
                  : st.phase === "running"
                    ? "正在创建…（切去别的页面也不会中断，回来接着看）"
                    : st.phase === "created"
                      ? "已创建，正在切回任务视图"
                      : st.phase === "failed"
                        ? "没建成"
                        : "可以继续补充要求"
              }
              action={
                <button
                  type="button"
                  onClick={() => {
                    // 重来：彻底作废这一轮流程（连接、记录、快照、轮询全清）。
                    reset();
                    setText("");
                    setFollowUp("");
                    setSlow(false);
                  }}
                  className="flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-ink-3 transition-colors hover:bg-surface-2 hover:text-ink"
                >
                  <Trash2 className="h-3.5 w-3.5" /> 重来
                </button>
              }
            >
              <div ref={scrollRef} className="max-h-[46vh] min-h-[220px] space-y-3 overflow-y-auto p-4">
                {st.lines.map((l) => {
                  if (l.role === "system") {
                    return (
                      <p key={l.id} className="text-[11px] text-ink-3">
                        {l.text}
                      </p>
                    );
                  }
                  if (l.role === "tool") {
                    return (
                      <div
                        key={l.id}
                        className="rounded-md border border-line bg-surface-0 px-2.5 py-1.5 font-mono text-[11px] text-ink-2"
                      >
                        <span className="text-ink-3">$ </span>
                        {l.toolName}
                        {l.pending ? (
                          <Loader2 className="ml-1 inline h-3 w-3 animate-spin text-ink-3" />
                        ) : l.text ? (
                          <span className="mt-1 block whitespace-pre-wrap break-words text-ink-3">{l.text}</span>
                        ) : null}
                      </div>
                    );
                  }
                  return (
                    <div key={l.id} className={cn("flex", l.role === "user" ? "justify-end" : "justify-start")}>
                      <div
                        className={cn(
                          "max-w-[85%] whitespace-pre-wrap break-words rounded-lg px-3 py-2 text-sm",
                          l.role === "user" ? "bg-accent text-accent-ink" : "bg-surface-2 text-ink"
                        )}
                      >
                        {l.text}
                      </div>
                    </div>
                  );
                })}
                {busy && (
                  <div className="flex items-center gap-2 text-sm text-ink-3">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    {st.phase === "connecting" ? "连接管家…" : "管家正在建任务…"}
                  </div>
                )}
                {st.phase === "created" && (
                  <div className="flex items-start gap-2 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
                    <Check className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>已创建「{st.createdGoal?.title || "任务"}」，正在打开任务详情…</span>
                  </div>
                )}
                {slow && st.phase !== "failed" && st.phase !== "created" && (
                  <div className="space-y-2 rounded-md border border-line bg-surface-2 px-3 py-2 text-xs text-ink-2">
                    <p>已经 60 秒没有新的进展——连接可能处于半死状态（socket 还在，但机器那头没回应）。</p>
                    <div className="flex flex-wrap items-center gap-3">
                      {st.lastAsk && (
                        <button
                          type="button"
                          onClick={retry}
                          className="font-medium underline decoration-dotted underline-offset-2 hover:text-ink"
                        >
                          断开重试
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => setManual(true)}
                        className="underline decoration-dotted underline-offset-2 hover:text-ink"
                      >
                        改用手动填写
                      </button>
                    </div>
                  </div>
                )}
                {st.error && (
                  <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
                    {st.error.split("\n").map((line, i) => (
                      <p key={i}>{line}</p>
                    ))}
                    <div className="flex flex-wrap items-center gap-3">
                      {st.lastAsk && (
                        <button
                          type="button"
                          onClick={retry}
                          className="font-medium underline decoration-dotted underline-offset-2 hover:text-amber-200"
                        >
                          重试（重新连接并再提交一次）
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => setManual(true)}
                        className="underline decoration-dotted underline-offset-2 hover:text-amber-200"
                      >
                        改用手动填写
                      </button>
                    </div>
                  </div>
                )}
              </div>

              {st.phase !== "created" && (
                <div className="flex gap-2 border-t border-line bg-surface-0/40 px-4 py-3">
                  <input
                    className={cn(inputCls, "flex-1")}
                    placeholder={
                      st.phase === "failed"
                        ? "管家没连上，可以直接手动填…"
                        : "补充一句：用哪个负责人、挂哪个项目…"
                    }
                    value={followUp}
                    onChange={(e) => setFollowUp(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !e.shiftKey) {
                        e.preventDefault();
                        void ask(followUp, false);
                        setFollowUp("");
                      }
                    }}
                    disabled={busy || st.phase === "failed"}
                  />
                  {busy ? (
                    <Button
                      variant="danger"
                      onClick={stop}
                      className="gap-1.5"
                      title="中断当前这轮（连接保留，可以继续补充）"
                    >
                      <Square className="h-3.5 w-3.5 fill-current" /> 停止
                    </Button>
                  ) : (
                    <Button
                      onClick={() => {
                        void ask(followUp, false);
                        setFollowUp("");
                      }}
                      disabled={st.phase === "failed" || !followUp.trim()}
                      className="gap-1.5"
                    >
                      <Send className="h-3.5 w-3.5" /> 发送
                    </Button>
                  )}
                </div>
              )}
            </SectionCard>
          )}
        </div>

        <aside className="space-y-5">
          <SectionCard title="会被建成什么样">
            <ul className="space-y-2.5 p-4 text-xs leading-relaxed text-ink-2">
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                没指定负责人 → 进「待办」，你回头在看板上拖到「进行中」才开跑。
              </li>
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                说了负责人/项目 → 直接进「进行中」，agent 立刻开始。
              </li>
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                信息不够时管家会先问你要（比如挂哪个项目），补一句就行。
              </li>
            </ul>
          </SectionCard>

          <SectionCard title="不会写？照这几个改" desc="点一下放进输入框">
            <div className="divide-y divide-line">
              {EXAMPLES.map((e) => (
                <button
                  key={e}
                  type="button"
                  onClick={() => {
                    setText(e);
                    if (st.phase !== "idle") reset();
                  }}
                  className="flex w-full items-start gap-2 px-4 py-2.5 text-left text-xs leading-relaxed text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
                >
                  <ArrowRight className="mt-0.5 h-3 w-3 shrink-0 text-ink-3" />
                  {e}
                </button>
              ))}
            </div>
          </SectionCard>
        </aside>
      </div>

      {manual && (
        <NewGoalForm
          agents={agents}
          squads={squads}
          domains={domains}
          onClose={() => setManual(false)}
          onCreated={() => {
            setManual(false);
            router.push("/goals");
          }}
        />
      )}

      {perm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-[420px] rounded-xl border border-line bg-surface-1 p-4 shadow-xl shadow-black/30">
            <p className="text-sm font-medium text-ink">管家想执行一个命令</p>
            <p className="mt-1 text-xs text-ink-3">
              {perm.req.toolCall?.title || "tool"}（{perm.req.toolCall?.kind || "unknown"}）
            </p>
            <div className="mt-3 flex justify-end gap-2">
              {(perm.req.options ?? [])
                .filter((o) => o.kind?.startsWith("allow"))
                .map((o) => (
                  <Button
                    key={o.optionId}
                    onClick={() => {
                      perm.respond({ optionId: o.optionId });
                      clearPerm();
                    }}
                  >
                    {o.name || "允许"}
                  </Button>
                ))}
              {(perm.req.options ?? [])
                .filter((o) => o.kind?.startsWith("reject"))
                .map((o) => (
                  <Button
                    key={o.optionId}
                    variant="outline"
                    onClick={() => {
                      perm.respond({ optionId: o.optionId });
                      clearPerm();
                    }}
                  >
                    {o.name || "拒绝"}
                  </Button>
                ))}
              <Button
                variant="outline"
                onClick={() => {
                  perm.respond({ cancelled: true });
                  clearPerm();
                }}
              >
                取消
              </Button>
            </div>
          </div>
        </div>
      )}
    </PageBody>
  );
}
