package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func setWorkflowRuntimeProvider(t *testing.T, backend string) {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if err := localnodeconfig.SetRuntimeProvider("TEST", backend); err != nil {
		t.Fatalf("set runtime provider: %v", err)
	}
}

// When the epic-runner run resolves to the local task runner and the backend
// CLI/auth is missing, the run must be rejected fail-closed (400) and no
// DriverRun created — never queued to fake-complete later.
func TestCreateWorkflowRunPreflightFailsClosedForLocalRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	module := newWorkflowTestModule(st)
	module.backendHealth = backendHealthQueryFunc(func(string) (BackendHealth, bool) {
		return BackendHealth{Installed: false, APIKeySet: false, Message: "codex binary not found on PATH"}, true
	})

	mux := http.NewServeMux()
	module.Register(mux)

	// Absent runner field => UI "Locally" default => local-task-runner.
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 (fail-closed preflight)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "local task runner cannot start") || !strings.Contains(body, "local_backend_unavailable") {
		t.Fatalf("error body = %q, want local_backend_unavailable preflight message", body)
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

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202 (healthy preflight)", rec.Code, rec.Body.String())
	}
}

// An explicit non-local runner (e.g. daytona) is NOT gated by local preflight,
// even when the local backend is unhealthy.
func TestCreateWorkflowRunPreflightSkipsExplicitNonLocalRunner(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	called := false
	module := newWorkflowTestModule(st)
	module.backendHealth = backendHealthQueryFunc(func(string) (BackendHealth, bool) {
		called = true
		return BackendHealth{Installed: false}, true
	})

	mux := http.NewServeMux()
	module.Register(mux)

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

func TestCreateWorkflowRunPreflightUsesConfiguredBackend(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	setWorkflowRuntimeProvider(t, "gemini")

	checked := ""
	module := newWorkflowTestModule(st)
	module.backendHealth = backendHealthQueryFunc(func(name string) (BackendHealth, bool) {
		checked = name
		return BackendHealth{Available: true, Installed: true, APIKeySet: true}, true
	})

	mux := http.NewServeMux()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	if checked != "gemini" {
		t.Fatalf("health-checked backend = %q, want configured gemini", checked)
	}
}

func TestCreateWorkflowRunPreflightRejectsMissingAuth(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	setWorkflowRuntimeProvider(t, "codex")

	module := newWorkflowTestModule(st)
	module.backendHealth = backendHealthQueryFunc(func(string) (BackendHealth, bool) {
		return BackendHealth{Installed: true, APIKeySet: false, Message: "OPENAI_API_KEY not set"}, true
	})

	mux := http.NewServeMux()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "local_backend_auth_missing") {
		t.Fatalf("status = %d body=%s, want missing-auth rejection", rec.Code, rec.Body.String())
	}
}

func TestCreateWorkflowRunPreflightRejectsUnknownBackend(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	setWorkflowRuntimeProvider(t, "made-up")

	module := newWorkflowTestModule(st)
	module.backendHealth = backendHealthQueryFunc(func(string) (BackendHealth, bool) {
		return BackendHealth{}, false
	})

	mux := http.NewServeMux()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "made-up") {
		t.Fatalf("status = %d body=%s, want unknown-backend rejection", rec.Code, rec.Body.String())
	}
}

func TestCreateWorkflowRunPreflightRejectsMissingHealthPort(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	module := newWorkflowTestModule(st)
	module.backendHealth = nil
	mux := http.NewServeMux()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "backend health is unavailable") {
		t.Fatalf("status = %d body=%s, want missing-port fail-closed rejection", rec.Code, rec.Body.String())
	}
	runs, err := st.DriverRuns().List(context.Background(), "TEST", store.DriverRunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("created %d runs without a health port, want 0", len(runs))
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
