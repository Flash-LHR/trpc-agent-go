//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/candidate"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/teacher"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/config"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/evaluator/jsonschema"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/evaluator/llmcritic"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/issues"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/iterfs"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	promptBeforeFilename      = "prompt_before.md"
	promptAfterFilename       = "prompt_after.md"
	aggregatedGradientRelPath = "aggregated_gradient.json"
	optimizerChangesRelPath   = "optimizer_changes.json"
)

// Orchestrator drives the multi-iteration prompt improvement loop.
type Orchestrator struct {
	cfg           config.Config
	iterFS        *iterfs.IterFS
	evalSetMgr    evalset.Manager
	metricMgr     metric.Manager
	evalResultMgr evalresult.Manager
	evaluator     evaluation.AgentEvaluator
	registry      registry.Registry
	evalSetIDs    []string
	candidate     runner.Runner
	teacher       *teacher.Teacher
	aggregator    *aggregator.Aggregator
	optimizer     *optimizer.Optimizer
}

// New builds all runtime dependencies.
func New(ctx context.Context, cfg config.Config) (*Orchestrator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	orch := &Orchestrator{
		cfg:    cfg,
		iterFS: iterfs.New(cfg.OutputDir),
	}
	fail := func(err error) (*Orchestrator, error) {
		return nil, errors.Join(err, orch.Close())
	}
	// Build agents.
	var err error
	orch.candidate, err = candidate.New(candidate.Config{
		AppName:          cfg.AppName,
		ProviderName:     cfg.CandidateModel.ProviderName,
		ModelName:        cfg.CandidateModel.ModelName,
		Generation:       cfg.CandidateModel.Generation,
		OutputSchemaPath: cfg.SchemaPath,
	})
	if err != nil {
		return fail(fmt.Errorf("create candidate runner: %w", err))
	}
	orch.teacher, err = teacher.New(teacher.Config{
		ProviderName:     cfg.TeacherModel.ProviderName,
		ModelName:        cfg.TeacherModel.ModelName,
		Generation:       cfg.TeacherModel.Generation,
		InstructionPath:  cfg.TeacherPromptPath,
		OutputSchemaPath: cfg.SchemaPath,
	})
	if err != nil {
		return fail(fmt.Errorf("create teacher: %w", err))
	}
	orch.aggregator, err = aggregator.New(aggregator.Config{
		ProviderName:       cfg.AggregatorModel.ProviderName,
		ModelName:          cfg.AggregatorModel.ModelName,
		Generation:         cfg.AggregatorModel.Generation,
		PromptTemplatePath: cfg.GradientAggregatorPromptPath,
		OutputSchemaPath:   cfg.AggregatedGradientSchemaPath,
	})
	if err != nil {
		return fail(fmt.Errorf("create aggregator: %w", err))
	}
	orch.optimizer, err = optimizer.New(optimizer.Config{
		ProviderName:    cfg.OptimizerModel.ProviderName,
		ModelName:       cfg.OptimizerModel.ModelName,
		Generation:      cfg.OptimizerModel.Generation,
		InstructionPath: cfg.PromptOptimizerPromptPath,
		BaseDir:         cfg.OutputDir,
	})
	if err != nil {
		return fail(fmt.Errorf("create optimizer: %w", err))
	}
	// Build managers.
	orch.evalSetMgr = evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	orch.metricMgr = metriclocal.New(metric.WithBaseDir(cfg.DataDir))
	evalResultBaseDir := filepath.Join(cfg.OutputDir, "evalresult")
	orch.evalResultMgr = evalresultlocal.New(evalresult.WithBaseDir(evalResultBaseDir))
	// Register evaluators.
	orch.registry = registry.New()
	jsonSchemaEvaluator, err := jsonschema.New(cfg.SchemaPath)
	if err != nil {
		return fail(fmt.Errorf("create schema evaluator: %w", err))
	}
	llmCriticEvaluator, err := llmcritic.New(orch.teacher, cfg.JudgePromptPath, cfg.JudgeOutputSchemaPath)
	if err != nil {
		return fail(fmt.Errorf("create critic evaluator: %w", err))
	}
	if err := orch.registry.Register(jsonSchemaEvaluator.Name(), jsonSchemaEvaluator); err != nil {
		return fail(fmt.Errorf("register evaluator %s: %w", jsonSchemaEvaluator.Name(), err))
	}
	if err := orch.registry.Register(llmCriticEvaluator.Name(), llmCriticEvaluator); err != nil {
		return fail(fmt.Errorf("register evaluator %s: %w", llmCriticEvaluator.Name(), err))
	}
	// Build agent evaluator.
	orch.evaluator, err = evaluation.New(
		cfg.AppName,
		orch.candidate,
		evaluation.WithEvalSetManager(orch.evalSetMgr),
		evaluation.WithMetricManager(orch.metricMgr),
		evaluation.WithEvalResultManager(orch.evalResultMgr),
		evaluation.WithRegistry(orch.registry),
	)
	if err != nil {
		return fail(fmt.Errorf("create evaluator: %w", err))
	}
	// Load evalsets.
	evalSetIDs := cfg.EvalSetIDs
	if len(evalSetIDs) == 0 {
		evalSetIDs, err = orch.evalSetMgr.List(ctx, cfg.AppName)
		if err != nil {
			return fail(fmt.Errorf("list evalsets: %w", err))
		}
	}
	orch.evalSetIDs = evalSetIDs
	return orch, nil
}

