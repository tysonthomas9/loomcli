package cli_test

// TestFullStackTrace_AgentRun_StructureAssertion is the structural
// regression gate for the in-process portion of loom's OpenTelemetry
// tracing pipeline.
//
// What this test covers
// ---------------------
// For a representative agent run (root CLI span → TaskClaimed → TaskCompleted),
// it asserts the resulting trace tree has the shape we promise to operators:
//
//   - At least one loom.task span exists in the exporter output.
//   - loom.task.parent_span_id == the simulated CLI root span_id (the
//     event-derived span must be parented to the active span instead of
//     fragmenting into its own trace; see
//     docs/observability/events-tracing-spike.md).
//   - loom.task.trace_id == root.trace_id.
//   - The same Bus.Emit path also writes JSONL events to disk (the durable
//     audit log).
//   - No emitted span name contains digits, hex IDs, or other obviously
//     high-cardinality content. This is the cheap in-process counterpart to
//     the dedicated cardinality lint test; it catches the easy mistakes
//     (someone naming a span "loom.task.42" or interpolating a UUID).
//
// In particular, this test fails if a future change re-introduces
// context.Background() in the hot agent emit path (the bus → otelexport
// hand-off): without an active span on context, the otelexport handler
// would create a root span instead of a child, and the parent_span_id
// assertion fires.
//
// What this test does NOT cover (intentionally)
// ---------------------------------------------
// These need a real backend and live in higher-tier E2E suites:
//
//   - HTTP server middleware spans (otelhttp on loom-serve / fleet-db).
//   - Redis command spans (require a real Redis).
//   - Postgres spans via otelpgx (require a real Postgres).
//   - Cross-process trace propagation through a subprocess boundary.
//   - End-to-end OTLP export to a collector.
//
// If you find yourself wanting to assert any of those, add the assertion to
// the live full-stack suite (Tier 4N / E2E verification), not here. This
// file is the fast (<1s, no network) in-process gate.
//
// Pattern note
// ------------
// We construct the events.Bus + otelexport.Exporter directly rather than
// going through cli.AgentEventBus() so test state is isolated. The
// AgentEventBus singleton uses sync.Once and binds to the global
// TracerProvider on first call, which makes it order-dependent across
// tests in the same package. The constructed bus exercises exactly the
// same wiring (events.NewBus + otelexport.New + bus.Subscribe(exp.HandleEvent))
// that initAgentEventBus does, so this test still regresses the same
// integration surface.

import (
	"context"
	"os"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"
)

// rootSpanName is what cli.Execute installs at process start (loom.cli.<verb>).
// Using "loom.cli.task" mirrors a `loom task` invocation — the most common
// agent entry point. Keep this stable; other assertions key off it.
const rootSpanName = "loom.cli.task"

// disallowedNameChars catches the most common cardinality offenders in span
// names: digits and the hex-id shapes (UUIDs, short IDs) that operators
// regularly accidentally interpolate. A real cardinality test (Tier 4N)
// should use a richer rule set, but this single check costs nothing here
// and catches the most common mistake.
var disallowedNameChars = regexp.MustCompile(`[0-9]|[0-9a-f]{8,}`)

