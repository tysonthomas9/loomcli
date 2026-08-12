package automode

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// automodeTracerName is the instrumentation library name for spans emitted
// from this package. Stable so dashboards filtering on it don't break.
const automodeTracerName = "github.com/tysonthomas9/loomcli/internal/cli/automode"

// startPollSpan opens a span for one cycle of the automode ready-issue
// poller. parentID and repoLabel scope the cycle and are recorded as
// low-cardinality attrs (parent_id is bounded by epic count, repo_label by
// configured repo set).
//
// Caller is responsible for calling span.End(); typically done via defer.
// Returns the new ctx so descendant operations (e.g., the Work Items
// Ready call) inherit the span as parent.
func startPollSpan(ctx context.Context, parentID, repoLabel string) (context.Context, trace.Span) {
	tracer := otel.GetTracerProvider().Tracer(automodeTracerName)
	attrs := []attribute.KeyValue{}
	if parentID != "" {
		attrs = append(attrs, attribute.String("automode.parent_id", parentID))
	}
	if repoLabel != "" {
		attrs = append(attrs, attribute.String("automode.repo_label", repoLabel))
	}
	return tracer.Start(ctx, "automode.poll.cycle",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// recordPollErr maps an error to the span and applies the contract's
// low-cardinality status convention. No-op for nil errors.
func recordPollErr(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "automode.poll.failed")
}
