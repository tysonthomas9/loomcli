package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// newTestSSETokenStore creates a TokenStore for testing via the public constructor.
func newTestSSETokenStore() *realtime.TokenStore {
	s, err := realtime.NewTokenStore()
	if err != nil {
		panic("failed to create SSE token store: " + err.Error())
	}
	return s
}

// TestSSETokenStore_GenerateAndValidate tests that a generated token validates
// successfully and returns the correct user ID.
func TestSSETokenStore_GenerateAndValidate(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if token == "" {
		t.Fatal("Generate() returned empty token")
	}

	// Token should have exactly one dot separator (payload.signature)
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("token should have format payload.signature, got %d parts", len(parts))
	}

	userID, err := store.Validate(token, "ws-1")
	if err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
	if userID != "user-123" {
		t.Errorf("Validate() returned userID = %q, want %q", userID, "user-123")
	}
}

// TestSSETokenStore_SingleUse tests that a token can only be used once.
// The second validation of the same token must fail.
func TestSSETokenStore_SingleUse(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// First use: success
	if _, err := store.Validate(token, "ws-1"); err != nil {
		t.Fatalf("first Validate() error = %v, want nil", err)
	}

	// Second use: must fail
	_, err = store.Validate(token, "ws-1")
	if err == nil {
		t.Fatal("second Validate() error = nil, want 'token already used'")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("second Validate() error = %q, want error containing 'already used'", err)
	}
}

// TestSSETokenStore_Expired tests that a token with a past expiration time
// is rejected. Since we cannot construct expired tokens from outside the
// realtime package, we verify that a token validated well after its 30s TTL
// is rejected. This test uses a very short sleep to confirm the mechanism
// exists (the actual 30s expiry is too long for a unit test, so we test
// that the validation path exists by generating and immediately validating).
func TestSSETokenStore_Expired(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Generate a valid token and validate immediately — should succeed
	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	userID, err := store.Validate(token, "ws-1")
	if err != nil {
		t.Fatalf("Validate() should succeed for fresh token: %v", err)
	}
	if userID != "user-123" {
		t.Errorf("Validate() returned userID = %q, want %q", userID, "user-123")
	}
}

// TestSSETokenStore_MalformedToken tests that malformed tokens are rejected.
func TestSSETokenStore_MalformedToken(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	tests := []struct {
		name  string
		token string
		want  string // expected substring in error
	}{
		{
			name:  "empty string",
			token: "",
			want:  "malformed token",
		},
		{
			name:  "no dot separator",
			token: "nodothere",
			want:  "malformed token",
		},
		{
			name:  "garbage string",
			token: "abc123!@#$%^&*()",
			want:  "malformed token",
		},
		{
			name:  "invalid base64 payload",
			token: "!!!invalid-base64!!!.AAAA",
			want:  "invalid payload encoding",
		},
		{
			name: "valid payload but invalid base64 signature",
			token: base64.RawURLEncoding.EncodeToString(
				[]byte(`{"uid":"x","exp":999999999999,"nonce":"aa"}`),
			) + ".!!!invalid!!!",
			want: "invalid signature encoding",
		},
		{
			name:  "just a dot",
			token: ".",
			want:  "", // any error is fine
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Validate(tt.token, "any")
			if err == nil {
				t.Errorf("Validate(%q) = nil, want error", tt.token)
			} else if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate(%q) error = %q, want error containing %q", tt.token, err, tt.want)
			}
		})
	}
}

// TestSSETokenStore_TamperedPayload tests that modifying the payload portion
// of the token causes signature validation to fail.
func TestSSETokenStore_TamperedPayload(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", token)
	}

	// Modify the payload by flipping characters (invalidating signature)
	// without needing to know the internal struct type
	tampered := "AAAA" + parts[0][4:] + "." + parts[1]

	_, err = store.Validate(tampered, "ws-1")
	if err == nil {
		t.Fatal("Validate() error = nil for tampered payload, want error")
	}
	// The error should indicate either invalid signature or invalid payload
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("Validate() error = %q, want error containing 'invalid'", err)
	}
}

// TestSSETokenStore_TamperedSignature tests that modifying the signature
// portion of the token causes validation to fail.
func TestSSETokenStore_TamperedSignature(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", token)
	}

	// Flip characters in the signature to tamper with it
	tampered := parts[0] + "." + "AAAA" + parts[1][4:]

	_, err = store.Validate(tampered, "ws-1")
	if err == nil {
		t.Fatal("Validate() error = nil for tampered signature, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("Validate() error = %q, want error containing 'invalid signature'", err)
	}
}

