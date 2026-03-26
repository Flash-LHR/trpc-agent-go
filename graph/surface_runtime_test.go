//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLLMNode_SurfacePatch_OverridesInstructionFewShotModelAndTools(t *testing.T) {
	staticModel := &captureModel{}
	patchedModel := &captureModel{}

	sg := NewStateGraph(MessagesStateSchema())
	sg.AddLLMNode(
		"llm",
		staticModel,
		"static instruction",
		map[string]tool.Tool{"old_tool": &echoTool{name: "old_tool"}},
	)

	var patch agent.SurfacePatch
	patch.SetInstruction("patched instruction")
	patch.SetFewShot([][]model.Message{{
		model.NewUserMessage("few-shot user"),
		model.NewAssistantMessage("few-shot assistant"),
	}})
	patch.SetModel(patchedModel)
	patch.SetTools([]tool.Tool{&echoTool{name: "new_tool"}})

	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("graph"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("graph/llm", patch),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	node := sg.graph.nodes["llm"]
	exec := &ExecutionContext{InvocationID: inv.InvocationID, Invocation: inv}

	_, err := node.Function(ctx, State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "llm",
		StateKeyUserInput:     "actual user",
	})
	require.NoError(t, err)

	require.Nil(t, staticModel.lastReq)
	require.NotNil(t, patchedModel.lastReq)
	require.Len(t, patchedModel.lastReq.Messages, 4)
	require.Equal(t, model.RoleSystem, patchedModel.lastReq.Messages[0].Role)
	require.Contains(t, patchedModel.lastReq.Messages[0].Content, "patched instruction")
	require.NotContains(t, patchedModel.lastReq.Messages[0].Content, "static instruction")
	require.Equal(t, "few-shot user", patchedModel.lastReq.Messages[1].Content)
	require.Equal(t, "few-shot assistant", patchedModel.lastReq.Messages[2].Content)
	require.Equal(t, "actual user", patchedModel.lastReq.Messages[3].Content)
	require.Contains(t, patchedModel.lastReq.Tools, "new_tool")
	require.NotContains(t, patchedModel.lastReq.Tools, "old_tool")
}

func TestToolsNode_SurfacePatch_OverridesExplicitTools(t *testing.T) {
	sg := NewStateGraph(MessagesStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{
		"old_tool": &echoTool{name: "old_tool"},
	})

	var patch agent.SurfacePatch
	patch.SetTools([]tool.Tool{&echoTool{name: "new_tool"}})

	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("graph"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("graph/tools", patch),
		)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	node := sg.graph.nodes["tools"]
	exec := &ExecutionContext{InvocationID: inv.InvocationID, Invocation: inv}

	result, err := node.Function(ctx, State{
		StateKeyExecContext:   exec,
		StateKeyCurrentNodeID: "tools",
		StateKeyMessages: []model.Message{
			model.NewUserMessage("hi"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "new_tool",
						Arguments: []byte(`{}`),
					},
				}},
			},
		},
	})
	require.NoError(t, err)

	state, ok := result.(State)
	require.True(t, ok)
	require.NotNil(t, state[StateKeyMessages])
}
