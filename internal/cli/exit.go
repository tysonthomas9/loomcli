package cli

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// activeRootSpan and activeTraceShutdown are populated by Execute() with the
// per-invocation root span and trace-provider shutdown function. ExitWithFlush
// reads them to end the root span and flush exporters before the process
// terminates via os.Exit.
//
// Stored as atomic values so concurrent goroutines (e.g., the signal handler
// in Execute and a Cobra Run handler racing to exit) can read them without a
// lock. Both are nil before tracing init and a no-op after shutdown.
var (
	activeRootSpan      atomic.Value // trace.Span
	activeTraceShutdown atomic.Value // func(context.Context) error
)

// RegisterActiveTraceState publishes the per-invocation tracing state so
// ExitWithFlush can find it from any goroutine. Called from cli.Execute
// after tracing.Init.
func RegisterActiveTraceState(span trace.Span, shutdown func(context.Context) error) {
	activeRootSpan.Store(span)
	activeTraceShutdown.Store(shutdown)
}

// ExitWithFlush ends the active root span, flushes the trace provider with a
// short timeout, and then calls os.Exit(code). Use this from CLI handlers
// instead of os.Exit when traces should be exported even on the failure
// path. Safe to call from any goroutine; multiple concurrent callers are
// idempotent (the underlying provider's Shutdown is itself idempotent).
func ExitWithFlush(code int) {
	if v := activeRootSpan.Load(); v != nil {
		if span, ok := v.(trace.Span); ok && span != nil {
			span.End()
		}
	}
	if v := activeTraceShutdown.Load(); v != nil {
		if fn, ok := v.(func(context.Context) error); ok && fn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = fn(ctx)
			cancel()
		}
	}
	os.Exit(code)
}
