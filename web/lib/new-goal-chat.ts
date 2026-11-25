// /goals/new 的管家对话 store。
//
// 为什么存在（2026-11-16 的线上 bug）：对话连接和记录原本长在页面组件上，
// 切到别的页面（Next.js app router 会把页面整个卸载）就触发两条灾难链——
//   a) 记录只是组件 state，卸载即丢；
//   b) 卸载 cleanup 里 disposeClient() 关 WebSocket → daemon 视为会话断开
//      （chat.go CloseChat）→ 机器侧关 CLI stdin → 3 秒强杀进程。管家正在
//      跑的「flowloop create goal」被掐死，任务永远建不出来。
// 把对话搬进模块作用域后，切页只是「视图离开」：连接、记录、建任务轮询
// 全部在后台继续；回到 /goals/new 直接续上现场。
//
// 浏览器刷新/关闭没有 module 作用域可依赖，用两层兜底：
//   1) 会话历史：CLI 自己的 session store 按 chat cwd 落盘（chat.go 的设计），
//      所以按快照钉住的 sessionId 走 listSessions→loadSession 就能接回原文；
//   2) 建没建成：快照里记了 startedAt，恢复后接着轮询 /goals 找这条新任务。

import {
  AcpChatClient,
  AcpCloseInfo,
  ChatEvent,
  PermissionRequest,
  SessionInfo,
  formatAcpClose,
} from "./acp";
import { listAgents, listBlueprints, listDomains, listGoals, listSchedules, listSquads } from "./api";
import type { CreateKind } from "./task-template";
import { describeStewardFailure } from "./steward";

// 每种创建对象的管家指令措辞：CLI 的 kind、要补齐的字段、建好后回哪个
// 视图。放在 store 里而不是页面，刷新恢复后（只有快照没有页面状态）也能
// 把同一段流程补完。
const CREATE_INTENTS: Record<
  CreateKind,
  { label: string; cliKind: string; cli: string; fields: string; backView: string }
> = {
  goal: {
    label: "任务",
    cliKind: "goal",
    cli: 'flowloop create goal --title "..."',
    fields: "负责人/项目",
    backView: "任务视图",
  },
  project: {
    label: "项目",
    cliKind: "domain（项目）",
    cli: 'flowloop create domain --name "..."',
    fields: "仓库地址/默认分支/验收级别（hitl、hootl 或不设）",
    backView: "项目列表",
  },
  squad: {
    label: "小队",
    cliKind: "squad（小队）",
    cli: 'flowloop create squad --name "..."',
    fields: "负责人/成员/职责",
    backView: "小队列表",
  },
  agent: {
    label: "智能体",
    cliKind: "agent",
    cli: 'flowloop create agent --name "..."',
    fields: "运行时/模型/职责（智能体是公共资源，不挂小队）",
    backView: "智能体列表",
  },
  workflow: {
    label: "工作流",
    cliKind: "blueprint（工作流）",
    cli: 'flowloop create blueprint --name "..."',
    fields: "所属小队/步骤/人工卡点/适用场景",
    backView: "工作流列表",
  },
  schedule: {
    label: "定时任务",
    cliKind: "schedule",
    cli: 'flowloop create schedule --name "..."',
    fields: "项目/cron/负责人/验收分类/是否生成复盘",
    backView: "定时任务列表",
  },
};

export type NewGoalPhase = "idle" | "connecting" | "ready" | "running" | "created" | "failed";

export interface NewGoalLine {
  id: number;
  role: "user" | "agent" | "tool" | "system";
  text: string;
  toolName?: string;
  pending?: boolean;
}

export interface NewGoalState {
  phase: NewGoalPhase;
  createdKind: CreateKind;
  lines: NewGoalLine[];
  error: string;
  busy: boolean;
  lastAsk: { say: string; first: boolean } | null;
  createdGoal: { id: string; title: string } | null;
  perm: { req: PermissionRequest; respond: (o: { optionId?: string; cancelled?: boolean }) => void } | null;
}

// ── 模块状态（整个 store 的本体，页面卸载不动它） ───────────────────────

let phase: NewGoalPhase = "idle";
let lines: NewGoalLine[] = [];
let error = "";
let lastAsk: { say: string; first: boolean } | null = null;
let createdGoal: { id: string; title: string } | null = null;
// 这一轮在创建什么：任务 / 项目 / 小队 / 智能体 / 工作流。决定管家指令的
// 措辞，也决定「建好了没」去轮询哪张表。
let flowKind: CreateKind = "goal";
// 工作流必须挂在某个小队下：聊天创建时把页面选中的 owner 钉进 CLI 参数。
let flowSquadId = "";
let perm: NewGoalState["perm"] = null;

