//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// Evaluate runs evaluation metrics on inference results.
func Evaluate(
	ctx context.Context,
	svc service.Service,
	appName string,
	evalSetID string,
	inferenceResults []*service.InferenceResult,
	evalMetrics []*metric.EvalMetric,
) (*service.EvalSetRunResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("evaluation service is nil")
	}
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: evalMetrics,
		},
	}
	return svc.Evaluate(ctx, req)
}

// LoadEvalMetrics loads all metrics for the given app and evalset from the provided metric manager.
func LoadEvalMetrics(ctx context.Context, metricMgr metric.Manager, appName, evalSetID string) ([]*metric.EvalMetric, error) {
	if metricMgr == nil {
		return nil, fmt.Errorf("metric manager is nil")
	}
	names, err := metricMgr.List(ctx, appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}
	out := make([]*metric.EvalMetric, 0, len(names))
	for _, name := range names {
		m, err := metricMgr.Get(ctx, appName, evalSetID, name)
		if err != nil {
			return nil, fmt.Errorf("get metric %s: %w", name, err)
		}
		out = append(out, m)
	}
	return out, nil
}
