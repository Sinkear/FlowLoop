import type { Metadata } from "next";
import { IBM_Plex_Sans, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import { Providers } from "@/components/providers";
import { Nav } from "@/components/nav";
import { ErrorBoundary } from "@/components/error-boundary";
import { CreateDialogProvider } from "@/components/create/create-dialog";

const plexSans = IBM_Plex_Sans({
  variable: "--font-plex-sans",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
});

const jetbrainsMono = JetBrains_Mono({
  variable: "--font-jetbrains-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "flowloop",
  description: "Task-driven multi-runtime agent management",
};

// 首屏防闪烁：在 body 渲染前读回用户主题（localStorage）。
// 默认浅色 Studio Paper；深色 Terminal Studio 为可选项。
// 注意：html 上刻意不写 data-theme——由这段脚本独占该属性，React 便不会在
// hydration 时把它“修”回默认值（否则某些客户端路由会闪回浅色）。
const THEME_BOOT = `(function(){try{var t=localStorage.getItem('flowloop-theme');if(t!=='dark'&&t!=='light')t='light';document.documentElement.dataset.theme=t;}catch(e){document.documentElement.dataset.theme='light';}})()`;

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="zh-CN"
      className={`${plexSans.variable} ${jetbrainsMono.variable} h-full antialiased`}
      suppressHydrationWarning
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOT }} />
      </head>
      {/* h-dvh 定高：app shell 的滚动发生在 <main> 内部，页面里的
          `h-full` / `min-h-0` 才有确定的父高度。此前 body 只有
          min-h-full，高度由内容撑开，`PageBody full` 的全屏工作台
          （goal 详情、chat）会退化成"整页跟着内容变长"。 */}
      <body className="flex h-dvh flex-col overflow-hidden bg-surface-0 md:flex-row">
        <Providers>
          <CreateDialogProvider>
            <Nav />
            <main className="min-h-0 flex-1 overflow-y-auto">
              <ErrorBoundary>{children}</ErrorBoundary>
            </main>
          </CreateDialogProvider>
        </Providers>
      </body>
    </html>
  );
}
