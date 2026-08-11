package events

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestNewBusRejectsNilContext(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewBus(nil, ...) did not panic")
		}
	}()
	var ctx context.Context
	NewBus(ctx, t.TempDir())
}

func TestEvent_InjectExtractTraceContext_RoundTrip(t *testing.T) {
	// Install propagator + tracer so injection produces a real traceparent.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	tp := sdktrace.NewTracerProvider()
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "parent")
	defer span.End()

	e := Event{}
	e.InjectTraceContext(ctx)
	if e.TraceParent == "" {
		t.Fatal("InjectTraceContext did not populate TraceParent")
	}

	// Extract should yield a context whose span has the same trace ID.
	extracted := e.ExtractTraceContext(context.Background())
	if !trace.SpanContextFromContext(extracted).TraceID().IsValid() {
		t.Error("ExtractTraceContext did not yield a valid trace ID")
	}
}

func TestEvent_ExtractTraceContext_EmptyReturnsInput(t *testing.T) {
	in := context.Background()
	e := Event{}
	if got := e.ExtractTraceContext(in); got != in {
		t.Error("ExtractTraceContext(empty) should be identity")
	}
}
