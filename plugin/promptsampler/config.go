//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"errors"
	"sync/atomic"
)

// RuntimeConfig is the mutable runtime configuration of a PromptSampler.
//
// It is the JSON-serialisable contract shared between the in-process sampler
// and the external control-plane (see PromptSampler.ConfigHandler). The
// control-plane may round-trip the struct in HTTP request / response bodies.
//
// SamplerToken (below) is a *business isolation* label that the writer
// forwards to the log_collector as ReportTraceRequest.Token. It identifies
// the calling tenant / app on the collector side. Tenant-level access
// control (deciding which SamplerToken values are acceptable) is handled
// by the log_collector itself, not by this plugin.
type RuntimeConfig struct {
	// Enabled is the master switch; when false, sampling is skipped entirely.
	Enabled bool `json:"enabled"`
	// SampleRate is the probability of sampling an invocation, in [0, 1].
	SampleRate float64 `json:"sample_rate"`
	// SamplerToken is the business isolation token forwarded to the log
	// collector via ReportTraceRequest.Token. It is assigned by the platform
	// and used to filter traces by tenant / app.
	SamplerToken string `json:"sampler_token,omitempty"`
}

// Validate checks whether the configuration is acceptable.
func (c *RuntimeConfig) Validate() error {
	if c == nil {
		return errors.New("config must not be nil")
	}
	if c.SampleRate < 0 || c.SampleRate > 1 {
		return errors.New("sample_rate must be between 0 and 1")
	}
	return nil
}

// Clone returns a deep copy of the RuntimeConfig.
func (c *RuntimeConfig) Clone() *RuntimeConfig {
	if c == nil {
		return nil
	}
	return &RuntimeConfig{
		Enabled:      c.Enabled,
		SampleRate:   c.SampleRate,
		SamplerToken: c.SamplerToken,
	}
}

// configHolder wraps atomic.Value to provide lock-free typed access to the
// sampler's full configuration snapshot (default + per-app overrides). It is
// used in the sampler's hot path so that concurrent invocations can read the
// current config without contention.
type configHolder struct {
	value atomic.Value // stores *appConfigs
}

// newConfigHolder constructs a configHolder initialised with the given
// default enabled / sampleRate values (and no per-app overrides).
func newConfigHolder(enabled bool, sampleRate float64) *configHolder {
	h := &configHolder{}
	h.value.Store(&appConfigs{
		defaults: &RuntimeConfig{
			Enabled:    enabled,
			SampleRate: sampleRate,
		},
		overrides: map[string]*RuntimeConfig{},
	})
	return h
}

// loadSnapshot returns the current full configuration snapshot. The returned
// pointer must be treated as read-only; callers that need to mutate it should
// Clone first (and then use storeSnapshot to publish the new version).
func (h *configHolder) loadSnapshot() *appConfigs {
	return h.value.Load().(*appConfigs)
}

// storeSnapshot atomically replaces the held snapshot. Callers should pass in
// a fresh instance (or a Clone of loadSnapshot's result) to avoid aliasing.
func (h *configHolder) storeSnapshot(snap *appConfigs) {
	h.value.Store(snap)
}

// Load returns a copy of the current default RuntimeConfig, preserving the
// legacy semantics used by GetConfig. It performs a single atomic.Load.
func (h *configHolder) Load() *RuntimeConfig {
	return h.loadSnapshot().defaults
}

// Store atomically replaces the default RuntimeConfig while keeping the
// current per-app overrides untouched. It is used by SetConfig.
func (h *configHolder) Store(cfg *RuntimeConfig) {
	cur := h.loadSnapshot()
	next := cur.Clone()
	next.defaults = cfg
	h.storeSnapshot(next)
}

// appConfigs is the full, immutable configuration snapshot: a default
// RuntimeConfig plus a map of per-app overrides. A fresh appConfigs is built
// whenever the snapshot changes (copy-on-write), which keeps the hot path
// lock-free while preserving defensive independence between updates.
type appConfigs struct {
	// defaults is the RuntimeConfig used when no per-app override matches.
	defaults *RuntimeConfig
	// overrides maps appName to a RuntimeConfig that replaces defaults for
	// invocations whose resolved appName matches the key. A nil map behaves
	// the same as an empty map and is never nil on a snapshot emitted by
	// newConfigHolder.
	overrides map[string]*RuntimeConfig
}

// Clone returns a deep copy of the snapshot. Mutations performed on the
// result must not be visible on the source, which makes copy-on-write safe
// even when writers interleave.
func (s *appConfigs) Clone() *appConfigs {
	if s == nil {
		return nil
	}
	out := &appConfigs{
		defaults:  s.defaults.Clone(),
		overrides: make(map[string]*RuntimeConfig, len(s.overrides)),
	}
	for k, v := range s.overrides {
		out.overrides[k] = v.Clone()
	}
	return out
}

// effective returns the RuntimeConfig that applies to the given appName.
// When an override is registered it wins; otherwise the default config is
// returned. The result shares storage with the snapshot and must be treated
// as read-only.
func (s *appConfigs) effective(app string) *RuntimeConfig {
	if s == nil {
		return nil
	}
	if app != "" && len(s.overrides) > 0 {
		if cfg, ok := s.overrides[app]; ok && cfg != nil {
			return cfg
		}
	}
	return s.defaults
}
