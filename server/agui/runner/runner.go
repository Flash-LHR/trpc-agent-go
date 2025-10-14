//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package runner wraps a trpc-agent-go runner and translates it to AG-UI events.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Runner executes AG-UI runs and emits AG-UI events.
type Runner interface {
	// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
	Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// New wraps a trpc-agent-go runner with AG-UI specific translation logic.
func New(r trunner.Runner, opt ...Option) Runner {
	opts := NewOptions(opt...)
	run := &runner{
		runner:            r,
		translatorFactory: opts.TranslatorFactory,
		userIDResolver:    opts.UserIDResolver,
	}
	return run
}

// runner is the default implementation of the Runner.
type runner struct {
	runner            trunner.Runner
	translatorFactory TranslatorFactory
	userIDResolver    UserIDResolver
}

// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
func (r *runner) Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	events := make(chan aguievents.Event)
	go r.run(ctx, runAgentInput, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, runAgentInput *adapter.RunAgentInput, events chan<- aguievents.Event) {
	defer close(events)

	translator := r.translatorFactory(runAgentInput)

	events <- aguievents.NewRunStartedEvent(runAgentInput.ThreadID, runAgentInput.RunID)
	if len(runAgentInput.Messages) == 0 {
		events <- aguievents.NewRunErrorEvent("no messages provided", aguievents.WithRunID(runAgentInput.RunID))
		return
	}

	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		msg := fmt.Sprintf("resolve user ID: %v", err)
		events <- aguievents.NewRunErrorEvent(msg, aguievents.WithRunID(runAgentInput.RunID))
		return
	}

	userMessage := runAgentInput.Messages[len(runAgentInput.Messages)-1]
	userInput := formatAGUIInput(userMessage)
	if userMessage.Role != model.RoleUser {
		events <- aguievents.NewRunErrorEvent("last message is not a user message", aguievents.WithRunID(runAgentInput.RunID))
		return
	}

	parentCtxCh := make(chan context.Context, 1)
	ctxWithObserver := itelemetry.WithSpanObserver(ctx, func(c context.Context) {
		select {
		case parentCtxCh <- c:
		default:
		}
	})

	ch, err := r.runner.Run(ctxWithObserver, userID, runAgentInput.ThreadID, userMessage)
	if err != nil {
		msg := fmt.Sprintf("run agent: %v", err)
		events <- aguievents.NewRunErrorEvent(msg, aguievents.WithRunID(runAgentInput.RunID))
		return
	}

	parentCtx := ctx
	select {
	case parentCtx = <-parentCtxCh:
	case <-ctx.Done():
	}

	aguiCtx, aguiSpan := atrace.Tracer.Start(parentCtx, itelemetry.SpanNameAGUI)
	aguiSpan.SetAttributes(
		attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUI),
		attribute.String(itelemetry.KeyAGUIThreadID, runAgentInput.ThreadID),
		attribute.String(itelemetry.KeyAGUIRunID, runAgentInput.RunID),
		attribute.String(itelemetry.KeyRunnerSessionID, runAgentInput.ThreadID),
		attribute.String(itelemetry.KeyInvocationID, runAgentInput.RunID),
		attribute.String(itelemetry.KeyRunnerUserID, userID),
	)
	if userInput != "" {
		aguiSpan.SetAttributes(
			attribute.String(itelemetry.KeyRunnerInput, userInput),
			attribute.String(itelemetry.KeyLLMRequest, userInput),
		)
	}
	defer aguiSpan.End()

	runCtx, runSpan := atrace.Tracer.Start(aguiCtx, itelemetry.SpanNameAGUIRun)
	runSpan.SetAttributes(
		attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUIRun),
		attribute.String(itelemetry.KeyRunnerSessionID, runAgentInput.ThreadID),
		attribute.String(itelemetry.KeyInvocationID, runAgentInput.RunID),
		attribute.String(itelemetry.KeyRunnerUserID, userID),
	)
	if userInput != "" {
		runSpan.SetAttributes(
			attribute.String(itelemetry.KeyRunnerInput, userInput),
			attribute.String(itelemetry.KeyLLMRequest, userInput),
		)
	}
	defer runSpan.End()

	tracker := newAGUISpanTracker(runCtx, aguiSpan, runSpan, userInput)
	defer tracker.Close()

	for event := range ch {
		aguiEvents, err := translator.Translate(event)
		if err != nil {
			msg := fmt.Sprintf("translate event: %v", err)
			tracker.RecordFailure(msg)
			events <- aguievents.NewRunErrorEvent(msg, aguievents.WithRunID(runAgentInput.RunID))
			return
		}
		for _, aguiEvent := range aguiEvents {
			tracker.Observe(aguiEvent)
			events <- aguiEvent
		}
	}

	tracker.Complete()
}

