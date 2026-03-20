# trpc-agent-go Execution Trace 设计文档

## 1. 设计定位

Execution Trace 是 `trpc-agent-go` 的框架级执行工件能力，用于稳定记录一次请求在运行时实际发生的执行步骤、步骤依赖关系以及步骤级输入输出快照。

它的目标不是替代 OpenTelemetry span，也不是替代现有事件流，而是提供一份更适合算法消费、评测对齐、问题回放和离线分析的结构化执行事实。

Execution Trace 应当作为独立框架能力存在，而不是作为 PromptIter 的私有实现细节，原因有三点：

1. PromptIter 只是它的一个消费者，调试回放、trace reporter、质量分析、行为审计都可能复用同一份执行工件。
2. 执行轨迹的事实来源在运行时内核，而不在上层算法模块；把能力放在 PromptIter 内会导致依赖方向错误。
3. `trpc-agent-go` 是框架，不应把“记录执行步骤”这种底层能力绑死到某个具体算法特性上。

一句话概括本设计的归属边界：

- 能力归执行层。
- 对齐归 evaluation。
- 消费归 PromptIter、reporter 与其他上层功能。

## 2. 目标

本设计解决以下问题：

1. 为 graph、llmagent 以及 child-invocation 型组合运行时提供请求级、按需开启的执行轨迹采集能力。
2. 为 evaluation 提供稳定的 case 级轨迹对齐能力，而不是依赖外层对事件流做事后拼装。
3. 为 PromptIter 提供可转换为 step-DAG 的统一输入工件。
4. 为 trace reporter、调试回放、离线诊断提供统一基础设施。
5. 保持默认零侵入、低开销，只有显式开启时才发生额外采集成本。

## 3. 非目标

本设计不解决以下问题：

1. 不替代 OpenTelemetry spans、Langfuse exporter 或现有 observability 能力。
2. 不把完整 session、完整 state、完整消息历史无边界地全部转储进轨迹。
3. 不尝试让所有自定义 Agent 自动具备多 step 轨迹能力。
4. 不在本设计里同时解决 PromptIter 的 metric-to-step 归因问题。那是 evaluation 结果模型的独立能力。
5. 不把 trace reporter 插件本身定义为核心能力；插件只是消费者，不是能力本体。

## 4. 关键判断

### 4.1 是否值得做成独立功能

值得，而且有必要。

如果只把这项能力塞进 PromptIter，会立即出现三个问题：

1. PromptIter 需要侵入 graph/agent 内核去拿 step 级事实。
2. 其他需要执行轨迹的功能只能重复造轮子。
3. `trace_reporter`、evaluation、PromptIter 三者会分别维护不同口径的“轨迹”。

因此，本设计采用独立功能形态，命名为 `Execution Trace`，实现上放在 `agent/trace` 子包中承载契约，而不是新增顶层 `trace` 目录。

### 4.2 应该放在哪一层

最合理的分层是：

1. `agent/trace` package 负责定义公共契约、采集句柄和最小公共工具。
2. `graph`、`agent/llmagent` 和 child-invocation 型组合运行时负责产出执行事实。
3. `evaluation` 负责把执行轨迹和 `EvalSetID / EvalCaseID / SessionID / InvocationID` 对齐。
4. `promptiter` 负责把通用轨迹转换成自己的 `promptiter.Trace` 视图。
5. `trace_reporter` 或其他插件负责消费这份轨迹，而不是拥有它。

### 4.3 为什么不把能力做在 evaluation 里

因为 evaluation 不是执行层，它知道评测上下文，但不知道稳定的 step 事实应该如何在运行时生成。

evaluation 很适合作为接入点，不适合作为事实生产者。它的职责应该是：

1. 在 case 维度开启采集。
2. 把轨迹和评测工件对齐。
3. 把轨迹交给上层消费者或持久化策略。

### 4.4 为什么不把能力做成插件本体

插件适合做“消费”和“上报”，不适合做能力所有权。

如果把执行轨迹能力本身做成插件，会把运行时的基础数据通道隐式化，导致：

1. graph/agent runtime 仍然要暴露内部事实给插件。
2. evaluation 依赖具体插件才能拿到轨迹。
3. PromptIter 无法把“轨迹是必需输入”表达成稳定框架契约。

正确做法是：

1. 核心能力独立存在。
2. 插件按需消费核心能力产出的 `ExecutionTrace`。

## 5. 术语

### 5.1 Execution Trace

