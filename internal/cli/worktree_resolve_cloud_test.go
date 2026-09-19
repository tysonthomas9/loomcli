package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func newFleetDBTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("TCP listeners unavailable in this test environment: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestNewResolverModeCloudUsesScopedWorkspaceRoute(t *testing.T) {
	var scopedWorkspaceHits, adminWorkspaceHits int
	server := newFleetDBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/WS1/workspace":
			scopedWorkspaceHits++
			if r.Method != http.MethodGet {
				t.Errorf("scoped workspace method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"key":  "WS1",
				"name": "Workspace One",
			})
		case "/api/v1/WS1/repos":
			_ = json.NewEncoder(w).Encode(map[string]any{"repos": []any{}})
		case "/api/v1/WS1/daemon":
			http.NotFound(w, r)
		case "/api/v1/admin/workspaces":
			adminWorkspaceHits++
			http.Error(w, "global workspace listing is forbidden", http.StatusForbidden)
		default:
			t.Errorf("unexpected FleetDB request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv(bootstrap.EnvWorkspace, "WS1")
	config.InvalidateConfigCache()
	t.Cleanup(config.InvalidateConfigCache)

	resolver, err := NewResolver()
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	if resolver.Workspace != "WS1" {
		t.Errorf("workspace = %q, want WS1", resolver.Workspace)
	}
	if len(resolver.Config.Workspaces) != 1 {
		t.Fatalf("workspace count = %d, want 1", len(resolver.Config.Workspaces))
	}
	if _, ok := resolver.Config.Workspaces["WS1"]; !ok {
		t.Error("scoped workspace missing from resolver config")
	}
	if scopedWorkspaceHits != 1 {
		t.Errorf("scoped workspace route hits = %d, want 1", scopedWorkspaceHits)
	}
	if adminWorkspaceHits != 0 {
		t.Errorf("global workspace list hits = %d, want 0", adminWorkspaceHits)
	}
}

func TestAcquireLockModeCloudAvoidsGlobalWorkspaceList(t *testing.T) {
	adminWorkspaceHits := 0
	server := newFleetDBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/admin/workspaces" {
			adminWorkspaceHits++
		}
		http.NotFound(w, r)
	}))

	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv(bootstrap.EnvWorkspace, "WS1")
	dir := t.TempDir()
	if err := AcquireLock(dir, "task", "agent"); err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	t.Cleanup(func() { _ = ReleaseLock(dir) })

	info, err := ReadLockFile(dir)
	if err != nil {
		t.Fatalf("ReadLockFile() error = %v", err)
	}
	if info.Workspace != "WS1" {
		t.Errorf("lock workspace = %q, want WS1", info.Workspace)
	}
	if adminWorkspaceHits != 0 {
		t.Errorf("global workspace list hits = %d, want 0", adminWorkspaceHits)
	}
}

func TestGetWorkspaceRuntimeDirModeCloudAvoidsFleetDB(t *testing.T) {
	requests := 0
	server := newFleetDBTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))

	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv(bootstrap.EnvWorkspace, "WS1")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")

	if got := GetWorkspaceRuntimeDir(); got != "." {
		t.Errorf("GetWorkspaceRuntimeDir() = %q, want .", got)
	}
	if requests != 0 {
		t.Errorf("FleetDB requests = %d, want 0", requests)
	}
}
