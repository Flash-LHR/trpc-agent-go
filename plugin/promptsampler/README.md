# promptsampler

`promptsampler` 是一个 trpc-agent-go 采样插件，用于采集一次 Runner 任务执行的完整轨迹（Trace），并通过 tRPC 调用 `log_collector` 服务统一上报。

**Module 路径**: `trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler`

> **近期变更（BREAKING）**
> - `RuntimeConfig.Token` 已重命名为 `RuntimeConfig.SamplerToken`（JSON `sampler_token`）。
> - 构造选项 `WithToken(...)` 同步改为 `WithSamplerToken(...)`。
> - 新增 HTTP `ConfigHandler` 控制面，支持 default + per-app 配置。
> - `ConfigHandler` 已移除静态 admin token 鉴权：`WithAdminToken` / `WithAdminTokens` 选项被删除，handler 默认放行所有请求；需要自定义鉴权请改用 `WithAuthFunc`，或在外层 HTTP middleware 中处理。
> - 详见下文 **迁移指南** 和 **控制面接口** 章节。

---

## 功能特性

- 🎯 **一次任务一次上报** —— root Agent `AfterAgent` 触发时，输出聚合了所有 sub-agent / model / tool 步骤的 Trace
- 📊 **采样率控制** —— 支持 `[0, 1]` 可配置采样率
- 🔥 **运行时热更新** —— `Enabled` / `SampleRate` / `SamplerToken` 可通过 `SetConfig` 或 HTTP `ConfigHandler` 原子更新
- 🎯 **per-app 覆盖** —— 一个 sampler 同时服务多个 Runner，可按 appName 下发差异化采样策略
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
| `WithSamplerToken(string)` | 初始业务隔离 token（SamplerToken） | `""` |
| `WithWriter(TraceWriter)` | 自定义 writer | `LogWriter` |
| `WithLogWriter()` | 用日志输出 trace | — |
| `WithPrettyLogWriter()` | 缩进 JSON 日志 | — |
| `WithTRPCWriter(opts...)` | tRPC 上报 log_collector（开源版 tRPC） | — |
| `WithRPCWriter(opts...)` | 通用上报：ReportFunc 注入（兼容内部版 tRPC / 其他 transport） | — |
| `WithMaxSteps(int)` | Step 上限 | `1000` |
| `WithAsyncWrite(int)` | 异步队列长度 | `0`（同步） |
| `WithStructureID(string)` | 默认 structure ID | agent name |

### TRPCWriter 子选项（开源版 tRPC 用户）

| Option | 作用 | 默认 |
|--------|------|------|
| `WithTRPCCaller(string)` | 显式覆盖 caller | 自动读 `trpc.GlobalConfig()` |
| `WithTRPCTarget(string)` | 目标服务名 | `"trpc.trs.prompt_log_collector.LogCollector"` |
| `WithTRPCTimeout(Duration)` | 单次调用超时 | `3s` |
| `WithTRPCClient(proxy)` | 注入 mock / 自定义 proxy | 默认 tRPC client |

### RPCWriter 子选项（自定义 transport / 内部版 tRPC）

| Option | 作用 | 默认 |
|--------|------|------|
| `WithRPCReportFunc(ReportFunc)` | **必选**：用户提供的上报回调 | — |
| `WithRPCCaller(string)` | **必选**：本服务名，plugin 不会尝试自动解析（避免跨 tRPC 版本） | — |
| `WithRPCTimeout(Duration)` | 单次调用超时 | `3s` |

---

## 控制面接口

本插件自带一个 `http.Handler`，业务方在启动时把它挂到任意 `http.ServeMux` 前缀下即可：

```go
sampler := promptsampler.New(
    promptsampler.WithSampleRate(0.1),
    promptsampler.WithTRPCWriter(),
)

mux := http.NewServeMux()
mux.Handle(
    // URL 前缀由业务方决定。
    "/promptiter/v1/plugins/trace_reporter/config",
    sampler.ConfigHandler(),
)
go http.ListenAndServe(":9090", mux)
```

### 默认放行；鉴权属于业务方

`ConfigHandler()` 默认 **permissive**：未传入 `WithAuthFunc(...)` 时，任何请求都会进入路由（GET/PUT/DELETE）。这是出于以下设计原则：

