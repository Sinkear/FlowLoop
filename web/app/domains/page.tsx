import { redirect } from "next/navigation";

// 项目是任务的上下文，旧 /domains 列表页让位给 /projects（列表 + 详情）。
// 旧书签和文档里的链接继续可用。
export default function DomainsRedirect() {
  redirect("/projects");
}
