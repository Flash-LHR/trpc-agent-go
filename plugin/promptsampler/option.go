//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

// Option configures a PromptSampler at construction time.
type Option func(*PromptSampler)

// WithName overrides the plugin name. Plugins registered in the same plugin
// manager must have unique names.
func WithName(name string) Option {
	return func(s *PromptSampler) {
		if name != "" {
			s.name = name
		}
	}
}

// WithSampleRate sets the sampling rate in [0, 1]. Values outside that range
// are clamped. A rate of 0 disables sampling, 1 samples every invocation.
//
// The rate can also be updated at runtime via SetConfig or the HTTP
// ConfigHandler.
func WithSampleRate(rate float64) Option {
	return func(s *PromptSampler) {
		if rate < 0 {
			rate = 0
		}
		if rate > 1 {
			rate = 1
		}
		cfg := s.runtimeConfig.Load()
		s.runtimeConfig.Store(&RuntimeConfig{
			Enabled:      cfg.Enabled,
			SampleRate:   rate,
			SamplerToken: cfg.SamplerToken,
		})
	}
}

// WithEnabled toggles the master sampling switch. When disabled, no
// invocation is sampled regardless of sample rate.
func WithEnabled(enabled bool) Option {
	return func(s *PromptSampler) {
		cfg := s.runtimeConfig.Load()
		s.runtimeConfig.Store(&RuntimeConfig{
			Enabled:      enabled,
			SampleRate:   cfg.SampleRate,
			SamplerToken: cfg.SamplerToken,
		})
	}
}

// WithSamplerToken sets the initial business isolation token (SamplerToken).
// This token is forwarded to the log collector as ReportTraceRequest.Token.
// It is a tenant / app label, not an access credential; the log collector
// is responsible for deciding which SamplerToken values to accept.
//
// The token can also be updated at runtime via SetConfig or the HTTP
// ConfigHandler; updates are propagated atomically to TokenSetter writers.
func WithSamplerToken(token string) Option {
	return func(s *PromptSampler) {
		cfg := s.runtimeConfig.Load()
		s.runtimeConfig.Store(&RuntimeConfig{
			Enabled:      cfg.Enabled,
			SampleRate:   cfg.SampleRate,
			SamplerToken: token,
		})
		if ts, ok := s.writer.(TokenSetter); ok {
			ts.SetToken(token)
		}
	}
}

// WithWriter installs a custom TraceWriter. Passing nil is a no-op.
func WithWriter(w TraceWriter) Option {
	return func(s *PromptSampler) {
		if w != nil {
			s.writer = w
		}
	}
}

// WithLogWriter selects the standard compact-JSON log writer.
func WithLogWriter() Option {
	return func(s *PromptSampler) {
		s.writer = NewLogWriter()
	}
}

// WithPrettyLogWriter selects the indented-JSON log writer.
func WithPrettyLogWriter() Option {
	return func(s *PromptSampler) {
		s.writer = NewPrettyLogWriter()
	}
}

// WithTRPCWriter installs the tRPC-based trace writer that uploads each trace
// to the log_collector service. This is the recommended writer for
// production deployments that already use the open-source tRPC distribution
// ("trpc.group/trpc-go/trpc-go").
//
// Typical usage:
//
//	sampler := promptsampler.New(
//	    promptsampler.WithSampleRate(1.0),
//	    promptsampler.WithTRPCWriter(),
//	    promptsampler.WithAsyncWrite(100),
//	)
func WithTRPCWriter(opts ...TRPCWriterOption) Option {
	return func(s *PromptSampler) {
		s.writer = NewTRPCWriter(opts...)
	}
}

// WithRPCWriter installs a TraceWriter that delegates transport to a
// caller-supplied ReportFunc. Use this when the host process cannot share a
// tRPC distribution with the plugin (for example Tencent's internal
// "git.code.oa.com/trpc-go/trpc-go") or needs to ship traces over a
// different protocol entirely.
//
// Typical usage (host process uses internal-version tRPC):
//
//	proxy := logpb.NewLogCollectorClientProxy()
//	reportTrace := func(ctx context.Context, caller, traceJSON, token string) error {
//	    rsp, err := proxy.ReportTrace(ctx, &logpb.ReportTraceRequest{
//	        Caller: caller, TraceJson: traceJSON, Token: token,
//	    })
//	    if err != nil { return err }
//	    if rsp.GetCode() != 0 {
//	        return fmt.Errorf("code=%d message=%s", rsp.GetCode(), rsp.GetMessage())
//	    }
//	    return nil
//	}
//	sampler := promptsampler.New(
//	    promptsampler.WithRPCWriter(
//	        promptsampler.WithRPCCaller("trpc.myapp.myserver"),
//	        promptsampler.WithRPCReportFunc(reportTrace),
//	    ),
//	    promptsampler.WithAsyncWrite(100),
//	)
func WithRPCWriter(opts ...RPCWriterOption) Option {
	return func(s *PromptSampler) {
		s.writer = NewRPCWriter(opts...)
	}
}

// WithMaxSteps caps the number of steps recorded per invocation. Once the
// cap is reached, further steps are dropped silently to bound memory use.
func WithMaxSteps(n int) Option {
	return func(s *PromptSampler) {
		if n > 0 {
			s.maxSteps = n
		}
	}
}

// WithAsyncWrite enables background trace uploads with the given queue
// length. Writes become non-blocking: when the queue saturates, Write
// returns ErrAsyncQueueFull and the trace is dropped.
//
// A queue length of 0 or less keeps synchronous behaviour.
func WithAsyncWrite(queueLen int) Option {
	return func(s *PromptSampler) {
		s.asyncQueueLen = queueLen
	}
}

// WithStructureID sets a default structure ID stamped onto every trace when
// the invocation has no explicit structure of its own. Defaults to the root
// agent name.
func WithStructureID(id string) Option {
	return func(s *PromptSampler) {
		s.defaultStructureID = id
	}
}
