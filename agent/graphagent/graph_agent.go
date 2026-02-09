//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/barrier"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name              string
	description       string
	invokeSpanName    string
	graph             *graph.Graph
	executor          *graph.Executor
	subAgents         []agent.Agent
	agentCallbacks    *agent.Callbacks
	initialState      graph.State
	channelBufferSize int
	options           Options
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	// set default channel buffer size.
	options := defaultOptions

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	var (
		executor *graph.Executor
		err      error
	)
	if options.CheckpointSaver != nil {
		executor, err = graph.NewExecutor(
			g,
			graph.WithChannelBufferSize(options.ChannelBufferSize),
			graph.WithCheckpointSaver(options.CheckpointSaver),
		)
	} else {
		executor, err = graph.NewExecutor(
			g,
			graph.WithChannelBufferSize(options.ChannelBufferSize),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:              name,
		description:       options.Description,
		invokeSpanName:    itelemetry.OperationInvokeAgent + " " + name,
		graph:             g,
		executor:          executor,
		subAgents:         options.SubAgents,
		agentCallbacks:    options.AgentCallbacks,
		initialState:      options.InitialState,
		channelBufferSize: options.ChannelBufferSize,
		options:           options,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Setup invocation
	ga.setupInvocation(invocation)

	tracingDisabled := invocation != nil && invocation.RunOptions.DisableTracing
	// When the framework tracer is still the default no-op tracer, treating
	// tracing as disabled avoids extra goroutine/channel hops on hot paths while
	// preserving observable behavior (no spans are recorded).
	if !tracingDisabled && trace.IsNoopTracer() {
		tracingDisabled = true
	}

	if invocation != nil &&
		tracingDisabled &&
		ga.agentCallbacks == nil &&
		!barrier.Enabled(invocation) {
		initialState := ga.createInitialState(ctx, invocation)
		eventChan, err := ga.executor.Execute(ctx, initialState, invocation)
		if err != nil {
			out := make(chan *event.Event, 1)
			evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
				model.ErrorTypeFlowError, err.Error())
			if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
				log.Errorf("graphagent: emit error event failed: %v", emitErr)
			}
			close(out)
			return out, nil
		}
		return eventChan, nil
	}

	outSize := ga.channelBufferSize
	if invocation != nil && invocation.RunOptions.EventChannelBufferSize > 0 {
		outSize = invocation.RunOptions.EventChannelBufferSize
	}
	out := make(chan *event.Event, outSize)
	runCtx := agent.CloneContext(ctx)
	go ga.runWithBarrier(runCtx, invocation, out)
	return out, nil
}

// RunWithEventHandler executes the graph and delivers emitted events to handler.
//
// When tracing is disabled and there are no agent callbacks/barriers, this runs
// the graph in the current goroutine and avoids creating an extra event channel
// hop. This can reduce overhead for streaming integrations (for example SSE).
//
// For other execution modes it falls back to Agent.Run and forwards events from
// the returned channel to handler.
func (ga *GraphAgent) RunWithEventHandler(
	ctx context.Context,
	invocation *agent.Invocation,
	handler agent.EventHandler,
) error {
	if handler == nil {
		return fmt.Errorf("handler is nil")
	}

	ga.setupInvocation(invocation)

	if invocation != nil &&
		ga.agentCallbacks == nil &&
		!barrier.Enabled(invocation) {
		runCtx, cancel := context.WithCancel(agent.CloneContext(ctx))
		defer cancel()

		// Match GraphAgent.Run tracing semantics even when we avoid the extra
		// channel hop.
		if !invocation.RunOptions.DisableTracing {
			var span oteltrace.Span
			runCtx, span = trace.StartSpan(runCtx, ga.invokeSpanName)
			itelemetry.TraceBeforeInvokeAgent(span, invocation, ga.description, "", nil)
			defer span.End()
		}

		initialState := ga.createInitialState(runCtx, invocation)

		type handlerErrHolder struct {
			err error
		}
		var handlerErr atomic.Pointer[handlerErrHolder]

		prevHandler := invocation.EventHandler()
		invocation.SetEventHandler(func(hctx context.Context, evt *event.Event) error {
			if herr := handlerErr.Load(); herr != nil {
				return herr.err
			}
			if err := handler(hctx, evt); err != nil {
				if handlerErr.CompareAndSwap(nil, &handlerErrHolder{err: err}) {
					cancel()
				}
				return err
			}
			return nil
		})
		defer invocation.SetEventHandler(prevHandler)

		err := ga.executor.ExecuteBlocking(runCtx, initialState, invocation, nil)
		if herr := handlerErr.Load(); herr != nil {
			return herr.err
		}
		return err
	}

	eventChan, err := ga.Run(ctx, invocation)
	if err != nil {
		return err
	}
	for evt := range eventChan {
		if err := handler(ctx, evt); err != nil {
			return err
		}
	}
	return nil
}

