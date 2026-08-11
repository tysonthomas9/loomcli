package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/events/otelexport"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// agentBus is the process-wide event bus for one-shot agent runs (loom plan,
// loom task, loom agent, loom lead). Constructed lazily on first access so
// non-agent commands pay no overhead. Subscribed to the otelexport.Exporter
// (sharing the global TracerProvider) so loom.task and loom.agent.lifecycle
// spans become visible without requiring a daemon.
var (
	agentBusOnce sync.Once
	agentBus     *events.Bus
	agentBusErr  error
)

// AgentEventBus returns a process-wide events.Bus subscribed to the OTel
// exporter, so emitted task/agent events become spans under the active
// trace. Returns nil if construction fails (e.g., events directory cannot
// be created); callers should handle that as "no event sink".
//
// Safe to call concurrently. Idempotent.
func AgentEventBus(ctx context.Context) *events.Bus {
	agentBusOnce.Do(func() { initAgentEventBus(ctx) })
	return agentBus
}

func initAgentEventBus(ctx context.Context) {
	dir := os.Getenv("LOOM_EVENTS_DIR")
	if dir == "" {
		// Default events location alongside the loom data dir; mirrors the
		// daemon's default so post-run analysis tools find the JSONL.
		base := bootstrap.LoomDir()
		if base == "" {
			base = filepath.Join(os.TempDir(), "loom")
		}
		dir = filepath.Join(base, "events")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("agent-events: mkdir failed (events disabled)", "dir", dir, "err", err)
		agentBusErr = err
		return
	}

	bus := events.NewBus(ctx, dir)

	// Wire otelexport into the bus, sharing the global TracerProvider so
	// emitted spans land in the same exporter as everything else. We pass
	// WithTracerProvider so otelexport's New skips constructing its own
	// trace exporter — but it would still try to construct a metric exporter,
	// so we explicitly disable metrics (we don't need otelexport's metrics
	// here; trace emission is enough).
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		falseB := false
		exp, err := otelexport.New(otelexport.Config{
			Endpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
			ServiceName: "loom-agent",
			Metrics:     &falseB,
		}, otelexport.WithTracerProvider(tp))
		if err == nil {
			bus.Subscribe(exp.HandleEvent)
		} else {
			slog.Warn("agent-events: otelexport init failed (events still flow to JSONL)", "err", err)
		}
	}

	agentBus = bus
}

// CloseAgentEventBus flushes and closes the agent event bus if one was
// constructed. Safe to call from cli.Execute's defer chain.
func CloseAgentEventBus(_ context.Context) {
	if agentBus != nil {
		_ = agentBus.Close()
	}
}

// TestingResetAgentEventBus resets the singleton so the next AgentEventBus()
// call re-runs initAgentEventBus against the current LOOM_EVENTS_DIR. Closes
// any previously constructed bus to flush its writer. Test-only — production
// code should rely on sync.Once for one-time init.
//
// Tests that need a fresh bus per case (e.g., capturing JSONL output to a
// per-test tempdir) call this in t.Cleanup so the next test gets a clean
// initializer.
func TestingResetAgentEventBus() {
	if agentBus != nil {
		_ = agentBus.Close()
	}
	agentBus = nil
	agentBusErr = nil
	agentBusOnce = sync.Once{}
}
