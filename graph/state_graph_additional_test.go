//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// captureModel captures the last request passed to GenerateContent.
type captureModel struct{ lastReq *model.Request }

func (c *captureModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	c.lastReq = req
	ch := make(chan *model.Response, 1)
	// Mark Done=true to avoid emitting streaming response events and keep focus on model start/complete events.
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	close(ch)
	return ch, nil
}

func (c *captureModel) Info() model.Info { return model.Info{Name: "capture"} }

// echoTool is a minimal CallableTool used for ToolSet injection tests.
type echoTool struct{ name string }

func (e *echoTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: e.name} }
func (e *echoTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return map[string]any{"ok": true}, nil
}

// simpleToolSet returns a fixed set of tools.
type simpleToolSet struct {
	name string
}

func (s *simpleToolSet) Tools(ctx context.Context) []tool.Tool {
	return []tool.Tool{&echoTool{name: "echo"}}
}
func (s *simpleToolSet) Close() error { return nil }
func (s *simpleToolSet) Name() string { return s.name }

// stubAgent is a minimal agent implementation used for subgraph tests.
type stubAgent struct {
	name string
}

type iterErrorModel struct {
	err error
}

func (m *iterErrorModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *iterErrorModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return nil, m.err
}

func (m *iterErrorModel) Info() model.Info {
	return model.Info{Name: "iter-error-model"}
}

type noResponseModel struct{}

func (m *noResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *noResponseModel) Info() model.Info {
	return model.Info{Name: "no-response-model"}
}

func (a *stubAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, nil
}

func (a *stubAgent) Tools() []tool.Tool { return nil }

func (a *stubAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *stubAgent) SubAgents() []agent.Agent { return nil }

func (a *stubAgent) FindSubAgent(name string) agent.Agent { return nil }

func TestAddLLMNode_ToolSetInjection_And_ModelEventInput(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	// Inject toolset via node options
	sg.AddLLMNode(
		"llm",
		cm,
		"inst",
		nil,
		WithToolSets([]tool.ToolSet{&simpleToolSet{"simple"}}),
	)
	// Ensure node type is LLM
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	require.Equal(t, NodeTypeLLM, n.Type)

	// Build a minimal exec context to receive events
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "llm", StateKeyUserInput: "hi"}

	// Call the node function directly
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	// Verify model received tools injected from ToolSet
	require.NotNil(t, cm.lastReq)
	require.Contains(t, cm.lastReq.Tools, "simple_echo") // Tool name is now namespaced with toolset name

	// Drain available events and verify model start/complete include input built from instruction+user_input
	var modelInputs []string
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					if meta.Input != "" {
						modelInputs = append(modelInputs, meta.Input)
					}
				}
			}
		default:
			goto DONE
		}
	}
DONE:
	// Expect at least one model event carrying the combined input string
	require.NotEmpty(t, modelInputs)
	found := false
	for _, in := range modelInputs {
		if in == "inst\n\nhi" || (len(in) >= 2 && in[0:4] == "inst") {
			found = true
			break
		}
	}
	require.True(t, found, "expected model event input to contain instruction and user input: %v", modelInputs)
}

func TestAddLLMNode_GenerationConfigOption(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)

	temp := 0.2
	maxTok := 128

	cfg := model.GenerationConfig{
		Stream:      false,
		Temperature: &temp,
		MaxTokens:   &maxTok,
		Stop:        []string{"END"},
	}

	sg.AddLLMNode("llm", cm, "inst", nil, WithGenerationConfig(cfg))

	// Sanity: node exists
	n := sg.graph.nodes["llm"]
	require.NotNil(t, n)

	// Execute node
	_, err := n.Function(context.Background(), State{StateKeyUserInput: "hi"})
	require.NoError(t, err)

	// Verify request picked up generation config
	require.NotNil(t, cm.lastReq)
	got := cm.lastReq.GenerationConfig
	require.Equal(t, cfg.Stream, got.Stream)
	if cfg.Temperature != nil {
		require.NotNil(t, got.Temperature)
		require.InDelta(t, *cfg.Temperature, *got.Temperature, 1e-9)
	}
	if cfg.MaxTokens != nil {
		require.NotNil(t, got.MaxTokens)
		require.Equal(t, *cfg.MaxTokens, *got.MaxTokens)
	}
	require.Equal(t, cfg.Stop, got.Stop)

}

