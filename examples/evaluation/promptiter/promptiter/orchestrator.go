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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/agent/teacher"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/evaluators"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/issues"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/iterfs"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/promptmd"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Orchestrator drives the multi-iteration prompt improvement loop.
type Orchestrator struct {
	cfg               Config
	iterFS            *iterfs.IterFS
	evalSetMgr        evalset.Manager
	metricMgr         metric.Manager
	evaluator         evaluation.AgentEvaluator
	registry          registry.Registry
	evalSetIDs        []string
	outputSchema      map[string]any
	outputSchemaBytes []byte
	candidateRunner   runner.Runner
	teacherRunner     runner.Runner
	teacher           *teacher.Teacher
	aggregator        *aggregator.Aggregator
	optimizer         *optimizer.Optimizer
}

// NewOrchestrator builds all runtime dependencies.
func NewOrchestrator(ctx context.Context, cfg Config) (orch *Orchestrator, err error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Load schema and prompts.
	schemaBytes, schemaMap, err := readJSONFile(cfg.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("load output schema: %w", err)
	}
	_, aggSchemaMap, err := readJSONFile(cfg.AggregatedGradientSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("load aggregated gradient schema: %w", err)
	}
	teacherPrompt, err := os.ReadFile(cfg.TeacherPromptPath)
	if err != nil {
		return nil, fmt.Errorf("read teacher prompt: %w", err)
	}
	orch = &Orchestrator{
		cfg:               cfg,
		iterFS:            iterfs.New(cfg.OutputDir),
		outputSchema:      schemaMap,
		outputSchemaBytes: schemaBytes,
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, orch.Close())
		}
	}()
	// Build runners.
	orch.candidateRunner, err = newCandidateRunner(cfg.AppName, cfg.CandidateModel, schemaMap)
	if err != nil {
		return nil, err
	}
	orch.teacherRunner, orch.teacher, err = newTeacher(cfg, teacherPrompt, schemaBytes, schemaMap)
	if err != nil {
		return nil, err
	}
	// Build managers.
	orch.evalSetMgr = evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	orch.metricMgr = metriclocal.New(metric.WithBaseDir(cfg.DataDir))
	// Register evaluators.
	orch.registry = registry.New()
	schemaEval, err := evaluators.NewJSONSchemaValid(cfg.SchemaPath)
	if err != nil {
		return nil, err
	}
	criticEval, err := evaluators.NewLLMRubricCritic(orch.teacher, cfg.JudgePromptPath)
	if err != nil {
		return nil, err
	}
	if err := orch.registry.Register(schemaEval.Name(), schemaEval); err != nil {
		return nil, fmt.Errorf("register evaluator %s: %w", schemaEval.Name(), err)
	}
	if err := orch.registry.Register(criticEval.Name(), criticEval); err != nil {
		return nil, fmt.Errorf("register evaluator %s: %w", criticEval.Name(), err)
	}
	orch.evaluator, err = evaluation.New(
		cfg.AppName,
		orch.candidateRunner,
		evaluation.WithEvalSetManager(orch.evalSetMgr),
		evaluation.WithMetricManager(orch.metricMgr),
		evaluation.WithEvalResultManager(inmemory.New()),
		evaluation.WithRegistry(orch.registry),
	)
	if err != nil {
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	// Load evalsets.
	orch.evalSetIDs, err = resolveEvalSetIDs(ctx, orch.evalSetMgr, cfg.AppName, cfg.EvalSetIDs)
	if err != nil {
		return nil, err
	}
	for _, evalSetID := range orch.evalSetIDs {
		if _, err := orch.evalSetMgr.Get(ctx, cfg.AppName, evalSetID); err != nil {
			return nil, fmt.Errorf("load evalset %s: %w", evalSetID, err)
		}
	}
	// Build loop agents.
	aggregatorModel, err := provider.Model(
		cfg.AggregatorModel.ProviderName,
		cfg.AggregatorModel.ModelName,
		provider.WithAPIKey(cfg.AggregatorModel.APIKey),
		provider.WithBaseURL(cfg.AggregatorModel.BaseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("create aggregator model: %w", err)
	}
	orch.aggregator, err = aggregator.New(aggregatorModel, cfg.AggregatorModel.Generation, cfg.GradientAggregatorPromptPath, aggSchemaMap)
	if err != nil {
		return nil, err
	}
	optimizerModel, err := provider.Model(
		cfg.OptimizerModel.ProviderName,
		cfg.OptimizerModel.ModelName,
		provider.WithAPIKey(cfg.OptimizerModel.APIKey),
		provider.WithBaseURL(cfg.OptimizerModel.BaseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("create optimizer model: %w", err)
	}
	orch.optimizer, err = optimizer.New(optimizerModel, cfg.OptimizerModel.Generation, cfg.PromptOptimizerPromptPath, cfg.OutputDir)
	if err != nil {
		return nil, err
	}
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
	if o.candidateRunner != nil {
		errs = append(errs, o.candidateRunner.Close())
	}
	if o.teacherRunner != nil {
		errs = append(errs, o.teacherRunner.Close())
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
		// Prepare iteration files.
		if _, err := o.iterFS.CopyFile(iter, basePromptPath, "prompt_before.md"); err != nil {
			return fmt.Errorf("write prompt_before: %w", err)
		}
		if _, err := o.iterFS.CopyFile(iter, basePromptPath, "prompt.md"); err != nil {
			return fmt.Errorf("write prompt.md: %w", err)
		}
		// Load and parse current prompt.
		promptBytes, _, err := o.iterFS.ReadFile(iter, "prompt.md")
		if err != nil {
			return fmt.Errorf("read prompt.md: %w", err)
		}
		promptText := string(promptBytes)
		beforeDoc, err := promptmd.Parse(promptText)
		if err != nil {
			return fmt.Errorf("parse prompt.md: %w", err)
		}
		// Run candidate inference and evaluation for each evalset.
		runResults := make(map[string]*evalresult.EvalSetResult, len(o.evalSetIDs))
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
			runResults[evalSetID] = result.EvalResult
			if !evalSetPassed(result.EvalResult) {
				allPassed = false
			}
			evalDir := filepath.Join("evalsets", safePathSegment(evalSetID))
			if _, err := o.iterFS.WriteJSON(iter, filepath.Join(evalDir, "evalset_result.json"), result.EvalResult); err != nil {
				return fmt.Errorf("write evalset_result.json for %s: %w", evalSetID, err)
			}
			if err := ensureMetricsEvaluated(evalSetID, result.EvalResult); err != nil {
				return err
			}
			for _, cr := range result.EvalResult.EvalCaseResults {
				rawIssues = append(rawIssues, issues.ExtractFromCaseResult(evalSetID, cr)...)
			}
		}
		// Stop early if all metrics passed.
		if allPassed {
			if _, err := o.iterFS.WriteJSON(iter, "aggregated_gradient.json", &issues.AggregatedGradient{
				Issues:    []issues.AggregatedIssue{},
				BySection: map[string][]string{},
				Notes:     "all_metrics_passed",
			}); err != nil {
				return fmt.Errorf("write aggregated_gradient.json: %w", err)
			}
			if _, err := o.iterFS.WriteFile(iter, "prompt_after.md", promptBytes); err != nil {
				return fmt.Errorf("write prompt_after.md: %w", err)
			}
			if _, err := o.iterFS.WriteJSON(iter, "optimizer_changes.json", optimizerChanges{
				NoChange:        true,
				ChangedSections: []string{},
			}); err != nil {
				return fmt.Errorf("write optimizer_changes.json: %w", err)
			}
			return nil
		}
		// Aggregate gradient.
		examples := o.buildAggregatorExamples(ctx, runResults, rawIssues)
		aggGradient, _, aggErr := o.aggregator.Aggregate(ctx, beforeDoc.SectionIDs(), rawIssues, examples)
		if aggErr != nil {
			aggGradient = fallbackAggregate(rawIssues, beforeDoc.SectionIDs())
			aggGradient.Notes = "fallback_aggregator_used"
		}
		if _, err := o.iterFS.WriteJSON(iter, "aggregated_gradient.json", aggGradient); err != nil {
			return fmt.Errorf("write aggregated_gradient.json: %w", err)
		}
		// Optimize prompt using file tools.
		iterRelDir := filepath.Base(iterDir)
		userMessage := fmt.Sprintf("请根据 %s/aggregated_gradient.json 修改 %s/prompt.md。优先修复 P0，再处理 P1。修改要最小且精准。不得修改其他文件。", iterRelDir, iterRelDir)
		_, err = o.optimizer.Optimize(ctx, userMessage)
		if err != nil {
			return fmt.Errorf("optimizer: %w", err)
		}
		// Validate optimized prompt sections and persist artifacts.
		afterBytes, _, err := o.iterFS.ReadFile(iter, "prompt.md")
		if err != nil {
			return fmt.Errorf("read optimized prompt.md: %w", err)
		}
		afterDoc, err := promptmd.Parse(string(afterBytes))
		if err != nil {
			return fmt.Errorf("parse optimized prompt.md: %w", err)
		}
		if err := promptmd.ValidateStable(beforeDoc, afterDoc); err != nil {
			return fmt.Errorf("prompt section_id changed: %w", err)
		}
		changedSections, err := promptmd.ChangedSectionIDs(beforeDoc, afterDoc)
		if err != nil {
			return err
		}
		if _, err := o.iterFS.WriteFile(iter, "prompt_after.md", afterBytes); err != nil {
			return fmt.Errorf("write prompt_after.md: %w", err)
		}
		if _, err := o.iterFS.WriteJSON(iter, "optimizer_changes.json", optimizerChanges{
			NoChange:        len(changedSections) == 0,
			ChangedSections: changedSections,
		}); err != nil {
			return fmt.Errorf("write optimizer_changes.json: %w", err)
		}
		// Stop if optimizer made no changes.
		if len(changedSections) == 0 {
			return nil
		}
		// Use the optimized prompt for the next iteration.
		basePromptPath = filepath.Join(iterDir, "prompt_after.md")
	}
	return nil
}

type optimizerChanges struct {
	// NoChange indicates whether the optimizer made any edits to the prompt.
	NoChange bool `json:"no_change,omitempty"`
	// ChangedSections lists the section ids whose bodies changed after optimization.
	ChangedSections []string `json:"changed_sections,omitempty"`
}

type aggregatorExample struct {
	// EvalSetID is the identifier of the eval set that produced this example.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// EvalCaseID is the identifier of the eval case that produced this example.
	EvalCaseID string `json:"eval_case_id,omitempty"`
	// UserInput is the raw user input content for the case.
	UserInput string `json:"user_input,omitempty"`
	// CandidateOutput is the candidate final response content for the case.
	CandidateOutput string `json:"candidate_output,omitempty"`
	// TeacherOutput is the cached teacher reference output for the case.
	TeacherOutput string `json:"teacher_output,omitempty"`
	// MetricReasons stores per-metric details for the case.
	MetricReasons map[string]string `json:"metric_reasons,omitempty"`
}

func (o *Orchestrator) buildAggregatorExamples(ctx context.Context, runResults map[string]*evalresult.EvalSetResult, rawIssues []issues.IssueRecord) []aggregatorExample {
	p0Cases := make(map[string]struct{})
	for _, r := range rawIssues {
		if r.Severity == issues.SeverityP0 {
			p0Cases[evalCaseKey(r.EvalSetID, r.EvalCaseID)] = struct{}{}
		}
	}
	examples := make([]aggregatorExample, 0, 3)
	for _, evalSetID := range o.evalSetIDs {
		runResult := runResults[evalSetID]
		if runResult == nil {
			continue
		}
		for _, cr := range runResult.EvalCaseResults {
			if cr == nil {
				continue
			}
			if _, ok := p0Cases[evalCaseKey(evalSetID, cr.EvalID)]; !ok && cr.FinalEvalStatus == status.EvalStatusPassed {
				continue
			}
			ex := aggregatorExample{
				EvalSetID:     evalSetID,
				EvalCaseID:    cr.EvalID,
				MetricReasons: make(map[string]string),
			}
			var actual *evalset.Invocation
			if len(cr.EvalMetricResultPerInvocation) > 0 && cr.EvalMetricResultPerInvocation[0] != nil {
				actual = cr.EvalMetricResultPerInvocation[0].ActualInvocation
				for _, mr := range cr.EvalMetricResultPerInvocation[0].EvalMetricResults {
					if mr != nil && mr.Details != nil && mr.MetricName != "" {
						ex.MetricReasons[mr.MetricName] = mr.Details.Reason
					}
				}
			}
			if actual != nil && actual.UserContent != nil {
				ex.UserInput = actual.UserContent.Content
				teacherOut, err := o.teacher.Get(ctx, *actual.UserContent)
				if err != nil {
					ex.MetricReasons["teacher_error"] = fmt.Sprintf("teacher get failed: %v", err)
				} else {
					ex.TeacherOutput = teacherOut
				}
			}
			if actual != nil && actual.FinalResponse != nil {
				ex.CandidateOutput = actual.FinalResponse.Content
			}
			examples = append(examples, ex)
			if len(examples) >= 3 {
				return examples
			}
		}
	}
	return examples
}

func evalCaseKey(evalSetID string, evalCaseID string) string {
	return strings.TrimSpace(evalSetID) + ":" + strings.TrimSpace(evalCaseID)
}

func evalSetPassed(runResult *evalresult.EvalSetResult) bool {
	if runResult == nil {
		return false
	}
	if runResult.Summary != nil && len(runResult.Summary.EvalCaseSummaries) > 0 {
		for _, cs := range runResult.Summary.EvalCaseSummaries {
			if cs == nil || cs.OverallStatus != status.EvalStatusPassed {
				return false
			}
		}
		return true
	}
	if len(runResult.EvalCaseResults) == 0 {
		return false
	}
	for _, cr := range runResult.EvalCaseResults {
		if cr == nil || cr.FinalEvalStatus != status.EvalStatusPassed {
			return false
		}
	}
	return true
}

func ensureMetricsEvaluated(evalSetID string, runResult *evalresult.EvalSetResult) error {
	if runResult == nil {
		return fmt.Errorf("eval set result for %s is nil", evalSetID)
	}
	if len(runResult.EvalCaseResults) == 0 {
		return fmt.Errorf("eval set %s produced no case results", evalSetID)
	}
	for _, cr := range runResult.EvalCaseResults {
		if cr == nil {
			return fmt.Errorf("eval set %s contains nil case result", evalSetID)
		}
		if len(cr.OverallEvalMetricResults) == 0 {
			return fmt.Errorf("no metrics evaluated for evalset %s case %s", evalSetID, cr.EvalID)
		}
	}
	return nil
}

func fallbackAggregate(raw []issues.IssueRecord, sectionIDs []string) *issues.AggregatedGradient {
	byKey := make(map[string]*issues.AggregatedIssue)
	for _, r := range raw {
		key := r.Key
		if key == "" {
			key = "unspecified"
		}
		existing, ok := byKey[key]
		if !ok {
			byKey[key] = &issues.AggregatedIssue{
				Severity: r.Severity,
				Key:      key,
				Summary:  r.Summary,
				Action:   r.Action,
				Cases:    []string{formatCaseRef(r)},
			}
			continue
		}
		if existing.Severity != issues.SeverityP0 && r.Severity == issues.SeverityP0 {
			existing.Severity = issues.SeverityP0
		}
		if existing.Summary == "" && r.Summary != "" {
			existing.Summary = r.Summary
		}
		if existing.Action == "" && r.Action != "" {
			existing.Action = r.Action
		}
		if ref := formatCaseRef(r); ref != "" {
			existing.Cases = append(existing.Cases, ref)
		}
	}
	issuesList := make([]issues.AggregatedIssue, 0, len(byKey))
	for _, v := range byKey {
		if v == nil {
			continue
		}
		v.Cases = uniqueStrings(v.Cases)
		issuesList = append(issuesList, *v)
	}
	sort.Slice(issuesList, func(i, j int) bool {
		if issuesList[i].Severity != issuesList[j].Severity {
			return issuesList[i].Severity < issuesList[j].Severity
		}
		return issuesList[i].Key < issuesList[j].Key
	})
	// Build a deterministic fallback mapping from issues to sections.
	out := &issues.AggregatedGradient{
		Issues:    issuesList,
		BySection: make(map[string][]string),
	}
	outputContract := pickSection(sectionIDs, "output_contract")
	inputSection := pickSection(sectionIDs, "input")
	for _, it := range issuesList {
		switch {
		case stringsContainsAny(it.Key, "json", "schema"):
			if outputContract != "" {
				out.BySection[it.Key] = []string{outputContract}
			}
		case stringsContainsAny(it.Key, "unknown", "missing", "inconsistent"):
			if inputSection != "" {
				out.BySection[it.Key] = []string{inputSection}
			}
		}
	}
	return out
}

func formatCaseRef(r issues.IssueRecord) string {
	evalCaseID := strings.TrimSpace(r.EvalCaseID)
	if evalCaseID == "" {
		return ""
	}
	evalSetID := strings.TrimSpace(r.EvalSetID)
	if evalSetID == "" {
		return evalCaseID
	}
	return evalSetID + ":" + evalCaseID
}

func pickSection(sectionIDs []string, wanted string) string {
	for _, s := range sectionIDs {
		if s == wanted {
			return s
		}
	}
	if len(sectionIDs) > 0 {
		return sectionIDs[0]
	}
	return ""
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func stringsContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func safePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}

func resolveEvalSetIDs(ctx context.Context, mgr evalset.Manager, appName string, requested []string) ([]string, error) {
	if mgr == nil {
		return nil, errors.New("evalset manager is nil")
	}
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("app name is empty")
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(requested))
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range requested {
		add(id)
	}
	if len(out) == 0 {
		ids, err := mgr.List(ctx, appName)
		if err != nil {
			return nil, fmt.Errorf("list evalsets: %w", err)
		}
		for _, id := range ids {
			add(id)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no evalsets found for app %s", appName)
	}
	return out, nil
}

func readJSONFile(path string) ([]byte, map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, err
	}
	return b, m, nil
}

