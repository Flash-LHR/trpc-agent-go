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
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/yanyiwu/gojieba"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	dataDir   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	evalSetID = flag.String("eval-set", "jieba-rouge-zh", "Evaluation set identifier to execute")
	modelName = flag.String("model", "gpt-5.2", "Model to use for evaluation runs")
	streaming = flag.Bool("streaming", false, "Enable streaming responses from the agent")
	numRuns   = flag.Int("runs", 1, "Number of times to repeat the evaluation loop per case")
)

const appName = "jieba-eval-app"

type jiebaTokenizer struct {
	segmenter *gojieba.Jieba
}

// Tokenize implements the ROUGE tokenizer interface with Jieba segmentation.
func (t jiebaTokenizer) Tokenize(text string) []string {
	segments := t.segmenter.Cut(text, true)
	tokens := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			tokens = append(tokens, segment)
		}
	}
	return tokens
}

func main() {
	flag.Parse()
	ctx := context.Background()
	segmenter := gojieba.NewJieba()
	defer segmenter.Free()
	run := runner.NewRunner(appName, newJiebaAgent(*modelName, *streaming))
	defer run.Close()
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*dataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(*dataDir))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
	evaluatorRegistry := registry.New()
	metricRegistry := metricregistry.New()
	if err := metricRegistry.RegisterRougeTokenizer("jieba", jiebaTokenizer{segmenter: segmenter}); err != nil {
		log.Fatalf("register jieba tokenizer: %v", err)
	}
	agentEvaluator, err := evaluation.New(
		appName,
		run,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(evaluatorRegistry),
		evaluation.WithMetricRegistry(metricRegistry),
		evaluation.WithNumRuns(*numRuns),
	)
	if err != nil {
		log.Fatalf("create evaluator: %v", err)
	}
	defer agentEvaluator.Close()
	result, err := agentEvaluator.Evaluate(ctx, *evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	printSummary(result, *outputDir)
}

// printSummary prints a short human-readable evaluation summary.
func printSummary(result *evaluation.EvaluationResult, outDir string) {
	fmt.Println("✅ Evaluation completed with ROUGE criterion")
	fmt.Printf("App: %s\n", result.AppName)
	fmt.Printf("Eval Set: %s\n", result.EvalSetID)
	fmt.Printf("Overall Status: %s\n", result.OverallStatus)
	runs := 0
	if len(result.EvalCases) > 0 {
		runs = len(result.EvalCases[0].EvalCaseResults)
	}
	fmt.Printf("Runs: %d\n", runs)
	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus)
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf("  Metric %s: score %.2f (threshold %.2f) => %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.EvalStatus,
			)
		}
		fmt.Println()
	}
	fmt.Printf("Results saved under: %s\n", outDir)
}
