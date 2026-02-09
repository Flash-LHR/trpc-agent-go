//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trace

import (
	"context"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// IsNoopTracer reports whether the framework tracer is the default OpenTelemetry
// no-op tracer (i.e., trace.Start has not been called).
func IsNoopTracer() bool {
	_, ok := Tracer.(noop.Tracer)
	return ok
}

// StartSpan starts a tracing span when tracing is enabled.
//
// When the framework has not been initialized with trace.Start (the default
// no-op tracer), StartSpan avoids allocating context wrappers when there is no
// active parent span. When a parent span context exists, it falls back to the
// no-op tracer's Start implementation to ensure callers get a non-recording
// span that is safe to End() without accidentally ending or mutating a parent
// span.
func StartSpan(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	if _, ok := Tracer.(noop.Tracer); ok {
		parent := oteltrace.SpanFromContext(ctx)
		var zeroSC oteltrace.SpanContext
		if parent.SpanContext().Equal(zeroSC) {
			// No active span context: avoid ContextWithSpan allocation that
			// noop.Tracer.Start would otherwise perform.
			return ctx, parent
		}
	}
	return Tracer.Start(ctx, name, opts...)
}