func newCandidateRunner(appName string, cfg ModelConfig, outputSchema map[string]any) (runner.Runner, error) {
	cfg = resolveDeepSeekDefaults(cfg)
	m, err := provider.Model(
		cfg.ProviderName,
		cfg.ModelName,
		provider.WithAPIKey(cfg.APIKey),
		provider.WithBaseURL(cfg.BaseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("create candidate model: %w", err)
	}
	ag := llmagent.New(
		"candidate",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(cfg.Generation),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	return runner.NewRunner(appName, ag), nil
}

func newTeacher(cfg Config, teacherPrompt []byte, schemaBytes []byte, outputSchema map[string]any) (runner.Runner, *teacher.Teacher, error) {
	m, err := provider.Model(
		cfg.TeacherModel.ProviderName,
		cfg.TeacherModel.ModelName,
		provider.WithAPIKey(cfg.TeacherModel.APIKey),
		provider.WithBaseURL(cfg.TeacherModel.BaseURL),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create teacher model: %w", err)
	}
	ag := llmagent.New(
		"teacher",
		llmagent.WithModel(m),
		llmagent.WithInstruction(string(teacherPrompt)),
		llmagent.WithGenerationConfig(cfg.TeacherModel.Generation),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	r := runner.NewRunner("promptiter_teacher", ag)
	t, err := teacher.New(r, string(teacherPrompt), schemaBytes)
	if err != nil {
		return nil, nil, errors.Join(err, r.Close())
	}
	return r, t, nil
}

func resolveDeepSeekDefaults(cfg ModelConfig) ModelConfig {
	if strings.ToLower(cfg.ProviderName) != "openai" {
		return cfg
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.ModelName)), "deepseek") {
		return cfg
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		if v := strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL")); v != "" {
			cfg.BaseURL = v
		} else {
			cfg.BaseURL = "https://api.deepseek.com"
		}
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	return cfg
}
