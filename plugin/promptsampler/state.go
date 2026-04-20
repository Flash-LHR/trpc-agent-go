//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// invocationState holds the in-flight collection state for one root invocation.
//
// Sub-agent invocations do not own state; their steps are appended to the
// root's invocationState so that a single Trace represents the whole run.
type invocationState struct {
	invocationID string
	agentName    string
	structureID  string
	// sampled indicates whether this invocation was selected for sampling.
	sampled bool
	// startTime is when the root invocation started.
	startTime time.Time

	// steps and stepsMu protect the completed step slice.
	steps   []TraceStep
	stepsMu sync.Mutex

	// stepSeq generates monotonic step IDs.
	stepSeq atomic.Int64

	// lastStepID tracks the most recently completed step so that the next
	// step can be wired as its successor in the DAG.
	lastStepID string
	lastStepMu sync.Mutex

	// currentBuilders tracks in-flight step builders keyed by a caller-chosen
	// key (e.g. "<invocationID>:model" or "tool:<toolCallID>").
	currentBuilders map[string]*stepBuilder
	buildersMu      sync.Mutex
}

// newInvocationState creates a new invocationState.
func newInvocationState(invocationID, agentName, structureID string, sampled bool) *invocationState {
	return &invocationState{
		invocationID:    invocationID,
		agentName:       agentName,
		structureID:     structureID,
		sampled:         sampled,
		startTime:       time.Now(),
		steps:           make([]TraceStep, 0),
		currentBuilders: make(map[string]*stepBuilder),
	}
}

// addStep appends a completed step.
func (s *invocationState) addStep(step TraceStep) {
	s.stepsMu.Lock()
	defer s.stepsMu.Unlock()
	s.steps = append(s.steps, step)
}

// stepCount returns the current number of recorded steps.
func (s *invocationState) stepCount() int {
	s.stepsMu.Lock()
	defer s.stepsMu.Unlock()
	return len(s.steps)
}

// getSteps returns a defensive copy of all recorded steps.
func (s *invocationState) getSteps() []TraceStep {
	s.stepsMu.Lock()
	defer s.stepsMu.Unlock()
	result := make([]TraceStep, len(s.steps))
	copy(result, s.steps)
	return result
}

// nextStepID generates the next step ID of the form "s<shortInv>_<seq>".
func (s *invocationState) nextStepID() string {
	seq := s.stepSeq.Add(1)
	shortID := s.invocationID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return "s" + shortID + "_" + strconv.FormatInt(seq, 10)
}

// setLastStepID updates the most recently completed step ID.
func (s *invocationState) setLastStepID(stepID string) {
	s.lastStepMu.Lock()
	defer s.lastStepMu.Unlock()
	s.lastStepID = stepID
}

// getLastStepID reads the most recently completed step ID.
func (s *invocationState) getLastStepID() string {
	s.lastStepMu.Lock()
	defer s.lastStepMu.Unlock()
	return s.lastStepID
}

// setBuilder stores an in-flight step builder.
func (s *invocationState) setBuilder(key string, builder *stepBuilder) {
	s.buildersMu.Lock()
	defer s.buildersMu.Unlock()
	s.currentBuilders[key] = builder
}

// getBuilder retrieves and removes a step builder by key.
func (s *invocationState) getBuilder(key string) *stepBuilder {
	s.buildersMu.Lock()
	defer s.buildersMu.Unlock()
	builder := s.currentBuilders[key]
	delete(s.currentBuilders, key)
	return builder
}

// stepBuilder incrementally constructs a TraceStep.
type stepBuilder struct {
	stepID             string
	nodeID             string
	stepType           StepType
	nodeKind           NodeKind
	predecessorStepIDs []string
	input              *TraceInput
	startTime          time.Time
}

// newStepBuilder creates a new stepBuilder with startTime set to now.
func newStepBuilder(stepID, nodeID string, stepType StepType) *stepBuilder {
	return &stepBuilder{
		stepID:    stepID,
		nodeID:    nodeID,
		stepType:  stepType,
		startTime: time.Now(),
	}
}

// withPredecessors sets predecessor step IDs.
func (b *stepBuilder) withPredecessors(ids ...string) *stepBuilder {
	b.predecessorStepIDs = ids
	return b
}

// withNodeKind sets the node kind.
func (b *stepBuilder) withNodeKind(kind NodeKind) *stepBuilder {
	b.nodeKind = kind
	return b
}

// withInput sets the step input.
func (b *stepBuilder) withInput(input *TraceInput) *stepBuilder {
	b.input = input
	return b
}

// build finalises the step with the given output / error and returns it.
func (b *stepBuilder) build(output *TraceOutput, errMsg string) TraceStep {
	endTime := time.Now()
	return TraceStep{
		StepID:             b.stepID,
		NodeID:             b.nodeID,
		StepType:           b.stepType,
		NodeKind:           b.nodeKind,
		PredecessorStepIDs: b.predecessorStepIDs,
		Input:              b.input,
		Output:             output,
		Error:              errMsg,
		StartTime:          b.startTime,
		EndTime:            endTime,
		Duration:           endTime.Sub(b.startTime),
	}
}

// stateManager maintains per-root-invocation states using sync.Map for
// lock-free lookup in the hot path.
type stateManager struct {
	states sync.Map // map[string]*invocationState
}

// newStateManager creates an empty stateManager.
func newStateManager() *stateManager {
	return &stateManager{}
}

// getOrCreate fetches an existing state or creates a new one keyed by
// invocationID. The sampled flag is honoured only on first creation.
func (m *stateManager) getOrCreate(invocationID, agentName, structureID string, sampled bool) *invocationState {
	state := newInvocationState(invocationID, agentName, structureID, sampled)
	actual, _ := m.states.LoadOrStore(invocationID, state)
	return actual.(*invocationState)
}

// get retrieves an existing state or nil.
func (m *stateManager) get(invocationID string) *invocationState {
	val, ok := m.states.Load(invocationID)
	if !ok {
		return nil
	}
	return val.(*invocationState)
}

// delete removes a state.
func (m *stateManager) delete(invocationID string) {
	m.states.Delete(invocationID)
}

// clear removes all states.
func (m *stateManager) clear() {
	m.states.Range(func(key, _ any) bool {
		m.states.Delete(key)
		return true
	})
}
