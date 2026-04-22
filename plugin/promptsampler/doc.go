//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptsampler provides a Runner-scoped sampling plugin that collects
// execution traces and forwards them to the log_collector service.
//
// A single PromptSampler registers itself against the six standard callbacks
// (before/after Agent/Model/Tool) and emits exactly one Trace per root Runner
// invocation. Sub-agent invocations do not emit their own Trace; their steps
// are merged into the root Trace so that callers see a single DAG.
//
// Two transport writers are provided:
//
//   - TRPCWriter (WithTRPCWriter): zero-config upload via the open-source tRPC
//     distribution ("trpc.group/trpc-go/trpc-go"). Caller name is auto-resolved
//     from trpc.GlobalConfig().Server.Service[0].Name. Recommended for
//     services already in the open-source tRPC ecosystem.
//
//   - RPCWriter (WithRPCWriter): delegates transport to a caller-supplied
//     ReportFunc callback. Use this when the host process uses a different
//     tRPC distribution (e.g. Tencent's internal "git.code.oa.com/trpc-go/trpc-go")
//     or an entirely different protocol. The plugin assembles the payload
//     (caller, traceJSON, token) and the callback decides how to ship it.
//
// Typical usage with open-source tRPC:
//
//	sampler := promptsampler.New(
//	    promptsampler.WithSampleRate(1.0),
//	    promptsampler.WithTRPCWriter(),      // caller resolved from trpc.GlobalConfig()
//	    promptsampler.WithAsyncWrite(100),   // recommended for production
//	)
//
// Typical usage with an internal-version tRPC host:
//
//	sampler := promptsampler.New(
//	    promptsampler.WithSampleRate(1.0),
//	    promptsampler.WithRPCWriter(
//	        promptsampler.WithRPCCaller("trpc.myapp.myserver"),
//	        promptsampler.WithRPCReportFunc(myReportFunc),
//	    ),
//	    promptsampler.WithAsyncWrite(100),
//	)
//
// Runtime configuration - the Enabled flag, sample rate and business
// isolation token - can be updated via SetConfig. When the underlying writer
// implements TokenSetter (as TRPCWriter, RPCWriter and AsyncWriter do), the
// token is propagated automatically and takes effect on the next trace
// upload.
package promptsampler