func TestRunModelStream_IterModelError(t *testing.T) {
	iterErr := errors.New("iter boom")

	_, _, err := runModelStream(
		context.Background(),
		nil,
		nil,
		&iterErrorModel{err: iterErr},
		&model.Request{},
	)
	require.ErrorIs(t, err, iterErr)
	require.ErrorContains(t, err, "failed to generate content")
}

func TestEmitFastModelResponseEvent_DisablesPartialMetadata(t *testing.T) {
	t.Run("partial response omits generated ID and timestamp", func(t *testing.T) {
		ch := make(chan *event.Event, 1)
		resp := &model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("partial")},
			},
		}

		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast")),
			modelExecutionConfig{
				InvocationID: "inv-fast",
				EventChan:    ch,
				Span:         noop.Span{},
			},
			resp,
			"llm",
			true,
			true,
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, ev)
		require.Empty(t, ev.ID)
		require.True(t, ev.Timestamp.IsZero())
		require.Same(t, ev, <-ch)
	})

	t.Run("response error is surfaced", func(t *testing.T) {
		resp := &model.Response{
			Error: &model.ResponseError{Message: "api boom"},
		}

		ev, err := emitFastModelResponseEvent(
			context.Background(),
			agent.NewInvocation(agent.WithInvocationID("inv-fast-err")),
			modelExecutionConfig{
				InvocationID: "inv-fast-err",
				Span:         noop.Span{},
			},
			resp,
			"llm",
			false,
			false,
			&event.Event{},
		)
		require.ErrorContains(t, err, "model API error: api boom")
		require.NotNil(t, ev)
		require.Same(t, resp, ev.Response)
	})
}

func TestModelResponseProcessorConsume_FastPathSeq(t *testing.T) {
	ch := make(chan *event.Event, 2)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-fast-seq"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking:  true,
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	var runErr error
	processor := newModelResponseProcessor(
		context.Background(),
		modelExecutionConfig{
			Invocation:   invocation,
			InvocationID: invocation.InvocationID,
			EventChan:    ch,
			Request:      &model.Request{},
			Span:         noop.Span{},
			NodeID:       "llm",
		},
		invocation,
		&runErr,
	)
	require.True(t, processor.fastResponsePath)

	err := processor.consume(modelResponseStream{
		Seq: func(yield func(*model.Response) bool) {
			if !yield(nil) {
				return
			}
			yield(&model.Response{
				IsPartial: true,
				Choices: []model.Choice{
					{Message: model.NewAssistantMessage("partial")},
				},
				Timestamp: time.Now(),
			})
		},
	})
	require.NoError(t, err)
	require.NotNil(t, processor.lastEvent)
	require.NotNil(t, processor.finalResponse)
	require.Same(t, processor.lastEvent, <-ch)
}

func TestNewModelResponseProcessor_FastPathWhenOnlyOnePartialToggleIsDisabled(t *testing.T) {
	tests := []struct {
		name       string
		runOptions agent.RunOptions
	}{
		{
			name: "disable partial ids only",
			runOptions: agent.RunOptions{
				DisablePartialEventIDs: true,
			},
		},
		{
			name: "disable partial timestamps only",
			runOptions: agent.RunOptions{
				DisablePartialEventTimestamps: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation := agent.NewInvocation(
				agent.WithInvocationID("inv-fast-path"),
				agent.WithInvocationRunOptions(tt.runOptions),
			)
			var runErr error
			processor := newModelResponseProcessor(
				context.Background(),
				modelExecutionConfig{
					Invocation:   invocation,
					InvocationID: invocation.InvocationID,
					EventChan:    make(chan *event.Event, 1),
					Request:      &model.Request{},
					Span:         noop.Span{},
					NodeID:       "llm",
				},
				invocation,
				&runErr,
			)
			require.True(t, processor.fastResponsePath)
		})
	}
}

