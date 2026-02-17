package webui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestTerminalAuth creates a terminalAuth with a known secret for testing.
// Unlike newTerminalAuth, it does not start the cleanup goroutine.
func newTestTerminalAuth() *terminalAuth {
	return &terminalAuth{
		secret: []byte("test-secret-key-for-unit-tests!!"), // 32 bytes
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
}

// TestGenerateAndValidateToken_Success tests that a generated token validates
// successfully when the correct session is provided.
func TestGenerateAndValidateToken_Success(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "test-session"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	if token == "" {
		t.Fatal("GenerateToken() returned empty token")
	}

	// Token should have exactly one dot separator (payload.signature)
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("token should have format payload.signature, got %d parts", len(parts))
	}

	if err := ta.ValidateToken(token, session); err != nil {
		t.Errorf("ValidateToken() error = %v, want nil", err)
	}
}

// TestValidateToken_WrongSession tests that validation fails when the session
// does not match the one used to generate the token.
func TestValidateToken_WrongSession(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	token, err := ta.GenerateToken("session-a")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	err = ta.ValidateToken(token, "session-b")
	if err == nil {
		t.Fatal("ValidateToken() error = nil, want session mismatch error")
	}
	if !strings.Contains(err.Error(), "session mismatch") {
		t.Errorf("ValidateToken() error = %q, want error containing 'session mismatch'", err)
	}
}

// TestValidateToken_Reuse tests that a token can only be used once.
// The second validation of the same token must fail.
func TestValidateToken_Reuse(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "reuse-test"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// First use: success
	if err := ta.ValidateToken(token, session); err != nil {
		t.Fatalf("first ValidateToken() error = %v, want nil", err)
	}

	// Second use: must fail
	err = ta.ValidateToken(token, session)
	if err == nil {
		t.Fatal("second ValidateToken() error = nil, want 'token already used'")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("second ValidateToken() error = %q, want error containing 'already used'", err)
	}
}

// TestValidateToken_TamperedSignature tests that modifying the signature
// portion of the token causes validation to fail.
func TestValidateToken_TamperedSignature(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "tamper-sig"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", token)
	}

	// Flip a character in the signature to tamper with it
	tampered := parts[0] + "." + "AAAA" + parts[1][4:]

	err = ta.ValidateToken(tampered, session)
	if err == nil {
		t.Fatal("ValidateToken() error = nil for tampered signature, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("ValidateToken() error = %q, want error containing 'invalid signature'", err)
	}
}

// TestValidateToken_TamperedPayload tests that modifying the payload
// portion of the token causes signature validation to fail.
func TestValidateToken_TamperedPayload(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "tamper-payload"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
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

	var payload terminalTokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	// Change the session in the payload
	payload.Session = "hacked-session"
	modifiedBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal modified payload: %v", err)
	}

	modifiedB64 := base64.RawURLEncoding.EncodeToString(modifiedBytes)
	tampered := modifiedB64 + "." + parts[1]

	// Validate with the original session - signature won't match
	err = ta.ValidateToken(tampered, session)
	if err == nil {
		t.Fatal("ValidateToken() error = nil for tampered payload, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("ValidateToken() error = %q, want error containing 'invalid signature'", err)
	}

	// Validate with the hacked session - signature still won't match
	err = ta.ValidateToken(tampered, "hacked-session")
	if err == nil {
		t.Fatal("ValidateToken() error = nil for tampered payload with matching session, want error")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("ValidateToken() error = %q, want error containing 'invalid signature'", err)
	}
}

