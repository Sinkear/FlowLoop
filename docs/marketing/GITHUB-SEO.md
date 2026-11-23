# GitHub SEO Kit — OuroFlow

> Scope: repo metadata (`About` description + topics), social preview, README/site
> discoverability, and off-repo launch checklist for `github.com/Sinkear/ouroflow`.
> Written 2026-11-23. GitHub topic traffic is the repo count on each `/topics/<name>`
> page at that date (a demand proxy, not search volume).

---

## 0. TL;DR — paste these

**About / description** (326 chars, limit 350):

```text
Open-source, self-hosted multi-agent orchestration with human-in-the-loop gates for self-improving workflow automation (RPA-style). Define 'done'; coding agents execute, machine-verify, merge and push; a workflow RSI loop proposes the next round — you approve proposals, the next goal and gates. Go + Next.js, ACP/MCP, SQLite.
```

**Topics** (20, limit 20):

```text
ai-agents, ai-agent, multi-agent, agent-orchestration, agentic-ai, rpa, coding-agents, autonomous-agents, human-in-the-loop, recursive-self-improvement, mcp, model-context-protocol, opencode, agent-client-protocol, workflow-automation, self-hosted, golang, nextjs, sqlite, developer-tools
```

This revision adds the two terms the product actually differentiates on — **RPA-style
workflow automation** and **workflow RSI** — but states the human boundary explicitly
(`you approve proposals, the next goal and gates`). It no longer implies an unattended
closed loop. See §1 for why RPA is a qualifier, not the category, and §8 for the
volume data behind that choice.

Paste as one comma-separated line in **repo → About (gear icon) → Topics**. GitHub
lowercases and hyphenates automatically.

**Website field**: `https://sinkear.github.io/ouroflow/` — but only after the Pages
deploy is fixed (§7, P0-1). Today that URL is a 404, and a dead homepage link hurts
trust more than no link.

---

## 1. Why this description

| Element | Choice | Reason |
|---|---|---|
| Lead keyword | `multi-agent orchestration` | Highest-intent category phrase with real search demand; front-loaded in the first 40 chars. |
| Human boundary | `human-in-the-loop gates` + `you approve proposals, the next goal and gates` | The loop is not an unattended closed loop: proposal adoption, the next round's goal and every gate stay with the human. This is a feature, not a disclaimer. |
| Self-iteration | `workflow RSI` | Names the W-RSI loop (`docs/rsi/self-iter-playbook.md`): benchmark → optimizer proposal → human review → build → next round. |
| Category bridge | `RPA-style workflow automation` | Captures RPA demand **as a qualifier**. OuroFlow automates software/workflow processes, not desktop UI robots; "RPA-style" is the honest bridge. |
| Differentiator | `define 'done'` → execute → machine-verify → merge + push | The actual product loop, written as a benefit, not a feature list. |
| Proof / integrations | `ACP/MCP`, `SQLite`, `Go + Next.js` | Captures protocol/stack queries without keyword stuffing. |
| Deployment | `self-hosted` | Matches the `self-hosted` topic and the local daemon/SQLite reality. |

- 326 chars, under GitHub's 350 cap.
- First sentence is **131 chars** and ends with the RPA/RSI hook; Google/Bing typically
  truncate around 155–160, so the snippet carries the category, the human boundary and
  the start of the loop instead of cutting mid-claim. GitHub's OG card uses the full string.
- It does **not** repeat the repo name as the first words ("OuroFlow is…"), so the
  description adds information instead of restating the `<title>`.
- The human-approval claim is evidence-backed: `docs/rsi/self-iter-playbook.md` T6 makes
  human approval mandatory before any platform-code change; `docs/use-cases/2026-11-21-blueprint-self-iteration/timeline.md`
  records `human_wait` 1,735.5m ≈ 28.9h, **78.1%** of wall+wait across 50 runs. Do not
  replace this with "autonomous" or "fully unattended".
