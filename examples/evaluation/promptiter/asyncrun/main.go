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
	"log"
	"time"
)

var (
	dataDir                   = flag.String("data-dir", "./data", "Directory containing evaluation set and metric files")
	outputDir                 = flag.String("output-dir", "./output", "Directory where evaluation results will be stored")
	modelName                 = flag.String("model", "deepseek-v3-local-II", "Model identifier used by the candidate agent")
	candidateInstruction      = flag.String("candidate-instruction", defaultCandidateInstruction, "Instruction used by the candidate agent")
	judgeModelName            = flag.String("judge-model", "gpt-5.4", "Model identifier used by the judge agent")
	workerModelName           = flag.String("worker-model", "gpt-5.4", "Model identifier used by the PromptIter worker agent")
	numRuns                   = flag.Int("runs", 1, "Number of evaluation repeats per case")
	maxRounds                 = flag.Int("max-rounds", 4, "Maximum PromptIter optimization rounds")
	evalCaseParallelism       = flag.Int("eval-case-parallelism", 8, "Maximum number of eval cases processed in parallel")
	parallelInferenceEnabled  = flag.Bool("parallel-inference", true, "Enable parallel inference across eval cases")
	parallelEvaluationEnabled = flag.Bool("parallel-evaluation", true, "Enable parallel evaluation across eval cases")
	pollInterval              = flag.Duration("poll-interval", time.Second, "Polling interval used to wait for asynchronous run completion")
	debugIO                   = flag.Bool("debug-io", false, "Log candidate, teacher, judge, and PromptIter worker inputs and outputs for troubleshooting")
)

func main() {
	flag.Parse()
	logger := log.New(log.Writer(), "", log.LstdFlags|log.Lmicroseconds)
	if err := runAsyncRunExample(context.Background(), asyncRunConfig{
		DataDir:                   *dataDir,
		OutputDir:                 *outputDir,
		CandidateModelName:        *modelName,
		CandidateInstruction:      *candidateInstruction,
		JudgeModelName:            *judgeModelName,
		WorkerModelName:           *workerModelName,
		NumRuns:                   *numRuns,
		MaxRounds:                 *maxRounds,
		EvalCaseParallelism:       *evalCaseParallelism,
		ParallelInferenceEnabled:  *parallelInferenceEnabled,
		ParallelEvaluationEnabled: *parallelEvaluationEnabled,
		PollInterval:              *pollInterval,
		DebugIO:                   *debugIO,
		Logger:                    logger,
	}); err != nil {
		log.Fatal(err)
	}
}