func TestNewModelResponseProcessor_FastPathWithBeforeModelCallbacksOnly(t *testing.T) {
	tests := []struct {
		name           string
		invocation     *agent.Invocation
		modelCallbacks *model.Callbacks
	}{
		{
			name: "local before model callbacks only",
			invocation: agent.NewInvocation(
				agent.WithInvocationID("inv-local-before-only"),
			),
			modelCallbacks: model.NewCallbacks().RegisterBeforeModel(
				func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					return &model.BeforeModelResult{}, nil
				},
			),
		},
		{
			name: "plugin before model callbacks only",
			invocation: agent.NewInvocation(
				agent.WithInvocationID("inv-plugin-before-only"),
				agent.WithInvocationPlugins(plugin.MustNewManager(&hookPlugin{
					name: "before-only",
					reg: func(r *plugin.Registry) {
						r.BeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
							return &model.BeforeModelResult{}, nil
						})
					},
				})),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runErr error
			processor := newModelResponseProcessor(
				context.Background(),
				modelExecutionConfig{
					Invocation:     tt.invocation,
					InvocationID:   tt.invocation.InvocationID,
					ModelCallbacks: tt.modelCallbacks,
					EventChan:      make(chan *event.Event, 1),
					Request:        &model.Request{},
					Span:           noop.Span{},
					NodeID:         "llm",
				},
				tt.invocation,
				&runErr,
			)
			require.True(t, processor.fastResponsePath)
		})
	}
}

func TestProcessModelResponse_DisablesPartialMetadataOnSlowPath(t *testing.T) {
	ch := make(chan *event.Event, 1)
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-slow-partial"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisablePartialEventIDs:        true,
			DisablePartialEventTimestamps: true,
		}),
	)
	var ctx context.Context = agent.NewInvocationContext(context.Background(), invocation)
	resp := &model.Response{
		IsPartial: true,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("partial")},
		},
	}
	ctx, ev, err := processModelResponse(ctx, modelResponseConfig{
		Response:     resp,
		EventChan:    ch,
		InvocationID: invocation.InvocationID,
		Request:      &model.Request{},
		Span:         noop.Span{},
		NodeID:       "llm",
	})
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.NotNil(t, ev)
	require.Empty(t, ev.ID)
	require.True(t, ev.Timestamp.IsZero())
	require.Same(t, ev, <-ch)
}

func TestExecuteModelAndProcessResponses_UsesInvocationFromCallbackContext(t *testing.T) {
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
		agent.WithInvocationRunOptions(agent.RunOptions{}),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableResponseUsageTracking: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	resp, err := executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		modelExecutionConfig{
			Invocation:     baseInvocation,
			ModelCallbacks: callbacks,
			LLMModel:       &captureModel{},
			Request:        &model.Request{},
			InvocationID:   baseInvocation.InvocationID,
			Span:           noop.Span{},
			NodeID:         "llm",
		},
	)
	require.NoError(t, err)
	finalResponse, ok := resp.(*model.Response)
	require.True(t, ok)
	require.Nil(t, finalResponse.Usage)
}

func TestAddLLMNode_SkipsModelCompleteEventWhenCallbackDisablesModelExecutionEvents(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode("llm", &captureModel{}, "inst", nil)
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	baseInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-base"),
	)
	updatedInvocation := agent.NewInvocation(
		agent.WithInvocationID("inv-updated"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableModelExecutionEvents: true,
		}),
	)
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				Context: agent.NewInvocationContext(ctx, updatedInvocation),
			}, nil
		},
	)
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{
		StateKeyExecContext:    exec,
		StateKeyCurrentNodeID:  "llm",
		StateKeyUserInput:      "hi",
		StateKeyModelCallbacks: callbacks,
	}
	_, err := n.Function(
		agent.NewInvocationContext(context.Background(), baseInvocation),
		state,
	)
	require.NoError(t, err)
	var phases []ModelExecutionPhase
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					phases = append(phases, meta.Phase)
				}
			}
		default:
			require.Equal(t, []ModelExecutionPhase{ModelExecutionPhaseStart}, phases)
			return
		}
	}
}

func TestExecuteModelAndProcessResponses_TracksFinalizeErrors(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	originalProvider := itelemetry.MeterProvider
	originalMeter := itelemetry.ChatMeter
	originalRequestCnt := itelemetry.ChatMetricTRPCAgentGoClientRequestCnt
	defer func() {
		itelemetry.MeterProvider = originalProvider
		itelemetry.ChatMeter = originalMeter
		itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
	}()
	itelemetry.MeterProvider = provider
	itelemetry.ChatMeter = provider.Meter(semconvmetrics.MeterNameChat)
	requestCnt, err := itelemetry.ChatMeter.Int64Counter("trpc_agent_go.client.request.cnt")
	require.NoError(t, err)
	itelemetry.ChatMetricTRPCAgentGoClientRequestCnt = requestCnt
	invocation := agent.NewInvocation(
		agent.WithInvocationID("inv-metrics"),
		agent.WithInvocationModel(&noResponseModel{}),
		agent.WithInvocationSession(&session.Session{ID: "sess-metrics"}),
	)
	_, err = executeModelAndProcessResponses(
		agent.NewInvocationContext(context.Background(), invocation),
		modelExecutionConfig{
			Invocation:   invocation,
			LLMModel:     invocation.Model,
			Request:      &model.Request{},
			InvocationID: invocation.InvocationID,
			SessionID:    invocation.Session.ID,
			Span:         noop.Span{},
			NodeID:       "llm",
		},
	)
	require.ErrorContains(t, err, errMsgNoModelResponse)
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.True(t, resourceMetricsContainAttribute(rm, itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
}

func resourceMetricsContainAttribute(rm metricdata.ResourceMetrics, key, value string) bool {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Sum[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[int64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					if attributeSetContains(point.Attributes, key, value) {
						return true
					}
				}
			}
		}
	}
	return false
}

