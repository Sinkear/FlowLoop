import type { Metadata } from "next";
import "./globals.css";

// 根 layout：唯一含 <html>/<body> 的布局（Next App Router 要求根 layout
// 必须有这两个标签）。locale 的 lang 属性无法在这里设置（根 layout 不
// 知道 locale）——默认 lang="en"，[locale]/layout 里用 NextIntlClientProvider
// 传 locale，文档 lang 在跳转到 /en 或 /zh 后由那里的 layout 语境决定。
// 实际上 [locale]/layout 不再包 html（根 layout 已包），所以 lang 保持
// "en" 默认；如需精确 lang，可在此处读 segment，但静态导出下保持简单。
export const metadata: Metadata = {
  title: {
    default: "ouroflow — AI Task Pipeline OS",
    template: "%s | ouroflow",
  },
  description:
    "A single-user control plane that turns CLI agent sessions into a durable, human-gated delivery loop: goal → execution → completion record → review → delivery.",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen flex flex-col">{children}</body>
    </html>
  );
}
