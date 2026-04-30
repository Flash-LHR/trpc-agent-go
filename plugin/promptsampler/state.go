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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// streamingAccumulator aggregates text, usage and tool_calls across the
// partial+terminal frames of a single streaming model step. One accumulator
// is created per active model step (keyed by builderKey inside
// invocationState) and released once the terminal frame has been handled.
//
// Rationale (see design D2~D5 of fix-streaming-model-output-recording):
//   - Delta.Content is appended when non-empty (streaming delta semantics).
//   - Otherwise Message.Content is treated as "latest full snapshot" and
//     REPLACES the buffer (covers providers that refill the full assistant
//     text on every frame, and also covers non-streaming single-frame calls).
//   - Usage and ToolCalls use last-wins: the most recent non-zero / non-empty
//     snapshot wins. Zero-value usage objects are ignored to defend against
//     providers that pre-fill Usage={0,0,0} on early partial frames.
//   - Safe for usage-only frames where len(Choices)==0.
//
// All fields are guarded by mu. Callers must hold no other locks when
// invoking append/snapshot to avoid deadlocks with the enclosing
// invocationState locks.
type streamingAccumulator struct {
	mu            sync.Mutex
	textBuf       strings.Builder
	lastUsage     *model.Usage
	lastToolCalls []model.ToolCall
}

// newStreamingAccumulator creates an empty accumulator.
func newStreamingAccumulator() *streamingAccumulator {
	return &streamingAccumulator{}
}

// append merges one model.Response frame (partial or terminal) into the
// accumulator. It is tolerant of nil responses and of frames with empty
// Choices (e.g. openai's usage-only frame emitted when
// stream_options.include_usage is enabled).
func (a *streamingAccumulator) append(resp *model.Response) {
	if a == nil || resp == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// Usage: last-wins, ignoring {0,0,0} sentinels that some providers
	// pre-fill on early partial frames.
	if resp.Usage != nil && resp.Usage.TotalTokens > 0 {
		usageCopy := *resp.Usage
		a.lastUsage = &usageCopy
	}

	// ToolCalls and text live in Choices[0]; skip cleanly if absent.
	if len(resp.Choices) == 0 {
		return
	}
	choice := resp.Choices[0]

	// Tool calls: last-wins when the current frame provides a non-empty
	// ToolCalls list. Framework-level tool_call aggregation is assumed to
	// have happened upstream, so we only take the latest full snapshot.
	if len(choice.Message.ToolCalls) > 0 {
		a.lastToolCalls = choice.Message.ToolCalls
	}

	// Text: Delta.Content (streaming delta) wins; otherwise
	// Message.Content is treated as a full snapshot and REPLACES the buffer.
	if choice.Delta.Content != "" {
		a.textBuf.WriteString(choice.Delta.Content)
	} else if choice.Message.Content != "" {
		a.textBuf.Reset()
		a.textBuf.WriteString(choice.Message.Content)
	}
}

// snapshot returns the fully-aggregated (text, usage, toolCalls) view. It
// does not clear the accumulator; callers are expected to release the
// accumulator via invocationState.deleteAccumulator once the terminal frame
// has been handled.
func (a *streamingAccumulator) snapshot() (string, *model.Usage, []model.ToolCall) {
	if a == nil {
		return "", nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.textBuf.String(), a.lastUsage, a.lastToolCalls
}

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

	// accumulators tracks in-flight streaming accumulators for active model
	// steps, keyed by the same builderKey as currentBuilders. The pair
	// (builder, accumulator) shares the same lifecycle: created in
	// beforeModel, consumed at the terminal afterModel frame, and
	// released together via getBuilder/deleteAccumulator.
	accumulators   map[string]*streamingAccumulator
	accumulatorsMu sync.Mutex
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
		accumulators:    make(map[string]*streamingAccumulator),
	}
}

// getOrCreateAccumulator returns the accumulator bound to key, creating a
// fresh one on first access. Used by beforeModel to pre-allocate the
// accumulator so that subsequent partial frames in afterModel can always
// find a live container regardless of frame ordering.
func (s *invocationState) getOrCreateAccumulator(key string) *streamingAccumulator {
	s.accumulatorsMu.Lock()
	defer s.accumulatorsMu.Unlock()
	if acc, ok := s.accumulators[key]; ok && acc != nil {
		return acc
	}
	acc := newStreamingAccumulator()
	s.accumulators[key] = acc
	return acc
}

// getAccumulator returns the accumulator bound to key, or nil if absent.
func (s *invocationState) getAccumulator(key string) *streamingAccumulator {
	s.accumulatorsMu.Lock()
	defer s.accumulatorsMu.Unlock()
	return s.accumulators[key]
}

// deleteAccumulator removes the accumulator bound to key. Idempotent.
func (s *invocationState) deleteAccumulator(key string) {
	s.accumulatorsMu.Lock()
	defer s.accumulatorsMu.Unlock()
	delete(s.accumulators, key)
}

// clearAccumulators releases all accumulators. Called at invocation end
// so that any accumulator whose terminal frame never arrived (client
// cancel, connection drop, error at agent level) is freed instead of
// leaking into the parent stateManager.
func (s *invocationState) clearAccumulators() {
	s.accumulatorsMu.Lock()
	defer s.accumulatorsMu.Unlock()
	for k := range s.accumulators {
		delete(s.accumulators, k)
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

// delete removes a state. It also proactively releases any lingering
// streaming accumulators on that state so that aborted invocations
// (client cancel, agent-level error before the model terminal frame
// arrived) do not leak accumulator buffers.
func (m *stateManager) delete(invocationID string) {
	if val, ok := m.states.LoadAndDelete(invocationID); ok {
		if st, _ := val.(*invocationState); st != nil {
			st.clearAccumulators()
		}
	}
}

// clear removes all states and releases their accumulators.
func (m *stateManager) clear() {
	m.states.Range(func(key, val any) bool {
		if st, _ := val.(*invocationState); st != nil {
			st.clearAccumulators()
		}
		m.states.Delete(key)
		return true
	})
}
