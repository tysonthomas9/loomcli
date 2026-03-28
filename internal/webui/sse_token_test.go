package webui

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestSSETokenStore creates an sseTokenStore with a known secret for testing.
// Unlike newSSETokenStore, it does not start the cleanup goroutine.
func newTestSSETokenStore() *sseTokenStore {
	return &sseTokenStore{
		secret: []byte("test-sse-secret-key-for-testing!"), // 32 bytes
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
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

	userID, err := store.Validate(token)
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
	if _, err := store.Validate(token); err != nil {
		t.Fatalf("first Validate() error = %v, want nil", err)
	}

	// Second use: must fail
	_, err = store.Validate(token)
	if err == nil {
		t.Fatal("second Validate() error = nil, want 'token already used'")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("second Validate() error = %q, want error containing 'already used'", err)
	}
}

// TestSSETokenStore_Expired tests that a token with a past expiration time
// is rejected.
func TestSSETokenStore_Expired(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Manually construct an expired token by setting exp in the past
	payload := sseTokenPayload{
		UserID:      "user-123",
		WorkspaceID: "ws-1",
		Exp:         time.Now().Add(-10 * time.Second).Unix(), // expired 10 seconds ago
		Nonce:       "deadbeef0123456789abcdef01234567",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// Sign with the correct secret
	mac := hmac.New(sha256.New, store.secret)
	mac.Write(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := payloadB64 + "." + sig

	_, err = store.Validate(token)
	if err == nil {
		t.Fatal("Validate() error = nil for expired token, want error")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("Validate() error = %q, want error containing 'expired'", err)
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
			_, err := store.Validate(tt.token)
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

	// Decode, modify, and re-encode the payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}

	var payload sseTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// Change the user ID in the payload
	payload.UserID = "hacked-user"
	modifiedBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal modified payload: %v", err)
	}

	modifiedB64 := base64.RawURLEncoding.EncodeToString(modifiedBytes)
	tampered := modifiedB64 + "." + parts[1]

	_, err = store.Validate(tampered)
	if err == nil {
		t.Fatal("Validate() error = nil for tampered payload, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("Validate() error = %q, want error containing 'invalid signature'", err)
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

	_, err = store.Validate(tampered)
	if err == nil {
		t.Fatal("Validate() error = nil for tampered signature, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("Validate() error = %q, want error containing 'invalid signature'", err)
	}
}

// TestSSETokenStore_EmptyUserID tests that a token with an empty user ID
// is rejected both at generation time and at validation time.
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

	// Manually construct a token with empty user ID to verify Validate also rejects
	payload := sseTokenPayload{
		UserID:      "",
		WorkspaceID: "ws-1",
		Exp:         time.Now().Add(30 * time.Second).Unix(),
		Nonce:       "deadbeef0123456789abcdef01234567",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, store.secret)
	mac.Write(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := payloadB64 + "." + sig

	_, err = store.Validate(token)
	if err == nil {
		t.Fatal("Validate() error = nil for empty user ID, want error")
	}
	if !strings.Contains(err.Error(), "missing user identity") {
		t.Errorf("Validate() error = %q, want error containing 'missing user identity'", err)
	}
}

// TestSSETokenStore_Cleanup tests that the cleanup method removes nonces
// older than sseTokenNonceMaxAge and retains recent ones.
func TestSSETokenStore_Cleanup(t *testing.T) {
	store := newTestSSETokenStore()
	defer store.Stop()

	// Directly populate the used map with old and recent nonces
	store.mu.Lock()
	store.used["old-nonce-1"] = time.Now().Add(-3 * time.Minute)   // older than sseTokenNonceMaxAge (2 min)
	store.used["old-nonce-2"] = time.Now().Add(-10 * time.Minute)  // much older
	store.used["recent-nonce"] = time.Now().Add(-30 * time.Second) // recent, should survive
	store.used["just-now-nonce"] = time.Now()                      // very recent
	store.mu.Unlock()

	// Run cleanup
	store.cleanup()

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, exists := store.used["old-nonce-1"]; exists {
		t.Error("old-nonce-1 should have been cleaned up")
	}
	if _, exists := store.used["old-nonce-2"]; exists {
		t.Error("old-nonce-2 should have been cleaned up")
	}
	if _, exists := store.used["recent-nonce"]; !exists {
		t.Error("recent-nonce should not have been cleaned up")
	}
	if _, exists := store.used["just-now-nonce"]; !exists {
		t.Error("just-now-nonce should not have been cleaned up")
	}
}

// TestSSETokenStore_Stop tests that Stop() is safe to call multiple times
// and closes the done channel.
func TestSSETokenStore_Stop(t *testing.T) {
	store := newTestSSETokenStore()

	// First call should succeed
	store.Stop()

	// The done channel should be closed
	select {
	case <-store.done:
		// success
	default:
		t.Error("done channel should be closed after Stop()")
	}

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

			gotUserID, err := store.Validate(token)
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

	handler := handleSSEToken(store)

	identity := UserIdentity{UserID: "user-123", Email: "test@example.com"}
	ctx := context.WithValue(context.Background(), userIdentityContextKey{}, identity)
	ctx = WithWorkspace(ctx, "ws-1")

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

	handler := handleSSEToken(store)

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

	handler := handleSSEToken(store)

	identity := UserIdentity{UserID: "user-456", Email: "test@example.com"}
	ctx := context.WithValue(context.Background(), userIdentityContextKey{}, identity)
	ctx = WithWorkspace(ctx, "ws-2")

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
	userID, err := store.Validate(token)
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
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	token, err := store.Generate("user-123", "ws-1")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	handler := NewSSEHandlerWithAuth(hub, nil, store)

	ctx, cancel := context.WithCancel(WithWorkspace(context.Background(), "test-ws"))
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
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	handler := NewSSEHandlerWithAuth(hub, nil, store)

	ctx := WithWorkspace(context.Background(), "test-ws")
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
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	store := newTestSSETokenStore()
	defer store.Stop()

	handler := NewSSEHandlerWithAuth(hub, nil, store)

	ctx := WithWorkspace(context.Background(), "test-ws")
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
	hub := NewSSEHub()
	go hub.Run()
	defer hub.Stop()

	handler := NewSSEHandler(hub, nil) // sseAuth is nil (open mode)

	ctx, cancel := context.WithCancel(WithWorkspace(context.Background(), "test-ws"))
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
