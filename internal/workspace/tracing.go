package workspace

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
const tracerName = "github.com/tysonthomas9/loomcli/internal/workspace"

// startSpan starts an internal-kind span for a workspace-package operation.
// Naming follows the trace contract §3: "service.Workspace.<Method>".
// The contract enforces this shape via a cardinality lint
// (internal/observability/tracing/cardinality_test.go), which is why span
// names live as string literals at call sites rather than being derived
// from runtime input.
//
// Returning the span lets callers stamp result.count / cache.hit / per-op
// attrs before End. The helper here is intentionally minimal — call-site
// attrs (loom.workspace, loom.repo, …) are passed via the variadic.
//
// Note: as of this commit the package surface is two pure-string helpers
// (ShortWorkspaceID, ResolveWorkspaceID) that touch only in-memory state.
// Per the trace contract §6 and the instrumentation guide ("skip
// trivially-fast pure getters"), neither is wrapped in a span. This helper
// is in place so future I/O-bearing methods on this package's main type(s)
// can be instrumented without adding a new dependency edge.
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
// the not-found case so the span's status stays unset.
//
// Cancellation (context.Canceled) is also a non-error per the contract;
// callers should pass nil in that case too.
func recordErr(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "error")
}

// Static-use guards: these silence "declared and not used" diagnostics
// while the package surface has nothing to instrument. Remove them in the
// same PR that adds the first wrapped method.
var (
	_ = startSpan
	_ = recordErr
)

// Common attribute keys (matched to the trace contract §4.2). Declared as
// package-level vars so the strings are colocated with the helper that
// uses them — no other producer should be inventing parallel keys.
var (
	attrLoomWorkspace = func(v string) attribute.KeyValue { return attribute.String("loom.workspace", v) }
	attrLoomRepo      = func(v string) attribute.KeyValue { return attribute.String("loom.repo", v) }
	attrResultCount   = func(v int) attribute.KeyValue { return attribute.Int("result.count", v) }
)

var (
	_ = attrLoomWorkspace
	_ = attrLoomRepo
	_ = attrResultCount
)
