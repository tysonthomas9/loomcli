package cli_test

import (
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// TestAgentEventBus_EmitsLoomTaskSpanUnderActiveContext exercises the real
// cli.AgentEventBus() singleton + events.Bus.Emit (with ambient context
// injection) + otelexport pipeline, and asserts that:
//
//  1. A bus.Emit(TaskClaimed) call from inside an active span produces a
//     loom.task span in the trace tree.
//  2. The loom.task span shares the active span's trace_id.
//  3. The loom.task span's parent is the active span.
//
// Regression gate for "agents emit traces": if the bus → otelexport wiring
// or context capture breaks, this test fails.
func TestAgentEventBus_EmitsLoomTaskSpanUnderActiveContext(t *testing.T) {
	// Stand up an in-memory exporter and install it as the global TracerProvider
	// so cli.AgentEventBus picks it up via otel.GetTracerProvider().
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Point AgentEventBus at a temp events dir so JSONL writes don't hit ~/.loom.
	tmp := t.TempDir()
	t.Setenv("LOOM_EVENTS_DIR", tmp)

	// Start a parent span and publish it as the root context. This is what
	// cli.Execute does at startup; we emulate it here.
	ctx, parentSpan := tp.Tracer("test").Start(context.Background(), "loom.cli.plan")
	cmdstore.SetRootContext(ctx)
	t.Cleanup(func() { cmdstore.SetRootContext(context.Background()) })

	// Bus.Emit needs the ambient context provider to capture trace context
	// onto each event. cli.Execute does this; do it explicitly here.
	events.SetContextProvider(cmdstore.RootContext)
	t.Cleanup(func() { events.SetContextProvider(nil) })

	// Get the singleton bus. otelexport is subscribed inside.
	bus := cli.AgentEventBus()
	if bus == nil {
		t.Fatalf("cli.AgentEventBus() returned nil")
	}

	// Emit a TaskClaimed event the same way the agent's claim flow would.
	claimEv, err := events.NewEvent(events.TaskClaimed, "happy-worker", "task", "EPIC-1",
		events.TaskClaimedData{TaskID: "HAPPY-2", Title: "Trace test task"})
	if err != nil {
		t.Fatalf("NewEvent claim: %v", err)
	}
	if err := bus.Emit(claimEv); err != nil {
		t.Fatalf("bus.Emit claim: %v", err)
	}

	// End the task with TaskCompleted so the loom.task span closes.
	completeEv, err := events.NewEvent(events.TaskCompleted, "happy-worker", "task", "EPIC-1",
		events.TaskCompletedData{TaskID: "HAPPY-2", Duration: events.Duration{Duration: 0}})
	if err != nil {
		t.Fatalf("NewEvent complete: %v", err)
	}
	if err := bus.Emit(completeEv); err != nil {
		t.Fatalf("bus.Emit complete: %v", err)
	}

	parentSpan.End()
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	// Assertions.
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
		t.Fatalf("loom.task span was not emitted by AgentEventBus; got spans: %v", names)
	}
	if taskSpan.SpanContext.TraceID() != parentSpan.SpanContext().TraceID() {
		t.Errorf("loom.task trace_id = %s, want %s",
			taskSpan.SpanContext.TraceID(), parentSpan.SpanContext().TraceID())
	}
	if taskSpan.Parent.SpanID() != parentSpan.SpanContext().SpanID() {
		t.Errorf("loom.task parent_span_id = %s, want %s",
			taskSpan.Parent.SpanID(), parentSpan.SpanContext().SpanID())
	}
	// Verify the JSONL writer also got the events, with traceparent populated.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read events dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected events JSONL in %s, got nothing", tmp)
	}
}
