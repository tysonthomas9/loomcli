package otelexport_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"
)

// TestEventSpanParentedToActiveSpan verifies that when an event is emitted
// via EmitCtx with an active span in context, the event-driven span
// (loom.task) reconstructed by otelexport is a child of that active span:
// they share the same trace_id and the loom.task parent_span_id matches
// the active span's span_id.
//
// Regresses the gap called out in docs/observability/events-tracing-spike.md:
// before this wiring, every loom.task span landed in its own trace,
// disconnected from the request that triggered it.
func TestEventSpanParentedToActiveSpan(t *testing.T) {
	// In-memory exporter so we can assert on emitted spans directly.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Wire the otelexport on top of this provider.
	exporter, err := otelexport.New(otelexport.Config{
		Endpoint:    "http://unused", // forces TracesEnabled
		ServiceName: "test",
	}, otelexport.WithTracerProvider(tp))
	if err != nil {
		t.Fatalf("otelexport.New: %v", err)
	}

	// Start an "originating request" span — this is the parent we expect
	// the event-derived span to attach to.
	ctx, parentSpan := tp.Tracer("test").Start(context.Background(), "incoming.request")
	parentSpanID := parentSpan.SpanContext().SpanID()
	traceID := parentSpan.SpanContext().TraceID()

	// Build an event and capture trace context into it (this is what
	// Bus.EmitCtx does at emit sites).
	ev, err := events.NewEvent(
		events.TaskClaimed, "agent-falcon", "reviewer", "EPIC-1",
		events.TaskClaimedData{TaskID: "TASK-42", Title: "do the thing"},
	)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	ev.InjectTraceContext(ctx)

	if ev.TraceParent == "" {
		t.Fatalf("InjectTraceContext did not capture traceparent (active span missing?)")
	}

	// Now drive the otelexport handler as the bus would. This emits the
	// loom.task span; the next event (TaskCompleted) ends it.
	exporter.HandleEvent(ev)

	doneEv, err := events.NewEvent(
		events.TaskCompleted, "agent-falcon", "reviewer", "EPIC-1",
		events.TaskCompletedData{
			TaskID:   "TASK-42",
			Duration: events.Duration{Duration: 0},
		},
	)
	if err != nil {
		t.Fatalf("NewEvent done: %v", err)
	}
	doneEv.InjectTraceContext(ctx)
	exporter.HandleEvent(doneEv)

	parentSpan.End()

	// Force flush before asserting.
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	// Find the loom.task span among the emitted spans.
	got := exp.GetSpans()
	var taskSpan *tracetest.SpanStub
	for i := range got {
		if got[i].Name == "loom.task" {
			s := got[i]
			taskSpan = &s
			break
		}
	}
	if taskSpan == nil {
		names := make([]string, 0, len(got))
		for _, s := range got {
			names = append(names, s.Name)
		}
		t.Fatalf("loom.task span not emitted; got %v", names)
	}

	// Same trace?
	if taskSpan.SpanContext.TraceID() != traceID {
		t.Errorf("loom.task trace_id = %s, want %s",
			taskSpan.SpanContext.TraceID(), traceID)
	}
	// Parented to the active request span?
	if taskSpan.Parent.SpanID() != parentSpanID {
		t.Errorf("loom.task parent span_id = %s, want %s (parent of incoming.request)",
			taskSpan.Parent.SpanID(), parentSpanID)
	}
}
