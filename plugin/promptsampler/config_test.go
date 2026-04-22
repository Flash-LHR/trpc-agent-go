//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RuntimeConfig
		wantErr bool
	}{
		{"nil", nil, true},
		{"rate_zero", &RuntimeConfig{SampleRate: 0}, false},
		{"rate_half", &RuntimeConfig{SampleRate: 0.5}, false},
		{"rate_one", &RuntimeConfig{SampleRate: 1}, false},
		{"rate_negative", &RuntimeConfig{SampleRate: -0.01}, true},
		{"rate_gt_one", &RuntimeConfig{SampleRate: 1.01}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRuntimeConfig_Clone(t *testing.T) {
	t.Run("nil_returns_nil", func(t *testing.T) {
		var c *RuntimeConfig
		assert.Nil(t, c.Clone())
	})
	t.Run("deep_copy", func(t *testing.T) {
		orig := &RuntimeConfig{Enabled: true, SampleRate: 0.3, SamplerToken: "t1"}
		clone := orig.Clone()
		require.NotNil(t, clone)
		assert.Equal(t, orig.Enabled, clone.Enabled)
		assert.Equal(t, orig.SampleRate, clone.SampleRate)
		assert.Equal(t, orig.SamplerToken, clone.SamplerToken)
		// Mutating the clone must not affect the original.
		clone.SamplerToken = "t2"
		assert.Equal(t, "t1", orig.SamplerToken)
	})
}

func TestConfigHolder_LoadStore_Atomic(t *testing.T) {
	h := newConfigHolder(true, 0.1)

	// Initial state.
	got := h.Load()
	assert.True(t, got.Enabled)
	assert.InDelta(t, 0.1, got.SampleRate, 0)
	assert.Equal(t, "", got.SamplerToken)

	// Replace.
	h.Store(&RuntimeConfig{Enabled: false, SampleRate: 0.5, SamplerToken: "biz"})
	got = h.Load()
	assert.False(t, got.Enabled)
	assert.InDelta(t, 0.5, got.SampleRate, 0)
	assert.Equal(t, "biz", got.SamplerToken)
}

func TestConfigHolder_Concurrent(t *testing.T) {
	h := newConfigHolder(true, 0)
	var wg sync.WaitGroup
	const goroutines = 32
	const iters = 200

	// Writers.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				h.Store(&RuntimeConfig{
					Enabled:      j%2 == 0,
					SampleRate:   float64(j%100) / 100.0,
					SamplerToken: "tok",
				})
			}
		}(i)
	}
	// Readers.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = h.Load()
			}
		}()
	}
	wg.Wait()

	final := h.Load()
	require.NotNil(t, final)
	assert.True(t, final.SampleRate >= 0 && final.SampleRate < 1)
}

func TestAppConfigs_CloneIsDeep(t *testing.T) {
	src := &appConfigs{
		defaults:  &RuntimeConfig{Enabled: true, SampleRate: 0.5, SamplerToken: "d"},
		overrides: map[string]*RuntimeConfig{"A": {SampleRate: 1.0, SamplerToken: "a"}},
	}
	dst := src.Clone()

	// Mutating dst must not affect src.
	dst.defaults.Enabled = false
	dst.defaults.SamplerToken = "d2"
	dst.overrides["A"].SampleRate = 0
	dst.overrides["B"] = &RuntimeConfig{SampleRate: 0.9}

	assert.True(t, src.defaults.Enabled)
	assert.Equal(t, "d", src.defaults.SamplerToken)
	assert.InDelta(t, 1.0, src.overrides["A"].SampleRate, 0)
	_, has := src.overrides["B"]
	assert.False(t, has)
}

func TestAppConfigs_EffectiveFallback(t *testing.T) {
	s := &appConfigs{
		defaults: &RuntimeConfig{SampleRate: 0.1, SamplerToken: "d"},
		overrides: map[string]*RuntimeConfig{
			"A": {SampleRate: 1.0, SamplerToken: "a"},
		},
	}
	// Override hit.
	assert.InDelta(t, 1.0, s.effective("A").SampleRate, 0)
	// Override miss falls back to defaults.
	assert.InDelta(t, 0.1, s.effective("B").SampleRate, 0)
	// Empty app name always uses defaults.
	assert.InDelta(t, 0.1, s.effective("").SampleRate, 0)
}

func TestConfigHolder_PerApp_Concurrent(t *testing.T) {
	// One sampler instance under concurrent writes across different app
	// names plus a concurrent reader pool.
	s := New(WithSampleRate(0))
	var wg sync.WaitGroup
	const writers = 16
	const iters = 200

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app := "app-" + string(rune('A'+i))
			for j := 0; j < iters; j++ {
				_ = s.SetAppConfig(app, &RuntimeConfig{
					Enabled:      j%2 == 0,
					SampleRate:   float64(j%100) / 100.0,
					SamplerToken: "tok",
				})
			}
		}(i)
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = s.ListAppConfigs()
				_, _ = s.GetAppConfig("app-A")
			}
		}()
	}
	wg.Wait()

	// All writers landed: each of the 16 apps must have some override.
	got := s.ListAppConfigs()
	assert.Equal(t, writers, len(got))
	for name, cfg := range got {
		require.NotNil(t, cfg, "override %q must not be nil", name)
		assert.Equal(t, "tok", cfg.SamplerToken)
	}
}