// aguiSpanTracker maintains nested spans for AGUI observability signals.
type aguiSpanTracker struct {
	ctx         context.Context
	aguiSpan    oteltrace.Span
	runSpan     oteltrace.Span
	textSpans   map[string]oteltrace.Span
	textBuffers map[string]string
	toolSpans   map[string]*aguiToolSpan
	failed      bool
	lastOutput  string
	userInput   string
}

type aguiToolSpan struct {
	ctx          context.Context
	span         oteltrace.Span
	callSpan     oteltrace.Span
	responseSpan oteltrace.Span
	messageID    string
	args         []string
}

func newAGUISpanTracker(ctx context.Context, aguiSpan, runSpan oteltrace.Span, userInput string) *aguiSpanTracker {
	return &aguiSpanTracker{
		ctx:         ctx,
		aguiSpan:    aguiSpan,
		runSpan:     runSpan,
		textSpans:   make(map[string]oteltrace.Span),
		textBuffers: make(map[string]string),
		toolSpans:   make(map[string]*aguiToolSpan),
		userInput:   userInput,
	}
}

func (t *aguiSpanTracker) RecordFailure(reason string) {
	errAttr := attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType)
	t.aguiSpan.SetStatus(codes.Error, reason)
	t.runSpan.SetStatus(codes.Error, reason)
	t.aguiSpan.SetAttributes(errAttr)
	t.runSpan.SetAttributes(errAttr)
	t.failed = true
}

func (t *aguiSpanTracker) Observe(evt aguievents.Event) {
	switch e := evt.(type) {
	case *aguievents.TextMessageStartEvent:
		t.startTextSpan(e)
	case *aguievents.TextMessageContentEvent:
		t.appendText(e)
	case *aguievents.TextMessageEndEvent:
		t.endTextSpan(e.MessageID)
	case *aguievents.ToolCallStartEvent:
		t.startToolSpan(e)
	case *aguievents.ToolCallArgsEvent:
		t.addToolArgs(e)
	case *aguievents.ToolCallEndEvent:
		t.finishToolCall(e)
	case *aguievents.ToolCallResultEvent:
		t.finishToolResult(e)
	case *aguievents.RunFinishedEvent:
		if !t.failed {
			t.aguiSpan.SetStatus(codes.Ok, "completed")
			t.runSpan.SetStatus(codes.Ok, "completed")
		}
	case *aguievents.RunErrorEvent:
		t.RecordFailure(e.Message)
	}
}

func (t *aguiSpanTracker) Complete() {
	if t.lastOutput != "" {
		outAttrs := []attribute.KeyValue{
			attribute.String(itelemetry.KeyRunnerOutput, t.lastOutput),
			attribute.String(itelemetry.KeyLLMResponse, t.lastOutput),
		}
		t.runSpan.SetAttributes(outAttrs...)
		t.aguiSpan.SetAttributes(outAttrs...)
	}
	if !t.failed {
		t.aguiSpan.SetStatus(codes.Ok, "completed")
		t.runSpan.SetStatus(codes.Ok, "completed")
	}
}

