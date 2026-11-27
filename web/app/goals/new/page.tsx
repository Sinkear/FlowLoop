"use client";

// 「新建」独立页：选类型 + 一句话描述 → 管家用 flowloop CLI 建出来。
//
// 2026-11-25 用户明确要求：新建是单独页面，不要弹窗；页面里要有 chat
// 窗口来创建。这个页面把“描述—管家反馈—追问”放在同一屏，右侧用树和
// 规则解释每个类型的归属，避免用户不知道自己在建什么。
//
// 对话连接/记录/建对象轮询住在模块级 store（web/lib/new-goal-chat.ts）：
// 切页不中断，刷新能接回；这里只负责视图。

import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import {
  ArrowRight,
  Bot,
  Boxes,
  CalendarClock,
  Check,
  GitBranch,
  ListChecks,
  Loader2,
  Send,
  Sparkles,
  Trash2,
  Users,
} from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, inputCls, StopButton } from "@/components/ui";
import { CreateTree } from "@/components/create/create-tree";
import { CreateSteps, type CreateStep } from "@/components/create/create-steps";
import { NewGoalForm } from "@/components/new-goal-form";
import { ProjectForm } from "@/components/project-form";
import { NewSquadForm } from "@/components/squad-form";
import { AgentForm } from "@/components/agent-form";
import { WorkflowCreateForm } from "@/components/workflow-create-form";
import { ScheduleCreateForm } from "@/components/schedule-create-form";
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
import { useAgents, useBlueprints, useDomains, useGoals, useMachines, useRuntimes, useSquads } from "@/lib/queries";
import {
  appendCreateTemplate,
  CREATE_TEMPLATES,
  hasCreateTemplate,
  type CreateKind,
} from "@/lib/task-template";
import { cn } from "@/lib/utils";

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
    icon: <ListChecks className="h-3.5 w-3.5" />,
    hint: "说清要做什么；项目、负责人、工作流可以交给管家补",
    placeholder: "例如：把 webhook 投递失败的重试逻辑写成文档，挂在 flowloop 项目下",
    examples: [
      "把 webhook 投递失败的重试逻辑写成一份文档，放到 webhook 面板旁边",
      "给任务看板加一个按负责人筛选的入口",
      "整理最近三次 agent 执行失败的原因，汇总成一条评论",
    ],
    backHref: "/goals",
    backLabel: "任务",
  },
  project: {
    label: "项目",
    icon: <Boxes className="h-3.5 w-3.5" />,
    hint: "仓库型或工作区型；验收级别可先不设",
    placeholder: "例如：创建项目「数据同步」，仓库 https://github.com/you/sync.git，验收 HOOTL",
    examples: [
      "创建一个项目「数据同步」，仓库 https://github.com/you/sync.git，验收 HOOTL",
      "建一个无仓库项目「资料整理」，验收级别先不设",
      "把 https://github.com/you/site.git 建成项目，默认分支 develop",
    ],
    backHref: "/projects",
    backLabel: "项目",
  },
  squad: {
    label: "小队",
    icon: <Users className="h-3.5 w-3.5" />,
    hint: "指定负责人（智能体），成员可以后加；小队是公共资源",
    placeholder: "例如：创建小队「发版小队」，负责人 release-lead，成员 fixer 和 reviewer",
    examples: [
      "创建小队「发版小队」，负责人 release-lead",
      "创建「调研小队」，两个成员分别做资料和写作",
      "建一个只有负责人的小队「前端」，我回头再加人",
    ],
    backHref: "/squads",
    backLabel: "小队",
  },
  agent: {
    label: "智能体",
    icon: <Bot className="h-3.5 w-3.5" />,
    hint: "需要在线运行时（机器 + CLI）和模型；智能体是公共资源",
    placeholder:
      "例如：创建智能体「reviewer」，用 opencode@sinkear，模型 deepseek，负责代码审查",
    examples: [
      "创建智能体「reviewer」，用 opencode@sinkear，模型 deepseek，负责代码审查",
      "新建一个管家智能体，负责接需求并拆任务",
      "创建一个只读代码审查智能体，绑 opencode",
    ],
    backHref: "/agents",
    backLabel: "智能体",
  },
  workflow: {
    label: "工作流",
    icon: <GitBranch className="h-3.5 w-3.5" />,
    hint: "工作流只能属于一支小队；先选小队，再描述步骤和卡点",
    placeholder:
      "例如：给「发版小队」创建工作流「发版检查」：跑测试、查变更、写公告，发布前人工确认",
    examples: [
      "给「发版小队」创建工作流「发版检查」：跑测试、查变更、写公告，发布前人工确认",
      "把现在的代码审查流程沉淀成工作流，审查通过前要人工确认",
      "创建研究工作流：收集资料、交叉验证、输出结论",
    ],
    backHref: "/workflows",
    backLabel: "工作流",
  },
  schedule: {
    label: "定时任务",
    icon: <CalendarClock className="h-3.5 w-3.5" />,
    hint: "按 cron 反复生成任务；需要项目、负责人和时区",
    placeholder: "例如：每天 9 点给「运维」项目建一条巡检任务，负责人 ops，完成后生成复盘",
    examples: [
      "每天 9 点给「运维」项目建一条巡检任务，负责人 ops",
      "每周一 10 点创建「周报」任务，绑定小队 weekly",
      "每小时检查一次告警，项目 ops，验收 HOOTL",
    ],
    backHref: "/schedules",
    backLabel: "定时任务",
  },
};