// runWithBarrier emits a start barrier, waits for completion, then runs the graph with callbacks
// pipeline and forwards all events to the provided output channel.
func (ga *GraphAgent) runWithBarrier(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	ctx, span := trace.StartSpan(ctx, ga.invokeSpanName)
	itelemetry.TraceBeforeInvokeAgent(span, invocation, ga.description, "", nil)
	defer span.End()

	// GraphAgent.Run already owns the goroutine boundary that delivers events to
	// out. When there are no agent callbacks, execute the graph in this goroutine
	// to avoid an additional executor goroutine and forwarding hop.
	if ga.agentCallbacks == nil {
		// Emit a barrier event and wait for completion in a dedicated goroutine so that the runner can append all prior
		// events before GraphAgent reads history.
		if err := ga.emitStartBarrierAndWait(ctx, invocation, out); err != nil {
			evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
				model.ErrorTypeFlowError, err.Error())
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(itelemetry.KeyErrorType, model.ErrorTypeFlowError))
			if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
				log.Errorf("graphagent: emit error event failed: %v", emitErr)
			}
			close(out)
			return
		}

		initialState := ga.createInitialState(ctx, invocation)
		// ExecuteBlocking closes out when execution completes.
		_ = ga.executor.ExecuteBlocking(ctx, initialState, invocation, out)
		return
	}

	defer close(out)
	// Emit a barrier event and wait for completion in a dedicated goroutine so that the runner can append all prior
	// events before GraphAgent reads history.
	if err := ga.emitStartBarrierAndWait(ctx, invocation, out); err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, model.ErrorTypeFlowError))
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v", emitErr)
		}
		return
	}
	innerChan, err := ga.runWithCallbacks(ctx, invocation)
	if err != nil {
		evt := event.NewErrorEvent(invocation.InvocationID, invocation.AgentName,
			model.ErrorTypeFlowError, err.Error())
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, model.ErrorTypeFlowError))
		if emitErr := agent.EmitEvent(ctx, invocation, out, evt); emitErr != nil {
			log.Errorf("graphagent: emit error event failed: %v.", emitErr)
		}
		return
	}
	for evt := range innerChan {
		if err := event.EmitEvent(ctx, out, evt); err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(itelemetry.KeyErrorType, model.ErrorTypeFlowError))
			log.Errorf("graphagent: emit event failed: %v.", err)
			return
		}
	}
}

// emitStartBarrierAndWait emits a barrier event and waits until the runner has processed it,
// ensuring that all prior events have been appended to the session before GraphAgent reads history.
func (ga *GraphAgent) emitStartBarrierAndWait(ctx context.Context, invocation *agent.Invocation,
	ch chan<- *event.Event) error {
	// If graph barrier is not enabled, skip.
	if !barrier.Enabled(invocation) {
		return nil
	}
	barrier := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithObject(graph.ObjectTypeGraphBarrier))
	barrier.RequiresCompletion = true
	completionID := agent.GetAppendEventNoticeKey(barrier.ID)
	if noticeCh := invocation.AddNoticeChannel(ctx, completionID); noticeCh == nil {
		return fmt.Errorf("add notice channel for %s", completionID)
	}
	if err := agent.EmitEvent(ctx, invocation, ch, barrier); err != nil {
		return fmt.Errorf("emit barrier event: %w", err)
	}
	timeout := llmflow.WaitEventTimeout(ctx)
	if err := invocation.AddNoticeChannelAndWait(ctx, completionID, timeout); err != nil {
		return fmt.Errorf("wait for barrier completion: %w", err)
	}
	return nil
}