func (t *aguiSpanTracker) Close() {
	for id, span := range t.textSpans {
		span.End()
		delete(t.textSpans, id)
	}
	for id, state := range t.toolSpans {
		if state.callSpan != nil {
			state.callSpan.End()
		}
		if state.responseSpan != nil {
			state.responseSpan.End()
		}
		if state.span != nil {
			state.span.End()
		}
		delete(t.toolSpans, id)
	}
}

func (t *aguiSpanTracker) startTextSpan(evt *aguievents.TextMessageStartEvent) {
	textCtx, span := atrace.Tracer.Start(t.ctx, itelemetry.SpanNameAGUIText)
	_ = textCtx
	span.SetAttributes(
		attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUIText),
		attribute.String(itelemetry.KeyAGUIMessageID, evt.MessageID),
	)
	if t.userInput != "" {
		span.SetAttributes(
			attribute.String(itelemetry.KeyLLMRequest, t.userInput),
			attribute.String(itelemetry.KeyRunnerInput, t.userInput),
		)
	}
	t.textSpans[evt.MessageID] = span
	if _, ok := t.textBuffers[evt.MessageID]; !ok {
		t.textBuffers[evt.MessageID] = ""
	}
}

func (t *aguiSpanTracker) appendText(evt *aguievents.TextMessageContentEvent) {
	if span, ok := t.textSpans[evt.MessageID]; ok {
		span.AddEvent("content", oteltrace.WithAttributes(attribute.String("delta", evt.Delta)))
	}
	t.textBuffers[evt.MessageID] += evt.Delta
}

func (t *aguiSpanTracker) endTextSpan(messageID string) {
	if span, ok := t.textSpans[messageID]; ok {
		if txt, exists := t.textBuffers[messageID]; exists {
			t.lastOutput = txt
			if txt != "" {
				span.SetAttributes(
					attribute.String(itelemetry.KeyLLMResponse, txt),
					attribute.String(itelemetry.KeyRunnerOutput, txt),
				)
			}
		}
		span.End()
		delete(t.textSpans, messageID)
	}
	delete(t.textBuffers, messageID)
}

func (t *aguiSpanTracker) startToolSpan(evt *aguievents.ToolCallStartEvent) {
	toolCtx, span := atrace.Tracer.Start(t.ctx, itelemetry.SpanNameAGUITool)
	attrs := []attribute.KeyValue{
		attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUITool),
		attribute.String(itelemetry.KeyAGUIToolCallID, evt.ToolCallID),
		attribute.String(itelemetry.KeyAGUIToolName, evt.ToolCallName),
	}
	messageID := ""
	if evt.ParentMessageID != nil {
		messageID = *evt.ParentMessageID
		attrs = append(attrs,
			attribute.String(itelemetry.KeyAGUIToolMessageID, messageID),
			attribute.String(itelemetry.KeyAGUIToolCallMessage, messageID),
		)
	}
	span.SetAttributes(attrs...)

	_, callSpan := atrace.Tracer.Start(toolCtx, itelemetry.SpanNameAGUIToolCall)
	callSpan.SetAttributes(
		attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUIToolCall),
		attribute.String(itelemetry.KeyAGUIToolCallID, evt.ToolCallID),
		attribute.String(itelemetry.KeyAGUIToolName, evt.ToolCallName),
	)

	t.toolSpans[evt.ToolCallID] = &aguiToolSpan{
		ctx:       toolCtx,
		span:      span,
		callSpan:  callSpan,
		messageID: messageID,
	}
}

func (t *aguiSpanTracker) addToolArgs(evt *aguievents.ToolCallArgsEvent) {
	if state, ok := t.toolSpans[evt.ToolCallID]; ok {
		state.args = append(state.args, evt.Delta)
		if state.callSpan != nil {
			state.callSpan.AddEvent("args", oteltrace.WithAttributes(attribute.String("delta", evt.Delta)))
		}
	}
}

