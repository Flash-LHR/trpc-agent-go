# Guardrail Capabilities Design

## 1. 目标

本文定义 `trpc-agent-go` guardrail 体系的最终设计，只保留稳定接口、运行语义和 capability 边界。

guardrail 的顶层 capability 固定为：

- `approval`
- `secret`
- `promptinjection`
- `unsafeintent`

guardrail 的核心目标是统一以下四类行为：

- 内容放行。
- 内容改写。
- 内容或动作阻断。

## 2. 范围与非目标

本文覆盖：

- 顶层插件与 capability 的组织方式。
- 公开 API 与构造约束。
- hook 分工、执行顺序和 callback 映射。
- `pass / transform / block` 的固定语义。
- `approval` 与内容类 guardrail 的职责边界。

本文不包含：

- approval workflow。
- 人工审批。
- 平台层 auth / authz。
- webhook 安全。
- 审计事件持久化。

## 3. 总体设计

### 3.1 顶层结构

顶层只暴露一个统一入口：

- `plugin/guardrail`

公开 capability 目录固定为：

- `plugin/guardrail/approval`
- `plugin/guardrail/secret`
- `plugin/guardrail/promptinjection`
- `plugin/guardrail/unsafeintent`

凡是依赖模型能力的 capability，都通过各自的 `review` 子包把 `runner.Runner` 适配为稳定的 `Reviewer` 接口：

- `plugin/guardrail/approval/review`
- `plugin/guardrail/secret/review`
- `plugin/guardrail/promptinjection/review`
- `plugin/guardrail/unsafeintent/review`

统一规则如下：

- capability 主包不直接依赖 `runner.Runner`。
- capability 主包统一使用 `New(opts...)`。
- 可选依赖统一通过 option 注入。
- 所有模型依赖统一命名为 `Reviewer`。
- 所有模型依赖注入统一使用 `WithReviewer(...)`。

### 3.2 顶层组合 API

顶层组合方式固定为：

```go
guardrailPlugin, err := guardrail.New(
    guardrail.WithApproval(approvalPlugin),
    guardrail.WithSecret(secretPlugin),
    guardrail.WithPromptInjection(promptInjectionPlugin),
    guardrail.WithUnsafeIntent(unsafeIntentPlugin),
)
```

约束如下：

- `guardrail.New(...)` 允许不挂载任何 capability；顶层插件可先空构造，再按需组合子能力。
- 顶层 option 按 capability 命名：
  - `WithApproval`
  - `WithSecret`
  - `WithPromptInjection`
  - `WithUnsafeIntent`

### 3.3 构造契约

| Capability | Hook | 默认检测链路 | Reviewer 要求 | 默认动作 |
| --- | --- | --- | --- | --- |
| `approval` | `BeforeTool` | tool policy + reviewer judgment | 仅当存在 `ToolPolicyRequireApproval` 路径时必填 | `pass` / `block` |
| `secret` | `BeforeModel`、`AfterModel` | 规则检测，可选追加模型 review | 可选 | `transform` |
| `promptinjection` | `BeforeModel` | classifier reviewer | 必填 | `block` |
| `unsafeintent` | `BeforeModel` | classifier reviewer | 必填 | `block` |

补充约束：

- `approval` 的 reviewer 不是无条件必填。
- 只有默认策略或任一工具策略会走到 `ToolPolicyRequireApproval` 时，`approval` 才要求 reviewer 必填。
- `promptinjection` 与 `unsafeintent` 不提供无 reviewer 模式。
- `secret` 的规则检测始终开启；显式注入 reviewer 后，再追加模型 review。

## 4. 运行时契约

### 4.1 Hook 分工与固定顺序

guardrail 的 capability 与 hook 映射固定如下：

| Hook | 固定顺序 |
| --- | --- |
| `BeforeModel` | `secret` -> `unsafeintent` -> `promptinjection` |
| `AfterModel` | `secret` |
| `BeforeTool` | `approval` |

补充规则：

- `BeforeTool` 不做通用 `unsafeintent` 检查。
- `BeforeTool` 不做通用 `promptinjection` 检查。
- 跨 capability 的顺序保证只对顶层 `plugin/guardrail` 统一入口成立。
- 调用方若单独注册某个子 capability，只保证该 capability 自身行为，不保证跨能力编排顺序。

### 4.2 动作语义

内部动作语义固定为：

```go
type Action string

const (
    ActionPass      Action = "pass"
    ActionBlock     Action = "block"
    ActionTransform Action = "transform"
)

type Result struct {
    Action Action
    Reason string
}
```

动作含义固定如下：

- `pass`：不修改内容，继续执行。
- `transform`：改写内容，后续 capability 读取改写后的结果。
- `block`：终止当前 hook，不再进入后续 capability。

### 4.3 Callback 映射

所有 guardrail 都必须映射到现有 callback contract。

`BeforeModel`：

- `pass`：不返回 `CustomResponse`，不修改 `args.Request`。
- `transform`：原位修改 `args.Request.Messages` 中的文本内容。
- `block`：返回安全 `CustomResponse`，跳过模型调用。

`AfterModel`：

- `pass`：不返回 `CustomResponse`。
- `transform`：原位修改 `args.Response`。
- `block`：返回安全 `CustomResponse`，替换当前模型响应。

`BeforeTool`：

- `pass`：不返回 `CustomResult`，不修改 `Arguments`。
- `transform`：返回 `ModifiedArguments`。
- `block`：返回 `CustomResult`，跳过工具执行。