- "RPA-style" is deliberately a modifier, not the category. The repo contains **no RPA
  feature or docs**; only `ouroflow-rpa-*` sandbox names in use-case records. Leading with
  "RPA" would attract UiPath/Power Automate buyers and invite a relevance complaint.
- **Claude Code is intentionally absent** — see §2, "Deliberately excluded". Add it only
  after a tested ACP adapter is documented in the README; it is the biggest untapped
  topic (128,694 repos), not a claim to make early.

**Site `<meta name="description">`** should be shorter (120–160 chars); use:

```text
Human-in-the-loop AI agent orchestration with workflow RSI: agents execute, verify, merge; you approve proposals, goals and gates. Self-hosted Go + Next.js.
```

(156 chars — does not repeat the title tag, keeps the human boundary, and ends with
the stack.)

---

## 2. Topics — the 20 and why

GitHub topics are tags, not ranking magic: they feed GitHub search (`topic:<name>`),
topic landing pages, and related-repo suggestions. The strategy is **6 category terms
+ 2 intent terms + 2 RSI/RPA terms + 4 integration/protocol terms + 2 workflow terms
+ 4 stack/deployment terms**, all relevant.

| # | Topic | Repos | Role | Fit |
|---:|---|---:|---|---|
| 1 | `ai-agents` | 211,953 | Category | Broadest AI-agent tag; top-of-funnel. |
| 2 | `ai-agent` | 49,303 | Category | Singular variant thousands of repos and searchers still use. |
| 3 | `multi-agent` | 24,942 | Category | Squads / sub-goals / concurrent runs. |
| 4 | `agent-orchestration` | 5,488 | Exact intent | The precise category phrase in the description. |
| 5 | `agentic-ai` | 30,420 | Category | Current umbrella term buyers search. |
| 6 | `developer-tools` | 97,742 | Category | Broad dev-tool discovery; already on the repo. |
| 7 | `coding-agents` | 9,968 | Intent | Coding-agent audience. |
| 8 | `autonomous-agents` | 6,959 | Intent | Unattended execution *between* gates (HOOTL) is a core mode. |
| 9 | `human-in-the-loop` | 10,009 | Differentiator | The default mode; the description now states exactly what humans approve. |
| 10 | `rpa` | n/a† | RPA bridge | Largest legacy automation audience. Only honest as "RPA-style workflow automation", not "RPA platform". |
| 11 | `recursive-self-improvement` | n/a† | RSI niche | Exact W-RSI concept tag; low traffic, high differentiation. |
| 12 | `mcp` | 129,235 | Integration | MCP workspace server; huge protocol audience. |
| 13 | `model-context-protocol` | 47,770 | Protocol | Spelled-out MCP query; captures a second search form. |
| 14 | `opencode` | 13,022 | Integration | The documented ACP runtime; differentiates from Claude-only tools. |
| 15 | `agent-client-protocol` | 686 | Exact protocol | ACP is a differentiator; tiny but high-intent. |
| 16 | `workflow-automation` | 10,816 | Use case | Goal → run → verify → deliver is a workflow; pairs with `rpa`. |
| 17 | `self-hosted` | 61,313 | Deployment | Local daemon, local SQLite, no cloud dependency. |
| 18 | `golang` | 156,206 | Stack | Canonical Go tag; replaces `go`. |
| 19 | `nextjs` | 223,579 | Stack | Web UI stack; second-largest tag used. |
| 20 | `sqlite` | 71,027 | Stack | Embedded DB; reinforces self-hosted deployment. |

† GitHub returned HTTP 429 for these topic pages during the 2026-11-23 audit; the
counts were not captured. Both are valid topic names (GitHub attaches them when used),
but re-check their landing pages before finalizing. The `rpa` case does **not** rely on repo count: Google
Trends puts RPA far above workflow automation in search interest (§8), and Stack
Overflow's `rpa` tag has 707 questions. The `recursive-self-improvement` case is the
opposite — near-zero search demand; it is here for exact-concept discovery, not traffic.

