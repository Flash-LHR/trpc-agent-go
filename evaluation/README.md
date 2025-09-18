# tRPC-Agent-Go Evaluation 设计文档

tRPC-Agent-Go 评估功能用于测试和评估 Agent 性能，支持多种评估器，适用于 Agent 单元测试/集成测试场景。

## 目标

- **标准化评估流程**：提供标准的评估接口、评估集和评估结果定义
- **多样化评估指标**：支持工具轨迹、响应质量、性能等多维度评估
- **可扩展性**：支持自定义评估器、评估集管理器、评估结果管理器、评估服务

## 业界实现

### ADK

ADK 评估主要关注以下指标：

- **最终响应的质量**：支持 ROUGE 文本相似度和 LLM Judge 两种评估方式
- **工具调用轨迹**：衡量工具轨迹匹配度，支持多种匹配策略
- **安全性**：基于 VertexAI 的安全性评估

ADK 支持三种评估方式来适应不同的开发场景：

1. 通过 Web UI 进行交互式评估和调试
2. 通过 pytest 集成到现有测试流程
3. 通过 CLI 命令实现自动化评估。

数据格式方面，ADK 使用 `.test.json` 文件存储单个测试用例，`.evalset.json` 文件管理批量评估集，并通过 `test_config.json` 配置评估标准，测试结果存储在 `.evalset_result.json`。

ADK-Web 支持将对话导出为测试用例，支持测试用例编辑、评估运行以及评估结果查看的可视化，极大降低了评估的复杂性。

**示例：**

```python
from google.adk.evaluation.agent_evaluator import AgentEvaluator
import pytest

@pytest.mark.asyncio
async def test_with_single_test_file():
    """Test the agent's basic ability via a session file."""
    await AgentEvaluator.evaluate(
        agent_module="home_automation_agent",
        eval_dataset_file_path_or_dir="tests/integration/fixture/home_automation_agent/simple_test.test.json",
    )
```

### Agno

Agno 评估主要关注以下指标：

- **准确性（Accuracy）**：使用 LLM-as-a-judge 模式评估响应的完整性、正确性和准确性
- **性能（Performance）**：测量 Agent 的响应延迟和内存占用
- **可靠性（Reliability）**：验证 Agent 是否执行了预期的工具调用

**示例：**

```python
from typing import Optional

from agno.agent import Agent
from agno.eval.reliability import ReliabilityEval, ReliabilityResult
from agno.tools.calculator import CalculatorTools
from agno.models.openai import OpenAIChat
from agno.run.response import RunResponse

def multiply_and_exponentiate():
    agent = Agent(
        model=OpenAIChat(id="gpt-4o-mini"),
        tools=[CalculatorTools(add=True, multiply=True, exponentiate=True)],
    )
    response: RunResponse = agent.run("What is 10*5 then to the power of 2? do it step by step")
    evaluation = ReliabilityEval(
        agent_response=response,
        expected_tool_calls=["multiply", "exponentiate"],
    )
    result: Optional[ReliabilityResult] = evaluation.run(print_results=True)
    result.assert_passed()


if __name__ == "__main__":
    multiply_and_exponentiate()
```

## 设计方案

### 组织结构

```
evaluation/
├── evalset/              # 评估数据集管理
│   ├── evalcase.go       # 评估用例定义
│   ├── evalset.go        # 评估集定义、评估集管理器接口定义
│   ├── local/            # 评估集管理器的本地文件实现
│   └── inmemory/         # 评估集管理器的内存实现
├── evalresult/           # 评估结果
│   ├── evalresult.go     # 评估结果定义、评估结果管理器接口定义
│   ├── local/            # 评估结果管理器的本地文件实现
│   └── inmemory/         # 评估结果管理器的内存实现
├── evaluator/            # 评估器
│   ├── evaluator.go      # 评估器接口定义
│   ├── registry.go       # 评估器注册
│   ├── response/         # 响应质量评估器
│   └── tooltrajectory/   # 工具轨迹评估器
├── metric/               # 评估指标
│   ├── metric.go         # 指标类型和配置
├── service/              # 评估服务
│   ├── service.go        # 评估服务接口定义
│   └── local/            # 本地评估服务实现
├── evaluation.go         # Agent 评估器 - 用户入口
```

### 架构设计

评估功能采用分层架构，从下到上分为：

```
┌─────────────────────────────────────┐
│           AgentEvaluator            │  ← 用户入口层
├─────────────────────────────────────┤
│         EvaluationService           │  ← 评估服务层
├─────────────────────────────────────┤
│    Evaluator Registry & Metrics     │  ← 评估器层
├─────────────────────────────────────┤
│   EvalSet Manager & Result Manager  │  ← 数据管理层
└─────────────────────────────────────┘
```

