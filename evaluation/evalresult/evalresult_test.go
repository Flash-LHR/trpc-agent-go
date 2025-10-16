//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestEvalSetResultJSONRoundTrip(t *testing.T) {
	const raw = `{
  "evalSetResultId": "result-1",
  "evalSetResultName": "result-name",
  "evalSetId": "greeting-set",
  "evalCaseResults": [
    {
      "evalSetId": "greeting-set",
      "evalId": "case-1",
      "finalEvalStatus": 1,
      "overallEvalMetricResults": [
        {
          "metricName": "tool_trajectory_avg_score",
          "score": 0.9,
          "status": 1,
          "threshold": 0.8,
          "details": {
            "comment": "trajectory matched"
          }
        }
      ],
      "evalMetricResultPerInvocation": [
        {
          "actualInvocation": {
            "invocationId": "invocation-actual",
            "userContent": {
              "role": "user",
              "parts": [
                {
                  "text": "calculate 1 + 2."
                }
              ]
            },
            "finalResponse": {
              "role": "assistant",
              "parts": [
                {
                  "text": "final: 1+2=3."
                }
              ]
            },
            "intermediateData": {
              "toolUses": [
                {
                  "id": "tool-call-1",
                  "name": "calculator",
                  "args": {
                    "operation": "add",
                    "a": 1,
                    "b": 2
                  }
                }
              ],
              "toolResponses": [],
              "intermediateResponses": [
                [
                  "assistant",
                  [
                    {
                      "text": "thinking..."
                    }
                  ]
                ]
              ]
            },
            "creationTimestamp": 1700000000
          },
          "expectedInvocation": {
            "invocationId": "invocation-expected",
            "userContent": {
              "role": "user",
              "parts": [
                {
                  "text": "calculate 1 + 2."
                }
              ]
            },
            "finalResponse": {
              "role": "assistant",
              "parts": [
                {
                  "text": "final: 1+2=3."
                }
              ]
            },
            "intermediateData": {
              "toolUses": [
                {
                  "name": "calculator",
                  "args": {
                    "operation": "add",
                    "a": 1,
                    "b": 2
                  }
                }
              ],
              "toolResponses": [],
              "intermediateResponses": [
                [
                  "assistant",
                  [
                    {
                      "text": "thinking..."
                    }
                  ]
                ]
              ]
            },
            "creationTimestamp": 1700000000
          },
          "metricResults": [
            {
              "metricName": "tool_trajectory_avg_score",
              "score": 0.9,
              "status": 1,
              "threshold": 0.8,
              "details": {
                "comment": "per invocation matched"
              }
            }
          ]
        }
      ],
      "sessionId": "session-1",
      "userId": "user-1"
    }
  ],
  "creationTimestamp": 1700000000
}`

	var result EvalSetResult
	err := json.Unmarshal([]byte(raw), &result)
	assert.NoError(t, err)

	assert.Equal(t, "result-1", result.EvalSetResultID)
	assert.Equal(t, "result-name", result.EvalSetResultName)
	assert.Equal(t, "greeting-set", result.EvalSetID)
	assert.NotNil(t, result.CreationTimestamp)
	assert.Equal(t, int64(1700000000), result.CreationTimestamp.Time.Unix())
	assert.Len(t, result.EvalCaseResults, 1)

	caseResult := result.EvalCaseResults[0]
	assert.Equal(t, "case-1", caseResult.EvalID)
	assert.Equal(t, status.EvalStatusPassed, caseResult.FinalEvalStatus)
	assert.Equal(t, "greeting-set", caseResult.EvalSetID)
	assert.Len(t, caseResult.OverallEvalMetricResults, 1)
	assert.Len(t, caseResult.EvalMetricResultPerInvocation, 1)

	overallMetric := caseResult.OverallEvalMetricResults[0]
	assert.Equal(t, "tool_trajectory_avg_score", overallMetric.MetricName)
	assert.Equal(t, 0.9, overallMetric.Score)
	assert.Equal(t, status.EvalStatusPassed, overallMetric.Status)
	assert.Equal(t, 0.8, overallMetric.Threshold)
	assert.Equal(t, "trajectory matched", overallMetric.Details["comment"])

	perInvocation := caseResult.EvalMetricResultPerInvocation[0]
	assert.NotNil(t, perInvocation.ActualInvocation)
	assert.NotNil(t, perInvocation.ExpectedInvocation)
	assert.Equal(t, "invocation-actual", perInvocation.ActualInvocation.InvocationID)
	assert.Equal(t, "invocation-expected", perInvocation.ExpectedInvocation.InvocationID)
	assert.Len(t, perInvocation.MetricResults, 1)

	perMetric := perInvocation.MetricResults[0]
	assert.Equal(t, "tool_trajectory_avg_score", perMetric.MetricName)
	assert.Equal(t, 0.9, perMetric.Score)
	assert.Equal(t, status.EvalStatusPassed, perMetric.Status)
	assert.Equal(t, 0.8, perMetric.Threshold)
	assert.Equal(t, "per invocation matched", perMetric.Details["comment"])

	encoded, marshalErr := json.Marshal(result)
	assert.NoError(t, marshalErr)
	assert.JSONEq(t, raw, string(encoded))
}
