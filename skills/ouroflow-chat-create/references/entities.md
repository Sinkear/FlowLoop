# 实体创建速查

所有创建都走 `ouroflow create <kind> [flags]`，输出是一个 IntakeResult JSON：

```json
{"status":"created","message":"✅ …","entity":{"id":"<32位 id>","…":"…"}}
{"status":"need_fields","message":"…","missing":["名称"],"platform_hint":"<system-reminder>…</system-reminder>"}
{"status":"replied","message":"…"}
```

- `created`：读 `entity.id`，回复用户 id 与验证页面。
- `need_fields`：转述 `message`，按 `missing` 一次问全；`platform_hint` 只给自己看。补齐后再调同一条命令：goal/agent/squad/domain/blueprint 会合并上一轮草稿（约 10 分钟有效）。**平台只补问一轮**——第二次调用若仍缺字段就直接失败，所以要一次问全；schedule/team 连草稿都没有，缺字段当场失败。
- `replied`：把 `message` 给用户（查询结果、后台任务启动、失败提示）。

查真实 id 再问或再填，不要猜：

```bash
ouroflow agent list
ouroflow squad list
ouroflow domain list
ouroflow skill list
```

`runtime` 与 skill 没有独立 CLI 清单：`need_fields` 的 `platform_hint` 里列了当前可用运行时和平台 skill，从中选，或直接问用户。

---

## goal（任务）

```bash
ouroflow create goal --title "修复登录超时" --domain "ouroflow" --assignee "主程" --category hitl
```

必填：`--title`、`--domain`；平台已有 agent 时 `--assignee` 也必填（`--assignee_type squad` 时传小队）。
可选：`--description`、`--assignee_type`（默认 agent）、`--category`（默认 hitl，`hootl` 跳过人工复核）。

```json
{"status":"created","message":"✅ 已创建任务：修复登录超时（goal 9f3c1a7b），agent 开始执行","entity":{"id":"9f3c1a7b2d4e5f6089ab7c6d5e4f3a21","title":"修复登录超时","status":"active","assignee_type":"agent"}}
```

解读：任务创建即 `active` 并排入首轮执行，没有“先确认再启动”的中间态。Web 打开 `/goals/<id>` 看执行与验收。

## agent（智能体）

```bash
ouroflow create agent --name "审查员" --runtime "claude-code" --skills "<skill-id>" --system_prompt "只做代码审查"
```

必填：`--name`、`--runtime`；平台已有技能时 `--skills` 也必填（技能 id 逗号分隔；明确不要传 `none`）。
可选：`--description`、`--system_prompt`。

```json
{"status":"created","message":"✅ 已创建 agent：审查员（a1b2c3d4）","entity":{"id":"a1b2c3d40e5f67890123456789abcdef","name":"审查员","runtime_id":"c0ffee1234567890abcdef1234567890"}}
```

解读：`entity.id` 是 agent id；后续任务 `--assignee`、小队 `--leader` 都用它。Web 打开 `/agents` 查看。

## squad（小队，对话里的「团队」）

```bash
ouroflow create squad --name "发布小队" --leader "<leader-agent-id>"
```

必填：`--name`、`--leader`（agent id 或名字）。
可选：`--description`、`--instructions`。

```json
{"status":"created","message":"✅ 已创建 squad：发布小队（3c2d4e5f）","entity":{"id":"3c2d4e5f6a7b8c9d0e1f2a3b4c5d6e7f","name":"发布小队","leader_id":"a1b2c3d40e5f67890123456789abcdef"}}
```

解读：创建只带 leader，成员要另加：`ouroflow squad add-member --squad "发布小队" --agent "<agent-id>"`，或去 Web 小队页加。Web 打开 `/squads/<id>` 看成员。

## domain（项目 / 仓库）

```bash
ouroflow create domain --name "ouroflow" --git_url "git@github.com:org/ouroflow.git"
```

必填：`--name`；默认 `--type repo`，此时 `--git_url` 必填。`--type scratch` 表示无仓库项目。
可选：`--default_branch`（默认 main）。

```json
{"status":"created","message":"✅ 已创建项目：ouroflow（d4e5f6a7）","entity":{"id":"d4e5f6a7b8c9d0e1f2a3b4c5d6e7f809","name":"ouroflow","type":"repo","git_url":"git@github.com:org/ouroflow.git"}}
```

解读：`entity.id` 给任务的 `--domain` 用。Web 打开 `/domains` 查看。

## schedule（定时任务）

```bash
ouroflow create schedule --name "每日巡检" --title "每日巡检" --cron "0 9 * * *" --domain "ouroflow" --assignee "主程"
```

必填：`--name`、`--title`（每次触发生成的任务标题）、`--cron`、`--domain`、`--assignee`（执行 agent 或小队）。
可选：`--description`、`--assignee_type`、`--category`。

```json
{"status":"created","message":"✅ 已创建定时任务：每日巡检（0 9 * * *），下次执行 11-21 09:00（本地时间）","entity":{"id":"8091a2b3c4d5e6f708192a3b4c5d6e7f","name":"每日巡检","cron_expression":"0 9 * * *","domain_id":"d4e5f6a7b8c9d0e1f2a3b4c5d6e7f809"}}
```

解读：定时任务**没有** `need_fields` 补问路径，缺字段直接回失败，所以先问全再一次调用。Web 打开 `/schedules` 查看或停用。

## team（从 git 仓库导入团队）

```bash
ouroflow create team --url "https://github.com/org/team-repo.git" --branch main
```

必填：`--url`。
可选：`--branch`（默认仓库默认分支）、`--credentials`（私有仓库）。

```json
{"status":"replied","message":"✅ 团队导入已启动（f1e2d3c4），完成后会通知你"}
```

解读：这不是建小队（建小队用 `squad`）。导入在后台跑，拿到 shortid 即可；完成后去 `/agents` 与 `/squads` 确认结果。

---

## 示例对话：建任务

> 用户：帮我建个任务，修一下登录超时。
>
> 管家：先查项目和 agent（`ouroflow domain list`、`ouroflow agent list`），发现缺执行者，只问缺的：任务挂在哪个项目、交给谁？
>
> 用户：挂在 ouroflow，交给主程。
>
> 管家：`ouroflow create goal --title "修复登录超时" --domain "ouroflow" --assignee "主程"`
>
> 平台：`{"status":"created","entity":{"id":"9f3c…"}}`
>
> 管家：已创建任务「修复登录超时」（goal 9f3c1a7b），主程开始执行。打开 `/goals/9f3c1a7b2d4e5f6089ab7c6d5e4f3a21` 看进度。

## 示例对话：建团队（小队）

> 用户：建个发布小队，主程当 leader，再把审查员拉进来。
>
> 管家：`ouroflow create squad --name "发布小队" --leader "主程"`
>
> 平台：`{"status":"created","entity":{"id":"3c2d…"}}`
>
> 管家：`ouroflow squad add-member --squad "发布小队" --agent "<审查员 id>"`
>
> 管家：已创建小队「发布小队」（squad 3c2d4e5f），主程是 leader，审查员已加入。打开 `/squads/3c2d4e5f6a7b8c9d0e1f2a3b4c5d6e7f` 看成员。

如果建小队时没给 leader，平台回 `need_fields`，`missing` 里是 leader；转述 message 并只问 leader，不要顺手把描述、指令也问一遍。