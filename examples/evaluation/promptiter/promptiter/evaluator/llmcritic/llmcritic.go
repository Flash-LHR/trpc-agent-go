//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmcritic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/teacher"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/issues"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type llmCriticEvaluator struct {
	llmBaseEvaluator llm.LLMEvaluator
	teacher          *teacher.Teacher
	judgeTmpl        *template.Template
	outputSchema     *jsonschema.Schema
}

// New builds the llm_critic evaluator.
func New(t *teacher.Teacher, judgePromptPath string, outputSchemaPath string) (evaluator.Evaluator, error) {
	if t == nil {
		return nil, errors.New("teacher is nil")
	}
	if strings.TrimSpace(judgePromptPath) == "" {
		return nil, errors.New("judge prompt path is empty")
	}
	if strings.TrimSpace(outputSchemaPath) == "" {
		return nil, errors.New("judge output schema path is empty")
	}
	judgePromptBytes, err := os.ReadFile(judgePromptPath)
	if err != nil {
		return nil, fmt.Errorf("read judge prompt: %w", err)
	}
	judgeTmpl, err := template.New("judge_critic").Parse(string(judgePromptBytes))
	if err != nil {
		return nil, fmt.Errorf("parse judge prompt template: %w", err)
	}
	s, err := compileJSONSchema(outputSchemaPath)
	if err != nil {
		return nil, err
	}
	e := &llmCriticEvaluator{
		teacher:      t,
		judgeTmpl:    judgeTmpl,
		outputSchema: s,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e, nil
}

// Name returns the metric name for this evaluator.
func (e *llmCriticEvaluator) Name() string {
	return "llm_rubric_critic"
}

// Description describes what this evaluator checks.
func (e *llmCriticEvaluator) Description() string {
	return "Evaluates candidate output with an LLM judge, using a cached teacher reference output"
}

// Evaluate scores each invocation using the configured judge model and a cached teacher output.
func (e *llmCriticEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if e.llmBaseEvaluator == nil {
		return nil, errors.New("llm base evaluator is nil")
	}
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages builds the judge prompt from invocation context.
func (e *llmCriticEvaluator) ConstructMessages(ctx context.Context, actuals, _ []*evalset.Invocation, evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if e.teacher == nil {
		return nil, errors.New("teacher is nil")
	}
	if e.judgeTmpl == nil {
		return nil, errors.New("judge template is nil")
	}
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, errors.New("llm judge criterion not configured")
	}
	if len(actuals) == 0 {
		return nil, errors.New("actuals is empty")
	}
	actual := actuals[len(actuals)-1]
	userContent := model.NewUserMessage(actual.UserContent.Content)
	teacherOutput, err := e.teacher.Get(ctx, userContent)
	if err != nil {
		return nil, fmt.Errorf("get teacher output: %w", err)
	}
	rubricsText := formatRubrics(evalMetric.Criterion.LLMJudge.Rubrics)
	prompt, err := renderJudgePrompt(e.judgeTmpl, judgePromptData{
		UserInput:       userContent.Content,
		CandidateOutput: actual.FinalResponse.Content,
		TeacherOutput:   teacherOutput,
		Rubrics:         rubricsText,
	})
	if err != nil {
		return nil, fmt.Errorf("render judge prompt: %w", err)
	}
	return []model.Message{model.NewUserMessage(prompt)}, nil
}

// ScoreBasedOnResponse parses the judge JSON output, computes the rubric score, and stores the critique JSON in Reason.
func (e *llmCriticEvaluator) ScoreBasedOnResponse(ctx context.Context, resp *model.Response, evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, errors.New("llm judge criterion not configured")
	}
	if resp == nil {
		return nil, errors.New("judge response is nil")
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("judge response has no choices")
	}
	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	if raw == "" {
		return nil, errors.New("judge returned empty output")
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("judge output is not valid JSON: %w", err)
	}
	if e.outputSchema == nil {
		return nil, errors.New("judge output schema is nil")
	}
	if err := e.outputSchema.Validate(v); err != nil {
		return nil, fmt.Errorf("judge output schema validation failed: %w", err)
	}
	var parsed issues.JudgeOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal judge output: %w", err)
	}
	score, rubricScores := scoreFromJudgeOutput(evalMetric, parsed)
	return &evaluator.ScoreResult{
		Reason:       raw,
		Score:        score,
		RubricScores: rubricScores,
	}, nil
}

// AggregateSamples resolves multiple judge samples into a single invocation result.
func (e *llmCriticEvaluator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	return majorityvote.New().AggregateSamples(ctx, samples, evalMetric)
}

// AggregateInvocations aggregates per-invocation results into a single metric score.
func (e *llmCriticEvaluator) AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return average.New().AggregateInvocations(ctx, results, evalMetric)
}

func scoreFromJudgeOutput(evalMetric *metric.EvalMetric, out issues.JudgeOutput) (float64, []*evalresult.RubricScore) {
	wanted := evalMetric.Criterion.LLMJudge.Rubrics
	byID := make(map[string]issues.JudgeRubric, len(out.Rubrics))
	for _, r := range out.Rubrics {
		byID[r.ID] = r
	}
	total := 0.0
	rubricScores := make([]*evalresult.RubricScore, 0, len(wanted))
	for _, w := range wanted {
		id := w.ID
		r, ok := byID[id]
		verdict := "no"
		reason := "Missing rubric verdict."
		if ok {
			verdict = strings.ToLower(strings.TrimSpace(r.Verdict))
			reason = strings.TrimSpace(r.Reason)
		}
		score := 0.0
		if verdict == "yes" {
			score = 1.0
		}
		total += score
		rubricScores = append(rubricScores, &evalresult.RubricScore{
			ID:     id,
			Reason: reason,
			Score:  score,
		})
	}
	if len(wanted) == 0 {
		return 0.0, rubricScores
	}
	return total / float64(len(wanted)), rubricScores
}

type judgePromptData struct {
	// UserInput is the raw user message content.
	UserInput string
	// CandidateOutput is the candidate model output content.
	CandidateOutput string
	// TeacherOutput is the cached teacher reference output.
	TeacherOutput string
	// Rubrics is the formatted rubric list for the judge.
	Rubrics string
}

func renderJudgePrompt(tmpl *template.Template, data judgePromptData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func formatRubrics(rubrics []*criterionllm.Rubric) string {
	if len(rubrics) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rubrics {
		if r == nil || r.Content == nil {
			continue
		}
		b.WriteString("- ")
		b.WriteString(r.ID)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(r.Content.Text))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func compileJSONSchema(schemaPath string) (*jsonschema.Schema, error) {
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	resourceName := "schema.json"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceName, strings.NewReader(string(schemaBytes))); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	s, err := compiler.Compile(resourceName)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return s, nil
}
