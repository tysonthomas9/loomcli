package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestWorkspaceAwareIssueBackendForURL_UsesConcreteURLWhenEnvUnset(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")

	fn := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "tester")
	be := fn(middleware.WithWorkspace(context.Background(), "CLEAN"))
	if be == nil {
		t.Fatal("backend was nil")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Fatalf("BackendName() = %q, want fleet", got)
	}
}

func TestWorkspaceAwareIssueBackendFallbacksAndCache(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)
	mock := &MockIssueBackend{}
	setDefaultIssueBackend(mock)

	localFn := WorkspaceAwareIssueBackendForURL("", "")
	if got := localFn(context.Background()); got != mock {
		t.Fatalf("local workspace-aware backend = %T, want mock", got)
	}

	remoteFn := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "")
	if got := remoteFn(context.Background()); got != mock {
		t.Fatalf("remote fn without workspace = %T, want default mock", got)
	}
	ctx := middleware.WithWorkspace(context.Background(), "WS")
	first := remoteFn(ctx)
	second := remoteFn(ctx)
	if first == nil || second == nil {
		t.Fatal("workspace backend was nil")
	}
	if first != second {
		t.Fatal("workspace backend should be cached per workspace")
	}
}

func TestCreateAPIIssueBackendValidationAndSuccess(t *testing.T) {
	oldServerFlag, oldWorkspaceFlag := serverFlag, workspaceFlag
	t.Cleanup(func() {
		serverFlag, workspaceFlag = oldServerFlag, oldWorkspaceFlag
	})
	serverFlag, workspaceFlag = "", ""
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_WORKSPACE", "")

	if _, err := createAPIIssueBackend(); err == nil || !strings.Contains(err.Error(), "requires --server") {
		t.Fatalf("missing server err = %v", err)
	}

	t.Run("auth discovery failure", func(t *testing.T) {
		serverFlag = "://bad-url"
		workspaceFlag = "WS"
		if _, err := createAPIIssueBackend(); err == nil || !strings.Contains(err.Error(), "api backend auth setup") {
			t.Fatalf("auth setup err = %v", err)
		}
	})

	t.Run("missing workspace after auth discovery", func(t *testing.T) {
		serverFlag, workspaceFlag = "", ""
		srv := newOpenAuthConfigServer(t)
		t.Setenv("LOOM_SERVER_URL", srv.URL)
		t.Setenv("LOOM_WORKSPACE", "")
		if _, err := createAPIIssueBackend(); err == nil || !strings.Contains(err.Error(), "requires --workspace") {
			t.Fatalf("missing workspace err = %v", err)
		}
	})

	t.Run("success uses flags over env", func(t *testing.T) {
		srv := newOpenAuthConfigServer(t)
		t.Setenv("LOOM_SERVER_URL", "://ignored-env")
		t.Setenv("LOOM_WORKSPACE", "ignored-env")
		serverFlag = srv.URL
		workspaceFlag = "WS-FLAG"
		be, err := createAPIIssueBackend()
		if err != nil {
			t.Fatalf("createAPIIssueBackend: %v", err)
		}
		if be == nil {
			t.Fatal("backend is nil")
		}
		if got := be.BackendName(); got != "api" {
			t.Fatalf("backend = %T name=%q, want api backend", be, got)
		}
	})
}

func newOpenAuthConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"open"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}
