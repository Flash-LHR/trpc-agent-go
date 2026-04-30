//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// runStreamingStep runs beforeAgent → beforeModel → N× afterModel(frames)
// and returns the trace captured by the sampler's writer once afterAgent
// finalises the invocation.
func runStreamingStep(t *testing.T, invID string, frames []*model.Response, finalErr error) (*Trace, *PromptSampler) {
	t.Helper()
	cw := &captureWriter{}
	s := New(WithWriter(cw), WithSampleRate(1.0))

	inv := &agent.Invocation{
		InvocationID: invID,
		AgentName:    "test-agent",
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// beforeAgent: creates invocationState (sampled=true because rate=1).
	_, err := s.beforeAgent(ctx, &agent.BeforeAgentArgs{Invocation: inv})
	require.NoError(t, err)

	// beforeModel: opens the model step and returns a ctx carrying the
	// modelBuilderKey. The *model.Request only needs Messages for the
	// input fingerprint.
	beforeRes, err := s.beforeModel(ctx, &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{Role: "user", Content: "hi"}},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, beforeRes)
	require.NotNil(t, beforeRes.Context)
	modelCtx := beforeRes.Context

	// afterModel for each frame. The last frame is expected to have
	// IsPartial=false (the caller controls frame ordering).
	for i, fr := range frames {
		var afterArgs *model.AfterModelArgs
		if fr != nil {
			afterArgs = &model.AfterModelArgs{Response: fr}
			// Attach error only to the terminal frame for simplicity.
			if i == len(frames)-1 && finalErr != nil {
				afterArgs.Error = finalErr
			}
		}
		_, err := s.afterModel(modelCtx, afterArgs)
		require.NoError(t, err)
	}

	// afterAgent: writes the trace to the capture writer.
	_, err = s.afterAgent(ctx, &agent.AfterAgentArgs{Invocation: inv})
	require.NoError(t, err)

	require.Len(t, cw.traces, 1, "expected exactly one trace written")
	return cw.traces[0], s
}

// Helper constructors ----------------------------------------------------

func partialFrame(deltaContent string, usage *model.Usage) *model.Response {
	return &model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{Content: deltaContent},
		}},
		Usage: usage,
	}
}

func terminalFrame(messageContent string, usage *model.Usage, toolCalls []model.ToolCall) *model.Response {
	return &model.Response{
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Content:   messageContent,
				ToolCalls: toolCalls,
			},
		}},
		Usage: usage,
	}
}

func usageOnlyFrame(usage *model.Usage) *model.Response {
	return &model.Response{
		IsPartial: true,
		Choices:   nil, // usage-only frame has no Choices
		Usage:     usage,
	}
}

// Tests ------------------------------------------------------------------

// 4.1: multi-partial + terminal with usage on the terminal frame.
func TestAfterModel_Streaming_MultiPartialWithTerminalUsage(t *testing.T) {
	frames := []*model.Response{
		partialFrame("Hello ", nil),
		partialFrame("world", nil),
		partialFrame("!", nil),
		terminalFrame("", &model.Usage{
			PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46,
		}, nil),
	}
	trace, _ := runStreamingStep(t, "inv-4.1", frames, nil)

	require.Len(t, trace.Steps, 1)
	step := trace.Steps[0]
	assert.Equal(t, StepTypeModel, step.StepType)
	require.NotNil(t, step.Output)
	assert.Equal(t, "Hello world!", step.Output.Text)
	require.NotNil(t, step.Output.TokenUsage)
	assert.Equal(t, 12, step.Output.TokenUsage.PromptTokens)
	assert.Equal(t, 34, step.Output.TokenUsage.CompletionTokens)
	assert.Equal(t, 46, step.Output.TokenUsage.TotalTokens)
}

// 4.2: usage arrives in a dedicated usage-only frame (openai-style).
func TestAfterModel_Streaming_UsageOnlyFrame(t *testing.T) {
	frames := []*model.Response{
		partialFrame("ab", nil),
		partialFrame("cd", nil),
		usageOnlyFrame(&model.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}),
		terminalFrame("abcd", nil, nil), // provider also filled Message.Content on terminal
	}
	trace, _ := runStreamingStep(t, "inv-4.2", frames, nil)

	require.Len(t, trace.Steps, 1)
	step := trace.Steps[0]
	assert.Equal(t, "abcd", step.Output.Text)
	require.NotNil(t, step.Output.TokenUsage)
	assert.Equal(t, 30, step.Output.TokenUsage.TotalTokens)
}

// 4.3: provider re-fills Message.Content with the cumulative text each frame.
// The last non-empty Message.Content must win (Reset semantics).
func TestAfterModel_Streaming_ProviderRepeatsMessageContent(t *testing.T) {
	mkFrame := func(partial bool, content string) *model.Response {
		return &model.Response{
			IsPartial: partial,
			Choices: []model.Choice{{
				Index:   0,
				Message: model.Message{Content: content},
			}},
		}
	}
	frames := []*model.Response{
		mkFrame(true, "A"),
		mkFrame(true, "AB"),
		mkFrame(true, "ABC"),
		mkFrame(false, "ABC"),
	}
	trace, _ := runStreamingStep(t, "inv-4.3", frames, nil)

	require.Len(t, trace.Steps, 1)
	assert.Equal(t, "ABC", trace.Steps[0].Output.Text,
		"Message.Content Reset semantics must yield the final full snapshot, not a concatenation")
}