### Changes vs. the current 8
### Changes vs. the current 8

| Current | Action | Why |
|---|---|---|
| `agent-runtime` | **drop** | 1.3k repos; a sub-concept, weaker intent than the 20 above. |
| `ai-agents` | keep | — |
| `developer-tools` | keep | — |
| `go` | **→ `golang`** | 156k vs 107k; `golang` is the canonical tag. |
| `multi-agent` | keep | — |
| `nextjs` | keep | — |
| `orchestration` | **drop** | 7.6k but redundant with `agent-orchestration`; keep the specific one. |
| `task-board` | **drop** | 195 repos; a UI feature, not the product category. |
| `task-orchestration` | **drop** | 221 repos; replaced by the `rpa` bridge for the self-iteration story. |
| `ai-coding-agent` | **drop** | 1,326 repos; `coding-agents` already covers the intent, and the slot goes to `recursive-self-improvement`. |
| `rpa` | **add** | Large legacy audience; qualified as "RPA-style" in the description, never as "RPA platform". |
| `recursive-self-improvement` | **add** | Exact W-RSI concept; low traffic, high differentiation. |

### Deliberately excluded

- `claude-code` (128,694): **the largest missed audience, but not supported today.**
  `internal/proto/jsonlbackend/backend.go` says "NOT IMPLEMENTED for MVP … the acp
  backend is the only wired protocol today"; the README never mentions Claude Code;
  the only reference is an example in `internal/service/assets/ouroflow-chat-create/SKILL.md:32`
  and a test fixture (`claude --acp`). The Claude Code CLI reference has no `--acp`
  flag (checked 2026-11-23). Claiming it invites a broken first-run and issue churn.
  **Unlock:** ship + document a tested ACP adapter (e.g. a wrapper that bridges
  Claude Code to ACP), add a "Tested with" line to the README, then add the topic and
  put it back in the description. It is worth more than any other single tag.
- `llm` (194,238): too broad — it surfaces model/framework repos, not orchestration;
  the description still contains the concept.
- `devops` / `ci-cd`: the product automates delivery but is not a CI system; risks
  wrong-audience stars and unsubscribes.
- `acp` (1,364): ambiguous acronym (also Audio Control Protocol, etc.) and already
  covered by the spelled-out `agent-client-protocol`; hold it until ACP is a known
  search term in this niche.
- `local-first` (36,342): SQLite state is local, but `ouroflow connect` runs agents on
  another machine and the web UI is built separately (`README.md:56-67`). Strict
  local-first readers would call that a stretch; don't spend a slot on it.
- `task-pipeline` (8) / `ai-task-automation` (8): near-zero traffic and no
  discoverability; the description carries the phrase instead.
- `workflow-rsi` / `agentic-rpa`: **not usable as GitHub topics** — GitHub search
  returned 0 repositories tagged with either on 2026-11-23, so they have no topic
  landing page and no audience. Keep the phrases in the description and README
  instead; `recursive-self-improvement` and `rpa` are the real tags.
- `agent-os` (276): small and ambiguous (competing projects use it).
- `feishu` / `lark` (1,892 / 1,137): real integration, but audience is regional and
  better reached by the Chinese copy (§3) than by an English repo topic slot.

---

## 3. Alternative descriptions

Keep one live at a time; GitHub has no A/B test, so change every 2–4 weeks and
compare GitHub Traffic → referrers/views, not stars alone.

**A. Integration-first (best for the ACP / opencode audience, 220 chars):**

```text
Orchestrate opencode or any ACP/MCP CLI agent around acceptance criteria you write in plain English. Agents execute, machine-verify, clear a human gate, then merge and push. Open-source, self-hosted Go + Next.js, SQLite.
```

**B. Category-first (brand-led, clearest, 243 chars):**

