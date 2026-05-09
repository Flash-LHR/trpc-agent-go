//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package engine

// RunResultSlimming controls which PromptIter run result fields are omitted
// from persisted or returned copies.
//
// The zero value keeps every field so existing callers keep the full result.
type RunResultSlimming struct {
	// OmitStructure removes the exported agent structure snapshot.
	OmitStructure bool
	// OmitEvaluationCases removes per-case evaluation details from all phases.
	OmitEvaluationCases bool
	// OmitBackward removes per-case backward results from each round.
	OmitBackward bool
	// OmitAggregation removes aggregated surface gradients from each round.
	OmitAggregation bool
	// OmitPatches removes optimizer patch proposals from each round.
	OmitPatches bool
	// OmitProfiles removes input, output, and accepted profiles.
	OmitProfiles bool
	// OmitLosses removes round loss details.
	OmitLosses bool
}

// IsZero reports whether the slimming policy preserves the full result.
func (s RunResultSlimming) IsZero() bool {
	return !s.OmitStructure &&
		!s.OmitEvaluationCases &&
		!s.OmitBackward &&
		!s.OmitAggregation &&
		!s.OmitPatches &&
		!s.OmitProfiles &&
		!s.OmitLosses
}

// SlimRunResult returns a copy of result with fields removed by slimming.
func SlimRunResult(result *RunResult, slimming RunResultSlimming) *RunResult {
	if result == nil || slimming.IsZero() {
		return result
	}
	slimmed := &RunResult{
		ID:                 result.ID,
		Status:             result.Status,
		CurrentRound:       result.CurrentRound,
		BaselineValidation: slimEvaluationResult(result.BaselineValidation, slimming),
		Rounds:             slimRounds(result.Rounds, slimming),
		ErrorMessage:       result.ErrorMessage,
	}
	if !slimming.OmitStructure {
		slimmed.Structure = result.Structure
	}
	if !slimming.OmitProfiles {
		slimmed.AcceptedProfile = result.AcceptedProfile
	}
	return slimmed
}

func slimRounds(rounds []RoundResult, slimming RunResultSlimming) []RoundResult {
	if len(rounds) == 0 {
		return nil
	}
	slimmed := make([]RoundResult, 0, len(rounds))
	for _, round := range rounds {
		next := RoundResult{
			Round:      round.Round,
			Train:      slimEvaluationResult(round.Train, slimming),
			Validation: slimEvaluationResult(round.Validation, slimming),
			Acceptance: round.Acceptance,
			Stop:       round.Stop,
		}
		if !slimming.OmitProfiles {
			next.InputProfile = round.InputProfile
			next.OutputProfile = round.OutputProfile
		}
		if !slimming.OmitLosses {
			next.Losses = round.Losses
		}
		if !slimming.OmitBackward {
			next.Backward = round.Backward
		}
		if !slimming.OmitAggregation {
			next.Aggregation = round.Aggregation
		}
		if !slimming.OmitPatches {
			next.Patches = round.Patches
		}
		slimmed = append(slimmed, next)
	}
	return slimmed
}

func slimEvaluationResult(result *EvaluationResult, slimming RunResultSlimming) *EvaluationResult {
	if result == nil {
		return nil
	}
	if !slimming.OmitEvaluationCases {
		return result
	}
	slimmed := &EvaluationResult{
		OverallScore: result.OverallScore,
		EvalSets:     make([]EvalSetResult, 0, len(result.EvalSets)),
	}
	for _, evalSet := range result.EvalSets {
		slimmed.EvalSets = append(slimmed.EvalSets, EvalSetResult{
			EvalSetID:    evalSet.EvalSetID,
			OverallScore: evalSet.OverallScore,
		})
	}
	return slimmed
}
