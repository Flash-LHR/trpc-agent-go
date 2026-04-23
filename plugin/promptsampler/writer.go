//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// TraceWriter is the destination for completed execution traces.
//
// Implementations must be safe for concurrent use because a single
// PromptSampler instance may be shared across concurrent Runner invocations.
type TraceWriter interface {
	// Write delivers a trace to the underlying destination. Returning an
	// error does not abort the Runner: the sampler merely logs it.
	Write(ctx context.Context, trace *Trace) error
}

// TokenSetter is an optional interface that TraceWriters can implement to
// receive business-isolation token updates when the runtime configuration
// changes (SetConfig -> TokenSetter.SetToken).
type TokenSetter interface {
	// SetToken updates the token used for subsequent Write calls.
	SetToken(token string)
}

// ---------- LogWriter ----------

// LogWriter serialises traces to JSON and emits them through the standard
// trpc-agent-go logger at info level.
//
// It is the default writer when WithTRPCWriter / WithWriter are not used.
type LogWriter struct {
	// Pretty enables indented JSON for human consumption.
	Pretty bool
}

// NewLogWriter creates a LogWriter emitting compact JSON.
func NewLogWriter() *LogWriter { return &LogWriter{Pretty: false} }

// NewPrettyLogWriter creates a LogWriter emitting indented JSON.
func NewPrettyLogWriter() *LogWriter { return &LogWriter{Pretty: true} }

// Write implements TraceWriter.
func (w *LogWriter) Write(ctx context.Context, trace *Trace) error {
	if trace == nil {
		return nil
	}
	var (
		data []byte
		err  error
	)
	if w.Pretty {
		data, err = json.MarshalIndent(trace, "", "  ")
	} else {
		data, err = json.Marshal(trace)
	}
	if err != nil {
		return fmt.Errorf("marshal trace: %w", err)
	}
	// [promptsampler-test] LogWriter 的录制结果：直接把 trace JSON 打到日志，
	// 便于本地/测试环境不接 log_collector 也能观察到录制内容。
	log.ErrorfContext(ctx, "[promptsampler-test] LogWriter trace: invocation_id=%s bytes=%d body=%s",
		trace.InvocationID, len(data), string(data))
	return nil
}

// ---------- NopWriter ----------

// NopWriter discards all traces. It is useful for tests and for disabling the
// plugin without unregistering it.
type NopWriter struct{}

// NewNopWriter creates a NopWriter.
func NewNopWriter() *NopWriter { return &NopWriter{} }

// Write implements TraceWriter.
func (w *NopWriter) Write(_ context.Context, _ *Trace) error { return nil }

// ---------- AsyncWriter ----------

// ErrAsyncQueueFull is returned by AsyncWriter.Write when the internal queue
// is saturated. Callers should treat it as a signal to either raise the queue
// length (WithAsyncWrite) or accept the back-pressure.
var ErrAsyncQueueFull = errors.New("promptsampler: async write queue full")

// AsyncWriter wraps another TraceWriter and performs the actual Write on a
// dedicated goroutine. It is intended for production use where the Runner
// hot path should not block on network I/O.
type AsyncWriter struct {
	writer   TraceWriter
	ch       chan *asyncJob
	done     chan struct{}
	queueLen int
}

type asyncJob struct {
	ctx   context.Context
	trace *Trace
}

// NewAsyncWriter wraps the given writer and starts the background worker.
// A queueLen of 0 or less is normalised to 100.
func NewAsyncWriter(writer TraceWriter, queueLen int) *AsyncWriter {
	if queueLen <= 0 {
		queueLen = 100
	}
	w := &AsyncWriter{
		writer:   writer,
		ch:       make(chan *asyncJob, queueLen),
		done:     make(chan struct{}),
		queueLen: queueLen,
	}
	go w.run()
	return w
}

// run drains the queue until it is closed.
func (w *AsyncWriter) run() {
	for job := range w.ch {
		if err := w.writer.Write(job.ctx, job.trace); err != nil {
			log.ErrorfContext(job.ctx,
				"[promptsampler] async write failed: %v", err)
		}
	}
	close(w.done)
}

// Write implements TraceWriter. It is non-blocking: when the queue is full it
// returns ErrAsyncQueueFull instead of blocking the Runner.
//
// The caller's context is detached from its cancel / deadline chain before
// being handed to the underlying writer so that the upload can outlive the
// request (context values such as tRPC metadata are preserved).
func (w *AsyncWriter) Write(ctx context.Context, trace *Trace) error {
	detached := context.WithoutCancel(ctx)
	select {
	case w.ch <- &asyncJob{ctx: detached, trace: trace}:
		// [promptsampler-test] 入队成功（真正落盘由后台 goroutine 执行）。
		if trace != nil {
			log.ErrorfContext(ctx,
				"[promptsampler-test] AsyncWriter enqueued: invocation_id=%s queue_len=%d",
				trace.InvocationID, w.queueLen,
			)
		}
		return nil
	default:
		if trace != nil {
			log.ErrorfContext(ctx,
				"[promptsampler-test] AsyncWriter queue full, drop: invocation_id=%s queue_len=%d",
				trace.InvocationID, w.queueLen,
			)
		}
		return ErrAsyncQueueFull
	}
}

// Close stops the AsyncWriter worker and waits for the queue to drain.
func (w *AsyncWriter) Close() error {
	close(w.ch)
	<-w.done
	return nil
}

// SetToken implements TokenSetter by forwarding to the wrapped writer if it
// supports it. This preserves the control-plane hot-reload semantics through
// the async layer.
func (w *AsyncWriter) SetToken(token string) {
	if ts, ok := w.writer.(TokenSetter); ok {
		ts.SetToken(token)
	}
}
