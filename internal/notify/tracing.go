package notify

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// notifyTracerName is the instrumentation library name reported on notify
// fan-out and (when wired) webhook spans. Stable so dashboards filtering on
// it don't break.
const notifyTracerName = "github.com/tysonthomas9/loomcli/internal/notify"

// startSpan opens a notify span. Caller is responsible for span.End() (typically via defer).
//
// The bus's Publish path does not take a context (it has no I/O and the
// in-process subscribers each manage their own contexts), so we always start
// from context.Background() — there is no parent ctx to inherit. If a
// future caller has a request-scoped ctx, prefer to pass it through; the
// signature accepts a ctx so that path is open without a breaking change.
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	tracer := otel.GetTracerProvider().Tracer(notifyTracerName)
	return tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// recordErr maps an error to the span and applies the contract's
// low-cardinality status convention. No-op on nil.
func recordErr(span trace.Span, err error, reason string) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, reason)
}