```text
The AI task pipeline OS: open-source and self-hosted, where natural-language acceptance is the contract. Coding agents execute, machine-verify, clear a human gate, then auto-deliver the merge. Multi-agent squads, ACP/MCP, Go + Next.js, SQLite.
```

**C. Problem-first (higher CTR on social, slightly less keyword-dense, 244 chars):**

```text
Stop babysitting coding agents. Define 'done' once and OuroFlow runs the loop: execute, machine-verify, human gate, merge and push, with squads for parallel work. Open-source, self-hosted Go + Next.js, ACP/MCP, SQLite, GitHub & Feishu triggers.
```

**D. RSI-first (differentiation-led; lowest search traffic, 250 chars):**

```text
Open-source workflow RSI (recursive self-improvement) for AI coding agents: define 'done', agents execute, machine-verify and merge, then propose the next round for your approval. Human-gated proposals, goals and gates. Go + Next.js, ACP/MCP, SQLite.
```

Ranking for the current goal: **the §0 description first** (demand terms + RPA/RSI
differentiator + explicit human boundary), then D for a launch post or an RSI-focused
README section, then A for the opencode/ACP audience. B and C are useful for the site
hero and social, not for the repo About field.

**Chinese / GitCode / 公众号 variant:**

```text
OuroFlow：开源、自托管的 AI 智能体编排与自迭代工作流平台。用自然语言定义「完成」，编码 Agent 执行、机器验证、合并推送；W-RSI 循环提出下一轮改进，但提案、下一轮目标与人工门都由你审批。支持 RPA 风格的工作流自动化、ACP/MCP、SQLite、Go + Next.js。
```

---

## 4. Social preview (currently the default GitHub card)

Upload **1280×640**, PNG/JPG, <1 MB, in **Settings → Social preview**. It is the
image on every X/Slack/Discord/WeChat share — a plain repo card loses clicks.

Design brief:

- Dark background (`#0B0F14`), OuroFlow wordmark left, tagline right:
  **“Define ‘done’. Agents run the loop.”**
- Four-node loop: `Goal → Run → Verify → Gate → Ship`, with `Agent` and `Squad`
  as actors; one accent color for the gate (human approval).
- Bottom strip: `Go · Next.js · ACP/MCP · SQLite · Self-hosted`, plus
  `github.com/Sinkear/ouroflow`.
- Export a 1200×630 OG variant for the website (`site/public/og.png`).
- Add descriptive `alt` text wherever the image appears.

---

## 5. README SEO (GitHub README is indexed and heavily quoted by AI answers)

Current state: 222 lines, English, 4 shields badges, **no product image**, no FAQ,
no CI badge. Fix in this order:

1. **First screen**: badges + one-sentence keyword subtitle under the H1:
   > OuroFlow is an open-source **AI task pipeline OS**: **multi-agent orchestration**
   > for **coding agents** with **human-in-the-loop gates** and a human-gated
   > **workflow RSI** loop.
   Keep it human-readable; don't turn the first paragraph into a keyword list.
2. **Add 2–3 visuals**: goal board, run/verification timeline, gate approval.
   Use `docs/images/` or `site/public/`; filenames like
   `ouroflow-goal-board.png`; descriptive alt text.
3. **CI badge**: the repo already has `.github/workflows/ci.yml`; add its badge
   next to the existing four. A green build is a trust signal.
4. **Add an FAQ** (also becomes `FAQPage` JSON-LD on the site):
   - What is an AI task pipeline OS?
   - How is OuroFlow different from an agent framework (LangGraph, CrewAI)?
   - Which CLI agents work today — and does Claude Code work? (Answer honestly: opencode
     is documented; Claude Code needs a tested ACP adapter first.)
   - Is it really self-hosted / local-first?
   - Can an agent merge without human approval?
5. **Add a short comparison table** (OuroFlow vs. agent frameworks vs. cloud
   coding agents) on dimensions: acceptance criteria, machine verification,
   human gate, delivery, self-hosted.
