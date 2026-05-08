package tracing_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

// TestBootstrapContext_InheritsLoomTraceParent verifies the consumer side of
// the daemon→agent (and loom→embedded-fleet-db) trace-propagation chain. A
// parent loom process serializes its active span context into LOOM_TRACE_PARENT
// when spawning a child; the child reads it via BootstrapContext and starts
// its root span as a remote child, sharing the parent's trace_id.
//
// Pairs with the supervisor.buildCommand path that injects the env var.
func TestBootstrapContext_InheritsLoomTraceParent(t *testing.T) {
	// Install the W3C propagator globally; BootstrapContext relies on it.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Simulate the parent process: build a span, format its traceparent
	// the way the supervisor's traceparentFromRootContext does.
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "parent.span")
	defer parentSpan.End()

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(parentCtx, carrier)
	traceParent := carrier.Get("traceparent")
	if traceParent == "" {
		t.Fatalf("Inject produced no traceparent")
	}

	// Now simulate the child process: set the env var and call
	// BootstrapContext on a fresh root context.
	t.Setenv("LOOM_TRACE_PARENT", traceParent)

	childRoot := tracing.BootstrapContext(context.Background())
	childSC := trace.SpanContextFromContext(childRoot)

	if !childSC.IsValid() {
		t.Fatalf("child SpanContext is invalid; LOOM_TRACE_PARENT not consumed")
	}
	if childSC.TraceID() != parentSpan.SpanContext().TraceID() {
		t.Errorf("child trace_id = %s, want %s",
			childSC.TraceID(), parentSpan.SpanContext().TraceID())
	}
	// The child's parent should reference the original span_id (so when the
	// child starts its own span, it'll be parented under parentSpan).
	if childSC.SpanID() != parentSpan.SpanContext().SpanID() {
		t.Errorf("child remote-parent span_id = %s, want %s",
			childSC.SpanID(), parentSpan.SpanContext().SpanID())
	}

	// Now simulate the child starting its own root span (loom.cli.plan)
	// from BootstrapContext. The new span should be a child of parentSpan
	// in the same trace.
	_, childSpan := tp.Tracer("test").Start(childRoot, "loom.cli.plan")
	defer childSpan.End()

	if childSpan.SpanContext().TraceID() != parentSpan.SpanContext().TraceID() {
		t.Errorf("child loom.cli.plan trace_id = %s, want %s (parent's)",
			childSpan.SpanContext().TraceID(), parentSpan.SpanContext().TraceID())
	}
}

// TestBootstrapContext_NoEnvVar verifies the no-op path: when LOOM_TRACE_PARENT
// is not set, BootstrapContext returns the input ctx unchanged.
func TestBootstrapContext_NoEnvVar(t *testing.T) {
	t.Setenv("LOOM_TRACE_PARENT", "")
	in := context.Background()
	out := tracing.BootstrapContext(in)
	if !trace.SpanContextFromContext(out).Equal(trace.SpanContextFromContext(in)) {
		t.Error("BootstrapContext changed ctx despite empty LOOM_TRACE_PARENT")
	}
}

// TestBootstrapContext_MalformedEnvVar verifies graceful handling: a garbage
// LOOM_TRACE_PARENT does not panic, returns a ctx with no valid span.
func TestBootstrapContext_MalformedEnvVar(t *testing.T) {
	t.Setenv("LOOM_TRACE_PARENT", "not-a-valid-traceparent")
	out := tracing.BootstrapContext(context.Background())
	sc := trace.SpanContextFromContext(out)
	if sc.IsValid() {
		t.Errorf("malformed traceparent unexpectedly produced a valid span context: %v", sc)
	}
}
