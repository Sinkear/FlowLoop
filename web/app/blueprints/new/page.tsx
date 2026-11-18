"use client";

// 新建蓝图页：模板起步 → 选执行者 → 改 JSON → 保存（V1–V6 逐条校验）。
// 这是 Phase A「不用 curl 也能走完 草稿→发布→绑定任务」的第一环。
// 占位符约定见 web/lib/blueprint-templates.ts 头注释：__AGENT_ID__ 在提交时
// 被选中的智能体 id 替换；{{goal.*}}/{{node.*.output}} 留给平台编译期解析。

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowRight, Check, Loader2, PlusCircle, Save } from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, Field, inputCls } from "@/components/ui";
import { useAgents, useCreateBlueprint } from "@/lib/queries";
import { BLUEPRINT_TEMPLATES } from "@/lib/blueprint-templates";
import { cn } from "@/lib/utils";

export default function NewBlueprintPage() {
  const router = useRouter();
  const { data: agents, isLoading: agentsLoading } = useAgents();
  const createBp = useCreateBlueprint();

  // 两个入口两种起点（m01408 对齐）：
  //   「新建蓝图」→ /blueprints/new        → 空白起步：模板卡片待选、JSON 为空；
  //   「从模板新建第一份」→ ?template=serial → 深链直接选中该模板并填进编辑器。
  const [tplKey, setTplKey] = useState<string>("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [content, setContent] = useState("");
  const [agentId, setAgentId] = useState<string>("");
  const [touchedJson, setTouchedJson] = useState(false);

  // 深链：?template=<key> 选中对应模板（列表空态 CTA / 未来模板画廊都用它）。
  useEffect(() => {
    const key = new URLSearchParams(window.location.search).get("template") ?? "";
    if (BLUEPRINT_TEMPLATES.some((x) => x.key === key)) pickTemplate(key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 有智能体列表后默认选第一个（大多数场景只有一个执行者）。
  useEffect(() => {
    if (!agentId && agents && agents.length > 0) setAgentId(agents[0].id);
  }, [agents, agentId]);

  const emptyContent = !content.trim();

  const jsonParseError = useMemo<string | null>(() => {
    if (emptyContent) return null; // 空内容是「还没开始」，提示走字段 hint，不算格式错误
    try {
      JSON.parse(content);
      return null;
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  }, [content, emptyContent]);

  const needsAgent = content.includes("__AGENT_ID__");
  const placeholderCount = (content.match(/__AGENT_ID__/g) ?? []).length;

  function pickTemplate(key: string) {
    const t = BLUEPRINT_TEMPLATES.find((x) => x.key === key);
    if (!t) return;
    setTplKey(key);
    // 模板一换，名字/描述跟着换（JSON 手改过后就别覆盖了——以表单字段为准）。
    if (!touchedJson) {
      setName(t.name);
      setDescription(t.description);
    }
    setContent(t.content);
  }

  async function submit() {
    if (emptyContent || jsonParseError || (needsAgent && !agentId) || !name.trim()) return;
    let parsed = content;
    try {
      const obj = JSON.parse(content) as { name?: string; description?: string };
      // 表单字段是唯一真相：同步进 spec JSON，避免两处名字不一致。
      obj.name = name.trim();
      obj.description = description.trim();
      parsed = JSON.stringify(obj, null, 2);
    } catch {
      return; // jsonParseError 已经拦了
    }
    if (agentId) parsed = parsed.split("__AGENT_ID__").join(agentId);
    try {
      const bp = await createBp.mutateAsync({ name: name.trim(), description: description.trim(), content: parsed });
      router.push(`/blueprints/${bp.id}`);
    } catch {
      // 服务端校验错误（ApiError.message 含逐条原因）在下方红框展示，
      // mutateAsync 抛出即被 catch——state.error 从 createBp.error 读。
    }
  }

  const serverError = createBp.error instanceof Error ? createBp.error.message : null;

  return (
    <PageBody>
      <PageHeader
        title="新建蓝图"
        desc="蓝图是一份结构化的执行计划：节点、依赖、分支都写成数据，由平台的代码保证执行——不靠提示词。保存后是草稿，发布并绑定到任务后生效。"
        action={
          <Link href="/blueprints" className="text-sm text-ink-3 transition-colors hover:text-ink">
            回到蓝本列表 <ArrowRight className="ml-0.5 inline h-3.5 w-3.5" />
          </Link>
        }
      />

      <SectionCard
        title="1 · 从模板起步"
        desc="三份合法预设，点一份进编辑器再改——不用从零手写 JSON；也可以跳过模板，直接在下面贴现成的规范 JSON。"
      >
        <div className="grid gap-3 p-4 sm:grid-cols-3">
          {BLUEPRINT_TEMPLATES.map((t) => (
            <button
              key={t.key}
              type="button"
              onClick={() => pickTemplate(t.key)}
              className={cn(
                "rounded-lg border p-3 text-left transition-colors",
                tplKey === t.key
                  ? "border-accent bg-accent/10"
                  : "border-line bg-surface-0 hover:border-ink-3/40 hover:bg-surface-2"
              )}
            >
              <p className="flex items-center gap-1.5 text-sm font-medium text-ink">
                {tplKey === t.key && <Check className="h-3.5 w-3.5 text-accent" />}
                {t.title}
              </p>
              <p className="mt-1 text-[11px] leading-relaxed text-ink-3">{t.desc}</p>
            </button>
          ))}
        </div>
      </SectionCard>

      <SectionCard
        title="2 · 蓝图内容"
        desc="执行者下拉会替换 JSON 里的 __AGENT_ID__ 占位符；{{goal.title}}、{{node.<id>.output}} 这类模板留给平台在编译/释放时解析。"
      >
        <div className="space-y-4 p-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="名称" hint="列表页显示用">
              <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="执行者" hint={needsAgent ? `将替换 ${placeholderCount} 处 __AGENT_ID__ 占位符` : "此模板没有占位符"}>
              <select className={inputCls} value={agentId} onChange={(e) => setAgentId(e.target.value)}>
                {agentsLoading && <option value="">加载中…</option>}
                {(agents ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}（{a.id.slice(0, 8)}…）
                  </option>
                ))}
                {!agentsLoading && (agents ?? []).length === 0 && <option value="">（没有可用智能体）</option>}
              </select>
            </Field>
          </div>
          <Field label="描述" hint="一句话说明这份蓝图适合什么场景">
            <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <Field
            label="规范 JSON"
            hint={
              emptyContent
                ? "内容为空：先从上面选一份模板，或把现成的规范 JSON 贴进来"
                : jsonParseError
                  ? "JSON 格式有误，修正后才能保存"
                  : "保存时服务端会跑 V1–V6 校验（节点/端口/可达性/引用），不过会在下面逐条告诉你"
            }
          >
            <textarea
              className={cn(inputCls, "min-h-[360px] resize-y font-mono text-[11.5px] leading-relaxed")}
              spellCheck={false}
              value={content}
              onChange={(e) => {
                setContent(e.target.value);
                setTouchedJson(true);
              }}
            />
          </Field>

          {jsonParseError && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 font-mono text-xs text-amber-300">
              JSON 解析错误：{jsonParseError}
            </p>
          )}
          {needsAgent && !agentId && !agentsLoading && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
              JSON 里有 {placeholderCount} 处 __AGENT_ID__ 占位符，但平台还没有智能体可选。先到「智能体」页建一个再回来。
            </p>
          )}
          {serverError && (
            <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
              <p className="mb-1 font-medium">服务端校验没过：</p>
              <pre className="whitespace-pre-wrap font-mono">{serverError}</pre>
            </div>
          )}

          <div className="flex items-center gap-2">
            <Button
              onClick={() => void submit()}
              disabled={emptyContent || !!jsonParseError || !name.trim() || createBp.isPending || (needsAgent && !agentId)}
              className="gap-1.5"
            >
              {createBp.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Save className="h-3.5 w-3.5" />
              )}
              保存为草稿
            </Button>
            <span className="text-[11px] text-ink-3">
              保存后是草稿：可以反复改，点「发布」才会变成可绑定任务的正式版本。
            </span>
          </div>
        </div>
      </SectionCard>

      <SectionCard title="3 · 保存之后" desc="草稿 → 发布 → 绑定任务，三步走完就能看编译效果">
        <ul className="space-y-2.5 p-4 text-xs leading-relaxed text-ink-2">
          <li className="flex gap-2">
            <PlusCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
            保存后进详情页，点「发布此版本」——发布过的版本不可改，改动走「开新草稿」。
          </li>
          <li className="flex gap-2">
            <PlusCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
            发布后到「新建任务」页绑定这份蓝图；任务激活时蓝图被编译成一串受依赖门控的子任务。
          </li>
          <li className="flex gap-2">
            <PlusCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
            任务详情页能对照看：teal 的「蓝本（计划）」图 vs 实际「编排」图。
          </li>
        </ul>
      </SectionCard>
    </PageBody>
  );
}
