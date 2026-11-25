"use client";

import { useState } from "react";
import { useAddSquadMember, useCreateSquad } from "@/lib/queries";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import type { Squad } from "@/lib/types";

export function NewSquadForm({
  agents,
  onClose,
  onCreated,
  embedded,
}: {
  agents?: { id: string; name: string }[];
  onClose: () => void;
  embedded?: boolean;
  onCreated?: (squad: Squad) => void;
}) {
  const createSquad = useCreateSquad();
  const addMember = useAddSquadMember();
  const [name, setName] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [leaderId, setLeaderId] = useState("");
  const [reviewerId, setReviewerId] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");

  const memberAgents = (agents ?? []).filter((a) => members.includes(a.id));
  const toggleMember = (id: string) => {
    const next = members.includes(id) ? members.filter((m) => m !== id) : [...members, id];
    setMembers(next);
    if (!next.includes(leaderId)) setLeaderId("");
    if (!next.includes(reviewerId)) setReviewerId("");
  };

  const [memberFailures, setMemberFailures] = useState<string[]>([]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createSquad.mutate(
      { name, leader_id: leaderId, description, instructions },
      {
        onSuccess: (sq) => {
          const rest = members.filter((m) => m !== leaderId);
          const failures: string[] = [];
          const enqueue = (i: number) => {
            if (i >= rest.length) {
              // 全部成员入队完毕：有失败留在弹窗里展示，全成功才关。
              if (failures.length > 0) setMemberFailures(failures);
              else {
                onCreated?.(sq);
                onClose();
              }
              return;
            }
            const m = rest[i];
            const mname = agents?.find((a) => a.id === m)?.name ?? m;
            addMember.mutate(
              { squadId: sq.id, member_type: "agent", member_id: m, role: m === reviewerId ? "reviewer" : "member" },
              { onSuccess: () => enqueue(i + 1), onError: () => { failures.push(mname); enqueue(i + 1); } }
            );
          };
          enqueue(0);
        },
        // 创建失败：弹窗保持打开，错误已在表单内展示（createSquad.isError）。
        // 原来这里 onClose() 会把错误信息和已填的表单一起吞掉。
      }
    );
  };

  return (
    <Dialog
      title="新建小队"
      size="wide"
      onClose={onClose}
      inline={embedded}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="squad-form" disabled={createSquad.isPending}>
            {createSquad.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="squad-form" onSubmit={handleSubmit} className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div className="md:col-span-2">
        <Field label="名称" hint="必填">
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputCls} required placeholder="小队名称…" />
        </Field>
        </div>
        <div className="md:col-span-2">
        <Field label="成员" hint="leader = 协调者；reviewer = 审查者（进审批时平台自动拉去审查）。先选成员，再指定 leader 和 reviewer">
          <div className="border border-line rounded-lg divide-y divide-line max-h-48 overflow-y-auto">
            {(agents ?? []).map((a) => (
              <label key={a.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer hover:bg-surface-0">
                <input type="checkbox" checked={members.includes(a.id)} onChange={() => toggleMember(a.id)} />
                {a.name}
              </label>
            ))}
          </div>
        </Field>
        </div>
        <Field label="Leader" hint="必填，从成员中选">
          <select value={leaderId} onChange={(e) => setLeaderId(e.target.value)} className={inputCls} required>
            <option value="">选择…</option>
            {memberAgents.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        <Field label="审核者（reviewer）" hint="可选——小队任务进审批时，平台会自动拉审核者审查">
          <select value={reviewerId} onChange={(e) => setReviewerId(e.target.value)} className={inputCls}>
            <option value="">无</option>
            {memberAgents.filter((a) => a.id !== leaderId).map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        <Field label="描述">
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} rows={2} />
        </Field>
        <Field label="Instructions" hint="leader 运行时会注入这些 instructions">
          <textarea value={instructions} onChange={(e) => setInstructions(e.target.value)} className={inputCls} rows={3} placeholder="小队工作说明…" />
        </Field>
        {createSquad.isError && (
          <p className="text-sm text-red-400 md:col-span-2">{String(createSquad.error)}</p>
        )}
        {memberFailures.length > 0 && (
          <p className="text-sm text-amber-400 md:col-span-2">
            小队已创建，但以下成员加入失败：{memberFailures.join("、")}。可稍后在 Squad 详情页手动添加。
          </p>
        )}
      </form>
    </Dialog>
  );
}