// TestValidateToken_Expired tests that a token with a past expiration time
// is rejected.
func TestValidateToken_Expired(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "expired-test"

	// Manually construct an expired token by setting exp in the past
	payload := terminalTokenPayload{
		Session: session,
		Exp:     time.Now().Add(-10 * time.Second).Unix(), // expired 10 seconds ago
		Nonce:   "deadbeef0123456789abcdef01234567",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// Sign with the correct secret
	mac := hmac.New(sha256.New, ta.secret)
	mac.Write(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := payloadB64 + "." + sig

	err = ta.ValidateToken(token, session)
	if err == nil {
		t.Fatal("ValidateToken() error = nil for expired token, want error")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("ValidateToken() error = %q, want error containing 'expired'", err)
	}
}

// TestValidateToken_Malformed tests that malformed tokens are rejected.
func TestValidateToken_Malformed(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

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
			name:  "invalid base64 payload",
			token: "!!!invalid-base64!!!.AAAA",
			want:  "invalid payload encoding",
		},
		{
			name:  "valid payload but invalid base64 signature",
			token: base64.RawURLEncoding.EncodeToString([]byte(`{"session":"x","exp":999999999999,"nonce":"aa"}`)) + ".!!!invalid!!!",
			want:  "invalid signature encoding",
		},
		{
			name:  "just a dot",
			token: ".",
			want:  "", // any error is fine (empty payload)
		},
		{
			name:  "multiple dots",
			token: "a.b.c",
			want:  "", // SplitN with n=2 handles this, but signature will be invalid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ta.ValidateToken(tt.token, "any-session")
			if err == nil {
				t.Errorf("ValidateToken(%q) = nil, want error", tt.token)
			} else if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ValidateToken(%q) error = %q, want error containing %q", tt.token, err, tt.want)
			}
		})
	}
}

// TestCleanup_RemovesOldNonces tests that the cleanup method removes nonces
// older than terminalNonceMaxAge and retains recent ones.
func TestCleanup_RemovesOldNonces(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	// Directly populate the used map with old and recent nonces
	ta.mu.Lock()
	ta.used["old-nonce-1"] = time.Now().Add(-3 * time.Minute)   // older than terminalNonceMaxAge (2 min)
	ta.used["old-nonce-2"] = time.Now().Add(-10 * time.Minute)  // much older
	ta.used["recent-nonce"] = time.Now().Add(-30 * time.Second) // recent, should survive
	ta.used["just-now-nonce"] = time.Now()                      // very recent
	ta.mu.Unlock()

	// Run cleanup
	ta.cleanup()

	ta.mu.Lock()
	defer ta.mu.Unlock()

	if _, exists := ta.used["old-nonce-1"]; exists {
		t.Error("old-nonce-1 should have been cleaned up")
	}
	if _, exists := ta.used["old-nonce-2"]; exists {
		t.Error("old-nonce-2 should have been cleaned up")
	}
	if _, exists := ta.used["recent-nonce"]; !exists {
		t.Error("recent-nonce should not have been cleaned up")
	}
	if _, exists := ta.used["just-now-nonce"]; !exists {
		t.Error("just-now-nonce should not have been cleaned up")
	}
}

// TestCleanup_EmptyMap tests that cleanup does not panic on an empty used map.
func TestCleanup_EmptyMap(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	// Should not panic
	ta.cleanup()

	ta.mu.Lock()
	defer ta.mu.Unlock()
	if len(ta.used) != 0 {
		t.Errorf("used map should be empty, got %d entries", len(ta.used))
	}
}

// TestConcurrentGenerateToken tests that GenerateToken is safe for concurrent use.
func TestConcurrentGenerateToken(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	tokens := make([]string, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			token, err := ta.GenerateToken("concurrent-session")
			tokens[idx] = token
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: GenerateToken() error = %v", i, err)
		}
	}

	// All tokens should be unique (different nonces)
	seen := make(map[string]bool, goroutines)
	for i, tok := range tokens {
		if tok == "" {
			continue // already reported error above
		}
		if seen[tok] {
			t.Errorf("goroutine %d: duplicate token %q", i, tok)
		}
		seen[tok] = true
	}
}

// TestConcurrentValidateToken tests that concurrent calls to ValidateToken
// on the same token result in exactly one success (single-use enforcement).
func TestConcurrentValidateToken(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "concurrent-validate"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = ta.ValidateToken(token, session)
		}(i)
	}

	wg.Wait()

	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		}
	}

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful validation, got %d", successCount)
	}
}

