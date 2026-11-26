# VOD 模板、示例与自检

## 模板

标题：`<对象> + <异常> + <影响>`（评论没有标题：首行用这句话开头即可；新建 Issue 的成稿才用它当标题）

````markdown
**<一句话：什么坏了 + 影响谁>**

## 问题

- 现象：……
- 触发条件：……
- 影响：谁/什么场景；频率、耗时、失败率、数据量（有就写，没有写「待确认」）。
- 严重度：阻塞 / 高 / 中 / 低（或 P0–P3，跟用户已有口径走）。

## 背景

- 发现方式：……
- 复现步骤：
  1. ……
  2. ……
- 环境：commit `……`、`flowloop version`、OS/运行时、相关配置（脱敏）。
- 证据：
  - `path:line`：……
  - 报错/日志：
    ```text
    ……
    ```
- 已排除：……
- 待确认：……（需要谁提供什么）

## 诉求

- 期望行为：……
- 验收点：
  1. ……
- 优先级：P?，理由：……
- 建议修复方向（可选）：……
````

## 字段要求

| 段 | 必须回答 | 常见错误 |
|---|---|---|
| 问题 | 坏了什么、什么时候坏、影响多大 | 只写「有 bug」；把猜测当现象 |
| 背景 | 怎么复现、什么环境、证据在哪 | 只贴一大段日志；版本写「最新」 |
| 诉求 | 期望什么、怎么算修好、多急 | 写实现方案；优先级空着 |

## 完整示例

标题：`flowloop issue comment 在非 Issue 来源任务里报错后没有给出路`

```markdown
**flowloop issue comment 在非 Issue 来源任务里报错后没有给出路**

## 问题

- 现象：在非 Issue/PR 来源的任务里执行 `flowloop issue comment --text ...`，命令以 400 失败，错误正文只有 `goal has no issue source`；调用方不知道下一步该做什么。
- 触发条件：任务没有 `source_ref`（例如 Web「新建任务」创建），但 agent 调用了 `issue comment`。
- 影响：agent 可能反复重试，或改走平台外的手工操作；用户以为问题已记录，实际没有。
- 严重度：中（不丢数据，但会误导与空转）。

## 背景

- 发现方式：整理 VOD 技能「发不出去时」的场景，阅读 CLI 与 handler 实现。
- 复现步骤：
  1. 创建一个没有 Issue 来源的任务。
  2. 在任务里执行 `flowloop issue comment --text "test"`。
  3. 观察返回。
- 环境：commit `c7571d1`，`flowloop version` 输出 `v0.0.1-beta.1`，Linux/amd64。
- 证据：
  - `cmd/flowloop-cli/main.go:113` 的 `issueCmd` 只负责 POST `/issue-comments`，不判断来源。
  - `internal/server/handler/handler.go:1491` 直接返回 `service.NewValidationError("goal has no issue source")`。
  - 4xx 时 stderr 形如：`flowloop-cli: issue comment failed: 400 Bad Request: {"code":"AW.10000001","error":"goal has no issue source"}`。
- 已排除：不是令牌问题（同一机器换到 Issue 来源任务后可正常评论）。
- 待确认：不同 runtime 是否都会把 4xx 正文完整展示给 agent。

## 诉求

- 期望行为：错误信息给出可操作指引，例如「当前任务不是 Issue 来源，请把整理稿交给用户，或从 Issue 创建任务后重试」。
- 验收点：
  1. 该场景的 stderr 含「不是 Issue 来源」与下一步建议。
  2. Issue 来源任务的成功路径不受影响。
- 优先级：P2。理由：有明确 workaround（交稿），但会持续造成空转。
- 建议修复方向：在 CLI 或 handler 层补一句指引；VOD skill 同时保留兜底。
```

## 反例

```text
标题：关于 issue comment 的问题

问题：issue comment 有问题，老是报错，很影响使用。
背景：试了好几次都不行。
诉求：希望尽快修复。
```

为什么不行：标题无信息量；现象不可复现；环境/证据缺失；影响与优先级没有依据；维护者无法验收。改法：补触发条件、实际返回、复现步骤与期望行为；查不到的写「待确认」。

## 发布前自检

1. 首行（或新建 Issue 的标题）是否能让没参与的人知道「什么坏了 + 影响谁」？
2. 每个关键结论是否能在正文找到 `path:line`、命令输出或日志？
3. 复现步骤是否从零开始、有前置条件？
4. 影响是否有数字或明确场景？
5. 「诉求」是否可验收，而不是「优化一下」？
6. 是否只讲一个问题、只发一条评论、已脱敏？
7. 发布后是否回报了 Issue/PR 位置；没发出去是否说清原因？