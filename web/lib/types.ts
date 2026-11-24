// API types — mirror the Go structs in internal/service.

// ProbeCLI is one agent CLI a remote machine reported (CLI 分支 Phase 1).
export interface ProbeCLI {
  name: string;
  version: string;
  acp_spawn: string[];
  skills_dir?: string;
  profile_files?: string[];
}

// Skill is a platform-managed skill package (CLI 分支 Phase 4).
export interface Skill {
  id: string;
  name: string; // 机器身份（目录名 / agent.skills 里存的 id），如 ouroflow-chat-create
  title?: string; // 中文展示名（SKILL.md frontmatter 的 title），可能为空
  description: string;
  created_at: string;
  source?: SkillSource | null;
  builtin?: boolean; // 平台随 daemon 二进制发布的内置技能（启动时自动刷新，不能手删）
}

// SkillSource is a skill's remote repository binding (Feature A). It is
// omitted when no repo_url is configured. The token is write-only: the API
// never serializes it — `has_token` says whether one is stored.
export interface SkillSource {
  repo_url: string;
  provider: string; // github | gitcode
  branch: string;
  path: string; // '' = repo root containing the skill
  has_token?: boolean;
  last_synced_at?: string;
  last_sync_action?: string; // '' | pulled | pushed | pr
  last_remote_sha?: string;
}

// SyncEntry is one file difference between the local skill dir and the
// remote repo (paths relative to the configured repo path).
export interface SyncEntry {
  path: string;
  status: "added" | "modified" | "removed";
  local_sha256?: string;
  remote_sha256?: string;
}

export interface SyncCounts {
  added: number;
  modified: number;
  removed: number;
  unchanged: number;
}

// SyncResult is GET /skills/{id}/sync (read-only compare, still probes the
// configured token's push permission) and the mutation responses for
// POST /skills/{id}/sync.
export interface SyncResult {
  skill_id?: string;
  identity?: string;
  can_push?: boolean;
  state?: "in_sync" | "local_ahead" | "remote_ahead" | "diverged";
  base_branch?: string;
  remote_sha?: string;
  entries?: SyncEntry[];
  counts?: SyncCounts;
  checked_at?: string;
  // local_missing: the skill's local files are gone (runs/skills wiped, or a
  // DB restored without runs/). A pull restores them; push/PR stay blocked.
  local_missing?: boolean;
  // mutation extras
  action?: string; // pulled | pushed
  branch?: string;
  commit?: string;
}

// SkillPullRequestResult is POST /skills/{id}/pull-request's response.
export interface SkillPullRequestResult {
  pr_url: string;
  pr_number: number;
  branch: string;
  base: string;
  identity: string;
  commit: string;
}

// ── Hooks（生命周期钩子，spec: tmp/hooks-spec.md v0.2） ──

