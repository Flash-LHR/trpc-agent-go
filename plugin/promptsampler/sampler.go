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
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Plugin-level defaults.
const (
	defaultPluginName = "promptsampler"
	defaultSampleRate = 0.0
	defaultMaxSteps   = 1000

	// teamMemberToolPrefix is the prefix for team-member tools (sub-agent
	// calls surfaced as tools). Their steps are filtered out so that the
	// sub-agent's own model/tool steps are recorded directly instead.
	teamMemberToolPrefix = "team-members-"

	// Truncation lengths for human-readable fields kept in the trace.
	inputTextMaxLen   = 1000
	outputTextMaxLen  = 1000
	toolResultMaxLen  = 2000
	toolArgsMaxLen    = 1000
	toolCallMaxLen    = 200
	reportFailTextLen = 256
)

// Context keys for passing builder identities between before/after callbacks.
type (
	modelBuilderKey struct{}
	toolBuilderKey  struct{}
)

// PromptSampler is a plugin.Plugin that samples, aggregates and exports
// execution traces from a trpc-agent-go Runner.
//
// A single PromptSampler is safe to reuse across concurrent Runner invocations.
// It keeps per-invocation state keyed by root invocation ID and writes exactly
// one Trace per root Runner task (on AfterAgent of the root).
type PromptSampler struct {
	name               string
	writer             TraceWriter
	maxSteps           int
	asyncQueueLen      int
	defaultStructureID string

	// runtimeConfig is read atomically on every sampling decision and can be
	// replaced via SetConfig without restarting the Runner.
	runtimeConfig *configHolder

	states      *stateManager
	asyncWriter *AsyncWriter

	closeOnce sync.Once
	closed    bool
	closeMu   sync.Mutex

	rng   *rand.Rand
	rngMu sync.Mutex
}

// New creates a new PromptSampler with the given options.
//
// Default behaviour:
//   - sampling rate 0 (nothing is sampled until configured)
//   - Enabled=true (so SetSampleRate is the single knob to turn it on)
//   - writer: LogWriter (compact JSON to the standard logger)
//   - synchronous writes (use WithAsyncWrite to enable back-pressure buffering)
func New(opts ...Option) *PromptSampler {
	s := &PromptSampler{
		name:          defaultPluginName,
		writer:        NewLogWriter(),
		maxSteps:      defaultMaxSteps,
		states:        newStateManager(),
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		runtimeConfig: newConfigHolder(true, defaultSampleRate),
	}
	for _, opt := range opts {
		opt(s)
	}
	// Wrap writer in async if requested.
	if s.asyncQueueLen > 0 {
		s.asyncWriter = NewAsyncWriter(s.writer, s.asyncQueueLen)
		s.writer = s.asyncWriter
	}
	return s
}

// Name implements plugin.Plugin.
func (s *PromptSampler) Name() string { return s.name }

// Register implements plugin.Plugin. It wires the sampler into the six
// agent/model/tool callbacks.
func (s *PromptSampler) Register(r *plugin.Registry) {
	if s == nil || r == nil {
		return
	}
	r.BeforeAgent(s.beforeAgent)
	r.AfterAgent(s.afterAgent)
	r.BeforeModel(s.beforeModel)
	r.AfterModel(s.afterModel)
	r.BeforeTool(s.beforeTool)
	r.AfterTool(s.afterTool)
}

// Close implements plugin.Closer. It drains the async writer (if used) and
// releases per-invocation state. Close is idempotent.
func (s *PromptSampler) Close(ctx context.Context) error {
	_ = ctx
	var err error
	s.closeOnce.Do(func() {
		s.closeMu.Lock()
		s.closed = true
		s.closeMu.Unlock()
		if s.asyncWriter != nil {
			err = s.asyncWriter.Close()
		}
		s.states.clear()
	})
	return err
}

// GetConfig returns a deep copy of the current runtime configuration.
// The returned pointer is owned by the caller and safe to mutate.
func (s *PromptSampler) GetConfig() *RuntimeConfig {
	return s.runtimeConfig.Load().Clone()
}

