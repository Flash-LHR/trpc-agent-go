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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingReportFunc captures every ReportFunc invocation for later
// assertion. It is defined once and reused across tests for brevity.
type recordingReportFunc struct {
	mu      sync.Mutex
	calls   []recordedReportCall
	callCnt atomic.Int32
	err     error
}

type recordedReportCall struct {
	caller    string
	traceJSON string
	token     string
	hasDeadline bool
}

func (r *recordingReportFunc) fn() ReportFunc {
	return func(ctx context.Context, caller, traceJSON, token string) error {
		r.callCnt.Add(1)
		_, ok := ctx.Deadline()
		r.mu.Lock()
		r.calls = append(r.calls, recordedReportCall{
			caller: caller, traceJSON: traceJSON, token: token, hasDeadline: ok,
		})
		r.mu.Unlock()
		return r.err
	}
}

func (r *recordingReportFunc) last() (recordedReportCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return recordedReportCall{}, false
	}
	return r.calls[len(r.calls)-1], true
}

func TestRPCWriter_Write_HappyPath(t *testing.T) {
	rec := &recordingReportFunc{}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)

	require.NoError(t, w.Write(context.Background(), sampleTrace()))

	call, ok := rec.last()
	require.True(t, ok)
	assert.Equal(t, "trpc.myapp.myserver", call.caller)
	assert.Empty(t, call.token)
	assert.True(t, call.hasDeadline, "reportFunc should receive a deadline-bounded ctx")

	// Payload should round-trip through JSON back to the original trace.
	var decoded Trace
	require.NoError(t, json.Unmarshal([]byte(call.traceJSON), &decoded))
	assert.Equal(t, "inv-001", decoded.InvocationID)
}

func TestRPCWriter_Write_NilTrace_NoOp(t *testing.T) {
	rec := &recordingReportFunc{}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)

	require.NoError(t, w.Write(context.Background(), nil))
	assert.EqualValues(t, 0, rec.callCnt.Load())
}

func TestRPCWriter_Write_NoReportFunc(t *testing.T) {
	w := NewRPCWriter(WithRPCCaller("trpc.myapp.myserver"))
	err := w.Write(context.Background(), sampleTrace())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRPCWriterNoReportFunc)
}

func TestRPCWriter_Write_NoCaller(t *testing.T) {
	rec := &recordingReportFunc{}
	w := NewRPCWriter(WithRPCReportFunc(rec.fn()))

	err := w.Write(context.Background(), sampleTrace())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRPCWriterNoCaller)
	assert.EqualValues(t, 0, rec.callCnt.Load(),
		"reportFunc must not be invoked when caller is empty")
}

func TestRPCWriter_TokenHotReload(t *testing.T) {
	rec := &recordingReportFunc{}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)

	// First call: no token yet.
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	call, _ := rec.last()
	assert.Empty(t, call.token)

	// Hot reload.
	w.SetToken("biz-a")
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	call, _ = rec.last()
	assert.Equal(t, "biz-a", call.token)

	// Change again.
	w.SetToken("biz-b")
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	call, _ = rec.last()
	assert.Equal(t, "biz-b", call.token)
}

func TestRPCWriter_Write_ReportFuncError(t *testing.T) {
	rec := &recordingReportFunc{err: errors.New("code=1003 invalid JSON")}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)

	err := w.Write(context.Background(), sampleTrace())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "code=1003")
}

func TestRPCWriter_Write_SurvivesCancelledContext(t *testing.T) {
	// Simulates the AsyncWriter scenario where the caller's ctx is already
	// cancelled by the time Write runs; the reportFunc must still be invoked
	// because we detach via context.WithoutCancel.
	rec := &recordingReportFunc{}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
		WithRPCTimeout(200*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, w.Write(ctx, sampleTrace()))
	assert.EqualValues(t, 1, rec.callCnt.Load())
}

func TestRPCWriter_Concurrent_SetTokenAndWrite(t *testing.T) {
	rec := &recordingReportFunc{}
	w := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)

	const (
		writers      = 8
		writesEach   = 50
		totalWrites  = writers * writesEach
		mutatorIters = 200
	)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < mutatorIters; i++ {
			w.SetToken("t" + string(rune('0'+i%10)))
		}
	}()

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesEach; j++ {
				_ = w.Write(context.Background(), sampleTrace())
			}
		}()
	}

	wg.Wait()
	assert.EqualValues(t, totalWrites, rec.callCnt.Load())
}

func TestRPCWriter_AsTokenSetter_ViaAsyncWriter(t *testing.T) {
	// AsyncWriter's SetToken forwards to the wrapped writer when that writer
	// implements TokenSetter. RPCWriter does, so tokens set on AsyncWriter
	// must appear in the next ReportFunc invocation.
	rec := &recordingReportFunc{}
	rpc := NewRPCWriter(
		WithRPCReportFunc(rec.fn()),
		WithRPCCaller("trpc.myapp.myserver"),
	)
	async := NewAsyncWriter(rpc, 4)
	defer async.Close()

	async.SetToken("forwarded")

	require.NoError(t, async.Write(context.Background(), sampleTrace()))
	// Let the background worker drain.
	time.Sleep(20 * time.Millisecond)

	call, ok := rec.last()
	require.True(t, ok)
	assert.Equal(t, "forwarded", call.token)
}
