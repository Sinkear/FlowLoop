"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Bot,
  FolderGit2,
  LayoutDashboard,
  LayoutTemplate,
  ListChecks,
  Menu,
  MessageSquare,
  MessagesSquare,
  Plus,
  ScrollText,
  Server,
  Settings,
  Sparkles,
  Users,
  Webhook,
  X,
} from "lucide-react";
import { useDomains, useGoals, usePendingGates } from "@/lib/queries";
import { useCreateDialog } from "@/components/create/create-dialog";
import { cn } from "@/lib/utils";

// 一级入口只回答“我平时在哪干活”：工作台 / 项目 / 小队 / 智能体 / 资源 / 运行环境 / 设置。
// 智能体是公共执行者，紧跟“小队”之后；工作流不在全局导航里（它归小队）；技能、Hooks、实践库退到“资源”下；
// IM 渠道属于运行环境的接入配置；日志压到左下角。任务板不再是首屏，而是项目、工作台和“新建任务”共同的落点。
type NavItem = {
  href: string;
  label: string;
  icon: typeof LayoutDashboard;
  note?: string;
};

const MAIN_ITEMS: NavItem[] = [
  { href: "/", label: "工作台", icon: LayoutDashboard },
  { href: "/goals", label: "任务", icon: ListChecks },
  { href: "/projects", label: "项目", icon: FolderGit2 },
  { href: "/squads", label: "小队", icon: Users },
  { href: "/agents", label: "智能体", icon: Bot },
];

const RESOURCE_ITEMS: NavItem[] = [
  { href: "/templates", label: "实践库", icon: LayoutTemplate },
  { href: "/skills", label: "技能", icon: Sparkles },
  { href: "/hooks", label: "Hooks", icon: Webhook },
];

const SYSTEM_ITEMS: NavItem[] = [
  { href: "/chat", label: "对话", icon: MessageSquare },
  { href: "/logs", label: "日志", icon: ScrollText },
  { href: "/settings", label: "设置", icon: Settings },
];



