import { redirect } from "next/navigation";

// 工作流详情已迁到 /workflows/[id]；保留 id，旧链接不丢上下文。
export default async function BlueprintDetailRedirect({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  redirect(`/workflows/${id}`);
}
