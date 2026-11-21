---
name: agentwork-chat-create
title: 对话式创建
description: 在对话里创建任务、小队、智能体、项目、定时任务、团队导入或蓝图：先按缺失字段一次问清，再用 agentwork CLI 落地并回报 id。用户说「帮我建……」这类请求时触发。
---

# 对话式创建

用户在对话里要建东西时，你替他把实体落到平台。创建只走 `agentwork create <kind>`，不要直接调 HTTP 或自己拼 id。

支持的 kind：`goal`、`agent`、`squad`、`domain`、`schedule`、`team`、`blueprint`。字段速查见 [references/entities.md](references/entities.md)，蓝图格式见 [references/blueprint.md](references/blueprint.md)。

## 何时用

- 用户要新建任务、小队、智能体、项目、定时任务、团队仓库或蓝图。
- 用户补充上一次创建缺少的字段。
- 用户问刚建的东西在哪，或明确说发布刚建的蓝图。

查询、修改、删除、审批用 `list` / `status` 命令或 Web 页面。

## 三条铁律

1. **只问缺失字段，一次问全。** 先调命令拿 `need_fields`，再按 `missing` 一起问；用户补充后再调一次，不要连续重复调用；平台只补问一轮，没问全就失败。
2. **不编造 id。** agent、squad、domain、skill 先 `agentwork agent list` / `squad list` / `domain list` / `skill list` 查真实值；runtime 没有独立清单，从 `platform_hint` 选或问用户；查不到就问。
3. **创建后回报 id 与去哪验证。** 从输出 `entity.id` 取 id，按实体给路径：任务 `/goals/<id>`、小队 `/squads/<id>`、蓝本 `/blueprints/<id>`、agent `/agents`、项目 `/domains`、定时任务 `/schedules`。

## 四种常用创建

```bash
agentwork create goal --title "修复登录超时" --domain "agentwork" --assignee "主程"
agentwork create squad --name "发布小队" --leader "主程"
agentwork create agent --name "审查员" --runtime "claude-code" --skills "<skill-id>"
agentwork create blueprint --name "串行两步" --content-file serial.json --agent <agent-id>
```

`--domain` / `--assignee` / `--runtime` / `--leader` 可传 id 或名字。蓝图内容长，写文件用 `--content-file`；其余见速查。

蓝图另有两条命令：`agentwork blueprint list` 看已有蓝图，`agentwork blueprint publish "<名字|id>"` 发布它的 draft 版本；只在用户明确说发布后跑。

## 处理 need_fields

输出是 IntakeResult JSON。看到 `"status":"need_fields"`：

1. 转述 `message` 给用户。
2. 按 `missing` 一次问全，只问列出的字段，不索要可选项。
3. `platform_hint`（`<system-reminder>` 里）给你查清单用，不要贴给用户。
4. 等用户回答后再调同一条命令补字段。`goal` / `agent` / `squad` / `domain` / `blueprint` 会合并上一轮草稿（约 10 分钟有效）；`schedule` / `team` 没有补问，缺字段直接失败。

`"status":"created"` 读 `entity.id`；`"status":"replied"` 把 `message` 给用户。

## 失败处理

- 命令非零退出：读 stderr 原文。`HTTP 4xx/5xx` 先 `agentwork status` 确认 daemon 在跑，再决定是否重试。
- `status=failed` 或 message 带「失败」：把原因告诉用户，不要换一个 id 猜着重试。
- 平台判字段非法（如蓝图 assignee 不是 `agent:<id>`）：查清单换真实 id，再向用户确认。
- 重名、id 不存在、校验不通过：照报错改参数重试一次；改不了给原文。

## 不要碰的事

- 用户没明确说发布，就不发布蓝图；默认让他去 Web 蓝本页点「发布此版本」，确认后也只跑 `agentwork blueprint publish "<名字|id>"`，不碰其他版本。
- 不把蓝图绑到任务。绑定在 Web 蓝本页「用此版本新建任务」由人完成。
- 不替用户审批、取消、删除、停用；告诉他去哪，或等他明确确认。
- 不替用户选模型、运行时或角色。

## 验证清单

- flags 带齐，id 来自 `list` 或用户原话。
- `need_fields` 只问 `missing`，没有重复调用。
- 回复带实体 id 和验证路径。