**数据流向：**
1. **输入**：Agent + EvalSet → **推理阶段** → InferenceResult
2. **评估**：InferenceResult + Metrics → **评估阶段** → EvalCaseResult  
3. **输出**：EvalCaseResult → **汇总阶段** → EvaluationResult

#### evalset - 评估数据集管理

负责管理评估用例和评估集的存储、读取。

注：为使用 ADK Web 可视化编辑测试集的能力，gotag 需要与 ADK 字段对齐。

**核心类型（与 ADK Pydantic 定义对齐）:**

```go
// EvalCase 表示单个评估用例
type EvalCase struct {
    EvalID            string        `json:"evalId"`
    Conversation      []Invocation  `json:"conversation"`
    SessionInput      *SessionInput `json:"sessionInput,omitempty"`
    CreationTimestamp EpochTime     `json:"creationTimestamp"`
}

// Invocation 表示对话中的单次交互，存储多模态内容。
type Invocation struct {
    InvocationID      string            `json:"invocationId,omitempty"`
    UserContent       *Content          `json:"userContent"`
    FinalResponse     *Content          `json:"finalResponse,omitempty"`
    IntermediateData  *IntermediateData `json:"intermediateData,omitempty"`
    CreationTimestamp EpochTime         `json:"creationTimestamp"`
}

type Content struct {
    Role  string `json:"role"`
    Parts []Part `json:"parts,omitempty"`
}

type Part struct {
    Text string `json:"text,omitempty"`
    // 后续可扩展 image/audio/file 等多模态字段。
}

// IntermediateData 捕获推理轨迹，便于工具调用评估。
type IntermediateData struct {
    ToolUses              []FunctionCall        `json:"toolUses,omitempty"`
    ToolResponses         []ToolResponse        `json:"toolResponses,omitempty"`
    IntermediateResponses []IntermediateMessage `json:"intermediateResponses,omitempty"`
}

type FunctionCall struct {
    ID   string                 `json:"id,omitempty"`
    Name string                 `json:"name"`
    Args map[string]interface{} `json:"args,omitempty"`
}

type ToolResponse struct {
    Name         string                 `json:"name"`
    Response     map[string]interface{} `json:"response"`
    ID           string                 `json:"id,omitempty"`
    WillContinue *bool                  `json:"willContinue,omitempty"`
    Scheduling   *string                `json:"scheduling,omitempty"`
}

type IntermediateMessage struct {
    Author string `json:"author"`
    Parts  []Part `json:"parts,omitempty"`
}

// SessionInput 表示 Session 初始化信息，兼容 ADK SessionInput。
type SessionInput struct {
    UserID string                 `json:"userId"`
    State  map[string]interface{} `json:"state,omitempty"`
}

// EpochTime 以 ADK 相同的秒级浮点序列化，便于互导。
type EpochTime struct{ time.Time }

// EvalSet 表示评估集
type EvalSet struct {
    EvalSetID         string     `json:"eval_set_id"`
    Name              string     `json:"name,omitempty"`
    Description       string     `json:"description,omitempty"`
    EvalCases         []EvalCase `json:"eval_cases"`
    CreationTimestamp time.Time  `json:"creation_timestamp"`
}
```

**管理器接口:**

```go
type Manager interface {
    Save(ctx context.Context, evalSet *EvalSet) error
    Get(ctx context.Context, evalSetID string) (*EvalSet, error)
    List(ctx context.Context) ([]*EvalSet, error)
    Delete(ctx context.Context, evalSetID string) error
}
```

**实现方式:**

- `inmemory` - 内存存储，适用于测试和小规模数据
- `local` - 本地文件存储，便于维护与分发

#### evalresult - 评估结果管理

负责管理评估结果的存储、读取和查询。

**核心类型:**

