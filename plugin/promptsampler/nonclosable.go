//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import "trpc.group/trpc-go/trpc-agent-go/plugin"

// nonClosablePlugin wraps a *PromptSampler so that it can be attached to one
// or more Runners via runner.WithPlugins(...) without the Runner being able
// to close the underlying sampler when Runner.Close runs.
//
// The wrapper implements plugin.Plugin (Name + Register) but deliberately
// does NOT implement plugin.Closer (no Close(ctx) method). That absence is
// load-bearing: plugin.Manager.Close checks every plugin via a type
// assertion `p.(Closer)` — because this wrapper type has no Close method,
// the assertion fails and the manager skips the wrapper entirely, leaving
// the shared core (AsyncWriter channel, configHolder, states) untouched.
//
// The core's Register is forwarded verbatim: each Runner ends up with its
// own plugin.Registry that calls straight into the shared core's hot-path
// callbacks. The core is already safe for concurrent use across Runners
// because per-invocation state is keyed by invocationID (sync.Map).
type nonClosablePlugin struct {
	core *PromptSampler
	name string
}

// Name implements plugin.Plugin.
func (p *nonClosablePlugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin. It forwards registration verbatim to
// the underlying *PromptSampler so that the six agent/model/tool callbacks
// are attached to the caller-supplied Registry. Registering the same core
// into N independent Registries is safe: the core's callbacks only read
// per-invocation state (keyed by invocationID) and never mutate shared
// construction-time fields.
func (p *nonClosablePlugin) Register(r *plugin.Registry) {
	if p == nil || p.core == nil {
		return
	}
	p.core.Register(r)
}

// Compile-time assertion: nonClosablePlugin MUST implement plugin.Plugin but
// MUST NOT satisfy plugin.Closer. Adding a Close(ctx context.Context) error
// method to this type would defeat the entire purpose of this file (re-enable
// the Runner.Close -> close(AsyncWriter.ch) -> next-request panic loop) and
// must not be done without replacing the wrapper mechanism.
var _ plugin.Plugin = (*nonClosablePlugin)(nil)