- `ConfigHandler` 应挂在**运维内网**（或进程自身控制的 admin 端口）上，不应对公网暴露。网络边界由业务方负责。
- `RuntimeConfig.SamplerToken` 是**业务隔离标签**，透传给 `log_collector` 的 `ReportTraceRequest.Token`，它不是凭证；**凭证级别的访问控制**（例如允许谁写、允许谁读 trace）由下游 `log_collector` 统一承担。
- plugin 不再提供静态 admin token 开关；需要进程内鉴权请用 `WithAuthFunc`，或在外层 HTTP middleware 中处理。

### SamplerToken 的定位

| 字段/选项 | 作用 | 方向 |
|-----------|------|------|
| `RuntimeConfig.SamplerToken` / `WithSamplerToken(...)` | 上报 trace 时透传给 log_collector 的业务隔离标签 | Agent → log_collector |

`SamplerToken` **不是** HTTP 访问凭证，也不参与 ConfigHandler 的鉴权；它只进 log_collector 的业务数据。

### HTTP API

#### GET 获取配置

```bash
# 不带 ?app= → 返回 default 配置 + 所有 per-app overrides
curl http://localhost:9090/promptiter/v1/plugins/trace_reporter/config

# 带 ?app=X → 返回 X 对应的生效配置（override 或 default fallback）
curl "http://localhost:9090/promptiter/v1/plugins/trace_reporter/config?app=my-app"
```

响应示例（不带 `?app=`）：

```json
{
  "config": {"enabled": true, "sample_rate": 0.1, "sampler_token": "default-tok"},
  "apps": {
    "my-app": {"enabled": true, "sample_rate": 1.0, "sampler_token": "app-tok"}
  }
}
```

响应示例（带 `?app=my-app`）：

```json
{
  "config": {"enabled": true, "sample_rate": 1.0, "sampler_token": "app-tok"},
  "source": "override"
}
```

未命中 override 时 `source` 为 `"default"`。

#### PUT 更新配置（完整覆盖语义）

```bash
# 更新 default 配置
curl -X PUT -H "Content-Type: application/json" \
     -d '{"config":{"enabled":true,"sample_rate":0.5,"sampler_token":"biz-a"}}' \
     http://localhost:9090/promptiter/v1/plugins/trace_reporter/config

# 为指定 app 写一条覆盖（一次 PUT = 一份完整配置，不做字段级合并）
curl -X PUT -H "Content-Type: application/json" \
     -d '{"config":{"enabled":true,"sample_rate":1.0,"sampler_token":"biz-a"}}' \
     "http://localhost:9090/promptiter/v1/plugins/trace_reporter/config?app=my-app"
```

#### DELETE 删除 app 覆盖

```bash
curl -X DELETE \
     "http://localhost:9090/promptiter/v1/plugins/trace_reporter/config?app=my-app"
```

成功 `204 No Content`；不存在的 override 返回 `404 Not Found`；**DELETE 不支持默认配置**（`405 Method Not Allowed`），如需重置请用 PUT。

#### 错误响应

统一格式：

```json
{"error": "<message>"}
```

典型状态码：

| 状态 | 触发场景 |
|------|----------|
| 200  | GET / PUT 成功 |
| 204  | DELETE 成功 |
| 400  | body 非 JSON / 缺少 `config` 字段 / `sample_rate` 超出 [0,1] |
| 401  | 仅当 `WithAuthFunc` 被传入且其返回 `false` 时出现 |
| 404  | DELETE 时指定的 app 没有 override |
| 405  | 方法不被支持；对 default 发 DELETE |

### 同进程 Go API

HTTP 之外也可从同进程直接调用：

```go
// 读取
cfg := sampler.GetConfig()  // default 配置
appCfg, isOverride := sampler.GetAppConfig("my-app")

// 写入
_ = sampler.SetConfig(&promptsampler.RuntimeConfig{
    Enabled:      true,
    SampleRate:   0.5,
    SamplerToken: "biz-a",
})
_ = sampler.SetAppConfig("my-app", &promptsampler.RuntimeConfig{
    Enabled:      true,
    SampleRate:   1.0,
    SamplerToken: "biz-a-full",
})

// 删除
removed := sampler.DeleteAppConfig("my-app")

// 列出所有 overrides
apps := sampler.ListAppConfigs()
```

`SetConfig` 成功后，下一次 Trace 上报时 `ReportTraceRequest.Token` 即为新值（通过 TokenSetter 传递）。

### 自定义鉴权（可选）

当 `ConfigHandler` 确实需要在进程内做一层轻量鉴权（例如接公司 IAM、签名、IP 白名单）时，可通过 `WithAuthFunc` 注入：

