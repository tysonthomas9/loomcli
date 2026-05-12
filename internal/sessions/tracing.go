package sessions

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation library name reported on every span this
// package emits. Must match the package import path so dashboards filtering
// by tracer remain stable across loom binaries.
const tracerName = "github.com/tysonthomas9/loomcli/internal/sessions"

// startSpan starts an internal-kind span for a sessions-package operation.
// Naming follows the trace contract §3: "service.Sessions.<Method>". The
// contract enforces this shape via a cardinality lint
// (internal/observability/tracing/cardinality_test.go).
//
// Returning the span lets callers stamp result.count / per-op attrs before
// End. The helper is intentionally minimal — call-site attrs (loom.agent,
// loom.session_id, loom.task_id) are passed via the variadic.
//
// PII rule: never put prompt text, transcript content, agent stdout, or
// any other free-form user-supplied string on a span. The §6 redaction
// policy lists session_id, agent, task_id, backend, and the §4.4 result
// attrs as safe; everything else needs review.
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.GetTracerProvider().Tracer(tracerName).Start(
		ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// recordErr maps an error to the span and applies the contract's
// low-cardinality status convention (codes.Error, "error"). Callers that
// distinguish a "not found" from a real error should pass nil here for
// the not-found case so the span's status stays unset. Cancellation
// (context.Canceled) is also a non-error per the contract; callers
// should pass nil in that case too.
func recordErr(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "error")
}

// Common attribute keys (matched to the trace contract §4.2). Declared as
// package-level helpers so the strings are colocated with the helper that
// uses them — no other producer should be inventing parallel keys.
//
//	loom.agent       — agent name (e.g. "falcon")
//	loom.session_id  — session UUID/short id
//	loom.task_id     — task issue ID, when known
//	loom.backend     — AI backend (claude / codex / opencode)
func attrLoomAgent(v string) attribute.KeyValue     { return attribute.String("loom.agent", v) }
func attrLoomSessionID(v string) attribute.KeyValue { return attribute.String("loom.session_id", v) }
func attrLoomTaskID(v string) attribute.KeyValue    { return attribute.String("loom.task_id", v) }
func attrLoomBackend(v string) attribute.KeyValue   { return attribute.String("loom.backend", v) }
func attrResultCount(v int) attribute.KeyValue      { return attribute.Int("result.count", v) }