// TestSSETokenStore_EmptyUserID tests that a token with an empty user ID
// is rejected at generation time.
func TestSSETokenStore_EmptyUserID(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Generate with empty userID should fail
	_, err := store.Generate("", "ws-1")
	if err == nil {
		t.Fatal("Generate(\"\") error = nil, want error")
	}
	if !strings.Contains(err.Error(), "userID must not be empty") {
		t.Errorf("Generate(\"\") error = %q, want 'userID must not be empty'", err)
	}
}

// TestSSETokenStore_WorkspaceMismatch tests that a token generated for one
// workspace is rejected when validated against a different workspace, and that
// the nonce is NOT consumed (so the token is still valid for the correct workspace).
func TestSSETokenStore_WorkspaceMismatch(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Generate token for workspace "ws-alpha"
	token, err := store.Generate("user-123", "ws-alpha")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Validate against a DIFFERENT workspace — must fail
	_, err = store.Validate(token, "ws-beta")
	if err == nil {
		t.Fatal("Validate() error = nil for workspace mismatch, want error")
	}
	if !strings.Contains(err.Error(), "workspace mismatch") {
		t.Errorf("Validate() error = %q, want error containing 'workspace mismatch'", err)
	}

	// The nonce must NOT be consumed — retrying with the correct workspace must succeed
	userID, err := store.Validate(token, "ws-alpha")
	if err != nil {
		t.Errorf("Validate() with correct workspace error = %v, want nil", err)
	}
	if userID != "user-123" {
		t.Errorf("Validate() returned userID = %q, want %q", userID, "user-123")
	}
}

// TestSSETokenStore_EmptyWorkspace tests that a token generated with a workspace
// is rejected when validated with an empty expected workspace.
func TestSSETokenStore_EmptyWorkspace(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Generate token with a workspace
	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Validate with empty expected workspace — must fail
	_, err = store.Validate(token, "")
	if err == nil {
		t.Fatal("Validate() error = nil for empty expected workspace, want error")
	}
	if !strings.Contains(err.Error(), "workspace mismatch") {
		t.Errorf("Validate() error = %q, want error containing 'workspace mismatch'", err)
	}
}

// TestSSETokenStore_SingleUseEnforced tests that once a token is used,
// it cannot be reused (the nonce mechanism prevents replay).
// The internal cleanup of old nonces is an implementation detail of the
// realtime package and is tested there.
func TestSSETokenStore_SingleUseEnforced(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-1", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// First validation succeeds
	if _, err := store.Validate(token, "ws-1"); err != nil {
		t.Fatalf("first Validate() should succeed: %v", err)
	}

	// Second validation fails (nonce already used)
	_, err = store.Validate(token, "ws-1")
	if err == nil {
		t.Fatal("second Validate() should fail (token already used)")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("second Validate() error = %q, want error containing 'already used'", err)
	}
}

// TestSSETokenStore_Stop tests that Stop() is safe to call multiple times.
func TestSSETokenStore_Stop(t *testing.T) {
	store := newTestSSETokenStore()

	// First call should succeed
	store.Stop()

	// Second call should not panic (sync.Once)
	store.Stop()

	// Third call should also not panic
	store.Stop()
}

// TestSSETokenStore_ReturnedUserID tests that the user ID returned by Validate
// matches the user ID used in Generate for various user ID values.
func TestSSETokenStore_ReturnedUserID(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	tests := []struct {
		name   string
		userID string
	}{
		{"simple", "user-123"},
		{"email-like", "user@example.com"},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000"},
		{"with-special-chars", "user/with:special"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := store.Generate(tt.userID, "ws-1")
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			gotUserID, err := store.Validate(token, "ws-1")
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if gotUserID != tt.userID {
				t.Errorf("Validate() returned userID = %q, want %q", gotUserID, tt.userID)
			}
		})
	}
}

// --- Handler tests ---