6. **Fix the factual inconsistency**: the heading says "13 concepts" but the table
   lists **14** (Domain, Goal, Run, Runtime, Agent, Chat, Squad, Sub-goal, Change,
   Verifier, Gates, Mention, Schedule, Trigger). E-E-A-T is mostly trust; small
   errors are expensive.
7. **Language switch**: put `[中文](README.zh.md)` next to the H1, and link back.
8. **Star history / demo GIF** at the bottom: social proof + a 10-second loop demo.
9. **Outbound links**: append `?utm_source=github&utm_medium=readme` to the site
   link so referrers are attributable in GitHub Traffic and Plausible/GA.

---

## 6. Website SEO (after the Pages deploy is fixed)

The site is a Next.js static export (`output: 'export'`, `basePath: '/ouroflow'`,
`trailingSlash: true`). Today it has a single root `metadata` object, no OG/Twitter
image, no canonical, no JSON-LD, no robots/sitemap/llms.txt.

Recommended metadata (per locale, in `site/app/[locale]/layout.tsx` or via
`generateMetadata`):

```ts
export const metadata: Metadata = {
  metadataBase: new URL("https://sinkear.github.io"),
  title: {
    default: "AI Task Pipeline OS for Coding Agents | OuroFlow", // 48 chars, keyword first
    template: "%s | OuroFlow",
  },
  description:
    "Human-in-the-loop AI agent orchestration with workflow RSI: agents execute, verify, merge; you approve proposals, goals and gates. Self-hosted Go + Next.js.",
  alternates: { canonical: "/ouroflow/en/" }, // per-locale
  openGraph: {
    type: "website",
    siteName: "OuroFlow",
    title: "AI Task Pipeline OS for Coding Agents",
    description: "Human-gated workflow RSI: agents execute and verify; you approve proposals, goals and gates.",
    url: "https://sinkear.github.io/ouroflow/en/",
    images: [{ url: "/ouroflow/og.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: "AI Task Pipeline OS for Coding Agents",
    description: "Define 'done'. Agents run the loop.",
    images: ["/ouroflow/og.png"],
  },
  robots: { index: true, follow: true },
};
```

Add:

- **JSON-LD** in the page body: `SoftwareApplication` (name, description,
  `applicationCategory: "DeveloperApplication"`, `operatingSystem: "Linux, macOS"`,
  `codeRepository`, MIT license, `offers.price: "0"`), plus `FAQPage` once the FAQ
  is rendered. Use `next/script` with `type="application/ld+json"`.
- **`site/public/robots.txt`**:
  ```text
  User-agent: *
  Allow: /
  Sitemap: https://sinkear.github.io/ouroflow/sitemap.xml
  ```
- **`site/app/sitemap.ts`** (static export emits `sitemap.xml`): root, `/en/`,
  `/zh/`, docs pages; `lastModified` from build time.
- **`site/public/llms.txt`** (GEO — how AI answer engines summarize the project):
  ```markdown
  # OuroFlow
  > Open-source, self-hosted multi-agent orchestration for human-gated workflow
  > automation. Define 'done'; coding agents execute, machine-verify, merge and push;
  > a workflow RSI loop proposes the next round, and you approve proposals, goals and
  > gates. Go + Next.js, ACP/MCP, SQLite.

  ## Links
  - GitHub: https://github.com/Sinkear/ouroflow
  - Docs: https://github.com/Sinkear/ouroflow/tree/main/docs
  - Site: https://sinkear.github.io/ouroflow/
  - README (中文): https://github.com/Sinkear/ouroflow/blob/main/README.zh.md
  ```
- **`hreflang`** for `en` / `zh` via `alternates.languages`.
- Fix brand casing in the existing root title (`ouroflow` → `OuroFlow`).

---

## 7. P0 blockers found during the audit

