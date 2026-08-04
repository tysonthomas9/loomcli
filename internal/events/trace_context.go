package events

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// ambientCtxProvider is consulted by Bus.Emit (when set) to capture trace
// context into every event without callers having to switch to EmitCtx. The
// CLI agent path installs this provider to point at cmdstore.RootContext()
// so all task/agent events emitted during a run land under the agent's root
// span. Stored as atomic.Value to allow lock-free reads on the hot Emit
// path.
var ambientCtxProvider atomic.Value // func() context.Context

// SetContextProvider installs a context provider read by Bus.Emit before
// each write. Pass nil to disable. Safe to call concurrently. See the
// contract doc for the policy: events emitted in agent processes carry
// the run's traceparent so otelexport can rebuild the parent span.
func SetContextProvider(fn func() context.Context) {
	if fn == nil {
		ambientCtxProvider.Store((func() context.Context)(nil))
		return
	}
	ambientCtxProvider.Store(fn)
}

func ambientCtx() context.Context {
	v := ambientCtxProvider.Load()
	if v == nil {
		return context.Background()
	}
	fn, _ := v.(func() context.Context)
	if fn == nil {
		return context.Background()
	}
	return fn()
}

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
