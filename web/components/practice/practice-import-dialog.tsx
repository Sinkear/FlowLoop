"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  FileJson,
  FileUp,
  Loader2,
  Package,
  Workflow,
} from "lucide-react";
import { Button, Dialog, Field, Skeleton, inputCls } from "@/components/ui";
import { useSquads } from "@/lib/queries";
import { cn } from "@/lib/utils";
import { PracticeSourceBadge } from "@/components/practice/practice-source-badge";
import {
  PRACTICE_KIND_LABEL,
  PRACTICE_SAMPLES,
  importPracticePackage,
  normalizePracticeKind,
  previewPracticeImport,
  type PracticeImportBody,
  type PracticeImportKind,
  type PracticeImportPreview,
  type PracticeImportRef,
  type PracticeImportResult,
} from "@/components/practice/practice-import";

type ParseState = { value?: Record<string, unknown>; error?: string };

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function ResultLink({ item }: { item: PracticeImportRef }) {
  return (
    <Link
      href={item.url}
      className="inline-flex items-center gap-1 rounded-md border border-line bg-surface-1 px-2 py-1 text-xs text-ink-2 transition-colors hover:text-ink"
    >
      {item.name}
    </Link>
  );
}

export function PracticeImportDialog({
  onClose,
  onImported,
  fixedKind,
}: {
  onClose: () => void;
  onImported?: () => void;
  /** 从「工作流」页签打开时固定成工作流包，避免误选其它示例。 */
  fixedKind?: PracticeImportKind;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState("");
  const [fileName, setFileName] = useState("");
  const [name, setName] = useState("");
  const [squadId, setSquadId] = useState("");
  const [gitURL, setGitURL] = useState("");
  const [credentials, setCredentials] = useState("");
  const [projectType, setProjectType] = useState<"" | "repo" | "scratch">("");
  const [preview, setPreview] = useState<PracticeImportPreview | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [result, setResult] = useState<PracticeImportResult | null>(null);
  const { data: squads } = useSquads();
  const onImportedRef = useRef(onImported);
  onImportedRef.current = onImported;

  const parsed = useMemo<ParseState>(() => {
    if (!text.trim()) return {};
    try {
      const value = JSON.parse(text) as unknown;
      if (!value || typeof value !== "object" || Array.isArray(value)) {
        return { error: "实践包必须是一个 JSON 对象。" };
      }
      return { value: value as Record<string, unknown> };
    } catch (err) {
      return { error: `JSON 解析失败：${errorText(err)}` };
    }
  }, [text]);

  const localKind = normalizePracticeKind(parsed.value?.kind) ?? fixedKind ?? null;
  const samples = PRACTICE_SAMPLES.filter((sample) => !fixedKind || sample.kind === fixedKind);

  function buildBody(): PracticeImportBody | null {
    if (!parsed.value) return null;
    const body: PracticeImportBody = { package: parsed.value };
    if (name.trim()) body.name = name.trim();
    if (squadId) body.squad_id = squadId;
    if (gitURL.trim()) body.git_url = gitURL.trim();
    if (credentials) body.git_credentials = credentials;
    if (projectType) body.project_type = projectType;
    return body;
  }

  // 首次预览与“改字段后重新预览”共用这一条防抖：不自动导入，只自动预检。
  useEffect(() => {
    if (result) return;
    if (!parsed.value) {
      setPreview(null);
      setPreviewError(parsed.error ?? null);
      setPreviewing(false);
      return;
    }
    let cancelled = false;
    setPreviewing(true);
    const timer = setTimeout(() => {
      const body: PracticeImportBody = { package: parsed.value as Record<string, unknown> };
      if (name.trim()) body.name = name.trim();
      if (squadId) body.squad_id = squadId;
      if (gitURL.trim()) body.git_url = gitURL.trim();
      if (credentials) body.git_credentials = credentials;
      if (projectType) body.project_type = projectType;
      void previewPracticeImport(body)
        .then((out) => {
          if (cancelled) return;
          setPreview(out);
          setPreviewError(null);
        })
        .catch((err: unknown) => {
          if (cancelled) return;
          setPreview(null);
          setPreviewError(errorText(err));
        })
        .finally(() => {
          if (!cancelled) setPreviewing(false);
        });
    }, 450);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [parsed.value, parsed.error, result, name, squadId, gitURL, credentials, projectType]);

  function applyPackageText(next: string) {
    setText(next);
    setResult(null);
    setImportError(null);
    try {
      const value = JSON.parse(next) as { metadata?: { name?: unknown } };
      const packageName = value?.metadata?.name;
      setName(typeof packageName === "string" ? packageName : "");
    } catch {
      // JSON 还没写完整时不用重置用户填的名称。
    }
  }

  async function handleFile(file: File | undefined) {
    if (!file) return;
    setFileName(file.name);
    applyPackageText(await file.text());
  }

  async function handleImport() {
    if (importing || previewing || !preview) return;
    const body = buildBody();
    if (!body) return;
    setImporting(true);
    setImportError(null);
    try {
      const out = await importPracticePackage(body);
      setResult(out);
      onImportedRef.current?.();
    } catch (err) {
      setImportError(errorText(err));
    } finally {
      setImporting(false);
    }
  }

  const kindLabel = preview ? PRACTICE_KIND_LABEL[preview.kind] : localKind ? PRACTICE_KIND_LABEL[localKind] : "实践包";
  const needsProjectFields = localKind === "project";
  const needsSquadField = localKind === "workflow";
  const effectiveProjectType = projectType || preview?.project_type || "repo";
  const importDisabled =
    !preview || previewing || importing || Boolean(preview?.needs_git_url) || Boolean(preview?.needs_squad);
  const failedItems = result?.items?.filter((item) => item.error) ?? [];
  const importFailed = Boolean(importError);
  const partial = failedItems.length > 0;

  return (
    <Dialog
      title={result ? "导入结果" : "导入实践包"}
      onClose={onClose}
      size="wide"
      footer={
        result ? (
          <Button onClick={onClose}>完成</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose} disabled={importing}>
              取消
            </Button>
            <Button
              onClick={() => void handleImport()}
              disabled={importDisabled}
              title={
                preview?.needs_git_url
                  ? "先填写 Git 仓库地址"
                  : preview?.needs_squad
                    ? "先选择归属小队"
                    : preview
                      ? "导入会创建副本，不会覆盖已有内容"
                      : "先粘贴或选择实践包 JSON"
              }
            >
              {importing ? (
                <span className="inline-flex items-center gap-1.5">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  导入中…
                </span>
              ) : (
                "确认导入"
              )}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <div className="space-y-4">
          <div className="flex items-start gap-2.5">
            {partial ? (
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-300" />
            ) : (
              <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />
            )}
            <div className="min-w-0">
              <p className="text-sm font-medium text-ink">
                {partial ? "已导入，但有部分内容没有创建成功" : `已导入${kindLabel}`}
              </p>
              <p className="mt-1 text-xs leading-relaxed text-ink-3">
                「{result.preview.name}」已生成属于你自己的副本，不会改动任何来源包。
              </p>
            </div>
          </div>

          <div className="space-y-2 rounded-lg border border-line bg-surface-1 p-3">
            <div className="flex flex-wrap items-center gap-2">
              <PracticeSourceBadge source={result.preview.source} />
              {result.preview.version && (
                <span className="font-mono text-[11px] text-ink-3">v{result.preview.version}</span>
              )}
              {result.preview.name_suffix && (
                <span className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-3">
                  已创建为副本
                </span>
              )}
            </div>
            {(result.project || result.squad || result.workflow) && (
              <div className="flex flex-wrap items-center gap-1.5">
                {result.project && <ResultLink item={result.project} />}
                {result.squad && <ResultLink item={result.squad} />}
                {result.workflow && <ResultLink item={result.workflow} />}
              </div>
            )}
            {!!result.agents?.length && (
              <div className="space-y-1">
                <p className="text-[11px] text-ink-3">创建的 Agent</p>
                <div className="flex flex-wrap gap-1.5">
                  {result.agents.map((agent) => (
                    <ResultLink key={agent.id} item={agent} />
                  ))}
                </div>
              </div>
            )}
            {(result.goals?.length || result.schedules?.length) && (
              <p className="text-[11px] text-ink-3">
                {result.goals?.length ? `${result.goals.length} 个目标` : "无目标"}
                {result.schedules?.length ? ` · ${result.schedules.length} 个定时任务` : ""}
              </p>
            )}
            {!result.project && !result.squad && !result.workflow && !partial && (
              <p className="text-xs text-ink-3">包内没有可创建的内容。</p>
            )}
          </div>

          {!!result.warnings?.length && (
            <ul className="space-y-1">
              {result.warnings.map((warning) => (
                <li key={warning} className="text-[11px] leading-relaxed text-ink-3">
                  · {warning}
                </li>
              ))}
            </ul>
          )}

          {partial && (
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3">
              <p className="text-xs font-medium text-amber-200">没有创建成功的内容</p>
              <ul className="mt-1.5 space-y-1">
                {failedItems.map((item, index) => (
                  <li key={`${item.kind}-${item.name}-${index}`} className="text-[11px] text-amber-200/90">
                    {item.name || item.kind}：{item.error}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="rounded-lg border border-line bg-surface-1 px-3 py-2.5">
            <p className="text-xs leading-relaxed text-ink-3">
              粘贴或选择一份实践包 JSON。导入只会创建副本，不会覆盖同名内容；本地导入一律标为
              <span className="mx-1 inline-flex align-middle">
                <PracticeSourceBadge source="unverified" />
              </span>
              ，请先检查包内的指令与脚本。
            </p>
          </div>

          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <input
                ref={fileRef}
                type="file"
                accept=".json,application/json"
                className="hidden"
                onChange={(event) => {
                  void handleFile(event.target.files?.[0]);
                  event.target.value = "";
                }}
              />
              <Button variant="outline" onClick={() => fileRef.current?.click()}>
                <FileUp className="h-3.5 w-3.5" />
                选择 JSON 文件
              </Button>
              {fileName && (
                <span className="inline-flex items-center gap-1 text-[11px] text-ink-3">
                  <FileJson className="h-3.5 w-3.5" />
                  {fileName}
                </span>
              )}
            </div>

            {!!samples.length && (
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="text-[11px] text-ink-3">或者填入示例：</span>
                {samples.map((sample) => (
                  <button
                    key={sample.kind}
                    type="button"
                    onClick={() => applyPackageText(sample.json)}
                    className="rounded-md border border-line bg-surface-1 px-2 py-0.5 text-[11px] text-ink-2 transition-colors hover:text-ink"
                  >
                    {sample.label}
                  </button>
                ))}
              </div>
            )}

            <textarea
              value={text}
              onChange={(event) => applyPackageText(event.target.value)}
              spellCheck={false}
              placeholder={'{\n  "api_version": "ouroflow/v1",\n  "kind": "squad",\n  "metadata": { "name": "我的小队包" },\n  "spec": { ... }\n}'}
              className={cn(inputCls, "min-h-[170px] resize-y font-mono text-[11px] leading-relaxed")}
            />
            {parsed.error && !parsed.value && (
              <p className="flex items-start gap-1.5 text-[11px] text-amber-300">
                <AlertCircle className="mt-0.5 h-3 w-3 shrink-0" />
                {parsed.error}
              </p>
            )}
          </div>

          {parsed.value && (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label="名称" hint="已存在同名内容时会自动创建副本（名称后加「副本」）。">
                <input
                  className={inputCls}
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={preview?.original_name || "默认使用包内名称"}
                />
              </Field>
              {needsProjectFields && (
                <Field label="项目类型" hint="scratch 不绑定仓库；repo 需要填写仓库地址。">
                  <select
                    className={inputCls}
                    value={effectiveProjectType}
                    onChange={(event) => setProjectType(event.target.value as "" | "repo" | "scratch")}
                  >
                    <option value="scratch">不绑定仓库（scratch）</option>
                    <option value="repo">绑定 Git 仓库（repo）</option>
                  </select>
                </Field>
              )}
              {needsSquadField && (
                <Field label="归属小队" hint="工作流只能把任务派给这支小队自己的成员，或用 agent:<名称> 指派具体 Agent。">
                  <select className={inputCls} value={squadId} onChange={(event) => setSquadId(event.target.value)}>
                    <option value="">选择一支小队（必选）</option>
                    {(squads ?? []).map((squad) => (
                      <option key={squad.id} value={squad.id}>
                        {squad.name}
                      </option>
                    ))}
                  </select>
                </Field>
              )}
              {needsProjectFields && effectiveProjectType === "repo" && (
                <>
                  <Field label="Git 仓库地址" hint="不会代建仓库；仓库必须已存在且可访问。">
                    <input
                      className={inputCls}
                      value={gitURL}
                      onChange={(event) => setGitURL(event.target.value)}
                      placeholder="https://gitcode.com/owner/repo.git"
                    />
                  </Field>
                  <Field label="仓库凭证" hint="可选。只用于本次连接测试与项目创建，不会回显。">
                    <input
                      type="password"
                      className={inputCls}
                      value={credentials}
                      onChange={(event) => setCredentials(event.target.value)}
                      placeholder="token 或 username:password"
                      autoComplete="off"
                    />
                  </Field>
                </>
              )}
            </div>
          )}

          {parsed.value && (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-xs font-medium text-ink-2">将创建的内容</p>
                {previewing && (
                  <span className="inline-flex items-center gap-1 text-[11px] text-ink-3">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    预检中…
                  </span>
                )}
              </div>

              {previewError && (
                <div className="flex items-start gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] leading-relaxed text-amber-200">
                  <AlertCircle className="mt-0.5 h-3 w-3 shrink-0" />
                  <span>{previewError}</span>
                </div>
              )}

              {!preview && previewing && !previewError && (
                <div className="space-y-2 rounded-lg border border-line p-3">
                  <Skeleton className="h-4 w-40" />
                  <Skeleton className="h-3 w-3/4" />
                  <Skeleton className="h-3 w-1/2" />
                </div>
              )}

              {preview && (
                <div className="space-y-3 rounded-lg border border-line bg-surface-1 p-3">
                  <div className="flex items-start gap-2.5">
                    <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3">
                      {preview.kind === "workflow" ? (
                        <Workflow className="h-4 w-4" />
                      ) : (
                        <Package className="h-4 w-4" />
                      )}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-sm font-medium text-ink" title={preview.name}>
                          {preview.name}
                        </span>
                        <PracticeSourceBadge source={preview.source} />
                        {preview.version && (
                          <span className="font-mono text-[10px] text-ink-3">v{preview.version}</span>
                        )}
                      </div>
                      <p className="mt-1 text-[11px] leading-relaxed text-ink-3">
                        {preview.description || `${PRACTICE_KIND_LABEL[preview.kind]} · 导入后生成你自己的副本`}
                      </p>
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-x-4 gap-y-1 text-[11px] sm:grid-cols-2">
                    <span className="text-ink-3">
                      类型：<span className="text-ink-2">{PRACTICE_KIND_LABEL[preview.kind]}</span>
                    </span>
                    {preview.project_type && (
                      <span className="text-ink-3">
                        项目类型：<span className="text-ink-2">{preview.project_type === "repo" ? "Git 仓库" : "不绑仓库"}</span>
                      </span>
                    )}
                    {preview.git_url && (
                      <span className="truncate text-ink-3">
                        仓库：<span className="text-ink-2">{preview.git_url}</span>
                      </span>
                    )}
                    {preview.owner_squad && (
                      <span className="text-ink-3">
                        归属小队：<span className="text-ink-2">{preview.owner_squad}</span>
                      </span>
                    )}
                    {preview.team_squad && (
                      <span className="text-ink-3">
                        随包创建小队：<span className="text-ink-2">{preview.team_squad}</span>
                      </span>
                    )}
                    {preview.kind === "workflow" && (
                      <span className="text-ink-3">
                        节点数：<span className="text-ink-2">{preview.workflow_node_count ?? 0}</span>
                      </span>
                    )}
                  </div>

                  {!!preview.agents?.length && (
                    <div className="space-y-1">
                      <p className="text-[11px] text-ink-3">Agent（{preview.agents.length}）</p>
                      <div className="flex flex-wrap gap-1">
                        {preview.agents.map((agent) => (
                          <span
                            key={agent.name}
                            className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] text-ink-2"
                            title={agent.renamed ? `原名「${agent.name}」已存在，将创建副本` : undefined}
                          >
                            {agent.final_name}
                            {agent.renamed && <span className="ml-1 text-ink-3">副本</span>}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  {!!preview.goals?.length && (
                    <p className="text-[11px] text-ink-3">
                      目标：<span className="text-ink-2">{preview.goals.join("、")}</span>
                    </p>
                  )}
                  {!!preview.schedules?.length && (
                    <p className="text-[11px] text-ink-3">
                      定时任务：<span className="text-ink-2">{preview.schedules.join("、")}</span>
                    </p>
                  )}

                  {preview.source_note && <p className="text-[11px] leading-relaxed text-ink-3">{preview.source_note}</p>}

                  {!!preview.warnings?.length && (
                    <ul className="space-y-1 border-t border-line pt-2">
                      {preview.warnings.map((warning) => (
                        <li key={warning} className="text-[11px] leading-relaxed text-amber-200/90">
                          · {warning}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          )}

          {importFailed && (
            <div className="flex items-start gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] leading-relaxed text-amber-200">
              <AlertCircle className="mt-0.5 h-3 w-3 shrink-0" />
              <span>导入失败：{importError}</span>
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}