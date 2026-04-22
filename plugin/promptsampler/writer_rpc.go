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
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ReportFunc delivers a serialised trace to an arbitrary transport. It is the
// extension point for users whose runtime uses a different tRPC distribution
// (for example the internal "git.code.oa.com/trpc-go/trpc-go") or an entirely
// different RPC / HTTP framework.
//
// Implementations receive the fully assembled payload (caller name, JSON
// serialised trace and business isolation token) and are expected to forward
// it to the log_collector service. Returning a non-nil error is treated as
// failure by the sampler: it is logged and fed back into the enclosing
// afterAgent hook, but never propagates to the Runner.
type ReportFunc func(ctx context.Context, caller, traceJSON, token string) error

// RPCWriter is a TraceWriter that delegates the actual transport to a
// caller-supplied ReportFunc. Unlike TRPCWriter, it does not import any tRPC
// client package and therefore avoids the multi-version pitfalls that can
// occur when a host process mixes "trpc.group/trpc-go/trpc-go" with internal
// distributions.
//
// RPCWriter is safe for concurrent use. Token updates arriving via the
// control plane (PromptSampler.SetConfig) are applied atomically and take
// effect on the next Write.
type RPCWriter struct {
	reportFunc ReportFunc
	caller     string
	timeout    time.Duration
	token      atomic.Value // string
}

// RPCWriterOption configures an RPCWriter.
type RPCWriterOption func(*RPCWriter)

// WithRPCReportFunc installs the transport-specific report function. It is
// required; constructing an RPCWriter without one causes Write to fail.
func WithRPCReportFunc(fn ReportFunc) RPCWriterOption {
	return func(w *RPCWriter) {
		if fn != nil {
			w.reportFunc = fn
		}
	}
}

// WithRPCCaller sets the caller service name (for example
// "trpc.myapp.myserver") stamped onto every request. Unlike TRPCWriter,
// RPCWriter does not attempt to auto-resolve the caller from the tRPC global
// config, because the import path of that config differs between
// distributions.
func WithRPCCaller(caller string) RPCWriterOption {
	return func(w *RPCWriter) { w.caller = caller }
}

// WithRPCTimeout bounds a single ReportFunc invocation. Defaults to 3s.
func WithRPCTimeout(d time.Duration) RPCWriterOption {
	return func(w *RPCWriter) {
		if d > 0 {
			w.timeout = d
		}
	}
}

// NewRPCWriter constructs an RPCWriter. WithRPCReportFunc is required; other
// options are optional.
func NewRPCWriter(opts ...RPCWriterOption) *RPCWriter {
	w := &RPCWriter{timeout: defaultTRPCTimeout}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// SetToken implements TokenSetter. The new token is applied atomically and
// used by all subsequent Write calls.
func (w *RPCWriter) SetToken(token string) {
	w.token.Store(token)
}

// loadToken returns the currently configured token, or the empty string.
func (w *RPCWriter) loadToken() string {
	v := w.token.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// ErrRPCWriterNoReportFunc is returned when Write is invoked on an RPCWriter
// constructed without WithRPCReportFunc.
var ErrRPCWriterNoReportFunc = errors.New("promptsampler: RPCWriter has no report func")

// ErrRPCWriterNoCaller is returned when Write is invoked on an RPCWriter
// without a configured caller. Unlike TRPCWriter, RPCWriter cannot fall back
// to an auto-resolved value.
var ErrRPCWriterNoCaller = errors.New("promptsampler: RPCWriter has no caller")

// Write implements TraceWriter. It serialises the trace to JSON and hands the
// three payload fields (caller, traceJSON, token) to the caller-supplied
// ReportFunc. Errors are logged at Error level so that they are never
// silently swallowed.
func (w *RPCWriter) Write(ctx context.Context, trace *Trace) error {
	if trace == nil {
		return nil
	}
	if w.reportFunc == nil {
		log.ErrorfContext(ctx,
			"[promptsampler] RPCWriter: no report func configured; dropping trace invocation_id=%s",
			trace.InvocationID,
		)
		return ErrRPCWriterNoReportFunc
	}
	if w.caller == "" {
		log.ErrorfContext(ctx,
			"[promptsampler] RPCWriter: no caller configured; dropping trace invocation_id=%s",
			trace.InvocationID,
		)
		return ErrRPCWriterNoCaller
	}

	data, err := json.Marshal(trace)
	if err != nil {
		log.ErrorfContext(ctx,
			"[promptsampler] RPCWriter: marshal trace failed: invocation_id=%s err=%v",
			trace.InvocationID, err,
		)
		return fmt.Errorf("marshal trace: %w", err)
	}

	// Detach from the caller's cancel/deadline chain (context values such as
	// tRPC metadata are preserved) and then impose our own timeout. This
	// mirrors TRPCWriter and is especially important when the writer is
	// wrapped by an AsyncWriter whose context is already cancelled.
	rpcCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.timeout)
	defer cancel()

	if err := w.reportFunc(rpcCtx, w.caller, string(data), w.loadToken()); err != nil {
		log.ErrorfContext(ctx,
			"[promptsampler] RPCWriter: report func failed: invocation_id=%s caller=%s err=%v",
			trace.InvocationID, w.caller, err,
		)
		return fmt.Errorf("rpc writer: %w", err)
	}
	return nil
}
