//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sse provides SSE service implementation.
package sse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"trpc.group/trpc-go/trpc-agent-go/internal/slowlog"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

// sse is a SSE service implementation.
type sse struct {
	path                    string
	messagesSnapshotPath    string
	cancelPath              string
	writer                  *aguisse.SSEWriter
	runner                  aguirunner.Runner
	handler                 http.Handler
	messagesSnapshotEnabled bool
	cancelEnabled           bool
	heartbeatInterval       time.Duration
}

// New creates a new SSE service.
func New(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &sse{
		path:                    opts.Path,
		messagesSnapshotPath:    opts.MessagesSnapshotPath,
		cancelPath:              opts.CancelPath,
		runner:                  runner,
		writer:                  aguisse.NewSSEWriter(),
		messagesSnapshotEnabled: opts.MessagesSnapshotEnabled,
		cancelEnabled:           opts.CancelEnabled,
		heartbeatInterval:       opts.HeartbeatInterval,
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	if s.messagesSnapshotEnabled {
		h.HandleFunc(s.messagesSnapshotPath, s.handleMessagesSnapshot)
	}
	if s.cancelEnabled {
		h.HandleFunc(s.cancelPath, s.handleCancel)
	}
	s.handler = h
	return s
}

// Handler returns an http.Handler that exposes the AG-UI SSE endpoint.
func (s *sse) Handler() http.Handler {
	return s.handler
}

// handle handles an AG-UI run request.
func (s *sse) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	handleStarted := time.Now()
	slowlog.Logf(
		ctx,
		"agui.sse.handle.start path=%s method=%s drain=%t",
		s.path,
		r.Method,
		true,
	)
	defer func() {
		slowlog.Logf(
			ctx,
			"agui.sse.handle.finish path=%s method=%s drain=%t ctx_err=%v elapsed=%v",
			s.path,
			r.Method,
			true,
			ctx.Err(),
			time.Since(handleStarted),
		)
	}()
	log.DebugfContext(
		ctx,
		"agui handle: path: %s, method: %s",
		s.path,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle: options request")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	parseStarted := time.Now()
	runAgentInput, err := runAgentInputFromReader(r.Body)
	slowlog.Logf(
		ctx,
		"agui.sse.parse_input path=%s method=%s thread_id=%s run_id=%s messages=%d err=%v elapsed=%v",
		s.path,
		r.Method,
		sseDiagRunAgentInputThreadID(runAgentInput),
		sseDiagRunAgentInputRunID(runAgentInput),
		sseDiagRunAgentInputMessages(runAgentInput),
		err,
		time.Since(parseStarted),
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runStarted := time.Now()
	eventsCh, err := s.runner.Run(ctx, runAgentInput)
	slowlog.Logf(
		ctx,
		"agui.sse.runner_run path=%s method=%s thread_id=%s run_id=%s events_nil=%t err=%v elapsed=%v",
		s.path,
		r.Method,
		runAgentInput.ThreadID,
		runAgentInput.RunID,
		eventsCh == nil,
		err,
		time.Since(runStarted),
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, run agent: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunAlreadyExists) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	handleEventsStarted := time.Now()
	err = s.handleEvents(ctx, w, eventsCh, true)
	slowlog.Logf(
		ctx,
		"agui.sse.handle_events_return path=%s method=%s thread_id=%s run_id=%s drain=%t err=%v elapsed=%v",
		s.path,
		r.Method,
		runAgentInput.ThreadID,
		runAgentInput.RunID,
		true,
		err,
		time.Since(handleEventsStarted),
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleMessagesSnapshot streams a synthetic snapshot run to the client.
func (s *sse) handleMessagesSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	handleStarted := time.Now()
	slowlog.Logf(
		ctx,
		"agui.sse.handle.start path=%s method=%s drain=%t",
		s.messagesSnapshotPath,
		r.Method,
		false,
	)
	defer func() {
		slowlog.Logf(
			ctx,
			"agui.sse.handle.finish path=%s method=%s drain=%t ctx_err=%v elapsed=%v",
			s.messagesSnapshotPath,
			r.Method,
			false,
			ctx.Err(),
			time.Since(handleStarted),
		)
	}()
	log.DebugfContext(
		ctx,
		"agui handle messages snapshot: path: %s, method: %s",
		s.messagesSnapshotPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(
			ctx,
			"agui handle messages snapshot: options request",
		)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle messages snapshot: method not allowed, "+
				"method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	parseStarted := time.Now()
	runAgentInput, err := runAgentInputFromReader(r.Body)
	slowlog.Logf(
		ctx,
		"agui.sse.parse_input path=%s method=%s thread_id=%s run_id=%s messages=%d err=%v elapsed=%v",
		s.messagesSnapshotPath,
		r.Method,
		sseDiagRunAgentInputThreadID(runAgentInput),
		sseDiagRunAgentInputRunID(runAgentInput),
		sseDiagRunAgentInputMessages(runAgentInput),
		err,
		time.Since(parseStarted),
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle messages snapshot: parse run agent "+
				"input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	messagesSnapshotter, ok := s.runner.(aguirunner.MessagesSnapshotter)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner does not "+
				"support messages snapshot",
		)
		http.Error(w, "runner does not support messages snapshot", http.StatusNotImplemented)
		return
	}
	runStarted := time.Now()
	eventsCh, err := messagesSnapshotter.MessagesSnapshot(ctx, runAgentInput)
	slowlog.Logf(
		ctx,
		"agui.sse.runner_run path=%s method=%s thread_id=%s run_id=%s events_nil=%t snapshot=%t err=%v elapsed=%v",
		s.messagesSnapshotPath,
		r.Method,
		runAgentInput.ThreadID,
		runAgentInput.RunID,
		eventsCh == nil,
		true,
		err,
		time.Since(runStarted),
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, runID: "+
				"%s, messages snapshot: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	handleEventsStarted := time.Now()
	err = s.handleEvents(ctx, w, eventsCh, false)
	slowlog.Logf(
		ctx,
		"agui.sse.handle_events_return path=%s method=%s thread_id=%s run_id=%s drain=%t err=%v elapsed=%v",
		s.messagesSnapshotPath,
		r.Method,
		runAgentInput.ThreadID,
		runAgentInput.RunID,
		false,
		err,
		time.Since(handleEventsStarted),
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, "+
				"runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *sse) handleEvents(
	ctx context.Context,
	w http.ResponseWriter,
	events <-chan aguievents.Event,
	drain bool,
) (retErr error) {
	started := time.Now()
	eventCount := 0
	eventElapsed := time.Duration(0)
	eventWaitElapsed := time.Duration(0)
	selectWaitElapsed := time.Duration(0)
	ctxDoneElapsed := time.Duration(0)
	heartbeatCount := 0
	heartbeatElapsed := time.Duration(0)
	lastThreadID := ""
	lastRunID := ""
	defer func() {
		slowlog.Logf(
			ctx,
			"agui.sse.handle_events thread_id=%s run_id=%s drain=%t events=%d select_wait_elapsed=%v event_wait_elapsed=%v event_elapsed=%v heartbeats=%d heartbeat_elapsed=%v ctx_done_elapsed=%v err=%v ctx_err=%v elapsed=%v",
			lastThreadID,
			lastRunID,
			drain,
			eventCount,
			selectWaitElapsed,
			eventWaitElapsed,
			eventElapsed,
			heartbeatCount,
			heartbeatElapsed,
			ctxDoneElapsed,
			retErr,
			ctx.Err(),
			time.Since(started),
		)
	}()
	var heartbeat <-chan time.Time
	if s.heartbeatInterval > 0 {
		ticker := time.NewTicker(s.heartbeatInterval)
		defer ticker.Stop()
		heartbeat = ticker.C
	}
	for {
		waitStarted := time.Now()
		select {
		case <-ctx.Done():
			waitElapsed := time.Since(waitStarted)
			selectWaitElapsed += waitElapsed
			ctxDoneElapsed += waitElapsed
			slowlog.Logf(
				ctx,
				"agui.sse.wait_event thread_id=%s run_id=%s ok=false reason=ctx_done err=%v elapsed=%v",
				lastThreadID,
				lastRunID,
				ctx.Err(),
				waitElapsed,
			)
			if drain {
				go drainEvents(events)
			}
			return nil
		case <-heartbeat:
			selectWaitElapsed += time.Since(waitStarted)
			heartbeatStarted := time.Now()
			err := writeHeartbeat(w)
			heartbeatWriteElapsed := time.Since(heartbeatStarted)
			heartbeatElapsed += heartbeatWriteElapsed
			heartbeatCount++
			slowlog.Logf(
				ctx,
				"agui.sse.write_heartbeat thread_id=%s run_id=%s err=%v elapsed=%v",
				lastThreadID,
				lastRunID,
				err,
				heartbeatWriteElapsed,
			)
			if err != nil {
				if drain {
					go drainEvents(events)
				}
				return err
			}
		case evt, ok := <-events:
			waitElapsed := time.Since(waitStarted)
			selectWaitElapsed += waitElapsed
			eventWaitElapsed += waitElapsed
			if !ok {
				slowlog.Logf(
					ctx,
					"agui.sse.wait_event thread_id=%s run_id=%s ok=false reason=closed err=<nil> elapsed=%v",
					lastThreadID,
					lastRunID,
					waitElapsed,
				)
				return nil
			}
			if threadID := sseDiagThreadID(evt); threadID != "" {
				lastThreadID = threadID
			}
			if runID := sseDiagRunID(evt); runID != "" {
				lastRunID = runID
			}
			slowlog.Logf(
				ctx,
				"agui.sse.wait_event thread_id=%s run_id=%s event_type=%s message_id=%s tool_call_id=%s ok=true err=<nil> elapsed=%v",
				lastThreadID,
				lastRunID,
				sseDiagEventType(evt),
				sseDiagMessageID(evt),
				sseDiagToolCallID(evt),
				waitElapsed,
			)
			writeStarted := time.Now()
			err := s.writer.WriteEvent(ctx, w, evt)
			writeElapsed := time.Since(writeStarted)
			eventElapsed += writeElapsed
			eventCount++
			slowlog.Logf(
				ctx,
				"agui.sse.write_event thread_id=%s run_id=%s event_type=%s message_id=%s tool_call_id=%s payload_bytes=%d err=%v elapsed=%v",
				lastThreadID,
				lastRunID,
				sseDiagEventType(evt),
				sseDiagMessageID(evt),
				sseDiagToolCallID(evt),
				sseDiagPayloadBytes(evt),
				err,
				writeElapsed,
			)
			if err != nil {
				if drain {
					go drainEvents(events)
				}
				return err
			}
		}
	}
}

func sseDiagEventType(event aguievents.Event) string {
	if event == nil {
		return ""
	}
	return string(event.Type())
}

func sseDiagRunAgentInputThreadID(input *adapter.RunAgentInput) string {
	if input == nil {
		return ""
	}
	return input.ThreadID
}

func sseDiagRunAgentInputRunID(input *adapter.RunAgentInput) string {
	if input == nil {
		return ""
	}
	return input.RunID
}

func sseDiagRunAgentInputMessages(input *adapter.RunAgentInput) int {
	if input == nil {
		return 0
	}
	return len(input.Messages)
}

func sseDiagThreadID(event aguievents.Event) string {
	if event == nil {
		return ""
	}
	return event.ThreadID()
}

func sseDiagRunID(event aguievents.Event) string {
	if event == nil {
		return ""
	}
	return event.RunID()
}

func sseDiagMessageID(event aguievents.Event) string {
	switch e := event.(type) {
	case *aguievents.TextMessageStartEvent:
		return e.MessageID
	case *aguievents.TextMessageContentEvent:
		return e.MessageID
	case *aguievents.TextMessageEndEvent:
		return e.MessageID
	case *aguievents.ToolCallStartEvent:
		return sseDiagStringPtr(e.ParentMessageID)
	case *aguievents.ToolCallResultEvent:
		return e.MessageID
	default:
		return ""
	}
}

func sseDiagToolCallID(event aguievents.Event) string {
	switch e := event.(type) {
	case *aguievents.ToolCallStartEvent:
		return e.ToolCallID
	case *aguievents.ToolCallArgsEvent:
		return e.ToolCallID
	case *aguievents.ToolCallEndEvent:
		return e.ToolCallID
	case *aguievents.ToolCallResultEvent:
		return e.ToolCallID
	default:
		return ""
	}
}

func sseDiagPayloadBytes(event aguievents.Event) int {
	switch e := event.(type) {
	case *aguievents.TextMessageContentEvent:
		return len(e.Delta)
	case *aguievents.ToolCallArgsEvent:
		return len(e.Delta)
	case *aguievents.ToolCallResultEvent:
		return len(e.Content)
	default:
		return 0
	}
}

func sseDiagStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func writeHeartbeat(w http.ResponseWriter) error {
	if _, err := w.Write([]byte(":\n\n")); err != nil {
		return err
	}
	if flusher, ok := w.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

// handleCancel cancels a running run identified by the request payload.
func (s *sse) handleCancel(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	log.DebugfContext(
		ctx,
		"agui handle cancel: path: %s, method: %s",
		s.cancelPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle cancel: options request")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle cancel: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle cancel: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canceler, ok := s.runner.(aguirunner.Canceler)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner does not support cancel",
		)
		http.Error(w, "runner does not support cancel", http.StatusNotImplemented)
		return
	}
	if err := canceler.Cancel(ctx, runAgentInput); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: threadID: %s, runID: %s, cancel: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}

// runAgentInputFromReader parses an AG-UI run request payload from a reader.
func runAgentInputFromReader(r io.Reader) (*adapter.RunAgentInput, error) {
	var input adapter.RunAgentInput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}

func drainEvents(events <-chan aguievents.Event) {
	for range events {
	}
}
