# 2026-11-16 /goals/new：切 tab 丢对话、建任务半路被杀

- **场景**：新建任务页（/goals/new）和管家对话创建任务 → 切到别的页面再切回来
- **现象**：对话记录全部消失；说了「帮我建任务」但没有新 goal 生成
- **结论（一句话）**：对话连接、记录、轮询全部长在页面组件上——app router 切页=组件卸载=主动关 socket=管家进程被杀（建任务命令没跑完）；本页还是全站唯一没接 ACP 会话持久化（listSessions/loadSession）的聊天界面
- **修复**：对话整体搬进模块级 store（web/lib/new-goal-chat.ts），页面降级为纯视图；切页不再断连；浏览器刷新/关闭用「流程快照 + CLI 会话持久化」两级恢复
- **影响面**：只改 web 前端（1 个新文件 + 1 个重写文件），后端 daemon/machine/CLI 零改动

---

## 1. 问题场景与时间线

用户操作序列（2026-11-16 报告，可稳定复现）：

| # | 动作 | 修复前平台的实际反应（代码事实） |
|---|------|----------------------------------|
| ① | 打开 /goals/new，写需求，点「交给管家创建」 | 页面建立 ws（`/backend/agents/{steward}/acp`）→ daemon `OpenChatForAgent` → 机器 spawn 一个管家 CLI 进程 → `newSession` → 发出建任务指令 |
| ② | 管家开始跑（通常能看到 tool 卡片在动） | CLI 在会话里执行 `flowloop create goal --title ...`，**这条命令需要数十秒**（LLM turn + 落库） |
| ③ | 用户切到别的页面（任务看板/任何 tab） | **app router 卸载本页组件** → 卸载 cleanup 执行 `disposeClient()`：`gen++`、`ws.close(1000)` |
| ④ | （用户看不到，但发生了） | daemon 侧 web ws 断开 → `CloseChat` → 通知机器 `MethodChatClose` → 机器 `chatBridge.cleanup`：关 CLI stdin（3 秒后强杀兜底）→ **管家 turn 被掐死，create goal 没跑完 → 永远没有新 goal** |
| ⑤ | 用户切回 /goals/new | 组件重新挂载：`phase` 回 `idle`、`lines` 为空（**记录只是组件 state，没有持久层**）→ 懒连接重新握手 → `newSession` 开的是**全新会话**——旧对话不在里面 |
| ⑥ | 用户观察 | 记录没了、任务也没建 → 判定「平台有 bug」（正确） |

两个症状同源：**「页面卸载」在旧实现里被当成「用户放弃」处理，但它只意味着「用户去看了别的页面」。**

## 1.2 数据指标

| 指标 | 修复前 | 修复后 |
|------|--------|--------|
| 切 tab 一次杀掉的管家 turn | 1 个（若正在跑：建任务命令半路被杀） | 0 |
| 切 tab 后存活的对括记录 | 0 行（纯组件 state） | 100%（模块 store） |
| 切 tab 后建任务落库成功率 | ≈0%（turn 必死） | 不受切 tab 影响 |
| 「建好了没」轮询的存活范围 | 组件卸载即停（goal 建成了也无人发现/跳转） | store 内持续跑（用户不在本页也能检测+回来补跳转） |
| 对话持久层 | 0 层 | 两级：模块 store（SPA 导航）+ CLI 会话 store（刷新/关闭，按 chat cwd 落盘） |
| 刷新/关页后的恢复能力 | 无（回到空白新会话） | 快照钉 sessionId → `listSessions`/`loadSession` 接回原文 + 续跑轮询 |

## 1.3 讽刺闭环

平台其实早就有这套持久化——**/chat 页（chat-panel.tsx）连接后第一件事就是 `listSessions()` 拉会话列表、`loadSession()` 回放历史**，管家 CLI 侧的 session store 也一直按 cwd 落盘（这正是 /chat 刷新不丢记录的原因）。**/goals/new 是全站唯一自己 newSession、从不 load 的 ACP 聊天界面**，同时又是唯一「卸载=断连」的界面。同一个仓库里，一个页面踩的坑正是另一个页面已经绕开的坑。

---

## 2. 根因链（四环，全部代码证实）

