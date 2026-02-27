//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// jsonSchemaValidEvaluator validates the final response against a JSON Schema.
type jsonSchemaValidEvaluator struct {
	schema *jsonschema.Schema
}

// NewJSONSchemaValid creates a new evaluator that validates against the schema at schemaPath.
func NewJSONSchemaValid(schemaPath string) (evaluator.Evaluator, error) {
	if schemaPath == "" {
		return nil, errors.New("schema path is empty")
	}
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", strings.NewReader(string(b))); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	s, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return &jsonSchemaValidEvaluator{schema: s}, nil
}

// Name returns the metric name for this evaluator.
func (e *jsonSchemaValidEvaluator) Name() string {
	return "json_schema_valid"
}

// Description describes what this evaluator checks.
func (e *jsonSchemaValidEvaluator) Description() string {
	return "Validates that the final response is a single JSON object matching the configured schema"
}

// Evaluate validates each invocation final response with the configured JSON schema.
func (e *jsonSchemaValidEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if e.schema == nil {
		return nil, errors.New("schema is nil")
	}
	if evalMetric == nil {
		return nil, errors.New("eval metric is nil")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	perInvocation := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	total := 0.0
	for i := range actuals {
		actual := actuals[i]
		expected := expecteds[i]
		score, reason := e.validateOne(actual)
		st := statusForScore(score, evalMetric.Threshold)
		perInvocation = append(perInvocation, &evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expected,
			Score:              score,
			Status:             st,
			Details: &evaluator.PerInvocationDetails{
				Reason: reason,
				Score:  score,
			},
		})
		total += score
	}
	if len(perInvocation) == 0 {
		return &evaluator.EvaluateResult{OverallStatus: status.EvalStatusNotEvaluated}, nil
	}
	overallScore := total / float64(len(perInvocation))
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        statusForScore(overallScore, evalMetric.Threshold),
		PerInvocationResults: perInvocation,
	}, nil
}

func (e *jsonSchemaValidEvaluator) validateOne(actual *evalset.Invocation) (float64, string) {
	if actual == nil || actual.FinalResponse == nil {
		return 0.0, "Missing final response."
	}
	raw := actual.FinalResponse.Content
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0.0, fmt.Sprintf("Invalid JSON: %v", err)
	}
	if err := e.schema.Validate(v); err != nil {
		return 0.0, fmt.Sprintf("Schema validation failed: %v", err)
	}
	return 1.0, "valid"
}

func statusForScore(score float64, threshold float64) status.EvalStatus {
	if score >= threshold {
		return status.EvalStatusPassed
	}
	return status.EvalStatusFailed
}