// EvalStatus 定义在 evalresult 包中，状态含 Passed/Failed/NotEvaluated。
```go
// EvalCaseResult 表示单个评估用例的结果
type EvalCaseResult struct {
    EvalSetID                     string                                `json:"eval_set_id"`
    EvalCaseID                    string                                `json:"eval_id"`
    FinalEvalStatus               EvalStatus                            `json:"final_eval_status"`
    OverallEvalMetricResults      []EvalMetricResult                    `json:"overall_eval_metric_results"`
    EvalMetricResultPerInvocation []EvalMetricResultPerInvocation       `json:"eval_metric_result_per_invocation"`
    SessionID                     string                                `json:"session_id"`
    UserID                        string                                `json:"user_id,omitempty"`
}

// EvalMetricResult 表示单个指标的结果
type EvalMetricResult struct {
    MetricName string                 `json:"metric_name"`
    Score      *float64               `json:"score,omitempty"`
    Status     EvalStatus             `json:"status"`
    Threshold  float64                `json:"threshold"`
    Details    map[string]interface{} `json:"details,omitempty"`
}

// EvalMetricResultPerInvocation 表示每轮对话的指标结果
type EvalMetricResultPerInvocation struct {
    InvocationIndex int               `json:"invocation_index"`
    MetricResults   []EvalMetricResult `json:"metric_results"`
}

// EvalSetResult 表示整个评估集的结果
type EvalSetResult struct {
    EvalSetResultID   string           `json:"eval_set_result_id"`
    EvalSetResultName string           `json:"eval_set_result_name,omitempty"`
    EvalSetID         string           `json:"eval_set_id"`
    EvalCaseResults   []EvalCaseResult `json:"eval_case_results"`
    CreationTimestamp float64          `json:"creation_timestamp"`
}
```

**管理器接口:**

```go
type Manager interface {
    Save(ctx context.Context, result *EvalSetResult) error
    Get(ctx context.Context, evalSetResultID string) (*EvalSetResult, error)
    List(ctx context.Context) ([]*EvalSetResult, error)
}
```

**实现方式:**

- `inmemory.Manager` - 内存存储，适用于测试和快速查询
- `local.Manager` - 本地文件存储，适用于结果持久化和历史记录

#### evaluator - 评估器

负责具体的评估逻辑实现，比较实际结果与期望结果。

**判定机制说明：**评估器通常会为每个指标计算一个分数（score），再与预先配置的阈值（threshold）比较；当指标达到阈值时，判定为通过（Passed），否则为不通过（Failed）。

**评估器接口:**

```go
// Go 侧接口直接复用 evaluator 包：
type Evaluator interface {
    Evaluate(ctx context.Context, actual, expected []evalset.Invocation) (*EvaluationResult, error)
    Name() string
    Description() string
}

type EvaluationResult struct {
    OverallScore         *float64               `json:"overall_score,omitempty"`
    OverallStatus        evalresult.EvalStatus  `json:"overall_status"`
    PerInvocationResults []PerInvocationResult  `json:"per_invocation_results"`
}

type PerInvocationResult struct {
    ActualInvocation   evalset.Invocation `json:"actual_invocation"`
    ExpectedInvocation evalset.Invocation `json:"expected_invocation"`
    Score              *float64           `json:"score,omitempty"`
    Status             evalresult.EvalStatus `json:"status"`
}
```

`InferenceResult` 持有 `EvalSetID/EvalCaseID`，评估器通过 `EvalSet.Manager.GetCase` 拉取期望值，保证 actual 与 expected 对齐；该流程与 ADK `LocalEvalService._evaluate_single_inference_result` 一致。

**常用的评估器实现:**

- `tooltrajectory` - 工具轨迹评估器，评估 Agent 工具调用的准确性
- `LLMJudgeEvaluator` - LLM评 判器，使用大语言模型评估响应质量
- `RougeEvaluator` - ROUGE 评估器，计算文本相似度分数

**注册器:**

```go
type Registry struct {
    evaluators map[string]Evaluator
}

func (r *Registry) Register(name string, evaluator Evaluator) error
func (r *Registry) Get(name string) (Evaluator, error)
func (r *Registry) List() []string
```

#### metric - 评估指标

定义各种评估指标的配置和结果类型。

**指标配置:**
```go
type EvalMetric struct {
    MetricName        string                 `json:"metric_name"`
    Threshold         float64                `json:"threshold"`
    JudgeModelOptions *JudgeModelOptions     `json:"judge_model_options,omitempty"`
    Config            map[string]interface{} `json:"config,omitempty"`
}

type JudgeModelOptions struct {
    JudgeModel   string   `json:"judge_model"`
    Temperature  *float64 `json:"temperature,omitempty"`
    MaxTokens    *int     `json:"max_tokens,omitempty"`
    NumSamples   *int     `json:"num_samples,omitempty"`
    CustomPrompt string   `json:"custom_prompt,omitempty"`
}
```

指标计算产物复用前文 `evalresult.EvalMetricResult`/`EvalMetricResultPerInvocation`，输出结构与 ADK 一致。

### service - 评估服务

提供高级的评估服务接口，协调各个组件完成完整的评估流程。

**服务接口:**

```go
type EvaluationService interface {
    // 推理阶段：从 EvalSet 拿到 expected，对 Runner 触发调用，返回有序 channel。
    PerformInference(ctx context.Context, request *InferenceRequest) (<-chan *InferenceResult, error)

    // 评估阶段：结合推理结果与期望值，返回 EvalCaseResult 流。
    Evaluate(ctx context.Context, request *EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error)
}
```

