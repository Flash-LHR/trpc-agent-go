//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates intercepting part of LLM tool calls in a custom node while still using
// AddToolsConditionalEdges to route tool flows.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	nodeLLM         = "llm"
	nodeToolHandler = "tool_handler"
	nodeFinal       = "final"
)

var (
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/agui", "HTTP path")
	modelName = flag.String("model", "deepseek-chat", "LLM model to use")
)

func main() {
	flag.Parse()
	g, err := buildGraph()
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}
	ga, err := graphagent.New(
		"tool-intercept-demo",
		g,
		graphagent.WithDescription("Intercept some tool calls and delegate the rest."),
	)
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}
	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(ga.Info().Name, ga, runner.WithSessionService(sessionService))
	defer r.Close()
	server, err := agui.New(
		r,
		agui.WithPath(*path),
		agui.WithAppName(ga.Info().Name),
		agui.WithSessionService(sessionService),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath("/history"),
	)
	if err != nil {
		log.Fatalf("Failed to create AG-UI server: %v", err)
	}
	log.Infof("Tool intercept demo started at http://%s%s", *address, *path)
	log.Info("Tip: say anything and the LLM will call the calculator tool.")
	log.Info("Flow: tool_handler intercepts calculator calls, runs them inline, and emits tool events.")
	if err = http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

func buildGraph() (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	modelInstance := openai.New(*modelName)
	sg.AddLLMNode(
		nodeLLM,
		modelInstance,
		"Always use the calculator tool to solve arithmetic between two numbers. Return tool calls instead of direct answers.",
		map[string]tool.Tool{"calculator": newCalculatorTool()},
		graph.WithGenerationConfig(model.GenerationConfig{
			Stream:      true,
			Temperature: floatPtr(0.7),
		}),
	)
	sg.AddNode(nodeToolHandler, toolHandler)
	sg.AddNode(nodeFinal, finalNode)
	sg.SetEntryPoint(nodeLLM)
	sg.AddToolsConditionalEdges(nodeLLM, nodeToolHandler, nodeFinal)
	sg.AddEdge(nodeToolHandler, nodeLLM)
	sg.SetFinishPoint(nodeFinal)
	return sg.Compile()
}

// toolHandler intercepts calculator tool calls and handles them inline.
func toolHandler(ctx context.Context, state graph.State) (any, error) {
	msgs, ok := state[graph.StateKeyMessages].([]model.Message)
	if !ok || len(msgs) == 0 {
		return nil, fmt.Errorf("messages missing")
	}
	asst := msgs[len(msgs)-1]
	if asst.Role != model.RoleAssistant || len(asst.ToolCalls) == 0 {
		return nil, fmt.Errorf("no assistant tool calls to handle")
	}
	callableCalc, ok := newCalculatorTool().(tool.CallableTool)
	if !ok {
		return nil, fmt.Errorf("calculator tool is not callable")
	}
	execCtx, ok := graph.GetStateValue[*graph.ExecutionContext](state, graph.StateKeyExecContext)
	if !ok {
		return nil, fmt.Errorf("execution context not found")
	}
	nodeID, ok := graph.GetStateValue[string](state, graph.StateKeyCurrentNodeID)
	if !ok {
		return nil, fmt.Errorf("current node ID not found")
	}
	responseID, ok := graph.GetStateValue[string](state, graph.StateKeyLastResponseID)
	if !ok {
		return nil, fmt.Errorf("last response ID not found")
	}
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("invocation not found")
	}
	var toolMsgs []model.Message
	for _, toolcall := range asst.ToolCalls {
		if toolcall.Function.Name != "calculator" {
			return nil, fmt.Errorf("unexpected tool: %s", toolcall.Function.Name)
		}
		emitter := newToolRunEmitter(ctx, invocation, toolRunMeta{
			eventChan:  execCtx.EventChan,
			nodeID:     nodeID,
			responseID: responseID,
			call:       toolcall,
			start:      time.Now(),
			input:      string(toolcall.Function.Arguments),
		})
		emitter.emitStart()
		result, err := callableCalc.Call(ctx, toolcall.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("calculator call failed: %w", err)
		}
		outputBytes, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal tool output: %w", err)
		}
		toolMsgs = append(toolMsgs, model.NewToolMessage(toolcall.ID, toolcall.Function.Name, string(outputBytes)))
		emitter.emitComplete(string(outputBytes), true)
	}
	return graph.State{
		graph.StateKeyMessages: []graph.MessageOp{
			graph.AppendMessages{Items: toolMsgs},
		},
	}, nil
}

func finalNode(ctx context.Context, state graph.State) (any, error) {
	return nil, nil
}

type toolRunEmitter struct {
	ctx        context.Context
	invocation *agent.Invocation
	meta       toolRunMeta
}

type toolRunMeta struct {
	eventChan  chan<- *event.Event
	nodeID     string
	responseID string
	call       model.ToolCall
	start      time.Time
	input      string
}

func newToolRunEmitter(ctx context.Context, invocation *agent.Invocation, meta toolRunMeta) toolRunEmitter {
	return toolRunEmitter{
		ctx:        ctx,
		invocation: invocation,
		meta:       meta,
	}
}

func (e toolRunEmitter) emitStart() {
	e.emit(graph.ToolExecutionPhaseStart, "", false)
}

func (e toolRunEmitter) emitComplete(output string, includeResponse bool) {
	e.emit(graph.ToolExecutionPhaseComplete, output, includeResponse)
}

func (e toolRunEmitter) emit(phase graph.ToolExecutionPhase, output string, includeResponse bool) {
	if e.meta.eventChan == nil || e.invocation == nil {
		return
	}
	evt := graph.NewToolExecutionEvent(
		graph.WithToolEventInvocationID(e.invocation.InvocationID),
		graph.WithToolEventToolName(e.meta.call.Function.Name),
		graph.WithToolEventToolID(e.meta.call.ID),
		graph.WithToolEventNodeID(e.meta.nodeID),
		graph.WithToolEventResponseID(e.meta.responseID),
		graph.WithToolEventPhase(phase),
		graph.WithToolEventStartTime(e.meta.start),
		graph.WithToolEventEndTime(time.Now()),
		graph.WithToolEventInput(e.meta.input),
		graph.WithToolEventOutput(output),
		graph.WithToolEventIncludeResponse(includeResponse),
	)
	agent.EmitEvent(e.ctx, e.invocation, e.meta.eventChan, evt)
}

func newCalculatorTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, params struct {
			A  float64 `json:"a"`
			B  float64 `json:"b"`
			Op string  `json:"op"`
		}) (map[string]any, error) {
			op := params.Op
			if op == "" {
				op = "add"
			}
			var res float64
			switch op {
			case "add":
				res = params.A + params.B
			case "subtract":
				res = params.A - params.B
			case "multiply":
				res = params.A * params.B
			case "divide":
				if params.B == 0 {
					return nil, fmt.Errorf("divide by zero")
				}
				res = params.A / params.B
			default:
				return nil, fmt.Errorf("unsupported op: %s", op)
			}
			return map[string]any{
				"a":   params.A,
				"b":   params.B,
				"op":  op,
				"res": res,
			}, nil
		},
		function.WithName("calculator"),
		function.WithDescription("Do basic arithmetic on two numbers with op in [add, subtract, multiply, divide]."),
	)
}

func floatPtr(f float64) *float64 {
	return &f
}
