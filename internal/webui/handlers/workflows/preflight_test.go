package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type failingDaemonTargetStore struct {
	store.Store
	err error
}

func (s failingDaemonTargetStore) Daemon() store.DaemonProfileStore {
	return failingDaemonProfileStore{err: s.err}
}

type failingDaemonProfileStore struct{ err error }

func (s failingDaemonProfileStore) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return nil, s.err
}

func (s failingDaemonProfileStore) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return nil, errors.New("unexpected daemon profile upsert")
}

// When the epic-runner run resolves to the local task runner and the backend
// CLI/auth is missing, the run must be rejected fail-closed (400) and no
// DriverRun created — never queued to fake-complete later.
func TestCreateWorkflowRunPreflightFailsClosedForLocalRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Installed: false, APIKeySet: false, Message: "codex binary not found on PATH"}, true
	})
	defer restore()

	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	// Absent runner field => UI "Locally" default => local-task-runner.
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 (fail-closed preflight)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error     string                  `json:"error"`
		Preflight runtimepreflight.Result `json:"preflight"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != body.Preflight.Message || body.Preflight.ErrorClass != runtimepreflight.ErrorClassUnavailable {
		t.Fatalf("error body = %+v, want canonical local_backend_unavailable verdict", body)
	}

	runs, err := st.DriverRuns().List(ctx, "TEST", store.DriverRunFilter{})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("created %d runs, want 0 (must not queue when preflight fails)", len(runs))
	}
}

// A healthy backend lets the local epic-runner run be created normally.
func TestCreateWorkflowRunPreflightPassesWhenHealthy(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"}, true
	})
	defer restore()

	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202 (healthy preflight)", rec.Code, rec.Body.String())
	}
}

func TestStepOneGateParityWebUI(t *testing.T) {
	fixture := loadWebUIGateParityFixture(t)
	st := memstore.New()
	if _, err := st.Daemon().Upsert(context.Background(), &domain.DaemonProfile{
		WorkspaceKey: fixture.Workspace,
		AgentBackend: fixture.Backend,
	}); err != nil {
		t.Fatalf("upsert daemon profile: %v", err)
	}
	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		return fixture.Health, true
	})
	t.Cleanup(restore)
	err := NewModule(st).preflightRunnerForRun(context.Background(), fixture.Workspace, BuiltinEpicRunnerWorkflowName, json.RawMessage(`{}`))
	var notReady *runtimepreflight.NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("Web UI gate error = %T %v, want *NotReadyError", err, err)
	}
	if notReady.Result.Backend != fixture.Backend || notReady.Result.Health == nil || *notReady.Result.Health != fixture.Health ||
		notReady.Result.ErrorClass != fixture.ErrorClass || notReady.Result.Message != fixture.Message {
		t.Fatalf("Web UI gate = %+v, want backend:%q health:%+v class:%q message:%q", notReady.Result, fixture.Backend, fixture.Health, fixture.ErrorClass, fixture.Message)
	}
}

type webUIGateParityFixture struct {
	Workspace  string                        `json:"workspace"`
	Backend    string                        `json:"backend"`
	Health     runtimepreflight.HealthStatus `json:"health"`
	ErrorClass runtimepreflight.ErrorClass   `json:"error_class"`
	Message    string                        `json:"message"`
}

func loadWebUIGateParityFixture(t *testing.T) webUIGateParityFixture {
	t.Helper()
	data, err := os.ReadFile("../../../runtimepreflight/testdata/gate-parity.json")
	if err != nil {
		t.Fatalf("read gate parity fixture: %v", err)
	}
	var fixture webUIGateParityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode gate parity fixture: %v", err)
	}
	return fixture
}

func TestCreateWorkflowRunPreflightOperationalFailureIsNotAVerdict(t *testing.T) {
	baseStore := memstore.New()
	st := failingDaemonTargetStore{Store: baseStore, err: errors.New("fleet unavailable")}
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("error body = %s, want plain error", rec.Body.String())
	}
	if _, ok := body["preflight"]; ok {
		t.Fatalf("operational failure body = %s, must not contain a verdict", rec.Body.String())
	}
}

// An explicit non-local runner (e.g. daytona) is NOT gated by local preflight,
// even when the local backend is unhealthy.
func TestCreateWorkflowRunPreflightSkipsExplicitNonLocalRunner(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	called := false
	restore := runtimepreflight.SetHealthCheckerForTest(func(string) (runtimepreflight.HealthStatus, bool) {
		called = true
		return runtimepreflight.HealthStatus{Installed: false}, true
	})
	defer restore()

	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","runner":"daytona-task-runner"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202 (non-local runner not gated)", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatalf("backend health check ran for an explicit non-local runner")
	}
}

func TestRunnerIsLocal(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"absent runner defaults local", `{"epicId":"E1"}`, true},
		{"empty payload defaults local", ``, true},
		{"empty braces defaults local", `{}`, true},
		{"explicit local", `{"runner":"local-task-runner"}`, true},
		{"explicit daytona", `{"runner":"daytona-task-runner"}`, false},
		{"explicit github review", `{"runner":"github-review-task-runner"}`, false},
		{"malformed json defaults local", `{not-json`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runnerIsLocal(json.RawMessage(tc.payload)); got != tc.want {
				t.Fatalf("runnerIsLocal(%q) = %v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}
