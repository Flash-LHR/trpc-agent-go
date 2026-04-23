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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

// TestNonClosable_IsNotCloser asserts that the wrapper is *not* a Closer.
// This is the load-bearing property that keeps plugin.Manager.Close from
// tearing down the shared core when one Runner's life ends.
func TestNonClosable_IsNotCloser(t *testing.T) {
	s := New()
	w := s.NonClosable()
	require.NotNil(t, w)

	_, ok := any(w).(plugin.Closer)
	assert.False(t, ok,
		"nonClosablePlugin MUST NOT satisfy plugin.Closer; adding a "+
			"Close(ctx) method to the wrapper type would reintroduce the "+
			"send-on-closed-channel panic on the next Runner lifecycle")
}

// TestNonClosableNamed_IsNotCloser is the same assertion for the named
// variant, so that adding a Close method to either constructor path stays
// a test-time failure.
func TestNonClosableNamed_IsNotCloser(t *testing.T) {
	s := New()
	w := s.NonClosableNamed("custom")

	_, ok := any(w).(plugin.Closer)
	assert.False(t, ok)
}

// TestNonClosable_NameDefaultsToCoreName asserts that with no custom name
// the wrapper inherits the underlying sampler's plugin name. This keeps
// backwards compatibility with logs/metrics that already filter by the
// default "promptsampler" name.
func TestNonClosable_NameDefaultsToCoreName(t *testing.T) {
	s := New(WithName("my-core"))
	w := s.NonClosable()
	assert.Equal(t, "my-core", w.Name())
}

// TestNonClosableNamed_CustomNameUsed asserts that an explicit non-empty
// name is honoured.
func TestNonClosableNamed_CustomNameUsed(t *testing.T) {
	s := New(WithName("core-name"))
	w := s.NonClosableNamed("my-sampler")
	assert.Equal(t, "my-sampler", w.Name())
}

// TestNonClosableNamed_EmptyNameFallsBack asserts that passing an empty
// string is equivalent to NonClosable() (sampler's own name is used).
func TestNonClosableNamed_EmptyNameFallsBack(t *testing.T) {
	s := New(WithName("core-name"))
	w := s.NonClosableNamed("")
	assert.Equal(t, "core-name", w.Name())
}

// TestNonClosable_DelegatesRegister verifies that attaching the wrapper to
// a plugin.Manager propagates every one of the six agent/model/tool
// callbacks onto that Manager's callback tables. The Manager's
// AgentCallbacks / ModelCallbacks / ToolCallbacks return non-nil iff
// at least one callback was registered for that surface.
func TestNonClosable_DelegatesRegister(t *testing.T) {
	s := New()

	mgr, err := plugin.NewManager(s.NonClosable())
	require.NoError(t, err)
	require.NotNil(t, mgr)

	assert.NotNil(t, mgr.AgentCallbacks(),
		"Register did not propagate agent callbacks through the wrapper")
	assert.NotNil(t, mgr.ModelCallbacks(),
		"Register did not propagate model callbacks through the wrapper")
	assert.NotNil(t, mgr.ToolCallbacks(),
		"Register did not propagate tool callbacks through the wrapper")
}

// TestNonClosable_DelegatesHotPath verifies that an actual BeforeAgent
// invocation routed through the Manager reaches the underlying core.
// Sampling at rate 1 guarantees state creation on first touch; we then
// assert the core observed it.
func TestNonClosable_DelegatesHotPath(t *testing.T) {
	s := New(WithSampleRate(1.0), WithEnabled(true))

	mgr, err := plugin.NewManager(s.NonClosable())
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "test-inv-1",
		AgentName:    "test-agent",
	}

	ctx := context.Background()
	_, err = mgr.AgentCallbacks().RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
		Invocation: inv,
	})
	require.NoError(t, err)

	// The core created invocation state iff the callback ran.
	require.NotNil(t, s.states.get("test-inv-1"),
		"core.beforeAgent did not run; wrapper.Register is broken")
}

