//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rubriccritic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConstructMessagesBuildsCriticPrompt(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "What is the capital of France?"},
		FinalResponse: &model.Message{Content: "Paris is the capital."},
	}
	expected := &evalset.Invocation{
		FinalResponse: &model.Message{Content: "The capital of France is Paris."},
	}
	evalMetric := &metric.EvalMetric{
		Criterion: &criterion.Criterion{
			LLMJudge: &llm.LLMCriterion{
				Rubrics: []*llm.Rubric{
					{
						ID:      "1",
						Content: &llm.RubricContent{Text: "The final answer states the correct city."},
					},
				},
			},
		},
	}
	messages, err := constructor.ConstructMessages(
		context.Background(),
		[]*evalset.Invocation{actual},
		[]*evalset.Invocation{expected},
		evalMetric,
	)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, model.RoleUser, messages[0].Role)
	assert.Contains(t, messages[0].Content, "What is the capital of France?")
	assert.Contains(t, messages[0].Content, "Paris is the capital.")
	assert.Contains(t, messages[0].Content, "The capital of France is Paris.")
	assert.Contains(t, messages[0].Content, "The final answer states the correct city.")
	assert.Contains(t, messages[0].Content, "You are llm_rubric_critic, the evaluator for this metric.")
	assert.Contains(t, messages[0].Content, "The GOLDEN ANSWER is the authoritative target.")
	assert.Contains(t, messages[0].Content, "Treat the GOLDEN ANSWER as the source of truth")
	assert.Contains(t, messages[0].Content, "A \"no\" must be caused by a material defect")
	assert.Contains(t, messages[0].Content, "Semantic equivalence is acceptable")
	assert.Contains(t, messages[0].Content, "Do not nitpick")
	assert.Contains(t, messages[0].Content, "When the verdict is \"no\", the reason must point to a concrete mismatch")
}

func TestConstructMessagesRequiresExpecteds(t *testing.T) {
	constructor := New()
	actual := &evalset.Invocation{
		UserContent:   &model.Message{Content: "prompt"},
		FinalResponse: &model.Message{Content: "answer"},
	}
	_, err := constructor.ConstructMessages(context.Background(), []*evalset.Invocation{actual}, nil, &metric.EvalMetric{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expecteds is empty")
}
