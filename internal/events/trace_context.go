package events

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// InjectTraceContext fills e.TraceParent and e.TraceState from the active
// span in ctx. Pass-through when no active span (or tracing disabled). Used
// by EmitCtx to capture the parent for downstream consumers (otelexport).
//
// Per the trace contract §5, only TraceContext propagator output is captured;
// baggage (which can carry workspace / actor) is NOT serialized into the
// event so we don't accidentally bake high-cardinality values into the
// JSONL log. Baggage is propagated via env vars at process boundaries
// instead.
func (e *Event) InjectTraceContext(ctx context.Context) {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	if tp := carrier.Get("traceparent"); tp != "" {
		e.TraceParent = tp
	}
	if ts := carrier.Get("tracestate"); ts != "" {
		e.TraceState = ts
	}
}

// ExtractTraceContext returns a context whose span context is reconstructed
// from e.TraceParent and e.TraceState. Returns the input ctx unchanged when
// the event has no trace fields (older records, or events emitted with no
// active span). Used by otelexport to root event-driven spans under the
// originating request.
func (e *Event) ExtractTraceContext(ctx context.Context) context.Context {
	if e.TraceParent == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{
		"traceparent": e.TraceParent,
	}
	if e.TraceState != "" {
		carrier["tracestate"] = e.TraceState
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

// EmitCtx is the context-aware emit path. It captures the active span context
// into the event before writing, so downstream consumers can rebuild it as
// the parent of any spans they emit. Falls back to plain Emit semantics
// when no span is active.
//
// The plain Emit method is preserved for callers that genuinely have no
// context to thread through (background timers, panic recovery sites). New
// code should prefer EmitCtx — see docs/observability/events-tracing-spike.md.
func (b *Bus) EmitCtx(ctx context.Context, e Event) error {
	e.InjectTraceContext(ctx)
	return b.Emit(e)
}