// TestNewTerminalAuth_StartsCleanly tests that newTerminalAuth returns a
// functioning instance with a random secret.
func TestNewTerminalAuth_StartsCleanly(t *testing.T) {
	ta, err := newTerminalAuth()
	if err != nil {
		t.Fatalf("newTerminalAuth() error = %v", err)
	}
	defer ta.Stop()

	if len(ta.secret) != terminalSecretBytes {
		t.Errorf("secret length = %d, want %d", len(ta.secret), terminalSecretBytes)
	}

	if ta.used == nil {
		t.Error("used map should be initialized")
	}

	// Verify it works end-to-end
	token, err := ta.GenerateToken("init-test")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if err := ta.ValidateToken(token, "init-test"); err != nil {
		t.Errorf("ValidateToken() error = %v", err)
	}
}

// TestNewTerminalAuth_UniqueSecrets tests that two instances have different secrets.
func TestNewTerminalAuth_UniqueSecrets(t *testing.T) {
	ta1, err := newTerminalAuth()
	if err != nil {
		t.Fatalf("first newTerminalAuth() error = %v", err)
	}
	defer ta1.Stop()

	ta2, err := newTerminalAuth()
	if err != nil {
		t.Fatalf("second newTerminalAuth() error = %v", err)
	}
	defer ta2.Stop()

	if string(ta1.secret) == string(ta2.secret) {
		t.Error("two terminalAuth instances should have different secrets")
	}
}

// TestGenerateToken_DifferentTokensPerCall tests that each call to
// GenerateToken produces a unique token (unique nonce).
func TestGenerateToken_DifferentTokensPerCall(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	session := "unique-test"
	tok1, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("first GenerateToken() error = %v", err)
	}

	tok2, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("second GenerateToken() error = %v", err)
	}

	if tok1 == tok2 {
		t.Error("two tokens for same session should be different (different nonces)")
	}
}

// --- Handler tests ---

// TestHandleTerminalToken_ValidSession tests that handleTerminalToken returns
// 200 with a JSON token for a valid session parameter.
func TestHandleTerminalToken_ValidSession(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session=my-session", nil)
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

	// The returned token should be valid for the same session
	if err := ta.ValidateToken(token, "my-session"); err != nil {
		t.Errorf("returned token should be valid: %v", err)
	}
}

// TestHandleTerminalToken_EmptySession tests that an empty session parameter
// returns 400.
func TestHandleTerminalToken_EmptySession(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleTerminalToken(ta)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/token", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["error"] != "invalid session" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid session")
	}
}

// TestHandleTerminalToken_InvalidSessionChars tests that sessions with invalid
// characters return 400.
func TestHandleTerminalToken_InvalidSessionChars(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleTerminalToken(ta)

	tests := []struct {
		name    string
		session string
	}{
		{"with space", "bad session"},
		{"with slash", "bad/session"},
		{"with dot", "bad.session"},
		{"with semicolon", "bad;session"},
		{"with at sign", "bad@session"},
		{"with special", "bad!#$%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := url.QueryEscape(tt.session)
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session="+encoded, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d for session %q", w.Code, http.StatusBadRequest, tt.session)
			}
		})
	}
}

// TestHandleTerminalWS_AuthNoToken tests that when auth is configured on
// handleTerminalWS, a request without a token returns 401.
func TestHandleTerminalWS_AuthNoToken(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	// Pass auth but nil manager. The auth check happens after the nil-manager
	// check and after session validation, so we need a non-nil manager.
	// However, looking at the code: nil manager check -> session check -> auth check.
	// With nil manager, we get 503 before auth is checked.
	// We need to provide a real manager or restructure. Let's try with a real manager.
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, ta, nil)

	// Request with valid session but no token
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=auth-test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["success"] != false {
		t.Error("expected success to be false")
	}

	if resp["error"] != "terminal authentication failed" {
		t.Errorf("error = %q, want %q", resp["error"], "terminal authentication failed")
	}
}

