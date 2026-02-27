//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluators

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/teacher"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/issues"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type llmRubricCriticEvaluator struct {
	teacher      *teacher.Teacher
	judgeTmpl    *template.Template
	outputSchema map[string]any
}

// NewLLMRubricCritic builds the llm_rubric_critic evaluator.
func NewLLMRubricCritic(t *teacher.Teacher, judgePromptPath string) (evaluator.Evaluator, error) {
	if t == nil {
		return nil, errors.New("teacher is nil")
	}
	if judgePromptPath == "" {
		return nil, errors.New("judge prompt path is empty")
	}
	b, err := os.ReadFile(judgePromptPath)
	if err != nil {
		return nil, fmt.Errorf("read judge prompt: %w", err)
	}
	tmpl, err := template.New("judge_critic").Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse judge prompt template: %w", err)
	}
	return &llmRubricCriticEvaluator{
		teacher:      t,
		judgeTmpl:    tmpl,
		outputSchema: judgeOutputSchema(),
	}, nil
}

// Name returns the metric name for this evaluator.
func (e *llmRubricCriticEvaluator) Name() string {
	return "llm_rubric_critic"
}

// Description describes what this evaluator checks.
func (e *llmRubricCriticEvaluator) Description() string {
	return "Evaluates candidate output with an LLM judge, using a cached teacher reference output"
}