let stewardId: string | null = null;

// 页面侧 StewardSetup 手动建好管家后回调进来：让下一次 ensureClient 直接
// 采纳这个 id，而不是再走 listAgents 兜底（新管家此刻就在列表里，但显式
// 采纳能避免竞态，也让「刚创建就点提交」不用等轮询）。
export function adoptSteward(id: string): void {
  if (id) stewardId = id;
}

// 本次「交给管家」的起点时间：建成功后靠它从 /goals 里认出这条新任务，
// 不依赖管家把 id 报回来（它可能省略、可能幻觉）。
let startedAt = 0;

let client: AcpChatClient | null = null;
// 握手中（还没成为正式 client）的那一个：重试/重来时必须能把它关掉，
// 否则机器侧会悬着一条没人收的 chat channel。
let inflightClient: AcpChatClient | null = null;
let connecting: Promise<AcpChatClient> | null = null;
let session = "";
// 代际：每次重试/重来/断开 +1。旧连接迟到的 onEvent/onClose 据此丢弃，
// 不污染新流程（StrictMode 双挂载同理）。
let gen = 0;
// 一次提交的代次：失败后迟到的收尾不再改 UI。
let attempt = 0;
// 同一帧连点（回车+按钮）的同步闸：disabled 挡不住同一 tick 里的两次调用。
let inFlight = false;
let lineId = 0;
// session/load 会把历史以 session/update 事件重放：回放期间 CLI 的用户侧
// 回显就是唯一记录来源（页面还没有本地气泡），必须收；实况轮次里页面自己
// 已经推过用户气泡，再收就重复了。
let replaying = false;
// 下一次握手要先「续上」上一次没做完的对话（按快照 sessionId load）。
let restorePending = false;
// ensureClient 刚做完一次恢复回放：紧随其后的 ask(first=true) 不能把回放
// 出来的记录清空（重试场景：恢复+重发同一条指令）。
let restoredJustNow = false;

let pollTimer: ReturnType<typeof setInterval> | null = null;

// ── React 订阅（useSyncExternalStore 的三件套） ─────────────────────────

let version = 0;
const listeners = new Set<() => void>();

function emit() {
  version += 1;
  for (const l of listeners) l();
}

