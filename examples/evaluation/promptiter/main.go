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

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter"
)

type evalsetIDsFlag struct {
	ids *[]string
	set bool
}

// String formats the flag value for logging and help output.
func (f *evalsetIDsFlag) String() string {
	if f == nil || f.ids == nil {
		return ""
	}
	return strings.Join(*f.ids, ",")
}

// Set appends repeatable or comma-separated evalset ids.
func (f *evalsetIDsFlag) Set(v string) error {
	if f == nil || f.ids == nil {
		return fmt.Errorf("evalset ids flag is not initialized")
	}
	if !f.set {
		*f.ids = nil
		f.set = true
	}
	for _, part := range strings.Split(v, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		*f.ids = append(*f.ids, id)
	}
	return nil
}

func main() {
	cfg := promptiter.DefaultConfig()
	// Configure flags.
	flag.StringVar(&cfg.AppName, "app", cfg.AppName, "App name used to locate evalset/metrics under data-dir")
	flag.Var(&evalsetIDsFlag{ids: &cfg.EvalSetIDs}, "evalset", "Eval set id (repeatable or comma-separated); omit to run all evalsets under app")
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "Directory containing evalset and metrics files")
	flag.StringVar(&cfg.OutputDir, "out-dir", cfg.OutputDir, "Directory to store iteration artifacts")
	flag.StringVar(&cfg.SchemaPath, "schema", cfg.SchemaPath, "Output JSON schema path")
	flag.IntVar(&cfg.MaxIters, "iters", cfg.MaxIters, "Max iteration rounds")
	flag.StringVar(&cfg.CandidateModel.ModelName, "candidate-model", cfg.CandidateModel.ModelName, "Candidate model name")
	flag.StringVar(&cfg.TeacherModel.ModelName, "teacher-model", cfg.TeacherModel.ModelName, "Teacher model name")
	flag.Parse()
	// Build and run orchestrator.
	ctx := context.Background()
	orch, err := promptiter.NewOrchestrator(ctx, cfg)
	if err != nil {
		log.Fatalf("create orchestrator: %v", err)
	}
	defer func() {
		if err := orch.Close(); err != nil {
			log.Printf("close orchestrator: %v", err)
		}
	}()
	if err := orch.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
	fmt.Printf("✅ Done. Artifacts saved under: %s\n", cfg.OutputDir)
}