// runWithCallbacks executes the GraphAgent flow: prepare initial state, run before-agent callbacks, execute the graph,
// and wrap with after-agent callbacks when present.
func (ga *GraphAgent) runWithCallbacks(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Execute the graph.
	if ga.agentCallbacks != nil {
		result, err := ga.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
			Invocation: invocation,
		})
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customevent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customevent)
			close(eventChan)
			return eventChan, nil
		}
	}

	// Prepare initial state after callbacks so that any modifications
	// made by callbacks to the invocation (for example, RuntimeState,
	// Session, or Message) are visible to the graph execution.
	initialState := ga.createInitialState(ctx, invocation)

	// Execute the graph.
	eventChan, err := ga.executor.Execute(ctx, initialState, invocation)
	if err != nil {
		return nil, err
	}
	if ga.agentCallbacks != nil {
		return ga.wrapEventChannel(ctx, invocation, eventChan), nil
	}
	return eventChan, nil
}

func (ga *GraphAgent) createInitialState(ctx context.Context, invocation *agent.Invocation) graph.State {
	var initialState graph.State

	if ga.initialState != nil {
		// Clone the base initial state to avoid modifying the original.
		initialState = ga.initialState.Clone()
	} else {
		initialState = make(graph.State, 8)
	}

	// Merge runtime state from RunOptions if provided.
	if invocation.RunOptions.RuntimeState != nil {
		for key, value := range invocation.RunOptions.RuntimeState {
			initialState[key] = value
		}
	}

	// Seed messages from session events so multi‑turn runs share history.
	// This mirrors ContentRequestProcessor behavior used by non-graph flows.
	if invocation.Session != nil {
		sess := invocation.Session
		sess.EventMu.RLock()
		hasEvents := len(sess.Events) > 0
		sess.EventMu.RUnlock()

		sess.TracksMu.RLock()
		hasTracks := len(sess.Tracks) > 0
		sess.TracksMu.RUnlock()

		sess.SummariesMu.RLock()
		hasSummaries := len(sess.Summaries) > 0
		sess.SummariesMu.RUnlock()

		if hasEvents || hasTracks || hasSummaries {
			// Build a temporary request to reuse the processor logic.
			req := &model.Request{}

			// Default processor: include (possibly overridden) + preserve same branch.
			contentOpts := []processor.ContentOption{
				processor.WithAddSessionSummary(ga.options.AddSessionSummary),
				processor.WithMaxHistoryRuns(ga.options.MaxHistoryRuns),
				processor.WithPreserveSameBranch(true),
				processor.WithTimelineFilterMode(ga.options.messageTimelineFilterMode),
				processor.WithBranchFilterMode(ga.options.messageBranchFilterMode),
			}
			if ga.options.ReasoningContentMode != "" {
				contentOpts = append(contentOpts,
					processor.WithReasoningContentMode(ga.options.ReasoningContentMode))
			}
			if ga.options.summaryFormatter != nil {
				contentOpts = append(contentOpts,
					processor.WithSummaryFormatter(ga.options.summaryFormatter))
			}
			p := processor.NewContentRequestProcessor(contentOpts...)
			// We only need messages side effect; no output channel needed.
			p.ProcessRequest(ctx, invocation, req, nil)
			if len(req.Messages) > 0 {
				initialState[graph.StateKeyMessages] = req.Messages
			}
		}
	}

	// Add invocation message to state.
	// When resuming from checkpoint, only add user input if it's meaningful content
	// (not just a resume signal), following LangGraph's pattern.
	isResuming := invocation.RunOptions.RuntimeState != nil &&
		invocation.RunOptions.RuntimeState[graph.CfgKeyCheckpointID] != nil

	if invocation.Message.Content != "" {
		// If resuming and the message is just "resume", don't add it as input
		// This allows pure checkpoint resumption without input interference
		if isResuming && invocation.Message.Content == "resume" {
			// Skip adding user_input to preserve checkpoint state
		} else {
			// Add user input for normal execution or resume with meaningful input
			initialState[graph.StateKeyUserInput] = invocation.Message.Content
		}
	}
	// Add session context if available.
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}

	// Add parent agent to state so agent nodes can access sub-agents.
	initialState[graph.StateKeyParentAgent] = ga
	// Set checkpoint namespace if not already set.
	if ns, ok := initialState[graph.CfgKeyCheckpointNS].(string); !ok || ns == "" {
		initialState[graph.CfgKeyCheckpointNS] = ga.name
	}

	return initialState
}

