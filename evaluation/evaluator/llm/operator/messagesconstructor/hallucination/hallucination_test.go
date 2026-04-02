//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hallucination

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type fakeJudgeRunner struct {
	events      []*event.Event
	runCalls    int
	lastMessage model.Message
}

func (f *fakeJudgeRunner) Run(_ context.Context, _ string, _ string, message model.Message,
	_ ...agent.RunOption) (<-chan *event.Event, error) {
	f.runCalls++
	f.lastMessage = message
	ch := make(chan *event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeJudgeRunner) Close() error {
	return nil
}

var _ runner.Runner = (*fakeJudgeRunner)(nil)

func TestConstructMessagesBuildsValidatorPromptFromSegmentedSentences(t *testing.T) {
	constructor := New()
	runner := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{Content: "<sentence>Paris is cloudy.</sentence>\n<sentence>It is 18C.</sentence>"},
				}},
				Done: true,
			}),
		},
	}
	actual := &evalset.Invocation{
		ContextMessages: []*model.Message{
			{Role: model.RoleSystem, Content: "Cite tool outputs only."},
		},
		IntermediateResponses: []*model.Message{
			{Role: model.RoleAssistant, Content: "Let me check the live weather feed."},
		},
		UserContent:   &model.Message{Content: "What is the weather in Paris?"},
		FinalResponse: &model.Message{Content: "Paris is cloudy and 18C."},
		Tools: []*evalset.Tool{
			{
				ID:        "tool-1",
				Name:      "weather_lookup",
				Arguments: map[string]any{"location": "Paris"},
				Result:    map[string]any{"temperatureC": 18, "condition": "Cloudy"},
			},
		},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		nil,
		buildEvalMetricWithRunner(runner),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Equal(t, 1, runner.runCalls)
	assert.Contains(t, runner.lastMessage.Content, "Segment the final answer into sentence-level or bullet-level claims.")
	assert.Contains(t, runner.lastMessage.Content, "Paris is cloudy and 18C.")
	assert.Contains(t, messages[0].Content, "<context>")
	assert.Contains(t, messages[0].Content, "User prompt:")
	assert.Contains(t, messages[0].Content, "What is the weather in Paris?")
	assert.Contains(t, messages[0].Content, "Cite tool outputs only.")
	assert.Contains(t, messages[0].Content, "Intermediate responses:")
	assert.Contains(t, messages[0].Content, "Let me check the live weather feed.")
	assert.Contains(t, messages[0].Content, "tool_calls:")
	assert.Contains(t, messages[0].Content, "\"id\": \"tool-1\"")
	assert.Contains(t, messages[0].Content, "\"temperatureC\": 18")
	assert.Contains(t, messages[0].Content, "tool_outputs:")
	assert.Contains(t, messages[0].Content, "<sentence id=\"1\">")
	assert.Contains(t, messages[0].Content, "Paris is cloudy.")
	assert.Contains(t, messages[0].Content, "<sentence id=\"2\">")
	assert.Contains(t, messages[0].Content, "It is 18C.")
	assert.Contains(t, messages[0].Content, "supported|unsupported|contradictory|disputed|not_applicable")
}

func TestConstructMessagesWithoutArtifacts(t *testing.T) {
	constructor := New()
	runner := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{Content: "<sentence>hello</sentence>"},
				}},
				Done: true,
			}),
		},
	}
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "hi"},
		FinalResponse: &model.Message{Content: "hello"},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		nil,
		buildEvalMetricWithRunner(runner),
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "User prompt:")
	assert.Contains(t, messages[0].Content, "hi")
	assert.Contains(t, messages[0].Content, "<sentence id=\"1\">")
	assert.Contains(t, messages[0].Content, "hello")
}

func TestConstructMessagesRequiresActuals(t *testing.T) {
	constructor := New()
	_, err := constructor.ConstructMessages(context.Background(), nil, nil, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actuals is empty")
}

func TestConstructMessagesRequiresSegmentedSentences(t *testing.T) {
	constructor := New()
	runner := &fakeJudgeRunner{
		events: []*event.Event{
			event.NewResponseEvent("inv", "judge", &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{Content: "Paris is cloudy."},
				}},
				Done: true,
			}),
		},
	}
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "hi"},
		FinalResponse: &model.Message{Content: "hello"},
	}
	_, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		nil,
		buildEvalMetricWithRunner(runner),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no segmented sentences found in response")
}

func buildEvalMetricWithRunner(judgeRunner runner.Runner) *metric.EvalMetric {
	return &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				JudgeRunnerOptions: &criterionllm.JudgeRunnerOptions{Runner: judgeRunner},
			},
		},
	}
}
