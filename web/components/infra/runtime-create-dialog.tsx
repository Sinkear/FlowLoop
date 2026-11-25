"use client";

import { useState } from "react";
import { useCreateRuntime } from "@/lib/queries";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { parseEnv, splitArgs } from "./shared";
import type { Machine } from "@/lib/types";

// 手动添加运行时（POST /runtimes）。探测自动注册仍是主路径：这里只补
// 「机器在线但探测漏了 / 需要自定义启动参数」的场景。失败原因按后端契约
// 翻译（同名 409 等），不把英文原文直接丢给用户。
export function RuntimeCreateDialog({
  machine,
  onClose,
}: {
  machine: Machine;
  onClose: () => void;
}) {
  const create = useCreateRuntime();
  const [name, setName] = useState("");
  const [args, setArgs] = useState("");
  const [env, setEnv] = useState("");
  const [error, setError] = useState("");

  const submit = () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("请填写运行时名称，建议沿用「CLI@机器名」的命名。");
      return;
    }
    let parsedEnv: Record<string, string>;
    try {
      parsedEnv = parseEnv(env);
    } catch (e) {
      setError(e instanceof Error ? e.message : "环境变量格式不正确。");
      return;
    }
    setError("");
    create.mutate(
      {
        name: trimmed,
        machine_id: machine.id,
        args: splitArgs(args),
        env: parsedEnv,
        status: "active",
      },
      {
        onSuccess: onClose,
        onError: (err) => {
          const e = err as { status?: number; code?: string; message?: string };
          if (e.status === 409 || e.code === "AW.10000009") {
            setError("这台机器上已有同名运行时，换一个名称。");
          } else {
            setError(e.message ? `创建失败：${e.message}` : "创建失败，请重试。");
          }
        },
      }
    );
  };

  return (
    <Dialog
      title={`添加运行时 · ${machine.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit} disabled={create.isPending}>
            {create.isPending ? "创建中…" : "创建运行时"}
          </Button>
        </>
      }
    >
      <p className="text-xs leading-relaxed text-ink-3">
        运行时是 Agent 在这台机器上的执行席位。机器执行{" "}
        <code className="font-mono">flowloop connect</code> 时会自动注册探测到的 CLI（
        <code className="font-mono">CLI@机器名</code>）；只有自动注册没覆盖到时才需要手动添加。
      </p>

      <Field
        label="名称"
        hint="建议沿用「CLI@机器名」，例如 claude@build-box；同名运行时不能重复创建。"
      >
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className={inputCls}
          placeholder={`claude@${machine.name}`}
          autoFocus
        />
      </Field>

      <Field label="启动参数（可留空）" hint="机器拉起该 CLI 时使用的参数，空格分隔；引号内可含空格。">
        <input
          value={args}
          onChange={(e) => setArgs(e.target.value)}
          className={`${inputCls} font-mono text-xs`}
          placeholder="acp --stdio"
        />
      </Field>

      <Field label="环境变量（可留空）" hint="每行一条 KEY=VALUE。">
        <textarea
          value={env}
          onChange={(e) => setEnv(e.target.value)}
          className={`${inputCls} min-h-20 font-mono text-xs`}
          placeholder={"ANTHROPIC_API_KEY=sk-…"}
          rows={3}
        />
      </Field>

      {error && <p className="text-xs leading-relaxed text-red-400">{error}</p>}
    </Dialog>
  );
}