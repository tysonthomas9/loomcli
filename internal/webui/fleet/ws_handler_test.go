package fleet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestFleetWSHandler_ResolvesStore(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   30 * time.Minute,
			CheckInterval: 1 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	defer reg.Close()

	// Register a workspace
	if err := reg.Register("ws-test"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	var receivedStore *Store
	handler := FleetWSHandler(reg.Get, func(s *Store) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			receivedStore = s
			w.WriteHeader(http.StatusOK)
		}
	})

	// Build a request with workspace in context
	req := httptest.NewRequest("POST", "/api/workspaces/ws-test/fleet/register", nil)
	ctx := middleware.WithWorkspace(req.Context(), "ws-test")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if receivedStore == nil {
		t.Fatal("expected makeHandler to receive a non-nil Store")
	}

	// Verify it's the same Store as from the registry
	expected, ok := reg.Get("ws-test")
	if !ok {
		t.Fatal("expected Get to return true for registered workspace")
	}
	if receivedStore != expected {
		t.Error("expected FleetWSHandler to pass the registry's Store to makeHandler")
	}
}

func TestFleetWSHandler_WorkspaceNotFound(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewStoreRegistry(
		RedisConfig{Address: mr.Addr()},
		TimeoutConfig{
			TaskTimeout:   30 * time.Minute,
			CheckInterval: 1 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	defer reg.Close()

	// Do NOT register any workspace — the lookup should fail
	handlerCalled := false
	handler := FleetWSHandler(reg.Get, func(s *Store) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		}
	})

	req := httptest.NewRequest("POST", "/api/workspaces/unknown-ws/fleet/register", nil)
	ctx := middleware.WithWorkspace(req.Context(), "unknown-ws")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rr.Code)
	}

	if handlerCalled {
		t.Error("inner handler should not have been called for unknown workspace")
	}

	// Verify the response body is JSON with the expected fields
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body["success"] != false {
		t.Errorf("expected success=false, got %v", body["success"])
	}
	errMsg, _ := body["error"].(string)
	if errMsg == "" {
		t.Error("expected non-empty error message in response")
	}
}
