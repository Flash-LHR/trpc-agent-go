//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package service provides evaluate service.
package service

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Service defines the interface that an evaluation service must satisfy.
// It covers two phases: inference to capture agent responses, and evaluation to
// score those responses with the configured metrics.
type Service interface {
	// Inference runs the agent for the requested eval cases and returns the inference results for each case.
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)
	// Evaluate runs the evaluation on the inference results and returns the case evaluation results.
	Evaluate(ctx context.Context, request *EvaluateRequest) ([]*evalresult.EvalCaseResult, error)
}

// InferenceRequest represents a request for running the agent inference on an eval set.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type InferenceRequest struct {
	// AppName is the name of the app.
	AppName string `json:"appName"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"evalSetID"`
	// EvalCaseIDs are the IDs of eval cases to process.
	// If not specified, all eval cases in the eval set will be processed.
	EvalCaseIDs []string `json:"evalCaseIDs"`
}

// InferenceResult contains the inference results for a single eval case.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type InferenceResult struct {
	// AppName is the name of the app.
	AppName string `json:"appName"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"evalSetID"`
	// EvalCaseID is the ID of the eval case.
	EvalCaseID string `json:"evalCaseID"`
	// Inferences are the inference results.
	Inferences []*evalset.Invocation `json:"inferences"`
	// SessionID is the ID of the inference session.
	SessionID string `json:"sessionID"`
	// Status is the status of the inference.
	Status status.EvalStatus `json:"status"`
	// ErrorMessage contains the error message if inference failed.
	ErrorMessage string `json:"errorMessage"`
}

// EvaluateRequest represents a request for running the evaluation on the inference results.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvaluateRequest struct {
	// InferenceResults are the inference results to be evaluated.
	InferenceResults []*InferenceResult `json:"inferenceResults"`
	// EvaluateConfig contains the evaluation configuration used during evaluation.
	EvaluateConfig *EvaluateConfig `json:"evaluateConfig"`
}

// EvaluateConfig contains evaluation configuration used during evaluation.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvaluateConfig struct {
	// EvalMetrics contains the metrics to be evaluated.
	EvalMetrics []*metric.EvalMetric `json:"evalMetrics"`
}
