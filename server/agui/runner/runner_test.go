//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	aguitranslator "trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestNew(t *testing.T) {
	r := New(nil)
	assert.NotNil(t, r)
	runner, ok := r.(*runner)
	assert.True(t, ok)

	trans := runner.translatorFactory(&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NotNil(t, trans)
	assert.IsType(t, translator.New("", ""), trans)

	userID, err := runner.userIDResolver(context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)
	assert.Equal(t, "user", userID)
}

func TestRunValidatesInput(t *testing.T) {
	r := &runner{}
	ch, err := r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)

	r.runner = &fakeRunner{}
	ch, err = r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestRunNoMessages(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUserIDResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunLastMessageNotUser(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleAssistant, Content: "bot"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUnderlyingRunnerError(t *testing.T) {
	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return nil, errors.New("fail")
	}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 1, underlying.calls)
}

func TestRunTranslateError(t *testing.T) {
	fakeTrans := &fakeTranslator{err: errors.New("bad event")}
	eventsCh := make(chan *agentevent.Event, 1)
	eventsCh <- &agentevent.Event{}
	close(eventsCh)

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message, _ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return eventsCh, nil
	}

	r := &runner{
		runner: underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator {
			return fakeTrans
		},
		userIDResolver: NewOptions().UserIDResolver,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	aguiCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

func TestRunNormal(t *testing.T) {
	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{
		{aguievents.NewTextMessageStartEvent("msg-1")},
		{aguievents.NewTextMessageEndEvent("msg-1"), aguievents.NewRunFinishedEvent("thread", "run")},
	}}

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message, _ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		assert.Equal(t, "user-123", userID)
		assert.Equal(t, "thread", sessionID)
		ch := make(chan *agentevent.Event, 2)
		ch <- &agentevent.Event{}
		ch <- &agentevent.Event{}
		close(ch)
		return ch, nil
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "user-123", nil
		},
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}

	aguiCh, err := r.Run(context.Background(), input)
	if !assert.NoError(t, err) {
		return
	}
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), evts[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), evts[2])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts[3])
	assert.Equal(t, 1, underlying.calls)
}

func TestRunTelemetrySpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	oldTracer := atrace.Tracer
	oldProvider := atrace.TracerProvider
	atrace.TracerProvider = tp
	atrace.Tracer = tp.Tracer(itelemetry.InstrumentName)
	t.Cleanup(func() {
		atrace.TracerProvider = oldProvider
		atrace.Tracer = oldTracer
	})
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{
		{aguievents.NewTextMessageStartEvent("msg-1")},
		{aguievents.NewTextMessageContentEvent("msg-1", "hi")},
		{aguievents.NewTextMessageEndEvent("msg-1")},
		{aguievents.NewToolCallStartEvent("tool-1", "lookup")},
		{aguievents.NewToolCallArgsEvent("tool-1", "{\"q\":\"foo\"}")},
		{aguievents.NewToolCallEndEvent("tool-1")},
		{aguievents.NewToolCallResultEvent("msg-1", "tool-1", "done")},
		{aguievents.NewRunFinishedEvent("thread", "run")},
	}}

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message, _ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		ch := make(chan *agentevent.Event, len(fakeTrans.events))
		for range fakeTrans.events {
			ch <- &agentevent.Event{}
		}
		close(ch)
		return ch, nil
	}

	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) aguitranslator.Translator { return fakeTrans },
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "user-telemetry", nil
		},
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}

	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	collectEvents(t, eventsCh)

	spans := recorder.Ended()
	nameSet := make(map[string]struct{})
	for _, span := range spans {
		nameSet[span.Name()] = struct{}{}
	}

	for _, name := range []string{
		itelemetry.SpanNameAGUI,
		itelemetry.SpanNameAGUIRun,
		itelemetry.SpanNameAGUIText,
		itelemetry.SpanNameAGUITool,
		itelemetry.SpanNameAGUIToolCall,
		itelemetry.SpanNameAGUIToolResponse,
	} {
		if _, ok := nameSet[name]; !ok {
			t.Fatalf("expected span %s not found", name)
		}
	}

	var runSpan trace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() == itelemetry.SpanNameAGUIRun {
			runSpan = span
			break
		}
	}
	require.NotNil(t, runSpan)

	attrs := make(map[string]string)
	for _, kv := range runSpan.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	require.Equal(t, "hi", attrs[itelemetry.KeyRunnerInput])
	require.Equal(t, "hi", attrs[itelemetry.KeyRunnerOutput])
	require.Equal(t, "thread", attrs[itelemetry.KeyRunnerSessionID])
	require.Equal(t, "run", attrs[itelemetry.KeyInvocationID])
}

type fakeTranslator struct {
	events [][]aguievents.Event
	err    error
}

func (f *fakeTranslator) Translate(evt *agentevent.Event) ([]aguievents.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.events) == 0 {
		return nil, nil
	}
	out := f.events[0]
	f.events = f.events[1:]
	return out, nil
}

type fakeRunner struct {
	run   func(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *agentevent.Event, error)
	calls int
}

func (f *fakeRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
	f.calls++
	if observer := itelemetry.SpanObserverFromContext(ctx); observer != nil {
		rootCtx, rootSpan := atrace.Tracer.Start(ctx, "fake_agent_root")
		observer(rootCtx)
		_, span := atrace.Tracer.Start(rootCtx, "fake_invoke_agent")
		span.End()
		rootSpan.End()
		ctx = rootCtx
	}
	if f.run != nil {
		return f.run(ctx, userID, sessionID, message, opts...)
	}
	return nil, nil
}

func collectEvents(t *testing.T, ch <-chan aguievents.Event) []aguievents.Event {
	t.Helper()
	var out []aguievents.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-time.After(time.Second):
			t.Fatalf("timeout collecting events")
		}
	}
}
