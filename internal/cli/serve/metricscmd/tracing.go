package metricscmd

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation library name reported on every span this
// package emits. Stable so dashboards filtering by it don't break.
const tracerName = "github.com/tysonthomas9/loomcli/internal/cli/serve/metricscmd"

// startSpan starts a service-layer span at the entry of a monitor HTTP
// handler. Naming follows the trace contract §3: "service.Monitor.<Method>".
//
// These spans wrap the orchestration done by /api/monitor/{status,agents,...}
// handlers, which fan out to fleet-db calls and store reads. The handler-level
// HTTP span (from otelhttp middleware) reflects the inbound HTTP shape;
// startSpan adds the per-request business-logic name between it and the
// downstream HTTP-client / store spans.
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.GetTracerProvider().Tracer(tracerName).Start(
		ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// recordErr maps an error to the span and applies the contract's
// low-cardinality status convention. nil is a no-op.
func recordErr(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "error")
}
