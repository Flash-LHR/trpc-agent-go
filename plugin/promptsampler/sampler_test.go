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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

// captureWriter is a TraceWriter that records each Write call. It also
// implements TokenSetter so that SetConfig token propagation can be asserted.
type captureWriter struct {
	mu       sync.Mutex
	traces   []*Trace
	token    string
	writeErr error
}

func (w *captureWriter) Write(_ context.Context, t *Trace) error {
	w.mu.Lock()
	w.traces = append(w.traces, t)
	w.mu.Unlock()
	return w.writeErr
}

func (w *captureWriter) SetToken(token string) {
	w.mu.Lock()
	w.token = token
	w.mu.Unlock()
}

func (w *captureWriter) snapshot() (int, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.traces), w.token
}

func TestNew_Defaults(t *testing.T) {
	s := New()
	require.NotNil(t, s)
	assert.Equal(t, defaultPluginName, s.Name())
	assert.Equal(t, defaultMaxSteps, s.maxSteps)
	cfg := s.GetConfig()
	assert.True(t, cfg.Enabled)
	assert.InDelta(t, 0.0, cfg.SampleRate, 0)
	assert.Empty(t, cfg.Token)
}

func TestNew_WithOptions(t *testing.T) {
	cw := &captureWriter{}
	s := New(
		WithName("custom"),
		WithSampleRate(0.5),
		WithEnabled(true),
		WithWriter(cw),
		WithMaxSteps(42),
		WithStructureID("struct-v1"),
		WithToken("initial-token"),
	)

	assert.Equal(t, "custom", s.Name())
	assert.Equal(t, 42, s.maxSteps)
	assert.Equal(t, "struct-v1", s.defaultStructureID)

	cfg := s.GetConfig()
	assert.True(t, cfg.Enabled)
	assert.InDelta(t, 0.5, cfg.SampleRate, 0)
	assert.Equal(t, "initial-token", cfg.Token)

	// WithToken must propagate into the writer's TokenSetter.
	_, tok := cw.snapshot()
	assert.Equal(t, "initial-token", tok)
}

func TestWithSampleRate_Clamps(t *testing.T) {
	s := New(WithSampleRate(2.5))
	assert.InDelta(t, 1.0, s.GetConfig().SampleRate, 0)

	s2 := New(WithSampleRate(-0.5))
	assert.InDelta(t, 0.0, s2.GetConfig().SampleRate, 0)
}

func TestShouldSample(t *testing.T) {
	t.Run("disabled_never_samples", func(t *testing.T) {
		s := New(WithEnabled(false), WithSampleRate(1.0))
		for i := 0; i < 50; i++ {
			assert.False(t, s.shouldSample())
		}
	})
	t.Run("rate_zero_never_samples", func(t *testing.T) {
		s := New(WithSampleRate(0))
		for i := 0; i < 50; i++ {
			assert.False(t, s.shouldSample())
		}
	})
	t.Run("rate_one_always_samples", func(t *testing.T) {
		s := New(WithSampleRate(1))
		for i := 0; i < 50; i++ {
			assert.True(t, s.shouldSample())
		}
	})
	t.Run("rate_half_has_both", func(t *testing.T) {
		s := New(WithSampleRate(0.5))
		var sampled, skipped int
		for i := 0; i < 500; i++ {
			if s.shouldSample() {
				sampled++
			} else {
				skipped++
			}
		}
		assert.Greater(t, sampled, 0)
		assert.Greater(t, skipped, 0)
	})
}

func TestSetConfig_Validation(t *testing.T) {
	s := New()

	err := s.SetConfig(nil)
	assert.Error(t, err)

	err = s.SetConfig(&RuntimeConfig{SampleRate: 2})
	assert.Error(t, err)

	err = s.SetConfig(&RuntimeConfig{Enabled: true, SampleRate: 0.7, Token: "t"})
	require.NoError(t, err)

	got := s.GetConfig()
	assert.Equal(t, true, got.Enabled)
	assert.InDelta(t, 0.7, got.SampleRate, 0)
	assert.Equal(t, "t", got.Token)
}

func TestSetConfig_PropagatesTokenToWriter(t *testing.T) {
	cw := &captureWriter{}
	s := New(WithWriter(cw))

	require.NoError(t, s.SetConfig(&RuntimeConfig{
		Enabled: true, SampleRate: 0.1, Token: "new-token",
	}))

	_, tok := cw.snapshot()
	assert.Equal(t, "new-token", tok)
}