export function Nav() {
  const pathname = usePathname();
  const { data: domains } = useDomains();
  const { data: goals } = useGoals();
  const { data: pendingGates } = usePendingGates();
  const { openMenu } = useCreateDialog();
  // 「待审核」= 工作流人工门 + 待审批任务。人工门不改变 goal.status，所以
  // 只数 review 会把「流程停着等人拍板」漏掉——这正是审核延迟的来源。
  const pendingApprovalCount =
    (pendingGates?.gates.length ?? 0) +
    (goals ?? []).filter((g) => g.status === "review").length;
  const onCreatePage = pathname === "/goals/new";
  const [mobileOpen, setMobileOpen] = useState(false);

  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);



  const isActive = (item: NavItem) => {
    if (item.href === "/") return pathname === "/";
    // 工作流归小队：/workflows 与 /workflows/<id> 都应把小队列为当前区。
    if (item.href === "/squads") {
      return pathname === "/squads" || pathname.startsWith("/squads/")
        || pathname === "/workflows" || pathname.startsWith("/workflows/");
    }
    return pathname === item.href || pathname.startsWith(`${item.href}/`);
  };

  const recentProjects = (domains ?? []).slice(0, 3);

  return (
    <nav className="sticky top-0 z-50 flex h-12 w-full shrink-0 items-center border-b border-line bg-surface-1/95 backdrop-blur md:h-screen md:w-60 md:flex-col md:items-stretch md:border-b-0 md:border-r md:p-3">
      <div className="flex w-full items-center gap-2 px-3 md:hidden">
        <Link href="/" className="flex min-w-0 items-center gap-2">
          <span className="h-2 w-2 shrink-0 rounded-full bg-accent" />
          <span className="font-mono text-sm font-semibold tracking-tight text-ink">flowloop</span>
        </Link>
        <div className="ml-auto flex items-center gap-1">
          <button
            type="button"
            onClick={openMenu}
            aria-current={onCreatePage ? "page" : undefined}
            className={cn(
              "inline-flex items-center gap-1 rounded-md border px-2.5 py-1.5 text-xs font-medium",
              onCreatePage
                ? "border-accent/40 bg-accent/15 text-accent"
                : "border-line bg-surface-2/50 text-ink-2 hover:text-ink",
            )}
          >
            <Plus className="h-3.5 w-3.5" /> 新建
          </button>
          <button
            type="button"
            onClick={() => setMobileOpen((open) => !open)}
            aria-expanded={mobileOpen}
            aria-label={mobileOpen ? "关闭导航" : "打开导航"}
            className="inline-flex h-8 w-8 items-center justify-center rounded-md text-ink-2 hover:bg-surface-2"
          >
            {mobileOpen ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
          </button>
        </div>
      </div>

      {mobileOpen && (
        <div className="absolute inset-x-0 top-full z-50 max-h-[calc(100vh-3rem)] overflow-y-auto border-b border-line bg-surface-1 p-3 shadow-2xl md:hidden">
          <MobileGroup
            title="常用"
            items={[
              { href: "/", label: "工作台", badge: pendingApprovalCount },
              { href: "/goals", label: "任务" },
              { href: "/projects", label: "项目" },
              { href: "/squads", label: "小队" },
              { href: "/agents", label: "智能体" },
            ]}
            onNavigate={() => setMobileOpen(false)}
          />
          <MobileGroup
            title="资源"
            items={RESOURCE_ITEMS.map((item) => ({ href: item.href, label: item.label }))}
            onNavigate={() => setMobileOpen(false)}
          />
          <MobileGroup
            title="运行与复盘"
            items={[
              { href: "/infra", label: "运行环境" },
              { href: "/im", label: "IM 渠道" },
              { href: "/schedules", label: "定时任务" },
              { href: "/training", label: "复盘" },
            ]}
            onNavigate={() => setMobileOpen(false)}
          />
          <MobileGroup
            title="其他"
            items={SYSTEM_ITEMS.map((item) => ({ href: item.href, label: item.label }))}
            onNavigate={() => setMobileOpen(false)}
          />
        </div>
      )}

      <div className="hidden min-h-0 flex-1 flex-col md:flex">
      <div className="mb-4">
        <Link
          href="/"
          title="返回工作台"
          className="flex items-center gap-2 rounded-md px-3 pb-3 pt-2 transition-colors hover:bg-surface-2/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent"
        >
          <span className="h-2 w-2 shrink-0 rounded-full bg-accent" />
          <span className="font-mono text-sm font-semibold tracking-tight text-ink">flowloop</span>
        </Link>

        <button
          type="button"
          onClick={openMenu}
          aria-current={onCreatePage ? "page" : undefined}
          className={cn(
            "flex w-full items-center justify-between rounded-md border px-3 py-2 text-sm font-medium transition-colors",
            onCreatePage
              ? "border-accent/40 bg-accent/15 text-accent"
              : "border-line bg-surface-2/50 text-ink-2 hover:border-line hover:bg-surface-2 hover:text-ink",
          )}
        >
          <span className="flex items-center gap-2">
            <Plus className="h-4 w-4" />
            新建
          </span>
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="flex flex-col gap-0.5">
          {MAIN_ITEMS.map((item) => (
            <div key={item.href}>
              <NavLink
                item={item}
                active={isActive(item)}
                badge={item.href === "/" ? pendingApprovalCount : undefined}
              />
              {item.href === "/projects" && recentProjects.length > 0 && (
                <div className="mb-1 ml-3 mt-0.5 flex flex-col gap-0.5 border-l border-line/70 pl-2">
                  {recentProjects.map((project) => (
                    <Link
                      key={project.id}
                      href={`/projects/${project.id}`}
                      className={cn(
                        "truncate rounded px-2 py-0.5 text-[11px] transition-colors",
                        pathname === `/projects/${project.id}`
                          ? "bg-surface-2 text-ink"
                          : "text-ink-3 hover:bg-surface-2/60 hover:text-ink-2"
                      )}
                      title={project.name}
                    >
                      {project.name}
                    </Link>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>

        <div className="mb-2 mt-4">
          <div className="select-none px-3 pb-1 text-[11px] font-medium text-ink-3">资源</div>
          <div className="flex flex-col gap-0.5">
            {RESOURCE_ITEMS.map((item) => (
              <NavLink key={item.href} item={item} active={isActive(item)} />
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-0.5 border-t border-line/70 pt-2">
          <NavLink item={{ href: "/infra", label: "运行环境", icon: Server }} active={pathname.startsWith("/infra")} />
          <NavLink item={{ href: "/im", label: "IM 渠道", icon: MessagesSquare }} active={isActive({ href: "/im", label: "IM 渠道", icon: MessagesSquare })} />
        </div>
      </div>

      <div className="mt-2 shrink-0 border-t border-line px-1 pt-2">
        <div className="grid grid-cols-3 gap-1">
          {SYSTEM_ITEMS.map((item) => {
            const active = isActive(item);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                title={item.label}
                className={cn(
                  "flex flex-col items-center gap-1 rounded-md py-2 text-[11px] transition-colors duration-150",
                  active ? "bg-surface-3 text-ink" : "text-ink-3 hover:bg-surface-2 hover:text-ink-2"
                )}
              >
                <Icon className={cn("h-4 w-4", active && "text-accent")} />
                {item.label}
              </Link>
            );
          })}
        </div>
      </div>
      </div>
    </nav>
  );
}

function MobileGroup({
  title,
  items,
  onNavigate,
}: {
  title: string;
  items: { href: string; label: string; badge?: number }[];
  onNavigate: () => void;
}) {
  return (
    <div className="mb-3">
      <div className="px-1 pb-1 text-[11px] font-medium text-ink-3">{title}</div>
      <div className="grid grid-cols-2 gap-1">
        {items.map((item) => (
          <Link
            key={item.href}
            href={item.href}
            onClick={onNavigate}
            className="flex items-center justify-between gap-2 rounded-md px-3 py-2 text-sm text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
          >
            <span>{item.label}</span>
            {item.badge ? <span className="rounded-full bg-red-500 px-1.5 font-mono text-[10px] font-medium leading-4 text-white">{item.badge > 99 ? "99+" : item.badge}</span> : null}
          </Link>
        ))}
      </div>
    </div>
  );
}

function NavLink({ item, active, badge }: { item: NavItem; active: boolean; badge?: number }) {
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      className={cn(
        "group relative flex items-center gap-2.5 rounded-md px-3 py-1.5 text-sm transition-colors duration-150",
        active ? "bg-surface-3 text-ink" : "text-ink-2 hover:bg-surface-2 hover:text-ink"
      )}
    >
      {active && <span className="absolute bottom-1.5 left-0 top-1.5 w-0.5 rounded-full bg-accent" />}
      <Icon className={cn("h-4 w-4 shrink-0", active ? "text-accent" : "text-ink-3 group-hover:text-ink-2")} />
      <span className="min-w-0 flex-1 truncate">{item.label}</span>
      {badge ? (
        <span
          title={`${badge} 项待你审核`}
          className="shrink-0 rounded-full bg-red-500 px-1.5 font-mono text-[10px] font-medium leading-4 text-white"
        >
          {badge > 99 ? "99+" : badge}
        </span>
      ) : null}
      {item.note && <span className="shrink-0 rounded border border-line px-1 text-[10px] text-ink-3">{item.note}</span>}
    </Link>
  );
}