export function subscribeNewGoal(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getNewGoalVersion(): number {
  return version;
}

// Next 在服务端也会预渲染 client 组件：服务端必须看到初态，不能碰 window。
export function getNewGoalServerVersion(): number {
  return 0;
}

export function getNewGoalState(): NewGoalState {
  return {
    phase,
    lines,
    error,
    busy: phase === "connecting" || phase === "running",
    lastAsk,
    createdGoal,
    createdKind: flowKind,
    perm,
  };
}

// ── 流程快照（浏览器刷新/关闭后的恢复依据；流程结束即清） ───────────────

// 一段流程 = 一次「交给管家创建」的 first ask 及其追问。在途时把 ACP
// sessionId（CLI 已按 cwd 持久化它）和 startedAt 钉进 sessionStorage：
// 刷新回来 listSessions→loadSession 接回同一段对话，轮询从 startedAt 续跑。
const SNAPSHOT_KEY = "flowloop-new-goal-flow";
const SNAPSHOT_MAX_AGE_MS = 24 * 60 * 60 * 1000;
const POLL_INTERVAL_MS = 2000;
// 轮询上限：管家把建任务卡死时不至于永远打 /goals。
const POLL_MAX_MS = 30 * 60 * 1000;

interface FlowSnapshot {
  sessionId: string;
  startedAt: number;
  savedAt: number;
  kind?: CreateKind;
}

function readSnapshot(): FlowSnapshot | null {
  try {
    const raw = sessionStorage.getItem(SNAPSHOT_KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as FlowSnapshot;
    if (!s?.sessionId || !s.startedAt) return null;
    if (Date.now() - s.savedAt > SNAPSHOT_MAX_AGE_MS) {
      sessionStorage.removeItem(SNAPSHOT_KEY);
      return null;
    }
    return s;
  } catch {
    return null;
  }
}

function writeSnapshot(): void {
  if (!session || !startedAt) return;
  try {
    const snap: FlowSnapshot = { sessionId: session, startedAt, savedAt: Date.now(), kind: flowKind };
    sessionStorage.setItem(SNAPSHOT_KEY, JSON.stringify(snap));
  } catch {
    // 私有模式配额等：刷新恢复降级，实况流程不受影响。
  }
}

function clearSnapshot(): void {
  try {
    sessionStorage.removeItem(SNAPSHOT_KEY);
  } catch {
    // ignore
  }
}

// ── 建任务轮询 ───────────────────────────────────────────────────────────

function stopPoll() {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

// 出现「本次提交之后创建」的任务即成功。轮询活在 store 里——用户切去别的
// 页面它照样跑，回到 /goals/new 才把人送去任务详情。
// listKindRows 把 5 种创建结果统一成 {id,name,created_at}：轮询只关心
// 「这次提交之后多出来的那一条」。每张表的分页/字段不同，但列表接口都
// 返回 id + name + created_at，够用来认领新对象。
async function listKindRows(
  kind: CreateKind
): Promise<{ id: string; name: string; created_at?: string }[]> {
  switch (kind) {
    case "project":
      return (await listDomains()).map((d) => ({ id: d.id, name: d.name, created_at: d.created_at }));
    case "squad":
      return (await listSquads()).map((d) => ({ id: d.id, name: d.name, created_at: d.created_at }));
    case "agent":
      return (await listAgents()).map((d) => ({ id: d.id, name: d.name, created_at: d.created_at }));
    case "workflow":
      return (await listBlueprints()).map((d) => ({ id: d.id, name: d.name, created_at: d.created_at }));
    case "schedule":
      return (await listSchedules()).map((d) => ({ id: d.id, name: d.name, created_at: d.created_at }));
    default:
      return (await listGoals()).map((d) => ({ id: d.id, name: d.title, created_at: d.created_at }));
  }
}

function ensurePolling() {
  if (!startedAt) return;
  if (phase === "created" || phase === "failed") return;
  if (pollTimer !== null) return;
  const startISO = new Date(startedAt - 2000).toISOString();
  const deadline = Date.now() + POLL_MAX_MS;
  const kind = flowKind;
  pollTimer = setInterval(async () => {
    if (Date.now() > deadline) {
      stopPoll();
      return;
    }
    if (flowKind !== kind) return; // 中途切了类型：让新流程自己起轮询
    try {
      const rows = await listKindRows(kind);
      const hit = rows.find((r) => r.created_at && r.created_at >= startISO);
      if (hit) {
        createdGoal = { id: hit.id, title: hit.name };
        stopPoll();
        clearSnapshot();
        setPhase("created");
      }
    } catch {
      // 轮询偶发失败不打断对话，下一轮再试。
    }
  }, POLL_INTERVAL_MS);
}

// ── 记录维护 ──────────────────────────────────────────────────────────────

function setPhase(p: NewGoalPhase) {
  phase = p;
  emit();
}

function push(line: Omit<NewGoalLine, "id">) {
  // 连续 agent 片段合并进同一个气泡（跟管家聊天一致）。
  const last = lines[lines.length - 1];
  if (last && last.role === line.role && line.role === "agent" && !last.pending) {
    lines = [...lines.slice(0, -1), { ...last, text: last.text + line.text }];
  } else {
    // id 在数组外分配（原实现如此：懒更新执行两次会让 key 重复）。
    const id = (lineId += 1);
    lines = [...lines, { ...line, id }];
  }
  emit();
}

// 关掉当前连接（含握手中的那一个），作废这一代所有回调。
// 页面卸载【不】调这个——只有「重来/重试/停止握手」和失败收尾才调。
function disposeClient() {
  gen += 1;
  connecting = null;
  const current = client;
  const inflight = inflightClient;
  client = null;
  inflightClient = null;
  session = "";
  if (inflight && inflight !== current) inflight.close();
  current?.close();
  perm = null;
  emit();
}

// ── 连接管家（懒连接：打开页面不建会话，不浪费一次 CLI 启动） ──────────

// 并发保护：同一帧连点两次只会有一个握手（connecting 复用在途 promise）。
// 会话选择：restorePending 时先按快照 session/load 续上未完成的对话，
// 否则 session/new 开新会话。
async function ensureClient(): Promise<AcpChatClient> {
  if (client && session) return client;
  if (connecting) return connecting;
  const myGen = gen;
  const run = (async () => {
    setPhase("connecting");
    const steward = stewardId
      ? { id: stewardId }
      : (await listAgents()).find((a) => a.type === "steward");
    if (!steward) {
      throw new Error("还没有管家 agent——先连一台机器并重启服务，平台会自动创建管家。");
    }
    stewardId = steward.id;
    const c = new AcpChatClient({
      onEvent: (ev: ChatEvent) => {
        if (myGen !== gen) return;
        if (ev.kind === "agent") {
          push({ role: "agent", text: ev.text });
        } else if (ev.kind === "tool") {
          if (ev.isResult) {
            lines = lines.map((l) =>
              l.role === "tool" && l.toolName === ev.toolName && l.pending
                ? { ...l, text: ev.output ?? "", pending: false }
                : l
            );
            emit();
          } else {
            push({ role: "tool", text: ev.text, toolName: ev.toolName, pending: true });
          }
        } else if (ev.kind === "user" && replaying) {
          push({ role: "user", text: ev.text });
        }
      },
      onPermissionRequest: (req, respond) => {
        if (myGen !== gen) return;
        perm = { req, respond };
        emit();
      },
      onClose: (info?: AcpCloseInfo) => {
        if (myGen !== gen) return;
        client = null;
        session = "";
        // 流程还活着 → 下次握手续同一个会话（CLI 已把它持久化）。
        if (startedAt > 0) restorePending = true;
        if (phase !== "created") setPhase("failed");
        if (!error) {
          const detail = info ? formatAcpClose(info) : "连接已断开";
          error = `与管家的连接断开了（${detail}）——可以点「重试」重新连接，或改用手动填写。`;
          emit();
        }
      },
    });
    inflightClient = c;
    const host = window.location.host;
    const proto = window.location.protocol === "https:" ? "s" : "";
    await c.connect(`ws${proto}://${host}/backend/agents/${steward.id}/acp`);
    if (myGen !== gen) {
      c.close();
      throw new Error("连接已取消（页面状态已重置）");
    }
    const snap = restorePending ? readSnapshot() : null;
    if (snap) {
      // 续上未完成的对话：按快照钉住的 sessionId 精确匹配，不拿「最新的」，
      // 以免误载 /chat 页开过的别的会话。
      let list: SessionInfo[] = [];
      try {
        list = await c.listSessions();
      } catch {
        list = [];
      }
      const target = list.find((s) => s.sessionId === snap.sessionId);
      if (target) {
        replaying = true;
        try {
          await c.loadSession(target.sessionId, target.cwd);
        } finally {
          replaying = false;
        }
        if (myGen !== gen) {
          c.close();
          throw new Error("连接已取消（页面状态已重置）");
        }
        session = target.sessionId;
        restoredJustNow = true;
        if (!startedAt) startedAt = snap.startedAt;
        push({
          role: "system",
          text: "已接回上一次没做完的对话（上次页面关闭/刷新把连接断了）——建好的话马上会自动跳转。",
        });
      }
    }
    if (!session) {
      session = await c.newSession();
      if (myGen !== gen) {
        c.close();
        throw new Error("连接已取消（页面状态已重置）");
      }
    }
    restorePending = false;
    client = c;
    inflightClient = null;
    return c;
  })();
  connecting = run;
  try {
    return await run;
  } finally {
    if (connecting === run) connecting = null;
  }
}

// ── 对外动作 ──────────────────────────────────────────────────────────────

// 跟管家说一句话：first=true 把「要做什么」包成一条建任务的指令（让 CLI
// 直接建，缺字段问我要）；first=false 原文追问。
export async function ask(say: string, first: boolean, kind: CreateKind = "goal", opts: { squadId?: string } = {}): Promise<void> {
  const body = say.trim();
  if (!body || inFlight) return;
  inFlight = true;
  const myAttempt = ++attempt;
  if (first) {
    flowKind = kind;
    flowSquadId = opts.squadId ?? "";
  }
  lastAsk = { say: body, first };
  error = "";
  emit();
  try {
    const c = await ensureClient();
    // 期间被「重试/重来」取消：这次请求的结果一律不再改 UI。
    if (myAttempt !== attempt) return;
    setPhase("running");
    if (first) {
      // 刚做过恢复回放时保留记录（重试场景）；否则这是一段全新流程，
      // 清掉上一轮的残留。
      const keepLines = restoredJustNow;
      restoredJustNow = false;
      startedAt = Date.now();
      stopPoll();
      if (!keepLines) {
        lines = [];
        lineId = 0;
        emit();
      }
      const spec = CREATE_INTENTS[flowKind];
      const squadFlag = flowKind === "workflow" && flowSquadId ? ` --squad "${flowSquadId}"` : "";
      const cli = `${spec.cli}${squadFlag}`;
      push({ role: "user", text: body });
      push({
        role: "system",
        text: `已交给管家：它用 flowloop CLI 建${spec.label}，建好会自动切回${spec.backView}。`,
      });
      ensurePolling();
      writeSnapshot();
      await c.prompt(
        session,
        [
          `帮我创建一个${spec.label}（${spec.cliKind}），需求如下：`,
          "",
          body,
          "",
          `请用 \`${cli}\` 建出来（${spec.fields}；能判断就一起带上，缺字段就问我要）。`,
          `建完后用一句话告诉我${spec.label}名称，不要做别的改动。`,
        ].join("\n")
      );
    } else {
      push({ role: "user", text: body });
      await c.prompt(session, body);
    }
    if (myAttempt !== attempt) return;
    setPhase("ready");
    writeSnapshot();
  } catch (e) {
    if (myAttempt !== attempt) return;
    setPhase("failed");
    // 完整暴露失败阶段 + WS 关闭码/原因，不再只留一个无声的转圈。
    const raw = String((e as Error)?.message ?? e);
    error = `没建成：${raw}`;
    emit();
    // 流程未完成 → 下次握手接回同一会话（重试即续）。
    if (startedAt > 0) restorePending = true;
    disposeClient();
    // 诊断慢一点没关系：先摆事实，再补上具体到机器/CLI 的解释。
    void describeStewardFailure(stewardId).then((why) => {
      if (why) {
        error = error ? `${error}\n${why}` : why;
        emit();
      }
    });
  } finally {
    inFlight = false;
  }
}

// 失败后的「重试」：关掉旧连接，用同一句话重新走一遍握手。
// restorePending 已在失败时置位 → 握手先续上同一会话（管家看得见它上次
// 做到哪了），再把这句话重发进去。
export function retry(): void {
  const last = lastAsk;
  // 作废还在途中的旧请求（半死 socket 上的 prompt 可能要很久才超时）。
  attempt += 1;
  inFlight = false;
  error = "";
  if (startedAt > 0) restorePending = true;
  disposeClient();
  lines = [];
  lineId = 0;
  emit();
  if (!last) {
    setPhase("idle");
    return;
  }
  void ask(last.say, last.first, flowKind, { squadId: flowSquadId });
}

// 「重来」/创建成功跳转后的复位：彻底清空这一轮流程（连接、记录、快照、
// 轮询），下一次提问开一个全新会话。
export function reset(): void {
  attempt += 1;
  inFlight = false;
  lastAsk = null;
  startedAt = 0;
  restoredJustNow = false;
  restorePending = false;
  stopPoll();
  clearSnapshot();
  disposeClient();
  lines = [];
  lineId = 0;
  error = "";
  createdGoal = null;
  flowKind = "goal";
  flowSquadId = "";
  perm = null;
  setPhase("idle");
}

// 停止：只中断当前这一轮（session/cancel）——连接和会话都保留，改一句
// 话就能接着继续。握手阶段还没有会话可取消，那就作废这次连接（下一句话
// 会重新握手）。
export function stop(): void {
  if (phase === "connecting") {
    attempt += 1;
    inFlight = false;
    disposeClient();
    setPhase("ready");
    return;
  }
  if (client && session) client.cancel(session);
  // running 轮的 prompt 会以 cancel 结束，ask 的收尾自然把 phase 拉回
  // ready；这里不抢跑，避免和迟到的轮询/收尾打架。
}

export function clearPerm(): void {
  perm = null;
  emit();
}

// 页面挂载时调用：如果有一次没做完的流程（刷新/关闭页面留下的快照），
// 自动接回它的对话并继续轮询「建好了没」。
export async function resumeIfNeeded(): Promise<void> {
  if (client || connecting) return;
  const snap = readSnapshot();
  if (!snap) return;
  startedAt = snap.startedAt;
  if (snap.kind) flowKind = snap.kind;
  restorePending = true;
  try {
    await ensureClient();
    if (phase === "created" || phase === "failed") return;
    setPhase("ready");
    ensurePolling();
    if (lines.length === 0) {
      push({
        role: "system",
        text: "上次的对话记录已过期——补一句话让管家继续，或者点「重来」重新描述。",
      });
    }
  } catch {
    // ensureClient 失败路径已经置 failed + error；这里不用重复。
  }
}