func TestFullStackTrace_AgentRun_StructureAssertion(t *testing.T) {
	// 1. Stand up an in-memory exporter as the global TracerProvider.
	// Mirrors the agent_event_bus_test.go boilerplate so the rest of the
	// agent code (which reads otel.GetTracerProvider()) sees the same
	// provider this test is asserting against.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// 2. Wire SetContextProvider so events.Bus.Emit picks up the ambient
	// CLI root context (the trace context loom.cli.<verb> installs).
	// Without this, Emit captures context.Background() and the loom.task
	// span fragments into its own trace — exactly the regression we guard
	// against.
	events.SetContextProvider(cmdstore.RootContext)
	t.Cleanup(func() { events.SetContextProvider(nil) })

	// 3. Construct a fresh bus + otelexport pair pointed at the in-memory
	// TracerProvider. Mirrors initAgentEventBus's wiring without touching
	// the cli.AgentEventBus() singleton (which is order-dependent across
	// tests). Disable metrics — this test asserts trace tree shape only.
	tmp := t.TempDir()
	t.Setenv("LOOM_EVENTS_DIR", tmp)

	bus := events.NewBus(tmp)
	t.Cleanup(func() { _ = bus.Close() })

	metricsOff := false
	exporter, err := otelexport.New(otelexport.Config{
		Endpoint:    "http://unused", // forces TracesEnabled
		ServiceName: "loom-agent-test",
		Metrics:     &metricsOff,
	}, otelexport.WithTracerProvider(tp))
	if err != nil {
		t.Fatalf("otelexport.New: %v", err)
	}
	bus.Subscribe(exporter.HandleEvent)

	// 4. Start the simulated CLI root span (what cli.Execute does) and
	// publish it as the root context so events.SetContextProvider's
	// cmdstore.RootContext lookup finds it.
	rootCtx, rootSpan := tp.Tracer("test").Start(context.Background(), rootSpanName)
	cmdstore.SetRootContext(rootCtx)
	t.Cleanup(func() { cmdstore.SetRootContext(context.Background()) })

	// 5. Drive the representative agent flow: claim → complete.
	// This is the same shape as the agent runtime's task loop.
	const (
		agentName = "structure-test-agent"
		roleName  = "task"
		epicID    = "EPIC-STRUCT"
		taskID    = "TASK-STRUCT-A"
	)
	claimEv, err := events.NewEvent(events.TaskClaimed, agentName, roleName, epicID,
		events.TaskClaimedData{TaskID: taskID, Title: "Trace structure smoke task"})
	if err != nil {
		t.Fatalf("NewEvent claim: %v", err)
	}
	if err := bus.Emit(claimEv); err != nil {
		t.Fatalf("bus.Emit claim: %v", err)
	}

	completeEv, err := events.NewEvent(events.TaskCompleted, agentName, roleName, epicID,
		events.TaskCompletedData{TaskID: taskID, Duration: events.Duration{Duration: 0}})
	if err != nil {
		t.Fatalf("NewEvent complete: %v", err)
	}
	if err := bus.Emit(completeEv); err != nil {
		t.Fatalf("bus.Emit complete: %v", err)
	}

	rootSpan.End()
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	// 6. Pull spans and assert structure.
	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatalf("expected spans in exporter, got none")
	}

	// Index spans by name for clearer assertion messages. We take the first
	// match for each name; this flow only emits one of each.
	byName := make(map[string]tracetest.SpanStub, len(spans))
	allNames := make([]string, 0, len(spans))
	for _, s := range spans {
		allNames = append(allNames, s.Name)
		if _, seen := byName[s.Name]; !seen {
			byName[s.Name] = s
		}
	}

	// 6a. Root CLI span is present.
	gotRoot, ok := byName[rootSpanName]
	if !ok {
		t.Fatalf("expected root span %q in trace, got %v", rootSpanName, allNames)
	}

	// 6b. loom.task span is present and structurally correct.
	gotTask, ok := byName["loom.task"]
	if !ok {
		t.Fatalf("expected loom.task span in trace, got %v", allNames)
	}
	if gotTask.SpanContext.TraceID() != gotRoot.SpanContext.TraceID() {
		t.Errorf("loom.task trace_id = %s, want %s (same as %s)",
			gotTask.SpanContext.TraceID(), gotRoot.SpanContext.TraceID(), rootSpanName)
	}
	if gotTask.Parent.SpanID() != gotRoot.SpanContext.SpanID() {
		t.Errorf("loom.task parent_span_id = %s, want %s (regression: events emitted with context.Background()?)",
			gotTask.Parent.SpanID(), gotRoot.SpanContext.SpanID())
	}
	if !gotTask.Parent.IsValid() {
		t.Errorf("loom.task has no valid parent span context — likely fragmented into its own trace")
	}

	// 6c. Cheap cardinality screen: no emitted span name should look like
	// it contains a digit, hash, or hex id. This is intentionally aggressive
	// — every legitimate span name we emit today is lower-case dotted
	// identifiers (loom.task, loom.cli.task, loom.agent.lifecycle, etc.).
	// If you're adding a span name with a number in it, prefer attributes.
	for _, name := range allNames {
		if disallowedNameChars.MatchString(name) {
			t.Errorf("span name %q contains digits or hex run; use attributes for variable values", name)
		}
	}

	// 6d. JSONL events were also written to the on-disk audit log. The bus
	// path goes through both the otelexport handler and the JSONL writer;
	// if either side regresses we want to know.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read events dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected JSONL events under %s, got nothing (bus.NewBus dir wiring broken?)", tmp)
	}
}
