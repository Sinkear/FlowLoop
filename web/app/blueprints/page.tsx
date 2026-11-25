import { redirect } from "next/navigation";

// Phase B：工作流的家是小队详情 → 工作流 tab；跨队浏览走 /workflows。
// 旧地址保留为跳转，旧书签和文档里的链接继续可用。
export default function BlueprintsRedirect() {
  redirect("/workflows");
}
