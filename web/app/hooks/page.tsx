"use client";

import { useState } from "react";
import { Webhook, Pencil, FileCode2, Settings2 } from "lucide-react";
import { ParamsEditor } from "@/components/params-editor";
import {
  useHookEvents,
  useHooks,
  useCreateHook,
  useUpdateHook,
  useUpdateHookContent,
  useTestHook,
  useDeleteHook,
  useHookRuns,
} from "@/lib/queries";
import {
  Button,
  Dialog,
  Empty,
  Field,
  inputCls,
  PageBody,
  PageHeader,
  SectionCard,
  Skeleton,
  StatStrip,
  Toolbar,
} from "@/components/ui";
import { relTime } from "@/lib/utils";
import {
  HOOK_EVENTS,
  hookEventLabel,
  type Hook,
  type HookListItem,
  type HookRun,
} from "@/lib/types";

const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_120px_210px_170px] md:items-center md:gap-3";
const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_120px_210px_170px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

// 运行状态 → 徽标样式（skills 页 SYNC_STATE_META 同款）。running 用绿色脉冲点
// 之外的静态样式即可——列表刷新靠 WS invalidate，不做逐帧动画。
const RUN_STATUS_META: Record<string, { label: string; cls: string }> = {
  running: { label: "执行中", cls: "border-green-500/30 bg-green-500/10 text-green-300" },
  completed: { label: "已完成", cls: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" },
  failed: { label: "失败", cls: "border-red-500/30 bg-red-500/10 text-red-300" },
  timeout: { label: "超时", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  skipped: { label: "已跳过", cls: "border-line-strong bg-surface-2 text-ink-3" },
};

const INTERPRETERS = [
  { value: "", label: "遵循脚本 shebang（默认）" },
  { value: "bash", label: "bash" },
  { value: "sh", label: "sh" },
  { value: "python3", label: "python3" },
  { value: "node", label: "node" },
];

function runStatusMeta(status?: string) {
  return RUN_STATUS_META[status ?? ""] ?? { label: "从未运行", cls: "border-line-strong bg-surface-2 text-ink-3" };
}

function RunStatusChip({ status }: { status?: string }) {
  const m = runStatusMeta(status);
  return (
    <span className={`inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-md border ${m.cls}`}>
      {m.label}
    </span>
  );
}

// tripwire 徽标（spec §4.2 可见性信号）：脚本内容与最近一次实际运行的
// content_hash 不一致——只提示，不拦截。
function TripwireChip() {
  return (
    <span
      className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-md border border-amber-500/30 bg-amber-500/10 text-amber-300"
      title="脚本内容自上次运行后有修改；下次触发时将执行新内容"
    >
      内容自上次运行后有修改
    </span>
  );
}

function fullTime(iso: string): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

function HookRow({
  item,
  onDetail,
  onTest,
  onDelete,
  onToggleEnabled,
  deleting,
}: {
  item: HookListItem;
  onDetail: (h: HookListItem) => void;
  onTest: (h: HookListItem) => void;
  onDelete: (h: HookListItem) => void;
  onToggleEnabled: (h: HookListItem) => void;
  deleting: boolean;
}) {
  const h = item.hook;
  return (
    <li className={ROW_GRID}>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium text-ink">{h.name}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">{h.id.slice(0, 8)}</span>
          {!h.enabled && (
            <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-md border border-line-strong bg-surface-2 text-ink-3">
              已停用
            </span>
          )}
          {item.content_changed_since_last_run && <TripwireChip />}
        </div>
        <p className="mt-0.5 text-xs leading-relaxed text-ink-3">{h.description || "（无描述）"}</p>
        <p className="mt-0.5 font-mono text-[10px] text-ink-3">
          {h.interpreter ? h.interpreter : "shebang"} · 超时 {h.timeout_sec}s ·{" "}
          <span title={`content_hash: ${h.content_hash}`}>{h.content_hash.slice(0, 12)}</span>
        </p>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">绑定</span>
        <span className="font-mono text-ink-2">{item.binding_count}</span>
      </div>

      <div className="min-w-0">
        <RunStatusChip status={item.last_run_status} />
        {item.last_run_finished_at && (
          <p className="mt-0.5 text-[10px] text-ink-3" title={fullTime(item.last_run_finished_at)}>
            {relTime(item.last_run_finished_at)}
          </p>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs">
        <button
          type="button"
          onClick={() => onToggleEnabled(item)}
          className="text-ink-2 transition-colors hover:text-accent"
        >
          {h.enabled ? "停用" : "启用"}
        </button>
        <button type="button" onClick={() => onTest(item)} className="text-ink-2 transition-colors hover:text-accent">
          试跑
        </button>
        <button type="button" onClick={() => onDetail(item)} className="text-ink-2 transition-colors hover:text-accent">
          详情
        </button>
        <button
          type="button"
          disabled={deleting}
          onClick={() => {
            if (confirm(`删除 hook「${h.name}」？其绑定会一并删除；历史运行记录保留。`)) onDelete(item);
          }}
          className="text-ink-3 transition-colors hover:text-red-400 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {deleting ? "删除中…" : "删除"}
        </button>
      </div>
    </li>
  );
}

// 新建 Hook：JSON 模式（name/description/interpreter/timeout/content/params），
// 支持从本地文件加载脚本文本（后端同一端点也收 multipart，这里走 JSON）。
function CreateHookDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateHook();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [interpreter, setInterpreter] = useState("");
  const [timeoutSec, setTimeoutSec] = useState(60);
  const [content, setContent] = useState("#!/bin/sh\n# 工作流结束后执行：stdin 收到一条 JSON（事件名、goal 标题等）\n");
  const [params, setParams] = useState<Record<string, string> | undefined>(undefined);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    create.mutate(
      { name, description, content, interpreter, timeout_sec: timeoutSec, params },
      { onSuccess: onClose }
    );
  };

  return (
    <Dialog
      title="新建 Hook"
      onClose={onClose}
      wide
      footer={
        <>
          <Button type="button" variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" form="hook-form" disabled={create.isPending}>
            {create.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="hook-form" onSubmit={submit} className="space-y-4">
        <Field label="名称" hint="小写字母开头，仅小写字母/数字/连字符，如 notify-wechat">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
            required
            pattern="[a-z][a-z0-9-]{0,63}"
            placeholder="notify-wechat"
          />
        </Field>
        <Field label="描述">
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className={inputCls}
            placeholder="一句话说明这个 hook 干什么"
          />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="解释器" hint="默认遵循脚本首行 shebang">
            <select value={interpreter} onChange={(e) => setInterpreter(e.target.value)} className={inputCls}>
              {INTERPRETERS.map((it) => (
                <option key={it.value} value={it.value}>
                  {it.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="超时（秒）" hint="1–600，默认 60">
            <input
              type="number"
              min={1}
              max={600}
              value={timeoutSec}
              onChange={(e) => setTimeoutSec(Number(e.target.value))}
              className={inputCls}
            />
          </Field>
        </div>
        <Field
          label="脚本"
          hint="通过 stdin 收到一条 JSON（hook_event_name、goal_id、title 等）；退出码 0 = 成功，非 0 只记录失败，不影响任务状态"
        >
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            className={`${inputCls} font-mono text-xs`}
            rows={10}
            required
            spellCheck={false}
          />
        </Field>
        <Field label="从文件加载" hint="可选：选一个本地脚本文件填入上面的编辑框">
          <input
            type="file"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              if (f) setContent(await f.text());
            }}
            className={`${inputCls} file:mr-3 file:rounded-lg file:border-0 file:bg-accent/10 file:px-3 file:py-1 file:text-sm file:text-accent`}
          />
        </Field>
        <Field
          label="默认参数"
          hint="扁平 {参数名: 值}。注入三通道：stdin payload 的 params 键、AGENTWORK_HOOK_PARAMS（完整 JSON）、AGENTWORK_PARAM_<参数名>（扁平 env）。绑定时可按绑定覆盖"
        >
          <ParamsEditor value={params} onChange={setParams} />
        </Field>
        {create.isError && <p className="text-sm text-red-400">{String(create.error)}</p>}
      </form>
    </Dialog>
  );
}

// 运行历史表：详情对话框内复用。
function RunsList({ runs }: { runs: HookRun[] }) {
  if (!runs.length) return <p className="px-4 py-6 text-center text-sm text-ink-3">还没有运行记录</p>;
  return (
    <ul className="divide-y divide-line">
      {runs.map((r) => (
        <li key={r.id} className="px-4 py-2.5">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <RunStatusChip status={r.status} />
            <span className="text-xs text-ink-2">{hookEventLabel(r.event)}</span>
            {r.exit_code != null && (
              <span className={`font-mono text-[11px] ${r.exit_code === 0 ? "text-emerald-300" : "text-red-400"}`}>
                exit {r.exit_code}
              </span>
            )}
            {r.goal_id && (
              <span className="font-mono text-[11px] text-ink-3" title={r.goal_id}>
                goal {r.goal_id.slice(0, 8)}
              </span>
            )}
            <span className="ml-auto text-[11px] text-ink-3" title={`${fullTime(r.started_at)} → ${fullTime(r.finished_at)}`}>
              {relTime(r.started_at)}
            </span>
          </div>
          {r.output && (
            <details className="mt-1">
              <summary className="cursor-pointer select-none text-[11px] text-ink-3 hover:text-ink-2">输出</summary>
              <pre className="mt-1 max-h-48 overflow-auto rounded bg-surface-2 p-2 font-mono text-[11px] leading-relaxed text-ink-2 whitespace-pre-wrap break-all">
                {r.output}
              </pre>
            </details>
          )}
        </li>
      ))}
    </ul>
  );
}

// 详情对话框：脚本只读 + 运行历史 + 编辑脚本入口（编辑会点亮 tripwire 徽标）
// + 编辑信息入口（name/description/interpreter/timeout/params —— 不点亮 tripwire）。
function HookDetailDialog({ item, onClose }: { item: HookListItem; onClose: () => void }) {
  const [editing, setEditing] = useState(false);
  const [editingMeta, setEditingMeta] = useState(false);
  const updateContent = useUpdateHookContent();
  const { data: runs, isLoading: runsLoading } = useHookRuns(item.hook.id, 20);
  const [draft, setDraft] = useState<string | null>(null);
  const h = item.hook;

  if (editingMeta) {
    return <EditHookMetaDialog item={item} onClose={() => setEditingMeta(false)} />;
  }

  return (
    <Dialog
      title={`Hook · ${h.name}`}
      onClose={onClose}
      wide
      footer={
        <>
          <Button type="button" variant="outline" onClick={onClose}>
            关闭
          </Button>
          <Button type="button" variant="outline" onClick={() => setEditingMeta(true)}>
            <Settings2 className="mr-1 h-3.5 w-3.5" />
            编辑信息
          </Button>
          {editing ? (
            <Button
              type="button"
              disabled={updateContent.isPending}
              onClick={() =>
                updateContent.mutate(
                  { id: h.id, content: draft ?? h.content ?? "" },
                  { onSuccess: () => setEditing(false) }
                )
              }
            >
              {updateContent.isPending ? "保存中…" : "保存脚本"}
            </Button>
          ) : (
            <Button type="button" variant="outline" onClick={() => setEditing(true)}>
              <Pencil className="mr-1 h-3.5 w-3.5" />
              编辑脚本
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-xs text-ink-3">
          <span className="font-mono">{h.id.slice(0, 8)}</span> · {h.interpreter || "shebang"} · 超时 {h.timeout_sec}s ·{" "}
          绑定 {item.binding_count} 处
          {item.content_changed_since_last_run && (
            <>
              {" · "}
              <span className="text-amber-300">内容自上次运行后有修改</span>
            </>
          )}
        </p>
        {h.description && <p className="text-sm text-ink-2">{h.description}</p>}
        {h.params && Object.keys(h.params).length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(h.params).map(([k, v]) => (
              <span key={k} className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-2" title={v}>
                {k}={v.length > 24 ? `${v.slice(0, 24)}…` : v}
              </span>
            ))}
          </div>
        )}
        {editing ? (
          <textarea
            value={draft ?? h.content ?? ""}
            onChange={(e) => setDraft(e.target.value)}
            className={`${inputCls} font-mono text-xs`}
            rows={12}
            spellCheck={false}
          />
        ) : (
          <pre className="max-h-64 overflow-auto rounded bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-ink-2 whitespace-pre-wrap break-all">
            {h.content || "（脚本内容为空）"}
          </pre>
        )}
        {editing && updateContent.isError && <p className="text-sm text-red-400">{String(updateContent.error)}</p>}
        <div>
          <h3 className="mb-1 text-xs font-medium text-ink-2">最近运行</h3>
          {runsLoading ? (
            <Skeleton className="h-16" />
          ) : (
            <div className="overflow-hidden rounded-lg border border-line">
              <RunsList runs={runs ?? []} />
            </div>
          )}
        </div>
        <p className="text-[11px] text-ink-3">
          绑定关系在「任务详情」或项目页的「配置」里管理；此处只读展示脚本与历史。
        </p>
      </div>
    </Dialog>
  );
}

// 编辑信息：meta 字段（不含脚本内容）。params 是 replace-all 语义——
// 每次保存都提交编辑器里的完整参数集（清空所有行即删除全部参数）。
function EditHookMetaDialog({ item, onClose }: { item: HookListItem; onClose: () => void }) {
  const updateMeta = useUpdateHook();
  const h = item.hook;
  const [name, setName] = useState(h.name);
  const [description, setDescription] = useState(h.description);
  const [interpreter, setInterpreter] = useState(h.interpreter);
  const [timeoutSec, setTimeoutSec] = useState(h.timeout_sec);
  const [params, setParams] = useState<Record<string, string> | undefined>(h.params);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    updateMeta.mutate(
      { id: h.id, body: { name, description, interpreter, timeout_sec: timeoutSec, params } },
      { onSuccess: onClose }
    );
  };

  return (
    <Dialog
      title={`编辑信息 · ${h.name}`}
      onClose={onClose}
      footer={
        <>
          <Button type="button" variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" form="hook-meta-form" disabled={updateMeta.isPending}>
            {updateMeta.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <form id="hook-meta-form" onSubmit={submit} className="space-y-4">
        <Field label="名称" hint="小写字母开头，仅小写字母/数字/连字符">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className={inputCls}
            required
            pattern="[a-z][a-z0-9-]{0,63}"
          />
        </Field>
        <Field label="描述">
          <input value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="解释器" hint="默认遵循脚本首行 shebang">
            <select value={interpreter} onChange={(e) => setInterpreter(e.target.value)} className={inputCls}>
              {INTERPRETERS.map((it) => (
                <option key={it.value} value={it.value}>
                  {it.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="超时（秒）" hint="1–600">
            <input
              type="number"
              min={1}
              max={600}
              value={timeoutSec}
              onChange={(e) => setTimeoutSec(Number(e.target.value))}
              className={inputCls}
            />
          </Field>
        </div>
        <Field label="默认参数" hint="保存即整体替换；脚本通过 stdin params / AGENTWORK_HOOK_PARAMS / AGENTWORK_PARAM_<名> 三通道读取">
          <ParamsEditor value={params} onChange={setParams} />
        </Field>
        {updateMeta.isError && <p className="text-sm text-red-400">{String(updateMeta.error)}</p>}
        <p className="text-[11px] text-ink-3">改名/描述/参数不会点亮 tripwire 徽标；只有改脚本内容会。</p>
      </form>
    </Dialog>
  );
}

// 试跑结果：POST /hooks/{id}/test 返回的 hook_run 记录（真实执行一次）。
function TestResultDialog({ run, onClose }: { run: HookRun; onClose: () => void }) {
  return (
    <Dialog
      title="试跑结果"
      onClose={onClose}
      wide
      footer={
        <Button type="button" variant="outline" onClick={onClose}>
          关闭
        </Button>
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <RunStatusChip status={run.status} />
          {run.exit_code != null && (
            <span className={`font-mono text-xs ${run.exit_code === 0 ? "text-emerald-300" : "text-red-400"}`}>
              exit {run.exit_code}
            </span>
          )}
          <span className="text-xs text-ink-3">{hookEventLabel(run.event)}</span>
          <span className="text-[11px] text-ink-3" title={fullTime(run.started_at)}>
            {relTime(run.started_at)}
          </span>
        </div>
        <div>
          <span className="text-[11px] text-ink-3">content_hash</span>
          <p className="break-all font-mono text-[11px] text-ink-2">{run.content_hash}</p>
        </div>
        <div>
          <span className="text-[11px] text-ink-3">输出（stdout + stderr，末 8 KiB）</span>
          <pre className="mt-1 max-h-64 overflow-auto rounded bg-surface-2 p-3 font-mono text-[11px] leading-relaxed text-ink-2 whitespace-pre-wrap break-all">
            {run.output || "（无输出）"}
          </pre>
        </div>
      </div>
    </Dialog>
  );
}

export default function HooksPage() {
  useHookEvents(); // hook:started / hook:completed → 列表与详情实时刷新
  const { data: items, isLoading } = useHooks();
  const create = useCreateHook();
  const del = useDeleteHook();
  const test = useTestHook();
  const updateMeta = useUpdateHook();
  const [showCreate, setShowCreate] = useState(false);
  const [detail, setDetail] = useState<HookListItem | null>(null);
  const [testResult, setTestResult] = useState<HookRun | null>(null);
  const [q, setQ] = useState("");

  const list = items ?? [];
  const filtered = q
    ? list.filter(
        (it) =>
          it.hook.name.toLowerCase().includes(q.toLowerCase()) ||
          it.hook.description.toLowerCase().includes(q.toLowerCase())
      )
    : list;
  const enabledCount = list.filter((it) => it.hook.enabled).length;
  const bindingTotal = list.reduce((n, it) => n + it.binding_count, 0);
  const recentFailed = list.filter((it) => it.last_run_status === "failed" || it.last_run_status === "timeout").length;

  const doTest = (it: HookListItem) => {
    test.mutate({ id: it.hook.id }, { onSuccess: (run) => setTestResult(run) });
  };
  const toggleEnabled = (it: HookListItem) => {
    updateMeta.mutate({ id: it.hook.id, body: { enabled: !it.hook.enabled } });
  };

  return (
    <PageBody>
      <PageHeader
        title="Hooks"
        desc="工作流结束后执行的自定义脚本——通知、度量、清理、回写外部系统。在任务或项目上绑定，事件触发时由 daemon 执行。"
        action={
          <Button onClick={() => setShowCreate(true)}>
            <FileCode2 className="mr-1 h-4 w-4" />
            新建 Hook
          </Button>
        }
      />

      <StatStrip
        items={[
          { label: "HOOKS", value: list.length, hint: `启用 ${enabledCount} · 停用 ${list.length - enabledCount}` },
          { label: "绑定", value: bindingTotal, hint: "任务级 + 项目级" },
          {
            label: "最近运行失败",
            value: recentFailed,
            hint: "最后一次运行为 failed/timeout 的 hook 数",
            tone: recentFailed > 0 ? "err" : "ok",
          },
        ]}
      />

      <SectionCard
        title="全部 Hooks"
        count={filtered.length}
        desc="试跑用合成样例真实执行一次"
        bodyClassName="p-0"
        action={
          <Toolbar>
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="搜索名称或描述…"
              className={`${inputCls} h-8 w-48 py-1`}
            />
          </Toolbar>
        }
      >
        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : !filtered.length ? (
          <Empty icon={<Webhook className="h-5 w-5" />}>
            还没有 Hook。上传一个脚本，再到任务详情或项目配置里把它绑到「任务完成」等事件上。
          </Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>名称 / 说明</span>
              <span>绑定</span>
              <span>最近运行</span>
              <span className="text-right">操作</span>
            </div>
            <ul className="divide-y divide-line">
              {filtered.map((it) => (
                <HookRow
                  key={it.hook.id}
                  item={it}
                  onDetail={setDetail}
                  onTest={doTest}
                  onDelete={(x) => del.mutate(x.hook.id)}
                  onToggleEnabled={toggleEnabled}
                  deleting={del.isPending && del.variables === it.hook.id}
                />
              ))}
            </ul>
          </>
        )}
        {test.isError && <p className="px-4 py-2 text-sm text-red-400">{String(test.error)}</p>}
      </SectionCard>

      {showCreate && <CreateHookDialog onClose={() => setShowCreate(false)} />}
      {detail && <HookDetailDialog item={detail} onClose={() => setDetail(null)} />}
      {testResult && <TestResultDialog run={testResult} onClose={() => setTestResult(null)} />}
    </PageBody>
  );
}
