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

func shouldCaptureGraphCompletion(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capture, _ := ctx.Value(graphCompletionCaptureKey{}).(bool)
	return capture
}

func isGraphCompletionEvent(evt *event.Event) bool {
	return evt != nil && evt.Done && evt.Object == ObjectTypeGraphExecution
}
