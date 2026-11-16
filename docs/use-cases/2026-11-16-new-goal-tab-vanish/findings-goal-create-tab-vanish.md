# Findings：/goals/new 切 tab 丢对话、建任务半路被杀（2026-11-16）

一页结论（按影响排序）：

1. **F1_fatal** 页面卸载 cleanup 主动关 ws：切 tab（app router 正常导航）被当成「用户放弃」，连接必断。
2. **F2_fatal** 断连的下游是杀进程：daemon CloseChat → 机器关 CLI stdin（3s 强杀兜底）——管家正在跑的 `agentwork create goal` 半路死，**任务永远建不出来**（用户报告的第二个症状）。
3. **F3_major** 对话记录只是组件 state，没有任何持久层——切 tab 即清零（第一个症状）。
4. **F4_major** 重挂载后盲目 `newSession`：不查、不 load，回来必然是空白新会话。
5. **P1_positive** 平台早有全部零件：CLI session store 按 chat cwd 落盘、`session/list`+`session/load` 跨进程可用（/chat 页 chat-panel.tsx 一直在用）；steward-chat.tsx 用 localStorage 存 lines。/goals/new 是唯一没接这套的 ACP 界面。
6. **P2_positive** CloseChat→杀 CLI 对普通聊天页是正确语义（防止僵尸 CLI）；问题只在 /goals/new 把组件生命周期当成了对话生命周期。

---

## 二、逐项证据

### F1 卸载即断连

旧 `web/app/goals/new/page.tsx`（重写前，667 行）：

- :192-200 卸载 effect cleanup：`attemptRef++` + `disposeClient()`
- :103-114 `disposeClient`：`genRef++`（作废本代回调）→ `inflight.close()`/`client.close()`
- `web/lib/acp.ts:233-254` `close()`：`ws.close(1000, "client closed")`（真 close frame，中间盒不会挂半开 socket）
- 卸载触发源：Next.js app router 客户端导航（切任何 tab）都会卸载页面组件——用户「看一眼任务列表」= 一次完整断连

### F2 断连 → 杀进程 → turn 半路死

- `internal/daemon/chat.go`（416 行）：web ws 断开 → `CloseChat` → 校验 identity → `Notify(MethodChatClose)`
- `cmd/agentwork-cli/chat.go`（461 行）：`MethodChatClose` → `chatBridge.cleanup`：关 stdin（优雅退出留全历史）→ 3s 强杀兜底
- 时序：管家 turn 通常数十秒（LLM+CLI 命令），用户「看到气泡在动就切走」大概率落在 turn 中段 → `create goal` 未执行/未落库
- 第二症状「没有新的 goal 生成」的完整解释：不是管家不干活，是它**被打死了**

### F3 记录无持久层

- 旧 page.tsx:47 `const [lines, setLines] = useState<Line[]>([])`——与 localStorage、sessionStorage、ACP session 均无关系
- 对比同仓库：
  - `web/components/steward-chat.tsx`：`historyKey(agentId)` localStorage，100 条上限
  - `web/components/chat-panel.tsx:94-122`：连接后 `listSessions()`；选中后 `loadSession(s.sessionId, s.cwd)` 回放历史

### F4 重挂载即新会话

- 旧 page.tsx:118-190 `ensureClient`：只有一条会话路径 `sessionRef.current = await client.newSession()`——没有 listSessions、没有 loadSession、没有「上次的对话」概念
- 回放契约（`web/lib/acp.ts`）：`loadSession` 期间历史以 session/update 事件重放（user_message_chunk/agent_message_chunk/tool_call/tool_call_update），`user` 消息只在回放里出现（实况用户气泡由前端本地推）——这决定了恢复实现的 `replaying` 门控

---

## 三、修复设计（已实现）

### 架构反转：store 活在模块作用域，页面只是视图

新 `web/lib/new-goal-chat.ts`（模块 store）+ 重写 `web/app/goals/new/page.tsx`（薄视图）：

- store 持有：`phase`/`lines`/`error`/`perm`/`createdGoal` + `client`/`session`/`startedAt` + 轮询 interval；
- 页面用 `useSyncExternalStore(subscribeNewGoal, getNewGoalVersion)` 订阅，`getNewGoalState()` 取快照；
- **卸载零动作**：page.tsx 没有任何 unmount cleanup 碰连接——切 tab 只是视图离开；
- `disposeClient` 只被显式动作调用：`reset()`（重来/建成跳转）、`retry()`（失败重试）、`stop()`（握手中叫停）。

### 两级恢复

1. SPA 导航（用户报告的主场景）：module store 本身就是持久层，什么都不用做；
2. 整页刷新/关闭：`sessionStorage` 流程快照 `{sessionId, startedAt}`（SNAPSHOT_KEY=`agentwork-new-goal-flow`，24h 过期）→ 挂载时 `resumeIfNeeded()` → `ensureClient` 里按快照 sessionId 在 `listSessions()` 里**精确匹配**（不拿最新，防误载 /chat 开的别的会话）→ `loadSession` 回放（`replaying=true` 门控：user 消息只在此期间上屏）→ 续跑轮询（`startedAt` 不变）→ 若断线期间 goal 已建成，直接命中跳转。

### 关键语义保留（与旧实现/聊天页对齐）

- 代际防护全套保留在 store：`gen`（连接代，旧 socket 迟到回调丢弃）、`attempt`（提交代，失败后迟到收尾不改 UI）、`inFlight`（同帧连点闸）、`connecting`（握手去重，StrictMode 双挂载安全）；
- 「停止」= `session/cancel`（连接保留）；失败「重试」= loadSession 续原会话再重发原话；「重来」= 全清 + 新会话；
- ws 意外断（机器掉线）：`onClose` → `restorePending=true` → 下次握手自动续原会话（CLI 的 session store 落盘了它）；
- 轮询口径不变：`created_at >= startedAt-2000`，2s 间隔；新增 30 分钟窗口上限；
- ws URL/懒连接/权限弹窗/双输入框/示例卡/手动填表兜底：全部保留。

### 修改面

| 文件 | 改动 |
|------|------|
| `web/lib/new-goal-chat.ts` | **新增**（store：状态机+连接+轮询+快照恢复，~470 行） |
| `web/app/goals/new/page.tsx` | **重写**（667 行 → 薄视图：订阅 store+resume+created 跳转） |
| 后端（daemon/machine/CLI） | **零改动**——CloseChat 语义保持 |

### 验证

- `npx tsc --noEmit` exit 0；
- `close()` 契约核实（acp.ts:204）：主动 close 不触发 owner `onClose`——store 的 `disposeClient` 不会误报「连接断开」；
- `loadSession`→prompt 契约核实（chat-panel.tsx:120-156）：load 后用同一 sessionId prompt——store 同款。

### 已知取舍

- 对话结束后连接持有至显式 reset/轮询窗口到点：一个空闲管家 CLI 多活一会儿（后续可加空闲超时）；
- 刷新回放不含本地系统气泡（「已交给管家…」是 UI 文案，不进 ACP 历史）；
- 快照在 sessionStorage：同 tab 刷新/返回可恢复，跨浏览器 tab 不接力——主场景（SPA 切 tab）不依赖此兜底。
