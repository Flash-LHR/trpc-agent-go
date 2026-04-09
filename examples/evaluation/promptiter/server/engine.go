//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	promptitermanager "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName             = "promptiter-nba-commentary-app"
	candidateAppName    = "promptiter-nba-commentary-candidate"
	judgeAppName        = "promptiter-nba-commentary-judge"
	promptIterWorkerApp = "promptiter-nba-commentary-worker"
	sharedMetricFileID  = "sports-commentary"
)

type serverConfig struct {
	Addr                      string
	BasePath                  string
	DataDir                   string
	OutputDir                 string
	ModelName                 string
	NumRuns                   int
	EvalCaseParallelism       int
	ParallelInferenceEnabled  bool
	ParallelEvaluationEnabled bool
	DebugIO                   bool
	Logger                    *log.Logger
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	engine  promptiterengine.Engine
	manager promptitermanager.Manager
	close   func()
}

func buildPromptIterRuntime(ctx context.Context, cfg serverConfig) (*promptIterRuntime, error) {
	m, err := loadOpenAIModel(cfg.ModelName)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	if (cfg.ParallelInferenceEnabled || cfg.ParallelEvaluationEnabled) && cfg.EvalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0 when parallel inference or evaluation is enabled")
	}
	candidateAgent, err := newCandidateAgent(m)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	judgeAgent := newJudgeAgent(m)
	teacherAgent := newTeacherAgent(m)
	promptIterWorker := newPromptIterWorkerAgent(m)
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	judgeRunner := runner.NewRunner(judgeAppName, judgeAgent)
	teacherRunner := runner.NewRunner("promptiter-nba-commentary-teacher", teacherAgent)
	workerRunner := runner.NewRunner(promptIterWorkerApp, promptIterWorker)
	logger := cfg.Logger
	candidateLoggedRunner := newLoggingRunner("candidate", candidateRunner, logger, cfg.DebugIO)
	judgeLoggedRunner := newLoggingRunner("judge", judgeRunner, logger, cfg.DebugIO)
	teacherLoggedRunner := newLoggingRunner("teacher", teacherRunner, logger, cfg.DebugIO)
	backwarderRunner := newLoggingRunner("backwarder", workerRunner, logger, cfg.DebugIO)
	aggregatorRunner := newLoggingRunner("aggregator", workerRunner, logger, cfg.DebugIO)
	optimizerRunner := newLoggingRunner("optimizer", workerRunner, logger, cfg.DebugIO)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		teacherRunner.Close()
		workerRunner.Close()
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: sharedMetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	registry := registry.New()
	if err := registry.Register(commentaryLengthMetricName, newCommentaryLengthEvaluator()); err != nil {
		closeAll()
		return nil, fmt.Errorf("register commentary length evaluator: %w", err)
	}
	agentEvaluator, err := evaluation.New(
		appName,
		candidateLoggedRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithExpectedRunner(teacherLoggedRunner),
		evaluation.WithJudgeRunner(judgeLoggedRunner),
		evaluation.WithNumRuns(cfg.NumRuns),
		evaluation.WithEvalCaseParallelism(cfg.EvalCaseParallelism),
		evaluation.WithEvalCaseParallelInferenceEnabled(cfg.ParallelInferenceEnabled),
		evaluation.WithEvalCaseParallelEvaluationEnabled(cfg.ParallelEvaluationEnabled),
	)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	engineInstance, err := promptiterengine.New(
		ctx,
		candidateAgent,
		agentEvaluator,
		backwarderInstance,
		aggregatorInstance,
		optimizerInstance,
	)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	managerInstance, err := promptitermanager.New(engineInstance)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter manager: %w", err)
	}
	return &promptIterRuntime{
		engine:  engineInstance,
		manager: managerInstance,
		close: func() {
			managerInstance.Close()
			agentEvaluator.Close()
			closeAll()
		},
	}, nil
}

// Build maps every eval set to the shared metric file used by the example.
func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	name := strings.TrimSpace(modelName)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	switch {
	case name == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := make([]openai.Option, 0, 2)
	options = append(options, openai.WithAPIKey(apiKey))
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
