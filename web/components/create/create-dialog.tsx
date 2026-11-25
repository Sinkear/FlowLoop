"use client";

// 全局「新建」入口：只负责把上下文带到一个独立页面。
//
// 2026-11-25 用户明确要求：新建是单独页面，不要弹窗；页面里再用 chat
// 描述要建什么。这里保留 useCreateDialog() 的调用形态，避免散落在十几个
// 页面里的按钮同时改签名，但实现已经退化为“导航”。真正的创建界面在
// web/app/goals/new/page.tsx。

import { createContext, useCallback, useContext, useMemo } from "react";
import type { ReactNode } from "react";
import { useRouter } from "next/navigation";

export type CreateKind = "task" | "project" | "squad" | "agent" | "schedule" | "workflow";

export type CreateOptions = {
  squadId?: string;
  projectId?: string;
  blueprintVersionId?: string;
  templateKey?: string;
};

type CreateContextValue = {
  /** 打开空白新建页（侧栏「新建」）。 */
  openMenu: () => void;
  /** 带着上下文进入新建页（项目详情、小队工作流等入口）。 */
  open: (kind: CreateKind, options?: CreateOptions) => void;
};

const CreateContext = createContext<CreateContextValue | null>(null);

const KIND_QUERY: Record<CreateKind, string> = {
  task: "goal",
  project: "project",
  squad: "squad",
  agent: "agent",
  schedule: "schedule",
  workflow: "workflow",
};

export function useCreateDialog() {
  const context = useContext(CreateContext);
  if (!context) throw new Error("useCreateDialog must be used inside CreateDialogProvider");
  return context;
}

export function CreateDialogProvider({ children }: { children: ReactNode }) {
  const router = useRouter();

  const openMenu = useCallback(() => {
    router.push("/goals/new");
  }, [router]);

  const open = useCallback(
    (kind: CreateKind, options: CreateOptions = {}) => {
      const params = new URLSearchParams();
      params.set("kind", KIND_QUERY[kind]);
      if (options.projectId) params.set("project", options.projectId);
      if (options.squadId) params.set("squad", options.squadId);
      if (options.blueprintVersionId) params.set("blueprint", options.blueprintVersionId);
      if (options.templateKey) params.set("template", options.templateKey);
      router.push(`/goals/new?${params.toString()}`);
    },
    [router]
  );

  const value = useMemo(() => ({ openMenu, open }), [openMenu, open]);
  return <CreateContext.Provider value={value}>{children}</CreateContext.Provider>;
}