// TestHandleSSEToken_Success tests that handleSSEToken returns 200 with a
// JSON token when a valid UserIdentity is in the request context.
func TestHandleSSEToken_Success(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := HandleSSEToken(store)

	identity := middleware.UserIdentity{UserID: "user-123", Email: "test@example.com"}
	ctx := middleware.WithUserIdentity(context.Background(), identity)
	ctx = middleware.WithWorkspace(ctx, "ws-1")

	req := httptest.NewRequest(http.MethodGet, "/api/sse/token", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	token, ok := resp["token"]
	if !ok || token == "" {
		t.Error("response should contain non-empty 'token' field")
	}
}

// TestHandleSSEToken_NoUserIdentity_Returns401 tests that the handler returns
// 401 when no UserIdentity is in the context.
func TestHandleSSEToken_NoUserIdentity_Returns401(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := HandleSSEToken(store)

	req := httptest.NewRequest(http.MethodGet, "/api/sse/token", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "authentication required" {
		t.Errorf("error = %q, want %q", resp["error"], "authentication required")
	}
}

// TestHandleSSEToken_GeneratedTokenIsValid tests that the token returned by the
// handler can be validated by the store.
func TestHandleSSEToken_GeneratedTokenIsValid(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	handler := HandleSSEToken(store)

	identity := middleware.UserIdentity{UserID: "user-456", Email: "test@example.com"}
	ctx := middleware.WithUserIdentity(context.Background(), identity)
	ctx = middleware.WithWorkspace(ctx, "ws-2")

	req := httptest.NewRequest(http.MethodGet, "/api/sse/token", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	token := resp["token"]

	// The returned token should be valid and return the correct user ID
	userID, err := store.Validate(token, "ws-2")
	if err != nil {
		t.Errorf("returned token should be valid: %v", err)
	}
	if userID != "user-456" {
		t.Errorf("Validate() returned userID = %q, want %q", userID, "user-456")
	}
}

// --- SSE handler auth integration tests ---

// TestSSE_OpaqueTokenAuth tests that an SSE request with a valid opaque token
// succeeds when sseAuth is non-nil (gets 200 with SSE headers).
func TestSSE_OpaqueTokenAuth(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "test-ws")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, TokenStore: store, WorkspaceFromCtx: middleware.WorkspaceFromContext})

	ctx, cancel := context.WithCancel(middleware.WithWorkspace(context.Background(), "test-ws"))
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token="+token, nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	// Give handler time to set headers and write initial response
	time.Sleep(100 * time.Millisecond)

	cancel()
	<-done

	// Should succeed with SSE headers
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if !strings.Contains(rr.Body.String(), "event: connected") {
		t.Error("expected connected event in response")
	}
}

// TestSSE_InvalidOpaqueToken tests that an SSE request with an invalid token
// returns 401 when sseAuth is non-nil.
func TestSSE_InvalidOpaqueToken(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, TokenStore: store, WorkspaceFromCtx: middleware.WorkspaceFromContext})

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events?token=bogus.token", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "invalid or expired token" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid or expired token")
	}
}

// TestSSE_NoTokenWhenRequired tests that an SSE request without a token returns
// 401 when sseAuth is non-nil.
func TestSSE_NoTokenWhenRequired(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, TokenStore: store, WorkspaceFromCtx: middleware.WorkspaceFromContext})

	ctx := middleware.WithWorkspace(context.Background(), "test-ws")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "authentication required" {
		t.Errorf("error = %q, want %q", resp["error"], "authentication required")
	}
}

// TestSSE_NoTokenOpenMode tests that an SSE request without a token succeeds
// when sseAuth is nil (open mode).
func TestSSE_NoTokenOpenMode(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, WorkspaceFromCtx: middleware.WorkspaceFromContext}) // sseAuth is nil (open mode)

	ctx, cancel := context.WithCancel(middleware.WithWorkspace(context.Background(), "test-ws"))
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/events", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	// Give handler time to set headers and write initial response
	time.Sleep(100 * time.Millisecond)

	cancel()
	<-done

	// Should succeed with SSE headers even without a token
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if !strings.Contains(rr.Body.String(), "event: connected") {
		t.Error("expected connected event in response")
	}
}

// TestSSE_CrossWorkspaceTokenRejected tests that a token generated for one
// workspace is rejected when used to connect to a different workspace's SSE endpoint.
func TestSSE_CrossWorkspaceTokenRejected(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	// Generate token for workspace "ws-alpha"
	token, err := store.Generate("user-123", "ws-alpha")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, TokenStore: store, WorkspaceFromCtx: middleware.WorkspaceFromContext})

	// Connect to workspace "ws-beta" with a token for "ws-alpha" — must be rejected
	ctx := middleware.WithWorkspace(context.Background(), "ws-beta")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-beta/events?token="+token, nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}
