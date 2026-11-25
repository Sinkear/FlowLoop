"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Copy, Terminal } from "lucide-react";
import { Button, Dialog } from "@/components/ui";
import { copyText } from "./shared";

const CONNECT_CMD = "flowloop connect";

// “添加机器”没有后端注册端点：机器是通过在主机上运行 connect 命令完成
// 自注册的。所以这个弹窗给的是唯一可复制的命令 + 执行步骤，不伪造表单。
export function AddMachineDialog({ onClose }: { onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (timer.current) window.clearTimeout(timer.current);
    };
  }, []);

  const copy = async () => {
    const ok = await copyText(CONNECT_CMD);
    if (timer.current) window.clearTimeout(timer.current);
    if (!ok) {
      setFailed(true);
      return;
    }
    setFailed(false);
    setCopied(true);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <Dialog
      title="添加机器"
      onClose={onClose}
      footer={
        <Button variant="outline" onClick={onClose}>
          关闭
        </Button>
      }
    >
      <p className="text-sm leading-relaxed text-ink-2">
        机器由主机侧主动注册：在目标主机上安装 flowloop CLI，然后执行下面这条命令并保持进程运行。
        连接成功后这台机器会自动出现在左侧列表，探测到的 agent CLI 会自动注册成运行时。
      </p>

      <div className="flex items-center gap-2 rounded-md border border-line bg-surface-0 px-3 py-2">
        <span className="text-accent">$</span>
        <code className="min-w-0 flex-1 truncate font-mono text-xs text-ink" title={CONNECT_CMD}>
          {CONNECT_CMD}
        </code>
        <Button
          variant="outline"
          onClick={copy}
          className="shrink-0 gap-1.5 !px-2.5 !py-1 text-xs"
          aria-label="复制连接命令"
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-emerald-300" aria-hidden="true" />
          ) : (
            <Copy className="h-3.5 w-3.5" aria-hidden="true" />
          )}
          {copied ? "已复制" : "复制"}
        </Button>
      </div>

      {failed && (
        <p className="text-xs text-amber-300">
          浏览器不允许自动复制，请手动选中命令复制。
        </p>
      )}

      <ol className="space-y-1.5 text-xs leading-relaxed text-ink-3">
        <li className="flex gap-2">
          <span className="font-mono text-ink-3">1.</span>
          在主机安装与平台一致版本的 flowloop（安装后 <code className="font-mono">flowloop</code>{" "}
          在 PATH 上）。
        </li>
        <li className="flex gap-2">
          <span className="font-mono text-ink-3">2.</span>
          在主机执行 <code className="font-mono">flowloop connect</code>，保持进程常驻；心跳丢失后机器会标记为离线。
        </li>
        <li className="flex gap-2">
          <span className="font-mono text-ink-3">3.</span>
          回到本页刷新：机器出现在左侧，点开即可添加运行时、查看模型目录。平台暂不支持在这里远程重启机器。
        </li>
      </ol>

      <p className="flex items-start gap-1.5 rounded-md border border-line bg-surface-2/50 px-3 py-2 text-[11px] leading-relaxed text-ink-3">
        <Terminal className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
        <span>
          平台不会在这里直接创建机器记录：机器身份、主机名与探测结果都由主机侧上报，手动补录会立刻和心跳对不上。
        </span>
      </p>
    </Dialog>
  );
}