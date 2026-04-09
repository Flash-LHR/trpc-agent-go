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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type experimentConfig struct {
	Trials   int                 `json:"trials"`
	Variants []experimentVariant `json:"variants"`
}

type experimentVariant struct {
	Name                       string   `json:"name"`
	Description                string   `json:"description,omitempty"`
	CandidateInstruction       *string  `json:"candidate_instruction,omitempty"`
	CandidateModelName         *string  `json:"candidate_model,omitempty"`
	JudgeModelName             *string  `json:"judge_model,omitempty"`
	WorkerModelName            *string  `json:"worker_model,omitempty"`
	TeacherTemperature         *float64 `json:"teacher_temperature,omitempty"`
	BackwardInstruction        *string  `json:"backward_instruction,omitempty"`
	AggregatorInstruction      *string  `json:"aggregator_instruction,omitempty"`
	OptimizerInstruction       *string  `json:"optimizer_instruction,omitempty"`
	NumRuns                    *int     `json:"runs,omitempty"`
	MaxRounds                  *int     `json:"max_rounds,omitempty"`
	MinScoreGain               *float64 `json:"min_score_gain,omitempty"`
	MaxRoundsWithoutAcceptance *int     `json:"max_rounds_without_acceptance,omitempty"`
	TargetScore                *float64 `json:"target_score,omitempty"`
	EvalCaseParallelism        *int     `json:"eval_case_parallelism,omitempty"`
	ParallelInferenceEnabled   *bool    `json:"parallel_inference,omitempty"`
	ParallelEvaluationEnabled  *bool    `json:"parallel_evaluation,omitempty"`
}

type experimentSummary struct {
	ConfigPath   string                 `json:"config_path"`
	OutputRoot   string                 `json:"output_root"`
	Trials       int                    `json:"trials"`
	GeneratedAt  string                 `json:"generated_at"`
	VariantStats []experimentVariantRun `json:"variant_stats"`
}

type experimentVariantRun struct {
	Name                 string               `json:"name"`
	Description          string               `json:"description,omitempty"`
	Trials               int                  `json:"trials"`
	MeanInitialScore     float64              `json:"mean_initial_score"`
	MeanFinalScore       float64              `json:"mean_final_score"`
	MeanImprovement      float64              `json:"mean_improvement"`
	MedianFinalScore     float64              `json:"median_final_score"`
	MedianImprovement    float64              `json:"median_improvement"`
	MinFinalScore        float64              `json:"min_final_score"`
	MaxFinalScore        float64              `json:"max_final_score"`
	StdDevFinalScore     float64              `json:"stddev_final_score"`
	AcceptedRoundAverage float64              `json:"accepted_round_average"`
	AcceptedRoundCounts  map[string]int       `json:"accepted_round_counts"`
	TrialsSummary        []experimentTrialRun `json:"trials_summary"`
}

type experimentTrialRun struct {
	TrialIndex             int                    `json:"trial_index"`
	OutputDir              string                 `json:"output_dir"`
	InitialValidationScore float64                `json:"initial_validation_score"`
	FinalAcceptedScore     float64                `json:"final_accepted_score"`
	Improvement            float64                `json:"improvement"`
	AcceptedRound          int                    `json:"accepted_round"`
	AcceptedInstruction    string                 `json:"accepted_instruction"`
	RoundsExecuted         int                    `json:"rounds_executed"`
	Rounds                 []experimentRoundStats `json:"rounds"`
}

type experimentRoundStats struct {
	Round           int     `json:"round"`
	TrainScore      float64 `json:"train_score"`
	ValidationScore float64 `json:"validation_score"`
	Accepted        bool    `json:"accepted"`
	ScoreDelta      float64 `json:"score_delta"`
	Stop            bool    `json:"stop"`
	StopReason      string  `json:"stop_reason"`
}

