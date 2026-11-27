# Findings P5：规则盲区 —— 长人工等待与派发等待不可见

> 本文件是 P5 的问题档案：问题复述、证据（[facts.md](facts.md) §P5）、根因、影响、修复方案与修复状态，供 issue #31 修复验收与后续复盘引用。

## 1. 问题复述（附件 §6.6、§1）

附件 §6.6/§1 主张（P5 规则盲区）：

- R2 `human_wait_share=27.7%`（wait=5170s、wall≈13500s），**86 分钟人工等待**未触发任何规则（human_wait 阈值 30%）——"墙钟越长，share 被稀释，发现越少"；
- R2 `dispatch_wait_ms=13,498,787ms`（≈225 分钟）**零 finding**——dispatch_wait 无独立规则；
- serial_chain 方向问题：R1 链占墙钟 80%、R3 92% 均触发，R2 占 46%（104.6m/225.0m）未触发——附件推断"等待越严重、发现越少"（标注需源码确认）；
- 样本污染：R3 分析时刻（15:34）落在 token 故障期（15:08-15:20 ×3 failed），`retry_amplification=3` 系故障所致，非蓝本问题。

## 2. 证据（facts.md §P5 全部事实）

1. **human_wait 公式与阈值**：share=wait/(wall+wait)（`internal/service/workflow.go:592-595`）；`ruleHumanWaitShare=0.30` + `ruleHumanWaitFloorMS=600000`（`internal/service/rsi_analyze.go:51-52`，规则体 `:761-783`：share≥0.30 && wait≥10min 才出 info finding）。R2：5170/(5170+13500)=27.7%<30% → 未触发。**附件推断确认：绝对量巨大的等待被长墙钟稀释**。
2. **dispatch_wait 盲区确认**：`dispatch_wait_ms` 只有指标采集（`workflow.go:559`）+ merge_candidate 证据行（`rsi_analyze.go:485`）+ baseline 键（`:817`）——**无独立 finding 规则、无翻译器**。R2 225 分钟派发等待零产出。
3. **serial_chain 方向**：附件推断未复核（facts.md 标注），R1 80%/R3 92% 触发、R2 46% 未触发与阈值无方向冲突的观测一致，但"等待越严重、发现越少"的机制断言本轮未验证。
4. **样本污染**：baseline（`rsi_analyze.go:819-830`）取同域最近 20 份 ready 报告中位数，不区分健康/故障样本；R3 报告含故障期指标。
5. 翻译器 `translateHumanWait`（`rsi_translate.go:889-935`）与规则同双阈值——规则不改翻译器就不认。

## 3. 根因

- human_wait **只有 share 一个触发维度**：它设计为"相对占比"信号，但绝对量本身就是行动信号（86 分钟等人，无论占比），share 公式在长任务上系统性失明；
- dispatch_wait 从未建成规则：指标进了采集与 merge_candidate 证据，但自己的 finding 规则从未落地；
- 两者的共同根因：**规则库覆盖按"已见的坏味道"逐个补，没有对指标全集做覆盖率审计**。

判定：**部分成立**——human_wait 稀释与 dispatch_wait 盲区实锤；serial_chain 方向为附件推断（未复核、未修）。

## 4. 影响

- R2 这种"任务本身很慢但人工等待占比被稀释"的轮次，改进信号全部丢失；
- dispatch_wait 反映通道争用/派发节奏问题，是 wrong_assignee（P2 改派正要解决的场景）的上游证据，盲区使报告只能靠 serial_chain 一类拓扑规则兜底；
- 故障期样本进 baseline 污染中位数，后续所有 share 类对比的基线失真。

## 5. 修复方案（[execution-report.md](execution-report.md) §1.P5）

1. **human_wait 双触发**：share（≥0.30 && ≥10min）**或** 绝对值 `wait≥1h`（新常量 `ruleHumanWaitAbsoluteMS=3600000`）任一即出 finding，Evidence 标注 `trigger=share|absolute`；`translateHumanWait` 同步双触发（否则 absolute 触发的 finding 会被翻译器旧 share 单条件 skip）。
2. **新增 dispatch_wait 规则**：`dispatch_wait_ms≥5min`（`ruleDispatchWaitFloorMS=300000`）且 `dwShare≥0.15`（`ruleDispatchWaitShare=0.15`）→ info finding"派发等待占墙钟 %.0f%%"（解释强调不含依赖门控、来自并发队列/lane 争用；Suggestion 提高通道数/预派发；Evidence 含 lane_count）。**不注册翻译器**——lane 数是运行时配置不在蓝图 spec 里，无诚实的结构变换可做（注册表注释本就允许 deliberately absent），finding 进报告供人读。
3. **故障样本剔除（与 P7 联动）**：baseline 先收集样本再过滤 `goalHadFaultRuns`（见 findings-P7.md），防止故障期指标污染中位数。

## 6. 修复状态

**部分修复（本次 issue 处理，编译+定向测试通过）**：human_wait 双触发、dispatch_wait 规则、baseline 故障剔除均已落地（`rsi_analyze.go`/`rsi_translate.go`）；**serial_chain 阈值方向未动**（附件推断未复核，不建议盲改）。回归：`go test ./internal/service/ -run "Rsi|Blueprint|HumanWait|Dispatch"` ok；全量 `go test ./internal/...` 绿（skill zip 环境用例除外，见 execution-report §3）。
