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
	"reflect"
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
