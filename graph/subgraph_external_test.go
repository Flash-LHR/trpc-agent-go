//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSubgraph_DisableGraphCompletionEvent_PreservesWrappedGraphAgentOutput(t *testing.T) {
	const (
		childNodeName     = "child_handoff"
		afterNodeName     = "after"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
		userInput         = "hello"
		valuePrefix       = "computed: "
	)

	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		input, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
		childValue := valuePrefix + input
		return graph.State{
			childValueKey:              childValue,
			graph.StateKeyLastResponse: childValue,
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	graphChild, err := graphagent.New("graph-child", childCompiled)
	require.NoError(t, err)
	wrappedChild := chainagent.New(
		childNodeName,
		chainagent.WithSubAgents([]agent.Agent{graphChild}),
	)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentGraph.AddNode(afterNodeName, func(ctx context.Context, state graph.State) (any, error) {
		value, ok := graph.GetStateValue[string](state, valueFromChildKey)
		if !ok {
			return nil, nil
		}
		return graph.State{graph.StateKeyLastResponse: value}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{wrappedChild}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphCompletionEvent: true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var lastAssistant string
	for evt := range eventCh {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 {
			lastAssistant = evt.Response.Choices[0].Message.Content
		}
	}
	require.Equal(t, valuePrefix+userInput, lastAssistant)
}

func TestSubgraph_DisableGraphCompletionEvent_PreservesNestedCycleEscalation(t *testing.T) {
	const (
		childNodeName     = "cycle_child"
		afterNodeName     = "after"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
		userInput         = "hello"
		valuePrefix       = "computed: "
	)

	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		input, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
		childValue := valuePrefix + input
		return graph.State{
			childValueKey:              childValue,
			graph.StateKeyLastResponse: childValue,
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	graphChild, err := graphagent.New("graph-child", childCompiled)
	require.NoError(t, err)
	cycleChild := cycleagent.New(
		childNodeName,
		cycleagent.WithSubAgents([]agent.Agent{graphChild}),
		cycleagent.WithMaxIterations(2),
		cycleagent.WithEscalationFunc(func(evt *event.Event) bool {
			return evt != nil &&
				evt.Object == model.ObjectTypeChatCompletion &&
				len(evt.StateDelta) > 0
		}),
	)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentGraph.AddNode(afterNodeName, func(ctx context.Context, state graph.State) (any, error) {
		value, ok := graph.GetStateValue[string](state, valueFromChildKey)
		if !ok {
			return nil, nil
		}
		return graph.State{graph.StateKeyLastResponse: value}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{cycleChild}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphCompletionEvent: true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var childVisibleCompletionCount int
	var lastAssistant string
	for evt := range eventCh {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil &&
			evt.Object == model.ObjectTypeChatCompletion &&
			len(evt.StateDelta) > 0 &&
			evt.StateDelta[childValueKey] != nil {
			childVisibleCompletionCount++
		}
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 {
			lastAssistant = evt.Response.Choices[0].Message.Content
		}
	}

	require.Equal(t, 1, childVisibleCompletionCount)
	require.Equal(t, valuePrefix+userInput, lastAssistant)
}

func TestSubgraph_DisableGraphCompletionEvent_DropsStaleOutputAfterChildAfterCallbackError(t *testing.T) {
	const (
		childNodeName     = "child_handoff"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			childValueKey:              "child-state",
			graph.StateKeyLastResponse: "child-state",
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		return nil, errors.New("after callback failed")
	})
	childAgent, err := graphagent.New(
		childNodeName,
		childCompiled,
		graphagent.WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(childNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphCompletionEvent: true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var sawCallbackError bool
	var sawStaleChildValue bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			evt.Error.Type == agent.ErrorTypeAgentCallbackError &&
			evt.Error.Message == "after callback failed" {
			sawCallbackError = true
		}
		if evt != nil &&
			evt.StateDelta != nil &&
			string(evt.StateDelta[valueFromChildKey]) == `"child-state"` {
			sawStaleChildValue = true
		}
	}

	require.True(t, sawCallbackError)
	require.False(t, sawStaleChildValue)
}

func TestSubgraph_DisableGraphExecutorEvents_ChildFailureStopsParentGraph(t *testing.T) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	childAgent, err := graphagent.New(childNodeName, childCompiled)
	require.NoError(t, err)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphCompletionEvent: true,
			DisableGraphExecutorEvents:  true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}

	require.True(t, sawChildError)
	require.False(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphCompletionEvent_ChildFailureStopsParentGraph(
	t *testing.T,
) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	childAgent, err := graphagent.New(childNodeName, childCompiled)
	require.NoError(t, err)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphCompletionEvent: true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}
	require.True(t, sawChildError)
	require.False(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphExecutorEvents_PreservesChildAfterCallbackCustomResponse(
	t *testing.T,
) {
	const childNodeName = "child"
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		if args.Error == nil {
			return nil, nil
		}
		return &agent.AfterAgentResult{
			CustomResponse: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("recovered"),
				}},
			},
		}, nil
	})
	childAgent, err := graphagent.New(
		childNodeName,
		childCompiled,
		graphagent.WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(childNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			DisableGraphExecutorEvents: true,
		}),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var lastEvent *event.Event
	for evt := range eventCh {
		lastEvent = evt
	}
	require.NotNil(t, lastEvent)
	require.NotNil(t, lastEvent.Response)
	require.Nil(t, lastEvent.Error)
	require.Len(t, lastEvent.Response.Choices, 1)
	require.Equal(t, "recovered", lastEvent.Response.Choices[0].Message.Content)
}
