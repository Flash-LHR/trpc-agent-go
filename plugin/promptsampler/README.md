# promptsampler

`promptsampler` 是一个 trpc-agent-go 采样插件，用于采集一次 Runner 任务执行的完整轨迹（Trace），并通过 tRPC 调用 `log_collector` 服务统一上报。

**Module 路径**: `trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler`

---

## 功能特性

- 🎯 **一次任务一次上报** —— root Agent `AfterAgent` 触发时，输出聚合了所有 sub-agent / model / tool 步骤的 Trace
- 📊 **采样率控制** —— 支持 `[0, 1]` 可配置采样率
- 🔥 **运行时热更新** —— `Enabled` / `SampleRate` / `Token` 可通过 `SetConfig` 原子更新
- 🚀 **开箱即用** —— `WithTRPCWriter()` 零参数即可上报，caller 自动从 `trpc.GlobalConfig()` 读取
- 🛡️ **失败不影响 Runner** —— 上报错误以 `log.Errorf` 输出，永不影响业务主流程
- ⚡ **异步可选** —— `WithAsyncWrite(100)` 开启后台上报，hot path 不阻塞

---

## 快速开始

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/plugin"
    "trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

sampler := promptsampler.New(
    promptsampler.WithSampleRate(1.0),
    promptsampler.WithTRPCWriter(),     // caller 自动读 trpc.GlobalConfig()
    promptsampler.WithAsyncWrite(100),  // 建议生产环境开启
)

mgr, err := plugin.NewManager(sampler)
if err != nil { log.Fatal(err) }

r := runner.NewRunner(myAgent, runner.WithPluginManager(mgr))
```

业务 main 里通常只需要这几行，`trpc.NewServer()` 初始化完成后 `caller` 字段会自动填上 `trpc.GlobalConfig().Server.Service[0].Name`。

---

## Option 参考

| Option | 作用 | 默认 |
|--------|------|------|
| `WithName(string)` | 自定义插件名 | `"promptsampler"` |
| `WithEnabled(bool)` | 主开关 | `true` |
| `WithSampleRate(float64)` | 采样率 `[0,1]` | `0.0`（关闭） |
| `WithToken(string)` | 初始业务隔离 token | `""` |
| `WithWriter(TraceWriter)` | 自定义 writer | `LogWriter` |
| `WithLogWriter()` | 用日志输出 trace | — |
| `WithPrettyLogWriter()` | 缩进 JSON 日志 | — |
| `WithTRPCWriter(opts...)` | tRPC 上报 log_collector | — |
| `WithMaxSteps(int)` | Step 上限 | `1000` |
| `WithAsyncWrite(int)` | 异步队列长度 | `0`（同步） |
| `WithStructureID(string)` | 默认 structure ID | agent name |

### TRPCWriter 子选项

| Option | 作用 | 默认 |
|--------|------|------|
| `WithTRPCCaller(string)` | 显式覆盖 caller | 自动读 `trpc.GlobalConfig()` |
| `WithTRPCTarget(string)` | 目标服务名 | `"trpc.trs.prompt_log_collector.LogCollector"` |
| `WithTRPCTimeout(Duration)` | 单次调用超时 | `3s` |
| `WithTRPCClient(proxy)` | 注入 mock / 自定义 proxy | 默认 tRPC client |

---

## 控制面接口

控制面 HTTP API（`GET/PUT /promptiter/v1/apps/{app}/plugins/trace_reporter/config`）由 `trpc.group/trpc-go/trpc-agent-go/server/promptiter` 提供；本插件只对外暴露 `GetConfig` / `SetConfig` 两个同进程接口：

```go
// 读取
cfg := sampler.GetConfig()
fmt.Printf("enabled=%v sample_rate=%v token=%s\n",
    cfg.Enabled, cfg.SampleRate, cfg.Token)

// 更新（非法值将返回 error）
err := sampler.SetConfig(&promptsampler.RuntimeConfig{
    Enabled:    true,
    SampleRate: 0.5,
    Token:      "biz-a",
})
```

`SetConfig` 成功后，下一次 Trace 上报时 `ReportTraceRequest.Token` 即为新值。

---

## Trace 数据结构

```go
type Trace struct {
    StructureID  string        // 静态结构 ID
    InvocationID string        // root invocation ID
    AgentName    string
    Status       TraceStatus   // completed/incomplete/failed
    FinalOutput  *TraceOutput
    Steps        []TraceStep
    StartTime    time.Time
    EndTime      time.Time
    Duration     time.Duration
    Error        string
}

