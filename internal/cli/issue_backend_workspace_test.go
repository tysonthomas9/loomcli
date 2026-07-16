package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

func TestWorkspaceAwareIssueBackendForConfigAuthenticatesV2MutationWait(t *testing.T) {
	const serviceCredential = "embedded-service-credential"
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/SECURE/events/mutations" {
			t.Errorf("request = %s %s, want GET /api/v2/SECURE/events/mutations", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != serviceCredential || r.Header.Get("X-Fleet-API-Key") != serviceCredential {
			t.Error("v2 mutation wait did not carry the configured FleetDB service credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": "0"})
	}))
	defer server.Close()

	factory := WorkspaceAwareIssueBackendForConfig(server.URL, serviceCredential, "local-service")
	issueBackend := factory(middleware.WithWorkspace(context.Background(), "SECURE"))
	cursorBackend, ok := issueBackend.(backend.CursorMutationBackend)
	if !ok {
		t.Fatalf("backend %T does not implement CursorMutationBackend", issueBackend)
	}
	if _, err := cursorBackend.WaitForMutationsAfter(context.Background(), "0", 1); err != nil {
		t.Fatalf("WaitForMutationsAfter: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestWorkspaceAwareIssueBackendForURLKeepsExternalEnvironmentAuth(t *testing.T) {
	const externalCredential = "external-service-credential"
	t.Setenv(bootstrap.EnvFleetDBAPIKey, externalCredential)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != externalCredential {
			t.Error("URL-based factory did not preserve environment-provided FleetDB authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": "0"})
	}))
	defer server.Close()

	issueBackend := WorkspaceAwareIssueBackendForURL(server.URL, "external-service")(
		middleware.WithWorkspace(context.Background(), "EXTERNAL"),
	)
	cursorBackend := issueBackend.(backend.CursorMutationBackend)
	if _, err := cursorBackend.GetMutationsAfter(context.Background(), "0"); err != nil {
		t.Fatalf("GetMutationsAfter: %v", err)
	}
}
