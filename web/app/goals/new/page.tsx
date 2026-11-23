"use client";

import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import {
  ArrowRight,
  Bot,
  Boxes,
  Check,
  Loader2,
  PenLine,
  Send,
  Sparkles,
  Square,
  Target,
  Trash2,
  Users,
  Waypoints,
} from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, inputCls } from "@/components/ui";
import { NewGoalForm } from "@/components/new-goal-form";
import { StewardSetup } from "@/components/steward-setup";
import {
  adoptSteward,
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
import {
  appendCreateTemplate,
  CREATE_TEMPLATES,
  hasCreateTemplate,
  type CreateKind,
} from "@/lib/task-template";
import { cn } from "@/lib/utils";

// 「新建」页：选类型（任务/项目/团队/智能体/蓝本）+ 一句话描述 → 管家用
// ouroflow CLI 建出来 → 落库后自动切回对应视图（任务进详情，其余回列表）。
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

// 五类可创建对象。这一页从“只建任务”扩成通用创建入口：管家用
// `ouroflow create <kind>` 落地，建好后按类型回到对应列表/详情。
const KIND_META: Record<
  CreateKind,
  {
    label: string;
    icon: ReactNode;
    hint: string;
    placeholder: string;
    examples: string[];
    backHref: string;
    backLabel: string;
  }
> = {
  goal: {
    label: "任务",
    icon: <Target className="h-3.5 w-3.5" />,
    hint: "一句话说清要做什么，管家补负责人和项目",
    placeholder:
      "例如：创建一个任务「对齐 webhook 重试文档」，负责人 AI SHELL，挂在 ouroflow 项目下（Tab 补模板）",
    examples: [
      "把 webhook 投递失败的重试逻辑写成一份文档，放到 webhook 面板旁边",
      "给任务看板加一个按负责人筛选的入口",
      "整理最近三次 agent 执行失败的原因，汇总成一条评论",
    ],
    backHref: "/goals",
    backLabel: "任务",
  },
  domain: {
    label: "项目",
    icon: <Boxes className="h-3.5 w-3.5" />,
    hint: "仓库型或工作区型，验收级别可先不设",
    placeholder:
      "例如：创建一个项目「数据同步」，仓库 https://github.com/you/repo.git，默认分支 main，验收 HOOTL（Tab 补模板）",
    examples: [
      "创建一个项目「数据同步」，仓库 https://github.com/you/sync.git，验收 HOOTL",
      "建一个无仓库项目「资料整理」，验收级别先不设",
      "把 https://github.com/you/site.git 建成项目，默认分支 develop",
    ],
    backHref: "/domains",
    backLabel: "项目",
  },
  squad: {
    label: "团队",
    icon: <Users className="h-3.5 w-3.5" />,
    hint: "指定负责人（agent），成员可以后加",
    placeholder:
      "例如：创建一个团队「发版小队」，负责人 release-lead，成员 fixer 和 reviewer（Tab 补模板）",
    examples: [
      "创建一个团队「发版小队」，负责人 release-lead",
      "创建「调研小队」，两个成员分别做资料和写作",
      "建一个只有负责人的团队「前端」，我回头再加人",
    ],
    backHref: "/squads",
    backLabel: "小队",
  },
  agent: {
    label: "智能体",
    icon: <Bot className="h-3.5 w-3.5" />,
    hint: "需要运行时（机器 + CLI）和模型",
    placeholder:
      "例如：在「发版小队」里创建一个 agent「reviewer」，用 opencode@sinkear-dev，模型 deepseek（Tab 补模板）",
    examples: [
      "在「发版小队」里创建 agent「reviewer」，用 opencode@sinkear-dev，模型 deepseek",
      "新建一个管家智能体，负责接需求并拆任务",
      "创建一个只读代码审查 agent，绑 opencode-acp",
    ],
    backHref: "/agents",
    backLabel: "智能体",
  },
  blueprint: {
    label: "蓝本",
    icon: <Waypoints className="h-3.5 w-3.5" />,
    hint: "描述步骤和人工确认点，生成草稿后可发布",
    placeholder:
      "例如：根据「发版检查」场景创建一个蓝本，步骤包括跑测试、查变更、写公告（Tab 补模板）",
    examples: [
      "根据「发版检查」场景创建一个蓝本：跑测试、查变更、写公告",
      "把现在的代码审查流程沉淀成蓝本，审查通过前要人工确认",
      "创建一个研究类蓝本：收集资料、交叉验证、输出结论",
    ],
    backHref: "/blueprints",
    backLabel: "蓝本",
  },
};

const KIND_ORDER: CreateKind[] = ["goal", "domain", "squad", "agent", "blueprint"];

// 右侧「会被建成什么样」按类型给三条后果说明：不写实现细节，只讲用户
// 此刻做决定需要的规则（负责人、验收、运行时、发布）。
const KIND_NOTES: Record<CreateKind, string[]> = {
  goal: [
    "没指定负责人 → 进「待办」，你回头在看板上拖到「进行中」才开跑。",
    "说了负责人/项目 → 直接进「进行中」，agent 立刻开始。",
    "验收级别默认跟随项目；项目和任务都没设时按人工验收（HITL）。",
  ],
  domain: [
    "代码仓库：每个任务一个独立 worktree，可提交 PR；无仓库：持久项目目录。",
    "验收级别建好后还能改，先不设（待定）也可以。",
    "仓库地址会用来识别 GitHub / GitCode，并生成 webhook 入口。",
  ],
  squad: [
    "负责人必须是已有 agent，团队靠它来分派任务。",
    "成员可以建完再加，第一版只需一个负责人。",
    "团队不绑定运行时/模型，成员各自用自己的配置。",
  ],
  agent: [
    "必须选一个在线运行时（机器 + 对应 CLI）。",
    "模型下拉来自该运行时的探测结果，也可以稍后改。",
    "职责写在 system prompt 里，越具体，执行越稳。",
  ],
  blueprint: [
    "先生成草稿版本，确认无误后再发布。",
    "人工卡点会让流程停下来等人确认。",
    "发布后，新建任务时可以选择这个蓝本版本。",
  ],
};

export default function NewGoalPage() {
  const router = useRouter();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();

  useSyncExternalStore(subscribeNewGoal, getNewGoalVersion, getNewGoalServerVersion);
  const st = getNewGoalState();
  const perm = st.perm;
  const busy = st.busy;

  const [kind, setKind] = useState<CreateKind>("goal");
  const meta = KIND_META[kind];
  // 如果输入框里还只是上一类那一段未改动的模板，切类型时整段换掉；已经
  // 改过内容就不动（不覆盖用户写的东西）。
  const changeKind = (k: CreateKind) => {
    setKind(k);
    setText((prev) => {
      const trimmed = prev.trim();
      const untouched = Object.values(CREATE_TEMPLATES).includes(trimmed);
      return untouched ? CREATE_TEMPLATES[k] : prev;
    });
  };
  const [text, setText] = useState("");
  const [followUp, setFollowUp] = useState("");
  const [manual, setManual] = useState(false);
  // 半死连接提醒：连接在、但机器一直不回应时，别让用户盯着一个无声的转圈。
  const [slow, setSlow] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // 把选中类型的实践模板补到描述末尾；已经补过任意一种就什么都不做
  //（幂等——用户在类型之间切换时不追加第二段）。
  const insertTemplate = () => {
    setText((prev) => appendCreateTemplate(prev, kind));
    window.setTimeout(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }, 0);
  };

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

  // 深链：蓝图详情「用此版本新建任务」直达这里。管家对话不吃蓝图参数，
  // 直接展开手动表单并预选该版本（NewGoalForm 读同一个 ?blueprint= 参数）。
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("blueprint")) setManual(true);
  }, []);

  // 建成功 → 切回对应视图：任务进详情，项目/团队/智能体/蓝本回各自列表。
  // phase=created 可能发生在用户不在本页的时候（切去了别的页面，轮询在
  // store 里继续跑）——回到本页时补上这次跳转，然后 reset 让 store 干净
  // 地迎接下一段流程。
  useEffect(() => {
    if (st.phase !== "created" || !st.createdGoal) return;
    const target = st.createdGoal.id;
    const dest =
      st.createdKind === "goal" ? `/goals/${target}` : KIND_META[st.createdKind].backHref;
    const timer = window.setTimeout(() => {
      reset();
      router.push(dest);
    }, 900);
    return () => window.clearTimeout(timer);
  }, [st.phase, st.createdGoal, st.createdKind, router]);

  return (
    <PageBody>
      <PageHeader
        title="新建"
        desc="用一句话说要建什么：任务、项目、团队、智能体或蓝本，管家会补齐字段并用 CLI 落地。"
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
              title="要创建什么？"
              desc="选一个类型，写清关键字段就行——管家会按平台的规矩补齐并建出来。"
            >
              <div className="space-y-3 p-4">
                <div role="radiogroup" aria-label="创建对象类型" className="flex flex-wrap gap-1.5">
                  {KIND_ORDER.map((k) => {
                    const m = KIND_META[k];
                    const active = kind === k;
                    return (
                      <button
                        key={k}
                        type="button"
                        role="radio"
                        aria-checked={active}
                        onClick={() => changeKind(k)}
                        className={cn(
                          "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-medium transition-colors duration-150",
                          active
                            ? "border-accent bg-accent text-accent-ink"
                            : "border-line bg-surface-1 text-ink-3 hover:border-line-strong hover:text-ink"
                        )}
                      >
                        {m.icon}
                        {m.label}
                      </button>
                    );
                  })}
                </div>
                <p className="text-[11px] text-ink-3">{meta.hint}</p>
                <StewardSetup
                  onCreated={(id) => {
                    adoptSteward(id);
                  }}
                />
                <textarea
                  ref={textareaRef}
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
                    // Tab 补全当前类型的实践模板（只在还没补过时拦截，补过以后
                    // Tab 照常切焦点）。
                    if (
                      e.key === "Tab" &&
                      !e.shiftKey &&
                      !e.metaKey &&
                      !e.ctrlKey &&
                      !e.altKey &&
                      !hasCreateTemplate(text)
                    ) {
                      e.preventDefault();
                      insertTemplate();
                      return;
                    }
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault();
                      void ask(text, true, kind);
                    }
                  }}
                  rows={5}
                  placeholder={meta.placeholder}
                  className={cn(inputCls, "resize-none leading-relaxed")}
                />
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    onClick={() => void ask(text, true, kind)}
                    disabled={!text.trim()}
                    className="gap-1.5"
                  >
                    <Sparkles className="h-3.5 w-3.5" /> 交给管家创建
                  </Button>
                  {kind === "goal" ? (
                    <Button variant="outline" onClick={() => setManual(true)} className="gap-1.5">
                      <PenLine className="h-3.5 w-3.5" /> 手动填写
                    </Button>
                  ) : (
                    <Link
                      href={meta.backHref}
                      className="inline-flex items-center gap-1.5 rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 text-xs font-medium text-ink transition-colors hover:bg-surface-3"
                    >
                      去{meta.backLabel}页 <ArrowRight className="h-3.5 w-3.5" />
                    </Link>
                  )}
                  {hasCreateTemplate(text) ? (
                    <span className="text-[11px] text-ink-3">已带模板</span>
                  ) : (
                    <button
                      type="button"
                      onClick={insertTemplate}
                      className="text-[11px] text-ink-3 underline decoration-dotted underline-offset-2 transition-colors hover:text-ink"
                      title={`补一段「${meta.label}」的实践模板（也可在输入框里按 Tab）`}
                    >
                      Tab 补{meta.label}模板
                    </button>
                  )}
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
                      ? `已创建，正在打开${st.createdKind === "goal" ? "任务详情" : "列表"}`
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
                    {st.phase === "connecting" ? "连接管家…" : `管家正在建${meta.label}…`}
                  </div>
                )}
                {st.phase === "created" && (
                  <div className="flex items-start gap-2 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
                    <Check className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>
                      已创建「{st.createdGoal?.title || meta.label}」，正在打开
                      {st.createdKind === "goal" ? "任务详情" : `${KIND_META[st.createdKind].backLabel}列表`}…
                    </span>
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
                        onClick={() => {
                          if (kind === "goal") setManual(true);
                          else router.push(meta.backHref);
                        }}
                        className="underline decoration-dotted underline-offset-2 hover:text-ink"
                      >
                        {kind === "goal" ? "改用手动填写" : `去${meta.backLabel}页手动创建`}
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
                        onClick={() => {
                          if (kind === "goal") setManual(true);
                          else router.push(meta.backHref);
                        }}
                        className="underline decoration-dotted underline-offset-2 hover:text-amber-200"
                      >
                        {kind === "goal" ? "改用手动填写" : `去${meta.backLabel}页手动创建`}
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
          <SectionCard title={`会被建成什么样（${meta.label}）`}>
            <ul className="space-y-2.5 p-4 text-xs leading-relaxed text-ink-2">
              {(KIND_NOTES[kind] as string[]).map((note) => (
                <li key={note} className="flex gap-2">
                  <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                  {note}
                </li>
              ))}
            </ul>
          </SectionCard>

          <SectionCard title="不会写？照这几个改" desc="点一下放进输入框">
            <div className="divide-y divide-line">
              {meta.examples.map((e) => (
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
