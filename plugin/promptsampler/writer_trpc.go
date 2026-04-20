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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	trpc "trpc.group/trpc-go/trpc-go"
	"trpc.group/trpc-go/trpc-go/client"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler/proto"
)

// Default values for the TRPCWriter. The target service name is the log
// collector's fixed Polaris name and the timeout matches a sensible
// production default.
const (
	defaultTRPCTarget  = "trpc.trs.prompt_log_collector.LogCollector"
	defaultTRPCTimeout = 3 * time.Second
	fallbackCaller     = "unknown"
)

// TRPCWriter is a TraceWriter that uploads traces to the log_collector tRPC
// service. It is the default way to ship traces from a trpc-agent-go Runner
// to the centralised collector.
//
// The writer is safe for concurrent use. Token updates arriving via the
// control-plane (PromptSampler.SetConfig) are applied atomically and take
// effect on the next Write.
type TRPCWriter struct {
	proxy proto.LogCollectorClientProxy

	// caller is the service identifier of the *calling* process. When empty,
	// it is resolved lazily from trpc.GlobalConfig().Server.Service[0].Name.
	caller string

	// target is the callee's service name for tRPC naming / routing. It
	// propagates via client.WithTarget on each invocation.
	target string

	// timeout bounds a single ReportTrace RPC.
	timeout time.Duration

	// resolvedCaller caches the result of resolveCaller so that we only
	// dereference GlobalConfig once.
	resolvedCaller atomic.Value // string
	resolveOnce    sync.Once

	// token is the business isolation token forwarded in each request. It is
	// updated by SetToken (invoked through TokenSetter) at runtime.
	token atomic.Value // string
}

// TRPCWriterOption configures a TRPCWriter.
type TRPCWriterOption func(*TRPCWriter)

// WithTRPCCaller explicitly sets the caller service name, overriding the
// default lookup via trpc.GlobalConfig(). Useful when the binary does not
// initialise a tRPC server but still needs to report traces.
func WithTRPCCaller(caller string) TRPCWriterOption {
	return func(w *TRPCWriter) { w.caller = caller }
}

// WithTRPCTarget sets the callee's service name, defaulting to
// "trpc.trs.prompt_log_collector.LogCollector".
func WithTRPCTarget(target string) TRPCWriterOption {
	return func(w *TRPCWriter) {
		if target != "" {
			w.target = target
		}
	}
}

// WithTRPCTimeout sets the per-call timeout.
func WithTRPCTimeout(d time.Duration) TRPCWriterOption {
	return func(w *TRPCWriter) {
		if d > 0 {
			w.timeout = d
		}
	}
}

// WithTRPCClient injects a pre-built LogCollectorClientProxy. This is mainly
// intended for tests, where the proxy is replaced by a mock.
func WithTRPCClient(proxy proto.LogCollectorClientProxy) TRPCWriterOption {
	return func(w *TRPCWriter) {
		if proxy != nil {
			w.proxy = proxy
		}
	}
}

// NewTRPCWriter creates a TRPCWriter wired to the log_collector service.
// Most users do not need to pass any options: the writer will read the caller
// name from the local tRPC configuration on first use.
func NewTRPCWriter(opts ...TRPCWriterOption) *TRPCWriter {
	w := &TRPCWriter{
		target:  defaultTRPCTarget,
		timeout: defaultTRPCTimeout,
	}
	for _, opt := range opts {
		opt(w)
	}
	if w.proxy == nil {
		w.proxy = proto.NewLogCollectorClientProxy()
	}
	return w
}

// SetToken implements TokenSetter. The new token is applied atomically and
// used by all subsequent Write calls.
func (w *TRPCWriter) SetToken(token string) {
	w.token.Store(token)
}

// loadToken returns the currently configured token, or the empty string.
func (w *TRPCWriter) loadToken() string {
	v := w.token.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// resolveCaller returns the service name to send as ReportTraceRequest.Caller.
//
// Resolution order:
//  1. An explicit value set via WithTRPCCaller.
//  2. trpc.GlobalConfig().Server.Service[0].Name, captured lazily on first Write.
//  3. "unknown" as a last-resort fallback (warned once).
func (w *TRPCWriter) resolveCaller(ctx context.Context) string {
	if w.caller != "" {
		return w.caller
	}
	w.resolveOnce.Do(func() {
		name := readCallerFromGlobalConfig()
		if name == "" {
			log.WarnfContext(ctx,
				"[promptsampler] TRPCWriter: trpc.GlobalConfig has no server service, "+
					"falling back to caller=%q; pass WithTRPCCaller(...) to override",
				fallbackCaller,
			)
			name = fallbackCaller
		}
		w.resolvedCaller.Store(name)
	})
	v := w.resolvedCaller.Load()
	if v == nil {
		return fallbackCaller
	}
	return v.(string)
}

// readCallerFromGlobalConfig reads the first service name from the current
// tRPC global configuration. It returns an empty string if the configuration
// is missing or has no services.
//
// Defined as a package-level var (not a closure) so tests can override it.
var readCallerFromGlobalConfig = func() string {
	cfg := trpc.GlobalConfig()
	if cfg == nil {
		return ""
	}
	if len(cfg.Server.Service) == 0 {
		return ""
	}
	svc := cfg.Server.Service[0]
	if svc == nil {
		return ""
	}
	return svc.Name
}

// Write implements TraceWriter. It serialises the trace to JSON and invokes
// LogCollector.ReportTrace. Errors are logged here (so they are never
// silently swallowed) and also returned to the caller so that AsyncWriter /
// PromptSampler can log contextual information.
func (w *TRPCWriter) Write(ctx context.Context, trace *Trace) error {
	if trace == nil {
		return nil
	}
	data, err := json.Marshal(trace)
	if err != nil {
		log.ErrorfContext(ctx,
			"[promptsampler] TRPCWriter: marshal trace failed: invocation_id=%s err=%v",
			trace.InvocationID, err,
		)
		return fmt.Errorf("marshal trace: %w", err)
	}

	// Detach from the caller's cancel/deadline chain so that an AsyncWriter
	// handing us an already-cancelled ctx can still upload; then layer on our
	// own timeout so the call remains bounded.
	rpcCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.timeout)
	defer cancel()

	caller := w.resolveCaller(rpcCtx)
	req := &proto.ReportTraceRequest{
		Caller:    caller,
		TraceJson: string(data),
		Token:     w.loadToken(),
	}

	rsp, err := w.proxy.ReportTrace(rpcCtx, req, client.WithTarget(w.target))
	if err != nil {
		log.ErrorfContext(ctx,
			"[promptsampler] TRPCWriter: ReportTrace rpc failed: "+
				"invocation_id=%s caller=%s target=%s err=%v",
			trace.InvocationID, caller, w.target, err,
		)
		return fmt.Errorf("report trace rpc: %w", err)
	}
	if rsp.GetCode() != 0 {
		log.ErrorfContext(ctx,
			"[promptsampler] TRPCWriter: ReportTrace biz failure: "+
				"invocation_id=%s caller=%s code=%d message=%s",
			trace.InvocationID, caller, rsp.GetCode(), rsp.GetMessage(),
		)
		return fmt.Errorf("report trace code=%d message=%s",
			rsp.GetCode(), rsp.GetMessage())
	}
	return nil
}
