//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNewInvocation_InitializesExecutionTraceMetadata(t *testing.T) {
	inv := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant/root~"}),
		WithInvocationSession(&session.Session{ID: "session-1"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	require.True(t, executionTraceEnabled(inv))
	assert.Equal(t, "assistant~1root~0", InvocationTraceNodeID(inv))
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	assert.Equal(t, "assistant/root~", executionTrace.RootAgentName)
	assert.Equal(t, inv.InvocationID, executionTrace.RootInvocationID)
	assert.Equal(t, "session-1", executionTrace.SessionID)
	assert.Equal(t, atrace.TraceStatusCompleted, executionTrace.Status)
	assert.Empty(t, executionTrace.Steps)
}

func TestClone_PreservesExecutionTraceCaptureAndEntryPredecessors(t *testing.T) {
	root := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	rootStepID := StartExecutionTraceStep(
		root,
		InvocationTraceNodeID(root),
		&atrace.Snapshot{Text: "root input"},
		nil,
	)
	FinishExecutionTraceStep(root, rootStepID, &atrace.Snapshot{Text: "root output"}, nil)
	child := root.Clone(
		WithInvocationAgent(&mockAgent{name: "worker"}),
		WithInvocationTraceNodeID("assistant/worker"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	require.True(t, executionTraceEnabled(child))
	childStepID := StartExecutionTraceStep(
		child,
		InvocationTraceNodeID(child),
		&atrace.Snapshot{Text: "child input"},
		nil,
	)
	FinishExecutionTraceStep(child, childStepID, &atrace.Snapshot{Text: "child output"}, nil)
	executionTrace := BuildExecutionTrace(root, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 2)
	assert.Equal(t, rootStepID, executionTrace.Steps[0].StepID)
	assert.Equal(t, child.InvocationID, executionTrace.Steps[1].InvocationID)
	assert.Equal(t, []string{rootStepID}, executionTrace.Steps[1].PredecessorStepIDs)
	assert.Equal(t, "assistant/worker", executionTrace.Steps[1].NodeID)
}

func TestExecutionTrace_LazilyInitializesForDirectInvocationLiteral(t *testing.T) {
	inv := &Invocation{
		InvocationID: "inv-1",
		AgentName:    "assistant",
		RunOptions:   RunOptions{ExecutionTraceEnabled: true},
	}
	stepID := StartExecutionTraceStep(
		inv,
		InvocationTraceNodeID(inv),
		&atrace.Snapshot{Text: "input"},
		nil,
	)
	require.NotEmpty(t, stepID)
	FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "output"}, nil)
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 1)
	assert.Equal(t, "assistant", executionTrace.RootAgentName)
	assert.Equal(t, "assistant", executionTrace.Steps[0].NodeID)
}

func TestNextExecutionTracePredecessors_UsesNestedChildInvocationTerminals(t *testing.T) {
	root := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "workflow"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	rootStepID := StartExecutionTraceStep(
		root,
		InvocationTraceNodeID(root),
		&atrace.Snapshot{Text: "root input"},
		nil,
	)
	FinishExecutionTraceStep(root, rootStepID, &atrace.Snapshot{Text: "root output"}, nil)
	middle := root.Clone(
		WithInvocationAgent(&mockAgent{name: "fanout"}),
		WithInvocationTraceNodeID("workflow/fanout"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	leaf := middle.Clone(
		WithInvocationAgent(&mockAgent{name: "worker"}),
		WithInvocationTraceNodeID("workflow/fanout/worker"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	leafStepID := StartExecutionTraceStep(
		leaf,
		InvocationTraceNodeID(leaf),
		&atrace.Snapshot{Text: "leaf input"},
		nil,
	)
	FinishExecutionTraceStep(leaf, leafStepID, &atrace.Snapshot{Text: "leaf output"}, nil)
	assert.Equal(t, []string{leafStepID}, NextExecutionTracePredecessors(middle))
}
