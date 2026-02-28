//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package issues

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

const (
	metricJSONSchema = "json_schema"
	metricLLMCritic  = "llm_critic"
)

// ExtractFromCaseResult extracts normalized issues from a single eval case result.
func ExtractFromCaseResult(evalSetID string, caseResult *evalresult.EvalCaseResult) []IssueRecord {
	if caseResult == nil {
		return nil
	}
	out := make([]IssueRecord, 0)
	// Record case-level failures.
	if strings.TrimSpace(caseResult.ErrorMessage) != "" {
		out = append(out, IssueRecord{
			Issue: Issue{
				Severity: SeverityP0,
				Key:      "case_failed",
				Summary:  strings.TrimSpace(caseResult.ErrorMessage),
				Action:   "检查输入与推理/评估链路是否正常，确保每个 case 都能产生 candidate 输出。",
			},
			EvalSetID:  evalSetID,
			EvalCaseID: caseResult.EvalID,
			MetricName: "",
		})
	}
	// Extract metric-derived issues.
	for _, perInv := range caseResult.EvalMetricResultPerInvocation {
		if perInv == nil {
			continue
		}
		for _, metricResult := range perInv.EvalMetricResults {
			if metricResult == nil || metricResult.Details == nil {
				continue
			}
			switch metricResult.MetricName {
			case metricJSONSchema:
				if metricResult.Score >= metricResult.Threshold {
					continue
				}
				reason := strings.TrimSpace(metricResult.Details.Reason)
				if reason == "" {
					reason = "JSON schema validation failed."
				}
				out = append(out, IssueRecord{
					Issue: Issue{
						Severity: SeverityP0,
						Key:      "json_schema_invalid",
						Summary:  reason,
						Action:   "在 output_contract 中强化“仅输出 JSON、仅包含 title/content、不得额外字段”，并明确 content 允许的格式与边界。",
					},
					EvalSetID:  evalSetID,
					EvalCaseID: caseResult.EvalID,
					MetricName: metricResult.MetricName,
				})
			case metricLLMCritic:
				judgeJSON := strings.TrimSpace(metricResult.Details.Reason)
				if judgeJSON == "" {
					out = append(out, IssueRecord{
						Issue: Issue{
							Severity: SeverityP0,
							Key:      "judge_empty_reason",
							Summary:  "Judge returned empty reason.",
							Action:   "检查 judge_critic 提示词，确保输出严格 JSON，并包含 issues[]。",
						},
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
					continue
				}
				var parsed JudgeOutput
				if err := json.Unmarshal([]byte(judgeJSON), &parsed); err != nil {
					out = append(out, IssueRecord{
						Issue: Issue{
							Severity: SeverityP0,
							Key:      "judge_output_invalid_json",
							Summary:  fmt.Sprintf("Failed to parse judge output JSON: %v", err),
							Action:   "在 judge_critic 中强调“只输出 JSON”，并减少歧义；必要时降低输出长度与增加示例。",
						},
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
					continue
				}
				for _, iss := range parsed.Issues {
					normalized := normalizeIssue(iss)
					out = append(out, IssueRecord{
						Issue:      normalized,
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
				}
			}
		}
	}
	return out
}

func normalizeIssue(in Issue) Issue {
	if in.Severity != SeverityP0 && in.Severity != SeverityP1 {
		in.Severity = SeverityP1
	}
	in.Key = strings.TrimSpace(in.Key)
	in.Summary = strings.TrimSpace(in.Summary)
	in.Action = strings.TrimSpace(in.Action)
	if in.Key == "" {
		in.Key = "unspecified"
	}
	if in.Summary == "" {
		in.Summary = "No summary."
	}
	if in.Action == "" {
		in.Action = "Update the prompt to address this issue."
	}
	return in
}
