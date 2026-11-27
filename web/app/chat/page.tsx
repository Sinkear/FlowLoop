// 「对话」页本体已上移为全局保活层（components/chat-layer.tsx，挂在 layout）：
// ChatWorkbench 常驻、切走只隐藏不卸载——否则路由切换会关闭 WS、daemon 端
// 随即杀掉机器侧 chat CLI，正在生成的回复就被截断了。这里保留一个可路由的
// 空页：进入 /chat 时层自然显示在 main 区域之上，什么都不用渲染。
export default function ChatPage() {
  return null;
}
