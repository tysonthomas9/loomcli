package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// newTestSSETokenStore creates a TokenStore for testing via the public constructor.
func newTestSSETokenStore() *realtime.TokenStore {
	s, err := realtime.NewTokenStore()
	if err != nil {
		panic("failed to create SSE token store: " + err.Error())
	}
	return s
}

// --- Test 13: Token exchange Cache-Control: no-store ---

func TestSSETokenExchange_CacheControlNoStore(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := subscription.HandleSSEToken(store)

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

	handler := subscription.HandleSSEToken(store)

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
// validateSSEAuth is now internal to realtime.Handler. These tests exercise
// the same behavior through the Handler's ServeHTTP method.

func TestSSEAuth_NoToken_Returns401_WhenAuthEnabled(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:              hub,
		TokenStore:       store,
		WorkspaceFromCtx: middleware.WorkspaceFromContext,
	})

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

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

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:              hub,
		TokenStore:       store,
		WorkspaceFromCtx: middleware.WorkspaceFromContext,
	})

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token=garbage", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

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

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	token, err := store.Generate("user-1", "test-ws")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:              hub,
		TokenStore:       store,
		WorkspaceFromCtx: middleware.WorkspaceFromContext,
	})

	ctx, cancel := context.WithCancel(middleware.WithWorkspace(context.Background(), "test-ws"))
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Give the handler time to start streaming
	cancel()
	<-done

	// Should succeed (200 with SSE content type)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestSSEAuth_ReplayedToken_Returns401(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	token, err := store.Generate("user-1", "test-ws")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:              hub,
		TokenStore:       store,
		WorkspaceFromCtx: middleware.WorkspaceFromContext,
	})

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")

	// First use — consume the token through the handler
	ctx1, cancel1 := context.WithCancel(ctx)
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req1 = req1.WithContext(ctx1)
	w1 := httptest.NewRecorder()

	done1 := make(chan struct{})
	go func() {
		handler.ServeHTTP(w1, req1)
		close(done1)
	}()
	cancel1()
	<-done1

	// Second use (replay) — should fail
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req2 = req2.WithContext(ctx)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for replayed token", w2.Code)
	}
}

func TestSSEAuth_OpenMode_NoTokenRequired(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	// nil TokenStore = open mode
	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:              hub,
		WorkspaceFromCtx: middleware.WorkspaceFromContext,
	})

	ctx, cancel := context.WithCancel(middleware.WithWorkspace(context.Background(), "test-ws"))
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	cancel()
	<-done

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}
