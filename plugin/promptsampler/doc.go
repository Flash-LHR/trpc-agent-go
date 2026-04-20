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
// Typical usage:
//
//	sampler := promptsampler.New(
//	    promptsampler.WithSampleRate(1.0),
//	    promptsampler.WithTRPCWriter(),      // caller resolved from trpc.GlobalConfig()
//	    promptsampler.WithAsyncWrite(100),   // recommended for production
//	)
//	mgr, err := plugin.NewManager(sampler)
//	if err != nil { log.Fatal(err) }
//	runner := agent.NewRunner(myAgent, agent.WithPluginManager(mgr))
//
// Runtime configuration - the Enabled flag, sample rate and business
// isolation token - can be updated via SetConfig. When the underlying writer
// implements TokenSetter (as TRPCWriter and AsyncWriter do), the token is
// propagated automatically and takes effect on the next trace upload.
package promptsampler