```
[F1 卸载即断连]  page.tsx 卸载 cleanup → disposeClient() → ws.close(1000)
        │        （旧 page.tsx:192-200；懒连接本页没问题——问题是不加区分地关）
        ▼
[F2 进程被杀]    daemon CloseChat → 机器 MethodChatClose → chatBridge.cleanup
        │        （关 stdin→3s 强杀；internal/daemon/chat.go + cmd/flowloop-cli/chat.go）
        │        → 管家 turn 半路死 → flowloop create goal 没跑完 → 无 goal
        ▼
[F3 记录无持久层] lines=useState，无 localStorage/sessionStore/loadSession 任何一层
        │        （对比：steward-chat.tsx 用 localStorage；chat-panel 用 loadSession）
        ▼
[F4 回来即新会话] 重挂载 → ensureClient → newSession（全新、无历史）
                 → 用户看到空白：记录消失 × 任务没建
```

**关键定性**：这不是 daemon/machine 的 bug——CloseChat 的行为（web 断开→收摊）对普通聊天页是正确语义；错在 /goals/new 把「组件生命周期」当成了「对话生命周期」。

## 3. 修复设计

### 3.1 架构反转：页面降级为视图

新增 `web/lib/new-goal-chat.ts`——**对话的全部状态（记录 lines、phase、连接 client、sessionId、建任务轮询、权限弹窗）住进模块作用域**：

- 切页 = 组件卸载 = 只是「视图离开」：ws 不断、CLI 不死、轮询继续跑；
- 回到本页 = 重新订阅 store，现场原样接回（`useSyncExternalStore`）；
- goal 在用户不在本页时建成 → 轮询在 store 里命中 → 回到本页补上自动跳转 `/goals/{id}`；
- 显式「重来」/失败「重试」/建成功跳转后才 `reset()`/`disposeClient()`。

页面（page.tsx）只保留：输入框 state、手动填表弹窗、滚动/慢连接提示、渲染 store 快照。**卸载 cleanup 里不再有任何 dispose 动作。**

### 3.2 浏览器刷新/关闭的两级恢复（module store 之上的兜底）

刷新会杀掉整个 JS 上下文，模块 store 也活不了——这正是 CLI 侧会话持久化的用武之地：

1. **流程快照**（sessionStorage）：一次「交给管家」开始时把 `{sessionId, startedAt}` 钉下来；
2. 重新打开本页 → `resumeIfNeeded()` → 按快照 sessionId 精确 `listSessions`→`loadSession`（历史以 session/update 事件回放成气泡）→ 续跑「建好了没」轮询（`startedAt` 不变）；
3. goal 若在断线期间已建成 → 轮询直接命中 → 跳转；没建成 → 对话上下文还在，补一句话让管家继续。

### 3.3 细节语义（保留/对齐旧实现）

- 失败后的「重试」先 `loadSession` 续上原会话再重发原话（管家看得见它上次做到哪）；「重来」则全清开新会话；
- 「停止」只 cancel 当前轮，连接与会话保留（同旧实现）；
- ws 意外断开（机器掉线/daemon 重启）→ `restorePending` 置位 → 下一次握手自动 `loadSession` 续接；
- 轮询窗口上限 30 分钟（防管家卡死时永久打 /goals），其余轮询口径与旧实现一致（`created_at >= startedAt-2000`）。

## 4. 修复对照表

| 缺陷 | 修复点 |
|------|--------|
| F1 卸载即断连 | page.tsx 卸载无任何 dispose；连接移入模块 store（new-goal-chat.ts `disposeClient` 只被显式动作调用） |
| F2 turn 半路被杀 | 连接不随页面卸载断开 → CLI 进程存活 → create goal 跑完 → goal 落库 |
| F3 记录无持久层 | SPA 内：模块 store；刷新/关闭：快照 + `loadSession` 回放 |
| F4 回来即新会话 | `resumeIfNeeded`/`restorePending`：按快照 sessionId 续接，不再盲目 `newSession` |

## 5. 遗留与取舍

- **store 空转持有连接**：对话结束后（ready 且无 goal）连接保持至用户「重来」或轮询窗口到点——一个空闲管家 CLI 会多活一会儿。换来的正确性（半路任何时刻切页都零损失）远大于成本；后续可加空闲超时。
- **刷新恢复不保证逐字节还原**：系统气泡（「已交给管家…」）是本地 UI 文案，不进 ACP 历史；刷新回放的是真实对话内容。
- **快照以 sessionStorage 为界**：新开浏览器 tab 不接力（同一 tab 刷新/返回可恢复）——「切 tab」的主诉场景本来就不杀 store，此兜底只管「整页刷新/关闭」。
