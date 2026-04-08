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
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type retryableResult struct {
	value any
	fail  bool
}

func (r *retryableResult) RetryResultError() bool {
	return r.fail
}

func TestExecute_RetriesRawErrorAndEventuallySucceeds(t *testing.T) {
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"x":1}`),
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			if attempts == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return map[string]any{"ok": true}, nil
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, 2, attempts)
	require.Equal(t, map[string]any{"ok": true}, result.Result)
}

func TestExecute_RetriesResultErrorWhenRetryOnAllowsIt(t *testing.T) {
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return info.ResultError, nil
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			if attempts == 1 {
				return &retryableResult{value: "first", fail: true}, nil
			}
			return &retryableResult{value: "second"}, nil
		},
		ResultError: func(result any) bool {
			rg, ok := result.(interface{ RetryResultError() bool })
			return ok && rg.RetryResultError()
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, 2, attempts)
	finalResult, ok := result.Result.(*retryableResult)
	require.True(t, ok)
	require.Equal(t, "second", finalResult.value)
}

func TestExecute_StopsWhenRetryPolicyEvaluationFails(t *testing.T) {
	callErr := errors.New("call failed")
	policyErr := errors.New("policy failed")
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return false, policyErr
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			return "partial", callErr
		},
	})
	require.Error(t, result.Error)
	require.ErrorIs(t, result.Error, callErr)
	require.ErrorIs(t, result.Error, policyErr)
	require.Equal(t, "partial", result.Result)
}

func TestExecute_DoesNotRetryTerminalErrors(t *testing.T) {
	stopErr := errors.New("stop")
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     3,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return true, nil
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			return nil, stopErr
		},
		IsTerminalError: func(err error) bool {
			return errors.Is(err, stopErr)
		},
	})
	require.ErrorIs(t, result.Error, stopErr)
	require.Equal(t, 1, attempts)
}
