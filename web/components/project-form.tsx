"use client";

import { useState, type ReactNode } from "react";
import { FolderOpen, GitBranch } from "lucide-react";
import { Button, Dialog, Field, inputCls, Segmented } from "@/components/ui";
import { cn } from "@/lib/utils";
import { WebhookPanel } from "@/components/webhook-panel";
import {
  useAgents,
  useSquads,
  useCreateDomain,
  useUpdateDomain,
  useSetDomainAcceptanceLevel,
} from "@/lib/queries";
import type { Agent, Domain, Squad } from "@/lib/types";

type ProjectType = "repo" | "scratch";
type Acceptance = "" | "hitl" | "hootl";

// 项目类型是新建项目时最影响后续流程的一个选择（建完后不能改），所以做成
// 两张大的可选卡片，而不是一条不显眼的 segmented。图标 + 一句后果说明，
// 让人在点「保存」之前就知道自己选的是哪种工作方式。
const PROJECT_TYPES: {
  value: ProjectType;
  label: string;
  desc: string;
  icon: ReactNode;
}[] = [
  {
    value: "repo",
    label: "代码仓库",
    desc: "有共享仓库：每个任务一个独立 worktree，可提交 PR。",
    icon: <GitBranch className="h-4 w-4" />,
  },
  {
    value: "scratch",
    label: "无仓库工作区",
    desc: "持久项目目录：适合研究、信息整理、文档类任务。",
    icon: <FolderOpen className="h-4 w-4" />,
  },
];

const ACCEPTANCE_DESC: Record<Acceptance, string> = {
  "": "暂不指定，目标创建时再定（默认按人工验收）。",
  hitl: "任务跑完停在人工审批，人确认后才算完成。",
  hootl: "机器验收通过即自动完成，不再等人审批。",
};

