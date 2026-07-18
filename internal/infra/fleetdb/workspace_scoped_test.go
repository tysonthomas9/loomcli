package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// GetWorkspaceScoped must hit the workspace-scoped route
// (GET /api/v1/{workspace}/workspace), NOT the global admin route, so a
// workspace-scoped credential can resolve its workspace without a global role.
func TestWorkspaceStoreGetScopedUsesWorkspaceScopedRoute(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS1/workspace" {
			t.Fatalf("unexpected request %s %s (want GET /api/v1/WS1/workspace)", r.Method, r.URL.Path)
		}
		writeJSON(t, w, domain.Workspace{Key: "WS1", Name: "Workspace One", CreatedAt: now, UpdatedAt: now})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	sg, ok := client.Workspaces().(store.ScopedWorkspaceGetter)
	if !ok {
		t.Fatal("fleetdb workspace store must implement store.ScopedWorkspaceGetter")
	}
	ws, err := sg.GetWorkspaceScoped(t.Context(), "WS1")
	if err != nil {
		t.Fatalf("GetWorkspaceScoped: %v", err)
	}
	if ws.Key != "WS1" {
		t.Errorf("Key = %q, want WS1", ws.Key)
	}
}
