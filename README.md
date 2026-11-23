<div align="center">

# OuroFlow

**An AI task pipeline OS — you define "done" in natural language, CLI agents run the whole loop unattended.**

goal → execution → machine verification → human gate → automatic delivery

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v0.0.1--beta.1-blue)](https://github.com/Sinkear/ouroflow/releases)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS-lightgrey)](#-quick-start)

[English](README.md) · [简体中文](README.zh.md)

[Highlights](#-highlights) · [Quick Start](#-quick-start) · [Core Concepts](#-core-concepts) · [Collaboration](#-collaboration) · [Architecture](#-architecture) · [CLI](#-cli-tool) · [Docs](#-docs-upgrades--templates) · [Tech Stack](#-tech-stack) · [Acknowledgments](#-acknowledgments)

**Single process · single machine · single user · no auth.** Local SQLite state, in-process event bus, Go daemon + Next.js web UI.

</div>

## 🌟 Highlights

- **Full unattended loop** — goals, runs, machine verification, structural guards, human gates, automatic delivery (merge + re-verify + push)
- **Acceptance policy per domain** — you state "what done means" in natural language; a processor agent compiles it into executable checks (verify commands, guards, gates) which you confirm once
- **Sub-goals with changes** — the owner splits work into sub-goals (own assignee, own worktree, machine or agent verification); verified sub-goals produce **Changes** with revisions the owner integrates — conflicts wake the assignee to rework automatically
- **Four collaboration behaviors** — Comment / Consult / Handoff / Sub-goal, exposed as the `ouroflow` CLI with owner-only permissions enforced server-side
- **Multi-protocol & transport** — ACP / JSONL / JSON-RPC over stdio / ws / tcp; agents can carry their own extra MCP servers
- **Triggers** — Web, cron schedules, GitHub/GitCode issues + pull requests (poll; issues also webhook), Feishu IM inbound (@-bot task creation)
- **Task acceptance modes (HITL / HOOTL)** — goals and schedules are created as HITL (human recheck at gates — the default) or HOOTL (the goal finishes directly when its run completes, skipping the human gate)
- **Remote skill sources** — attach a GitHub/GitCode repository (branch, path, token) to a skill, compare local vs remote, pull remote updates, push local edits, or submit a pull request; sync and PR are verified against the provider API, and a pull immediately refreshes the skill copies of that agent's LIVE sessions
- **Feishu notifications** — approval cards (approve/reject right in IM), completion/failure pushes, a daily digest
- **Real-time web UI** — live event stream, execution timeline, sub-goal / change / verification panels
- **Conversation-first creation** — a built-in skill rides every steward conversation, so goals, squads, agents, schedules and blueprints are created by describing them in chat
- **Agent chat (ACP bridge)** — the web is a real ACP client for each agent: `GET /agents/{id}/acp` relays ACP frames unparsed to the machine-side CLI; permission requests surface as an approval modal, and turns can be interrupted or retried on a fresh session

> **One-line mental model:** a **goal** is a work item and the sole holder of state authority; a **run** is one agent's turn on it — runs report up, never decide. The platform judges completion; you hold the gates.

## 🚀 Quick Start

### Prerequisites

- Go 1.26+
- Node.js 20+
- npm 10+

### Backend

```bash
./build.sh   # builds build/ouroflow (CLI) + build/ouroflow-daemon
             # with one shared version stamp (OUROFLOW_COMPILE_VERSION)

# Start the daemon (default :47813)
./build/ouroflow-daemon

# And on the machine that runs the agents:
./build/ouroflow connect
```

<details open>
<summary><b>Frontend</b></summary>

```bash
cd web
npm install
npm run build && npm start
```

</details>

<details open>
<summary><b>First goal</b></summary>

1. Create a **domain** (project): repo URL + acceptance policy in natural language (or leave it unfrozen to force a human checkpoint)
2. Create a **runtime** (e.g. `opencode acp --pure`, stdio + acp) and an **agent**
3. Run `ouroflow connect` on your machine — the daemon dispatches runs to it via `run.poll` (pull model)
4. Create a **goal**, assign the agent, and watch the loop run: execution → verification → review → your approval → automatic delivery
5. Or chat with any agent directly from the Agents page — a per-agent ACP session with the persona/skills staged fresh at every open

</details>

## 🧩 Core Concepts

<details>
<summary><b>The object model — 13 concepts, one table</b></summary>

| Concept | What it is |
|---|---|
| **Domain** (Project) | An asset/evolution domain: shared repo + acceptance policy (NL intent compiled into checks) + default gates. |
| **Goal** | A work item (product plane). Status: backlog → active → review → done / failed / cancelled. The sole holder of state authority. |
| **Run** | One agent's turn on a goal. Status: queued → running → completed / failed / cancelled, plus a **role** (owner / subgoal / consult / review / verify). No authority — it reports to the goal layer. |
| **Runtime** | A launch spec — transport (stdio/ws/tcp) + provider (acp/jsonl/jsonrpc) + executable/args or endpoint + env. |
| **Agent** | A runtime + persona (system prompt / model / env / extra MCP servers / max concurrency). |
| **Chat** | A direct ACP conversation with an agent over `GET /agents/{id}/acp` — the daemon relays frames unparsed to the machine-side CLI; sessions live in the CLI's own store, permissions are answered in the panel. |
| **Squad** | A routing group that does no work itself: goals route to its leader, who delegates via sub-goals; members with role=reviewer are auto-pulled into review checkpoints. |
| **Sub-goal** | A work item split off a goal (not a child goal — no recursion). Own assignee + optional agent verifier; machine retries (≤3) and verifier rejections are counted separately. |
| **Change** | A sub-goal's logical deliverable: ready → integrating → integrated, or conflict → assignee rework → revision N+1. The owner integrates it via the `integrate_change` tool. |
| **Verifier** | Machine (domain verify commands) by default, or a named agent issuing structured verdicts (`verify_sub_goal`). |
| **Gates** | Human checkpoint rules from the acceptance policy (merge, diff_contains, ...); weak verification forces a checkpoint. |
| **Mention** | The Consult primitive: a structured URI in a comment pulls an agent into a read-only guest run; the requester resumes automatically after the answer. |
| **Schedule** | A cron template that clones a goal per firing (idempotent, timezone-aware). |
| **Trigger** | Web, cron, GitHub/GitCode issues + PRs (poll, deduped by source ref; issues also webhook), Feishu IM inbound. |

</details>

## 🤝 Collaboration

<details>
<summary><b>Four behaviors, one CLI, owner permissions enforced server-side</b></summary>

| Behavior | CLI command | Who may |
|---|---|---|
| **Comment** (say) | `ouroflow goal comment --text T` | anyone |
| **Consult** (ask) | a mention in `goal comment`: `[@Name](mention://agent/<id>)` | the goal's owner |
| **Handoff** (transfer) | `ouroflow goal assign <to-agent-id> [--note N]` | the goal's owner |
| **Sub-goal** (split) | `ouroflow subgoal create --title T --assignee A` / `subgoal cancel <id>` | the goal's owner |

Agents coordinate exclusively through the `ouroflow` CLI (a shim of the daemon binary, on the agent's PATH at spawn). Every command carries the per-run token (`OUROFLOW_TOKEN`), which the daemon's `/rpc` endpoint resolves to the run's `(goal, agent, role)` — the token is the ONLY identity, self-reported ids are ignored, and the handoff owner check is enforced server-side. Plus `subgoal verify`, `change integrate`, and the `*_list` commands. Full model in [Collaboration.v2.md](docs/design/Collaboration.v2.md).

</details>

## 🧱 Architecture

<details>
<summary><b>The whole system on one diagram</b></summary>

```
             ┌────────────── HTTP API + WS hub ──────────────┐
             │   goals/runs/sub-goals/changes/…               │
             │   + /agents/{id}/acp (ACP chat relay)          │
             ▼                                               ▼
       service layer (state authority)                web UI (Next.js)
             │  bus.Publish (after commit)              │ ACP client (chat)
             ▼                                           │
        SQLite (truth) ── daemon scheduler ──────────────┤
                             │ run.poll / chat.*        │
                             ▼                           ▼
                      ouroflow connect   ◄──  /connect (JSON-RPC)
                             │  runTask, chat bridge (spawn + relay)
                             ▼
                      runtime.Open(spec)
                             │  acp | jsonl | jsonrpc
                             ▼
                      agent CLI subprocess ◄── ACP frames relayed unparsed
                       (stdio/ws/tcp)
                       └─ fs/terminal RPC + ouroflow CLI (/rpc)
                          (worktree, collaboration commands)
             ┌───────────────┬─────────────────────────────┘
             ▼               ▼
      machine verify /   review gate → deliver
      guards (domain)    (human approve → merge+re-verify+push)
```

- **Event ≠ truth** — the bus is a wakeup hint; every transition is a conditional DB transaction, idempotent under replay
- **Machine executor** — `ouroflow connect` holds a persistent JSON-RPC link and pulls work (`run.poll`); it executes runs, hosts the per-agent chat bridge (spawns the CLI, relays ACP frames unparsed, tears down gracefully with stdin EOF before the kill)
- **Per-run worktrees** — `$OUROFLOW_HOME/runs/<runID>` (default `~/.ouroflow`) ephemeral git worktrees against per-domain bare repos; crash recovery replays terminal runs and re-derives attention at startup
- **Coordinator** — derived OwnerAttention (integration / recovery / user_action) wakes the owner exactly when there is work for it

</details>

## 🔧 CLI Tool

<details>
<summary><b>Machine executor + human debugging tool</b></summary>

`ouroflow` is the **machine executor** and a human debugging tool (agents use the `ouroflow` CLI shim for collaboration):

```bash
ouroflow connect               # the machine side: pulls dispatches, runs them, hosts chats
ouroflow goal list --limit 5   # debugging commands (default http://localhost:47813,
ouroflow stats                 #   or set OUROFLOW_SERVER_URL)
```

</details>

## 📚 Docs, Upgrades & Templates

- [Paths and data layout](docs/PATHS.md) — every directory/env var, and what must survive an upgrade
- [Upgrade without losing data](docs/UPGRADE.md) — stop → backup → replace only bin/web → start, including JEM
- [Run identity & instruction ownership](docs/IDENTITY.md) — a run's identity comes from THIS run alone: the shared-workdir hijack postmortem and the three-layer fix
- [Turn your workflow into a template](docs/TEMPLATES.md) — export with `ouroflow-daemon export-template`
- [Artifact-swap skill](skills/ouroflow-artifact-swap/SKILL.md) — the same data-safe flow, portable to the target host
- [Design baseline](docs/design/DESIGN.md) — the frozen product & architecture document
- [Collaboration model](docs/design/Collaboration.v2.md) — the full owner/role/permission model

## 🧪 Tech Stack

<details>
<summary><b>What it's built on</b></summary>

| Layer | Stack |
|---|---|
| **Backend** | Go 1.26, SQLite (modernc), gorilla/websocket, robfig/cron, MCP go-sdk |
| **Frontend** | Next.js 16, React 19, Tailwind CSS 4, TanStack React Query |
| **Protocols** | ACP, JSONL, JSON-RPC |
| **Transports** | stdio, WebSocket, TCP |
| **Notifications** | Feishu (approval cards, digest, IM inbound) |
| **Issue triggers** | GitHub, GitCode |

</details>

## 🙏 Acknowledgments

OuroFlow is an independent derivative distribution of [agentwork](https://github.com/yusheng-g/agentwork), maintained by [Sinkear](https://github.com/Sinkear).

| | |
|---|---|
| Upstream project | [yusheng-g/agentwork](https://github.com/yusheng-g/agentwork) |
| Maintainer | [Sinkear](https://github.com/Sinkear) |
| License | MIT — modifications © 2026 Sinkear, original © its authors |

---

<div align="center">

**OuroFlow** — you hold the gates, the platform runs the loop.

[⬆ Back to top](#ouroflow)

MIT © 2026 Sinkear

</div>