func attributeSetContains(set attribute.Set, key, value string) bool {
	for _, kv := range set.ToSlice() {
		if string(kv.Key) == key && kv.Value.AsString() == value {
			return true
		}
	}
	return false
}

func TestBuilderOptions_Destinations_And_Callbacks(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())

	before1 := func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }
	after1 := func(ctx context.Context, cb *NodeCallbackContext, st State, result any, nodeErr error) (any, error) {
		return nil, nil
	}
	onErr1 := func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}

	cbs := NewNodeCallbacks().
		RegisterBeforeNode(before1).
		RegisterAfterNode(after1).
		RegisterOnNodeError(onErr1)

	// Add node with destinations and per-node callbacks
	// Also add the declared destination node "A" so validation succeeds.
	sg.AddNode("A", func(ctx context.Context, st State) (any, error) { return st, nil })
	sg.AddNode("n", func(ctx context.Context, st State) (any, error) { return st, nil },
		WithDestinations(map[string]string{"A": "toA"}),
		WithNodeCallbacks(cbs),
		WithPreNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }),
		WithPostNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, result any, err error) (any, error) {
			return nil, nil
		}),
		WithNodeErrorCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}),
		WithAgentNodeEventCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, e *event.Event) {}),
	)

	// Compile to validate graph
	_, err := sg.SetEntryPoint("n").SetFinishPoint("n").Compile()
	require.NoError(t, err)

	node := sg.graph.nodes["n"]
	require.NotNil(t, node)
	require.Contains(t, node.destinations, "A")
	require.NotNil(t, node.callbacks)
	require.Len(t, node.callbacks.BeforeNode, 2)
	require.Len(t, node.callbacks.AfterNode, 2)
	require.Len(t, node.callbacks.OnNodeError, 2)
	require.Len(t, node.callbacks.AgentEvent, 1)
}

func TestAddEdge_PregelSetup(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }
	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.AddEdge("A", "B")
	_, err := sg.SetEntryPoint("A").SetFinishPoint("B").Compile()
	require.NoError(t, err)

	// Channel mapping should include branch:to:B -> [B]
	triggers := sg.graph.getTriggerToNodes()
	require.Contains(t, triggers, "branch:to:B")
	require.Contains(t, triggers["branch:to:B"], "B")

	// Writers on A should include the branch channel
	nodeA := sg.graph.nodes["A"]
	found := false
	for _, w := range nodeA.writers {
		if w.Channel == "branch:to:B" {
			found = true
			break
		}
	}
	require.True(t, found, "expected writer to branch:to:B on node A")
}

func TestAddToolsAndAgentNode_Types(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{"echo": &echoTool{name: "echo"}})
	sg.AddAgentNode("agent")
	require.Equal(t, NodeTypeTool, sg.graph.nodes["tools"].Type)
	require.Equal(t, NodeTypeAgent, sg.graph.nodes["agent"].Type)
}

