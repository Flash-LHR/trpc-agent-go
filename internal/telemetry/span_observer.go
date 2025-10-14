package telemetry

import "context"

// SpanObserver receives the context containing a newly started span.
type SpanObserver func(context.Context)

type spanObserverKey struct{}

// WithSpanObserver injects a span observer into context.
func WithSpanObserver(ctx context.Context, observer SpanObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, spanObserverKey{}, observer)
}

// SpanObserverFromContext returns the span observer from context if present.
func SpanObserverFromContext(ctx context.Context) SpanObserver {
	if v, ok := ctx.Value(spanObserverKey{}).(SpanObserver); ok {
		return v
	}
	return nil
}
