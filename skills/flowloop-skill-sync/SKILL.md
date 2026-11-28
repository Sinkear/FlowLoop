---
name: flowloop-skill-sync
title: Skill 同步仓库
description: 把平台 skill 与远端 git 仓库同步：安装、检查更新、拉取、提 PR。用户说「从仓库装个 skill」「检查 skill 更新」「拉取/更新 skill」「把 skill 改动提到仓库」这类请求时触发。
---

# Skill 同步仓库

平台 skill 可以绑定一个远端 git 仓库（GitHub / GitCode，公私有均可）。这个 skill 教你用 `flowloop skill` 的同步子命令完成「仓库 → 本地」与「本地 → 仓库」两个方向的流转。

平台的同步契约（重要，先记住再操作）：

- **拉取是真替换**：`pull` 会用仓库内容覆盖本地 skill 文件，本地未提交的修改会丢。
- **没有直接推送**：本地改动回仓库只走 PR（评审 + 人审），命令是 `pr`。
- **检查是轻量的**：`check` 只做一次 `ls-remote` SHA 比对，不 clone 不算 diff，适合高频轮询。
- **私有仓必须带 token**：装/绑定时 `--token` 传 git host 的 access token；没有 token 的私有仓会在拉取时报错。

## 何时用

- 用户要「从仓库装一个 skill」「把某个 repo 里的 skill 拉进平台」→ `install`。
- 用户要给已有 skill 绑定/换绑仓库 → `source set`。
- 用户问「skill 有没有更新」「远端是不是更新了」→ `check`（想看文件级差异用 `sync` 查状态）。
- 用户要「拉取更新」「更新到最新」→ `pull`。
- 用户要「把本地改动提交到仓库」「贡献回去」→ `pr`。
- 用户要解绑仓库 → `source clear`。

## 命令速查

```bash
# 从仓库安装新 skill（clone + 注册 + 绑源 + 记基线一步完成）
flowloop skill install --repo https://gitcode.com/Sinkear/huaweicloud-skills.git \
    --path skills/demo --branch master --token <token>

# 给已有 skill 绑定仓库
flowloop skill source set <skill-name> --repo <url> [--path p] [--branch b] [--token t]

# 轻量检查：远端分支头 vs 上次同步的 SHA
flowloop skill check <skill-name>
# → {"skill":"demo","state":"in_sync|remote_ahead|unknown","has_update":true,...}

# 文件级同步状态（含 diverged/local_ahead 判定与 next_step 提示）
flowloop skill sync <skill-name>
# → {"skill":"demo","state":"remote_ahead","next_step":"pull",...}

# 拉取：用仓库内容替换本地（覆盖本地未提交修改）
flowloop skill pull <skill-name>

# 把本地改动以 PR 提交到仓库（新开 flowloop/skill-* 分支）
flowloop skill pr <skill-name> [--title T] [--body B]

# 解绑仓库（本地文件保留）
flowloop skill source clear <skill-name>
```

## 状态语义

`check` 的 `state`：

- `in_sync`：远端分支头 == 上次同步 SHA，没有可拉的更新。
- `remote_ahead`：远端动了，`pull` 会带来新内容。
- `unknown`：还没同步过（没有基线），第一次 `pull` 就是安装意义的同步。

`sync` 的 `state` 多两档（文件级判定）：`local_ahead`（只有本地改了，该提 PR）、`diverged`（两边都改了）。`next_step` 字段直接给出建议动作：`pull` / `pr` / `compare` / `none`。

## 操作流

1. **装**：用户给出仓库地址（可能带子目录/分支），确认要不要 token，一次跑 `install`。装完报 skill 名字。
2. **查**：日常问更新用 `check`；`has_update:true` 才继续，避免无谓 clone。
3. **拉**：`pull` 之前向用户确认一次——真替换，本地改动会丢；用户说丢就丢（或先 `sync` 看差异再决定）。
4. **提**：本地改完 skill 后 `pr`。PR 分支 `flowloop/skill-*` 由平台自动建，输出 `pr_url` 给用户去评审合并。
5. **删重来**：`source clear` 只解绑不删文件；删 skill 走 `skill delete`。

## 失败处理

- 私有仓 403/404：token 没带或失效，让用户在 Web 上更新 token（`source set` 重传即可），不要反复重试。
- GitHub clone 超时：网络问题，报给用户换网络或稍后重试，一次足矣。
- `remote has no skill files`：`--path` 子目录下没有 skill 文件，和用户核对路径。
- PR 权限 403：token 身份对目标仓没有 push 权，向用户说明这是「无权上游」，只能走 Fork 流程（第一期先不支持，告知即可）。
- 命令非零退出：读 stderr 原文如实报，先 `flowloop status` 确认 daemon 在跑。

## 不要碰的事

- 不替用户决定覆盖本地修改：`pull` 前必须让用户确认。
- 不直接调 HTTP 端点绕过 CLI。
- 不在 `check` 说有更新时自作主张 `pull`。
- 不把 token 写进任何输出、日志或 PR 描述。

## 验证清单

- 装完/拉完报 skill 名字与结果 JSON 的关键字段。
- `pull` 前已获得用户确认。
- PR 输出了 `pr_url`。
- 私有仓全程带 token，且没有在任何输出里回显。
