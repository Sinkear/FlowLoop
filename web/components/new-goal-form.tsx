"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Button, Dialog, Field, SectionCard, inputCls } from "@/components/ui";
import { useAgents, useBlueprints, useCreateGoal, useDomains, useHooks, useSquads } from "@/lib/queries";
import { bindGoalHook, getBlueprint, listBlueprints, type CreateGoalBody } from "@/lib/api";
import { HOOK_EVENTS, type ExecutionMode, type HookEvent } from "@/lib/types";
import { OUTPUT_MARKER, appendOutputTemplate } from "@/lib/task-template";

// 新建任务的表单。
// 两个入口共用：/goals/new 直接以 embedded 形态渲染（不再经过管家对话），
// 任务页的「手动新建」仍以 Dialog 形态打开。两条路径共用同一份字段与校验。
export function NewGoalForm({
  agents: agentsProp,
  squads: squadsProp,
  domains: domainsProp,
  onClose,
  onCreated,
  embedded = false,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string; default_workflow_id?: string }[];
  domains?: { id: string; name: string; acceptance_level?: string }[];
  onClose?: () => void;
  onCreated?: () => void;
  embedded?: boolean;
}) {
  const createGoal = useCreateGoal();
  // 两个入口都能独立工作：页面可以传已取好的列表（省一次渲染抖动），
  // 没传时表单自己取（react-query 按同一个 key 去重，不会多发请求）。
  const { data: agentsData } = useAgents();
  const { data: squadsData } = useSquads();
  const { data: domainsData } = useDomains();
  const agents = agentsProp ?? agentsData;
  const squads = squadsProp ?? squadsData;
  const domains = domainsProp ?? domainsData;
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [domainId, setDomainId] = useState("");
  const [domainErr, setDomainErr] = useState("");
  // 验收级别优先级：任务级 > 项目默认 > 平台默认(HITL)。
  // 默认模式「跟随项目」在提交时把项目当前级别固化成任务级值——项目之后
  // 改级别不会回溯已有任务（创建时快照），这样任务页的徽章永远可信。
  const [categoryMode, setCategoryMode] = useState<"inherit" | "hitl" | "hootl">("inherit");
  const [assigneeType, setAssigneeType] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [assigneeErr, setAssigneeErr] = useState("");
  const [workflowErr, setWorkflowErr] = useState("");
  // 工作流钉版本：'' = 由小队默认解析，解析不到自动拆解。
  const [blueprintVersionId, setBlueprintVersionId] = useState("");
  // 深链 ?blueprint=<versionId> 解析结果。链接只带版本 id，不带小队归属，
  // 所以先按版本号反查所属工作流，再预选小队；查不到时保留版本并提示。
  const [deepWorkflow, setDeepWorkflow] = useState<{
    versionId: string;
    name: string;
    squadId: string;
    // 旧全局工作流（squad_id === ""）：没有小队归属但任何小队都可钉，
    // 与「查不到归属」区分开，选小队时不要把它的版本弄丢。
    legacy?: boolean;
  } | null>(null);
  const [deepVersionParam, setDeepVersionParam] = useState("");
  // 小队有默认工作流时，勾选此项显式跳过默认、走自动拆解。
  const [skipDefault, setSkipDefault] = useState(false);
  // 复盘与高级项（§5.6）：默认跟随「是否用工作流」；用户手动点过之后不再自动改。
  const [review, setReview] = useState(false);
  const [reviewTouched, setReviewTouched] = useState(false);
  const [contentCapture, setContentCapture] = useState<"redacted" | "none" | "full">("redacted");
  const [budgetUsd, setBudgetUsd] = useState("2");
  const [sampleRatePct, setSampleRatePct] = useState("100");
  // 结束后执行（Hooks）：hookId → 事件。创建后按返回的 goal.id 逐个绑定
  // （后端 Create 不收 hooks 数组，走独立的 POST /goals/{id}/hooks 端点）。
  const { data: hookItems } = useHooks();
  const [hookSel, setHookSel] = useState<Record<string, HookEvent>>({});
  const enabledHooks = (hookItems ?? []).filter((it) => it.hook.enabled);
  const hookNameOf = (id: string) => enabledHooks.find((it) => it.hook.id === id)?.hook.name ?? id;

  // 工作流列表按小队取：只有该小队的已发布工作流才出现在下拉里。
  // 没选小队时传一个不可能命中的 id，避免顺带拉全量列表（全局工作流不能钉）。
  const scopedSquadId = assigneeType === "squad" && assigneeId ? assigneeId : "__none__";
  const { data: squadBlueprints, isPending: squadWorkflowsPending } = useBlueprints(scopedSquadId);
  const squadWorkflows = (squadBlueprints ?? []).filter((b) => b.latest_version_id);

  const selectedSquad = (squads ?? []).find((s) => s.id === assigneeId);
  const defaultBlueprintId =
    assigneeType === "squad" && selectedSquad?.default_workflow_id
      ? selectedSquad.default_workflow_id
      : "";
  const defaultBlueprint = defaultBlueprintId
    ? squadWorkflows.find((b) => b.id === defaultBlueprintId)
    : undefined;
  // 未发布 = 没有 latest_version_id，此时小队默认不生效，后端自动拆解。
  const squadDefaultVersionId =
    assigneeType === "squad" && assigneeId ? defaultBlueprint?.latest_version_id ?? "" : "";
  // 小队默认指向的工作流没出现在本小队列表里（已删除、已转给别的小队，或全部
  // 版本都没发布）。后端仍会尝试解析这个默认值并在归属不符时报错，所以这种情况
  // 要显式 skip_default_workflow，让本次真正走自动拆解。
  const defaultNotOwned =
    assigneeType === "squad" &&
    !!assigneeId &&
    !!selectedSquad?.default_workflow_id &&
    squadBlueprints !== undefined &&
    !defaultBlueprint;
  const workflowInEffect =
    !!blueprintVersionId || (!!squadDefaultVersionId && !skipDefault);

  // 深链版本不在当前小队已发布列表时，补一个选项，至少让下拉显示清楚。
  const pinnedInList = squadWorkflows.some(
    (b) => b.latest_version_id === blueprintVersionId
  );
  const deepOption =
    deepWorkflow &&
    deepWorkflow.versionId === blueprintVersionId &&
    !pinnedInList &&
    (!deepWorkflow.squadId || deepWorkflow.squadId === assigneeId)
      ? deepWorkflow
      : null;

  // 深链预选：/goals/new?project=<id>、?squad=<id>
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const squad = params.get("squad");
    if (squad) {
      setAssigneeType("squad");
      setAssigneeId(squad);
    }
  }, []);

  // 深链预选：/goals/new?blueprint=<versionId>（工作流详情页「用此版本新建任务」）。
  // 版本号 → 小队归属没有直连接口：先按 latest_version_id 反查工作流列表；
  // 老版本（已发布但不是 latest）再逐个读 blueprints/{id} 的版本列表定位。
  // 都查不到才退化成「链接指定」并提示重选。
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const v = params.get("blueprint");
    if (!v) return;
    setDeepVersionParam(v);
    setBlueprintVersionId(v);
    setReview(true);
    let cancelled = false;
    void (async () => {
      try {
        const all = await listBlueprints();
        if (cancelled) return;
        let owner = all.find((b) => b.latest_version_id === v);
        if (!owner) {
          // 老版本兜底：详情接口带 versions，命中后还能拿到工作流的小队归属。
          for (const bp of all) {
            if (cancelled) return;
            try {
              const detail = await getBlueprint(bp.id);
              if (detail.versions.some((x) => x.id === v)) {
                owner = detail.blueprint;
                break;
              }
            } catch {
              // 单个工作流读失败不影响其它候选，继续找。
            }
          }
        }
        if (owner) {
          setDeepWorkflow({
            versionId: v,
            name: owner.name,
            squadId: owner.squad_id,
            legacy: owner.squad_id === "",
          });
          if (owner.squad_id) {
            setAssigneeType("squad");
            setAssigneeId(owner.squad_id);
          }
        } else {
          setDeepWorkflow({ versionId: v, name: "链接指定的工作流", squadId: "" });
        }
      } catch {
        if (!cancelled) setDeepWorkflow({ versionId: v, name: "链接指定的工作流", squadId: "" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const project = new URLSearchParams(window.location.search).get("project");
    if (project && domains?.some((d) => d.id === project)) setDomainId(project);
  }, [domains]);

  // 复盘默认：有工作流（显式钉或小队默认在生效）就开，自动拆解就关；
  // 用户手动勾过之后尊重用户选择，不再随字段变化来回跳。
  useEffect(() => {
    if (!reviewTouched) setReview(workflowInEffect);
  }, [workflowInEffect, reviewTouched]);

  const projectLevel = (() => {
    const lvl = domains?.find((d) => d.id === domainId)?.acceptance_level;
    return lvl === "hitl" || lvl === "hootl" ? lvl : "";
  })();
  const effectiveCategory: "hitl" | "hootl" =
    categoryMode === "inherit" ? (projectLevel || "hitl") : categoryMode;
  const executionMode: ExecutionMode = review ? "train" : "infer";

  const pickAssigneeType = (v: string) => {
    setAssigneeType(v);
    setAssigneeId("");
    setBlueprintVersionId("");
    setDeepWorkflow(null);
    setSkipDefault(false);
    setAssigneeErr("");
    setWorkflowErr("");
  };

  const pickSquad = (id: string) => {
    setAssigneeId(id);
    // 旧全局工作流的深链版本可以挂到任意小队，切小队时保留它；
    // 其它情况（普通钉版本/找归属失败）切小队就清掉，避免钉错小队。
    if (!(deepWorkflow?.legacy && deepWorkflow.versionId === blueprintVersionId)) {
      setBlueprintVersionId("");
      setDeepWorkflow(null);
    }
    setSkipDefault(false);
    setAssigneeErr("");
    setWorkflowErr("");
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setDomainErr("");
    setAssigneeErr("");
    setWorkflowErr("");

    const body: CreateGoalBody = { title, category: effectiveCategory };
    if (description) body.description = description;
    if (assigneeType && assigneeId) {
      body.assignee_type = assigneeType;
      body.assignee_id = assigneeId;
      body.status = "active";
    }
    // A chosen assignee TYPE without a chosen agent/squad would silently
    // drop the assignee and the backend rejects it as "agent goal without
    // assignee_id" — fail loudly here instead.
    if (assigneeType && !assigneeId) {
      setAssigneeErr(
        assigneeType === "agent"
          ? "请选择一个智能体——或先到「智能体」页创建 runtime 和智能体"
          : "请选择一支小队——或先到「小队」页创建小队"
      );
      return;
    }
    // v2: agent/squad-executed goals must belong to a domain (DESIGN.md §2).
    if (assigneeType === "agent" || assigneeType === "squad") {
      if (!domainId) {
        setDomainErr("请选择所属项目——智能体/小队执行的任务必须挂在一个项目上（项目提供 worktree 与验收策略）");
        return;
      }
    }
    // 项目归属不只为执行：验收级别默认值也来自项目（任务 > 项目 > HITL）。
    // 未指定负责人的待办任务只要选了项目，也把 domain_id 带上，否则
    // 后端只能落到平台默认 HITL，项目级验收级别会被静默丢掉。
    if (domainId) body.domain_id = domainId;
    // 运行模式随 goal 一起落库：infer 只记基础指标，train 才建分析会话。
    body.execution_mode = executionMode;
    // 工作流钉版本：只允许钉当前小队自己的已发布工作流（深链老版本除外，
    // 服务端仍会做最终校验）。UI 不提供其它小队的选项。
    if (blueprintVersionId) {
      if (assigneeType !== "squad" || !assigneeId) {
        setWorkflowErr("工作流只能钉给所属小队：请选择该工作流所属的小队，或改用自动拆解");
        return;
      }
      const ownedBySquad = squadWorkflows.some(
        (b) => b.latest_version_id === blueprintVersionId
      );
      const viaDeepLink =
        deepWorkflow?.versionId === blueprintVersionId &&
        !deepWorkflow.legacy &&
        (!deepWorkflow.squadId || deepWorkflow.squadId === assigneeId);
      if (deepWorkflow?.legacy && deepWorkflow.versionId === blueprintVersionId) {
        setWorkflowErr(
          "这是历史工作流（没有小队归属），已冻结，不能用于新任务；请在该小队下复制一份工作流再使用"
        );
        return;
      }
      if (!ownedBySquad && !viaDeepLink) {
        setWorkflowErr("选中的工作流不属于该小队，请重新选择或改用自动拆解");
        return;
      }
      body.blueprint_version_id = blueprintVersionId;
    } else if (assigneeType === "squad" && assigneeId && (skipDefault || defaultNotOwned)) {
      // 显式跳过小队默认工作流（或默认值已失效），交给负责人自动拆解。
      body.skip_default_workflow = true;
    }
    if (executionMode === "train") {
      body.content_capture = contentCapture;
      // 数值字段做一次夹取：空输入/非法值回落到默认，不让 NaN 写进库。
      // 后端约束：<=0 回落 2、>100 报 400，这里先夹到合法区间。
      const budget = Number(budgetUsd);
      body.budget_usd = Number.isFinite(budget) && budget > 0 && budget <= 100 ? budget : 2;
      const pct = Number(sampleRatePct);
      body.sample_rate = Number.isFinite(pct) && pct > 0 && pct <= 100 ? pct / 100 : 1;
    }
    createGoal.mutate(body, {
      onSuccess: async (created) => {
        // 先把勾选的 hooks 绑上再关对话框——绑定失败不吞掉（用户会以为绑上了）。
        const entries = Object.entries(hookSel);
        const failedNames: string[] = [];
        if (entries.length) {
          const results = await Promise.allSettled(
            entries.map(([hookId, event]) => bindGoalHook(created.id, { hook_id: hookId, event }))
          );
          results.forEach((r, i) => {
            if (r.status === "rejected") failedNames.push(hookNameOf(entries[i][0]));
          });
        }
        if (failedNames.length) {
          alert(`任务已创建，但以下 hook 绑定失败：${failedNames.join("、")}——可稍后在任务详情里重新绑定`);
        }
        return onCreated ? onCreated() : onClose?.();
      },
    });
  };

  const defaultBlock =
    assigneeType === "squad" && assigneeId ? (
      <div className="rounded-md border border-line bg-surface-1 px-3 py-2.5">
        {squads === undefined ? (
          <p className="text-[11px] text-ink-3">正在读取小队信息…</p>
        ) : !selectedSquad ? (
          <p className="text-[12px] leading-relaxed text-ink-3">
            找不到该小队（可能已删除）。本次将按自动拆解执行。
          </p>
        ) : !selectedSquad.default_workflow_id ? (
          <p className="text-[12px] leading-relaxed text-ink-3">
            该小队未设默认工作流；本次将自动拆解。
            <Link
              href={`/squads/${assigneeId}`}
              className="ml-1 text-accent transition-colors hover:underline"
            >
              到小队页设置默认工作流
            </Link>
          </p>
        ) : squadWorkflowsPending ? (
          <p className="text-[11px] text-ink-3">正在读取小队工作流…</p>
        ) : defaultBlueprint && squadDefaultVersionId ? (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-ink-3">默认工作流</span>
              <span className="font-medium text-ink">{defaultBlueprint.name}</span>
              <span className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-3">
                已发布
              </span>
            </div>
            <label className="flex cursor-pointer items-start gap-2">
              <input
                type="checkbox"
                checked={skipDefault}
                onChange={(e) => {
                  const checked = e.target.checked;
                  setSkipDefault(checked);
                  setWorkflowErr("");
                  if (checked) setBlueprintVersionId("");
                }}
                className="mt-0.5 accent-accent"
              />
              <span className="min-w-0">
                <span className="block text-sm text-ink">不使用默认工作流（自动拆解）</span>
                <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">
                  不勾选：任务开始时自动套用这份默认工作流，复盘默认打开；勾选：不钉工作流，由负责人边做边拆。
                </span>
              </span>
            </label>
          </div>
        ) : (
          <p className="text-[12px] leading-relaxed text-ink-3">
            默认工作流「{defaultBlueprint?.name ?? "已删除"}」当前不可用（还没有已发布版本，或已不属于该小队）；本次将自动拆解。
            <Link
              href={`/squads/${assigneeId}`}
              className="ml-1 text-accent transition-colors hover:underline"
            >
              到小队页检查
            </Link>
          </p>
        )}
      </div>
    ) : null;

  const fields = (
    <>
      <div className="grid gap-4 md:grid-cols-2">
        <div className="md:col-span-2">
          <Field label="标题" hint="必填">
            <input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className={inputCls}
              required
              placeholder="一句话说清要做什么…"
            />
          </Field>
        </div>
        <div className="md:col-span-2">
          <Field label="描述" hint="Tab 补输出要求">
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              onKeyDown={(e) => {
                if (
                  e.key === "Tab" &&
                  !e.shiftKey &&
                  !e.metaKey &&
                  !e.ctrlKey &&
                  !e.altKey &&
                  !description.includes(OUTPUT_MARKER)
                ) {
                  e.preventDefault();
                  setDescription((prev) => appendOutputTemplate(prev));
                }
              }}
              className={inputCls}
              rows={4}
              placeholder="可选：交付内容、验收标准、约束条件…（Tab 补一句默认输出要求）"
            />
          </Field>
        </div>
        <Field label="所属项目" hint="智能体/小队执行的任务必填">
          <select
            value={domainId}
            onChange={(e) => {
              setDomainId(e.target.value);
              setDomainErr("");
            }}
            className={inputCls}
          >
            <option value="">选择…</option>
            {domains?.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </Field>
        {/* 验收级别：优先级 任务 > 项目 > 平台默认。选「跟随项目」时按项目
            当前级别在创建时固化；显式选 HITL/HOOTL 则覆盖项目默认。 */}
        <Field
          label="验收级别"
          hint={
            categoryMode === "inherit"
              ? projectLevel === "hootl"
                ? "跟随项目：HOOTL 免复检（创建时固化）"
                : projectLevel === "hitl"
                  ? "跟随项目：HITL 人工验收（创建时固化）"
                  : domainId
                    ? "项目未设置验收级别 → 按 HITL"
                    : "选择项目后继承；未选项目默认 HITL"
              : "任务级覆盖项目默认——只影响本任务"
          }
        >
          <select
            value={categoryMode}
            onChange={(e) => {
              const v = e.target.value;
              setCategoryMode(v === "inherit" ? "inherit" : v === "hootl" ? "hootl" : "hitl");
            }}
            className={inputCls}
          >
            <option value="inherit">
              跟随项目{projectLevel ? `（当前：${projectLevel === "hootl" ? "HOOTL" : "HITL"}）` : domainId ? "（项目未设置 → HITL）" : "（选项目后继承，否则 HITL）"}
            </option>
            <option value="hitl">HITL · 人工验收（任务级覆盖）</option>
            <option value="hootl">HOOTL · 免复检（任务级覆盖）</option>
          </select>
        </Field>
        <Field label="负责人类型">
          <select
            value={assigneeType}
            onChange={(e) => pickAssigneeType(e.target.value)}
            className={inputCls}
          >
            <option value="">无（先放进待办）</option>
            <option value="agent">智能体</option>
            <option value="squad">小队</option>
          </select>
        </Field>
        {assigneeType === "agent" ? (
          <Field label="选择智能体">
            <select
              value={assigneeId}
              onChange={(e) => {
                setAssigneeId(e.target.value);
                setAssigneeErr("");
              }}
              className={inputCls}
              required
            >
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>
        ) : assigneeType === "squad" ? (
          <Field label="选择小队">
            <select
              value={assigneeId}
              onChange={(e) => pickSquad(e.target.value)}
              className={inputCls}
              required
            >
              <option value="">选择…</option>
              {squads?.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <div className="hidden md:block" />
        )}
      </div>

      {/* 执行方式与复盘拆开：工作流回答“怎么干”，复盘回答“跑完要不要看”。 */}
      <div className="space-y-3 rounded-md border border-line bg-surface-2/30 p-3">
        <div>
          <div className="text-xs font-medium text-ink-2">执行方式</div>
          <p className="mt-0.5 text-[11px] leading-relaxed text-ink-3">
            工作流把步骤和依赖固定成结构；自动拆解由负责人边做边拆。工作流只能钉本小队已发布的版本。
          </p>
        </div>

        {deepVersionParam && !deepWorkflow && (
          <p className="text-[11px] text-ink-3">正在解析链接中的工作流版本…</p>
        )}
        {deepWorkflow && !deepWorkflow.squadId && deepWorkflow.versionId === blueprintVersionId && (
          <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-2 text-[11px] leading-relaxed text-amber-200">
            {deepWorkflow.legacy
              ? "链接指定的是历史工作流（没有小队归属），已冻结，不能用于新任务；请在该小队下复制一份工作流再使用。"
              : "链接指定的工作流没有小队归属（旧数据或已删除）。请先选择一支小队再提交；也可以在该小队的工作流下拉里重新选一份。"}
          </p>
        )}

        {assigneeType === "squad" ? (
          !assigneeId ? (
            <p className="text-[12px] text-ink-3">先选择小队，再决定用不用工作流。</p>
          ) : (
            <>
              <Field
                label="工作流"
                hint="只列该小队已发布的工作流；不选则按小队默认，没有默认就自动拆解。"
              >
                <select
                  value={blueprintVersionId}
                  onChange={(e) => {
                    const v = e.target.value;
                    setBlueprintVersionId(v);
                    setWorkflowErr("");
                    if (v) setSkipDefault(false);
                  }}
                  className={inputCls}
                >
                  <option value="">
                    {!assigneeId
                      ? "先选择小队…"
                      : squadDefaultVersionId && !skipDefault
                        ? `小队默认：${defaultBlueprint?.name ?? ""}`
                        : "自动拆解（不钉工作流）"}
                  </option>
                  {squadWorkflows.map((b) => (
                    <option key={b.id} value={b.latest_version_id}>
                      {b.name}
                    </option>
                  ))}
                  {deepOption && (
                    <option value={deepOption.versionId}>{deepOption.name}（链接指定）</option>
                  )}
                </select>
              </Field>
              {defaultBlock}
              {!squadWorkflowsPending && squadWorkflows.length === 0 && (
                <p className="text-[11px] text-ink-3">
                  该小队还没有已发布的工作流，本次按默认/自动拆解执行。
                </p>
              )}
            </>
          )
        ) : assigneeType === "agent" ? (
          <p className="text-[12px] leading-relaxed text-ink-3">
            单个智能体只会自动拆解：它自己决定怎么拆、拆几步，不能钉工作流。需要固定流程时，把活派给一支小队。
          </p>
        ) : (
          <p className="text-[12px] text-ink-3">先选负责人，再决定用不用工作流。</p>
        )}

        <label className="flex cursor-pointer items-start gap-2 rounded-md border border-line bg-surface-1 px-3 py-2">
          <input
            type="checkbox"
            checked={review}
            onChange={(e) => {
              setReviewTouched(true);
              setReview(e.target.checked);
            }}
            className="mt-0.5 accent-accent"
          />
          <span className="min-w-0">
            <span className="block text-sm text-ink">完成后生成复盘</span>
            <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">
              复盘读这次执行的实际图，找卡点和可改的流程；不是训练模型，也不保存对话正文。用工作流（含小队默认）默认开，自动拆解默认关。
            </span>
          </span>
        </label>
      </div>

      {review && (
        <div className="space-y-3 rounded-md border border-line bg-surface-2/40 p-3">
          <div className="text-xs font-medium text-ink-2">复盘高级项</div>
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="内容采集" hint="预留字段：当前仅保存该选项，尚未读取工具正文或参数。">
              <select
                value={contentCapture}
                onChange={(e) => setContentCapture(e.target.value as "redacted" | "none" | "full")}
                className={inputCls}
              >
                <option value="redacted">脱敏（推荐）</option>
                <option value="none">仅结构（不读正文）</option>
                <option value="full">完整（含正文）</option>
              </select>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="分析预算（美元）" hint="预留字段：当前仅保存上限">
                <input
                  type="number"
                  min={0}
                  step={0.5}
                  value={budgetUsd}
                  onChange={(e) => setBudgetUsd(e.target.value)}
                  className={inputCls}
                />
              </Field>
              <Field label="采样率（%）" hint="低于 100% 时随机跳过复盘分析">
                <input
                  type="number"
                  min={1}
                  max={100}
                  step={1}
                  value={sampleRatePct}
                  onChange={(e) => setSampleRatePct(e.target.value)}
                  className={inputCls}
                />
              </Field>
            </div>
          </div>
        </div>
      )}

      {/* 结束后执行（Hooks）：折叠区，只有存在启用的 hook 时才出现（spec v0.2 §5.3） */}
      {enabledHooks.length > 0 && (
        <details className="rounded-lg border border-line/60 bg-surface-0/40 px-3 py-2">
          <summary className="cursor-pointer select-none text-sm text-ink-2">
            结束后执行（Hooks）· 可选
            {Object.keys(hookSel).length > 0 && `（已选 ${Object.keys(hookSel).length}）`}
          </summary>
          <div className="mt-2 space-y-1.5">
            <p className="text-[11px] text-ink-3">
              勾选任务结束后自动执行的脚本；事件默认「任务完成」，可改。项目级的默认绑定会自动生效，无需在这里重复勾选。
            </p>
            {enabledHooks.map((it) => {
              const h = it.hook;
              const checked = h.id in hookSel;
              return (
                <div key={h.id} className="flex flex-wrap items-center gap-2 text-sm">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => {
                        setHookSel((prev) => {
                          const next = { ...prev };
                          if (e.target.checked) next[h.id] = "goal_finished";
                          else delete next[h.id];
                          return next;
                        });
                      }}
                    />
                    <span className="font-mono text-ink-2">{h.name}</span>
                  </label>
                  {checked && (
                    <select
                      value={hookSel[h.id]}
                      onChange={(e) =>
                        setHookSel((prev) => ({ ...prev, [h.id]: e.target.value as HookEvent }))
                      }
                      className={`${inputCls} h-7 w-auto py-0 text-xs`}
                    >
                      {HOOK_EVENTS.map((ev) => (
                        <option key={ev.value} value={ev.value}>
                          {ev.label}
                        </option>
                      ))}
                    </select>
                  )}
                  {h.description && (
                    <span className="max-w-[16rem] truncate text-[11px] text-ink-3">
                      {h.description}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        </details>
      )}

      {(createGoal.isError || domainErr || assigneeErr || workflowErr) && (
        <p className="text-sm text-red-400">
          {domainErr || assigneeErr || workflowErr || String(createGoal.error)}
        </p>
      )}
    </>
  );

  if (embedded) {
    return (
      <SectionCard title="任务信息" desc="填完直接创建，不经过对话">
        <form id="new-goal-form" onSubmit={handleSubmit} className="space-y-4 p-5">
          {fields}
          <div className="flex items-center justify-between gap-3 border-t border-line pt-4">
            <p className="text-[11px] leading-relaxed text-ink-3">
              创建后进入任务看板；负责人和工作流都可以再改。
            </p>
            <Button type="submit" disabled={createGoal.isPending}>
              {createGoal.isPending ? "创建中…" : "创建任务"}
            </Button>
          </div>
        </form>
      </SectionCard>
    );
  }

  return (
    <Dialog
      title="新建任务"
      onClose={onClose ?? (() => {})}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" form="new-goal-form" disabled={createGoal.isPending}>
            {createGoal.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="new-goal-form" onSubmit={handleSubmit} className="space-y-4">
        {fields}
      </form>
    </Dialog>
  );
}