func TestSetConfig_PropagatesTokenThroughAsyncWriter(t *testing.T) {
	cw := &captureWriter{}
	s := New(WithWriter(cw), WithAsyncWrite(10))
	defer s.Close(context.Background())

	require.NoError(t, s.SetConfig(&RuntimeConfig{
		Enabled: true, SampleRate: 0.1, Token: "through-async",
	}))

	// Inner writer should have received the token through AsyncWriter's
	// TokenSetter forwarding.
	_, tok := cw.snapshot()
	assert.Equal(t, "through-async", tok)
}

func TestRegister_RegistersAllSixHooks(t *testing.T) {
	s := New(WithSampleRate(1.0))
	mgr, err := plugin.NewManager(s)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// The manager trims empty callback sets; non-nil means at least one hook
	// was registered for each plumbing point.
	assert.NotNil(t, mgr.AgentCallbacks())
	assert.NotNil(t, mgr.ModelCallbacks())
	assert.NotNil(t, mgr.ToolCallbacks())
}

func TestClose_Idempotent(t *testing.T) {
	s := New(WithAsyncWrite(4))

	require.NoError(t, s.Close(context.Background()))
	// Second close must not panic.
	require.NoError(t, s.Close(context.Background()))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.Equal(t, "abcd...", truncate("abcdef", 4))
	assert.Equal(t, "", truncate("", 5))
}

func TestFormatToolResult(t *testing.T) {
	assert.Equal(t, "", formatToolResult(nil))
	assert.Equal(t, "hi", formatToolResult("hi"))
	assert.Equal(t, "raw", formatToolResult([]byte("raw")))
	assert.Equal(t, "boom", formatToolResult(errors.New("boom")))
	// Structured value goes through json.Marshal.
	assert.Equal(t, `{"a":1}`, formatToolResult(map[string]int{"a": 1}))
}

// ---- Writer layer tests ----

func TestLogWriter_Write_NilTrace(t *testing.T) {
	w := NewLogWriter()
	require.NoError(t, w.Write(context.Background(), nil))
}

func TestNopWriter(t *testing.T) {
	w := NewNopWriter()
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
}

func TestAsyncWriter_Write_Succeeds(t *testing.T) {
	cw := &captureWriter{}
	aw := NewAsyncWriter(cw, 4)

	for i := 0; i < 4; i++ {
		require.NoError(t, aw.Write(context.Background(), sampleTrace()))
	}

	require.NoError(t, aw.Close())
	n, _ := cw.snapshot()
	assert.Equal(t, 4, n)
}

func TestAsyncWriter_QueueFull_ReturnsError(t *testing.T) {
	// A blocking writer holds up the worker so the queue fills.
	block := make(chan struct{})
	slow := &slowWriter{gate: block}
	aw := NewAsyncWriter(slow, 1)

	// 1st enqueues, consumed by the worker which then blocks on `gate`.
	require.NoError(t, aw.Write(context.Background(), sampleTrace()))
	// 2nd fills the 1-slot queue.
	require.NoError(t, aw.Write(context.Background(), sampleTrace()))
	// 3rd must fail fast.
	err := aw.Write(context.Background(), sampleTrace())
	assert.ErrorIs(t, err, ErrAsyncQueueFull)

	close(block)
	require.NoError(t, aw.Close())
}

func TestAsyncWriter_SetTokenForwards(t *testing.T) {
	cw := &captureWriter{}
	aw := NewAsyncWriter(cw, 4)
	defer aw.Close()

	aw.SetToken("forwarded")

	_, tok := cw.snapshot()
	assert.Equal(t, "forwarded", tok)
}

// slowWriter blocks on a gate channel, useful for simulating a clogged sink.
type slowWriter struct {
	gate chan struct{}
}

func (s *slowWriter) Write(_ context.Context, _ *Trace) error {
	<-s.gate
	return nil
}

func TestAsyncWriter_DetachesFromCancelledCtx(t *testing.T) {
	cw := &captureWriter{}
	aw := NewAsyncWriter(cw, 4)
	defer aw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	require.NoError(t, aw.Write(ctx, sampleTrace()))
	// Give the worker a moment to flush.
	time.Sleep(20 * time.Millisecond)
	n, _ := cw.snapshot()
	assert.Equal(t, 1, n, "async write should complete despite pre-cancelled ctx")
}
