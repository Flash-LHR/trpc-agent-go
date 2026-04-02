//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package failover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func TestNewReturnsErrorWithoutCandidates(t *testing.T) {
	llm, err := New()
	require.Error(t, err)
	assert.EqualError(t, err, "failover: at least one candidate model is required")
	assert.Nil(t, llm)
}

func TestNewReturnsErrorWithNilCandidate(t *testing.T) {
	llm, err := New(WithCandidates(nil))
	require.Error(t, err)
	assert.EqualError(t, err, "failover: candidate model at index 0 is nil")
	assert.Nil(t, llm)
}

func TestWithCandidatesAppendsInPriorityOrder(t *testing.T) {
	primary := openai.New("primary-model")
	backup := openai.New("backup-model")
	llm, err := New(
		WithCandidates(primary),
		WithCandidates(backup),
	)
	require.NoError(t, err)
	assert.Equal(t, "primary-model", llm.Info().Name)
	_, ok := llm.(model.IterModel)
	require.True(t, ok)
	impl, ok := llm.(*failoverModel)
	require.True(t, ok)
	require.Len(t, impl.candidates, 2)
	assert.Same(t, primary, impl.candidates[0])
	assert.Same(t, backup, impl.candidates[1])
}

func TestCloneRequestDeepCopiesSerializableFields(t *testing.T) {
	maxTokens := 128
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system"),
			model.NewUserMessage("user"),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:    true,
			MaxTokens: &maxTokens,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "answer",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
				},
			},
		},
	}
	cloned, err := cloneRequest(request)
	require.NoError(t, err)
	require.NotNil(t, cloned)
	require.NotSame(t, request, cloned)
	require.NotSame(t, request.StructuredOutput, cloned.StructuredOutput)
	require.NotSame(t, request.StructuredOutput.JSONSchema, cloned.StructuredOutput.JSONSchema)
	cloned.Messages[1].Content = "changed"
	cloned.StructuredOutput.JSONSchema.Name = "changed"
	cloned.StructuredOutput.JSONSchema.Schema["type"] = "array"
	assert.Equal(t, "user", request.Messages[1].Content)
	assert.Equal(t, "answer", request.StructuredOutput.JSONSchema.Name)
	assert.Equal(t, "object", request.StructuredOutput.JSONSchema.Schema["type"])
}

func TestHasFailoverResponseError(t *testing.T) {
	tests := []struct {
		name     string
		response *model.Response
		want     bool
	}{
		{
			name: "error message present",
			response: &model.Response{
				Error: &model.ResponseError{
					Message: "upstream unavailable",
				},
			},
			want: true,
		},
		{
			name: "error type present",
			response: &model.Response{
				Error: &model.ResponseError{
					Type: model.ErrorTypeAPIError,
				},
			},
			want: true,
		},
		{
			name: "error struct empty",
			response: &model.Response{
				Error: &model.ResponseError{},
			},
			want: false,
		},
		{
			name:     "nil response",
			response: nil,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasFailoverResponseError(tt.response))
		})
	}
}

func TestRunAttemptsFallsBackBeforeFirstNonErrorChunk(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeStreamError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "hello",
						},
					},
				},
				IsPartial: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 1)
	assert.Equal(t, "backup", responses[0].Model)
	assert.Equal(t, "hello", responses[0].Choices[0].Delta.Content)
}

func TestRunAttemptsStopsFallbackAfterFirstNonErrorChunk(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{ID: "primary-prelude", Model: "primary", Done: false},
			{
				Error: &model.ResponseError{
					Message: "primary failed",
					Type:    model.ErrorTypeStreamError,
				},
				Model: "primary",
				Done:  true,
			},
		},
	}
	backup := &scriptedIterModel{
		name: "backup",
		responses: []*model.Response{
			{
				Model: "backup",
				Choices: []model.Choice{
					{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "hello",
						},
					},
				},
				IsPartial: true,
			},
		},
	}
	llm, err := New(WithCandidates(primary, backup))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 2)
	assert.Equal(t, "primary-prelude", responses[0].ID)
	require.NotNil(t, responses[1].Error)
	assert.Equal(t, "primary failed", responses[1].Error.Message)
}

func TestRunAttemptsYieldsNonMeaningfulNonErrorResponses(t *testing.T) {
	primary := &scriptedIterModel{
		name: "primary",
		responses: []*model.Response{
			{ID: "primary-prelude", Model: "primary", Done: false},
			{ID: "primary-final", Model: "primary", Done: true},
		},
	}
	llm, err := New(WithCandidates(primary))
	require.NoError(t, err)
	responses := collectIterResponses(t, llm, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
	})
	require.Len(t, responses, 2)
	assert.Equal(t, "primary-prelude", responses[0].ID)
	assert.Equal(t, "primary-final", responses[1].ID)
}

type scriptedIterModel struct {
	name      string
	responses []*model.Response
	err       error
}

func (m *scriptedIterModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *scriptedIterModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	if m.err != nil {
		return nil, m.err
	}
	return func(yield func(*model.Response) bool) {
		for _, response := range m.responses {
			if !yield(response) {
				return
			}
		}
	}, nil
}

func (m *scriptedIterModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func collectIterResponses(t *testing.T, llm model.Model, request *model.Request) []*model.Response {
	t.Helper()
	iterModel, ok := llm.(model.IterModel)
	require.True(t, ok)
	seq, err := iterModel.GenerateContentIter(context.Background(), request)
	require.NoError(t, err)
	responses := make([]*model.Response, 0)
	seq(func(response *model.Response) bool {
		responses = append(responses, response)
		return true
	})
	return responses
}