const KIND_ORDER: CreateKind[] = ["project", "goal", "squad", "agent", "workflow", "schedule"];

const KIND_NOTES: Record<CreateKind, string[]> = {
  goal: [
    "没指定负责人 → 进「待办」，拖到「进行中」才开跑。",
    "说了负责人/项目 → 直接进「进行中」，智能体立刻开始。",
    "小队任务可以套用本队工作流；单干智能体自动拆解。",
  ],
  project: [
    "代码仓库：每个任务独立 worktree；无仓库：持久项目目录。",
    "验收级别建好后还能改，先不设（待定）也可以。",
    "项目是首层归属：任务、定时任务都挂在它下面。",
  ],
  squad: [
    "负责人必须是已有智能体，小队靠它分派任务。",
    "成员可以建完再加，第一版只需一个负责人。",
    "小队不绑定运行时/模型，成员各自用自己的配置。",
  ],
  agent: [
    "必须选一个在线运行时（机器 + 对应 CLI）。",
    "模型下拉来自运行时的探测结果，也可以稍后改。",
    "职责写在 system prompt 里，越具体，执行越稳。",
  ],
  workflow: [
    "工作流归属一支小队；跨队只能复制，不能共享。",
    "先生成草稿版本，确认无误后再发布。",
    "人工卡点会让流程停下来等人确认。",
  ],
  schedule: [
    "保存时 pin 住当前工作流版本，之后发布新版本不会偷偷改它。",
    "可以开启“完成后生成复盘”，让定时任务也进入优化闭环。",
    "触发时会在项目下生成一条普通任务，沿用项目的验收策略。",
  ],
};

