//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolretry

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CallFunc executes one raw tool-call attempt.
type CallFunc func(context.Context, []byte) (any, error)

// ResultErrorFunc classifies whether a raw tool result represents a result-level failure.
type ResultErrorFunc func(any) bool

// TerminalErrorFunc reports whether an error must not be retried.
type TerminalErrorFunc func(error) bool

// ExecuteInput contains the inputs required to execute a retryable tool call.
type ExecuteInput struct {
	ToolName        string
	ToolCallID      string
	Arguments       []byte
	Policy          *tool.RetryPolicy
	Call            CallFunc
	ResultError     ResultErrorFunc
	IsTerminalError TerminalErrorFunc
}

// Result contains the final outcome of the tool-call runner.
type Result struct {
	Result any
	Error  error
}

// Execute runs a tool call with the configured retry policy.
func Execute(ctx context.Context, input ExecuteInput) Result {
	if input.Call == nil {
		return Result{
			Error: errors.New("tool retry runner requires a call function"),
		}
	}
	policy := input.Policy
	maxAttempts := 1
	if policy != nil && policy.MaxAttempts > 1 {
		maxAttempts = policy.MaxAttempts
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{Error: ctxErr}
		}
		rawResult, rawErr := input.Call(ctx, input.Arguments)
		resultError := false
		if input.ResultError != nil {
			resultError = input.ResultError(rawResult)
		}
		if rawErr == nil && !resultError {
			return Result{Result: rawResult}
		}
		if policy == nil || attempt == maxAttempts {
			if resultError && rawErr == nil {
				return Result{Result: rawResult}
			}
			return Result{Result: rawResult, Error: rawErr}
		}
		if input.IsTerminalError != nil && rawErr != nil && input.IsTerminalError(rawErr) {
			return Result{Result: rawResult, Error: rawErr}
		}
		info := &tool.RetryInfo{
			ToolName:    input.ToolName,
			ToolCallID:  input.ToolCallID,
			Arguments:   cloneBytes(input.Arguments),
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
			Result:      rawResult,
			Error:       rawErr,
			ResultError: resultError,
		}
		retryOn := policy.RetryOn
		if retryOn == nil {
			retryOn = tool.DefaultRetryOn
		}
		shouldRetry, err := retryOn(ctx, info)
		if err != nil {
			return Result{
				Result: rawResult,
				Error:  joinPolicyEvaluationError(rawErr, err),
			}
		}
		if !shouldRetry {
			if resultError && rawErr == nil {
				return Result{Result: rawResult}
			}
			return Result{Result: rawResult, Error: rawErr}
		}
		if err := sleepWithPolicy(ctx, *policy, attempt); err != nil {
			return Result{Result: rawResult, Error: err}
		}
	}
	return Result{}
}

func sleepWithPolicy(
	ctx context.Context,
	policy tool.RetryPolicy,
	attempt int,
) error {
	delay := computeDelay(policy, attempt)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func computeDelay(
	policy tool.RetryPolicy,
	attempt int,
) time.Duration {
	if policy.InitialInterval <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	factor := policy.BackoffFactor
	if factor <= 1 {
		factor = 1
	}
	delay := float64(policy.InitialInterval)
	if attempt > 1 {
		delay *= math.Pow(factor, float64(attempt-1))
	}
	if policy.MaxInterval > 0 {
		delay = math.Min(delay, float64(policy.MaxInterval))
	}
	result := time.Duration(delay)
	if !policy.Jitter || result <= 0 {
		return result
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(result)))
	if err != nil {
		return result
	}
	return result + time.Duration(n.Int64())
}

func joinPolicyEvaluationError(
	rawErr error,
	policyErr error,
) error {
	if policyErr == nil {
		return rawErr
	}
	wrapped := fmt.Errorf("tool retry policy evaluation failed: %w", policyErr)
	if rawErr == nil {
		return wrapped
	}
	return errors.Join(rawErr, wrapped)
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
