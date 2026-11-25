import { redirect } from "next/navigation";

// 运行时已并入「运行环境」页（机器 → 运行时 → Agent 的归属在同一页）。
// 旧地址保留为跳转，避免收藏夹和文档里的链接失效。
export default function RuntimesPage() {
  redirect("/infra");
}