一次请求在运行时发生的结构化执行事实，包含 step、依赖关系、输入输出快照、错误和补充注解。在 multi-agent 或组合运行时场景下，它还负责把多个 child invocation 收敛成一份根 trace。

### 5.2 Step

一次实际执行的节点访问。静态 node 可以在一次请求中执行多次，每次执行都对应一个独立 step。

### 5.3 Snapshot

对 step 输入或输出的标准化快照，不等于完整原始对象。Snapshot 必须可控、可截断、可脱敏。

### 5.4 Annotation

附着在 step 上的补充结构化引用，例如 PromptIter 需要的 `prompt_surface` 命中信息。

### 5.5 与 Telemetry Trace 的区别

Telemetry trace 面向观测系统，Execution Trace 面向框架消费者和算法输入。两者可以互补，但不是同一概念，也不应互相替代。

## 6. 总体方案

### 6.1 总体架构

```text
runner / graph / llmagent / composite-agent runtime
        │
        │ 产出执行事实
        ▼
agent/trace
        │
        ├── evaluation 对齐
        │       │
        │       └── 与 EvalCase / Session / Invocation 绑定
        │
        ├── promptiter 消费
        │       │
        │       └── 转为 promptiter.Trace
        │
        └── reporter / replay / debug 消费
```

### 6.2 启用策略

Execution Trace 默认关闭，按请求开启。

启用路径必须满足以下要求：

1. 对单次请求生效。
2. 不要求重建 agent 或 runner。
3. 不依赖全局开关。
4. 不开启时只有极低的 nil-check 成本。

### 6.3 支持范围

1. `graph` 提供原生多 step 执行轨迹。
2. `llmagent` 提供单 step trace。
3. `ChainAgent`、`ParallelAgent`、`CycleAgent`、`Team/Swarm` 以及 `transfer_to_agent` 这类 child-invocation 型组合运行时需要接入跨 invocation 轨迹拼接能力。
4. 其他自定义 agent 类型默认不保证具备 execution trace；但只要它们通过 child invocation 组合子 agent，就应复用同一套拼接协议接入。

## 7. 公共包设计

新增公共子包：

```text
/agent/trace
```

它承载框架级公共契约，但不新增顶层目录，而是作为 `agent` 下的运行时公共子包存在。

这样做的原因如下：

1. 该能力本质上依附于 invocation、run option、child invocation 拼接和 agent 运行时，而这些核心对象都在 `agent` 层。
2. `graph` 当前已经依赖 `agent`，因此放在 `agent` 子包不会引入新的反向依赖问题。
3. `runner` 不适合承载该类型，因为 graph 和 llmagent 不能反向依赖 runner。
4. `evaluation` 不适合承载该类型，因为它只是对齐和消费者，不是运行时事实定义层。
5. `artifact` 现有语义是内容工件和持久化服务，Execution Trace 不应与内容 artifact 混在同一抽象层。
6. `trace` 在顶层已经强烈指向 telemetry tracing，而 `agent/trace` 明确限定在 agent runtime 作用域内，语义更清晰。
7. 相比新增顶层目录，`agent/trace` 更符合当前仓库的包层次习惯，也避免顶层 package 持续膨胀。

### 7.1 核心对象模型

```go
package trace

type TraceStatus string

const (
    TraceStatusCompleted  TraceStatus = "completed"
    TraceStatusIncomplete TraceStatus = "incomplete"
    TraceStatusFailed     TraceStatus = "failed"
)

type Options struct {
    MaxSnapshotBytes int
    MaxSteps         int
}

type Trace struct {
    TraceID       string
    AppName       string
    AgentName     string
    InvocationID  string
    Branch        string
    SessionID     string
    UserID        string
    StartedAt     time.Time
    EndedAt       time.Time
    Status        TraceStatus
    FinalOutput   *Snapshot
    Steps         []Step
}

type Step struct {
    StepID             string
    InvocationID       string
    ParentInvocationID string
    AgentName          string
    Branch             string
    NodeID             string
    NodeName           string
    Kind               string
    StepNumber         int
    Attempt            int
    StartedAt          time.Time
    EndedAt            time.Time
    PredecessorStepIDs []string
    TriggerRefs        []string
    Input              *Snapshot
    Output             *Snapshot
    Error              string
    Annotations        []Annotation
}

type Snapshot struct {
    Text      string
    Truncated bool
}

type Annotation struct {
    Kind   string
    ID     string
    Labels map[string]string
}
```

设计要点如下：

