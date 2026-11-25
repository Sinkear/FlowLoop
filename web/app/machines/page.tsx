import { redirect } from "next/navigation";

// 机器已并入「运行环境」页；旧地址保留为跳转。
export default function MachinesPage() {
  redirect("/infra");
}