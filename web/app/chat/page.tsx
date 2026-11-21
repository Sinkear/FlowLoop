"use client";

import { PageBody, PageHeader } from "@/components/ui";
import { ChatWorkbench } from "@/components/chat-workbench";

// 「对话」页 = 一个工作台：左侧本机历史、中间讨论、右侧在线智能体。
// 一个对话里可以 @ 多个智能体；每次发送把同一段共享上下文拼进每个人的 prompt，
// 所以它们是在同一条讨论线上说话，而不是各开一局。
export default function ChatPage() {
  return (
    <PageBody full>
      <PageHeader
        title="对话"
        desc="叫上多个智能体一起讨论：输入 @ 选择对象，不 @ 时由管家回应。每个智能体用自己的运行时和模型。"
      />
      <ChatWorkbench />
    </PageBody>
  );
}