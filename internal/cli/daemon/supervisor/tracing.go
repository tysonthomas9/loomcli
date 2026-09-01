package supervisor

import (
	"context"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"

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

// recordSpawnFailure is the single exit for a failed spawn: it records the
// error on the span with the class's bounded reason string, and counts the
// failure against the role. Every failure branch in spawnAgent goes through
// here, so a new branch cannot be tagged on the span but missed by the metric.
//
// role is a parameter rather than something read off the AgentProcess: the
// call sites straddle ap.Mu (two before it is taken, one just after it is
// released), so reaching for the lock in here would deadlock the supervisor.
//
// No-op on a nil err, matching recordErr, so it can be called unconditionally.
func (s *Supervisor) recordSpawnFailure(span trace.Span, err error, role string, c spawnmetrics.Class) {
	if err == nil {
		return
	}
	recordErr(span, err, c.SpanReason())
	s.SpawnMetrics.RecordFailure(role, c)
}

// errorTypeFromAgentErr maps an *agenterr.AgentError to the low-cardinality
// loom.error_type enum used by the trace contract §4.1. Returns "unknown"
// when the agent error is nil so the attribute is always present and bounded.
func errorTypeFromAgentErr(e *agenterr.AgentError) string {
	if e == nil {
		return "unknown"
	}
	switch {
	case e.Class.IsClass(wrapper.ErrTimeout):
		return "timeout"
	case e.Class.IsClass(wrapper.ErrRateLimited):
		return "rate_limited"
	case e.Class.Is(agenterr.NoWorkOutcome):
		return "no_work"
	case e.Class.IsClass(wrapper.ErrUnknown):
		return "unknown"
	default:
		// agenterr.Outcome.String() values are bounded (canonical wire names),
		// so this stays low-cardinality even for classes we don't have
		// explicit cases for here.
		return e.Class.String()
	}
}