type TraceStep struct {
    StepID             string    // s<shortInvID>_<seq>
    NodeID             string    // agent 名或 tool 名
    StepType           StepType  // model / tool / agent
    NodeKind           NodeKind  // coordinator / member / tool
    PredecessorStepIDs []string
    Input              *TraceInput
    Output             *TraceOutput  // Output.TokenUsage 为 LLM token 用量
    Error              string
    StartTime, EndTime time.Time
    Duration           time.Duration
}
```

> 注意：`TraceOutput.TokenUsage`（LLM prompt/completion tokens）与 `RuntimeConfig.Token`（业务身份 token）是两个完全独立的概念。前者记录 LLM 调用成本，后者用于在 log_collector 侧按业务隔离数据。

---

## 错误处理

上报失败时，以下日志会依次输出（调用方无需任何代码即能观测到）：

| 场景 | 来源 | 日志 |
|------|------|------|
| JSON 序列化失败 | `TRPCWriter.Write` | `[promptsampler] TRPCWriter: marshal trace failed: invocation_id=... err=...` |
| RPC 层错误 | `TRPCWriter.Write` | `[promptsampler] TRPCWriter: ReportTrace rpc failed: ...` |
| 业务错误码（`rsp.Code != 0`） | `TRPCWriter.Write` | `[promptsampler] TRPCWriter: ReportTrace biz failure: invocation_id=... code=1003 message=...` |
| Writer 返回任何 error | `PromptSampler.afterAgent` | `[promptsampler] trace write failed, dropped: invocation_id=... err=...` |

### log_collector 错误码对照

| code | 含义 |
|------|------|
| 0 | 成功 |
| 1001 | caller 为空 |
| 1002 | trace_json 为空 |
| 1003 | JSON 格式非法 |
| 1004 | 缺少必填字段 invocation_id |
| 2001 | 存储写入失败 |

---

## proto 协议同步约定

本目录的 `proto/log_collector.proto` 是 LogCollector 服务协议的**权威真源**。log_collector 服务仓中的 `git.woa.com/PromptHub/log_collector/proto/log_collector.proto` 是本文件的**只读镜像**，两份内容必须保持一致（仅 `go_package` 允许不同）。

> 为什么要保留两份 `.proto`？因为 log_collector 使用内部版 trpc（`git.code.oa.com/trpc-go/trpc-go`），本插件使用开源版 trpc（`trpc.group/trpc-go/trpc-go`），两者生成的 `.pb.go` / `.trpc.go` 无法直接共享（`server.Service` 等类型属于不同 Go 包，编译不互通）。因此**协议契约共享**，**生成物各自独立**。

修改流程：

1. **先在本仓** 改 `trpc-agent-go/plugin/promptsampler/proto/log_collector.proto`；
2. 用开源版工具链重新生成本仓的 `log_collector.pb.go` / `log_collector.trpc.go`；
3. 把 `.proto` 同步复制到 `log_collector/proto/log_collector.proto`（只调整 `go_package` 一行）；
4. 在 log_collector 仓用其内部版工具链重新生成该仓的 `pb.go` / `trpc.go`；
5. 同一次改动尽量同步提交两仓 PR，便于审计。

---

## 与 log_collector 集成

`log_collector` 服务暴露 `trpc.trs.prompt_log_collector.LogCollector` tRPC 服务，对应 proto 方法 `ReportTrace(ReportTraceRequest) returns (ReportTraceResponse)`。插件默认 target 即为该服务名，无需配置。

业务方典型部署架构：

```
┌──────────────────────┐    tRPC     ┌─────────────────────┐   MySQL   ┌───────┐
│  Your Agent Runner   │ ───────────►│   log_collector     │ ────────► │  DB   │
│  + promptsampler     │  ReportTrace│ (trpc.trs.prompt... │           └───────┘
└──────────────────────┘             └─────────────────────┘
```

---

## 注意事项

1. **性能影响**：`SampleRate=0` 或 `Enabled=false` 时，钩子开销极小（仅一次 atomic load）。
2. **内存控制**：长 trace 会累积 step，`WithMaxSteps` 可设上限，超限时后续 step 被丢弃。
3. **并发安全**：`PromptSampler` 可被多个 Runner 实例共享，内部按 root invocation ID 隔离。
4. **Context 脱离**：`AsyncWriter` 与 `TRPCWriter` 内部都使用 `context.WithoutCancel`，保证上游 Runner 返回后，后台上报仍能完成。