// TestHandleTerminalWS_AuthInvalidToken tests that a bad token returns 401.
func TestHandleTerminalWS_AuthInvalidToken(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, ta, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=auth-test&token=bogus.token", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// TestHandleTerminalWS_AuthValidToken tests that a valid token passes the auth
// check. The request will fail at WebSocket upgrade (no upgrade headers in
// httptest), but it should NOT return 401.
func TestHandleTerminalWS_AuthValidToken(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, ta, nil)

	session := "auth-valid"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session="+session+"&token="+token, nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should NOT be 401 (auth passed). It will likely fail at WebSocket upgrade
	// since httptest.NewRequest doesn't include upgrade headers, but the status
	// should not be 401 or 400.
	if w.Code == http.StatusUnauthorized {
		t.Errorf("status = %d; valid token should pass auth check", w.Code)
	}
	if w.Code == http.StatusBadRequest {
		// Check if it's a session validation error vs WebSocket error
		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
			errMsg, _ := resp["error"].(string)
			if errMsg == "terminal authentication failed" || errMsg == "invalid session" {
				t.Errorf("valid token + valid session should not produce auth/session error, got %q", errMsg)
			}
		}
	}
}

// TestHandleTerminalWS_AuthNilPassesThrough tests that when auth is nil
// (not configured), the handler does not require a token.
func TestHandleTerminalWS_AuthNilPassesThrough(t *testing.T) {
	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	// auth=nil means no token auth required
	handler := handleTerminalWS(manager, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?session=no-auth", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should not be 401 since auth is disabled
	if w.Code == http.StatusUnauthorized {
		t.Errorf("status = %d; nil auth should not require token", w.Code)
	}
}

// TestHandleTerminalWS_AuthReusedTokenFails tests that reusing a token for
// the WebSocket endpoint returns 401 on the second attempt.
func TestHandleTerminalWS_AuthReusedTokenFails(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	manager, err := NewTerminalManager("", "", 0)
	if err == ErrTmuxNotFound {
		t.Skip("tmux not installed, skipping test")
	}
	if err != nil {
		t.Fatalf("failed to create terminal manager: %v", err)
	}
	defer manager.Shutdown()

	handler := handleTerminalWS(manager, ta, nil)

	session := "reuse-ws"
	token, err := ta.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	url := "/api/terminal/ws?session=" + session + "&token=" + token

	// First request: uses the token (will fail at WS upgrade, but auth passes)
	req1 := httptest.NewRequest(http.MethodGet, url, nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if w1.Code == http.StatusUnauthorized {
		t.Fatalf("first request should pass auth, got 401")
	}

	// Second request: same token should be rejected as already used
	req2 := httptest.NewRequest(http.MethodGet, url, nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("second request status = %d, want %d (token already used)", w2.Code, http.StatusUnauthorized)
	}
}

// TestValidateToken_WrongSecret tests that a token signed by a different
// terminalAuth instance (different secret) is rejected.
func TestValidateToken_WrongSecret(t *testing.T) {
	ta1 := newTestTerminalAuth()
	defer ta1.Stop()

	ta2 := &terminalAuth{
		secret: []byte("different-secret-key-different!!!"), // 32 bytes, different
		used:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	defer ta2.Stop()

	session := "cross-secret"
	token, err := ta1.GenerateToken(session)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	err = ta2.ValidateToken(token, session)
	if err == nil {
		t.Fatal("ValidateToken() with wrong secret should fail")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("error = %q, want 'invalid signature'", err)
	}
}

// TestStop_ClosesChannel tests that Stop() closes the done channel,
// which would signal the cleanup goroutine to exit.
func TestStop_ClosesChannel(t *testing.T) {
	ta := newTestTerminalAuth()
	ta.Stop()

	// The done channel should be closed
	select {
	case <-ta.done:
		// success
	default:
		t.Error("done channel should be closed after Stop()")
	}
}

// TestHandleTerminalToken_ValidSessionNames tests that various valid session
// names work correctly with the token endpoint.
func TestHandleTerminalToken_ValidSessionNames(t *testing.T) {
	ta := newTestTerminalAuth()
	defer ta.Stop()

	handler := handleTerminalToken(ta)

	tests := []struct {
		name    string
		session string
	}{
		{"alphanumeric", "test123"},
		{"with hyphen", "test-session"},
		{"with underscore", "test_session"},
		{"mixed case", "Test-Session_123"},
		{"single char", "a"},
		{"numbers only", "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/terminal/token?session="+tt.session, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d for session %q", w.Code, http.StatusOK, tt.session)
			}

			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}

			if resp["token"] == "" {
				t.Error("expected non-empty token")
			}
		})
	}
}
