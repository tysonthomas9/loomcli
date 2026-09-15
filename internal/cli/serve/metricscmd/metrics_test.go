package metricscmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// --- fixtures -------------------------------------------------------------
//
// Every fixture below is deliberately NON-ZERO and guarded by an assertion that
// it is. The bug this file exists for shipped because the gauges read a constant
// zero and no test fixture could tell that apart from a correct empty queue.

// metricsWorkspaceFixture describes one workspace's expected numbers.
type metricsWorkspaceFixture struct {
	key        string
	ready      map[int]int // priority -> count
	inProgress int
	agents     []*domain.Agent
	// collectorFails makes the monitor collection return nil for this workspace.
	collectorFails bool
}

func readyIssuesFor(fx metricsWorkspaceFixture) []backend.IssueData {
	var issues []backend.IssueData
	n := 0
	for p := 0; p <= 4; p++ {
		for i := 0; i < fx.ready[p]; i++ {
			n++
			issues = append(issues, backend.IssueData{
				ID:        fx.key + "-R" + string(rune('a'+n)),
				Status:    "open",
				Priority:  p,
				Design:    "plan",
				IssueType: "task",
			})
		}
	}
	return issues
}

func inProgressIssuesFor(fx metricsWorkspaceFixture) []backend.IssueData {
	issues := make([]backend.IssueData, 0, fx.inProgress)
	for i := 0; i < fx.inProgress; i++ {
		issues = append(issues, backend.IssueData{
			ID:        fx.key + "-P" + string(rune('a'+i)),
			Status:    "in_progress",
			IssueType: "task",
		})
	}
	return issues
}

// newMetricsHandler wires a real HandleMetrics over the fixtures.
func newMetricsHandler(t *testing.T, fixtures ...metricsWorkspaceFixture) http.HandlerFunc {
	t.Helper()

	byKey := make(map[string]metricsWorkspaceFixture, len(fixtures))
	workspaces := make([]*domain.Workspace, 0, len(fixtures))
	agentsByWorkspace := make(map[string][]*domain.Agent, len(fixtures))
	for _, fx := range fixtures {
		byKey[fx.key] = fx
		workspaces = append(workspaces, &domain.Workspace{Key: fx.key, Name: fx.key})
		agentsByWorkspace[fx.key] = fx.agents
	}

	st := newFakeMetricsStore(workspaces, agentsByWorkspace)

	backendFn := func(ctx context.Context) backend.IssueBackend {
		fx, ok := byKey[middleware.WorkspaceFromContext(ctx)]
		if !ok || fx.collectorFails {
			// A nil backend falls through to collectDataFn, which returns nil:
			// that is this test's "collection failed for this workspace".
			return nil
		}
		mock := clitest.NewMockIssueBackend()
		mock.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
			return readyIssuesFor(fx), nil
		}
		mock.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.Status == "in_progress" {
				return inProgressIssuesFor(fx), nil
			}
			return nil, nil
		}
		return mock
	}
	collectDataFn := func() *monitor.MonitorData { return nil }

	ds := NewMonitorDataSourceWithTTL(collectDataFn, backendFn, time.Millisecond)
	storeDS := NewMonitorStoreDataSourceWithTTL(st, time.Millisecond)
	return HandleMetrics(ds, storeDS, st)
}

// scrape runs the handler and returns the raw body plus the parsed families,
// failing the test if the body is not valid Prometheus text. The parse is the
// point: it is what catches a HELP/TYPE line repeated per workspace.
func scrape(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	rr := httptest.NewRecorder()
	handler(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; a scrape endpoint must never error", rr.Code)
	}
	body := rr.Body.String()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	if _, err := parser.TextToMetricFamilies(strings.NewReader(body)); err != nil {
		t.Fatalf("body is not valid Prometheus text: %v\n\n%s", err, body)
	}
	return body
}

func requireLines(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("missing sample line %q\n\nfull body:\n%s", line, body)
		}
	}
}

// --- tests ----------------------------------------------------------------