func runExperiment(
	ctx context.Context,
	baseCfg syncRunConfig,
	configPath string,
	summaryPath string,
) error {
	config, err := loadExperimentConfig(configPath)
	if err != nil {
		return err
	}
	outputRoot := filepath.Join(baseCfg.OutputDir, "experiments", time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return fmt.Errorf("create experiment output root: %w", err)
	}
	summary := experimentSummary{
		ConfigPath:   configPath,
		OutputRoot:   outputRoot,
		Trials:       config.Trials,
		GeneratedAt:  time.Now().Format(time.RFC3339),
		VariantStats: make([]experimentVariantRun, 0, len(config.Variants)),
	}
	for _, variant := range config.Variants {
		variantCfg, err := variant.apply(baseCfg)
		if err != nil {
			return fmt.Errorf("apply variant %q: %w", variant.Name, err)
		}
		variantOutputRoot := filepath.Join(outputRoot, sanitizePathComponent(variant.Name))
		if err := os.MkdirAll(variantOutputRoot, 0o755); err != nil {
			return fmt.Errorf("create variant output root: %w", err)
		}
		trials := make([]experimentTrialRun, 0, config.Trials)
		for trialIndex := 1; trialIndex <= config.Trials; trialIndex++ {
			trialCfg := variantCfg
			trialCfg.OutputDir = filepath.Join(variantOutputRoot, fmt.Sprintf("trial-%02d", trialIndex))
			if err := os.MkdirAll(trialCfg.OutputDir, 0o755); err != nil {
				return fmt.Errorf("create trial output dir: %w", err)
			}
			result, targetSurfaceID, err := runSyncRun(ctx, trialCfg)
			if err != nil {
				return fmt.Errorf("run variant %q trial %d: %w", variant.Name, trialIndex, err)
			}
			trialSummary := summarizeTrial(result, trialCfg.CandidateInstruction, targetSurfaceID, trialCfg.OutputDir, trialIndex)
			trials = append(trials, trialSummary)
			fmt.Printf(
				"[experiment] variant=%s trial=%d/%d initial=%.2f final=%.2f delta=%.2f accepted_round=%d\n",
				variant.Name,
				trialIndex,
				config.Trials,
				trialSummary.InitialValidationScore,
				trialSummary.FinalAcceptedScore,
				trialSummary.Improvement,
				trialSummary.AcceptedRound,
			)
		}
		variantSummary := summarizeVariant(variant, trials)
		summary.VariantStats = append(summary.VariantStats, variantSummary)
	}
	sort.Slice(summary.VariantStats, func(i, j int) bool {
		if summary.VariantStats[i].MedianFinalScore == summary.VariantStats[j].MedianFinalScore {
			return summary.VariantStats[i].MeanFinalScore > summary.VariantStats[j].MeanFinalScore
		}
		return summary.VariantStats[i].MedianFinalScore > summary.VariantStats[j].MedianFinalScore
	})
	if summaryPath == "" {
		summaryPath = filepath.Join(outputRoot, "summary.json")
	}
	if err := writeExperimentSummary(summaryPath, &summary); err != nil {
		return err
	}
	printExperimentSummary(&summary)
	return nil
}

func loadExperimentConfig(path string) (*experimentConfig, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read experiment config: %w", err)
	}
	var config experimentConfig
	if err := json.Unmarshal(payload, &config); err != nil {
		return nil, fmt.Errorf("unmarshal experiment config: %w", err)
	}
	switch {
	case config.Trials <= 0:
		return nil, errors.New("experiment trials must be greater than 0")
	case len(config.Variants) == 0:
		return nil, errors.New("experiment variants are empty")
	}
	for _, variant := range config.Variants {
		if strings.TrimSpace(variant.Name) == "" {
			return nil, errors.New("experiment variant name is empty")
		}
	}
	return &config, nil
}

func (variant experimentVariant) apply(base syncRunConfig) (syncRunConfig, error) {
	cfg := base
	if variant.CandidateInstruction != nil {
		cfg.CandidateInstruction = *variant.CandidateInstruction
	}
	if variant.CandidateModelName != nil {
		cfg.CandidateModelName = *variant.CandidateModelName
	}
	if variant.JudgeModelName != nil {
		cfg.JudgeModelName = *variant.JudgeModelName
	}
	if variant.WorkerModelName != nil {
		cfg.WorkerModelName = *variant.WorkerModelName
	}
	if variant.TeacherTemperature != nil {
		cfg.TeacherTemperature = *variant.TeacherTemperature
	}
	if variant.BackwardInstruction != nil {
		cfg.BackwardInstruction = *variant.BackwardInstruction
	}
	if variant.AggregatorInstruction != nil {
		cfg.AggregatorInstruction = *variant.AggregatorInstruction
	}
	if variant.OptimizerInstruction != nil {
		cfg.OptimizerInstruction = *variant.OptimizerInstruction
	}
	if variant.NumRuns != nil {
		cfg.NumRuns = *variant.NumRuns
	}
	if variant.MaxRounds != nil {
		cfg.MaxRounds = *variant.MaxRounds
	}
	if variant.MinScoreGain != nil {
		cfg.MinScoreGain = *variant.MinScoreGain
	}
	if variant.MaxRoundsWithoutAcceptance != nil {
		cfg.MaxRoundsWithoutAcceptance = *variant.MaxRoundsWithoutAcceptance
	}
	if variant.TargetScore != nil {
		cfg.TargetScore = *variant.TargetScore
	}
	if variant.EvalCaseParallelism != nil {
		cfg.EvalCaseParallelism = *variant.EvalCaseParallelism
	}
	if variant.ParallelInferenceEnabled != nil {
		cfg.ParallelInferenceEnabled = *variant.ParallelInferenceEnabled
	}
	if variant.ParallelEvaluationEnabled != nil {
		cfg.ParallelEvaluationEnabled = *variant.ParallelEvaluationEnabled
	}
	switch {
	case cfg.MaxRounds <= 0:
		return syncRunConfig{}, errors.New("max rounds must be greater than 0")
	case cfg.MinScoreGain < 0:
		return syncRunConfig{}, errors.New("min score gain must be greater than or equal to 0")
	case cfg.TeacherTemperature < 0:
		return syncRunConfig{}, errors.New("teacher temperature must be greater than or equal to 0")
	case cfg.MaxRoundsWithoutAcceptance <= 0:
		return syncRunConfig{}, errors.New("max rounds without acceptance must be greater than 0")
	case cfg.TargetScore <= 0:
		return syncRunConfig{}, errors.New("target score must be greater than 0")
	}
	return cfg, nil
}

