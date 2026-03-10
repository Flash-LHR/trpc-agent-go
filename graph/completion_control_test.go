//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestVisibleGraphCompletionEvent_AddsCompletionMetadataWhenMissing(t *testing.T) {
	raw := &event.Event{
		Response: &model.Response{
			Object: "graph.execution",
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("manual-final"),
			}},
		},
		StateDelta: map[string][]byte{
			"child_state": []byte(`"child-state"`),
		},
	}

	visible, ok := VisibleGraphCompletionEvent(raw)
	require.True(t, ok)
	require.True(t, IsVisibleGraphCompletionEvent(visible))
	require.Equal(t, model.ObjectTypeChatCompletion, visible.Object)
	require.Equal(t, []byte("{}"), visible.StateDelta[MetadataKeyCompletion])
}

func TestVisibleGraphCompletionEventWithDedup_DedupsByAssistantChoicesWhenResponseIDEmpty(
	t *testing.T,
) {
	finishReason := "stop"
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("answer"),
				FinishReason: &finishReason,
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse: "answer",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.True(t, IsVisibleGraphCompletionEvent(visible))
	require.Empty(t, visible.Response.Choices)
	require.Equal(t, []byte(`"answer"`), visible.StateDelta[StateKeyLastResponse])
}

func TestVisibleGraphCompletionEventWithDedup_DoesNotFallbackToSignatureWhenResponseIDDiffers(
	t *testing.T,
) {
	emitted := RecordAssistantResponseID(nil, &event.Event{
		Response: &model.Response{
			ID:     "resp-1",
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}},
		},
	})
	raw := NewGraphCompletionEvent(
		WithCompletionEventFinalState(State{
			StateKeyLastResponse:   "answer",
			StateKeyLastResponseID: "resp-2",
		}),
	)

	visible, ok := VisibleGraphCompletionEventWithDedup(raw, emitted)
	require.True(t, ok)
	require.Len(t, visible.Response.Choices, 1)
	require.Equal(t, "answer", visible.Response.Choices[0].Message.Content)
	require.Equal(t, []byte(`"resp-2"`), visible.StateDelta[StateKeyLastResponseID])
}
