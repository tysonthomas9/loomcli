package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// This file covers the IN-PROCESS leg of the daemon's trace tree: control-plane
// HTTP calls made during spawnAgent must record as children of the
// daemon.supervisor.spawn span. The SUBPROCESS leg (LOOM_TRACE_PARENT in the
// agent's env) is covered by trace_propagate_test.go — a different mechanism
// with a different failure mode; keep the two apart.
//
// PUPPET-241: spawnAgent used to discard the context startSpan returns
// (`_, span := startSpan(...)`), so every fleet-db call underneath it started
// from the daemon ROOT context and landed as a SIBLING of the spawn span. In
// Jaeger that renders as a flat fan: with several agents spawning at once, a
// 404 cannot be attributed to the agent whose spawn caused it.
//
// Cost when tracing is disabled (measured on darwin/arm64, go1.26, default
// no-op TracerProvider, testing.AllocsPerRun over 2000 iterations):
// context.WithTimeout(ctx, …) allocates 4/op; wrapping it in
// context.WithoutCancel makes it 5/op. One extra allocation per
// control-plane call, on a per-spawn path — nothing in a hot loop.

// traceParentAgentStore issues a real otelhttp-instrumented request per
// Agents().Update, so the client span the assertions look for is produced by
// the same instrumentation the production path uses (internal/backend/fleet's
// shared transport wraps otelhttp exactly this way). Only Update is exercised;
// the embedded interface panics on anything else, which is the intent.
type traceParentAgentStore struct {
	store.AgentStore
	baseURL   string
	client    *http.Client
	workspace string
}

func (a *traceParentAgentStore) Update(ctx context.Context, workspaceKey, name string, _ store.AgentUpdate) (*domain.Agent, error) {
	url := fmt.Sprintf("%s/api/v1/%s/agents/%s", a.baseURL, workspaceKey, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil, fmt.Errorf("agent update: status %d", resp.StatusCode)
}

// traceParentStoreOverride swaps only the agent store; the rest is the shared
// memstore and goes unused here (same pattern as ownershipStoreOverride).
type traceParentStoreOverride struct {
	*memstore.Store
	agents store.AgentStore
}

func (s *traceParentStoreOverride) Agents() store.AgentStore { return s.agents }

// newTraceParentExporter installs an in-memory SDK tracer provider and the W3C
// propagator for the duration of the test, restoring the globals afterwards.
func newTraceParentExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
	)
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return exporter
}

// newTraceParentFixture stands up a supervisor whose backend gate always fails,
// so spawnAgent takes exactly one control-plane write (the
// AgentStateBackendUnavailable update) and then returns. The httptest server
// answers 404 — the ticket's own observed failure — because the parentage of
// the span is what matters, not the response.
func newTraceParentFixture(t *testing.T) (*Supervisor, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := newTraceParentExporter(t)

	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: false, InstallHint: "not on PATH"}, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	agents := &traceParentAgentStore{
		baseURL:   srv.URL,
		client:    &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		workspace: "WS",
	}
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: "codex"}
		},
		ProjectDir:    t.TempDir(),
		WorkspaceID:   "WS",
		ControlStore:  &traceParentStoreOverride{Store: memstore.New(), agents: agents},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
	return s, exporter
}

func newTraceParentAgent(t *testing.T, name string) *AgentProcess {
	t.Helper()
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: name, Role: "plan", Backend: "codex"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "test"},
		WorktreePath: t.TempDir(),
		StopReason:   StopReasonBackendUnavailable, // force the recovery-path write too
	}
}

// collectTraceParentSpans splits the exporter's output into spawn spans and
// HTTP client spans, keyed the way the assertions need them.
func collectTraceParentSpans(t *testing.T, exporter *tracetest.InMemoryExporter) (spawns, clients []tracetest.SpanStub) {
	t.Helper()
	for _, sp := range exporter.GetSpans() {
		switch {
		case sp.Name == "daemon.supervisor.spawn":
			spawns = append(spawns, sp)
		case sp.SpanKind.String() == "client":
			clients = append(clients, sp)
		}
	}
	return spawns, clients
}