func TestLLMNode_PlaceholdersInjected_FromSessionState(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	instr := "Hello {research_topics}. {user:topics?} - {app:banner?}"
	sg.AddLLMNode("llm", cm, instr, nil)

	// Build a minimal exec context and session with state for placeholder injection.
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-ph", EventChan: ch}
	sess := &session.Session{ID: "s1", State: session.StateMap{
		"research_topics": []byte("AI"),
		"user:topics":     []byte("DL"),
		"app:banner":      []byte("Banner"),
	}}
	state := State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeySession:       sess,
		StateKeyUserInput:     "ask",
	}

	n := sg.graph.nodes["llm"]
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	// Verify request has system message with injected content.
	require.NotNil(t, cm.lastReq)
	require.GreaterOrEqual(t, len(cm.lastReq.Messages), 1)
	sys := cm.lastReq.Messages[0]
	require.Equal(t, model.RoleSystem, sys.Role)
	require.Contains(t, sys.Content, "AI")
	require.Contains(t, sys.Content, "DL")
	require.Contains(t, sys.Content, "Banner")
	require.NotContains(t, sys.Content, "{research_topics}")
	require.NotContains(t, sys.Content, "{user:topics}")
	require.NotContains(t, sys.Content, "{app:banner}")

	// Drain model events and verify model input uses injected instruction.
	var inputs []string
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					if meta.Input != "" {
						inputs = append(inputs, meta.Input)
					}
				}
			}
		default:
			goto DONE
		}
	}
DONE:
	require.NotEmpty(t, inputs)
	found := false
	for _, in := range inputs {
		if in == "Hello AI. DL - Banner\n\nask" || in == "Hello AI. DL - Banner" {
			found = true
			break
		}
	}
	require.True(t, found, "model input should contain injected instruction: %v", inputs)
}

func TestLLMNode_PlaceholdersOptionalMissing(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	instr := "Show {research_topics} {user:topics?} {app:banner?}"
	sg.AddLLMNode("llm", cm, instr, nil)

	ch := make(chan *event.Event, 4)
	exec := &ExecutionContext{InvocationID: "inv-ph2", EventChan: ch}
	// Only provide research_topics; optional prefixed keys are omitted.
	sess := &session.Session{ID: "s2", State: session.StateMap{
		"research_topics": []byte("AI"),
	}}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "llm", StateKeySession: sess}

	n := sg.graph.nodes["llm"]
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	require.NotNil(t, cm.lastReq)
	require.GreaterOrEqual(t, len(cm.lastReq.Messages), 1)
	sys := cm.lastReq.Messages[0]
	require.Equal(t, model.RoleSystem, sys.Role)
	// research_topics is injected; optional ones are blanked out (no braces remain)
	require.Contains(t, sys.Content, "AI")
	require.NotContains(t, sys.Content, "{user:topics?")
	require.NotContains(t, sys.Content, "{app:banner?")
}

// Verify StateSchema.ApplyUpdate skips unknown internal keys while still
// applying other unknown keys using default override behavior.
func TestStateSchema_ApplyUpdate_SkipsInternalUnknownKeys(t *testing.T) {
	schema := NewStateSchema().
		AddField("x", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer})

	current := State{"x": 1}
	update := State{
		StateKeyExecContext: map[string]any{"should": "skip"},
		"y":                 2,
	}

	result := schema.ApplyUpdate(current, update)

	// Internal key should be ignored entirely.
	require.NotContains(t, result, StateKeyExecContext)
	// Unknown non-internal key should be applied with default override.
	require.Equal(t, 2, result["y"])
	// Existing schema field remains unless overridden.
	require.Equal(t, 1, result["x"])
}

func TestBuildAgentInvocationWithStateAndScope_ParentAndScope(t *testing.T) {
	parent := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("root"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"scope",
	)

	key := inv.GetEventFilterKey()
	parts := strings.Split(key, event.FilterKeyDelimiter)
	// FilterKey is now stable without UUID: "root/scope"
	require.Len(t, parts, 2)
	require.Equal(t, "root", parts[0])
	require.Equal(t, "scope", parts[1])
}

func TestBuildAgentInvocationWithStateAndScope_ParentNoScope(t *testing.T) {
	parent := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("root"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"",
	)

	key := inv.GetEventFilterKey()
	parts := strings.Split(key, event.FilterKeyDelimiter)
	// FilterKey is now stable without UUID: "root/child"
	require.Len(t, parts, 2)
	require.Equal(t, "root", parts[0])
	require.Equal(t, "child", parts[1])
}

func TestBuildAgentInvocationWithStateAndScope_NoParentKey(t *testing.T) {
	// Parent invocation without an explicit filter key.
	parent := &agent.Invocation{}
	ctx := agent.NewInvocationContext(context.Background(), parent)

	target := &stubAgent{name: "child"}
	inv := buildAgentInvocationWithStateAndScope(
		ctx,
		State{},
		State{},
		target,
		"",
		"scope",
	)

	key := inv.GetEventFilterKey()
	// FilterKey is now stable without UUID: just "scope"
	require.Equal(t, "scope", key)
}