// SetConfig atomically installs a new runtime configuration. If the new
// configuration is invalid, the existing configuration is left unchanged and
// the error is returned.
//
// When the writer implements TokenSetter, the token field is forwarded so
// that subsequent trace uploads carry the new token.
func (s *PromptSampler) SetConfig(config *RuntimeConfig) error {
	if config == nil {
		return errors.New("config must not be nil")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	s.runtimeConfig.Store(config.Clone())
	if ts, ok := s.writer.(TokenSetter); ok {
		ts.SetToken(config.Token)
	}
	return nil
}

// shouldSample decides whether a root invocation should be sampled. It is
// called exactly once per root invocation in beforeAgent.
func (s *PromptSampler) shouldSample() bool {
	cfg := s.runtimeConfig.Load()
	if !cfg.Enabled {
		return false
	}
	switch {
	case cfg.SampleRate <= 0:
		return false
	case cfg.SampleRate >= 1:
		return true
	}
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	return s.rng.Float64() < cfg.SampleRate
}

// ---------- helpers ----------

// getRootInvocationID walks up the parent chain to the root invocation's ID.
func getRootInvocationID(inv *agent.Invocation) string {
	for inv.GetParentInvocation() != nil {
		inv = inv.GetParentInvocation()
	}
	return inv.InvocationID
}

// isSubAgentInvocation reports whether the invocation has a parent.
func isSubAgentInvocation(inv *agent.Invocation) bool {
	return inv.GetParentInvocation() != nil
}

// ---------- agent callbacks ----------

// beforeAgent initialises per-invocation state for the root agent. Sub-agents
// reuse their root's state so that all their steps are merged into one trace.
func (s *PromptSampler) beforeAgent(
	_ context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	inv := args.Invocation
	if isSubAgentInvocation(inv) {
		return nil, nil
	}
	sampled := s.shouldSample()
	structureID := s.defaultStructureID
	if structureID == "" {
		structureID = inv.AgentName
	}
	s.states.getOrCreate(inv.InvocationID, inv.AgentName, structureID, sampled)
	return nil, nil
}

// afterAgent builds the aggregate Trace for the root invocation and hands it
// to the writer. Errors are logged but never surfaced back to the Runner so
// that trace upload failures cannot break user-visible execution.
func (s *PromptSampler) afterAgent(
	ctx context.Context,
	args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return nil, nil
	}
	inv := args.Invocation
	// Sub-agents do not emit their own trace.
	if isSubAgentInvocation(inv) {
		return nil, nil
	}

	state := s.states.get(inv.InvocationID)
	if state == nil || !state.sampled {
		s.states.delete(inv.InvocationID)
		return nil, nil
	}

	trace := s.buildTrace(state, args)

	// Clean up state before writing to avoid accidental re-use.
	s.states.delete(inv.InvocationID)

	if err := s.writer.Write(ctx, trace); err != nil {
		// Writer implementations already log their own errors; we add a
		// top-level entry with the invocation ID so that operators can
		// correlate dropped traces even when the writer's log is filtered.
		log.ErrorfContext(ctx,
			"[promptsampler] trace write failed, dropped: invocation_id=%s err=%v",
			trace.InvocationID, err,
		)
	}
	return nil, nil
}

// buildTrace converts the accumulated state into the wire-level Trace.
func (s *PromptSampler) buildTrace(state *invocationState, args *agent.AfterAgentArgs) *Trace {
	endTime := time.Now()
	steps := state.getSteps()

	status := TraceStatusCompleted
	var errMsg string
	if args.Error != nil {
		status = TraceStatusFailed
		errMsg = args.Error.Error()
	}

	// Prefer the last model step's output as the final answer: in team
	// orchestration the FullResponseEvent can contain a coordinator-synthesized
	// response that duplicates member agent content.
	var finalOutput *TraceOutput
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.StepType == StepTypeModel && step.Output != nil && step.Output.Text != "" {
			finalOutput = &TraceOutput{Text: step.Output.Text}
			break
		}
	}
	if finalOutput == nil && args.FullResponseEvent != nil && args.FullResponseEvent.Response != nil {
		if len(args.FullResponseEvent.Response.Choices) > 0 {
			text := args.FullResponseEvent.Response.Choices[0].Message.Content
			if text != "" {
				finalOutput = &TraceOutput{Text: text}
			}
		}
	}

	return &Trace{
		StructureID:  state.structureID,
		InvocationID: state.invocationID,
		AgentName:    state.agentName,
		Status:       status,
		FinalOutput:  finalOutput,
		Steps:        steps,
		StartTime:    state.startTime,
		EndTime:      endTime,
		Duration:     endTime.Sub(state.startTime),
		Error:        errMsg,
	}
}

// ---------- model callbacks ----------

// beforeModel opens a model step in the root invocation's state.
func (s *PromptSampler) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}
	state := s.states.get(getRootInvocationID(inv))
	if state == nil || !state.sampled {
		return nil, nil
	}
	if state.stepCount() >= s.maxSteps {
		return nil, nil
	}

	stepID := state.nextStepID()

	// Use the last message's content as the textual "input" fingerprint.
	var inputText string
	msgCount := len(args.Request.Messages)
	if msgCount > 0 {
		inputText = args.Request.Messages[msgCount-1].Content
	}
	input := &TraceInput{
		Text:         truncate(inputText, inputTextMaxLen),
		MessageCount: msgCount,
	}

	builder := newStepBuilder(stepID, inv.AgentName, StepTypeModel).
		withInput(input)
	if isSubAgentInvocation(inv) {
		builder.withNodeKind(NodeKindMember)
	} else {
		builder.withNodeKind(NodeKindCoordinator)
	}
	if lastID := state.getLastStepID(); lastID != "" {
		builder.withPredecessors(lastID)
	}

	// Key the builder by the current invocation ID so nested agents don't
	// overwrite each other's in-flight builders.
	builderKey := inv.InvocationID + ":model"
	state.setBuilder(builderKey, builder)

	return &model.BeforeModelResult{
		Context: context.WithValue(ctx, modelBuilderKey{}, builderKey),
	}, nil
}

