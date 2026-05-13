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

// TestAmbientCtx_DoesNotPanic pins the safety guarantee questioned in
// the OTel PR review (P1 finding on trace_context.go:32): the bot
// claimed atomic.Value.Load before any Store panics. Per Go's
// documented contract, Load returns nil cleanly when nothing has been
// stored — and ambientCtx() explicitly handles that nil branch with a
// context.Background fallback. This test exercises the public surface
// (ambientCtx via the same code path Bus.Emit hits) and asserts it
// never panics, regardless of whether SetContextProvider has been
// invoked before.
//
// The bot's underlying concern — "Bus.Emit before SetContextProvider
// crashes the process" — is therefore not reproducible; if the test
// ever does fire, it would mean ambientCtx's nil-guard branch was
// dropped.
func TestAmbientCtx_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ambientCtx panicked: %v", r)
		}
	}()
	// Call multiple times in mixed states.
	if got := ambientCtx(); got == nil {
		t.Error("ambientCtx returned nil context")
	}
	SetContextProvider(nil)
	t.Cleanup(func() { SetContextProvider(nil) })
	if got := ambientCtx(); got == nil {
		t.Error("ambientCtx returned nil context after SetContextProvider(nil)")
	}
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
