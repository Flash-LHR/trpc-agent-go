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
	"strings"

	promptconfig "trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/config"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func parseFlags() promptconfig.Config {
	cfg := promptconfig.DefaultConfig()
	flag.StringVar(&cfg.AppName, "app", cfg.AppName, "App name used to locate evalset/metrics under data-dir")
	evalsetSet := false
	flag.Func("evalset", "Eval set id (repeatable or comma-separated); omit to run all evalsets under app", func(v string) error {
		if !evalsetSet {
			cfg.EvalSetIDs = nil
			evalsetSet = true
		}
		for part := range strings.SplitSeq(v, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			cfg.EvalSetIDs = append(cfg.EvalSetIDs, id)
		}
		return nil
	})
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "Directory containing evalset and metrics files")
	flag.StringVar(&cfg.OutputDir, "out-dir", cfg.OutputDir, "Directory to store iteration artifacts")
	flag.StringVar(&cfg.SchemaPath, "schema", cfg.SchemaPath, "Output JSON schema path")
	flag.IntVar(&cfg.MaxIters, "iters", cfg.MaxIters, "Max iteration rounds")
	flag.StringVar(&cfg.CandidateModel.ModelName, "candidate-model", cfg.CandidateModel.ModelName, "Candidate model name")
	flag.StringVar(&cfg.TeacherModel.ModelName, "teacher-model", cfg.TeacherModel.ModelName, "Teacher model name")
	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()
	// Build and run orchestrator.
	ctx := context.Background()
	orch, err := orchestrator.New(ctx, cfg)
	if err != nil {
		log.Fatalf("create orchestrator: %v", err)
	}
	defer func() {
		if err := orch.Close(); err != nil {
			log.Errorf("close orchestrator: %v", err)
		}
	}()
	if err := orch.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
	log.Infof("✅ Done. Artifacts saved under: %s\n", cfg.OutputDir)
}
