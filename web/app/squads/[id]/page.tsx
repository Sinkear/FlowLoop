"use client";

import { useState } from "react";
import { useCreateDialog } from "@/components/create/create-dialog";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  useSquads,
  useAgents,
  useSquadMembers,
  useAddSquadMember,
  useUpdateSquad,
  useRemoveSquadMember,
  useSquadRoles,
  useRegisterSquadRole,
  useRemoveSquadRole,
  useGoalEvents,
  useDomains,
  useConnections,
  useGoals,
  useBlueprints,
} from "@/lib/queries";
import {
  Button,
  PageBody,
  PageHeader,
  SectionCard,
  StatStrip,
  Segmented,
  Empty,
  Skeleton,
  Dialog,
  Field,
  inputCls,
} from "@/components/ui";
import { Users, Trash2, BookA } from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import type { SquadMember } from "@/lib/types";
import { SquadRecentExecutions } from "@/components/squads/squad-recent-executions";
import { SquadBlueprints } from "@/components/squads/squad-blueprints";

// 小队详情最多 6 个 tab（IA 4.5）。Phase B 起「工作流」只显示本队工作流；
// 跨队浏览走 /workflows 搜索视图，不再有「全局工作流」的说法。
type SquadTab = "overview" | "members" | "workflows";

const SQUAD_TABS: { value: SquadTab; label: string }[] = [
  { value: "overview", label: "概览" },
  { value: "members", label: "成员" },
  { value: "workflows", label: "工作流" },
];

