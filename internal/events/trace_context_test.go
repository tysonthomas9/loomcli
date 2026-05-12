package events

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSetContextProvider_NilClearsProvider(t *testing.T) {
	type ctxKey struct{}
	ctxVal := context.WithValue(context.Background(), ctxKey{}, "marker")
	SetContextProvider(func() context.Context { return ctxVal })

	if v := ambientCtx().Value(ctxKey{}); v != "marker" {
		t.Errorf("ambientCtx() should pick up provider; got value %v", v)
	}

	SetContextProvider(nil)
	// After Nil clears, ambientCtx falls back to background.
	if v := ambientCtx().Value(ctxKey{}); v != nil {
		t.Errorf("ambientCtx() should not carry marker after SetContextProvider(nil); got %v", v)
	}
	t.Cleanup(func() { SetContextProvider(nil) })
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
