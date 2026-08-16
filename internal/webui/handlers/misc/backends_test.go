package misc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
)

// mockBackendOps implements operationalview.BackendHealthQuery for testing.
type mockBackendOps struct {
	fn func() ([]operationalview.Backend, error)
}

func (m *mockBackendOps) ListBackendsHealth() ([]operationalview.Backend, error) {
	return m.fn()
}

func (m *mockBackendOps) BackendHealth(string) (operationalview.Backend, bool) {
	return operationalview.Backend{}, false
}

func TestHandleGetBackendsHealth_AllAvailable(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]operationalview.Backend, error) {
		return []operationalview.Backend{
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
	backendOps := &mockBackendOps{fn: func() ([]operationalview.Backend, error) {
		return []operationalview.Backend{
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
	backendOps := &mockBackendOps{fn: func() ([]operationalview.Backend, error) {
		return []operationalview.Backend{}, nil
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
	backendOps := &mockBackendOps{fn: func() ([]operationalview.Backend, error) {
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

func TestHandleGetBackendsHealth_Error(t *testing.T) {
	backendOps := &mockBackendOps{fn: func() ([]operationalview.Backend, error) {
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
