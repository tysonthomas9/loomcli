package supervisor

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// TestBuildCommand_InjectsLoomTraceParent verifies the producer side of the
// daemon→agent trace-propagation chain. When the daemon has an active root
// span (published via cmdstore.SetRootContext), supervisor.buildCommand
// must add LOOM_TRACE_PARENT=<W3C traceparent> to the spawned agent's env,
// referencing the daemon's span_id. The consumer side (BootstrapContext in
// the agent process) is covered by tracing.TestBootstrapContext_*.
//
// Pair this test with the consumer-side tests for full chain coverage.
func TestBuildCommand_InjectsLoomTraceParent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Simulate: daemon process has a live "loom.cli.daemon" span.
	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "loom.cli.daemon")
	defer parentSpan.End()
	cmdstore.SetRootContext(parentCtx)
	t.Cleanup(func() { cmdstore.SetRootContext(context.Background()) })

	// Stand up a minimal Supervisor (mirrors lifecycle_test.go patterns).
	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	// Find LOOM_TRACE_PARENT in cmd.Env.
	var got string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_TRACE_PARENT=") {
			got = strings.TrimPrefix(env, "LOOM_TRACE_PARENT=")
			break
		}
	}
	if got == "" {
		t.Fatalf("LOOM_TRACE_PARENT not in cmd.Env\nenv was: %v", cmd.Env)
	}

	// W3C traceparent format: 00-<32 hex>-<16 hex>-<2 hex>
	parts := strings.Split(got, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		t.Errorf("LOOM_TRACE_PARENT %q is not valid W3C format", got)
	}

	// trace_id portion must match the parent span's trace_id.
	wantTraceID := parentSpan.SpanContext().TraceID().String()
	if parts[1] != wantTraceID {
		t.Errorf("trace_id in LOOM_TRACE_PARENT = %s, want %s", parts[1], wantTraceID)
	}
	// span_id portion must match the parent span's span_id (so the agent
	// is parented under it, not under some unrelated span).
	wantSpanID := parentSpan.SpanContext().SpanID().String()
	if parts[2] != wantSpanID {
		t.Errorf("span_id in LOOM_TRACE_PARENT = %s, want %s", parts[2], wantSpanID)
	}
}

// TestBuildCommand_NoTraceParentWhenNoActiveSpan verifies the no-op path:
// when the daemon has no active span, no LOOM_TRACE_PARENT should be added
// (don't pollute the agent's env with an empty value).
func TestBuildCommand_NoTraceParentWhenNoActiveSpan(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	cmdstore.SetRootContext(context.Background()) // no span
	t.Cleanup(func() { cmdstore.SetRootContext(context.Background()) })

	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}

	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_TRACE_PARENT=") {
			t.Errorf("LOOM_TRACE_PARENT unexpectedly present without active span: %s", env)
		}
	}
}