// Hook is the registry entity (script + meta). Content is only populated by
// GET /hooks/{id}; the list omits it (scripts can be 256 KiB each).
export interface Hook {
  id: string;
  name: string;
  description: string;
  content?: string;
  content_hash: string;
  interpreter: string; // '' = 遵循脚本 shebang，否则强制 bash|sh|python3|node
  timeout_sec: number;
  params?: Record<string, string>; // 默认参数（扁平 {string:string}）
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

// HookListItem is one row of GET /hooks: the hook plus the list-page
// decorations (binding count, last run, and the content-changed tripwire).
export interface HookListItem {
  hook: Hook;
  binding_count: number;
  last_run_status: string; // '' = 从未运行过
  last_run_finished_at: string;
  last_run_content_hash: string;
  content_changed_since_last_run?: boolean;
}

// HookBinding attaches a hook to an event at a scope (goal | domain).
// GET /goals/{id}/hooks returns the EFFECTIVE set: domain-inherited
// bindings carry scope="domain" (the goal-level ones say scope="goal").
export interface HookBinding {
  id: string;
  hook_id: string;
  hook_name?: string;
  scope: string; // goal | domain
  scope_id: string;
  event: HookEvent;
  seq: number;
  params?: Record<string, string>; // 绑定级参数，浅合并覆盖 hook 默认
  created_at: string;
}

// HookRun is one execution record (or a skip/timeout record).
export interface HookRun {
  id: string;
  hook_id: string;
  hook_name?: string;
  binding_id: string;
  goal_id: string;
  event: HookEvent;
  content_hash: string; // 执行那一刻的脚本 sha256（版本溯源）
  status: "running" | "completed" | "failed" | "timeout" | "skipped";
  exit_code?: number | null; // null = skipped/timeout
  started_at: string;
  finished_at: string;
  output: string; // stdout+stderr 合并（尾部 8 KiB），skip 原因也写在这里
}

// V1 事件注册表（后端 hookEventTopics 的镜像）。
export type HookEvent =
  | "goal_finished"
  | "goal_delivered"
  | "goal_deliver_failed"
  | "goal_reviewing"
  | "goal_review_resolved";

export const HOOK_EVENTS: { value: HookEvent; label: string; desc: string }[] = [
  { value: "goal_finished", label: "任务完成", desc: "goal 进入终态（done / failed / cancelled）后触发" },
  { value: "goal_delivered", label: "交付完成", desc: "变更已提交进仓库后触发" },
  { value: "goal_deliver_failed", label: "交付失败", desc: "交付动作失败（进入 review）后触发" },
  { value: "goal_reviewing", label: "进入审查", desc: "goal 停靠进 review 窗口时触发" },
  { value: "goal_review_resolved", label: "审批决定", desc: "人审做出 approve / reject 后触发" },
];

export function hookEventLabel(event: string): string {
  return HOOK_EVENTS.find((e) => e.value === event)?.label ?? event;
}

// Machine is a remote host registered via `ouroflow connect`.
export interface Machine {
  id: string;
  name: string;
  hostname: string;
  version: string;
  probed_clis: string; // JSON []ProbeCLI
  last_seen_at: string;
  status: string; // connected | offline
  created_at: string;
}

export interface Runtime {
  id: string;
  name: string;
  machine_id: string; // the registered machine that executes this runtime's runs
  args: string[]; // acp_spawn — how the machine starts the CLI
  env: Record<string, string>;
  status: string; // active | absent — absent = the machine's latest probe no longer sees the CLI
  // Probe-detected model catalog (GET /runtimes/{id}/models) — the runtime
  // page lists it read-only; agents may pick from it.
  models?: RuntimeModel[];
  models_at?: string; // last successful detection ('' = never detected)
  created_at: string;
}

export interface RuntimeModel {
  id: string;
  name?: string;
  description?: string;
}

export interface RuntimeModels {
  runtime_id: string;
  name: string;
  cli: string; // CLI type, i.e. the runtime name before '@'
  machine_id: string;
  models: RuntimeModel[];
  detected_at: string;
}

// McpServer mirrors acp.McpServer: an extra MCP server the agent's runs
// advertise at session/new alongside the platform's workspace server.
export interface McpServer {
  type?: string; // ""=stdio | http | sse
  name: string;
  command?: string; // stdio
  args?: string[]; // stdio
  env?: { name: string; value: string }[]; // stdio: process env
  url?: string; // http / sse
  headers?: { name: string; value: string }[]; // http / sse: request headers
}

export interface Agent {
  id: string;
  name: string;
  type: string; // standard | steward
  description?: string; // human-facing one-liner (shown in the web list); distinct from system_prompt
  runtime_id: string;
  system_prompt: string;
  model: string;
  env: Record<string, string>;
  mcp_servers: McpServer[]; // the agent's own tools (browser/db/…), always after the workspace server
  skills: string[]; // platform-managed skill ids (CLI 分支 Phase 4) — pushed to the agent's machine
  max_concurrent: number;
  created_at: string;
}

export type GoalStatus =
  | "backlog"
  | "active"
  | "review"
  | "done"
  | "failed"
  | "cancelled";

// 运行模式（goal 级开关，RSI 训练分析的入口）：
//   infer = 只记录 run 级基础指标，不建分析会话（老 goal 缺省即此值）
//   train = 记录结构 trace，goal 终态后自动产出分析报告
export type ExecutionMode = "infer" | "train";

export interface Goal {
  id: string;
  title: string;
  description: string;
  domain_id: string;
  assignee_type: string; // agent | squad | human
  assignee_id: string;
  status: GoalStatus;
  category: string; // hitl | hootl — hootl 完成即结束，跳过人工复检
  handoff_note: string;
  review_request: string;
  human_iterations: number;
  created_by_type: string;
  created_by_id: string;
  created_at: string;
  source_ref: string; // M4-B: "github:owner/repo#123" (external issue source)
  current_agent_id: string; // latest running/queued run's agent ('' = none)
  attention: string; // v2 OwnerAttention：'' | integration | wrapup | recovery | user_action
  review_phase: string; // 审查窗口派生阶段：'' | awaiting_review | reviewing | awaiting_approval（决策 6-19 延伸）
  execution_mode?: ExecutionMode; // 缺省/旧数据视为 infer（后端 DDL 默认 infer）
  content_capture?: string; // none|redacted|full（train 模式）
  budget_usd?: number; // 分析预算（train 模式默认 2）
  sample_rate?: number; // 采样率 0..1（train 模式默认 1）
  blueprint_version_id?: string; // Phase A：''/缺省 = infer 模式；非空 = 蓝本驱动（Activate 编译 + guard 计数）
}

export interface GateStat {
  rule: string;
  total: number;
  approved: number;
  rejected: number;
}

export interface Domain {
  id: string;
  type: string; // repo (M0)
  name: string;
  git_url: string;
  default_branch: string;
  git_identity: string;
  git_credentials: string;
  max_run_duration: number;
  issue_repo: string; // M4-B: "owner/repo" tracked for issues ('' = none)
  issue_assignee: string; // M4-B: agent|squad handling this repo's issues
  issue_assignee_type: string; // M4-B: agent | squad
  issue_provider: string; // M4-B: github | gitcode
  pull_assignee: string; // M4-B PR pull: agent|squad handling this repo's PRs ('' = don't pull PRs)
  pull_assignee_type: string; // M4-B PR pull: agent | squad
  webhook_secret: string; // 仓库级 webhook secret（'' = 用平台 secret）
  // 项目级验收级别：hitl | hootl | ''（待定）。仓库 webhook 自动建的目标
  // 继承这个级别；'' 时沿用目标默认（hitl）。
  acceptance_level: string;
  created_at: string;
  scratch_dir?: string; // scratch 域的项目目录（repo 域无）——人找产物的路径
}

// ── Webhook（每仓库接入状态） ──
// GET /domains/{id}/webhook 的宿主侧 hook 与投递日志视图。

export interface Webhook {
  id: number;
  url: string;
  active: boolean;
  events: string[];
}

export interface WebhookDelivery {
  event: string; // issues | pull_request | ''
  action: string; // opened / reopened / synchronize / ...
  repo: string; // owner/repo（解析不出为 ''）
  ok: boolean;
  error: string;
  received_at: string;
}

export interface WebhookStatus {
  domain_id: string;
  provider: string; // github | gitcode
  repo: string; // owner/repo（'' = 没有配置跟踪仓库）
  path: string; // /webhooks/{provider}
  secret_source: "repo" | "platform" | "";
  secret_set: boolean;
  events_required: string[];
  hooks: { supported: boolean; ok: boolean; error: string; items: Webhook[] };
  deliveries: WebhookDelivery[]; // 最近 20 条，received_at DESC
}

export interface WebhookRegisterResult {
  created: boolean;
  hook: Webhook;
}

// ── Connection（仓库连接：每条连接一套独立 webhook + secret） ──
// 镜像 internal/service/connection.go。同一仓库可被多个小队同时接入，
// 每条 connection 有独立的 webhook 路径，因此互不覆盖。
export interface Connection {
  id: string;
  squad_id: string;
  domain_id: string;
  provider: string; // github | gitcode
  repo: string; // owner/repo
  events: string[]; // issues | pull_request
  secret_set: boolean;
  webhook_path: string; // /webhooks/{provider}/{id}
  created_at: string;
  secret?: string; // 仅 POST /squads/{id}/connections 响应一次性返回明文
}

// GET /connections/{id}/webhook 的宿主侧 hook 与投递日志视图。
export interface ConnectionWebhookStatus {
  connection_id: string;
  provider: string; // github | gitcode
  repo: string; // owner/repo
  path: string; // /webhooks/{provider}/{id}
  secret_set: boolean;
  events_required: string[];
  hooks: { supported: boolean; ok: boolean; error: string; items: Webhook[] };
  deliveries: WebhookDelivery[]; // 最近 20 条，received_at DESC
}

export type RunStatus = "queued" | "running" | "completed" | "failed" | "cancelled";

// DomainGitTestResult is the outcome of POST /domains/test (决策 6-24):
// the config-time git probe — repo URL + branch + token read permission,
// verified BEFORE the first run instead of failing it.
export interface DomainGitTestResult {
  ok: boolean;
  branch_exists: boolean;
  resolved_branch?: string; // the branch checked (configured, or the remote's HEAD default)
  refs?: string[];
  error?: string;
  latency_ms: number;
}

export interface Run {
  id: string;
  goal_id: string;
  agent_id: string;
  run_kind: string; // worker|processor
  domain_id: string;
  prompt: string;
  session_id: string;
  workdir: string;
  status: RunStatus;
  cancel_reason?: string; // structured: idle_watchdog|handoff|stopped|timeout|runaway|goal_terminal|goal_cancelled
  role: string; // owner | subgoal | consult | review | verify（决策 5-4/6-9，enqueue 时派生）
  attempt: number;
  result_summary: string;
  evidence: string; // JSON: diff stats + verify output + agent summary
  trigger_comment_id: string;
  is_leader_run: boolean;
  squad_id: string;
  queued_at: string;
  started_at: string;
  finished_at: string;
  created_at: string;
}

export interface Comment {
  id: string;
  goal_id: string;
  author_type: string; // human | agent | system
  author_id: string;
  parent_id: string;
  content: string;
  created_at: string;
  run_id?: string; // the run whose product this comment is ('' = trigger/context)
  ask_human?: boolean; // 决策 7-3: agent's --ask question to the human (goal creator)
}

export interface Squad {
  id: string;
  name: string;
  description: string;
  leader_id: string;
  instructions: string;
  created_at: string;
}

export interface SubGoal {
  id: string;
  goal_id: string;
  title: string;
  description: string;
  assignee_id: string;
  verifier_id: string; // '' = 机器验证
  status: string; // running | verifying | verified | rejected | cancelled | failed
  execution_attempt: number;
  quality_iteration: number;
  depends_on?: string[]; // 前置子任务 id 列表——全部 verified 后才释放执行
  created_at: string;
}

// Change is a sub-goal's logical deliverable (v2 决策 6-3): the owner
// integrates it into the goal branch. HeadRef = the LATEST revision's head.
export interface Change {
  id: string;
  goal_id: string;
  sub_goal_id: string;
  status: string; // ready | integrating | integrated | conflict
  head_ref: string;
  created_at: string;
}

// ChangeRevision binds a change to the integration base it was built
// against — conflict rework appends seq N+1 on the SAME change.
export interface ChangeRevision {
  id: string;
  change_id: string;
  seq: number;
  base_ref: string;
  head_ref: string;
  created_at: string;
}

// ChangeDetail is a change with its revision history (GET /goals/{id}/changes).
export interface ChangeDetail extends Change {
  revisions: ChangeRevision[];
}

// VerificationResult is one verification round of a sub-goal (v2 决策 6-5):
// machine checks or an agent verifier's structured verdict.
export interface VerificationResult {
  id: string;
  goal_id: string;
  sub_goal_id: string;
  verifier_run_id: string; // '' = machine verification
  status: string; // passed | rejected
  summary: string;
  evidence: string;
  created_at: string;
}

export interface SquadMember {
  id: string;
  squad_id: string;
  member_type: string; // agent | human
  member_id: string;
  role: string;
  created_at: string;
}

// Role vocabulary registry word (BLUEPRINT-PROPOSAL §11.8): name is the
// capability slug blueprint "capability:<name>" resolves against;
// description is the job spec (岗位说明书).
export interface SquadRole {
  id: string;
  squad_id: string;
  name: string;
  description: string;
  created_at: string;
}

// Registry word joined with squad name — the cross-squad listing the
// blueprint author reads so capability: references stay aligned.
export interface SquadRoleView {
  squad_id: string;
  squad_name: string;
  name: string;
  description: string;
  created_at: string;
}

export interface Schedule {
  id: string;
  name: string;
  title_template: string;
  description: string;
  assignee_type: string; // agent | squad
  assignee_id: string;
  domain_id: string; // 触发时克隆 goal 所属的域（验收策略 + worktree）
  cron_expression: string;
  timezone: string;
  category: string; // hitl | hootl — 派生 goal 的验收分类
  enabled: boolean;
  next_run_at: string;
  last_run_at: string;
  created_at: string;
}

// ScheduleRun is one firing of a schedule — the fired goal's identity and
// current status (the schedule detail's firing history).
export interface ScheduleRun {
  id: string;
  schedule_id: string;
  goal_id: string;
  goal_title: string;
  goal_status: string;
  planned_at: string;
  status: string; // dispatched|failed
  created_at: string;
}

// LogLine is one daemon log line (GET /logs and the live log:line stream).
export interface LogLine {
  ts: string;
  level: string; // debug | info | warn | error
  text: string;
}

// TeamImport is a TEMPORARY tracking row for a team-definition-repo import
// processor run. The steward agent decides which runtime each imported agent
// binds to (from the team definition or random assignment). git_url/
// credentials/branch persist from the HTTP request to daemon dispatch.
// ImportTeam cleans up old completed/failed rows.
export interface TeamImport {
  id: string;
  run_id: string;
  git_url: string;
  git_credentials: string;
  default_branch: string;
  status: string; // pending|completed|failed
  result: string; // JSON summary
  created_at: string;
}

export interface TeamImportResponse {
  team_import: TeamImport;
  run: Run;
}

// ── Templates (GitCode YAML template library) ──
// Mirror internal/service/template.go + template_apply.go. The list endpoint
// returns TemplateSummary; the get-one endpoint returns TemplateDetail with
// the raw YAML + the inner spec node re-encoded as JSON.

// TemplateMeta is the common front matter of every template.
export interface TemplateMeta {
  api_version: string; // "ouroflow/v1"
  kind: string; // "squad-template" | "project-template"
  id: string;
  name: string;
  description: string;
  version: string;
  tags?: string[];
  icon?: string;
}

// TemplateSummary is the list-item shape (metadata only, no spec).
export interface TemplateSummary extends TemplateMeta {
  path: string;
}

// TemplateDetail is the get-one shape: metadata + raw YAML + decoded spec.
// `spec` is the INNER spec node only (repo/domain/team/…), not the whole
// document — the f9a6bb1 fix made Get return spec, not {api_version,…,spec}.
export interface TemplateDetail extends TemplateMeta {
  path: string;
  spec_yaml: string;
  spec: Record<string, unknown>;
}

// ── Template spec shapes (mirror the Go spec structs, for preview/derive) ──
// These describe what the backend parsed; the dialog derives its form state
// from spec.repo.create / spec.domain.type / spec.team / spec.goals.

export interface TplRepoCreate {
  create: boolean;
  visibility: string; // "private" | "public"
  auto_init?: boolean;
  gitignore_template?: string;
  org?: string;
}

export interface TplDomain {
  type: string; // "repo" | "scratch" (default repo)
  default_branch: string;
  git_identity?: string;
  issue_assignee?: string;
  issue_assignee_type?: string; // "agent" | "squad"
}

export interface TplAgent {
  name: string;
  description?: string;
  system_prompt?: string;
  skills?: string[];
  runtime?: string;
  max_concurrent?: number;
  model?: string;
  env?: Record<string, string>;
}

export interface TplMember {
  name: string;
  role?: string;
}

export interface TplSquad {
  name: string;
  description?: string;
  leader: string;
  instructions?: string;
  members?: TplMember[];
}

// squad-template spec.
export interface SquadTemplateSpec {
  strategy?: string; // "create" | "upsert" (default upsert)
  agents: TplAgent[];
  squad: TplSquad;
}

// project-template spec.
export interface ProjectTemplateSpec {
  repo?: TplRepoCreate; // absent = user supplies git_url
  domain: TplDomain;
  team?: SquadTemplateSpec; // optional agents+squad riding the project
  goals?: TplGoal[];
  schedules?: TplSchedule[];
}

export interface TplGoal {
  title: string;
  description?: string;
  assignee?: string; // agent or squad name
  assignee_type?: string; // "agent" | "squad" (default agent)
  start?: boolean; // true = active; false = backlog
}

export interface TplSchedule {
  name: string;
  title?: string;
  description?: string;
  cron: string;
  assignee?: string;
  assignee_type?: string;
}

// ── Apply requests / results ──

// ApplySquadOverrides: the squad dialog's fields.
export interface ApplySquadOverrides {
  squad_name?: string; // overrides spec.squad.name
  rename?: Record<string, string>; // template agent name → per-apply name
  strategy?: string; // overrides spec.strategy
}

// ApplyProjectOverrides: the project dialog's fields.
export interface ApplyProjectOverrides {
  name: string; // required: the domain name
  git_url?: string; // required unless spec.repo.create
  git_credentials?: string;
  repo_token?: string; // the repo-creation token (GitCode)
  template_token?: string; // unused by apply (list phase); kept for symmetry
  repo?: Partial<TplRepoCreate>; // overlay on spec.repo
  rename?: Record<string, string>;
  strategy?: string;
  goal_start?: boolean; // overlay on every goal's start
}

// ApplyItem is one entity's per-step outcome.
export interface ApplyItem {
  kind: string; // skill | agent | squad | domain | repo | goal | schedule | member | domain.issue_assignee
  name: string;
  id?: string;
  action: string; // created | updated | skipped | failed
  error?: string;
}

export interface ApplySquadResult {
  squad?: Squad;
  agents?: Agent[];
  skills?: Skill[];
  items: ApplyItem[];
}

export interface ApplyProjectResult {
  domain?: Domain;
  repo_url?: string; // set when the repo was created
  agents?: Agent[];
  squad?: Squad;
  goals?: Goal[];
  schedules?: Schedule[];
  items: ApplyItem[];
}

// TemplateFetchResult reports one refresh.
export interface TemplateFetchResult {
  fetched: number;
  errors?: string[];
  fetched_at: string;
}

// WS event shape from the hub: {"topic":"goal:created","payload":{...}}
export type WSTopic =
  | "goal:created" | "goal:assigned" | "goal:finished"
  | "goal:retrying" | "goal:retry_failed" | "goal:deleted"
  | "goal:reviewing" | "goal:review_ready" | "goal:approved" | "goal:review_resolved"
  | "goal:delivered" | "goal:deliver_failed"
  | "run:enqueued" | "run:coalesced" | "run:claimed" | "run:discarded" | "run:event" | "run:cancelled" | "run.terminal"
  | "sub_goal.created" | "sub_goal.verifying" | "sub_goal.verified" | "sub_goal.rejected" | "sub_goal.retrying" | "sub_goal.failed" | "sub_goal.cancelled"
  | "change.ready" | "change.integrated" | "change.conflict"
  | "comment:created"
  | "log:line"
  | "agent:created" | "agent:deleted" | "agent:pin_changed"
  | "squad:created" | "squad:deleted" | "squad:member_added" | "squad:member_removed"
  | "schedule:created" | "schedule:fired"
  | "domain:created" | "domain:deleted" | "domain:compiled" | "domain:compile_failed"
  | "hook:started" | "hook:completed"
  | "team:import_enqueued" | "team:imported" | "team:import_failed";

export interface WSEvent {
  topic: WSTopic;
  payload: Record<string, unknown>;
}

// TimelineItem is one event in a goal's execution flow — a run segment (an
// agent's turn), an action point (created/handoff/review entry/…), or a gate
// decision (approve/reject). Served by GET /goals/{id}/timeline, merged and
// time-ordered by the backend.
export interface TimelineItem {
  at: string;                    // RFC3339 — the event's point in time
  kind: "run" | "action" | "decision";
  run_id?: string;               // run: the run row (for detail fetch)
  agent_id?: string;             // run: the executing agent
  run_status?: string;           // run: queued|running|completed|failed|cancelled
  role?: string;                 // run: owner|subgoal|consult|review|verify
  attempt?: number;              // run: machine-retry counter
  started_at?: string;           // run: execution window
  finished_at?: string;
  actor_type?: string;           // action: human|agent|system
  actor_id?: string;             // action: which agent/human ('' for system)
  action?: string;               // action: created|handoff|entered_review|…
  detail?: string;
  gate_rule?: string;            // decision: which rule fired
  decision?: string;             // decision: approve|reject|redirect
  reason?: string;               // decision: the human's words
  review_duration_s?: number;    // decision: seconds spent in review
}

// ══════════════════════════════════════════════════════════════
// RSI 训练分析（docs/research/rsi-summary.md §5.2 / §5.5）
//
// 契约要点：`metrics` / `baseline` / `attrs` 是对象、`evidence` 是字符串数组，
// 后端直接序列化 Go 结构体（不是 JSON 字符串）。这里仍按“对象或 unknown”
// 读取，页面渲染前统一做容错——后端字段演进时旧前端不能白屏。
// ══════════════════════════════════════════════════════════════

// 会话状态机（后端 schema.sql）：waiting（会话已建，等 goal 进终态）→
// analyzing（冻结编排图、跑结构规则）→ ready；failed / skipped 是终态。
// UI 对未知值原样显示，不崩、不误判。
export type RsiSessionStatus = "waiting" | "analyzing" | "ready" | "failed" | "skipped";
export type RsiContentCapture = "none" | "redacted" | "full";

export interface RsiSession {
  id: string;
  goal_id: string;
  goal_title: string; // 列表 join goal 标题（后端已拼好，前端不再二次请求）
  domain_id: string;
  status: RsiSessionStatus;
  content_capture: RsiContentCapture;
  sample_rate: number; // 0..1
  budget_usd: number;
  node_count: number; // 冻结编排图里的节点数（不是 span 数）
  critical_path_ms: number;
  parallel_speedup: number; // work_sum / critical_path，>=1；1 表示完全串行
  finding_count: number;
  top_category: string; // '' = 未归因到具体瓶颈
  top_title: string;
  error: string;
  started_at: string;
  sealed_at: string;
  finished_at: string; // '' = 尚未结束
}

// 严重度是后端的 info|warn|critical（rsi_analyze.go：score >= 0.5 的 warn 升为 critical）。
export type RsiSeverity = "critical" | "warn" | "info";
// 三级置信度（§4.3）：L1 单次观察 / L2 单 run 统计 / L3 跨 run 基线。
export type RsiConfidence = "L1" | "L2" | "L3";
export type RsiFindingStatus = "open" | "confirmed" | "dismissed";

export interface RsiFinding {
  id: string;
  report_id: string;
  // serial_chain|split_candidate|lost_parallelism|merge_candidate|wrong_assignee|
  // ping_pong|brief_quality|consult_repeat|retry_amplification|human_wait
  category: string;
  severity: RsiSeverity;
  confidence: RsiConfidence;
  title: string;
  explanation: string; // 判据：命中规则 + 具体数值
  suggestion: string;
  // 后端 evidence_json 是 []string：节点 key / 链路 或 "metric=value" 测量串。
  evidence: string[];
  score: number; // 排序权重，越大越值得先看
  status: RsiFindingStatus;
  created_at?: string;
}

export interface RsiReport {
  id: string;
  session_id?: string;
  status: string; // ready|failed
  summary: string;
  metrics: Record<string, unknown>;
  baseline: Record<string, unknown>;
  narrative: string; // 可选 LLM 归因（P1），当前为空
  created_at: string;
  finished_at: string;
  error: string;
}

// ── 优化轮次账本（W-RSI v3 insight panel） ──
// 每次分析（终态触发或手动重跑）都会在 rsi_round 追加一轮，冻结当轮
// metrics/baseline/findings，并记录与上一轮的 delta。详情页的
// rsi_report/rsi_finding 仍是最新一轮，轮次历史只读。
export type RsiRoundTrigger = "terminal" | "manual_retry";

export interface RsiDeltaEntry {
  from?: number;
  to?: number;
  /** 相对上一轮的变化率（不是百分点）；from=0 时后端省略。 */
  pct?: number;
}

export type RsiRoundDelta = Record<string, RsiDeltaEntry>;

export interface RsiRound {
  id: string;
  session_id: string;
  goal_id: string;
  round_no: number;
  trigger: RsiRoundTrigger;
  metrics: Record<string, unknown>;
  baseline: Record<string, unknown>;
  delta: RsiRoundDelta;
  finding_count: number;
  critical_count: number;
  top_category: string;
  top_title: string;
  findings: RsiFinding[]; // 当轮冻结副本（只读）
  summary: string;
  narrative: string;
  created_at: string;
}

// GET /rsi/overview 的 trend：每条是 finished round，时间升序。
export interface RsiTrendPoint {
  round_id: string;
  session_id: string;
  goal_id: string;
  goal_title: string;
  round_no: number;
  created_at: string;
  parallel_speedup: number;
  critical_path_ms: number;
  rework_ratio: number;
  verify_overhead_ratio: number;
  serialization_loss_ms: number;
  finding_count: number;
}

// ── 编排图（goal 终态时冻结；训练详情读冻结版，goal 详情读 live 版） ──
// Spec-only kinds (task/condition/router/gate/spawn/end) render a blueprint
// version's normative graph through the same WorkflowGraph shape (Phase A,
// docs/BLUEPRINT-PROPOSAL.md §4.12) — one renderer, two graph flavors.
export type WorkflowNodeKind =
  | "goal"
  | "agent"
  | "verify"
  | "review"
  | "consult"
  | "integrate"
  | "task"
  | "condition"
  | "router"
  | "gate"
  | "spawn"
  | "end";
export type WorkflowEdgeKind =
  | "created"
  | "depends_on"
  | "seq"
  | "verified_by"
  | "consult"
  | "integrated"
  | "spec";

export interface WorkflowNode {
  key: string;
  kind: WorkflowNodeKind;
  label: string;
  assignee_id: string;
  sub_goal_id: string;
  run_id: string;
  status: string;
  attempt: number;
  reworks: number;
  duration_ms: number;
  started_at_ms: number;
  ended_at_ms: number;
  critical_share: number; // 0..1，占关键路径的时长比例
  // 验证节点专用（issue #13）：轮次是否都有计时来源。unmeasured=true 表示存在
  // 没有独立时间窗的轮次（机器验证在 daemon 内完成），duration_ms=0 应读作
  // 「未度量」而不是「不花时间」。
  unmeasured?: boolean;
  measured_rounds?: number;
  unmeasured_rounds?: number;
}

export interface WorkflowEdge {
  from: string;
  to: string;
  kind: WorkflowEdgeKind;
  source: "explicit" | "inferred";
}

export interface WorkflowGraph {
  goal_id: string;
  generated_at: string;
  frozen: boolean;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  critical_path: string[]; // 有序节点 key
  metrics: Record<string, unknown>;
}

export interface WorkflowGoalSummary {
  goal_id: string;
  goal_title: string;
  status: string;
  node_count: number;
  edge_count: number;
  critical_path_ms: number;
  parallel_speedup: number;
  rework_ratio: number;
}

export interface RsiDetail {
  session: RsiSession;
  report: RsiReport | null; // 会话还在 waiting/analyzing 时为 null
  findings: RsiFinding[];
  workflow: WorkflowGraph | null; // 分析失败/跳过时可能为 null
}

// GET /rsi/overview?days=30 —— 列表页顶部概览条。
export interface RsiOverview {
  sessions: number;
  success_rate: number; // 0..1
  p95_lead_time_ms: number;
  human_wait_ms: number;
  avg_parallel_speedup: number;
  avg_verify_overhead_ratio: number;
  avg_rework_ratio: number;
  findings: number;
  /** 旧后端可能没有该字段；页面必须容忍 undefined/空数组。 */
  trend?: RsiTrendPoint[];
}

// ── 蓝本（Phase A，docs/BLUEPRINT-PROPOSAL.md）：结构化编排权威 ──
// goal.blueprint_version_id 钉住一个 published 版本；Activate 时编译成
// gated sub_goal（拍 1 建结构，拍 2 释放时填 brief）。规范图经
// /workflows/blueprints/{versionId} 以 WorkflowGraph 形状渲染。

export interface Blueprint {
  id: string;
  name: string;
  description: string;
  created_at: string;
  latest_version_id: string; // 最新「已发布」版本 id；'' = 尚未发布任何版本
  version_count: number; // 版本总数（含草稿）
  latest_version: number; // 最新版本号（含草稿）；0 = 还没有版本
  latest_version_status: "" | "draft" | "published"; // 最新版本的状态
}

export type BlueprintVersionStatus = "draft" | "published";

export interface BlueprintVersion {
  id: string;
  blueprint_id: string;
  version: number; // 从 1 递增
  status: BlueprintVersionStatus;
  content: string; // 规范图 JSON（发布后不可变）
  content_hash: string; // sha256(content)，读取时校验
  changelog: string;
  created_at: string;
  published_at: string; // RFC3339；未发布为 ''
}

export interface BlueprintDetail {
  blueprint: Blueprint;
  versions: BlueprintVersion[];
}

// ── 蓝本人工门（B-3，§4.2）：goal 详情页的决策卡数据 ──
// 只列 OPEN 的门（已持门、等人拍板）；held_since 起超时
// timeout_after 后 sweeper 自动执行 timeout_action（decided_by=system）。
export interface HeldGateAction {
  id: string;
  label: string;
}

export interface HeldGate {
  node_key: string;
  title: string;
  prompt: string; // {{node.*}} 已在持门时解析
  actions: HeldGateAction[];
  held_since?: string;
  timeout_after?: string;
  timeout_action?: string;
  // 运行时自动判定（BUG-4 §C）：上游 output 满足 auto_when 时自动执行
  // auto_action（decided_by=auto），否则继续等人。
  auto_when?: string;
  auto_action?: string;
  sub_goal_id: string;
}

// ── scratch 环境变更账本（BUG-4 P2）：执行者对作业环境做了什么 ──
export interface EnvChange {
  id: string;
  goal_id: string;
  run_id: string;
  node_key: string;
  env_id: string;
  before_version: string;
  after_version: string;
  snapshot_ref: string;
  manifest_sha256: string;
  status: string; // started | succeeded | failed | rolled_back
  operator: string;
  started_at: string;
  finished_at: string;
  error: string;
  created_at: string;
}

// ── 计划 vs 实际对账（BUG-4）：编译图（blueprint_node）与实际执行
// （sub_goal / run / consult_request）的逐节点 join。deviations 非空即偏离。
export interface PlanAlignmentRun {
  id: string;
  role: string;
  status: string;
  summary?: string;
}

export interface PlanAlignmentNode {
  node_key: string;
  node_type: string;
  state: string;
  sub_goal_id?: string;
  sub_goal_status?: string;
  title?: string;
  runs?: PlanAlignmentRun[];
}

export interface PlanAlignmentSubGoal {
  id: string;
  title: string;
  status: string;
  assignee_id?: string;
  planned: boolean;
}

export interface PlanAlignment {
  goal_id: string;
  blueprint_version_id: string;
  blueprint_name?: string;
  pinned: boolean;
  materialized: boolean;
  nodes: PlanAlignmentNode[];
  sub_goals: PlanAlignmentSubGoal[];
  planned: number;
  settled: number;
  mapped: number;
  executed: number;
  unplanned_sub_goals: number;
  consult_count: number;
  writes: number;
  attributed_writes: number;
  unattributed_writes: number;
  unattributed_commits: string[];
  deviations: string[];
}

// ── 蓝本提案（Phase C，§12.1）：结构化补丁 + 人工批准 ──
// 批准 = 开新草稿（changelog=hypothesis），永不自动发布。
export type BlueprintPatchOp =
  | "add_node"
  | "update_node"
  | "delete_node"
  | "add_edge"
  | "delete_edge";

export interface BlueprintPatch {
  op: BlueprintPatchOp;
  id?: string;
  node?: { id: string; type: string; title: string; [k: string]: unknown };
  edge?: { source: string; source_handle?: string; target: string };
}

export interface BlueprintProposal {
  id: string;
  blueprint_id: string;
  base_version_id: string;
  title: string;
  hypothesis: string;
  expected_metric: string;
  expected_direction: string; // up|down|""
  patches: BlueprintPatch[];
  finding_id: string;
  status: "pending" | "approved" | "rejected";
  applied_version_id: string;
  decided_at: string;
  created_by_type: string;
  created_at: string;
}

export interface DiffNode {
  id: string;
  kind: string;
  base: Record<string, unknown>;
  patched: Record<string, unknown>;
}

export interface BlueprintProposalDiff {
  proposal_id: string;
  base_version_id: string;
  base: WorkflowGraph;
  patched: WorkflowGraph;
  patched_content: string;
  nodes_added: string[];
  nodes_removed: string[];
  nodes_changed: DiffNode[];
  edges_added: string[];
  edges_removed: string[];
}

export interface ABGoalFace {
  goal_id: string;
  goal_title: string;
  status: string;
  blueprint_version_id: string;
  metrics: Record<string, number | string>;
}

export interface ABCompareResult {
  goal_a: ABGoalFace;
  goal_b: ABGoalFace;
  deltas: Record<string, number>;
  notes?: string[];
}