1. `Trace` 是通用执行轨迹，不直接依赖 `promptiter.Trace`。
2. `Step.PredecessorStepIDs` 是框架公共能力，不只为 PromptIter 服务。
3. `Step.Annotations` 是通用扩展点，PromptIter 需要的 `AppliedSurfaceIDs` 可通过 `Kind=prompt_surface` 的 annotation 承载。
4. `Snapshot` 只记录标准化快照，不记录原始任意对象。
5. `Options` 只保留必要的成本边界控制，避免把采集策略设计成复杂策略系统。
6. `Step.InvocationID / ParentInvocationID / AgentName / Branch` 用于表达多 Agent 组合运行时的真实调用边界。

### 7.2 采集句柄

公共包提供请求级采集句柄：

```go
package trace

type Handle struct {
    // Omitted.
}

func NewHandle(opts Options) *Handle

func WithHandle(h *Handle) agent.RunOption

func HandleFromContext(ctx context.Context) (*Handle, bool)

func WithHandleContext(ctx context.Context, h *Handle) context.Context

func (h *Handle) Trace() (*Trace, bool)
```

设计要点如下：

1. `Handle` 是面向调用方的稳定抓手。
2. 直接使用 Runner 的调用方可以通过 `WithHandle(...)` 开启采集。
3. evaluation 等编排层可以通过 context 在 case 维度传递 handle。
4. 运行时只认 handle，不关心上层是谁。

### 7.3 为什么同时提供 RunOption 和 Context 两种入口

两者解决的是不同使用场景：

1. 普通 Runner 用户更适合用 `agent.RunOption`。
2. evaluation 的 case 级动态编排更适合用 context。
3. 两种入口最终都收敛到同一个 `Handle`，不会形成双轨能力。

### 7.4 跨 invocation 拼接

multi-agent 或组合运行时需要把“child invocation 的首个 step 依赖谁”稳定传下去。

这件事是运行时内部协议，不需要暴露成公共 API。做法如下：

1. 组合运行时在创建 child invocation 时，把 parent step id 写入内部 runtime state。
2. child runtime 的首个 step 读取这个内部值并写入 `PredecessorStepIDs`。
3. 这套拼接协议只在框架内部使用，不向普通调用方暴露 `InvocationEdge` 之类的公共类型。

这样可以减少公共 API 面积，同时保留 graph、llmagent 和多 Agent 运行时所需的最小拼接能力。

## 8. graph 集成设计

### 8.1 设计原则

graph 是多 step execution trace 的主要生产者，必须原生产出 step 事实，不能依赖外层回放事件流拼装。

### 8.2 集成位置

不修改 `agent.Agent` 公共接口。

在 graph 内部集成的主要位置如下：

1. `ExecutionContext` 增加 execution trace recorder 与 provenance 状态。
2. 节点执行开始时创建 step。
3. 节点执行结束时补齐输出或错误。
4. channel 更新时维护 channel provenance。
5. 图执行结束时收敛成最终 `Trace`。

### 8.3 ExecutionContext 扩展

在 `graph.ExecutionContext` 增加以下字段：

```go
type ExecutionContext struct {
    // Existing fields.

    traceHandle       *trace.Handle
    traceRecorder     *traceRecorder
    channelProvenance map[string][]string
}
```

含义如下：

1. `traceHandle` 表示本次请求是否开启轨迹采集。
2. `traceRecorder` 负责 step 生命周期管理。
3. `channelProvenance` 维护“当前可见 channel 值来自哪些 step”。

### 8.4 StepID 生成

采用 trace 内单调递增序号生成稳定 `StepID`：

```text
s1, s2, s3, ...
```

不要把 `NodeID`、`StepNumber`、`Attempt` 直接拼成外部 ID，原因是：

1. 对消费者不够简洁。
2. 一旦内部调度策略调整，ID 稳定性会受影响。
3. `NodeID`、`StepNumber`、`Attempt` 已经是独立字段，不需要重复编码进 `StepID`。

### 8.5 前驱 step 计算

graph 使用 `Task.Triggers` 和 channel provenance 计算直接前驱。

计算规则如下：

1. 每个 node task 在创建时已经带有 `Triggers`。
2. 每个 trigger channel 维护当前值的 provenance，也就是“哪些 step 产生了当前可见值”。
3. 节点开始执行时，取其所有 trigger channel 的 provenance 并集，稳定排序后写入 `PredecessorStepIDs`。

这种做法有三个优点：