**请求与结果类型（与 ADK BaseEvalService 对齐）:**

```go
type InferenceConfig struct {
    AgentConfig map[string]interface{} `json:"agent_config"`
    MaxTokens   int                    `json:"max_tokens"`
    Temperature float64                `json:"temperature"`
}

type InferenceRequest struct {
    AppName         string          `json:"app_name"`
    EvalSetID       string          `json:"eval_set_id"`
    EvalCaseIDs     []string        `json:"eval_case_ids,omitempty"`
    InferenceConfig InferenceConfig `json:"inference_config"`
}

type InferenceResult struct {
    AppName    string               `json:"app_name"`
    EvalSetID  string               `json:"eval_set_id"`
    EvalCaseID string               `json:"eval_case_id"`
    Inferences []evalset.Invocation `json:"inferences,omitempty"`
    SessionID  string               `json:"session_id,omitempty"`
    Status     InferenceStatus      `json:"status"`
    ErrorMessage string             `json:"error_message,omitempty"`
}

type InferenceStatus int

const (
    InferenceStatusUnknown InferenceStatus = iota
    InferenceStatusSuccess
    InferenceStatusFailure
)

type EvaluateConfig struct {
    Metrics            []metric.EvalMetric `json:"metrics"`
    InferenceConfig    InferenceConfig     `json:"inference_config"`
    ConcurrencyConfig  ConcurrencyConfig   `json:"concurrency_config"`
}

type ConcurrencyConfig struct {
    MaxInferenceConcurrency int `json:"max_inference_concurrency"`
    MaxEvalConcurrency      int `json:"max_eval_concurrency"`
}

type EvaluateRequest struct {
    InferenceResults []InferenceResult `json:"inference_results"`
    EvaluateConfig   EvaluateConfig    `json:"evaluate_config"`
}
```

`InferenceResult` 中的标识确保 Local/远端 Service 可以回查 EvalSet，复用期望答案、会话元信息等，消除此前文档里“expected 来源不明”的问题。

#### AgentEvaluator - 用户入口

**AgentEvaluator** 是用户入口

**核心接口:**

```go
type AgentEvaluator struct {
    service  service.EvaluationService
    registry *evaluator.Registry
    cfg      AgentEvaluatorConfig
}

func NewAgentEvaluator(opts ...Option) *AgentEvaluator

func (a *AgentEvaluator) Evaluate(ctx context.Context, runner runner.Runner) (*EvaluationResult, error)
```

通过 Option 模式注入 `EvaluationService`、`Evaluator.Registry`、默认指标阈值等依赖；若未显式提供，默认使用 `service/local.New()` 与内置注册表，保持与 ADK `AgentEvaluator` 类似的开箱体验。

**评估结果:**

```go
type EvaluationResult struct {
    OverallStatus evalresult.EvalStatus      `json:"overall_status"`
    MetricResults map[string]MetricSummary  `json:"metric_results"`
    TotalCases    int                       `json:"total_cases"`
    ExecutionTime time.Duration             `json:"execution_time"`
}

type MetricSummary struct {
    MetricName   string                `json:"metric_name"`
    OverallScore *float64              `json:"overall_score,omitempty"`
    Threshold    float64               `json:"threshold"`
    Status       evalresult.EvalStatus `json:"status"`
    NumSamples   int                   `json:"num_samples"`
}
```

### 使用示例

```go
package main

import (
    "context"
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/evaluation"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent"
)

func main() {
    ctx := context.Background()
    
    // 1. 创建 Agent 实例
    myAgent := &MyAgent{} // 实现 agent.Agent 接口
    
    // 2. 创建 Runner
    appRunner := runner.NewRunner("my-app", myAgent)
    
    // 3. 创建 AgentEvaluator
    evaluator := evaluation.NewAgentEvaluator()
    
    // 4. 运行评估
    result, err := evaluator.Evaluate(ctx, appRunner)
    if err != nil {
        log.Fatal("评估失败:", err)
    }
    
    // 5. 检查结果
    if result.OverallStatus == evaluation.EvalStatusPassed {
        fmt.Printf("🎉 评估通过! 处理了 %d 个用例，耗时 %v\n", 
            result.TotalCases, result.ExecutionTime)
    } else {
        fmt.Printf("❌ 评估失败! 请查看详细结果\n")
        
        // 也可以在测试中使用断言
        // assert.Equal(t, evaluation.EvalStatusPassed, result.OverallStatus, "Agent evaluation failed")
    }
}

// MyAgent 是 Agent 实现示例
type MyAgent struct {
    // 你的 Agent 字段
}
```