// Evaluate scores each invocation using the configured judge model and a cached teacher output.
func (e *llmRubricCriticEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if e.teacher == nil {
		return nil, errors.New("teacher is nil")
	}
	if e.judgeTmpl == nil {
		return nil, errors.New("judge template is nil")
	}
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil || evalMetric.Criterion.LLMJudge.JudgeModel == nil {
		return nil, errors.New("llm judge criterion not configured")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("actual invocations (%d) and expected invocations (%d) count mismatch",
			len(actuals), len(expecteds))
	}
	// Score each invocation with the judge model.
	perInvocation := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	total := 0.0
	for i := range actuals {
		actual := actuals[i]
		expected := expecteds[i]
		score, reasonJSON, rubricScores := e.evaluateOne(ctx, actual, evalMetric)
		st := statusForScore(score, evalMetric.Threshold)
		perInvocation = append(perInvocation, &evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expected,
			Score:              score,
			Status:             st,
			Details: &evaluator.PerInvocationDetails{
				Reason:       reasonJSON,
				Score:        score,
				RubricScores: rubricScores,
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

func (e *llmRubricCriticEvaluator) evaluateOne(ctx context.Context, actual *evalset.Invocation, evalMetric *metric.EvalMetric) (float64, string, []*evalresult.RubricScore) {
	userContent := model.Message{Role: model.RoleUser, Content: ""}
	if actual != nil && actual.UserContent != nil {
		userContent = *actual.UserContent
	}
	// Get teacher reference output.
	teacherOutput, err := e.teacher.Get(ctx, userContent)
	if err != nil {
		fallback := e.fallbackJudgeOutput(evalMetric, issues.Issue{
			Severity: issues.SeverityP0,
			Key:      "teacher_failed",
			Summary:  fmt.Sprintf("Teacher failed: %v", err),
			Action:   "检查 teacher 模型与提示词，确保能稳定产出 JSON 输出。",
		})
		return 0.0, fallback, rubricScoresFromFallback(evalMetric, "Teacher failed.")
	}
	// Collect candidate output.
	candidateOutput := ""
	if actual != nil && actual.FinalResponse != nil {
		candidateOutput = actual.FinalResponse.Content
	}
	// Render judge prompt.
	rubricsText := formatRubrics(evalMetric.Criterion.LLMJudge.Rubrics)
	prompt, err := e.renderJudgePrompt(judgePromptData{
		UserInput:       userContent.Content,
		CandidateOutput: candidateOutput,
		TeacherOutput:   teacherOutput,
		Rubrics:         rubricsText,
	})
	if err != nil {
		fallback := e.fallbackJudgeOutput(evalMetric, issues.Issue{
			Severity: issues.SeverityP0,
			Key:      "judge_prompt_render_failed",
			Summary:  fmt.Sprintf("Render judge prompt failed: %v", err),
			Action:   "检查 judge_critic 模板占位符与渲染逻辑，减少模板复杂度。",
		})
		return 0.0, fallback, rubricScoresFromFallback(evalMetric, "Render judge prompt failed.")
	}
	// Call judge and parse its JSON output.
	parsed, raw, err := e.callJudgeAndParse(ctx, evalMetric, prompt)
	if err != nil {
		issue := issues.Issue{
			Severity: issues.SeverityP0,
			Key:      "judge_output_invalid_json",
			Summary:  fmt.Sprintf("Judge output is not valid JSON: %v", err),
			Action:   "在 judge_critic 中强调“只输出 JSON”，并使用更严格的输出格式约束。",
		}
		if raw != "" {
			issue.Summary = truncate(issue.Summary+" | raw="+raw, 800)
		}
		fallback := e.fallbackJudgeOutput(evalMetric, issue)
		return 0.0, fallback, rubricScoresFromFallback(evalMetric, "Judge output invalid.")
	}
	score, rubricScores := scoreFromJudgeOutput(evalMetric, parsed)
	reason := raw
	if reason == "" {
		if b, err := json.Marshal(parsed); err == nil {
			reason = string(b)
		}
	}
	return score, reason, rubricScores
}

func (e *llmRubricCriticEvaluator) renderJudgePrompt(data judgePromptData) (string, error) {
	var buf bytes.Buffer
	if err := e.judgeTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (e *llmRubricCriticEvaluator) callJudgeAndParse(ctx context.Context, evalMetric *metric.EvalMetric, prompt string) (issues.JudgeOutput, string, error) {
	raw, err := e.callJudgeOnce(ctx, evalMetric, prompt)
	if err != nil {
		return issues.JudgeOutput{}, "", err
	}
	parsed, perr := parseJudgeOutput(raw)
	if perr == nil {
		return parsed, strings.TrimSpace(raw), nil
	}
	raw2, err2 := e.callJudgeOnce(ctx, evalMetric, prompt)
	if err2 != nil {
		return issues.JudgeOutput{}, strings.TrimSpace(raw), perr
	}
	parsed2, perr2 := parseJudgeOutput(raw2)
	if perr2 != nil {
		return issues.JudgeOutput{}, strings.TrimSpace(raw2), perr2
	}
	return parsed2, strings.TrimSpace(raw2), nil
}

func (e *llmRubricCriticEvaluator) callJudgeOnce(ctx context.Context, evalMetric *metric.EvalMetric, prompt string) (raw string, retErr error) {
	judge := evalMetric.Criterion.LLMJudge.JudgeModel
	gen := judge.Generation
	if gen == nil {
		gen = &criterionllm.DefaultGeneration
	}
	genConfig := *gen
	genConfig.Stream = false
	m, err := provider.Model(
		judge.ProviderName,
		judge.ModelName,
		provider.WithAPIKey(judge.APIKey),
		provider.WithBaseURL(judge.BaseURL),
		provider.WithExtraFields(judge.ExtraFields),
	)
	if err != nil {
		return "", fmt.Errorf("create judge model instance: %w", err)
	}
	ag := llmagent.New(
		"judge_critic",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithStructuredOutputJSONSchema("judge_output", e.outputSchema, true, "Rubric verdicts and prompt gradient issues."),
	)
	r := runner.NewRunner("promptiter_judge", ag)
	defer func() {
		if err := r.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close judge runner: %w", err))
		}
	}()
	sessionID := uuid.NewString()
	events, err := r.Run(ctx, "judge_user", sessionID, model.Message{Role: model.RoleUser, Content: prompt})
	if err != nil {
		return "", fmt.Errorf("run judge agent: %w", err)
	}
	return captureJudgeFinalContent(events)
}

func captureJudgeFinalContent(events <-chan *event.Event) (string, error) {
	var final *model.Message
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return "", fmt.Errorf("judge event error: %v", evt.Error)
		}
		if evt.IsFinalResponse() {
			if len(evt.Response.Choices) == 0 {
				return "", errors.New("judge final response has no choices")
			}
			final = &evt.Response.Choices[0].Message
		}
	}
	if final == nil {
		return "", errors.New("judge did not return a final response")
	}
	return final.Content, nil
}

func parseJudgeOutput(raw string) (issues.JudgeOutput, error) {
	var out issues.JudgeOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return issues.JudgeOutput{}, err
	}
	return out, nil
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

func rubricScoresFromFallback(evalMetric *metric.EvalMetric, reason string) []*evalresult.RubricScore {
	rubricScores := make([]*evalresult.RubricScore, 0, len(evalMetric.Criterion.LLMJudge.Rubrics))
	for _, r := range evalMetric.Criterion.LLMJudge.Rubrics {
		rubricScores = append(rubricScores, &evalresult.RubricScore{
			ID:     r.ID,
			Reason: reason,
			Score:  0.0,
		})
	}
	return rubricScores
}

func (e *llmRubricCriticEvaluator) fallbackJudgeOutput(evalMetric *metric.EvalMetric, iss issues.Issue) string {
	out := issues.JudgeOutput{
		Rubrics: make([]issues.JudgeRubric, 0, len(evalMetric.Criterion.LLMJudge.Rubrics)),
	}
	for _, r := range evalMetric.Criterion.LLMJudge.Rubrics {
		out.Rubrics = append(out.Rubrics, issues.JudgeRubric{
			ID:      r.ID,
			Verdict: "no",
			Reason:  "Judge output unavailable.",
		})
	}
	out.Gradient.Issues = []issues.Issue{{
		Severity: iss.Severity,
		Key:      iss.Key,
		Summary:  iss.Summary,
		Action:   iss.Action,
	}}
	b, err := json.Marshal(out)
	if err != nil {
		return "{\"rubrics\":[],\"gradient\":{\"issues\":[]}}"
	}
	return string(b)
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

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func judgeOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"rubrics", "gradient"},
		"properties": map[string]any{
			"rubrics": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"id", "verdict", "reason"},
					"properties": map[string]any{
						"id": map[string]any{"type": "string", "minLength": 1},
						"verdict": map[string]any{
							"type": "string",
							"enum": []any{"yes", "no"},
						},
						"reason": map[string]any{"type": "string"},
					},
				},
			},
			"gradient": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []any{"issues"},
				"properties": map[string]any{
					"issues": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []any{"severity", "key", "summary", "action"},
							"properties": map[string]any{
								"severity": map[string]any{
									"type": "string",
									"enum": []any{"P0", "P1"},
								},
								"key":     map[string]any{"type": "string", "minLength": 1},
								"summary": map[string]any{"type": "string", "minLength": 1},
								"action":  map[string]any{"type": "string", "minLength": 1},
							},
						},
					},
				},
			},
		},
	}
}