```go
handler := sampler.ConfigHandler(
    promptsampler.WithAuthFunc(func(r *http.Request) bool {
        // 返回 true 放行；false 返回 401 Unauthorized
        return myIAM.Verify(r)
    }),
)
```

`WithAuthFunc` 是 plugin 内**唯一**的鉴权扩展点。若不调用它，handler 对所有请求放行。

### 迁移指南

#### 从旧字段名迁移

| 旧 | 新 |
|----|----|
| `RuntimeConfig.Token` | `RuntimeConfig.SamplerToken`（JSON `"sampler_token"`） |
| `WithToken("biz")`     | `WithSamplerToken("biz")` |

字段改名是**破坏性变更**，需要同步修改所有直接构造 `RuntimeConfig` 字面量的调用点。字段语义不变：仍是上报给 log_collector 的业务隔离标签。

#### 从 admin token 鉴权迁移

| 旧 | 新 |
|----|----|
| `sampler.ConfigHandler(promptsampler.WithAdminToken(token))` | `sampler.ConfigHandler()`（默认放行） |
| `sampler.ConfigHandler(promptsampler.WithAdminTokens(t1, t2))` | `sampler.ConfigHandler()`，在外层 HTTP middleware 自行匹配 / 或 `WithAuthFunc(...)` |
| 请求必带 `Authorization: Bearer <token>` | 无鉴权需求，直接 curl；若配置了 `WithAuthFunc`，按其约定鉴权 |
| 部署环境变量 `PROMPTSAMPLER_ADMIN_TOKEN` | 不再使用；部署平台可移除 |

凭证级访问控制现在由下游 `log_collector` 对 `SamplerToken` 的校验承担（独立变更，不在 plugin 仓）。

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

> 注意：`TraceOutput.TokenUsage`（LLM prompt/completion tokens）与 `RuntimeConfig.SamplerToken`（业务身份 token）是两个完全独立的概念。前者记录 LLM 调用成本，后者用于在 log_collector 侧按业务隔离数据。

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

## 适配内部版 tRPC / 自定义 transport

本插件自身使用**开源版 tRPC**（`trpc.group/trpc-go/trpc-go`）生成 proto client，无法与腾讯内部版（`git.code.oa.com/trpc-go/trpc-go`）生成的 `client.Client` 互通 —— 两个版本的 `server.Service`、`client.Client`、filter / naming 注册表均独立，强行同时引入会导致 polaris 解析失败、filter 链失效。

**解决方式**：使用 `WithRPCWriter(WithRPCReportFunc(...), WithRPCCaller(...))`，由业务方在自己的 tRPC 生态里组装 client proxy 并提供上报回调，plugin 只负责拼装 `caller / traceJSON / token` 三个字段：

```go
import (
    logpb "git.woa.com/PromptHub/log_collector/proto"        // 内部版生成的 pb.go
    tclient "git.code.oa.com/trpc-go/trpc-go/client"
    trpc "git.code.oa.com/trpc-go/trpc-go"
    "trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler"
)

proxy := logpb.NewLogCollectorClientProxy()
callerName := trpc.GlobalConfig().Server.Service[0].Name

reportTrace := func(ctx context.Context, caller, traceJSON, token string) error {
    rsp, err := proxy.ReportTrace(
        ctx,
        &logpb.ReportTraceRequest{
            Caller:    caller,
            TraceJson: traceJSON,
            Token:     token,
        },
    )
    if err != nil {
        return err
    }
    if rsp.GetCode() != 0 {
        return fmt.Errorf("code=%d message=%s", rsp.GetCode(), rsp.GetMessage())
    }
    return nil
}

sampler := promptsampler.New(
    promptsampler.WithSampleRate(1.0),
    promptsampler.WithStructureID("my-agent"),
    promptsampler.WithRPCWriter(
        promptsampler.WithRPCCaller(callerName),
        promptsampler.WithRPCReportFunc(reportTrace),
    ),
    promptsampler.WithAsyncWrite(100),
)
```

### 何时用 TRPCWriter，何时用 RPCWriter

| 场景 | 推荐 |
|------|------|
| 你的服务 import `trpc.group/trpc-go/trpc-go`（开源版） | `WithTRPCWriter()` —— 零参数接入 |
| 你的服务 import `git.code.oa.com/trpc-go/trpc-go`（内部版） | `WithRPCWriter(...)` + 业务侧 5 行适配 |
| 你要通过 HTTP / gRPC / Kafka 等其他通路上报 | `WithRPCWriter(...)` + 业务侧回调 |
| 单测 / mock | `WithWriter(customWriter)` —— 最灵活 |

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