// 4.4: streaming with only tool_calls, no text.
func TestAfterModel_Streaming_ToolCallsOnly(t *testing.T) {
	frames := []*model.Response{
		partialFrame("", nil),
		terminalFrame("", nil, []model.ToolCall{
			{
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"q":"x"}`),
				},
			},
		}),
	}
	trace, _ := runStreamingStep(t, "inv-4.4", frames, nil)

	require.Len(t, trace.Steps, 1)
	text := trace.Steps[0].Output.Text
	assert.True(t, strings.HasPrefix(text, "→ search("),
		"expected formatToolCalls output, got: %q", text)
	assert.Contains(t, text, `{"q":"x"}`)
}

// 4.5: no usage ever reported → TokenUsage must be nil (not {0,0,0}).
func TestAfterModel_Streaming_NoUsageEver(t *testing.T) {
	frames := []*model.Response{
		partialFrame("foo", nil),
		partialFrame("bar", nil),
		terminalFrame("foobar", nil, nil),
	}
	trace, _ := runStreamingStep(t, "inv-4.5", frames, nil)

	require.Len(t, trace.Steps, 1)
	step := trace.Steps[0]
	assert.Equal(t, "foobar", step.Output.Text)
	assert.Nil(t, step.Output.TokenUsage,
		"TokenUsage must stay nil when provider never reported usage")
}

// 4.6: {0,0,0} usage snapshots are ignored, only non-zero usage wins.
func TestAfterModel_Streaming_ZeroUsageIgnored(t *testing.T) {
	frames := []*model.Response{
		partialFrame("x", &model.Usage{}), // TotalTokens=0 → ignored
		partialFrame("y", nil),
		usageOnlyFrame(&model.Usage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}),
		terminalFrame("xy", nil, nil),
	}
	trace, _ := runStreamingStep(t, "inv-4.6", frames, nil)

	require.Len(t, trace.Steps, 1)
	require.NotNil(t, trace.Steps[0].Output.TokenUsage)
	assert.Equal(t, 12, trace.Steps[0].Output.TokenUsage.TotalTokens)
	assert.Equal(t, 5, trace.Steps[0].Output.TokenUsage.PromptTokens)
}

// 4.7: non-streaming single-frame call still works (regression).
func TestAfterModel_NonStreaming_StillWorks(t *testing.T) {
	frames := []*model.Response{
		terminalFrame("The answer is 42",
			&model.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}, nil),
	}
	trace, _ := runStreamingStep(t, "inv-4.7", frames, nil)

	require.Len(t, trace.Steps, 1)
	assert.Equal(t, "The answer is 42", trace.Steps[0].Output.Text)
	require.NotNil(t, trace.Steps[0].Output.TokenUsage)
	assert.Equal(t, 8, trace.Steps[0].Output.TokenUsage.TotalTokens)
}

// 4.8: beforeModel + afterAgent (no afterModel ever) must clear state and
// leave no lingering accumulator or builder behind.
func TestAfterModel_InvocationCancelled_NoAccumulatorLeak(t *testing.T) {
	cw := &captureWriter{}
	s := New(WithWriter(cw), WithSampleRate(1.0))

	inv := &agent.Invocation{InvocationID: "inv-4.8", AgentName: "a"}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := s.beforeAgent(ctx, &agent.BeforeAgentArgs{Invocation: inv})
	require.NoError(t, err)

	// Open a model step but never close it.
	_, err = s.beforeModel(ctx, &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{{Content: "x"}}},
	})
	require.NoError(t, err)

	// Sanity: state + accumulator exist mid-flight.
	stateMid := s.states.get("inv-4.8")
	require.NotNil(t, stateMid)
	require.NotNil(t, stateMid.getAccumulator("inv-4.8:model"))

	// afterAgent finalises and schedules state deletion (via states.delete).
	_, err = s.afterAgent(ctx, &agent.AfterAgentArgs{Invocation: inv})
	require.NoError(t, err)

	// State manager must have released the invocation state entirely.
	assert.Nil(t, s.states.get("inv-4.8"),
		"invocation state must be removed after afterAgent")
}

// 4.x bonus: error path still emits a step (regression for task 2.3).
func TestAfterModel_Streaming_ErrorStillCommitsStep(t *testing.T) {
	frames := []*model.Response{
		partialFrame("partial", nil),
		terminalFrame("", nil, nil),
	}
	trace, _ := runStreamingStep(t, "inv-err", frames, errors.New("boom"))

	require.Len(t, trace.Steps, 1)
	assert.Equal(t, "partial", trace.Steps[0].Output.Text,
		"text accumulated prior to error should be preserved")
	assert.Equal(t, "boom", trace.Steps[0].Error)
}