func summarizeTrial(
	result *promptiterengine.RunResult,
	initialInstruction string,
	targetSurfaceID string,
	outputDir string,
	trialIndex int,
) experimentTrialRun {
	rounds := make([]experimentRoundStats, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		rounds = append(rounds, experimentRoundStats{
			Round:           round.Round,
			TrainScore:      evaluationResultScore(round.Train),
			ValidationScore: evaluationResultScore(round.Validation),
			Accepted:        round.Acceptance.Accepted,
			ScoreDelta:      round.Acceptance.ScoreDelta,
			Stop:            round.Stop.ShouldStop,
			StopReason:      round.Stop.Reason,
		})
	}
	initialScore := initialValidationScore(result)
	finalScore := finalAcceptedValidationScore(result)
	return experimentTrialRun{
		TrialIndex:             trialIndex,
		OutputDir:              outputDir,
		InitialValidationScore: initialScore,
		FinalAcceptedScore:     finalScore,
		Improvement:            finalScore - initialScore,
		AcceptedRound:          acceptedRound(result),
		AcceptedInstruction:    acceptedInstructionText(result, initialInstruction, targetSurfaceID),
		RoundsExecuted:         len(result.Rounds),
		Rounds:                 rounds,
	}
}

func summarizeVariant(
	variant experimentVariant,
	trials []experimentTrialRun,
) experimentVariantRun {
	initialScores := make([]float64, 0, len(trials))
	finalScores := make([]float64, 0, len(trials))
	improvements := make([]float64, 0, len(trials))
	acceptedRounds := make([]float64, 0, len(trials))
	acceptedRoundCounts := make(map[string]int, len(trials))
	for _, trial := range trials {
		initialScores = append(initialScores, trial.InitialValidationScore)
		finalScores = append(finalScores, trial.FinalAcceptedScore)
		improvements = append(improvements, trial.Improvement)
		acceptedRounds = append(acceptedRounds, float64(trial.AcceptedRound))
		acceptedRoundCounts[fmt.Sprintf("%d", trial.AcceptedRound)]++
	}
	return experimentVariantRun{
		Name:                 variant.Name,
		Description:          variant.Description,
		Trials:               len(trials),
		MeanInitialScore:     meanFloat64(initialScores),
		MeanFinalScore:       meanFloat64(finalScores),
		MeanImprovement:      meanFloat64(improvements),
		MedianFinalScore:     medianFloat64(finalScores),
		MedianImprovement:    medianFloat64(improvements),
		MinFinalScore:        minFloat64(finalScores),
		MaxFinalScore:        maxFloat64(finalScores),
		StdDevFinalScore:     stddevFloat64(finalScores),
		AcceptedRoundAverage: meanFloat64(acceptedRounds),
		AcceptedRoundCounts:  acceptedRoundCounts,
		TrialsSummary:        trials,
	}
}

func writeExperimentSummary(path string, summary *experimentSummary) error {
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("create experiment summary dir: %w", err)
	}
	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal experiment summary: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write experiment summary: %w", err)
	}
	return nil
}

func printExperimentSummary(summary *experimentSummary) {
	fmt.Println("✅ PromptIter syncrun experiment completed")
	fmt.Printf("Experiment config: %s\n", summary.ConfigPath)
	fmt.Printf("Experiment output root: %s\n", summary.OutputRoot)
	fmt.Printf("Trials per variant: %d\n", summary.Trials)
	for _, variant := range summary.VariantStats {
		fmt.Printf(
			"Variant %s -> mean final %.3f, median final %.3f, stddev %.3f, mean delta %.3f, accepted round avg %.2f\n",
			variant.Name,
			variant.MeanFinalScore,
			variant.MedianFinalScore,
			variant.StdDevFinalScore,
			variant.MeanImprovement,
			variant.AcceptedRoundAverage,
		)
	}
}

func sanitizePathComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "variant"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
		case r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	sanitized := strings.Trim(builder.String(), "-")
	if sanitized == "" {
		return "variant"
	}
	return sanitized
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func medianFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func minFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minValue := values[0]
	for _, value := range values[1:] {
		if value < minValue {
			minValue = value
		}
	}
	return minValue
}

func maxFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func stddevFloat64(values []float64) float64 {
	if len(values) <= 1 {
		return 0
	}
	meanValue := meanFloat64(values)
	sumSquaredDiffs := 0.0
	for _, value := range values {
		diff := value - meanValue
		sumSquaredDiffs += diff * diff
	}
	return math.Sqrt(sumSquaredDiffs / float64(len(values)))
}
