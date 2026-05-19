package handlermux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestBuildHandlersConstructsExpectedHandlers(t *testing.T) {
	hub := realtime.NewHub()
	h := BuildHandlers(HandlerDeps{
		Hub:                hub,
		ExtAuthURL:         "https://auth.example.test",
		BackendsHealthH:    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) },
		NotifyToken:        "secret",
		DaemonSupervisor:   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		DaemonConfig:       func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) },
		FleetTimeoutsFn:    func() int64 { return 3 },
		TerminalGraceMS:    100,
		TerminalIdleMS:     200,
		TerminalMaxSession: 4,
		IssueBackendFn: func(context.Context) backend.IssueBackend {
			return &stubIssueBackend{}
		},
		DaemonExpected: false,
	})
	if h == nil {
		t.Fatal("BuildHandlers returned nil")
	}
	if h.Health == nil || h.APIHealth == nil || h.ClientErrors == nil || h.AuthConfig == nil || h.Metrics == nil {
		t.Fatalf("missing core handlers: %+v", h)
	}
	if h.GetTerminalConfig == nil || h.GetBackendsHealth == nil || h.ListEditors == nil || h.OpenEditor == nil {
		t.Fatalf("missing utility handlers: %+v", h)
	}
	if h.NotifySessionChange == nil || h.DaemonSupervisor == nil || h.DaemonConfig == nil {
		t.Fatalf("missing optional handlers: %+v", h)
	}

	rec := httptest.NewRecorder()
	h.APIHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("APIHealth no-daemon status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.GetBackendsHealth(rec, httptest.NewRequest(http.MethodGet, "/api/backends/health", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("GetBackendsHealth status = %d", rec.Code)
	}

	h.ClientErrLimiter.Stop()
	h.AuthCfgLimiter.Stop()
}

func TestBuildHandlersOmitsOptionalHandlersWhenDepsAbsent(t *testing.T) {
	h := BuildHandlers(HandlerDeps{})
	if h.NotifySessionChange != nil {
		t.Fatal("NotifySessionChange should be nil without hub")
	}
	if h.GetBackendsHealth != nil || h.DaemonSupervisor != nil || h.DaemonConfig != nil {
		t.Fatalf("optional handlers should be nil: %+v", h)
	}
	h.ClientErrLimiter.Stop()
	h.AuthCfgLimiter.Stop()
}

func TestWorkspaceOpsModuleAgentQueueOptionalRoute(t *testing.T) {
	agentQueue := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{}, agentQueue)
	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents/falcon/queue", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("agent queue status = %d", rec.Code)
	}
}

type typedNilPool struct {
	*stubErrorPool
}

func TestNormalizePoolTypedNilPointer(t *testing.T) {
	var pool *typedNilPool
	if normalizePool(pool) != nil {
		t.Fatal("typed nil pool should normalize to nil")
	}
	if normalizePool(&stubErrorPool{}) == nil {
		t.Fatal("non-nil pool normalized to nil")
	}
}

func TestSetupWorkerAPIRoutesWrapper(t *testing.T) {
	mux := http.NewServeMux()
	SetupWorkerAPIRoutes(
		mux,
		"token",
		func(workspaceID, agentID string) string { return "/tmp/" + workspaceID + "/" + agentID },
		func(workspaceID string) string { return "/tmp/" + workspaceID + "/events" },
		func(workspaceID, agentID string) string { return "/tmp/" + workspaceID + "/" + agentID + ".log" },
		func(workspaceID string) bool { return workspaceID == "WS" },
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/workers/register", strings.NewReader(`{"workspace":"WS","agent":"falcon"}`))
	req.Header.Set("Authorization", "Bearer token")
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("worker route not registered, status=%d body=%s", rec.Code, rec.Body.String())
	}
}
