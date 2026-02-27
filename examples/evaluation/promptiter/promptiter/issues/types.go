//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package issues

// Severity indicates the priority level for a prompt issue.
type Severity string

const (
	// SeverityP0 indicates a must-fix issue.
	SeverityP0 Severity = "P0"
	// SeverityP1 indicates an important but non-blocking issue.
	SeverityP1 Severity = "P1"
)

// Issue is a normalized prompt issue extracted from evaluations.
type Issue struct {
	// Severity is the priority level of the issue.
	Severity Severity `json:"severity,omitempty"`
	// Key is a stable identifier used for deduplication.
	Key string `json:"key,omitempty"`
	// Summary describes the observed problem in a concise form.
	Summary string `json:"summary,omitempty"`
	// Action describes how to update the prompt to address the issue.
	Action string `json:"action,omitempty"`
}

// IssueRecord attaches an issue to a specific eval case and metric.
type IssueRecord struct {
	// Issue carries the normalized issue details.
	Issue
	// EvalSetID is the eval set identifier where the issue is observed.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// EvalCaseID is the eval case identifier where the issue is observed.
	EvalCaseID string `json:"eval_case_id,omitempty"`
	// MetricName is the metric name that produced the issue, when applicable.
	MetricName string `json:"metric_name,omitempty"`
}

// JudgeOutput is the expected JSON payload produced by llm_rubric_critic judge.
type JudgeOutput struct {
	// Rubrics contains per-rubric verdicts produced by the judge.
	Rubrics []JudgeRubric `json:"rubrics,omitempty"`
	// Gradient contains prompt issues extracted by the judge.
	Gradient struct {
		// Issues are normalized prompt issues suggested by the judge.
		Issues []Issue `json:"issues,omitempty"`
	} `json:"gradient,omitempty"`
}

// JudgeRubric captures per-rubric verdict details.
type JudgeRubric struct {
	// ID is the rubric identifier.
	ID string `json:"id,omitempty"`
	// Verdict is the rubric verdict value.
	Verdict string `json:"verdict,omitempty"`
	// Reason explains the rubric verdict.
	Reason string `json:"reason,omitempty"`
}

// AggregatedGradient is the output of the gradient aggregator agent.
type AggregatedGradient struct {
	// Issues is a deduplicated list of aggregated issues.
	Issues []AggregatedIssue `json:"issues,omitempty"`
	// BySection maps issue keys to the target prompt section ids.
	BySection map[string][]string `json:"by_section,omitempty"`
	// Notes contains optional global guidance for the optimizer.
	Notes string `json:"notes,omitempty"`
}

// AggregatedIssue is a deduplicated issue with representative cases.
type AggregatedIssue struct {
	// Severity is the highest severity observed for the issue.
	Severity Severity `json:"severity,omitempty"`
	// Key is a stable identifier used for deduplication.
	Key string `json:"key,omitempty"`
	// Summary describes the issue in a concise form.
	Summary string `json:"summary,omitempty"`
	// Action describes the intended prompt change.
	Action string `json:"action,omitempty"`
	// Cases lists representative eval case references for debugging.
	Cases []string `json:"cases,omitempty"`
}