export default function NewGoalPage() {
  const router = useRouter();
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const { data: domains } = useDomains();
  const { data: machines } = useMachines();
  const { data: runtimes } = useRuntimes();
  const { data: blueprints } = useBlueprints();
  const { data: goals } = useGoals();
  useSyncExternalStore(subscribeNewGoal, getNewGoalVersion, getNewGoalServerVersion);
  const st = getNewGoalState();
  const perm = st.perm;
  const busy = st.busy;

  const [kind, setKind] = useState<CreateKind>("goal");
  const [squadId, setSquadId] = useState("");
  const [manual, setManual] = useState(false);
  const [initialProject, setInitialProject] = useState("");
  const [initialBlueprint, setInitialBlueprint] = useState("");
  const meta = KIND_META[kind];
  const machineUp = (machines ?? []).some((m) => m.status === "connected");
  const runtimeUp = (runtimes ?? []).some((r) => r.status === "active");
  const steps: CreateStep[] = [
    {
      key: "project",
      label: "项目",
      desc: "任务的归属地，提供仓库与验收策略",
      done: (domains ?? []).length > 0,
      count: (domains ?? []).length,
      kind: "project",
      href: "/projects",
    },
    {
      key: "infra",
      label: "运行环境",
      desc: "机器在线 + 运行时可用，智能体才能跑",
      done: machineUp && runtimeUp,
      count: (runtimes ?? []).filter((r) => r.status === "active").length,
      href: "/infra",
    },
    {
      key: "agent",
      label: "智能体",
      desc: "公共执行者，绑定运行时和模型",
      done: (agents ?? []).length > 0,
      count: (agents ?? []).length,
      kind: "agent",
      href: "/agents",
    },
    {
      key: "squad",
      label: "小队",
      desc: "负责人 + 成员；工作流必须归一支小队",
      done: (squads ?? []).length > 0,
      count: (squads ?? []).length,
      kind: "squad",
      href: "/squads",
    },
    {
      key: "workflow",
      label: "工作流",
      desc: "把流程沉淀成可发布版本，再给任务用",
      done: (blueprints ?? []).length > 0,
      count: (blueprints ?? []).length,
      kind: "workflow",
      href: "/workflows",
    },
    {
      key: "goal",
      label: "任务",
      desc: "挂项目、选小队/工作流，开始干活",
      done: (goals ?? []).length > 0,
      count: (goals ?? []).length,
      kind: "goal",
      href: "/goals",
    },
  ];
  const [text, setText] = useState("");
  const [followUp, setFollowUp] = useState("");
  const [slow, setSlow] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  // 最近一次活动时间跟着 store 走；effect 里通过 ref 读取，避免每次流式
  // 文本都重建 60s 计时器。
  const lastActivityRef = useRef(st.lastActivityAt);
  lastActivityRef.current = st.lastActivityAt;
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // 深链：?kind= 决定初始类型；?squad= 给工作流预选归属。
  useEffect(() => {
    const q = new URLSearchParams(window.location.search);
    const raw = q.get("kind") ?? "";
    const normalized = raw === "domain" ? "project" : raw === "blueprint" ? "workflow" : raw;
    if (KIND_ORDER.includes(normalized as CreateKind)) {
      setKind(normalized as CreateKind);
      if (normalized === "workflow") {
        const squad = q.get("squad") ?? "";
        if (squad) setSquadId(squad);
      }
    }
    // 从项目/工作流详情进来时，直接落到带上下文的手动表单：这两个上下文
    // 字段（项目、版本）在聊天里靠自然语言补齐不稳，表单是确定路径。
    const project = q.get("project") ?? "";
    const blueprint = q.get("blueprint") ?? "";
    if (project) setInitialProject(project);
    if (blueprint) setInitialBlueprint(blueprint);
    if (project || blueprint) {
      setKind("goal");
      setManual(true);
    }
    void resumeIfNeeded();
  }, []);

  useEffect(() => {
    scrollRef.current?.scrollTo(0, scrollRef.current.scrollHeight);
  }, [st.lines, busy]);

  // 「60 秒没有进展」只看最近一次活动时间，而不是 phase：一个回合里
  // phase 会一直保持 running，只按 phase 计时会把持续输出的正常连接
  // 误报成「半死」。
  useEffect(() => {
    if (st.phase !== "running" && st.phase !== "connecting") {
      setSlow(false);
      return;
    }
    const tick = () => {
      const last = lastActivityRef.current || Date.now();
      setSlow(Date.now() - last > 60_000);
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [st.phase]);

  // 建成功 → 切回对应视图：任务进详情，其它进详情或列表。
  useEffect(() => {
    if (st.phase !== "created" || !st.createdGoal) return;
    const target = st.createdGoal.id;
    const dest =
      st.createdKind === "goal"
        ? `/goals/${target}`
        : st.createdKind === "project"
          ? `/projects/${target}`
          : st.createdKind === "squad"
            ? `/squads/${target}`
            : st.createdKind === "workflow"
              ? `/workflows/${target}`
              : KIND_META[st.createdKind].backHref;
    const timer = window.setTimeout(() => {
      reset();
      router.push(dest);
    }, 900);
    return () => window.clearTimeout(timer);
  }, [st.phase, st.createdGoal, st.createdKind, router]);

  const changeKind = (k: CreateKind) => {
    setKind(k);
    setText((prev) => {
      const trimmed = prev.trim();
      const untouched = Object.values(CREATE_TEMPLATES).includes(trimmed);
      return untouched ? CREATE_TEMPLATES[k] : prev;
    });
  };

  const insertTemplate = () => {
    setText((prev) => appendCreateTemplate(prev, kind));
    window.setTimeout(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }, 0);
  };

  const send = (say: string) => {
    if (kind === "workflow" && !squadId) return;
    void ask(say, true, kind, { squadId: kind === "workflow" ? squadId : undefined });
  };

  return (
    <PageBody>
      <PageHeader
        title="新建"
        desc="用一句话说要建什么。管家会补齐字段、用 CLI 落地；你也可以在对话里继续追问。"
        action={
          <Link href="/" className="text-sm text-ink-3 transition-colors hover:text-ink">
            回到工作台 <ArrowRight className="ml-0.5 inline h-3.5 w-3.5" />
          </Link>
        }
      />

      <CreateSteps
        steps={steps}
        currentKind={kind}
        disabled={busy || st.phase !== "idle"}
        onPick={changeKind}
      />

      <div className="mt-5 grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          {manual ? (
            <SectionCard
              title={`手动填写${meta.label}`}
              desc="不走对话，直接填字段。创建成功后会带你到对应页面。"
              action={
                <button
                  type="button"
                  onClick={() => setManual(false)}
                  className="text-[11px] text-ink-3 underline decoration-dotted underline-offset-2 hover:text-ink"
                >
                  回到对话
                </button>
              }
            >
              <div className="p-4">
                {kind === "goal" && (
                  <NewGoalForm
                    embedded
                    initialProjectId={initialProject || undefined}
                    initialBlueprintVersionId={initialBlueprint || undefined}
                    initialSquadId={squadId || undefined}
                    onCreated={(goal) => router.push(`/goals/${goal.id}`)}
                  />
                )}
                {kind === "project" && (
                  <ProjectForm
                    embedded
                    onClose={() => setManual(false)}
                    onCreated={(project) => router.push(`/projects/${project.id}`)}
                  />
                )}
                {kind === "squad" && (
                  <NewSquadForm
                    embedded
                    agents={agents}
                    onClose={() => setManual(false)}
                    onCreated={(squad) => router.push(`/squads/${squad.id}?tab=workflows`)}
                  />
                )}
                {kind === "agent" && (
                  <AgentForm
                    embedded
                    onClose={() => setManual(false)}
                    onCreated={() => router.push("/agents")}
                  />
                )}
                {kind === "workflow" && (
                  <WorkflowCreateForm
                    embedded
                    squadId={squadId || undefined}
                    onClose={() => setManual(false)}
                    onCreated={(workflow) => router.push(`/workflows/${workflow.id}`)}
                  />
                )}
                {kind === "schedule" && (
                  <ScheduleCreateForm
                    embedded
                    onClose={() => setManual(false)}
                    onCreated={() => router.push("/schedules")}
                  />
                )}
              </div>
            </SectionCard>
          ) : st.phase === "idle" ? (
            <SectionCard
              title="要创建什么？"
              desc="选一个类型，写清关键字段就行——右侧说明这个类型会被建成什么样。"
            >
              <div className="space-y-4 p-4">
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

                {kind === "workflow" && (
                  <label className="block space-y-1.5">
                    <span className="text-xs font-medium text-ink">归属小队</span>
                    <select
                      className={inputCls}
                      value={squadId}
                      onChange={(e) => setSquadId(e.target.value)}
                    >
                      <option value="">选择一支小队…</option>
                      {(squads ?? []).map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                    {!squads?.length ? (
                      <span className="block text-[11px] leading-relaxed text-ink-3">
                        还没有小队。工作流必须属于一支小队——先去{" "}
                        <Link href="/squads?new=1" className="underline decoration-dotted text-ink">
                          创建小队
                        </Link>
                        。
                      </span>
                    ) : null}
                  </label>
                )}

                <StewardSetup onCreated={(id) => adoptSteward(id)} />

                <textarea
                  ref={textareaRef}
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
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
                      send(text);
                    }
                  }}
                  rows={6}
                  placeholder={meta.placeholder}
                  className={cn(inputCls, "resize-none leading-relaxed")}
                />
                <div className="flex flex-wrap items-center gap-2">
                  <Button onClick={() => send(text)} disabled={!text.trim() || (kind === "workflow" && !squadId)} className="gap-1.5">
                    <Sparkles className="h-3.5 w-3.5" /> 交给管家创建
                  </Button>
                  <Button variant="outline" onClick={() => setManual(true)}>
                    手动填写
                  </Button>
                  <Link
                    href={meta.backHref}
                    className="inline-flex items-center gap-1.5 rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 text-xs font-medium text-ink transition-colors hover:bg-surface-3"
                  >
                    去{meta.backLabel}页 <ArrowRight className="h-3.5 w-3.5" />
                  </Link>
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
                      ? "已创建，正在打开"
                      : st.phase === "failed"
                        ? "没建成"
                        : "可以继续补充要求"
              }
              action={
                <button
                  type="button"
                  onClick={() => {
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
                      <p key={l.id} className="text-[11px] leading-relaxed text-ink-3">
                        {l.text}
                      </p>
                    );
                  }
                  if (l.role === "tool") {
                    return (
                      <div key={l.id} className="rounded-md border border-line bg-surface-0 px-2.5 py-1.5 font-mono text-[11px] text-ink-2">
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
                    <span>已创建「{st.createdGoal?.title || meta.label}」，正在打开…</span>
                  </div>
                )}
                {slow && st.phase !== "failed" && st.phase !== "created" && (
                  <div className="space-y-2 rounded-md border border-line bg-surface-2 px-3 py-2 text-xs text-ink-2">
                    <p>已经 60 秒没有收到新的进展了——如果智能体还在执行长命令，可以再等等；也可以直接断开重试。</p>
                    {st.lastAsk && (
                      <button type="button" onClick={retry} className="font-medium underline decoration-dotted underline-offset-2 hover:text-ink">
                        断开重试
                      </button>
                    )}
                  </div>
                )}
                {st.error && (
                  <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
                    {st.error.split("\n").map((line, i) => (
                      <p key={i}>{line}</p>
                    ))}
                    {st.lastAsk && (
                      <button type="button" onClick={retry} className="font-medium underline decoration-dotted underline-offset-2 hover:text-amber-200">
                        重试（重新连接并再提交一次）
                      </button>
                    )}
                  </div>
                )}
              </div>

              {st.phase !== "created" && (
                <div className="flex gap-2 border-t border-line bg-surface-0/40 px-4 py-3">
                  <input
                    className={cn(inputCls, "flex-1")}
                    placeholder={st.phase === "failed" ? "管家没连上，可以补充后再试…" : "补充一句：用哪个负责人、挂哪个项目…"}
                    value={followUp}
                    onChange={(e) => setFollowUp(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !e.shiftKey) {
                        e.preventDefault();
                        if (followUp.trim()) {
                          void ask(followUp, false);
                          setFollowUp("");
                        }
                      }
                    }}
                    disabled={busy || st.phase === "failed"}
                  />
                  {busy ? (
                    <StopButton onClick={stop} title="中断当前这轮（连接保留，可以继续补充）" />
                  ) : (
                    <Button
                      onClick={() => {
                        if (!followUp.trim()) return;
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
          <SectionCard title="层级与归属" desc="任务挂项目，工作流归小队，智能体是公共资源">
            <div className="p-3">
              <CreateTree interactive={false} plain />
            </div>
          </SectionCard>

          <SectionCard title={`会被建成什么样（${meta.label}）`}>
            <ul className="space-y-2.5 p-4 text-xs leading-relaxed text-ink-2">
              {KIND_NOTES[kind].map((note) => (
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