export default function SquadDetailPage() {
  const { open } = useCreateDialog();
  useGoalEvents();
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const searchParams = useSearchParams();
  const id = params.id;
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const { data: members, isLoading } = useSquadMembers(id);
  const { data: goals } = useGoals();
  const { data: domains } = useDomains();
  const { data: connections } = useConnections(id);
  const { data: blueprints } = useBlueprints(id);
  const updateSquad = useUpdateSquad();
  const removeMember = useRemoveSquadMember();
  const [tab, setTab] = useState<SquadTab>(() => {
    const wanted = searchParams.get("tab");
    return SQUAD_TABS.some((t) => t.value === wanted) ? (wanted as SquadTab) : "overview";
  });
  const [showAdd, setShowAdd] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [editName, setEditName] = useState("");
  const [editLeader, setEditLeader] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editInstr, setEditInstr] = useState("");
  const [editDefaultWorkflow, setEditDefaultWorkflow] = useState("");

  const squad = squads?.find((s) => s.id === id);
  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;

  // 真数据派生：人数、reviewer 数、绑到本 squad 的仓库、状态为 active 的任务。
  const reviewerCount = (members ?? []).filter((m) => m.role === "reviewer").length;
  const agentMembers = (members ?? []).filter((m) => m.member_type === "agent").length;
  const humanMembers = (members ?? []).filter((m) => m.member_type === "human").length;
  // 负责仓库 = 本 squad 的连接所指向的仓库 ∪ 旧版 domain assignee 绑定的仓库。
  // Connection 是主路径；legacy 绑定仍显示，避免升级后数字突然归零。
  const legacyBoundRepos = (domains ?? [])
    .filter((d) => d.issue_repo !== "" && (d.issue_assignee === id || d.pull_assignee === id))
    .map((d) => d.issue_repo);
  const boundRepoCount = new Set([
    ...(connections ?? []).map((c) => c.repo).filter(Boolean),
    ...legacyBoundRepos,
  ]).size;
  const trackedRepoCount = (domains ?? []).filter((d) => d.type === "repo" && d.issue_repo !== "").length;
  const squadGoals = (goals ?? []).filter(
    (g) => g.assignee_type === "squad" && g.assignee_id === id,
  );
  const activeGoals = squadGoals.filter((g) => g.status === "active").length;
  const reviewGoals = squadGoals.filter((g) => g.status === "review").length;
  // 「工作流数」= 本队工作流数（Phase B：squad_id 归属）；已发布数单独说明。
  const publishedBlueprintCount = (blueprints ?? []).filter((b) => !!b.latest_version_id).length;

  const syncEditForm = () => {
    if (!squad) return;
    setEditName(squad.name);
    setEditLeader(squad.leader_id);
    setEditDesc(squad.description || "");
    setEditInstr(squad.instructions || "");
    setEditDefaultWorkflow(squad.default_workflow_id || "");
  };

  const changeTab = (next: SquadTab) => {
    setTab(next);
    // 让工作流详情能返回「工作流」tab，而不是总落回概览。
    const params = new URLSearchParams(searchParams.toString());
    if (next === "overview") params.delete("tab");
    else params.set("tab", next);
    const query = params.toString();
    router.replace(`/squads/${id}${query ? `?${query}` : ""}`, { scroll: false });
  };

  const openEdit = () => {
    syncEditForm();
    setShowEdit(true);
  };

  // 设置 tab 与编辑对话框共用同一份表单与 mutation。
  const saveSquad = (onSuccess?: () => void) => {
    updateSquad.mutate(
      {
        id,
        name: editName,
        description: editDesc,
        leader_id: editLeader,
        instructions: editInstr,
        // '' = 自动拆解（不用工作流）；只能选本队工作流，服务端会校验归属。
        default_workflow_id: editDefaultWorkflow || "",
      },
      { onSuccess }
    );
  };

  if (!squad) {
    return (
      <PageBody>
        <PageHeader title="小队详情" desc="成员、任务与仓库接入" />
        {squads === undefined ? (
          <div className="space-y-3">
            <Skeleton className="h-24" />
            <Skeleton className="h-44" />
          </div>
        ) : (
          <SectionCard bodyClassName="p-0">
            <Empty icon={<Users className="h-4 w-4" />}>
              找不到这个 Squad，可能已被删除。
              <Link href="/squads" className="mt-1 block text-accent hover:text-accent-dim">
                返回小队列表
              </Link>
            </Empty>
          </SectionCard>
        )}
      </PageBody>
    );
  }

  return (
    <PageBody>
      <PageHeader
        title={squad.name}
        desc={squad.description || "这支小队还没有描述。"}
        action={
          <div className="flex items-center gap-3">
            <Link
              href="/squads"
              className="text-xs text-ink-3 transition-colors hover:text-ink-2"
            >
              ← 返回小队列表
            </Link>
            <Button variant="outline" onClick={openEdit}>
              编辑
            </Button>
          </div>
        }
      />

      <StatStrip
        items={[
          {
            label: "Leader",
            value: (
              <span className="block truncate" title={agentName(squad.leader_id)}>
                {agentName(squad.leader_id)}
              </span>
            ),
            hint: "接任务并分派给成员",
          },
          {
            label: "成员",
            value: members?.length ?? 0,
            hint: `${agentMembers} 智能体 · ${humanMembers} 人类${reviewerCount > 0 ? ` · ${reviewerCount} 位 reviewer` : ""}`,
          },
          {
            label: "服务项目",
            value: boundRepoCount,
            tone: boundRepoCount > 0 ? "info" : "muted",
            hint: `${connections?.length ?? 0} 条仓库连接 · 共跟踪 ${trackedRepoCount} 个仓库`,
          },
          {
            label: "工作流",
            value: blueprints?.length ?? 0,
            tone: (blueprints?.length ?? 0) > 0 ? "info" : "muted",
            hint: `${publishedBlueprintCount} 个已发布 · 本队工作流`,
          },
          {
            label: "进行中任务",
            value: activeGoals,
            tone: activeGoals > 0 ? "info" : "muted",
            hint: reviewGoals > 0 ? `另有 ${reviewGoals} 个待审批` : "状态 active 的任务",
          },
        ]}
      />

      {/* 窄屏允许横向滚动：6 个 tab 不能把整页撑出横向溢出 */}
      <div className="-mx-1 overflow-x-auto px-1">
        <div className="w-max">
          <Segmented value={tab} onChange={changeTab} options={SQUAD_TABS} />
        </div>
      </div>

      {tab === "overview" && (
        <div className="space-y-6">
          {/* 小队信息：leader / 创建时间 / 成员构成 / instructions */}
          <SectionCard
            title="概览"
            desc="Leader 接任务并分派给成员；instructions 会注入 leader 的运行"
          >
            <dl className="divide-y divide-line">
              <div className="flex items-center justify-between gap-3 px-4 py-2.5">
                <dt className="text-xs text-ink-3">Leader</dt>
                <dd className="truncate text-sm text-ink-2">{agentName(squad.leader_id)}</dd>
              </div>
              <div className="flex items-center justify-between gap-3 px-4 py-2.5">
                <dt className="text-xs text-ink-3">成员构成</dt>
                <dd className="font-mono text-xs text-ink-2">
                  {members?.length ?? 0} 人（智能体 {agentMembers} · 人类 {humanMembers}）
                </dd>
              </div>
              <div className="flex items-center justify-between gap-3 px-4 py-2.5">
                <dt className="text-xs text-ink-3">创建时间</dt>
                <dd className="font-mono text-xs text-ink-2" title={squad.created_at}>
                  {new Date(squad.created_at).toLocaleString()}
                </dd>
              </div>
            </dl>
            {squad.instructions && (
              <div className="border-t border-line px-4 py-3">
                <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                  Instructions
                </div>
                <p className="mt-1 whitespace-pre-wrap text-sm leading-relaxed text-ink-2">
                  {squad.instructions}
                </p>
              </div>
            )}
          </SectionCard>

          {/* 工作流是本队最重要的资产：概览必须能一眼看到并点进去，
              不能只藏在第 4 个 tab 里。 */}
          <SectionCard
            title="本队工作流"
            count={blueprints?.length ?? 0}
            desc="任务只能钉本队已发布的版本；工作流归小队，跨队复用请复制一份。"
            action={
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => changeTab("workflows")}
                  className="text-[11px] text-ink-3 underline decoration-dotted underline-offset-2 hover:text-ink"
                >
                  查看全部工作流
                </button>
                <Link
                  href={`/goals/new?kind=workflow&squad=${id}`}
                  className="rounded-md bg-primary px-2.5 py-1.5 text-[11px] font-medium text-white hover:opacity-90"
                >
                  新建工作流
                </Link>
              </div>
            }
          >
            {!blueprints ? (
              <div className="p-4"><Skeleton className="h-16 w-full" /></div>
            ) : blueprints.length === 0 ? (
              <div className="px-4 py-5">
                <div className="text-sm text-ink-2">这支小队还没有工作流。</div>
                <p className="mt-1 text-xs leading-relaxed text-ink-3">
                  工作流把步骤、依赖和执行者固定成一张图。先建一份，任务才能钉住它；不建也能用自动拆解。
                </p>
                <p className="mt-2 text-xs text-ink-3">用本卡片右上角「新建工作流」从模板起步。</p>
              </div>
            ) : (
              <ul className="divide-y divide-line">
                {blueprints.slice(0, 3).map((b) => (
                  <li key={b.id}>
                    <Link href={`/workflows/${b.id}`} className="group flex items-center gap-3 px-4 py-3 hover:bg-surface-2">
                      <BookA className="h-4 w-4 shrink-0 text-ink-3 group-hover:text-primary" />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium text-ink">{b.name}</div>
                        <div className="mt-0.5 text-[11px] text-ink-3">
                          {b.latest_version_id
                            ? `最新 v${b.latest_version ?? "?"} · ${b.latest_version_status === "published" ? "已发布" : "草稿"} · 可绑定任务`
                            : "还没有发布版本"}
                        </div>
                      </div>
                      <span className="shrink-0 text-[11px] text-ink-3 group-hover:text-primary">打开 →</span>
                    </Link>
                  </li>
                ))}
                {(blueprints?.length ?? 0) > 3 && (
                  <li className="px-4 py-2 text-[11px] text-ink-3">
                    还有 {(blueprints?.length ?? 0) - 3} 份，去「工作流」tab 看全部。
                  </li>
                )}
              </ul>
            )}
          </SectionCard>

          {/* 编排摘要并进概览：原「编排」tab 去掉后不允许功能凭空消失。 */}
          <SquadRecentExecutions squadId={id} />
        </div>
      )}

      {tab === "members" && (
        <div className="space-y-6">
          {/* Members */}
          <SectionCard
            title="成员"
            count={members?.length ?? 0}
            desc="leader 由 squad 自身指定；reviewer 在进审批时被平台自动拉入"
            bodyClassName="p-0"
            action={
              <Button onClick={() => setShowAdd(true)} className="h-7 px-2.5 text-xs">
                + 添加
              </Button>
            }
          >
            {isLoading ? (
              <div className="divide-y divide-line">
                {[0, 1, 2].map((i) => (
                  <div key={i} className="flex items-center gap-3 px-4 py-2.5">
                    <Skeleton className="h-5 w-12" />
                    <Skeleton className="h-3 w-48" />
                  </div>
                ))}
              </div>
            ) : !members || members.length === 0 ? (
              <Empty icon={<Users className="h-4 w-4" />}>
                暂无成员。点「+ 添加」把 agent 拉进小队。
              </Empty>
            ) : (
              <ul className="divide-y divide-line">
                {members.map((m: SquadMember) => {
                  const isLeader = m.member_type === "agent" && m.member_id === squad.leader_id;
                  const memberName =
                    m.member_type === "agent" ? agentName(m.member_id) || m.member_id : m.member_id;
                  return (
                    <li key={m.id} className="flex items-center gap-3 px-4 py-2.5">
                      <span
                        className={cn(
                          "inline-flex shrink-0 items-center rounded border px-1.5 py-0.5 font-mono text-[10px] font-medium",
                          m.member_type === "agent"
                            ? "border-blue-500/30 bg-blue-500/10 text-blue-300"
                            : "border-line bg-surface-2 text-ink-2",
                        )}
                      >
                        {m.member_type}
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="truncate text-sm text-ink-2">{memberName}</span>
                          {isLeader && (
                            <span className="inline-flex items-center rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                              leader
                            </span>
                          )}
                          {m.role === "reviewer" && (
                            <span className="inline-flex items-center rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-300">
                              reviewer
                            </span>
                          )}
                          {!isLeader && m.role && m.role !== "reviewer" && (
                            <span className="font-mono text-[10px] text-ink-3">{m.role}</span>
                          )}
                        </div>
                        <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-ink-3">
                          <span className="font-mono" title={m.member_id}>
                            {m.member_id.slice(0, 8)}
                          </span>
                          <span>·</span>
                          <span title={new Date(m.created_at).toLocaleString()}>
                            {relTime(m.created_at)}加入
                          </span>
                        </div>
                      </div>
                      <button
                        onClick={() => removeMember.mutate({ squadId: id, memberId: m.member_id })}
                        disabled={removeMember.isPending && removeMember.variables?.memberId === m.member_id}
                        className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400 disabled:opacity-40"
                        title="移除该成员"
                        aria-label={`移除成员 ${memberName}`}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </SectionCard>

          {/* 成员移除失败必须显示——后端 400 错误静默等于「点了没反应」。 */}
          {removeMember.isError && (
            <p className="text-sm text-red-400">{String(removeMember.error)}</p>
          )}

          {/* 角色词汇表（BLUEPRINT-PROPOSAL §11.8）：能力词注册表。
              工作流 capability:<name> 按这里注册的词解析成员；技能差异要编码成
              不同的词（如 design-implementer / proprietary-implementer）。
              注册过任何一个词后，成员角色就只能从词表（或保留词 reviewer/member）里选。 */}
          <SquadRoleRegistry squadId={id} />
        </div>
      )}

      {/* 接入：仓库连接与 webhook */}
      {/* 工作流：本队列表（useBlueprints(id)），新建深链带上 ?squad= */}
      {tab === "workflows" && <SquadBlueprints squadId={id} />}

      {/* 设置已并入右上角「编辑」对话框；这里不再保留重复的 settings tab。 */}
      {/* Add member dialog */}
      {showAdd && (
        <AddMemberDialog
          squadId={id}
          agents={agents}
          onClose={() => setShowAdd(false)}
        />
      )}

      {showEdit && (
        <Dialog
          title={`编辑 Squad：${squad.name}`}
          onClose={() => setShowEdit(false)}
          footer={
            <>
              <Button variant="outline" onClick={() => setShowEdit(false)}>取消</Button>
              <Button
                onClick={() => saveSquad(() => setShowEdit(false))}
                disabled={updateSquad.isPending || !editName.trim() || !editLeader}
              >
                {updateSquad.isPending ? "保存中…" : "保存"}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <SquadBasicFields
              agents={agents}
              name={editName}
              setName={setEditName}
              leader={editLeader}
              setLeader={setEditLeader}
              description={editDesc}
              setDescription={setEditDesc}
              instructions={editInstr}
              setInstructions={setEditInstr}
            />
            <Field label="默认工作流" hint="小队任务默认使用它；任务里可以勾选不使用">
              <select
                className={inputCls}
                value={editDefaultWorkflow}
                onChange={(e) => setEditDefaultWorkflow(e.target.value)}
                aria-label="默认工作流"
              >
                <option value="">自动拆解（不使用工作流）</option>
                {blueprints !== undefined &&
                  editDefaultWorkflow &&
                  !blueprints.some((b) => b.id === editDefaultWorkflow) && (
                    <option value={editDefaultWorkflow}>
                      当前默认：{editDefaultWorkflow.slice(0, 8)}（不在本队列表中）
                    </option>
                  )}
                {(blueprints ?? []).map((b) => (
                  <option key={b.id} value={b.id}>
                    {b.name || b.id.slice(0, 8)}
                    {b.latest_version_id ? "" : "（未发布）"}
                  </option>
                ))}
              </select>
            </Field>
            {blueprints && blueprints.length === 0 ? (
              <p className="text-xs leading-relaxed text-ink-3">
                这支小队还没有工作流，暂时只能选「自动拆解」。
                <button
                  type="button"
                  onClick={() => open("workflow", { squadId: id })}
                  className="ml-1 text-accent hover:text-accent-dim"
                >
                  新建工作流 →
                </button>
              </p>
            ) : null}
            {/* 保存失败必须显示——leader 校验等后端 400 错误静默等于「点了没反应」。 */}
            {updateSquad.isError && (
              <p className="text-sm text-red-400">{String(updateSquad.error)}</p>
            )}
          </div>
        </Dialog>
      )}
    </PageBody>
  );
}

// SquadBasicFields: 编辑对话框与「设置」tab 共用的基础字段，
// 避免两处表单各自漂移（尤其 Leader 校验文案）。
function SquadBasicFields({
  agents,
  name,
  setName,
  leader,
  setLeader,
  description,
  setDescription,
  instructions,
  setInstructions,
}: {
  agents?: { id: string; name: string }[];
  name: string;
  setName: (v: string) => void;
  leader: string;
  setLeader: (v: string) => void;
  description: string;
  setDescription: (v: string) => void;
  instructions: string;
  setInstructions: (v: string) => void;
}) {
  return (
    <>
      <Field label="名称" hint="必填">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className={inputCls}
          placeholder="Squad 名称…"
        />
      </Field>
      <Field
        label="Leader"
        hint="必填，leader 必须是 agent（角色动态生效：旧 leader 的 in-flight run 会孤儿化）"
      >
        <select value={leader} onChange={(e) => setLeader(e.target.value)} className={inputCls}>
          <option value="">选择…</option>
          {agents?.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="描述">
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className={inputCls}
          rows={2}
        />
      </Field>
      <Field label="Instructions" hint="leader 运行时会注入这些 instructions">
        <textarea
          value={instructions}
          onChange={(e) => setInstructions(e.target.value)}
          className={inputCls}
          rows={3}
          placeholder="Squad 工作说明…"
        />
      </Field>
    </>
  );
}

// SquadRoleRegistry: the squad's role vocabulary (proposal §11.8).
// Words registered here are the ONLY capability:<name> references blueprint
// nodes can resolve against this squad; the description is the job spec the
// blueprint author and the spawn planner read. Empty registry = free-text
// roles (legacy / template-apply squads); registering the first word turns
// on the vocabulary gate (AddMember refuses unregistered words).
function SquadRoleRegistry({ squadId }: { squadId: string }) {
  const { data: roles, isLoading } = useSquadRoles(squadId);
  const registerRole = useRegisterSquadRole();
  const removeRole = useRemoveSquadRole();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    registerRole.mutate(
      { squadId, name, description: desc },
      {
        onSuccess: () => {
          setName("");
          setDesc("");
          setShowForm(false);
        },
      }
    );
  };

  return (
    <SectionCard
      title="角色词汇表"
      count={roles?.length ?? 0}
      desc="工作流 capability:<角色词> 按这里的词解析成员。技能差异请编码成不同的词（如 design-implementer / proprietary-implementer）；注册过词后成员角色只能从词表里选"
      action={
        <Button onClick={() => setShowForm((v) => !v)} className="h-7 px-2.5 text-xs">
          + 注册角色
        </Button>
      }
    >
      <div className="border-b border-line bg-surface-2/30 px-4 py-3 text-[11px] leading-relaxed text-ink-3">
        <p><span className="text-ink-2">这是什么：</span>给工作流用的岗位名。工作流节点写 <span className="font-mono text-teal-300">capability:reviewer</span>，平台就会在本队找角色是 reviewer 的成员来干。</p>
        <p className="mt-1"><span className="text-ink-2">不用也可以：</span>不注册时成员角色是自由文本，按名字直接派活；注册第一个词后才切换成词表制。</p>
        <p className="mt-1"><span className="text-ink-2">怎么配：</span>先想清楚小队里有哪些固定岗位（如 reviewer / implementer），在这里注册词并写岗位说明；成员角色再从词表里选。</p>
      </div>
      {isLoading ? (
        <div className="space-y-2">
          {[0, 1].map((i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : !roles || roles.length === 0 ? (
        <Empty icon={<BookA className="h-4 w-4" />}>
          还没注册角色词——成员角色仍是自由文本。
          注册第一个词后，本小队进入「词表制」：成员角色只能从词表（或保留词 reviewer/member）里选，
          工作流按角色词派工才有确定性保证。
        </Empty>
      ) : (
        <ul className="divide-y divide-line">
          {roles.map((r) => (
            <li key={r.id} className="flex items-start gap-3 px-4 py-2.5">
              <span className="mt-0.5 inline-flex shrink-0 items-center rounded border border-teal-500/30 bg-teal-500/10 px-1.5 py-0.5 font-mono text-[10px] font-medium text-teal-300">
                capability:{r.name}
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-sm text-ink-2">{r.description || "（未写岗位说明）"}</p>
                <p className="mt-0.5 text-[10px] text-ink-3" title={r.created_at}>
                  {relTime(r.created_at)}注册
                </p>
              </div>
              <button
                onClick={() => removeRole.mutate({ squadId, name: r.name })}
                disabled={removeRole.isPending && removeRole.variables?.name === r.name}
                className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400 disabled:opacity-40"
                title="删除该角色词（仍有成员使用时会被拒绝）"
                aria-label={`删除角色词 ${r.name}`}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </li>
          ))}
        </ul>
      )}
      {registerRole.isError && (
        <p className="mt-2 text-sm text-red-400">{String(registerRole.error)}</p>
      )}
      {removeRole.isError && (
        <p className="mt-2 text-sm text-red-400">{String(removeRole.error)}</p>
      )}
      {showForm && (
        <form onSubmit={handleSubmit} className="mt-3 space-y-3 border-t border-line pt-3">
          <Field label="角色词" hint="小写词，可用数字、-、_（就是工作流里 capability: 后面那一段）">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className={inputCls}
              placeholder="design-implementer"
              required
              pattern="[a-z][a-z0-9_-]*"
            />
          </Field>
          <Field label="岗位说明" hint="这个角色干什么、隐含什么专有技能——工作流作者和拆解规划者都会读它">
            <textarea
              value={desc}
              onChange={(e) => setDesc(e.target.value)}
              className={`${inputCls} min-h-20`}
              placeholder="负责 UI/视觉实现，持有设计技能…"
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setShowForm(false)}>取消</Button>
            <Button type="submit" disabled={registerRole.isPending || !name.trim()}>
              {registerRole.isPending ? "…" : "注册"}
            </Button>
          </div>
        </form>
      )}
    </SectionCard>
  );
}

function AddMemberDialog({
  squadId,
  agents,
  onClose,
}: {
  squadId: string;
  agents?: { id: string; name: string }[];
  onClose: () => void;
}) {
  const addMember = useAddSquadMember();
  const { data: roles } = useSquadRoles(squadId);
  const [memberType, setMemberType] = useState("agent");
  const [memberId, setMemberId] = useState("");
  const [role, setRole] = useState("member");

  // §11.8 vocabulary gate: once the squad registers ANY role word, the picker
  // lists registry words + reserved words only; unregistered squads keep the
  // legacy two options (free text still allowed server-side).
  const registryActive = (roles?.length ?? 0) > 0;
  const roleOptions = registryActive
    ? [
        { value: "member", label: "member（执行者）" },
        { value: "reviewer", label: "reviewer（审核者，进审批时被平台拉去审查）" },
        ...roles!.map((r) => ({
          value: r.name,
          label: `${r.name}（${r.description ? r.description.slice(0, 20) : "见角色词汇表"}）`,
        })),
      ]
    : [
        { value: "member", label: "执行者（被 leader 分派干活）" },
        { value: "reviewer", label: "审核者（进审批时被平台自动拉去审查）" },
      ];

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    addMember.mutate(
      { squadId, member_type: memberType, member_id: memberId, role },
      { onSuccess: onClose }
    );
  };

  return (
    <Dialog
      title="添加成员"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="add-member-form" disabled={addMember.isPending}>
            {addMember.isPending ? "…" : "添加"}
          </Button>
        </>
      }
    >
      <form id="add-member-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="成员类型">
          <select value={memberType} onChange={(e) => { setMemberType(e.target.value); setMemberId(""); }} className={inputCls}>
            <option value="agent">Agent</option>
            <option value="human">人类</option>
          </select>
        </Field>
        {memberType === "agent" && (
          <Field label="选择 Agent">
            <select value={memberId} onChange={(e) => setMemberId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>{a.name}</option>
              ))}
            </select>
          </Field>
        )}
        {memberType === "human" && (
          <Field label="Human ID">
            <input value={memberId} onChange={(e) => setMemberId(e.target.value)} className={inputCls} required placeholder="人类成员 id…" />
          </Field>
        )}
        <Field label="角色" hint={registryActive ? "角色词已注册：只能从词表选择（BLUEPRINT-PROPOSAL §11.8）" : "本小队未注册角色词汇表，暂用自由文本；想让工作流按角色派工，请先在上方「角色词汇表」注册"}>
          <select value={role} onChange={(e) => setRole(e.target.value)} className={inputCls}>
            {roleOptions.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </Field>
        {addMember.isError && (
          <p className="text-sm text-red-400">{String(addMember.error)}</p>
        )}
      </form>
    </Dialog>
  );
}
