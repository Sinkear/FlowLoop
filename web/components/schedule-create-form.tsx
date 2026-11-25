"use client";

import { useState } from "react";
import { useCreateSchedule, useAgents, useSquads, useDomains } from "@/lib/queries";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import type { Schedule } from "@/lib/types";

type FormState = {
  name: string;
  title_template: string;
  description: string;
  assignee_type: string;
  assignee_id: string;
  domain_id: string;
  cron_expression: string;
  timezone: string;
  category: string;
};

const INITIAL: FormState = {
  name: "",
  title_template: "",
  description: "",
  assignee_type: "agent",
  assignee_id: "",
  domain_id: "",
  cron_expression: "",
  timezone: "UTC",
  category: "hitl",
};

export function ScheduleCreateForm({ onClose, onCreated, embedded, initialProjectId }: { onClose: () => void; onCreated?: (schedule: Schedule) => void; embedded?: boolean; initialProjectId?: string }) {
  const create = useCreateSchedule();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();
  const [form, setForm] = useState<FormState>({ ...INITIAL, domain_id: initialProjectId ?? "" });

  const set = (patch: Partial<FormState>) => setForm((current) => ({ ...current, ...patch }));
  const error = create.error instanceof Error ? create.error.message : null;

  return (
    <Dialog
      title="新建定时任务"
      size="wide"
      onClose={onClose}
      inline={embedded}
      footer={
        <>
          <span className="mr-auto text-[11px] leading-relaxed text-ink-3">
            每次触发都会按模板生成一个任务；负责人和项目决定任务在哪里跑。
          </span>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="schedule-create-form" disabled={create.isPending}>
            {create.isPending ? "创建中…" : "创建定时任务"}
          </Button>
        </>
      }
    >
      <form
        id="schedule-create-form"
        className="grid grid-cols-1 gap-4 md:grid-cols-2"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate(form, { onSuccess: (created) => { onCreated?.(created); onClose(); } });
        }}
      >
        <div className="md:col-span-2">
          <Field label="名称" hint="必填，在列表里显示">
            <input
              value={form.name}
              onChange={(event) => set({ name: event.target.value })}
              className={inputCls}
              placeholder="例如：每日站会"
              required
            />
          </Field>
        </div>

        <Field label="标题模板" hint="每次触发用这个模板生成任务标题，可用 {{date}}">
          <input
            value={form.title_template}
            onChange={(event) => set({ title_template: event.target.value })}
            className={inputCls}
            placeholder="每日站会 {{date}}"
            required
          />
        </Field>

        <Field label="所属项目" hint="必填，决定仓库、worktree 和验收策略">
          <select
            value={form.domain_id}
            onChange={(event) => set({ domain_id: event.target.value })}
            className={inputCls}
            required
          >
            <option value="">选择项目…</option>
            {(domains ?? []).map((domain) => (
              <option key={domain.id} value={domain.id}>{domain.name}</option>
            ))}
          </select>
        </Field>

        <Field label="负责人类型">
          <select
            value={form.assignee_type}
            onChange={(event) => set({ assignee_type: event.target.value, assignee_id: "" })}
            className={inputCls}
          >
            <option value="agent">智能体</option>
            <option value="squad">小队</option>
          </select>
        </Field>

        <Field label={form.assignee_type === "squad" ? "选择小队" : "选择智能体"} hint="必填">
          <select
            value={form.assignee_id}
            onChange={(event) => set({ assignee_id: event.target.value })}
            className={inputCls}
            required
          >
            <option value="">选择…</option>
            {(form.assignee_type === "squad" ? squads ?? [] : agents ?? []).map((item) => (
              <option key={item.id} value={item.id}>{item.name}</option>
            ))}
          </select>
        </Field>

        <Field label="Cron 表达式" hint="5 字段：分 时 日 月 星期">
          <input
            value={form.cron_expression}
            onChange={(event) => set({ cron_expression: event.target.value })}
            className={`${inputCls} font-mono`}
            placeholder="0 9 * * 1-5"
            required
          />
        </Field>

        <Field label="时区">
          <input
            value={form.timezone}
            onChange={(event) => set({ timezone: event.target.value })}
            className={inputCls}
            placeholder="UTC"
          />
        </Field>

        <Field label="验收分类">
          <select
            value={form.category}
            onChange={(event) => set({ category: event.target.value })}
            className={inputCls}
          >
            <option value="hitl">HITL · 人工验收</option>
            <option value="hootl">HOOTL · 免复检</option>
          </select>
        </Field>

        <div className="md:col-span-2">
          <Field label="描述" hint="可选">
            <textarea
              value={form.description}
              onChange={(event) => set({ description: event.target.value })}
              className={inputCls}
              rows={2}
            />
          </Field>
        </div>

        {error && <p className="text-sm text-red-400 md:col-span-2">{error}</p>}
      </form>
    </Dialog>
  );
}
