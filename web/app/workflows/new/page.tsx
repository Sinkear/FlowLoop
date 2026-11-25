"use client";

// 兼容旧入口：以前“新建工作流”是一个整页 JSON 编辑器；现在统一到
// 独立的新建页（/goals/new）里选类型。这里只做一次无闪烁的地址替换。
import { useEffect } from "react";
import { useRouter } from "next/navigation";

export default function NewWorkflowPage() {
  const router = useRouter();

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const next = new URLSearchParams({ kind: "workflow" });
    const squadId = params.get("squad");
    const templateKey = params.get("template");
    if (squadId) next.set("squad", squadId);
    if (templateKey) next.set("template", templateKey);
    router.replace(`/goals/new?${next.toString()}`);
  }, [router]);

  return null;
}