func (t *aguiSpanTracker) finishToolCall(evt *aguievents.ToolCallEndEvent) {
	if state, ok := t.toolSpans[evt.ToolCallID]; ok {
		if state.callSpan != nil {
			combined := strings.Join(state.args, "")
			if combined != "" {
				attrs := []attribute.KeyValue{
					attribute.String(itelemetry.KeyAGUIToolCallInput, combined),
					attribute.String(itelemetry.KeyLLMRequest, combined),
					attribute.String(itelemetry.KeyRunnerInput, combined),
				}
				state.callSpan.SetAttributes(attrs...)
				if state.span != nil {
					state.span.SetAttributes(attrs...)
				}
			}
			state.callSpan.End()
			state.callSpan = nil
		}
		_, responseSpan := atrace.Tracer.Start(state.ctx, itelemetry.SpanNameAGUIToolResponse)
		attrs := []attribute.KeyValue{
			attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUIToolResponse),
			attribute.String(itelemetry.KeyAGUIToolCallID, evt.ToolCallID),
		}
		if state.messageID != "" {
			attrs = append(attrs, attribute.String(itelemetry.KeyAGUIToolMessageID, state.messageID))
		}
		responseSpan.SetAttributes(attrs...)
		state.responseSpan = responseSpan
	}
}

func (t *aguiSpanTracker) finishToolResult(evt *aguievents.ToolCallResultEvent) {
	state, ok := t.toolSpans[evt.ToolCallID]
	if !ok {
		return
	}
	combined := strings.Join(state.args, "")
	if state.callSpan != nil {
		if combined != "" {
			attrs := []attribute.KeyValue{
				attribute.String(itelemetry.KeyAGUIToolCallInput, combined),
				attribute.String(itelemetry.KeyLLMRequest, combined),
			}
			state.callSpan.SetAttributes(attrs...)
			if state.span != nil {
				state.span.SetAttributes(attrs...)
			}
		}
		state.callSpan.End()
		state.callSpan = nil
	}
	if state.span != nil && combined != "" {
		state.span.SetAttributes(
			attribute.String(itelemetry.KeyAGUIToolCallInput, combined),
			attribute.String(itelemetry.KeyLLMRequest, combined),
			attribute.String(itelemetry.KeyRunnerInput, combined),
		)
	}
	if state.responseSpan == nil {
		_, responseSpan := atrace.Tracer.Start(state.ctx, itelemetry.SpanNameAGUIToolResponse)
		attrs := []attribute.KeyValue{
			attribute.String(itelemetry.KeyAGUIEventType, itelemetry.SpanNameAGUIToolResponse),
			attribute.String(itelemetry.KeyAGUIToolCallID, evt.ToolCallID),
		}
		if state.messageID != "" {
			attrs = append(attrs, attribute.String(itelemetry.KeyAGUIToolMessageID, state.messageID))
		}
		responseSpan.SetAttributes(attrs...)
		state.responseSpan = responseSpan
	}
	if state.responseSpan != nil {
		attrs := []attribute.KeyValue{
			attribute.String(itelemetry.KeyAGUIToolCallOutput, evt.Content),
			attribute.String(itelemetry.KeyLLMResponse, evt.Content),
			attribute.String(itelemetry.KeyRunnerOutput, evt.Content),
		}
		state.responseSpan.SetAttributes(attrs...)
		if evt.MessageID != "" {
			state.responseSpan.SetAttributes(attribute.String(itelemetry.KeyAGUIToolMessageID, evt.MessageID))
		}
		state.responseSpan.End()
		state.responseSpan = nil
	}
	if state.span != nil {
		state.span.SetAttributes(
			attribute.String(itelemetry.KeyAGUIToolCallOutput, evt.Content),
			attribute.String(itelemetry.KeyLLMResponse, evt.Content),
			attribute.String(itelemetry.KeyRunnerOutput, evt.Content),
		)
		state.span.End()
		state.span = nil
	}
	delete(t.toolSpans, evt.ToolCallID)
}

func formatAGUIInput(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	if len(msg.ContentParts) > 0 {
		if b, err := json.Marshal(msg.ContentParts); err == nil {
			return string(b)
		}
	}
	return ""
}
