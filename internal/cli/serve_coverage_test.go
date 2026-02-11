package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsMiddleware_DefaultOrigin(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Empty corsOrigin should use default localhost:<webui-port>
	wrapped := corsMiddleware("", handler)

	req := httptest.NewRequest("GET", "/api/status", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	origin := rr.Header().Get("Access-Control-Allow-Origin")
	if origin == "" {
		t.Error("expected Access-Control-Allow-Origin header to be set")
	}
}

func TestCorsMiddleware_CustomOrigin(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := corsMiddleware("https://example.com", handler)

	req := httptest.NewRequest("GET", "/api/status", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	origin := rr.Header().Get("Access-Control-Allow-Origin")
	if origin != "https://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", origin, "https://example.com")
	}

	methods := rr.Header().Get("Access-Control-Allow-Methods")
	if methods != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", methods, "GET, OPTIONS")
	}
}

func TestCorsMiddleware_Options(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for OPTIONS")
	})

	wrapped := corsMiddleware("https://example.com", handler)

	req := httptest.NewRequest("OPTIONS", "/api/status", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", rr.Code)
	}
}

func TestHandleHealth_Coverage(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}

func TestGroupAgentsByWorkspace_Coverage(t *testing.T) {
	agents := []AgentStatus{
		{Name: "alpha", Workspace: "ws1"},
		{Name: "beta", Workspace: "ws1"},
		{Name: "gamma", Workspace: "ws2"},
		{Name: "delta", Workspace: ""},
	}

	groups := groupAgentsByWorkspace(agents)

	if len(groups["ws1"]) != 2 {
		t.Errorf("ws1 count = %d, want 2", len(groups["ws1"]))
	}
	if len(groups["ws2"]) != 1 {
		t.Errorf("ws2 count = %d, want 1", len(groups["ws2"]))
	}
	if len(groups["(legacy)"]) != 1 {
		t.Errorf("(legacy) count = %d, want 1", len(groups["(legacy)"]))
	}
}

func TestGroupAgentsByWorkspace_Empty(t *testing.T) {
	groups := groupAgentsByWorkspace([]AgentStatus{})
	if len(groups) != 0 {
		t.Errorf("expected empty map, got %d entries", len(groups))
	}
}

func TestGroupAgentsByWorkspace_AllSameWorkspace(t *testing.T) {
	agents := []AgentStatus{
		{Name: "a", Workspace: "ws"},
		{Name: "b", Workspace: "ws"},
		{Name: "c", Workspace: "ws"},
	}

	groups := groupAgentsByWorkspace(agents)
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
	if len(groups["ws"]) != 3 {
		t.Errorf("ws count = %d, want 3", len(groups["ws"]))
	}
}

func TestWriteJSON_Coverage(t *testing.T) {
	rr := httptest.NewRecorder()

	data := map[string]string{"key": "value"}
	writeJSON(rr, data)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}

func TestHandleStaleDetector_NotEnabled(t *testing.T) {
	// Ensure staleDetectorInstance is nil
	origInstance := staleDetectorInstance
	staleDetectorInstance = nil
	t.Cleanup(func() { staleDetectorInstance = origInstance })

	req := httptest.NewRequest("GET", "/api/stale-detector", nil)
	rr := httptest.NewRecorder()

	handleStaleDetector(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if body == "" {
		t.Error("response body should not be empty")
	}
}