func (ga *GraphAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = ga
	invocation.AgentName = ga.name

	ga.tuneRunOptionsForDirectStreaming(invocation)
}

func (ga *GraphAgent) tuneRunOptionsForDirectStreaming(invocation *agent.Invocation) {
	if invocation == nil {
		return
	}
	ro := &invocation.RunOptions
	if !ro.StreamModeEnabled || len(ro.StreamModes) == 0 {
		return
	}
	// When running without Runner's event loop (no appender attached), avoid
	// emitting graph lifecycle events that the caller did not request via
	// StreamModes. This preserves existing behavior when Runner is present and
	// reduces overhead for direct streaming integrations.
	if appender.IsAttached(invocation) {
		return
	}

	allowTasks := false
	allowUpdates := false
	for _, mode := range ro.StreamModes {
		switch mode {
		case agent.StreamModeTasks, agent.StreamModeDebug:
			allowTasks = true
		case agent.StreamModeUpdates:
			allowUpdates = true
		default:
		}
		if allowTasks && allowUpdates {
			break
		}
	}
	if !allowTasks {
		ro.DisableGraphExecutorEvents = true
		ro.DisableModelExecutionEvents = true
	}
	if !allowUpdates {
		ro.DisableGraphCompletionEvent = true
	}
	// Reduce per-request allocations for the common "direct streaming messages"
	// integration (e.g., SSE) when callers do not request task/update events.
	//
	// Callers can still override this explicitly via WithEventChannelBufferSize.
	if ro.EventChannelBufferSize <= 0 && !allowTasks && !allowUpdates {
		ro.EventChannelBufferSize = 32
	}
}

// Tools returns the list of tools available to this agent.
func (ga *GraphAgent) Tools() []tool.Tool { return nil }

// Info returns the basic information about this agent.
func (ga *GraphAgent) Info() agent.Info {
	return agent.Info{
		Name:        ga.name,
		Description: ga.description,
	}
}

// SubAgents returns the list of sub-agents available to this agent.
func (ga *GraphAgent) SubAgents() []agent.Agent {
	return ga.subAgents
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range ga.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (ga *GraphAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, ga.channelBufferSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(wrappedChan)
		var fullRespEvent *event.Event
		// Forward all events from the original channel
		for evt := range originalChan {
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				fullRespEvent = evt
			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}

		// Collect error from the final response event so after-agent
		// callbacks can observe execution failures, matching LLMAgent
		// semantics.
		var agentErr error
		if fullRespEvent != nil &&
			fullRespEvent.Response != nil &&
			fullRespEvent.Response.Error != nil {
			agentErr = fmt.Errorf(
				"%s: %s",
				fullRespEvent.Response.Error.Type,
				fullRespEvent.Response.Error.Message,
			)
		}

		// After all events are processed, run after agent callbacks
		result, err := ga.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
			Invocation:        invocation,
			Error:             agentErr,
			FullResponseEvent: fullRespEvent,
		})
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		var evt *event.Event
		if err != nil {
			// Send error event.
			evt = event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				agent.ErrorTypeAgentCallbackError,
				err.Error(),
			)
		} else if result != nil && result.CustomResponse != nil {
			// Create an event from the custom response.
			evt = event.NewResponseEvent(
				invocation.InvocationID,
				invocation.AgentName,
				result.CustomResponse,
			)
		}

		agent.EmitEvent(ctx, invocation, wrappedChan, evt)
	}(runCtx)
	return wrappedChan
}

// Executor returns the graph executor for direct access to checkpoint management.
func (ga *GraphAgent) Executor() *graph.Executor {
	return ga.executor
}
