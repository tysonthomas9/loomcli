package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

// CodeReviewBase reads the task from the given workspace's FleetDB, and a
// failed read is an error, not "no blocker" (P1.26).
func TestCodeReviewBaseReadsTheTaskFromItsWorkspace(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.Error(w, "fleet down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv(bootstrap.EnvFleetDBURL, srv.URL)
	t.Setenv(bootstrap.EnvFleetDBActor, "tester")

	if _, found, err := CodeReviewBase(context.Background(), "WS-A", "T1"); err == nil || found {
		t.Fatalf("CodeReviewBase with FleetDB failing = found %v, err %v; want an error", found, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[0] != "/api/v1/WS-A/issues/T1" {
		t.Fatalf("FleetDB requests = %v, want /api/v1/WS-A/issues/T1 first", paths)
	}
}
