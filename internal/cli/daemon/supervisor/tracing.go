package supervisor

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// supervisorTracerName is the instrumentation library name reported on
// supervisor lifecycle spans (spawn / restart / stop). Stable so dashboards
// filtering on it don't break.
const supervisorTracerName = "github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"

// startSpan opens a supervisor lifecycle span. The caller is responsible for
// calling span.End() (typically via defer). The returned ctx carries the new
// span as parent so any descendant operations inherit it.
//
// When ctx is nil this falls back to cmdstore.RootContext() so spans started
// from background goroutines (like superviseAgent) still parent under the
// daemon's root span. Per the trace contract §5 the daemon installs a root
// span at startup; supervisor lifecycle spans nest under it via the root ctx.
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = cmdstore.RootContext()
	}
	tracer := otel.GetTracerProvider().Tracer(supervisorTracerName)
	return tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// recordErr maps an error onto the span and applies the contract's
// low-cardinality status convention (a short reason category, not the raw
// error message). No-op when err is nil so callers can defer it
// unconditionally without re-checking.
func recordErr(span trace.Span, err error, reason string) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, reason)
}

// errorTypeFromAgentErr maps an *agenterr.AgentError to the low-cardinality
// loom.error_type enum used by the trace contract §4.1. Returns "unknown"
// when the agent error is nil so the attribute is always present and bounded.
func errorTypeFromAgentErr(e *agenterr.AgentError) string {
	if e == nil {
		return "unknown"
	}
	switch e.Class {
	case agenterr.Timeout:
		return "timeout"
	case agenterr.RateLimited:
		return "rate_limited"
	case agenterr.NoWork:
		return "no_work"
	case agenterr.Unknown:
		return "unknown"
	default:
		// agenterr.ErrorClass.String() values are bounded by the agenterr
		// package, so this stays low-cardinality even for classes we don't
		// have explicit cases for here.
		return e.Class.String()
	}
}
