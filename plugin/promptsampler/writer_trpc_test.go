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
	"trpc.group/trpc-go/trpc-go/client"

	"trpc.group/trpc-go/trpc-agent-go/plugin/promptsampler/proto"
)

// mockProxy captures the last ReportTraceRequest for assertion and returns a
// canned response or error.
type mockProxy struct {
	mu       sync.Mutex
	lastReq  *proto.ReportTraceRequest
	rsp      *proto.ReportTraceResponse
	err      error
	callCnt  atomic.Int32
	onInvoke func() // optional hook for synchronisation in advanced tests
}

func (m *mockProxy) ReportTrace(
	_ context.Context, req *proto.ReportTraceRequest, _ ...client.Option,
) (*proto.ReportTraceResponse, error) {
	m.mu.Lock()
	// Make a defensive copy so callers cannot race with the pointer.
	reqCopy := *req
	m.lastReq = &reqCopy
	m.mu.Unlock()
	m.callCnt.Add(1)
	if m.onInvoke != nil {
		m.onInvoke()
	}
	return m.rsp, m.err
}

func (m *mockProxy) last() *proto.ReportTraceRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReq
}

// withStubbedGlobalConfig temporarily replaces the GlobalConfig lookup so
// tests can drive the caller-resolution path deterministically.
func withStubbedGlobalConfig(t *testing.T, serviceName string) {
	t.Helper()
	original := readCallerFromGlobalConfig
	readCallerFromGlobalConfig = func() string { return serviceName }
	t.Cleanup(func() { readCallerFromGlobalConfig = original })
}

func sampleTrace() *Trace {
	return &Trace{
		StructureID:  "agent-a",
		InvocationID: "inv-001",
		AgentName:    "agent-a",
		Status:       TraceStatusCompleted,
		Steps: []TraceStep{{
			StepID:   "sinv00000_1",
			NodeID:   "agent-a",
			StepType: StepTypeModel,
			Output:   &TraceOutput{Text: "ok"},
		}},
		StartTime: time.Unix(0, 0),
		EndTime:   time.Unix(1, 0),
		Duration:  time.Second,
	}
}

func TestTRPCWriter_Write_Success(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(WithTRPCClient(mp))

	err := w.Write(context.Background(), sampleTrace())
	require.NoError(t, err)

	req := mp.last()
	require.NotNil(t, req)
	assert.Equal(t, "trpc.myapp.myserver", req.Caller,
		"caller should be auto-resolved from GlobalConfig")
	assert.NotEmpty(t, req.TraceJson, "trace_json must be populated")
	assert.Empty(t, req.Token, "token is empty until SetToken is called")

	// Verify the payload is the JSON marshal of the trace.
	var decoded Trace
	require.NoError(t, json.Unmarshal([]byte(req.TraceJson), &decoded))
	assert.Equal(t, "inv-001", decoded.InvocationID)
}

func TestTRPCWriter_Write_NilTrace_NoOp(t *testing.T) {
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}
	w := NewTRPCWriter(WithTRPCClient(mp))

	require.NoError(t, w.Write(context.Background(), nil))
	assert.EqualValues(t, 0, mp.callCnt.Load())
}

func TestTRPCWriter_ExplicitCaller_OverridesGlobalConfig(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.auto.resolved")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(
		WithTRPCClient(mp),
		WithTRPCCaller("trpc.override.server"),
	)
	require.NoError(t, w.Write(context.Background(), sampleTrace()))

	assert.Equal(t, "trpc.override.server", mp.last().Caller)
}

func TestTRPCWriter_FallbackCaller_WhenGlobalConfigEmpty(t *testing.T) {
	withStubbedGlobalConfig(t, "")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(WithTRPCClient(mp))
	require.NoError(t, w.Write(context.Background(), sampleTrace()))

	assert.Equal(t, fallbackCaller, mp.last().Caller)
}

func TestTRPCWriter_TokenHotReload(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(WithTRPCClient(mp))

	// 1st call: no token yet.
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	assert.Empty(t, mp.last().Token)

	// Hot reload.
	w.SetToken("biz-a")

	// 2nd call: token should appear.
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	assert.Equal(t, "biz-a", mp.last().Token)

	// Change again.
	w.SetToken("biz-b")
	require.NoError(t, w.Write(context.Background(), sampleTrace()))
	assert.Equal(t, "biz-b", mp.last().Token)
}

func TestTRPCWriter_Write_RPCError(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{err: errors.New("network down")}

	w := NewTRPCWriter(WithTRPCClient(mp))
	err := w.Write(context.Background(), sampleTrace())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network down")
}

func TestTRPCWriter_Write_BusinessErrorCode(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{
		rsp: &proto.ReportTraceResponse{Code: 1003, Message: "JSON 格式非法"},
	}

	w := NewTRPCWriter(WithTRPCClient(mp))
	err := w.Write(context.Background(), sampleTrace())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "code=1003")
	assert.Contains(t, err.Error(), "JSON 格式非法")
}

func TestTRPCWriter_Write_SurvivesCancelledContext(t *testing.T) {
	// Simulates the AsyncWriter scenario where the caller's ctx is already
	// cancelled by the time Write runs; the RPC must still go through (within
	// its own timeout) because we detach via context.WithoutCancel.
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(
		WithTRPCClient(mp),
		WithTRPCTimeout(200*time.Millisecond),
	)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Write(cancelledCtx, sampleTrace())
	require.NoError(t, err)
	assert.EqualValues(t, 1, mp.callCnt.Load())
}

func TestTRPCWriter_Concurrent_SetTokenAndWrite(t *testing.T) {
	withStubbedGlobalConfig(t, "trpc.myapp.myserver")
	mp := &mockProxy{rsp: &proto.ReportTraceResponse{Code: 0}}

	w := NewTRPCWriter(WithTRPCClient(mp))

	const (
		writers      = 8
		writesEach   = 50
		totalWrites  = writers * writesEach
		mutatorIters = 200
	)

	var wg sync.WaitGroup

	// Token mutator.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < mutatorIters; i++ {
			w.SetToken("t" + string(rune('0'+i%10)))
		}
	}()

	// Writers.
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
	assert.EqualValues(t, totalWrites, mp.callCnt.Load())
}
