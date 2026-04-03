package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// --- Test 13: Token exchange Cache-Control: no-store ---

func TestSSETokenExchange_CacheControlNoStore(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := handleSSEToken(store)

	identity := middleware.UserIdentity{UserID: "user-123", Email: "test@example.com"}
	ctx := middleware.WithUserIdentity(context.Background(), identity)
	ctx = middleware.WithWorkspace(ctx, "test-ws")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events/token", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	// Verify Cache-Control: no-store (prevents browser/proxy caching of tokens)
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}

	// Verify Content-Type
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Verify response contains a token
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["token"] == "" {
		t.Error("response should contain non-empty 'token' field")
	}
}

func TestSSETokenExchange_NoIdentity_Returns401(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := handleSSEToken(store)

	// No UserIdentity in context
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events/token", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["error"] != "authentication required" {
		t.Errorf("error = %q, want %q", resp["error"], "authentication required")
	}
}

// --- Test 14: SSE auth enforcement ---

func TestSSEAuth_NoToken_Returns401_WhenAuthEnabled(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ok := validateSSEAuth(w, req, store)
	if ok {
		t.Error("validateSSEAuth should return false without token when auth enabled")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "authentication required" {
		t.Errorf("error = %q, want %q", resp["error"], "authentication required")
	}
}

func TestSSEAuth_InvalidToken_Returns401(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token=garbage", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ok := validateSSEAuth(w, req, store)
	if ok {
		t.Error("validateSSEAuth should return false for invalid token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "invalid or expired token" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid or expired token")
	}
}

func TestSSEAuth_ValidOpaqueToken_Passes(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-1", "test-ws")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ok := validateSSEAuth(w, req, store)
	if !ok {
		t.Error("validateSSEAuth should return true for valid opaque token")
	}
}

func TestSSEAuth_ReplayedToken_Returns401(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-1", "test-ws")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")

	// First use — should pass
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req1 = req1.WithContext(ctx)
	w1 := httptest.NewRecorder()
	ok := validateSSEAuth(w1, req1, store)
	if !ok {
		t.Fatal("first use should pass")
	}

	// Second use (replay) — should fail
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req2 = req2.WithContext(ctx)
	w2 := httptest.NewRecorder()
	ok2 := validateSSEAuth(w2, req2, store)
	if ok2 {
		t.Error("replayed token should be rejected")
	}
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for replayed token", w2.Code)
	}
}

func TestSSEAuth_OpenMode_NoTokenRequired(t *testing.T) {
	// nil sseAuth = open mode
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	w := httptest.NewRecorder()

	ok := validateSSEAuth(w, req, nil)
	if !ok {
		t.Error("validateSSEAuth should return true in open mode (nil sseAuth)")
	}
}