1. 它直接利用了 graph 当前的调度模型，不需要额外发明第二套依赖图。
2. 它表达的是“实际触发关系”，而不是静态边关系。
3. 它能自然覆盖 fan-in、fan-out 和回环展开后的多次执行。

### 8.6 channel provenance 更新规则

channel provenance 的更新规则与 channel 行为一致：

1. 对覆盖型 channel，新值写入后 provenance 重置为当前 step。
2. 对聚合型 channel，新值写入后 provenance 追加当前 step，并去重保持顺序。
3. 对输入初始化阶段生成的 channel，provenance 为空。

这保证了 `PredecessorStepIDs` 表示的是“直接可见来源”，而不是整条历史路径。

### 8.7 输入输出快照

graph 不记录完整 `State`，只记录标准化 snapshot。

默认策略如下：

1. LLM 节点输入使用最终模型输入摘要；输出使用最终模型输出摘要。
2. Tool 节点输入使用工具参数摘要；输出使用工具返回摘要。
3. 普通 Function/Router/Join 节点默认走通用状态序列化器，只提取文本友好的关键字段并做截断。
4. 任意 snapshot 都受 `trace.Options.MaxSnapshotBytes` 限制。

不引入公共 `Snapshotter` 接口。节点差异由 graph 内部默认序列化逻辑处理，避免过早冻结一套 node 级扩展协议。

### 8.8 Step 注解

Execution Trace 不把 PromptIter 的 `AppliedSurfaceIDs` 直接写死在核心模型里，而是通过 annotation 承载。

例如：

```text
Kind=prompt_surface
ID=surface_id
Labels={node_id=planner,type=instruction}
```

这样可以同时满足：

1. PromptIter 需要稳定 surface 命中信息。
2. 核心 trace 模型保持算法无关。
3. 其他功能也可以定义自己的 annotation kind。

## 9. llmagent 与 multi-agent / 组合运行时集成设计

### 9.1 设计目标

单个 `llmagent` 也应具备 execution trace，但其语义天然是 single-step trace。

### 9.2 表达方式

一次 llmagent run 生成一个单 step trace：

1. `StepID = s1`
2. `NodeID = agent.Info().Name`
3. `PredecessorStepIDs = nil`
4. 输入使用最终拼装后的模型输入快照
5. 输出使用最终 assistant 输出快照

### 9.3 为什么不把 tool/model 子过程拆成多个 step

不把 tool/model 子过程拆成多个 step，原因如下：

1. llmagent 当前公共抽象没有 graph 那样稳定的多节点执行语义。
2. 把工具循环拆成多个 step 会把 llmagent 的内部流控细节暴露成公共契约，风险太大。
3. 对 PromptIter 而言，single-step trace 已经足够承载 `instruction/global_instruction/model` 这类 surface。

### 9.4 multi-agent / 组合运行时设计目标

Execution Trace 不能只覆盖 graph 和单 agent。`trpc-agent-go` 已有 `ChainAgent`、`ParallelAgent`、`CycleAgent`、`Team/Swarm`，同时也存在 `transfer_to_agent` 这类通过 child invocation 进行控制切换的运行时形态，这些场景都需要被稳定表达。

这里的核心问题不是“有没有 step”，而是“多个 invocation 如何拼成同一份根 trace，并保持真实控制依赖”。

### 9.5 multi-agent / 组合运行时基本语义

multi-agent 或组合运行时场景采用“一次根请求、一份根 trace、多条 invocation 分支”的表达方式。

基本规则如下：

1. 根请求只生成一份 `Trace`。
2. 子 agent invocation 不开启独立 trace，而是把自己的 step 追加到同一份根 trace。
3. 每个 step 必须记录 `InvocationID`、`ParentInvocationID`、`AgentName` 和 `Branch`。
4. 跨 agent 的控制切换通过 `PredecessorStepIDs` 表达，而不是靠事后推断 branch 文本。

### 9.6 multi-agent / 组合运行时直接前驱规则

直接前驱规则如下，且适用于任何 child-invocation 型运行时：

1. 子 invocation 的首个 step，前驱取“触发这次子 invocation 的直接上游 terminal step”。
2. 如果是并行 fan-out，多个子 invocation 的首个 step 可以共享同一个前驱。
3. 如果是串行 handoff，后一个子 invocation 的首个 step 以前一个子 invocation 的 terminal step 为前驱。
4. 如果当前 orchestrator 本身没有业务 step，可以记录一个 orchestration step 作为稳定连接点。

### 9.7 不同运行时形态的落地方式

`ChainAgent`：

