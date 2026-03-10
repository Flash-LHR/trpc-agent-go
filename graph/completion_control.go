//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

type graphCompletionCaptureKey struct{}

// WithGraphCompletionCapture keeps terminal graph completion events available
// to internal graph consumers even when caller-visible forwarding is disabled.
func WithGraphCompletionCapture(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, graphCompletionCaptureKey{}, true)
}

// ShouldCaptureGraphCompletion reports whether the current context keeps
// terminal graph completion events available for internal consumers.
func ShouldCaptureGraphCompletion(ctx context.Context) bool {
	return shouldCaptureGraphCompletion(ctx)
}

func shouldCaptureGraphCompletion(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capture, _ := ctx.Value(graphCompletionCaptureKey{}).(bool)
	return capture
}

// IsGraphCompletionEvent reports whether the event is a terminal
// graph.execution event.
func IsGraphCompletionEvent(evt *event.Event) bool {
	return isGraphCompletionEvent(evt)
}

func isGraphCompletionEvent(evt *event.Event) bool {
	return evt != nil && evt.Done && evt.Object == ObjectTypeGraphExecution
}

// ShouldSuppressGraphCompletionEvent reports whether the caller-visible stream
// should hide the terminal graph completion event for this invocation.
func ShouldSuppressGraphCompletionEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	evt *event.Event,
) bool {
	if invocation == nil || !invocation.RunOptions.DisableGraphCompletionEvent {
		return false
	}
	if ShouldCaptureGraphCompletion(ctx) {
		return false
	}
	return IsGraphCompletionEvent(evt)
}
