import Link from "next/link";
import { cn } from "@/lib/utils";
import { SectionCard } from "@/components/ui";

// 创建什么？——纯 CSS 的层级树。目的不是装饰，而是回答「这东西挂在哪」：
// 任务挂项目、工作流归小队、智能体是公共资源，运行环境则是机器→运行时→智能体
// 的物理归属链。每个节点直接链到对应列表/创建页（有创建页的优先给创建页）。
// 这里不引第三方图形库：四组、十几行，flex + 边框就够，且不增加包体。

type TreeNode = {
  label: string;
  href?: string;
  hint?: string;
  children?: TreeNode[];
  accent?: boolean; // 当前正在创建的这类
};

const GROUPS: TreeNode[] = [
  {
    label: "项目",
    href: "/projects?new=1",
    hint: "新建项目",
    children: [
      { label: "任务", hint: "当前页", accent: true },
      { label: "定时任务", href: "/schedules" },
      { label: "接入小队", href: "/squads" },
    ],
  },
  {
    label: "小队",
    href: "/squads",
    children: [
      { label: "成员", href: "/squads" },
      { label: "工作流", href: "/workflows" },
      { label: "复盘", href: "/training" },
    ],
  },
  {
    label: "资源（跨项目）",
    children: [
      { label: "智能体", href: "/agents" },
      { label: "技能", href: "/skills" },
      { label: "Hooks", href: "/hooks" },
    ],
  },
  {
    label: "运行环境",
    href: "/infra",
    children: [
      {
        label: "机器",
        href: "/infra",
        children: [
          {
            label: "运行时",
            href: "/infra",
            children: [{ label: "智能体", href: "/agents" }],
          },
        ],
      },
    ],
  },
];

function Row({ node, interactive = true }: { node: TreeNode; interactive?: boolean }) {
  const body = (
    <>
      <span className="flex min-w-0 items-center gap-1.5">
        {node.accent ? (
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" aria-hidden />
        ) : null}
        <span className="truncate">{node.label}</span>
      </span>
      {node.hint ? (
        <span className="shrink-0 text-[10px] text-ink-3">{node.hint}</span>
      ) : null}
    </>
  );

  return interactive && node.href ? (
    <Link
      href={node.href}
      className={cn(
        "flex items-center justify-between gap-2 rounded px-1.5 py-1 text-[12px] leading-tight transition-colors",
        node.accent
          ? "text-accent hover:bg-accent/10"
          : "text-ink-2 hover:bg-surface-2 hover:text-ink"
      )}
    >
      {body}
    </Link>
  ) : (
    <div className="flex items-center justify-between gap-2 px-1.5 py-1 text-[12px] leading-tight text-ink-2">
      {body}
    </div>
  );
}

function Tree({ nodes, group, interactive = true }: { nodes: TreeNode[]; group?: boolean; interactive?: boolean }) {
  if (nodes.length === 0) return null;
  return (
    <ul className={cn("space-y-0.5", !group && "ml-2.5 border-l border-line pl-3")}>
      {nodes.map((node) => (
        <li key={node.label} className="relative">
          {!group && (
            <span
              className="absolute -left-3 top-1/2 h-px w-2.5 bg-line"
              aria-hidden
            />
          )}
          <Row node={node} interactive={interactive} />
          {node.children ? <Tree nodes={node.children} interactive={interactive} /> : null}
        </li>
      ))}
    </ul>
  );
}

export function CreateTree({ className, interactive = true, plain = false }: { className?: string; interactive?: boolean; plain?: boolean }) {
  const body = (
      <div className="space-y-3.5">
        {GROUPS.map((group) => (
          <div key={group.label} className="space-y-0.5">
            <Row node={group} interactive={interactive} />
            {group.children ? <Tree nodes={group.children} interactive={interactive} /> : null}
          </div>
        ))}
        <p className="rounded-md border border-line bg-surface-2/60 px-2.5 py-2 text-[11px] leading-relaxed text-ink-3">
          任务挂项目，工作流归小队，智能体是公共资源。
        </p>
      </div>
  );
  if (plain) return <div className={className}>{body}</div>;
  return (
    <SectionCard
      title="创建什么？"
      desc="按归属层级找入口"
      className={className}
      bodyClassName="p-3.5"
    >
      {body}
    </SectionCard>
  );
}