1. 每个子 agent 的首个 step，以前一个子 agent 的 terminal step 为前驱。
2. 第一个子 agent 如果是从父 orchestration 直接进入，则以前一个父 step 或 orchestration step 为前驱。

`ParallelAgent`：

1. 父 agent 生成一个 orchestration step。
2. 每个并行子 agent 的首个 step 都以前述 orchestration step 为直接前驱。
3. 并行子分支共享同一 root trace，但保留各自 `Branch`。

`CycleAgent`：

1. 每轮子 agent 的首个 step，以当前循环中的上一个 terminal step 为前驱。
2. 如果一轮循环从“循环开始”直接进入第一个子 agent，则以前一轮的 terminal step 或 cycle orchestration step 为前驱。

`Team/Coordinator`：

1. coordinator 自身按普通 agent 记录 step。
2. 当 coordinator 调起 member agent 时，member 首个 step 以前一个 coordinator terminal step 为前驱。

`Team/Swarm`：

1. transfer 目标 agent 的首个 step，以 source agent 的 terminal step 为前驱。
2. 跨请求 transfer 仍然可以复用 `AgentName` 和 `Branch` 表达活跃 agent，但跨请求不合并进同一 root trace。

`llmagent transfer_to_agent`：

1. 当前 llmagent 触发 transfer 后，目标 agent 的首个 step 以前一个 llmagent terminal step 为前驱。
2. 如果 transfer 发生在同一根请求内，则继续写入同一份 root trace。
3. 如果 transfer 被框架实现为新的 child invocation，则通过内部 runtime state 传递前驱，而不是靠 branch 字符串推断。

### 9.8 句柄传播

multi-agent 不应额外发明第二套 trace 传播协议。直接复用现有 invocation 复制语义即可：

1. 根请求在 run option 或 context 上绑定 `trace.Handle`。
2. 子 invocation 在 clone 时沿用同一 handle。
3. 组合运行时在创建 child invocation 后写入内部 runtime state。
4. child runtime 的首个 step 读取该内部值并写入 `PredecessorStepIDs`。
5. root recorder 用 `InvocationID / ParentInvocationID / Branch` 把子分支拼接回同一份 trace。

## 10. evaluation 集成设计

### 10.1 evaluation 的职责

evaluation 不负责生成轨迹，负责把轨迹和评测工件对齐。

它需要解决的是：

1. 每个 eval case 如何独立开启 execution trace。
2. 如何把 trace 和 `EvalSetID / EvalCaseID / SessionID / InvocationID` 对齐。
3. 如何把 trace 交给上层消费者而不污染核心 result 模型。

### 10.2 回调接口扩展

当前 `BeforeInferenceCaseResult` 只有 `Context`，不够表达“给这个 case 动态附加 run options”。

扩展为：

```go
type BeforeInferenceCaseResult struct {
    Context    context.Context
    RunOptions []agent.RunOption
}
```

这是一个通用增强，不只服务 execution trace。它能让 evaluation 在每个 case 维度动态注入：

1. execution trace handle
2. prompt profile override
3. sandbox / model / tracing 等其他按 case 变化的能力

### 10.3 AfterInferenceCaseArgs 扩展

新增结构化工件字段：

```go
type InferenceArtifacts struct {
    ExecutionTrace *trace.Trace
}

type AfterInferenceCaseArgs struct {
    Request   *InferenceRequest
    Result    *InferenceResult
    Artifacts *InferenceArtifacts
    Error     error
    StartTime time.Time
}
```

这样做的好处是：

1. `InferenceResult` 保持轻量。
2. evaluation 可以把大体积轨迹作为附加工件传递，而不是绑进默认结果模型。
3. PromptIter、reporter、调试工具都可以直接在 callback 中消费。

### 10.4 为什么不把 ExecutionTrace 直接塞进 InferenceResult

不建议，原因如下：

1. 轨迹体积可能明显大于普通 inference result。
2. 不是所有 evaluation 用户都需要它。
3. 轨迹的持久化策略可能与普通评测结果不同。
4. callback artifacts 已经足够满足框架主链路使用。

### 10.5 evaluation 侧典型流程

```text
BeforeInferenceCase:
    create trace handle
    put handle into case-scoped run options
    put handle into callback context

Runner.Run:
    runtime records execution trace into handle

AfterInferenceCase:
    read trace from handle
    place trace into Artifacts.ExecutionTrace
```

## 11. PromptIter 集成方式

PromptIter 不直接依赖 graph 内部结构，也不直接解析 event stream。

