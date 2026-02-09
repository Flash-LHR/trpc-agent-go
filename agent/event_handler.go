package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// EventHandler consumes agent events during execution.
//
// Returning a non-nil error signals the caller to stop execution (typically by
// canceling the context) and propagate the error.
type EventHandler func(context.Context, *event.Event) error

// HandlerRunner is implemented by agents that can drive execution while
// delivering events directly to a handler, instead of returning an event
// channel.
//
// This is useful for low-latency streaming integrations (for example SSE)
// where eliminating an extra channel hop and goroutine can reduce overhead.
type HandlerRunner interface {
	RunWithEventHandler(ctx context.Context, invocation *Invocation, handler EventHandler) error
}

