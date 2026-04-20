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
// It is the contract shared between the in-process sampler and the external
// control-plane (GET/PUT /promptiter/v1/apps/{app}/plugins/trace_reporter/config).
// The struct is JSON-serializable so the control-plane can round-trip it.
type RuntimeConfig struct {
	// Enabled is the master switch; when false, sampling is skipped entirely.
	Enabled bool `json:"enabled"`
	// SampleRate is the probability of sampling an invocation, in [0, 1].
	SampleRate float64 `json:"sample_rate"`
	// Token is the business isolation token forwarded to the log collector
	// via ReportTraceRequest.Token. It is assigned by the platform and used
	// to filter traces by tenant / app.
	Token string `json:"token,omitempty"`
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
		Enabled:    c.Enabled,
		SampleRate: c.SampleRate,
		Token:      c.Token,
	}
}

// configHolder wraps atomic.Value to provide lock-free typed access to a
// RuntimeConfig. It is used in the sampler's hot path so that concurrent
// invocations can read the current config without contention.
type configHolder struct {
	value atomic.Value // stores *RuntimeConfig
}

// newConfigHolder constructs a configHolder initialised with the given
// enabled / sampleRate values and an empty token.
func newConfigHolder(enabled bool, sampleRate float64) *configHolder {
	h := &configHolder{}
	h.value.Store(&RuntimeConfig{
		Enabled:    enabled,
		SampleRate: sampleRate,
	})
	return h
}

// Load returns the current RuntimeConfig. The returned pointer must be treated
// as read-only; callers that need to mutate it should Clone first.
func (h *configHolder) Load() *RuntimeConfig {
	return h.value.Load().(*RuntimeConfig)
}

// Store atomically replaces the held RuntimeConfig. Callers should pass in a
// fresh instance (or Clone the result of Load) to avoid aliasing.
func (h *configHolder) Store(config *RuntimeConfig) {
	h.value.Store(config)
}
