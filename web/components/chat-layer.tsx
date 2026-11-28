"use client";

import { useEffect, useState } from "react";
import { usePathname } from "next/navigation";
import { ChatWorkbench } from "@/components/chat-workbench";
import { cn } from "@/lib/utils";

// ── 对话全局保活层 ─────────────────────────────────────────────
// 为什么这一层要存在于 layout：对话页的 ACP 会话（机器侧 chat CLI）跟随
// 页面组件的生命周期。此前 /chat 挂在 <main> 里，切到任何别的路由都会
// 卸载 ChatWorkbench → 关闭全部 WS → daemon 端 CloseChat 杀掉 CLI——
// 正在生成的回复被拦腰截断，「切走再回来对话就停了」。
//
// 现在 ChatWorkbench 只挂载一次（首次进入 /chat 时懒加载），此后离开
// /chat 只是 display:none 隐藏：WS 保持、流式输出继续进 state、
// debounced 保存照常把新消息落库；回到 /chat 立即看到最新的现场。
// 层本体 absolute 覆盖在 main 区域之上（layout 里 main 外包了一层
// relative 容器），不参与 flex 分配，Nav 也不受影响。z-40 低于 Nav(z-50)
// 和审批 Toast(z-[60])，chat 内部的权限弹窗经 portal 挂到 body 之上。
export function ChatLayer() {
  const pathname = usePathname();
  const onChat = pathname === "/chat" || pathname.startsWith("/chat/");
  const [everOpened, setEverOpened] = useState(false);
  // 用 state 而不是 ref：+1 必须触发一次重渲染，ChatWorkbench 才能收到
  // 新的 shownTick 并把滚动贴回底部。
  const [shownTick, setShownTick] = useState(0);
  useEffect(() => {
    if (onChat) {
      setEverOpened(true);
      setShownTick((t) => t + 1);
    }
  }, [onChat]);

  if (!everOpened) return null;
  return (
    <div
      aria-hidden={!onChat}
      className={cn(
        "absolute inset-0 z-40 flex flex-col gap-2.5 bg-surface-0 px-4 py-3.5 sm:px-6",
        onChat ? "flex" : "hidden"
      )}
    >
      <header className="flex shrink-0 items-center gap-3">
        <h1 className="text-2xl font-semibold tracking-tight text-ink">对话</h1>
        <p className="min-w-0 truncate text-xs text-ink-3">
          叫上多个智能体一起讨论；页面切走对话也不会断，回来自动接着看。
        </p>
      </header>
      <ChatWorkbench className="min-h-0 flex-1" shownTick={shownTick} />
    </div>
  );
}