// deriveGitTarget splits the repo URL into the webhook provider + owner/repo.
// The host decides the provider; anything that isn't GitCode is treated as
// GitHub (the same "default to GitHub" rule the old form used).
function deriveGitTarget(gitUrl: string): { provider: string; repo: string } {
  let u = gitUrl.trim().replace(/^https?:\/\//i, "").replace(/\.git$/i, "").replace(/\/+$/, "");
  const at = u.indexOf("@");
  if (at >= 0) u = u.slice(at + 1).replace(":", "/");
  const parts = u.replace(/^\/+/, "").split("/").filter(Boolean);
  if (parts.length < 3) return { provider: "", repo: "" };
  return {
    provider: parts[0].toLowerCase().includes("gitcode") ? "gitcode" : "github",
    repo: `${parts[parts.length - 2]}/${parts[parts.length - 1]}`,
  };
}

// Fold is the low-frequency section inside the dialog: a bordered <details>
// so 自动处理 / Webhook 接入 never push the primary fields below the fold.
function Fold({ summary, hint, children }: { summary: string; hint?: string; children: React.ReactNode }) {
  return (
    <details className="rounded-lg border border-line bg-surface-2/30 px-3 py-2.5">
      <summary className="cursor-pointer select-none text-xs font-medium text-ink-2 hover:text-ink">
        {summary}
        {hint && <span className="ml-1.5 font-normal text-ink-3">{hint}</span>}
      </summary>
      <div className="mt-3 space-y-3 border-t border-line pt-3">{children}</div>
    </details>
  );
}

// AssigneePicker combines agents and squads in one select; the option value
// carries the kind ("agent:id" / "squad:id") so both columns stay in sync.
function AssigneePicker({
  label,
  hint,
  assignee,
  assigneeType,
  agents,
  squads,
  onChange,
}: {
  label: string;
  hint?: string;
  assignee: string;
  assigneeType: string;
  agents?: Agent[];
  squads?: Squad[];
  onChange: (id: string, type: string) => void;
}) {
  const value = assignee ? `${assigneeType || "agent"}:${assignee}` : "";
  const pick = (raw: string) => {
    if (!raw) return onChange("", "");
    const i = raw.indexOf(":");
    onChange(raw.slice(i + 1), raw.slice(0, i));
  };
  return (
    <Field label={label} hint={hint}>
      <select value={value} onChange={(e) => pick(e.target.value)} className={inputCls}>
        <option value="">不自动处理</option>
        <optgroup label="Agent">
          {(agents ?? []).map((a) => (
            <option key={a.id} value={`agent:${a.id}`}>{a.name}</option>
          ))}
        </optgroup>
        <optgroup label="小队">
          {(squads ?? []).map((s) => (
            <option key={s.id} value={`squad:${s.id}`}>{s.name}</option>
          ))}
        </optgroup>
      </select>
    </Field>
  );
}

export function ProjectForm({ project, onClose }: { project?: Domain; onClose: () => void }) {
  const isEdit = !!project;
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const create = useCreateDomain();
  const update = useUpdateDomain();
  const setLevel = useSetDomainAcceptanceLevel();

  const [name, setName] = useState(project?.name ?? "");
  const [type, setType] = useState<ProjectType>(project?.type === "scratch" ? "scratch" : "repo");
  const [gitUrl, setGitUrl] = useState(project?.git_url ?? "");
  const [branch, setBranch] = useState(project?.default_branch ?? "");
  const [identity, setIdentity] = useState(project?.git_identity ?? "");
  // 编辑时留空 = 保持原令牌（提交时回填 project.git_credentials）。
  const [credentials, setCredentials] = useState("");
  const [level, setLevelState] = useState<Acceptance>(
    project?.acceptance_level === "hitl" || project?.acceptance_level === "hootl" ? project.acceptance_level : ""
  );
  const [issueAssignee, setIssueAssignee] = useState(project?.issue_assignee ?? "");
  const [issueType, setIssueType] = useState(project?.issue_assignee_type || "agent");
  const [pullAssignee, setPullAssignee] = useState(project?.pull_assignee ?? "");
  const [pullType, setPullType] = useState(project?.pull_assignee_type || "agent");
  const [error, setError] = useState("");

  const isRepo = type === "repo";
  const pending = create.isPending || update.isPending || setLevel.isPending;
  const gitTarget = deriveGitTarget(gitUrl);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const body = {
      name: name.trim(),
      git_url: isRepo ? gitUrl.trim() : "",
      default_branch: isRepo ? branch.trim() || "main" : "",
      git_identity: isRepo ? identity.trim() : "",
      git_credentials: isRepo ? credentials.trim() || project?.git_credentials || "" : "",
      // 解析不出来时保留后端已有的 issue 路由，避免一次改名把 issue 轮询清空。
      issue_repo: isRepo ? gitTarget.repo || project?.issue_repo || "" : "",
      issue_provider: isRepo ? gitTarget.provider || project?.issue_provider || "" : "",
      issue_assignee: isRepo ? issueAssignee : "",
      issue_assignee_type: isRepo ? issueType : "",
      pull_assignee: isRepo ? pullAssignee : "",
      pull_assignee_type: isRepo ? pullType : "",
    };
    if (!project) {
      create.mutate(
        { ...body, type, acceptance_level: level },
        { onSuccess: onClose, onError: (err) => setError(String(err)) }
      );
      return;
    }
    // Update first (empty acceptance_level means "unchanged" there), then
    // always set the level explicitly — that PATCH is the only way back to 待定.
    update.mutate(
      { id: project.id, body },
      {
        onSuccess: () =>
          setLevel.mutate(
            { id: project.id, level },
            { onSuccess: onClose, onError: (err) => setError(String(err)) }
          ),
        onError: (err) => setError(String(err)),
      }
    );
  };

  return (
    <Dialog
      title={project ? `配置项目：${project.name}` : "新建项目"}
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="project-form" disabled={pending || !name.trim() || (isRepo && !gitUrl.trim())}>
            {pending ? "保存中…" : project ? "保存" : "创建"}
          </Button>
        </>
      }
    >
      <form id="project-form" onSubmit={submit} className="space-y-4">
        <Field label="名称">
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputCls} placeholder="如：agentwork" required />
        </Field>

        <Field label="项目类型" hint={project ? "类型创建后不可修改" : undefined}>
          <div
            className={cn("grid grid-cols-2 gap-2", project && "pointer-events-none opacity-50")}
            aria-disabled={project ? true : undefined}
            role="radiogroup"
            aria-label="项目类型"
          >
            {PROJECT_TYPES.map((t) => {
              const active = type === t.value;
              return (
                <button
                  key={t.value}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  onClick={() => setType(t.value)}
                  className={cn(
                    "flex min-h-[92px] items-start gap-2.5 rounded-md border p-3 text-left transition-colors duration-150",
                    active
                      ? "border-accent bg-surface-2"
                      : "border-line bg-surface-1 hover:border-line-strong hover:bg-surface-2"
                  )}
                >
                  <span
                    className={cn(
                      "mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md",
                      active ? "bg-accent text-accent-ink" : "bg-surface-3 text-ink-3"
                    )}
                  >
                    {t.icon}
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-medium text-ink">{t.label}</span>
                    <span className="mt-1 block text-xs leading-relaxed text-ink-3">{t.desc}</span>
                  </span>
                </button>
              );
            })}
          </div>
        </Field>

        {isRepo && (
          <Field label="仓库地址">
            <input value={gitUrl} onChange={(e) => setGitUrl(e.target.value)} className={inputCls} placeholder="https://github.com/you/repo.git" required />
          </Field>
        )}

        {isRepo && (
          <div className="grid grid-cols-2 gap-3">
            <Field label="默认分支">
              <input value={branch} onChange={(e) => setBranch(e.target.value)} className={inputCls} placeholder="main" />
            </Field>
            <Field label="提交身份">
              <input value={identity} onChange={(e) => setIdentity(e.target.value)} className={inputCls} placeholder="agentwork-bot <bot@example.com>" />
            </Field>
          </div>
        )}

        {isRepo && (
          <Field label="访问令牌" hint="用于 clone / push / webhook / 读写 PR；编辑时留空表示保持原令牌。">
            <input
              type="password"
              value={credentials}
              onChange={(e) => setCredentials(e.target.value)}
              className={inputCls}
              placeholder={project ? "留空 = 保持原令牌" : "GitHub PAT 或 GitCode token"}
              autoComplete="new-password"
            />
          </Field>
        )}

        <Field label="验收级别" hint={ACCEPTANCE_DESC[level]}>
          <Segmented
            value={level}
            onChange={(v) => setLevelState(v)}
            options={[
              { value: "", label: "待定" },
              { value: "hitl", label: "人工验收 HITL" },
              { value: "hootl", label: "自动验收 HOOTL" },
            ]}
          />
        </Field>

        {isRepo && (
          <Fold summary="自动处理 PR / Issue" hint="可选">
            <p className="text-xs text-ink-3">
              选定处理方后，仓库 webhook 收到的新 PR / Issue 会自动创建任务（合并 / 关闭仍由人决定）。
            </p>
            <AssigneePicker
              label="处理 PR 的 Agent / 小队"
              assignee={pullAssignee}
              assigneeType={pullType}
              agents={agents}
              squads={squads}
              onChange={(id, t) => { setPullAssignee(id); setPullType(t || "agent"); }}
            />
            <AssigneePicker
              label="处理 Issue 的 Agent / 小队"
              assignee={issueAssignee}
              assigneeType={issueType}
              agents={agents}
              squads={squads}
              onChange={(id, t) => { setIssueAssignee(id); setIssueType(t || "agent"); }}
            />
          </Fold>
        )}

        {project && project.type === "repo" && gitTarget.repo && (
          <Fold summary="Webhook 接入" hint={gitTarget.repo}>
            <div className="rounded-md border border-line p-3">
              <WebhookPanel domainId={project.id} provider={gitTarget.provider} repo={gitTarget.repo} />
            </div>
          </Fold>
        )}

        {error && <p className="text-sm text-red-400 break-words">{error}</p>}
      </form>
    </Dialog>
  );
}