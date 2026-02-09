package trace

import (
	"context"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	otelnoop "go.opentelemetry.io/otel/trace/noop"
)

type recordingSpan struct {
	embedded.Span
	ended atomic.Bool
	sc    oteltrace.SpanContext
}

func (s *recordingSpan) SpanContext() oteltrace.SpanContext { return s.sc }
func (s *recordingSpan) IsRecording() bool                 { return true }
func (s *recordingSpan) SetStatus(codes.Code, string)      {}
func (s *recordingSpan) SetAttributes(...attribute.KeyValue) {
}
func (s *recordingSpan) End(...oteltrace.SpanEndOption) { s.ended.Store(true) }
func (s *recordingSpan) RecordError(error, ...oteltrace.EventOption) {
}
func (s *recordingSpan) AddEvent(string, ...oteltrace.EventOption) {}
func (s *recordingSpan) AddLink(oteltrace.Link)                    {}
func (s *recordingSpan) SetName(string)                            {}
func (s *recordingSpan) TracerProvider() oteltrace.TracerProvider  { return otelnoop.NewTracerProvider() }

func TestStartSpan_NoopTracer_NoParentSpanContext_NoContextWrapper(t *testing.T) {
	origTracer := Tracer
	Tracer = otelnoop.NewTracerProvider().Tracer("")
	defer func() { Tracer = origTracer }()

	ctx := context.Background()
	newCtx, span := StartSpan(ctx, "test")
	if newCtx != ctx {
		t.Fatalf("expected context to be unchanged for background ctx")
	}
	if span == nil {
		t.Fatalf("expected non-nil span")
	}
	var zeroSC oteltrace.SpanContext
	if sc := span.SpanContext(); !sc.Equal(zeroSC) {
		t.Fatalf("expected zero span context, got %v", sc)
	}
}

func TestStartSpan_NoopTracer_WithRecordingParent_DoesNotEndParent(t *testing.T) {
	origTracer := Tracer
	Tracer = otelnoop.NewTracerProvider().Tracer("")
	defer func() { Tracer = origTracer }()

	traceID := oteltrace.TraceID([16]byte{1, 2, 3})
	spanID := oteltrace.SpanID([8]byte{4, 5, 6})
	parentSC := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
	parent := &recordingSpan{sc: parentSC}
	ctx := oteltrace.ContextWithSpan(context.Background(), parent)

	newCtx, span := StartSpan(ctx, "child")
	if newCtx == ctx {
		t.Fatalf("expected a wrapped context when parent span context exists")
	}
	if span == nil {
		t.Fatalf("expected non-nil span")
	}
	if span.IsRecording() {
		t.Fatalf("expected non-recording span from no-op tracer")
	}

	span.End()
	if parent.ended.Load() {
		t.Fatalf("parent span End() was called")
	}

	// Ensure the context contains a non-recording span even though the parent was recording.
	if got := oteltrace.SpanFromContext(newCtx); got == nil || got.IsRecording() {
		t.Fatalf("expected non-recording span in returned context")
	}
}