// TestSpawnAgent_ControlPlaneSpansParentUnderSpawn is the PUPPET-241
// acceptance criterion: the fleet-db call made inside spawnAgent must have the
// spawn span as its PARENT, not merely share its trace. Asserting on trace id
// alone would be vacuous — the buggy code already produced matching trace ids.
func TestSpawnAgent_ControlPlaneSpansParentUnderSpawn(t *testing.T) {
	s, exporter := newTraceParentFixture(t)
	ap := newTraceParentAgent(t, "falcon")

	if err := s.spawnAgent(ap); err == nil {
		t.Fatal("spawnAgent: expected the backend gate to fail")
	}

	spawns, clients := collectTraceParentSpans(t, exporter)
	if len(spawns) != 1 {
		t.Fatalf("daemon.supervisor.spawn spans = %d, want 1", len(spawns))
	}
	if len(clients) == 0 {
		t.Fatal("no HTTP client span recorded; the control-plane write never happened")
	}
	spawnSC := spawns[0].SpanContext
	for _, c := range clients {
		if c.Parent.SpanID() != spawnSC.SpanID() {
			t.Errorf("client span %q parent span_id = %s, want the spawn span %s",
				c.Name, c.Parent.SpanID(), spawnSC.SpanID())
		}
		if c.Parent.TraceID() != spawnSC.TraceID() {
			t.Errorf("client span %q trace_id = %s, want %s",
				c.Name, c.Parent.TraceID(), spawnSC.TraceID())
		}
	}
}

// TestSpawnAgent_ConcurrentSpawnsKeepDisjointTraces is the operator-visible
// property the ticket is about: with two agents spawning at once, each one's
// control-plane calls hang under ITS OWN spawn span, so a 404 under one agent
// is never attributable to the other. Correlated through the URL path, which
// carries the agent name.
func TestSpawnAgent_ConcurrentSpawnsKeepDisjointTraces(t *testing.T) {
	s, exporter := newTraceParentFixture(t)

	var wg sync.WaitGroup
	for _, name := range []string{"agent-a", "agent-b"} {
		ap := newTraceParentAgent(t, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.spawnAgent(ap)
		}()
	}
	wg.Wait()

	spawns, clients := collectTraceParentSpans(t, exporter)
	if len(spawns) != 2 {
		t.Fatalf("daemon.supervisor.spawn spans = %d, want 2", len(spawns))
	}
	if spawns[0].SpanContext.SpanID() == spawns[1].SpanContext.SpanID() {
		t.Fatal("the two spawns share a span id")
	}
	if len(clients) == 0 {
		t.Fatal("no HTTP client spans recorded")
	}

	// Map spawn span id -> the agent it spawned, then require every client
	// span's parent to be a known spawn whose agent matches the URL it hit.
	agentBySpawn := map[string]string{}
	for _, sp := range spawns {
		for _, attr := range sp.Attributes {
			if attr.Key == "loom.agent" {
				agentBySpawn[sp.SpanContext.SpanID().String()] = attr.Value.AsString()
			}
		}
	}
	for _, c := range clients {
		agent, ok := agentBySpawn[c.Parent.SpanID().String()]
		if !ok {
			t.Errorf("client span %q parents under %s, which is not either spawn span",
				c.Name, c.Parent.SpanID())
			continue
		}
		var path string
		for _, attr := range c.Attributes {
			if attr.Key == "url.path" || attr.Key == "http.url" || attr.Key == "url.full" {
				path = attr.Value.AsString()
			}
		}
		if path != "" && !containsAgent(path, agent) {
			t.Errorf("client span for %q parents under the spawn of %q", path, agent)
		}
	}
}

func containsAgent(path, agent string) bool {
	return len(path) >= len(agent) && path[len(path)-len(agent):] == agent
}