func TestHandleMetrics_PerWorkspaceSamples(t *testing.T) {
	wsA := metricsWorkspaceFixture{
		key:        "WS_A",
		ready:      map[int]int{0: 1, 2: 3},
		inProgress: 4,
		agents: []*domain.Agent{
			{WorkspaceKey: "WS_A", Name: "a-working", State: domain.AgentStateActive, LiveStatus: domain.AgentLiveWorking},
			{WorkspaceKey: "WS_A", Name: "a-idle", State: domain.AgentStateIdle},
			{WorkspaceKey: "WS_A", Name: "a-stopped", State: domain.AgentStateStopped},
		},
	}
	wsB := metricsWorkspaceFixture{
		key:        "WS_B",
		ready:      map[int]int{1: 2, 4: 5},
		inProgress: 7,
		agents: []*domain.Agent{
			{WorkspaceKey: "WS_B", Name: "b-blocked", State: domain.AgentStateBackendUnavailable},
			{WorkspaceKey: "WS_B", Name: "b-idle-1", State: domain.AgentStateIdle},
			{WorkspaceKey: "WS_B", Name: "b-idle-2", State: domain.AgentStateActive, LiveStatus: domain.AgentLiveIdle},
		},
	}

	// Guard: the fixtures must be non-zero and must differ, or this test cannot
	// distinguish "reads the store" from "reads a constant".
	for _, fx := range []metricsWorkspaceFixture{wsA, wsB} {
		if fx.inProgress == 0 || len(fx.ready) == 0 || len(fx.agents) == 0 {
			t.Fatalf("fixture %s is empty; an empty fixture is what let the zero gauge ship", fx.key)
		}
	}
	if wsA.inProgress == wsB.inProgress {
		t.Fatal("fixtures must have distinct in_progress counts")
	}

	body := scrape(t, newMetricsHandler(t, wsA, wsB))

	requireLines(t, body,
		`loom_ready_tasks{workspace="WS_A",priority="0"} 1`,
		`loom_ready_tasks{workspace="WS_A",priority="1"} 0`,
		`loom_ready_tasks{workspace="WS_A",priority="2"} 3`,
		`loom_ready_tasks{workspace="WS_A",priority="3"} 0`,
		`loom_ready_tasks{workspace="WS_A",priority="4"} 0`,
		`loom_in_progress_tasks{workspace="WS_A"} 4`,
		`loom_fleet_workers{workspace="WS_A",status="active"} 1`,
		`loom_fleet_workers{workspace="WS_A",status="idle"} 1`,
		`loom_fleet_workers{workspace="WS_A",status="blocked"} 1`,
		`loom_monitor_collection_ok{workspace="WS_A"} 1`,

		`loom_ready_tasks{workspace="WS_B",priority="1"} 2`,
		`loom_ready_tasks{workspace="WS_B",priority="4"} 5`,
		`loom_ready_tasks{workspace="WS_B",priority="0"} 0`,
		`loom_in_progress_tasks{workspace="WS_B"} 7`,
		`loom_fleet_workers{workspace="WS_B",status="active"} 0`,
		`loom_fleet_workers{workspace="WS_B",status="idle"} 2`,
		`loom_fleet_workers{workspace="WS_B",status="blocked"} 1`,
		`loom_monitor_collection_ok{workspace="WS_B"} 1`,
	)

	// WS_A's numbers must not be WS_B's.
	if strings.Contains(body, `loom_in_progress_tasks{workspace="WS_B"} 4`) {
		t.Errorf("WS_B reported WS_A's in-progress count\n\n%s", body)
	}

	// Every loom_ready_tasks sample carries a workspace label.
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "loom_ready_tasks{") {
			continue
		}
		if !strings.Contains(line, `workspace="`) {
			t.Errorf("loom_ready_tasks line without a workspace label: %q", line)
		}
	}

	// A collection timestamp is emitted per workspace and is not the zero time.
	for _, ws := range []string{"WS_A", "WS_B"} {
		prefix := `loom_monitor_collection_timestamp_seconds{workspace="` + ws + `"} `
		_, rest, found := strings.Cut(body, prefix)
		if !found {
			t.Fatalf("missing collection timestamp for %s\n\n%s", ws, body)
		}
		value, _, _ := strings.Cut(rest, "\n")
		if value == "0" {
			t.Errorf("%s collection timestamp is 0", ws)
		}
	}
}