补充规则：

- `AfterModel` 的 `block` 是替换当前响应，不是中途中止模型生成。
- 如果当前响应携带 tool call，`AfterModel` 的替换会直接影响后续工具阶段。
- guardrail 不通过返回 error 实现正常阻断；error 只用于 capability 自身故障。

### 4.4 Streaming

流式场景的固定语义如下：

- `BeforeModel`：与非流式一致。
- `BeforeTool`：与非流式一致。
- `AfterModel`：对输出文本做聚合后再执行 guardrail。

规则固定为：

- `secret` 的输出检测需要完整文本。
- 流式 chunk 可能只包含部分内容。
- `AfterModel` 在流式场景下先缓冲 partial 文本，不提前放出原始文本内容。
- 在最终响应到达后，使用聚合后的完整文本执行 `transform / block`。

### 4.5 扫描范围

扫描范围固定如下：

| Hook | Include | Exclude |
| --- | --- | --- |
| `BeforeModel` | `Request.Messages[*].Content`，以及 `ContentParts` 中的 text part | `system` 消息、`ReasoningContent`、`ToolCalls[].Function.Arguments` |
| `AfterModel` | 当前模型响应中的 `Message.Content`，以及 `ContentParts` 中的 text part | `ReasoningContent`、`ToolCalls[].Function.Arguments` |
| `BeforeTool` | 当前待执行工具的 `ToolArguments` | 其他模型响应字段 |

额外规则：

- 默认不检查 `system` 消息。
- 默认读取 `user`、`assistant`、`tool` 文本。
- 不定义更细的来源标签。
- `unsafeintent` 只评估当前用户输入。
- 当前用户输入固定定义为 `BeforeModel` 时 `Request.Messages` 中最后一条 `role=user` 的消息文本，文本来源仅包括 `Content` 与 text `ContentParts`。
- `unsafeintent` 可把 `assistant / tool` 文本一并提供给 reviewer 作为上下文证据，但不把它们视为独立拦截对象。
- `promptinjection` 只对当前用户输入做阻断决策。
- `promptinjection` 可把 `assistant / tool` 文本一并提供给 reviewer 作为上下文证据，但不把它们视为独立拦截对象。

### 4.6 文本改写约束

`transform` 只能修改文本内容，不允许改动：

- model name。
- tools。
- response format。
- invocation metadata。

改写粒度固定为 `Segment` 级，不做 byte-offset 编辑。

### 4.7 错误处理

本设计不包含事件总线、审计 sink 或持久化存储。

capability 自身报错统一按 fail-closed 处理：

- 返回安全阻断结果，终止当前 hook。

## 5. Capability 设计

### 5.1 Approval

`approval` 是 tool guardrail，不是内容 guardrail。

定义如下：

- 目标：判断工具动作是否允许执行。
- 挂点：`BeforeTool`。
- 决策对象：当前工具调用。
- 构造方式：`approval.New(opts...)`。
- reviewer 注入：`approval.WithReviewer(reviewer)`。

规则如下：

- 只有存在 `ToolPolicyRequireApproval` 路径时 reviewer 才必填。
- 纯静态策略模式可以不注入 reviewer。
- `approval` 只负责“这个工具动作是否允许执行”，不负责文本内容改写。

### 5.2 Secret

`secret` 负责防止凭据、令牌和私钥进入模型或离开模型。

定义如下：

- 挂点：`BeforeModel`、`AfterModel`。
- 默认检测：规则检测。
- 可选增强：模型 reviewer。
- review 链：规则检测器 -> 模型 reviewer。

规则如下：

- 规则检测器始终开启。
- 模型 reviewer 仅在 `secret.WithReviewer(...)` 显式注入时参与。
- 模型 reviewer 读取规则检测器改写后的文本。
- 默认动作为 `transform`。

内置 pattern 至少包含：

- OpenAI key。
- AWS access key。
- Bearer token。
- GitHub token。
- `-----BEGIN ... PRIVATE KEY-----`。

### 5.3 Prompt Injection

`promptinjection` 负责识别覆盖系统约束、绕过安全策略、探测系统提示、伪造高优先级指令和诱导错误用工具的输入。

定义如下：

- 挂点：`BeforeModel`。
- review 方式：classifier reviewer。
- reviewer 注入：`promptinjection.WithReviewer(...)`。

规则如下：

- reviewer 为必填依赖。
- 默认动作为 `block`。

分类结果至少包含：

- `system_override`
- `policy_bypass`
- `prompt_exfiltration`
- `role_hijack`
- `tool_misuse_induction`

### 5.4 Unsafe Intent

`unsafeintent` 负责识别明显高风险或不允许的意图。

定义如下：

- 挂点：`BeforeModel`。
- review 方式：classifier reviewer。
- reviewer 注入：`unsafeintent.WithReviewer(...)`。

规则如下：

- reviewer 为必填依赖。
- 默认动作为 `block`。

## 6. 职责边界

guardrail 内部的职责边界固定如下：

| Capability | 负责的问题 |
| --- | --- |
| `approval` | 这个工具动作是否允许执行。 |
| `secret` | 这段内容是否包含凭据或密钥，是否需要改写或阻断。 |
| `promptinjection` | 这段输入是否在尝试覆盖约束、绕过策略或诱导错误行为。 |
| `unsafeintent` | 这段输入是否表达了明显高风险或不允许的意图。 |

边界规则只有一条：`approval` 判断动作许可，其他 capability 判断内容流转与文本改写。
