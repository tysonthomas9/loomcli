package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetBackends_WithListFn(t *testing.T) {
	listFn := func() ([]BackendInfo, error) {
		return []BackendInfo{
			{Name: "claude", DisplayName: "Claude", Provider: "anthropic", Status: "available", BrandColor: "#da7756", Version: "1.0.0"},
			{Name: "codex", DisplayName: "Codex", Provider: "openai", Status: "unavailable", BrandColor: "#412991"},
		}, nil
	}
	handler := handleGetBackends(listFn)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(resp.Data))
	}
	if resp.Data[0].Name != "claude" || resp.Data[0].Status != "available" {
		t.Errorf("unexpected backend[0]: %+v", resp.Data[0])
	}
	if resp.Data[1].Name != "codex" || resp.Data[1].Status != "unavailable" {
		t.Errorf("unexpected backend[1]: %+v", resp.Data[1])
	}
}

func TestHandleGetBackends_NilListFn(t *testing.T) {
	handler := handleGetBackends(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	// Should fall back to validBackends
	if len(resp.Data) != len(validBackends) {
		t.Fatalf("expected %d backends (validBackends), got %d", len(validBackends), len(resp.Data))
	}
	for i, b := range resp.Data {
		if b.Name != validBackends[i] {
			t.Errorf("expected backend[%d] name %q, got %q", i, validBackends[i], b.Name)
		}
		if b.Status != "unavailable" {
			t.Errorf("expected backend[%d] status unavailable, got %q", i, b.Status)
		}
	}
}

func TestHandleGetBackends_ListFnError(t *testing.T) {
	listFn := func() ([]BackendInfo, error) {
		return nil, errors.New("backend inspection failed")
	}
	handler := handleGetBackends(listFn)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsResponse
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

func TestHandleGetBackends_EmptyList(t *testing.T) {
	listFn := func() ([]BackendInfo, error) {
		return []BackendInfo{}, nil
	}
	handler := handleGetBackends(listFn)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify JSON contains [] not null
	body := rec.Body.String()
	var resp backendsResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
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

func TestHandleGetBackends_NilResult(t *testing.T) {
	// listFn returns nil slice (not empty slice) — handler should normalize to []
	listFn := func() ([]BackendInfo, error) {
		return nil, nil
	}
	handler := handleGetBackends(listFn)

	req := httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp backendsResponse
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