1. **Docs site is a 404 — the Pages workflow never runs.** Default branch is
   `main`; `.github/workflows/deploy-site.yml` only triggered on `master`, and
   there is no `gh-pages` branch on the remote at all (so it has never deployed).
   The one-line trigger fix is applied in this working tree; it still needs to be
   committed/merged and the workflow run once:
   ```diff
    on:
      push:
        branches:
   -      - master
   +      - main
   ```
   After merging, run the workflow manually once (`workflow_dispatch`), confirm a
   `gh-pages` branch appears, and set **Settings → Pages → Source: gh-pages**.
   Only then set the repo Website field.
2. **No homepage URL** on the repo (after #1, add it).
3. **Description + topics** are the current weak 8; apply §0.
4. **No social preview**; upload §4.
5. **README has no screenshots/GIF/FAQ/CI badge**; see §5.
6. **Stale `origin` URL**: local `origin` still points at
   `git@github.com:Sinkear/agentwork.git`. It redirects, but update it to
   `git@github.com:Sinkear/ouroflow.git` so clones, badges, and canonical links
   don't propagate the old name:
   ```bash
   git remote set-url origin git@github.com:Sinkear/ouroflow.git
   ```
7. **`13 concepts` vs 14 rows** in `README.md:85`; fixed in this working tree.
8. **No `llms.txt` / sitemap / JSON-LD**; see §6.

---

## 8. Keyword demand: RPA vs. workflow RSI (2026-11-23)

**Method.** Google Trends, worldwide, via `pytrends`, 5-year and 12-month windows.
Index is **relative (0–100) within each query**, not absolute monthly searches; use
Keyword Planner/Ahrefs for absolutes. Cross-checks: Stack Overflow tag counts, GitHub
topic pages, and the project's own docs. GitHub topic pages returned HTTP 429 during
the audit, so two repo counts are marked n/a above.

### 8.1 Same-scale comparison (anchor: AI agents)

| Term | 5y mean | 12m mean | Read |
|---|---:|---:|---|
| `RPA` | 12.0 | 23.0 | Largest legacy volume, but intent is UiPath/Power Automate desktop bots. |
| `AI agents` | 10.3 | 38.7 | Overtook RPA in the last 12 months; best-fit demand term. |
| `workflow automation` | 2.2 | 8.6 | ~1/4–1/3 of RPA; accurate, but not a headline traffic driver. |
| `recursive self-improvement` | 0.0 | 0.1 | Research/philosophy term; effectively no search demand. |
| `self-evolving agents` | 0.0 | 0.0 | Same; hype on X, not search behavior. |

### 8.2 Agentic / RSI long-tail (12-month groups)

| Term | Relative index | Read |
|---|---:|---|
| `agent orchestration` | 2.4 (AI agents = 51.9) | Real but small; it is a subcategory query. |
| `agentic workflow` | 1.3 | Small; useful in copy, not as lead keyword. |
| `AI RPA` | 2.3 (RPA = 23.0) | ~10% of RPA; the only viable RPA bridge with visible demand. |
| `agentic RPA` | 0.0 | Analyst term, not a search term yet. |
| `agentic process automation` | 0.0 | Same. |
| `self-improving agents` | 0.0 | No measurable demand. |
| `workflow RSI` | 0.0 | **No measurable Google demand**; keep it as a differentiator, not a traffic bet. |

`RSI` alone is unusable: in a 5-year group it scores 14.7, but the volume is finance
(Relative Strength Index), not recursive self-improvement. Always spell it out.

**Practical read**

1. **RPA is the highest-volume term in the pair, but it is the wrong lead.** Use it as
   a qualified modifier ("RPA-style workflow automation") and in comparison content
   ("OuroFlow vs. RPA: process automation vs. acceptance-driven agent delivery").
   Never call the product "an RPA platform".
2. **`AI agents` / `multi-agent orchestration` are the demand terms.** The description
   leads with `multi-agent orchestration` and keeps `AI agents` in the topic set; that
   is the traffic engine.
3. **`workflow RSI` is a differentiation term, not a traffic term.** It is worth
   including because it is precise and defensible, and because AI answer engines
   (GEO) reward the exact concept — not because it will rank for volume.
4. **The human boundary is the conversion lever.** The same docs that justify W-RSI
   show human_wait at 78.1% of elapsed time. Saying "you approve proposals, the next
   goal and gates" turns a weakness (not fully autonomous) into trust and accuracy.
5. **Do not invent topics for zero-repo phrases.** `workflow-rsi` and `agentic-rpa`
   returned 0 tagged repos; they create no topic page. `rpa` and
   `recursive-self-improvement` are the real tags.

---

## 9. Off-repo distribution (backlinks are the real ranking lever)

GitHub search alone caps out fast. Each item below creates an indexed page that
links to the repo.

**Directories / lists (open PRs; read each CONTRIBUTING first):**

- `awesome-ai-agents`, `awesome-agent-orchestration`, `awesome-opencode`,
  `awesome-mcp-servers` (if the MCP workspace server qualifies),
  `awesome-go` (Application), `awesome-selfhosted`.
- AlternativeTo, OpenAlternative, Product Hunt, Hacker News Show HN.

**Communities (one post each, tailored, not copy-paste):**

- English: r/LocalLLaMA, r/selfhosted, r/golang, r/nextjs, Lobsters, dev.to.
- Chinese: V2EX, 即刻, 少数派, 掘金, GitCode, OSChina.
- Show a 30–60s demo (define done → agent runs → gate → merge), not a feature list.

**Repo hygiene that feeds discovery:**

- Pin the repo on the maintainer profile and add it to the profile README.
- Cut a tagged release (`v0.0.1-beta.1`) with notes; releases appear in feeds and
  give awesome-list maintainers a stable link.
- Enable Discussions (Q&A) so long-tail questions become indexed pages.
- Add the site URL + demo video to the repo sidebar (Website field).

**Launch sequence (2 weeks):**

| Day | Action |
|---|---|
| 0 | Apply §0 metadata, social preview, fix Pages deploy, merge README §5. |
| 1 | Release tag + demo GIF; post to 2–3 awesome lists. |
| 2–3 | Show HN + 2 subreddits; Chinese post on V2EX/即刻. |
| 4–7 | Product Hunt / OpenAlternative; follow up on awesome-list PRs. |
| 8–14 | Measure (§10); rewrite the weakest sentence; publish a technical deep-dive (dev.to + 掘金) linking the repo. |

---

## 10. Measurement

- **GitHub → Insights → Traffic** (14-day): views, unique visitors, clones,
  referrers. Record weekly before/after metadata changes.
- **GitHub search ranking**: check `multi-agent orchestration`, `ai task pipeline`,
  `acp coding agent`, `mcp coding agent`; note page position for the repo.
- **Google/Bing**: `site:github.com/Sinkear/ouroflow`, plus the brand query; after
  the site deploys, verify with Search Console and submit the sitemap.
- **Leading indicators**: stars/week, forks, watchers, inbound issues/discussions,
  awesome-list acceptances, referrer domains.
- Don't judge a description change by stars in 48h; give it 2–4 weeks.

---

## 11. Claims policy — fact-check before publishing

Every description, README sentence, and launch post must pass this list. These are
the failure modes found while checking the current copy against the repo:

- **Not "webhook-driven issue/PR automation."** Issues are poll + webhook; pull
  requests are poll-only (`README.md:29`).
- **Not "single process · single machine."** `README.md:18` says it, but
  `ouroflow connect` runs agents on another machine (`README.md:56-57`) and the web
  UI is built/started separately (`README.md:64-67`). Say "single-user, local SQLite
  state, no cloud" instead.
- **Not "fully unattended by default."** HITL is the default; HOOTL skips the human
  gate (`README.md:30`). Say "unattended between gates" or "pass your gate".
- **"Automatic delivery" is not deployment.** It is merge into the domain repo +
  re-verify + push (`README.md:26`, `README.md:91-93`). Not releases, not production
  deploys, not upstream PRs.
- **Verification is not a correctness proof.** It runs executable checks and
  structural guards; weak verification forces a human gate (`README.md:99`). Never
  write "verified = correct" or "guaranteed".
- **Squads are routing groups, not swarms.** The leader delegates via sub-goals;
  reviewers are pulled into checkpoints (`README.md:95`).
- **No enterprise/security claims.** `v0.0.1-beta.1`, single user, no auth. Avoid
  "production-ready", "secure", "multi-tenant", "enterprise-grade".
- **No "first" or "built from scratch".** The README calls OuroFlow an independent
  derivative distribution of agentwork (`README.md:204`).
- **"RPA-style" is an analogy, not a feature claim.** OuroFlow does not ship desktop
  UI bots, recorder/attended RPA, or UiPath/Power Automate connectors. It automates
  software-delivery workflows with coding agents. Keep "RPA-style workflow automation"
  and do not shorten it to "RPA platform".
- **"Workflow RSI" is human-gated, not autonomous self-modification.**
  `docs/rsi/self-iter-playbook.md` T6: the platform never changes its own code without
  human approval → builder → next-round re-test. The optimizer is read-only and capped
  at 3 proposals per round; the candidate list is not an approval list. Say
  "proposes the next round", never "improves itself autonomously".
- **The 78.1% human_wait figure is evidence, not a marketing number.**
  `docs/use-cases/2026-11-21-blueprint-self-iteration/timeline.md`: 1,735.5m human wait
  out of 2,221.6m wall+wait across 50 runs. Use it to justify the human boundary and
  the "not a full closed loop" claim; do not round it into "80% autonomous".
- **Never write "RSI" alone in copy.** It reads as Relative Strength Index (finance)
  and has no agentic meaning; always "workflow RSI (recursive self-improvement)".
- **Claude Code is not supported today** (see §2). Do not put it in the description,
  topics, README, or launch posts until a tested ACP adapter is documented.
- **"13 concepts" was wrong; now fixed to 14** (`README.md:85` vs 14 table rows at
  `README.md:89-102`). Trust is the center of E-E-A-T; small errors are expensive.

## Appendix — topic traffic snapshot (2026-11-23)

```text
nextjs 223,579 · ai-agents 211,953 · llm 194,238 · golang 156,206 · mcp 129,235
claude-code 128,694 · go 106,591 · developer-tools 97,742 · sqlite 71,027
self-hosted 61,313 · devops 54,491 · ai-agent 49,303 · model-context-protocol 47,770
local-first 36,342 · agentic-ai 30,420 · multi-agent 24,942 · ci-cd 18,629
opencode 13,022 · workflow-automation 10,816 · human-in-the-loop 10,009
coding-agents 9,968 · coding-agent 9,318 · orchestration 7,623 · autonomous-agents 6,959
agent-orchestration 5,488 · task-management 5,453 · feishu 1,892 · acp 1,364
ai-coding-agent 1,326 · agent-runtime 1,282 · lark 1,137 · github-issues 943
agent-client-protocol 686 · agent-os 276 · autonomous-coding 264
task-orchestration 221 · task-board 195 · task-pipeline 8 · ai-task-automation 8
```

Zero/nonexistent topic pages at capture time: `ai-workflow`, `ai-task-pipeline`,
`autonomous-coding-agents`, `agent-workflow`. Avoid inventing tags nobody browses.
`workflow-rsi` and `agentic-rpa` also returned **0 tagged repositories** on
2026-11-23 — the phrases belong in copy, not in the topic list. `rpa` and
`recursive-self-improvement` are real tags, but their `/topics/` pages returned
HTTP 429 during the audit; re-check their repo counts before finalizing.