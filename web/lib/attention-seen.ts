"use client";

// ── 「待我处理」红点的已读账本（app 式通知语义）──
// 侧栏徽标不再显示"待办总量"（总量常年在，红点就常年在，用户无从消费），
// 改为显示"新出现、且还没看过"的数量：
//   · 打开工作台 = 看过了 → 当前清单全部记为已读，侧栏红点立即消失；
//   · 同一件事状态位变了（如 失败 → 待审），会生成新 key，红点再次亮起；
//   · 新人工门、新失败任务同理。
// key 设计是「身份 + 状态位」，不是单纯的 id——避免"处理完又变待审"这种
// 正常流转被误判成已读。账本落在 localStorage，刷新/重开浏览器不丢；
// 上限 800 条 FIFO 淘汰，最早的旧记录不值得留恋。

const STORAGE_KEY = "flowloop-attention-seen-v1";
const MAX_SEEN = 800;

let cache: string[] | null = null;
let version = 0;
const listeners = new Set<() => void>();

function load(): string[] {
  if (cache) return cache;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    const parsed = raw ? (JSON.parse(raw) as unknown) : [];
    cache = Array.isArray(parsed) ? (parsed as string[]) : [];
  } catch {
    cache = [];
  }
  return cache;
}

function persist(next: string[]) {
  cache = next.slice(-MAX_SEEN);
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cache));
  } catch {
    // 隐私模式等写不进去就算了：本会话内仍然生效，只是不跨会话。
  }
}

function notify() {
  version += 1;
  listeners.forEach((fn) => fn());
}

/** 供 useSyncExternalStore 订阅已读账本的变化。 */
export function subscribeAttentionSeen(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

/** 变更序号（只增不减），作为 useSyncExternalStore 的快照。 */
export function attentionSeenVersion(): number {
  return version;
}

/** 与工作台「待我处理」同一口径的过滤条件，nav 与页面共用，防止两处口径漂移。 */
export function isAttentionGoal(g: { status: string; attention?: string }): boolean {
  return g.status === "review" || g.status === "failed" || Boolean(g.attention);
}

/** goal 的提醒 key：状态位参与 key，同一任务状态翻转算一条新提醒。 */
export function attentionKeyOfGoal(g: {
  id: string;
  status: string;
  attention?: string;
  review_phase?: string;
}): string {
  return `g:${g.id}:${g.status}:${g.attention ?? ""}:${g.review_phase ?? ""}`;
}

/** 人工门的提醒 key：同一节点重新持门（held_since 变化）算新提醒。 */
export function attentionKeyOfGate(gate: {
  goal_id: string;
  node_key: string;
  held_since?: string;
}): string {
  return `gate:${gate.goal_id}:${gate.node_key}:${gate.held_since ?? ""}`;
}

/** 打开工作台（或清单变化）时调用：把当前清单全部记为已读。 */
export function markAttentionSeen(keys: string[]): void {
  const cur = load();
  const set = new Set(cur);
  let changed = false;
  for (const k of keys) {
    if (!set.has(k)) {
      set.add(k);
      changed = true;
    }
  }
  if (changed) {
    persist([...set]);
    notify();
  }
}

/** keys 里还有多少条没看过——这就是侧栏红点该亮的数字。 */
export function unseenAttentionCount(keys: string[]): number {
  const seen = new Set(load());
  let n = 0;
  for (const k of keys) {
    if (!seen.has(k)) n += 1;
  }
  return n;
}
