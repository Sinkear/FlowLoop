import { redirect } from "next/navigation";

// 总览页已下线：根路径直接进任务板。任务板本身就是“等我拍板的 / 在跑的 / 刚做完的”
// 唯一可信视图，再单独维护一页汇总只会和看板读数分叉。
export default function Home() {
  redirect("/goals");
}