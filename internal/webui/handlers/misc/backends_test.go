package misc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// mockBackendOps implements ops.BackendOps for testing.
type mockBackendOps struct {
	fn func() ([]ops.BackendHealth, error)
}

func (m *mockBackendOps) ListBackendsHealth() ([]ops.BackendHealth, error) {
	return m.fn()
}

func TestHandleGetBackendsHealth_AllAvailable(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{
			{Name: "claude", DisplayName: "Claude", Available: true, Installed: true, APIKeySet: true, Version: "1.0.0"},
			{Name: "codex", DisplayName: "Codex", Available: true, Installed: true, APIKeySet: true},
		}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(resp.Data))
	}
	if resp.Data[0].Name != "claude" || !resp.Data[0].Available {
		t.Errorf("unexpected backend[0]: %+v", resp.Data[0])
	}
	if resp.Data[1].Name != "codex" || !resp.Data[1].Available {
		t.Errorf("unexpected backend[1]: %+v", resp.Data[1])
	}
}

func TestHandleGetBackendsHealth_MixedAvailability(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{
			{Name: "claude", DisplayName: "Claude", Available: true, Installed: true, APIKeySet: true, Version: "1.0.0"},
			{Name: "codex", DisplayName: "Codex", Available: false, Installed: false, APIKeySet: false, Message: "codex not found on PATH"},
		}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(resp.Data))
	}
	if resp.Data[0].Available != true {
		t.Errorf("expected claude available, got %+v", resp.Data[0])
	}
	if resp.Data[1].Available != false || resp.Data[1].Installed != false {
		t.Errorf("expected codex unavailable/not installed, got %+v", resp.Data[1])
	}
}

func TestHandleGetBackendsHealth_EmptyList(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data slice")
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty backends, got %d", len(resp.Data))
	}
}

func TestHandleGetBackendsHealth_NilResult(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return nil, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data slice (should normalize nil to empty)")
	}
}

func TestHandleGetBackendsHealth_DecoratesKnownBackend(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{
			{Name: "claude", DisplayName: "Claude", Available: true, Installed: true, APIKeySet: true},
		}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(resp.Data))
	}
	got := resp.Data[0]
	if got.Description == "" {
		t.Error("expected curated description for claude, got empty")
	}
	if !got.Authenticated {
		t.Error("expected authenticated=true when api_key_set")
	}
	if !got.Ready {
		t.Error("expected ready=true when installed && authenticated")
	}
	if len(got.EnvVars) == 0 {
		t.Error("expected env_vars for claude")
	}
	if len(got.LoginActions) == 0 {
		t.Error("expected login_actions for claude")
	}
	for _, a := range got.InstallActions {
		if a.Command == "" || a.Label == "" {
			t.Errorf("install action missing label/command: %+v", a)
		}
	}
	for _, e := range got.EnvVars {
		if !e.RestartRequired {
			t.Errorf("env var %s should be restart_required=true (server reads env at startup)", e.Name)
		}
	}
}

func TestHandleGetBackendsHealth_UnknownBackendNoMetadata(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{
			{Name: "mystery", DisplayName: "Mystery", Installed: true, APIKeySet: false},
		}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := resp.Data[0]
	if got.Description != "" {
		t.Errorf("unknown backend should have empty description, got %q", got.Description)
	}
	if len(got.InstallActions) != 0 || len(got.LoginActions) != 0 || len(got.EnvVars) != 0 {
		t.Errorf("unknown backend should have no curated metadata: %+v", got)
	}
	if got.Authenticated {
		t.Error("authenticated must be false when api_key_set=false")
	}
	if got.Ready {
		t.Error("ready must be false when api_key_set=false")
	}
}

func TestHandleGetBackendsHealth_InstalledButNotAuthenticated(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return []ops.BackendHealth{
			{Name: "codex", DisplayName: "Codex", Installed: true, APIKeySet: false, Available: false},
		}, nil
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := resp.Data[0]
	if got.Authenticated {
		t.Error("authenticated must be false")
	}
	if got.Ready {
		t.Error("ready must be false when not authenticated")
	}
	// Installed-but-unauthenticated should still surface login actions
	// so the UI has a clear next step.
	if len(got.LoginActions) == 0 {
		t.Error("expected codex login actions for installed-but-not-authenticated case")
	}
}

func TestHandleGetBackendsHealth_Error(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]ops.BackendHealth, error) {
		return nil, errors.New("backend inspection failed")
	}}
	handler := handleGetBackendsHealth(backendOps)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsHealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "failed to list backends" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}
