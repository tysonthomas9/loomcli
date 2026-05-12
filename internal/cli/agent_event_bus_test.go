package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	// Reset + cleanup the bus singleton so this test gets a fresh init
	// against LOOM_EVENTS_DIR below and doesn't leak buffered writes
	// into subsequent tests' assertions.
	cli.TestingResetAgentEventBus()
	t.Cleanup(cli.TestingResetAgentEventBus)

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
	// Close the bus so the JSONL writer's bufio.Writer flushes to disk
	// before we read it. TestingResetAgentEventBus is idempotent; the
	// cleanup registered at the top still runs at end-of-test.
	cli.TestingResetAgentEventBus()

	// Verify the JSONL writer also got the events with the right shape.
	// Weak "directory not empty" check was insufficient — read each record
	// and assert (a) both events were written, (b) the right task_id flows
	// through, (c) the traceparent header is populated and shares the
	// parent's trace ID. This is the durable audit log; consumers
	// downstream depend on the structure.
	gotTypes, gotTaskIDs, gotTPs := readEventJSONL(t, tmp)
	if len(gotTypes) != 2 {
		t.Fatalf("expected 2 events in JSONL, got %d (types: %v)", len(gotTypes), gotTypes)
	}
	if gotTypes[0] != string(events.TaskClaimed) || gotTypes[1] != string(events.TaskCompleted) {
		t.Errorf("event types = %v, want [%s %s]", gotTypes, events.TaskClaimed, events.TaskCompleted)
	}
	for i, id := range gotTaskIDs {
		if id != "HAPPY-2" {
			t.Errorf("event[%d] task_id = %q, want HAPPY-2", i, id)
		}
	}
	wantTraceID := parentSpan.SpanContext().TraceID().String()
	for i, tp := range gotTPs {
		if tp == "" {
			t.Errorf("event[%d] traceparent is empty (ambient context provider not capturing?)", i)
			continue
		}
		// W3C traceparent format: "00-<trace-id>-<span-id>-<flags>".
		// The trace_id sub-field must match the active span's trace_id.
		if len(tp) < 3+32 || tp[3:3+32] != wantTraceID {
			t.Errorf("event[%d] traceparent = %q, want trace_id %s", i, tp, wantTraceID)
		}
	}
}

// readEventJSONL reads every Event record from the JSONL files under dir and
// projects the fields the active-context test asserts on: type, task_id
// (from Data), and traceparent.
func readEventJSONL(t *testing.T, dir string) (types, taskIDs, traceparents []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read events dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name(), err)
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			var ev events.Event
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				t.Fatalf("decode JSONL line in %s: %v", entry.Name(), err)
			}
			types = append(types, string(ev.Type))
			traceparents = append(traceparents, ev.TraceParent)
			var data struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(ev.Data, &data)
			taskIDs = append(taskIDs, data.TaskID)
		}
		_ = f.Close()
	}
	return types, taskIDs, traceparents
}

// TestAgentEventBus_MkdirFails_ReturnsNil pins the degraded-mode behavior
// guaranteed by the public AgentEventBus contract: when the events
// directory can't be created, the singleton must return nil rather than
// surface a half-initialized bus. The init path emits a slog.Warn
// ("agent-events: mkdir failed") in this case; that warning was changed
// from log.Printf during the OTel cleanup and previously had no test
// coverage.
func TestAgentEventBus_MkdirFails_ReturnsNil(t *testing.T) {
	cli.TestingResetAgentEventBus()
	t.Cleanup(cli.TestingResetAgentEventBus)

	// Construct an unwritable LOOM_EVENTS_DIR: a path whose parent is a
	// regular file. MkdirAll on a child of a non-directory must fail.
	parent := filepath.Join(t.TempDir(), "blocking-file")
	if err := os.WriteFile(parent, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(parent, "events"))

	if bus := cli.AgentEventBus(); bus != nil {
		t.Errorf("AgentEventBus() = %v, want nil when events dir is unwritable", bus)
	}
}