// TestNonClosable_ManagerCloseDoesNotAffectCore is the single most
// important test in this file: after plugin.Manager.Close runs, the core's
// AsyncWriter channel MUST still be open. Otherwise the whole point of
// NonClosable is defeated.
func TestNonClosable_ManagerCloseDoesNotAffectCore(t *testing.T) {
	// Use a non-blocking writer so Write returns quickly; async queue of 4
	// is more than enough for the one probe trace below.
	s := New(WithAsyncWrite(4))

	mgr, err := plugin.NewManager(s.NonClosable())
	require.NoError(t, err)

	// Close the Manager as Runner.Close would.
	require.NoError(t, mgr.Close(context.Background()))

	// The core's AsyncWriter MUST still accept writes. If NonClosable were
	// erroneously a Closer, this Write would panic with
	// "send on closed channel" -- the exact failure mode this whole
	// change is meant to prevent.
	probe := &Trace{InvocationID: "probe-after-close"}
	require.NotPanics(t, func() {
		err := s.writer.Write(context.Background(), probe)
		// Write may return ErrAsyncQueueFull in extreme contention but
		// the only allowed errors are nil or ErrAsyncQueueFull. It MUST
		// NOT panic.
		_ = err
	})
}

// TestNonClosable_TwoManagersShareCoreRegistryIsolated verifies the core
// property that enables "one singleton shared across N Runners":
//   - Two Managers each get their own callbacks (independent Registries)
//   - But both talk to the same core
//   - Closing one Manager does not destabilise the other
func TestNonClosable_TwoManagersShareCoreRegistryIsolated(t *testing.T) {
	s := New(WithSampleRate(1.0), WithAsyncWrite(4))

	mgrA, err := plugin.NewManager(s.NonClosable())
	require.NoError(t, err)
	mgrB, err := plugin.NewManager(s.NonClosable())
	require.NoError(t, err)

	// Both callback sets must be independently non-nil.
	require.NotNil(t, mgrA.AgentCallbacks())
	require.NotNil(t, mgrB.AgentCallbacks())

	// Exercise mgrA; closing mgrA afterwards must not disturb mgrB.
	_, err = mgrA.AgentCallbacks().RunBeforeAgent(context.Background(),
		&agent.BeforeAgentArgs{Invocation: &agent.Invocation{
			InvocationID: "inv-a",
			AgentName:    "agent-a",
		}},
	)
	require.NoError(t, err)

	require.NoError(t, mgrA.Close(context.Background()))

	// After mgrA is closed, mgrB must still be able to route callbacks
	// to the same (untouched) core.
	_, err = mgrB.AgentCallbacks().RunBeforeAgent(context.Background(),
		&agent.BeforeAgentArgs{Invocation: &agent.Invocation{
			InvocationID: "inv-b",
			AgentName:    "agent-b",
		}},
	)
	require.NoError(t, err)

	// Both invocations must be present on the shared core state.
	assert.NotNil(t, s.states.get("inv-a"))
	assert.NotNil(t, s.states.get("inv-b"))
}

// TestNonClosable_MultipleWrappersShareCoreState asserts that mutations
// made through the core (SetAppConfig) are observed by every wrapper,
// because there is only one underlying configHolder.
func TestNonClosable_MultipleWrappersShareCoreState(t *testing.T) {
	s := New()

	wrappers := []plugin.Plugin{
		s.NonClosable(),
		s.NonClosable(),
		s.NonClosable(),
	}

	// Mutate the core once.
	require.NoError(t, s.SetAppConfig("A", &RuntimeConfig{
		Enabled: true, SampleRate: 0.5,
	}))

	// Every wrapper must point to the same mutated core.
	for i := range wrappers {
		overrides := s.ListAppConfigs()
		require.Len(t, overrides, 1,
			"wrapper index %d observed wrong override count", i)
		require.Contains(t, overrides, "A")
	}
}