// Close releases owned resources.
func (o *Orchestrator) Close() error {
	var errs []error
	if o.evaluator != nil {
		errs = append(errs, o.evaluator.Close())
	} else {
		if o.evalSetMgr != nil {
			errs = append(errs, o.evalSetMgr.Close())
		}
		if o.metricMgr != nil {
			errs = append(errs, o.metricMgr.Close())
		}
	}
	if o.candidate != nil {
		errs = append(errs, o.candidate.Close())
	}
	if o.teacher != nil {
		errs = append(errs, o.teacher.Close())
	}
	if o.aggregator != nil {
		errs = append(errs, o.aggregator.Close())
	}
	if o.optimizer != nil {
		errs = append(errs, o.optimizer.Close())
	}
	return errors.Join(errs...)
}

// Run executes the closed-loop prompt iteration.
func (o *Orchestrator) Run(ctx context.Context) error {
	if len(o.evalSetIDs) == 0 {
		return errors.New("eval sets are empty")
	}
	if err := os.MkdirAll(o.cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	// Iterate prompt improvements.
	basePromptPath := o.cfg.TargetPromptPath
	for iter := 1; iter <= o.cfg.MaxIters; iter++ {
		iterDir, err := o.iterFS.EnsureIterDir(iter)
		if err != nil {
			return fmt.Errorf("ensure iter dir: %w", err)
		}
		iterRelDir := filepath.Base(iterDir)
		// Prepare iteration files.
		if _, err := o.iterFS.CopyFile(iter, basePromptPath, promptBeforeFilename); err != nil {
			return fmt.Errorf("write %s: %w", promptBeforeFilename, err)
		}
		if _, err := o.iterFS.CopyFile(iter, basePromptPath, promptAfterFilename); err != nil {
			return fmt.Errorf("write %s: %w", promptAfterFilename, err)
		}
		// Load current prompt.
		promptBytes, _, err := o.iterFS.ReadFile(iter, promptAfterFilename)
		if err != nil {
			return fmt.Errorf("read %s: %w", promptAfterFilename, err)
		}
		promptText := string(promptBytes)
		// Run candidate inference and evaluation for each evalset.
		rawIssues := make([]issues.IssueRecord, 0)
		allPassed := true
		for _, evalSetID := range o.evalSetIDs {
			result, err := o.evaluator.Evaluate(ctx, evalSetID, evaluation.WithRunOptions(agent.WithInstruction(promptText)))
			if err != nil {
				return fmt.Errorf("evaluate %s: %w", evalSetID, err)
			}
			if result == nil || result.EvalResult == nil {
				return fmt.Errorf("evaluation result for %s is nil", evalSetID)
			}
			if result.OverallStatus != status.EvalStatusPassed {
				allPassed = false
			}
			for _, cr := range result.EvalResult.EvalCaseResults {
				rawIssues = append(rawIssues, issues.ExtractFromCaseResult(evalSetID, cr)...)
			}
		}
		// Stop early if all metrics passed.
		if allPassed {
			if _, err := o.iterFS.WriteJSON(iter, aggregatedGradientRelPath, map[string]any{
				"issues": []issues.AggregatedIssue{},
				"notes":  "all_metrics_passed",
			}); err != nil {
				return fmt.Errorf("write %s: %w", aggregatedGradientRelPath, err)
			}
			if _, err := o.iterFS.WriteJSON(iter, optimizerChangesRelPath, optimizerChanges{
				ChangedSections: []string{},
			}); err != nil {
				return fmt.Errorf("write %s: %w", optimizerChangesRelPath, err)
			}
			return nil
		}
		// Aggregate gradient.
		aggGradient, aggErr := o.aggregator.Aggregate(ctx, rawIssues)
		if aggErr != nil {
			return fmt.Errorf("aggregate gradient: %w", aggErr)
		}
		if aggGradient == nil {
			return errors.New("aggregated gradient is nil")
		}
		issuesList := aggGradient.Issues
		if issuesList == nil {
			issuesList = []issues.AggregatedIssue{}
		}
		aggOut := map[string]any{
			"issues": issuesList,
		}
		if strings.TrimSpace(aggGradient.Notes) != "" {
			aggOut["notes"] = aggGradient.Notes
		}
		if _, err := o.iterFS.WriteJSON(iter, aggregatedGradientRelPath, aggOut); err != nil {
			return fmt.Errorf("write %s: %w", aggregatedGradientRelPath, err)
		}
		// Optimize prompt using file tools.
		userMessage := fmt.Sprintf("请根据 %s/%s 修改 %s/%s。", iterRelDir, aggregatedGradientRelPath, iterRelDir, promptAfterFilename)
		_, err = o.optimizer.Optimize(ctx, userMessage)
		if err != nil {
			return fmt.Errorf("optimizer: %w", err)
		}
		// Persist optimizer artifacts.
		if _, err := o.iterFS.WriteJSON(iter, optimizerChangesRelPath, optimizerChanges{
			ChangedSections: []string{},
		}); err != nil {
			return fmt.Errorf("write %s: %w", optimizerChangesRelPath, err)
		}
		// Use the optimized prompt for the next iteration.
		basePromptPath = filepath.Join(iterDir, promptAfterFilename)
	}
	return nil
}

type optimizerChanges struct {
	// ChangedSections is reserved for future section-level diffs.
	ChangedSections []string `json:"changed_sections,omitempty"`
}