它通过 evaluation 回调获取 `ExecutionTrace`，再做一次适配：

```text
trace.Trace -> promptiter.Trace
```

映射规则如下：

1. `trace.Step.StepID -> promptiter.TraceStep.StepID`
2. `trace.Step.NodeID -> promptiter.TraceStep.NodeID`
3. `trace.Step.PredecessorStepIDs -> promptiter.TraceStep.PredecessorStepIDs`
4. `trace.Step.Input/Output -> promptiter.TraceInput/TraceOutput`
5. `trace.Annotation(kind=prompt_surface) -> AppliedSurfaceIDs`

这样可以保证：

1. PromptIter 拿到的是自己需要的最小语义视图。
2. 下层不需要依赖 PromptIter 包。
3. 其他算法也可以在不修改运行时的情况下消费 `ExecutionTrace`。

## 12. trace reporter 与独立插件

本设计认为 trace reporter 应当是 `ExecutionTrace` 的消费者，而不是能力本身。

推荐做法如下：

1. 核心 `agent/trace` 能力先独立落地。
2. reporter 以插件或服务形式消费 `ExecutionTrace`。
3. 采样策略由 reporter 或调用方决定，是否创建 handle 即代表是否采集。

这样可以自然支持：

1. 在线采样上报
2. 本地 debug 回放
3. PromptIter 训练集/验证集按需采样

## 13. 成本控制与安全边界

### 13.1 默认关闭

Execution Trace 默认关闭，不对普通请求增加显著成本。

### 13.2 快照截断

所有 snapshot 都必须经过统一截断：

1. 单个 snapshot 最大 8KB。
2. 超出后标记 `Truncated=true`。
3. 对结构化对象优先走 summary，不做完整 JSON dump。

### 13.3 脱敏

框架默认不记录：

1. 完整 session transcript
2. 原始鉴权信息
3. 任意未受控二进制 payload

### 13.4 Step 数量上限

`trace.Options.MaxSteps` 用于保护异常回环或超长执行，超限后 trace 状态标记为 `incomplete`，并停止继续采集新 step。

## 14. 兼容性与演进

### 14.1 向后兼容

本设计不要求修改现有 `agent.Agent` 公共接口，因此不会引入全局 breaking change。

## 15. 备选方案比较

### 15.1 方案 A：仅放在 PromptIter 内

不采用。

原因：

1. 复用性差。
2. 依赖方向错误。
3. 其他轨迹消费者无法共享同一套事实模型。

### 15.2 方案 B：完全放在 evaluation 内

不采用。

原因：

1. evaluation 不是执行事实生产者。
2. 它只能在外层拼装，稳定性和精度都不足。

### 15.3 方案 C：完全依赖 event stream 回放重建

不采用。

原因：

1. 现有事件流元数据不足以稳定重建 `PredecessorStepIDs`。
2. 事件流是观测产物，不应强行承担算法输入语义。
3. 回放逻辑复杂且脆弱。

### 15.4 方案 D：独立框架能力 + evaluation 对齐 + 上层消费

采用。

这是兼顾通用性、可扩展性、依赖方向和开发收敛度的最佳方案。

## 16. 开发落地建议

这个能力推荐一次性做完，不按阶段版号切分，也不按过细的技术层拆成很多小 PR。

框架能力本体应当一次性包含以下内容：

1. `agent/trace` 公共子包与对象模型。
2. `Handle` 和必要的 limits 配置。
3. graph 的 step 记录、前驱计算和 snapshot 采集。
4. llmagent 的单 step trace。
5. `ChainAgent`、`ParallelAgent`、`CycleAgent`、`Team/Swarm` 以及 transfer-based runtime 的跨 invocation 拼接。
6. evaluation 的 run options 与 artifacts 接口扩展。

PromptIter 和 reporter 属于消费端。它们可以跟随各自功能 PR 接入，但不应该反过来驱动框架能力裁剪。

## 17. 最终结论

Execution Trace 应当作为 `trpc-agent-go` 的独立框架能力落地，而不是仅放在 evaluation 或 PromptIter 内部。

它的最佳边界是：

1. 公共契约在 `agent/trace` 子包。
2. graph、llmagent 与组合运行时负责生产事实。
3. evaluation 负责 case 级对齐与传递。
4. PromptIter、reporter、debug 工具负责消费。

这样做既为 PromptIter 铺路，但又不只是为 PromptIter 铺路；它本身就是一个成熟、通用、易用且可扩展的框架能力。
