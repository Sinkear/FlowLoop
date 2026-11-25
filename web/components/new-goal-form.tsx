"use client";

import { useEffect, useState } from "react";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { useBlueprints, useCreateGoal, useHooks } from "@/lib/queries";
import { bindGoalHook, type CreateGoalBody } from "@/lib/api";
import { HOOK_EVENTS, type ExecutionMode, type HookEvent } from "@/lib/types";
import { OUTPUT_MARKER, appendOutputTemplate } from "@/lib/task-template";
import { cn } from "@/lib/utils";

// 新建 Goal 的表单（对话框形态）。
// 单独抽成组件是因为两个入口都用它：任务页的「手动新建」，以及 /goals/new 里
// 管家没能创建成功时的兜底表单——兜底路径不能是死路。
export function NewGoalForm({
  agents,
  squads,
  domains,
  onClose,
  onCreated,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string; acceptance_level?: string }[];
  onClose: () => void;
  onCreated?: () => void;
}) {
  const createGoal = useCreateGoal();
  // 蓝本（Phase A）：钉住一个 published 版本才能选择；无蓝本时下拉只有
  // 「不绑定」一项——infer 模式语义完全不变（提案 §2 非目标护栏）。
  const { data: blueprints } = useBlueprints();
  const publishedBlueprints = (blueprints ?? []).filter((b) => b.latest_version_id);
  const [blueprintVersionId, setBlueprintVersionId] = useState("");

  // 深链预选（蓝图详情页「用此版本新建任务」按钮）：/goals/new?blueprint=<versionId>
  // 直接把版本钉进表单，省得在对话框里再找一遍。读 window.location 而不是
  // useSearchParams——避开静态预渲染页的 Suspense 边界约束。
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const v = params.get("blueprint");
    if (v) {
      setBlueprintVersionId(v);
      setReview(true);
    }
  }, []);

  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [domainId, setDomainId] = useState("");
  const [domainErr, setDomainErr] = useState("");
  useEffect(() => {
    const project = new URLSearchParams(window.location.search).get("project");
    if (project && domains?.some((d) => d.id === project)) setDomainId(project);
  }, [domains]);
  // 验收级别优先级：任务级 > 项目默认 > 平台默认(HITL)。
  // 默认模式「跟随项目」在提交时把项目当前级别固化成任务级值——项目之后
  // 改级别不会回溯已有任务（创建时快照），这样任务页的徽章永远可信。
  const [categoryMode, setCategoryMode] = useState<"inherit" | "hitl" | "hootl">("inherit");
  const projectLevel = (() => {
    const lvl = domains?.find((d) => d.id === domainId)?.acceptance_level;
    return lvl === "hitl" || lvl === "hootl" ? lvl : "";
  })();
  const effectiveCategory: "hitl" | "hootl" =
    categoryMode === "inherit" ? (projectLevel || "hitl") : categoryMode;
  const [assigneeType, setAssigneeType] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [assigneeErr, setAssigneeErr] = useState("");
  // 复盘与高级项（§5.6）：复盘默认关闭；选了蓝本后默认打开，可手动关掉。
  // 执行方式（蓝本 / 自动拆解）和复盘是两件事：可以钉蓝本但不复盘。
  const [review, setReview] = useState(false);
  const executionMode: ExecutionMode = review ? "train" : "infer";
  const [contentCapture, setContentCapture] = useState<"redacted" | "none" | "full">("redacted");
  const [budgetUsd, setBudgetUsd] = useState("2");
  const [sampleRatePct, setSampleRatePct] = useState("100");
  // 结束后执行（Hooks）：hookId → 事件。创建后按返回的 goal.id 逐个绑定
  // （后端 Create 不收 hooks 数组，走独立的 POST /goals/{id}/hooks 端点）。
  const { data: hookItems } = useHooks();
  const [hookSel, setHookSel] = useState<Record<string, HookEvent>>({});
  const enabledHooks = (hookItems ?? []).filter((it) => it.hook.enabled);
  const hookNameOf = (id: string) => enabledHooks.find((it) => it.hook.id === id)?.hook.name ?? id;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
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
          ? "请选择一个 Agent——或先到「Agent」页创建 runtime 和 agent"
          : "请选择一个 Squad——或先到「Squad」页创建小队"
      );
      return;
    }
    // v2: agent/squad-executed goals must belong to a domain (DESIGN.md §2).
    if (assigneeType === "agent" || assigneeType === "squad") {
      if (!domainId) {
        setDomainErr("请选择所属项目——agent 执行的 Goal 必须挂在一个项目上（项目提供 worktree 与验收策略）");
        return;
      }
    }
    // 项目归属不只为执行：验收级别默认值也来自项目（任务 > 项目 > HITL）。
    // 未指定负责人的待办任务只要选了项目，也把 domain_id 带上，否则
    // 后端只能落到平台默认 HITL，项目级验收级别会被静默丢掉。
    if (domainId) body.domain_id = domainId;
    // 运行模式随 goal 一起落库：infer 只记基础指标，train 才建分析会话。
    // （后端缺省也是 infer，这里仍显式传，便于抓包核对。）
    body.execution_mode = executionMode;
    // 蓝本钉版本（Phase A）：非空时 Activate 编译规范图为 gated sub_goal，
    // 子任务创建被结构拒绝。后端 Create 校验版本必须 published。
    if (blueprintVersionId) body.blueprint_version_id = blueprintVersionId;
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
        return onCreated ? onCreated() : onClose();
      },
    });
  };

  return (
    <Dialog
      title="新建任务"
      onClose={onClose}
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
        <Field label="标题" hint="必填">
          <input value={title} onChange={(e) => setTitle(e.target.value)} className={inputCls} required placeholder="一句话说清要做什么…" />
        </Field>
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
        <Field label="所属项目" hint="agent/squad 执行的任务必填">
          <select value={domainId} onChange={(e) => setDomainId(e.target.value)} className={inputCls}>
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
                ? "跟随项目：HOOTL 免复检（创建时固化，之后改项目不回溯本任务）"
                : projectLevel === "hitl"
                  ? "跟随项目：HITL 人工验收（创建时固化）"
                  : domainId
                    ? "项目未设置验收级别 → 按 HITL 人工验收"
                    : "选择项目后继承其验收级别；未选项目默认 HITL"
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
            onChange={(e) => {
              setAssigneeType(e.target.value);
              setAssigneeId("");
              if (e.target.value !== "squad") {
                setBlueprintVersionId("");
                setReview(false);
              }
            }}
            className={inputCls}
          >
            <option value="">无（先放进待办）</option>
            <option value="agent">Agent</option>
            <option value="squad">Squad</option>
          </select>
        </Field>
        {assigneeType === "agent" && (
          <Field label="选择 Agent">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {assigneeType === "squad" && (
          <Field label="选择 Squad">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {squads?.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {/* 执行方式与复盘拆开：蓝本回答“怎么干”，复盘回答“跑完要不要看”。 */}
        <div className="space-y-3 rounded-md border border-line bg-surface-2/30 p-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="text-xs font-medium text-ink-2">执行方式</div>
              <p className="mt-0.5 text-[11px] leading-relaxed text-ink-3">
                用蓝本时，平台在任务开始时把步骤编译成固定结构；自动拆解则由负责人边做边拆。
              </p>
            </div>
          </div>

          {assigneeType === "squad" ? (
            <>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <label
                  className={cn(
                    "flex cursor-pointer flex-col gap-1 rounded-md border px-3 py-2 transition-colors",
                    blueprintVersionId
                      ? "border-accent/50 bg-accent/10"
                      : "border-line bg-surface-1 hover:border-line-strong",
                    publishedBlueprints.length === 0 && "cursor-not-allowed opacity-60"
                  )}
                >
                  <span className="flex items-center gap-2 text-sm font-medium text-ink">
                    <input
                      type="radio"
                      name="execution_style"
                      checked={!!blueprintVersionId}
                      disabled={publishedBlueprints.length === 0}
                      onChange={() => {
                        const first = publishedBlueprints[0];
                        if (first?.latest_version_id) {
                          setBlueprintVersionId(first.latest_version_id);
                          setReview(true);
                        }
                      }}
                      className="accent-accent"
                    />
                    使用蓝本
                  </span>
                  <span className="pl-5 text-[11px] leading-relaxed text-ink-3">
                    步骤和依赖按蓝本固定编译，负责人只监控和收口。
                  </span>
                </label>

                <label
                  className={cn(
                    "flex cursor-pointer flex-col gap-1 rounded-md border px-3 py-2 transition-colors",
                    !blueprintVersionId
                      ? "border-accent/50 bg-accent/10"
                      : "border-line bg-surface-1 hover:border-line-strong"
                  )}
                >
                  <span className="flex items-center gap-2 text-sm font-medium text-ink">
                    <input
                      type="radio"
                      name="execution_style"
                      checked={!blueprintVersionId}
                      onChange={() => {
                        setBlueprintVersionId("");
                        setReview(false);
                      }}
                      className="accent-accent"
                    />
                    自动拆解
                  </span>
                  <span className="pl-5 text-[11px] leading-relaxed text-ink-3">
                    不预设步骤，负责人按目标自己拆给人；平台只记录实际结构。
                  </span>
                </label>
              </div>

              {blueprintVersionId && (
                <select
                  value={blueprintVersionId}
                  onChange={(e) => {
                    setBlueprintVersionId(e.target.value);
                    setReview(true);
                  }}
                  className={inputCls}
                >
                  {publishedBlueprints.map((b) => (
                    <option key={b.latest_version_id} value={b.latest_version_id}>
                      {b.name}
                    </option>
                  ))}
                </select>
              )}

              {publishedBlueprints.length === 0 ? (
                <p className="text-[11px] text-ink-3">
                  还没有已发布的蓝本，这次只能自动拆解。蓝本入口在「资源」里，迁移完成后会收进小队。
                </p>
              ) : (
                <p className="text-[11px] text-ink-3">
                  小队默认工作流要等工作流归属迁移完成；当前先手动选。选了蓝本后复盘默认打开，可以关掉。
                </p>
              )}
            </>
          ) : assigneeType === "agent" ? (
            <p className="text-[12px] text-ink-3">
              单 Agent 只会自动拆解：它自己决定怎么拆、拆几步，不能钉蓝本。需要固定流程时，把活派给一支小队。
            </p>
          ) : (
            <p className="text-[12px] text-ink-3">先选负责人，再决定用不用蓝本。</p>
          )}

          <label className="flex cursor-pointer items-start gap-2 rounded-md border border-line bg-surface-1 px-3 py-2">
            <input
              type="checkbox"
              checked={review}
              onChange={(e) => setReview(e.target.checked)}
              className="mt-0.5 accent-accent"
            />
            <span className="min-w-0">
              <span className="block text-sm text-ink">完成后生成复盘</span>
              <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">
                复盘读这次执行的实际图，找卡点和可改的流程；不是训练模型，也不保存对话正文。小队 + 蓝本默认开，自动拆解默认关。
              </span>
            </span>
          </label>
        </div>

        {review && (
          <div className="space-y-3 rounded-md border border-line bg-surface-2/40 p-3">
            <div className="text-xs font-medium text-ink-2">复盘高级项</div>
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
              <Field label="分析预算（美元）" hint="预留字段：当前仅保存上限，尚未触发预算降级">
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
                    {h.description && <span className="text-[11px] text-ink-3 truncate max-w-[16rem]">{h.description}</span>}
                  </div>
                );
              })}
            </div>
          </details>
        )}
        {(createGoal.isError || domainErr || assigneeErr) && (
          <p className="text-sm text-red-400">{domainErr || assigneeErr || String(createGoal.error)}</p>
        )}
      </form>
    </Dialog>
  );
}