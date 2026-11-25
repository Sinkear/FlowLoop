"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { PageBody, PageHeader } from "@/components/ui";
import { CreateTree } from "@/components/create/create-tree";
import { NewGoalForm } from "@/components/new-goal-form";
import { useAgents, useDomains, useSquads } from "@/lib/queries";

// 新建任务：直接给表单，不再先挂管家对话（对话在 /chat，创建入口不该被流式
// 输出拖慢）。左侧表单、右侧「创建什么？」层级树；桌面下树随滚动固定，移动端
// 落到表单下面。深链 ?project= / ?squad= / ?blueprint= 由表单自己读取预选。
export default function NewGoalPage() {
  const router = useRouter();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();

  return (
    <PageBody>
      <PageHeader
        title="新建任务"
        desc="直接填字段创建。工作流把步骤固定成结构，自动拆解交给负责人；创建后进入任务看板。"
        action={
          <Link
            href="/goals"
            className="inline-flex items-center gap-1 text-sm text-ink-3 transition-colors hover:text-ink"
          >
            <ArrowLeft className="h-3.5 w-3.5" /> 任务看板
          </Link>
        }
      />

      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="min-w-0">
          <NewGoalForm
            embedded
            agents={agents}
            squads={squads}
            domains={domains}
            onCreated={() => router.push("/goals")}
          />
        </div>
        <aside className="lg:sticky lg:top-6">
          <CreateTree />
        </aside>
      </div>
    </PageBody>
  );
}