func TestHandleMetrics_FailedWorkspaceDoesNotBlankOthers(t *testing.T) {
	healthy := metricsWorkspaceFixture{
		key:        "WS_OK",
		ready:      map[int]int{3: 2},
		inProgress: 6,
		agents: []*domain.Agent{
			{WorkspaceKey: "WS_OK", Name: "ok-1", State: domain.AgentStateIdle},
		},
	}
	broken := metricsWorkspaceFixture{key: "WS_BAD", collectorFails: true}

	body := scrape(t, newMetricsHandler(t, healthy, broken))

	requireLines(t, body,
		`loom_monitor_collection_ok{workspace="WS_OK"} 1`,
		`loom_monitor_collection_ok{workspace="WS_BAD"} 0`,
		`loom_ready_tasks{workspace="WS_OK",priority="3"} 2`,
		`loom_in_progress_tasks{workspace="WS_OK"} 6`,
	)

	// The broken workspace must be OMITTED, not reported as zero: a zero is
	// indistinguishable from a real empty queue.
	for _, unwanted := range []string{
		`loom_ready_tasks{workspace="WS_BAD"`,
		`loom_in_progress_tasks{workspace="WS_BAD"}`,
		`loom_fleet_workers{workspace="WS_BAD"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("broken workspace emitted %q; failed collection must omit samples, not zero them\n\n%s", unwanted, body)
		}
	}
}

func TestHandleMetrics_EmptyStore(t *testing.T) {
	body := scrape(t, newMetricsHandler(t))

	requireLines(t, body, `loom_monitor_collection_ok{workspace=""} 0`)
	if strings.Contains(body, "loom_ready_tasks{") {
		t.Errorf("empty store emitted task samples\n\n%s", body)
	}
	// The HELP/TYPE headers are still written, so a family that produces no
	// samples this scrape is still a declared family.
	for _, header := range []string{"# TYPE loom_ready_tasks gauge", "# TYPE loom_fleet_workers gauge"} {
		if !strings.Contains(body, header) {
			t.Errorf("missing %q on the empty-store path\n\n%s", header, body)
		}
	}
}

func TestHandleMetrics_WorkspaceListErrorReportsNotOK(t *testing.T) {
	st := newFakeMetricsStore(nil, nil)
	st.workspaces.err = context.DeadlineExceeded

	ds := NewMonitorDataSourceWithTTL(func() *monitor.MonitorData { return nil }, nil, time.Millisecond)
	storeDS := NewMonitorStoreDataSourceWithTTL(st, time.Millisecond)

	body := scrape(t, HandleMetrics(ds, storeDS, st))
	requireLines(t, body, `loom_monitor_collection_ok{workspace=""} 0`)
}

func TestHandleMetrics_EscapesWorkspaceLabel(t *testing.T) {
	fx := metricsWorkspaceFixture{
		key:        `we"ird\ws`,
		ready:      map[int]int{0: 2},
		inProgress: 3,
		agents: []*domain.Agent{
			{WorkspaceKey: `we"ird\ws`, Name: "q-1", State: domain.AgentStateIdle},
		},
	}

	// scrape() fails on a parse error, which is what an unescaped quote produces.
	body := scrape(t, newMetricsHandler(t, fx))

	requireLines(t, body, `loom_ready_tasks{workspace="we\"ird\\ws",priority="0"} 2`)
}

func TestEscapeLabel(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain":     "plain",
		`a"b`:       `a\"b`,
		`a\b`:       `a\\b`,
		"a\nb":      `a\nb`,
		`both"and\`: `both\"and\\`,
	}
	for in, want := range cases {
		if got := escapeLabel(in); got != want {
			t.Errorf("escapeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- fake store -----------------------------------------------------------

// fakeMetricsStore is a memstore with the workspace list and agent list under
// test control. The agent list has to be faked rather than created: LiveStatus
// is a derived, never-persisted field, so no store write can produce a working
// agent, and "active" is the bucket that matters most here.
type fakeMetricsStore struct {
	store.Store
	workspaces *fakeWorkspaceStore
	agents     *fakeAgentStore
}

func newFakeMetricsStore(workspaces []*domain.Workspace, agentsByWorkspace map[string][]*domain.Agent) *fakeMetricsStore {
	base := memstore.New()
	return &fakeMetricsStore{
		Store:      base,
		workspaces: &fakeWorkspaceStore{WorkspaceStore: base.Workspaces(), list: workspaces},
		agents:     &fakeAgentStore{AgentStore: base.Agents(), byWorkspace: agentsByWorkspace},
	}
}

func (s *fakeMetricsStore) Workspaces() store.WorkspaceStore { return s.workspaces }
func (s *fakeMetricsStore) Agents() store.AgentStore         { return s.agents }

type fakeWorkspaceStore struct {
	store.WorkspaceStore
	list []*domain.Workspace
	err  error
}

func (s *fakeWorkspaceStore) List(context.Context) ([]*domain.Workspace, error) {
	return s.list, s.err
}

type fakeAgentStore struct {
	store.AgentStore
	byWorkspace map[string][]*domain.Agent
}

func (s *fakeAgentStore) List(_ context.Context, workspaceKey string) ([]*domain.Agent, error) {
	return s.byWorkspace[workspaceKey], nil
}
