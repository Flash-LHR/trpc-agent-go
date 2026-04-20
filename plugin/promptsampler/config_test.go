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
		orig := &RuntimeConfig{Enabled: true, SampleRate: 0.3, Token: "t1"}
		clone := orig.Clone()
		require.NotNil(t, clone)
		assert.Equal(t, orig.Enabled, clone.Enabled)
		assert.Equal(t, orig.SampleRate, clone.SampleRate)
		assert.Equal(t, orig.Token, clone.Token)
		// Mutating the clone must not affect the original.
		clone.Token = "t2"
		assert.Equal(t, "t1", orig.Token)
	})
}

func TestConfigHolder_LoadStore_Atomic(t *testing.T) {
	h := newConfigHolder(true, 0.1)

	// Initial state.
	got := h.Load()
	assert.True(t, got.Enabled)
	assert.InDelta(t, 0.1, got.SampleRate, 0)
	assert.Equal(t, "", got.Token)

	// Replace.
	h.Store(&RuntimeConfig{Enabled: false, SampleRate: 0.5, Token: "biz"})
	got = h.Load()
	assert.False(t, got.Enabled)
	assert.InDelta(t, 0.5, got.SampleRate, 0)
	assert.Equal(t, "biz", got.Token)
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
					Enabled:    j%2 == 0,
					SampleRate: float64(j%100) / 100.0,
					Token:      "tok",
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