// afterModel finalises the model step created in beforeModel. Streaming
// partial responses are ignored; only the final response drives the trace.
func (s *PromptSampler) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	if args != nil && args.Response != nil && args.Response.IsPartial {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}
	state := s.states.get(getRootInvocationID(inv))
	if state == nil || !state.sampled {
		return nil, nil
	}
	builderKey, ok := ctx.Value(modelBuilderKey{}).(string)
	if !ok || builderKey == "" {
		return nil, nil
	}
	builder := state.getBuilder(builderKey)
	if builder == nil {
		return nil, nil
	}

	var (
		output *TraceOutput
		errMsg string
	)
	if args != nil {
		if args.Error != nil {
			errMsg = args.Error.Error()
		}
		if args.Response != nil {
			var outputText string
			if len(args.Response.Choices) > 0 {
				msg := args.Response.Choices[0].Message
				outputText = msg.Content
				if outputText == "" && len(msg.ToolCalls) > 0 {
					outputText = formatToolCalls(msg.ToolCalls)
				}
			}
			output = &TraceOutput{Text: truncate(outputText, outputTextMaxLen)}
			if args.Response.Usage != nil {
				output.TokenUsage = &TokenUsage{
					PromptTokens:     args.Response.Usage.PromptTokens,
					CompletionTokens: args.Response.Usage.CompletionTokens,
					TotalTokens:      args.Response.Usage.TotalTokens,
				}
			}
		}
	}

	step := builder.build(output, errMsg)
	state.addStep(step)
	state.setLastStepID(step.StepID)
	return nil, nil
}

// ---------- tool callbacks ----------

// beforeTool opens a tool step, skipping team-member tool wrappers.
func (s *PromptSampler) beforeTool(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	// Sub-agent dispatch tools are skipped; their underlying model/tool
	// steps are recorded directly via the aggregated state.
	if strings.HasPrefix(args.ToolName, teamMemberToolPrefix) {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}
	state := s.states.get(getRootInvocationID(inv))
	if state == nil || !state.sampled {
		return nil, nil
	}
	if state.stepCount() >= s.maxSteps {
		return nil, nil
	}

	stepID := state.nextStepID()
	input := &TraceInput{
		Text:          fmt.Sprintf("Tool call: %s", args.ToolName),
		ToolName:      args.ToolName,
		ToolArguments: truncate(string(args.Arguments), toolArgsMaxLen),
	}
	builder := newStepBuilder(stepID, args.ToolName, StepTypeTool).
		withInput(input).
		withNodeKind(NodeKindTool)
	if lastID := state.getLastStepID(); lastID != "" {
		builder.withPredecessors(lastID)
	}

	// Keyed by tool-call ID because the same tool can be invoked several
	// times within one invocation.
	builderKey := "tool:" + args.ToolCallID
	state.setBuilder(builderKey, builder)

	return &tool.BeforeToolResult{
		Context: context.WithValue(ctx, toolBuilderKey{}, builderKey),
	}, nil
}

// afterTool finalises the tool step created in beforeTool. Team-member tools
// that were filtered out in beforeTool have no matching builder and are a
// no-op here.
func (s *PromptSampler) afterTool(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (*tool.AfterToolResult, error) {
	if args == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, nil
	}
	state := s.states.get(getRootInvocationID(inv))
	if state == nil || !state.sampled {
		return nil, nil
	}
	builderKey := "tool:" + args.ToolCallID
	builder := state.getBuilder(builderKey)
	if builder == nil {
		// Expected for team-members-* tools that were filtered out.
		return nil, nil
	}

	var errMsg string
	if args.Error != nil {
		errMsg = args.Error.Error()
	}
	resultStr := formatToolResult(args.Result)
	output := &TraceOutput{
		Text:       truncate(resultStr, outputTextMaxLen),
		ToolResult: truncate(resultStr, toolResultMaxLen),
	}

	step := builder.build(output, errMsg)
	state.addStep(step)
	state.setLastStepID(step.StepID)
	return nil, nil
}

// ---------- formatting helpers ----------

// truncate returns s shortened to at most maxLen runes with an ellipsis
// suffix when truncation occurred.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatToolCalls renders a list of ToolCalls into a concise string so that
// the trace can record "the model asked to call X(args)" even when the model
// produced no textual Content.
func formatToolCalls(toolCalls []model.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Function.Name == "" {
			continue
		}
		args := string(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		parts = append(parts,
			fmt.Sprintf("→ %s(%s)", tc.Function.Name, truncate(args, toolCallMaxLen)))
	}
	return strings.Join(parts, "\n")
}

// formatToolResult renders a tool result into a display string. JSON-encoded
// structured results use json.Marshal; primitives fall back to fmt.
func formatToolResult(result any) string {
	if result == nil {
		return ""
	}
	switch v := result.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	default:
		data, err := json.Marshal(result)
		if err != nil {
			return fmt.Sprintf("%v", result)
		}
		return string(data)
	}
}

// Compile-time interface compliance checks.
var (
	_ plugin.Plugin = (*PromptSampler)(nil)
	_ plugin.Closer = (*PromptSampler)(nil)
)
