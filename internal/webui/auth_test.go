package webui

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestNewAuthMiddleware_Disabled verifies that when auth is disabled the
// middleware is a passthrough that never blocks requests.
func TestNewAuthMiddleware_Disabled(t *testing.T) {
	config := AuthConfig{
		Enabled: false,
		APIKey:  "secret-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	// No Authorization header at all
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "OK" {
		t.Errorf("body = %q, want %q", w.Body.String(), "OK")
	}
}

// TestNewAuthMiddleware_PublicRoutes verifies that public routes pass through
// without any authentication when auth is enabled.
func TestNewAuthMiddleware_PublicRoutes(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "test-api-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"health endpoint", http.MethodGet, "/health"},
		{"api health endpoint", http.MethodGet, "/api/health"},
		{"auth token endpoint", http.MethodGet, "/api/auth/token"},
		{"terminal ws (own auth)", http.MethodGet, "/api/terminal/ws"},
		{"frontend root", http.MethodGet, "/"},
		{"frontend asset", http.MethodGet, "/assets/main.js"},
		{"frontend SPA route", http.MethodGet, "/issues/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}
			if w.Body.String() != "OK" {
				t.Errorf("body = %q, want %q", w.Body.String(), "OK")
			}
		})
	}
}

// TestNewAuthMiddleware_RejectsProtectedRoutesWithoutAuth verifies that
// protected API routes return 401 when no authentication is provided.
func TestNewAuthMiddleware_RejectsProtectedRoutesWithoutAuth(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "test-api-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"GET api issues", http.MethodGet, "/api/issues"},
		{"POST api issues", http.MethodPost, "/api/issues"},
		{"GET api stats", http.MethodGet, "/api/stats"},
		{"PATCH api issue", http.MethodPatch, "/api/issues/123"},
		{"DELETE api issue", http.MethodDelete, "/api/issues/123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
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
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
		})
	}
}

// TestNewAuthMiddleware_RejectsProtectedRoutesWithWrongKey verifies that
// protected routes return 401 with "invalid token" when the wrong key is used.
func TestNewAuthMiddleware_RejectsProtectedRoutesWithWrongKey(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "correct-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["error"] != "invalid token" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid token")
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// TestNewAuthMiddleware_AllowsCorrectBearerToken verifies that protected
// routes are accessible with the correct Bearer token in the Authorization header.
func TestNewAuthMiddleware_AllowsCorrectBearerToken(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "correct-api-key-12345",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer correct-api-key-12345")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "OK" {
		t.Errorf("body = %q, want %q", w.Body.String(), "OK")
	}
}

// TestNewAuthMiddleware_AllowsCorrectTokenInQueryParam verifies that requests
// with the correct token in the "token" query parameter are allowed (for
// SSE connections that can't set custom headers).
func TestNewAuthMiddleware_AllowsCorrectTokenInQueryParam(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "ws-api-key-67890",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/events?token=ws-api-key-67890", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "OK" {
		t.Errorf("body = %q, want %q", w.Body.String(), "OK")
	}
}

// TestNewAuthMiddleware_OptionsPassthrough verifies that OPTIONS requests
// (CORS preflight) pass through without authentication.
func TestNewAuthMiddleware_OptionsPassthrough(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "test-api-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/issues", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "OK" {
		t.Errorf("body = %q, want %q", w.Body.String(), "OK")
	}
}

// TestIsPublicRoute verifies classification of various paths as public or protected.
func TestIsPublicRoute(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		// Public GET routes
		{"GET /health", http.MethodGet, "/health", true},
		{"GET /api/health", http.MethodGet, "/api/health", true},
		{"GET /api/auth/token", http.MethodGet, "/api/auth/token", true},
		{"GET / (frontend root)", http.MethodGet, "/", true},
		{"GET /assets/main.js (frontend asset)", http.MethodGet, "/assets/main.js", true},
		{"GET /issues/123 (SPA route)", http.MethodGet, "/issues/123", true},
		{"GET /favicon.ico", http.MethodGet, "/favicon.ico", true},

		// Terminal WS is public (uses its own one-time token auth in the handler)
		{"GET /api/terminal/ws", http.MethodGet, "/api/terminal/ws", true},

		// Protected API routes (require auth)
		{"GET /api/issues", http.MethodGet, "/api/issues", false},
		{"GET /api/stats", http.MethodGet, "/api/stats", false},
		{"GET /api/issues/123", http.MethodGet, "/api/issues/123", false},
		{"GET /api/events", http.MethodGet, "/api/events", false},

		// Non-GET methods on public paths should not be public
		{"POST /health", http.MethodPost, "/health", false},
		{"POST /api/health", http.MethodPost, "/api/health", false},
		{"POST /api/auth/token", http.MethodPost, "/api/auth/token", false},
		{"PUT /health", http.MethodPut, "/health", false},
		{"DELETE /api/health", http.MethodDelete, "/api/health", false},
		{"POST /", http.MethodPost, "/", false},

		// Non-GET on protected routes
		{"POST /api/issues", http.MethodPost, "/api/issues", false},
		{"PATCH /api/issues/123", http.MethodPatch, "/api/issues/123", false},
		{"DELETE /api/issues/123", http.MethodDelete, "/api/issues/123", false},

		// Fleet routes are public (they use their own auth: API key for register, JWT for claim/heartbeat)
		{"POST /api/fleet/register", http.MethodPost, "/api/fleet/register", true},
		{"POST /api/fleet/claim", http.MethodPost, "/api/fleet/claim", true},
		{"POST /api/fleet/heartbeat", http.MethodPost, "/api/fleet/heartbeat", true},
		{"POST /api/fleet/done/worker-1", http.MethodPost, "/api/fleet/done/worker-1", true},
		{"GET /api/fleet/register", http.MethodGet, "/api/fleet/register", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPublicRoute(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("isPublicRoute(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestExtractToken_AuthorizationHeader verifies that the token is correctly
// extracted from a properly formed Authorization: Bearer header.
func TestExtractToken_AuthorizationHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer my-secret-token")

	token := extractToken(req)
	if token != "my-secret-token" {
		t.Errorf("extractToken() = %q, want %q", token, "my-secret-token")
	}
}

// TestExtractToken_QueryParameter verifies that the token is extracted from
// the "token" query parameter when no Authorization header is present.
func TestExtractToken_QueryParameter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/terminal/ws?token=query-token-value", nil)

	token := extractToken(req)
	if token != "query-token-value" {
		t.Errorf("extractToken() = %q, want %q", token, "query-token-value")
	}
}

// TestExtractToken_MalformedAuthorizationHeader verifies that a malformed
// Authorization header (not starting with "Bearer ") returns empty string.
func TestExtractToken_MalformedAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"Basic auth", "Basic dXNlcjpwYXNz"},
		{"no prefix", "my-token-without-bearer"},
		{"lowercase bearer", "bearer my-token"},
		{"empty bearer value", "Bearer"},
		{"just space", " "},
		{"token scheme", "Token abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
			req.Header.Set("Authorization", tt.value)

			token := extractToken(req)
			if token != "" {
				t.Errorf("extractToken() = %q, want empty string", token)
			}
		})
	}
}

// TestExtractToken_NoAuth verifies that extractToken returns an empty string
// when neither an Authorization header nor a token query parameter is present.
func TestExtractToken_NoAuth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)

	token := extractToken(req)
	if token != "" {
		t.Errorf("extractToken() = %q, want empty string", token)
	}
}

// TestExtractToken_HeaderTakesPrecedenceOverQuery verifies that the
// Authorization header is preferred over the query parameter.
func TestExtractToken_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/issues?token=query-token", nil)
	req.Header.Set("Authorization", "Bearer header-token")

	token := extractToken(req)
	if token != "header-token" {
		t.Errorf("extractToken() = %q, want %q", token, "header-token")
	}
}

// TestGenerateAPIKey_Length verifies that GenerateAPIKey produces a hex string
// of the expected length (64 hex characters for 32 bytes).
func TestGenerateAPIKey_Length(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}

	// 32 bytes = 64 hex characters
	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}

	// Verify it's valid hex
	_, err = hex.DecodeString(key)
	if err != nil {
		t.Errorf("key is not valid hex: %v", err)
	}
}

// TestGenerateAPIKey_Unique verifies that successive calls to GenerateAPIKey
// produce different keys (tests randomness).
func TestGenerateAPIKey_Unique(t *testing.T) {
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey() iteration %d error = %v", i, err)
		}
		if keys[key] {
			t.Fatalf("GenerateAPIKey() produced duplicate key on iteration %d: %q", i, key)
		}
		keys[key] = true
	}
}

// TestLoadOrCreateAPIKey_CreatesNewFile verifies that LoadOrCreateAPIKey
// creates a new file with correct permissions when the file does not exist.
func TestLoadOrCreateAPIKey_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	// Key should be non-empty and correct length
	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}

	// File should exist with correct permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", keyPath, err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want %o", perm, 0600)
	}

	// File content should match the returned key (with trailing newline)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", keyPath, err)
	}
	if string(data) != key+"\n" {
		t.Errorf("file content = %q, want %q", string(data), key+"\n")
	}
}

// TestLoadOrCreateAPIKey_ReadsExistingFile verifies that LoadOrCreateAPIKey
// reads and returns an existing key from a file.
func TestLoadOrCreateAPIKey_ReadsExistingFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	existingKey := "existing-api-key-abcdef1234567890"
	if err := os.WriteFile(keyPath, []byte(existingKey+"\n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key != existingKey {
		t.Errorf("key = %q, want %q", key, existingKey)
	}
}

// TestLoadOrCreateAPIKey_ReadsExistingFileWithWhitespace verifies that
// LoadOrCreateAPIKey trims whitespace when reading an existing file.
func TestLoadOrCreateAPIKey_ReadsExistingFileWithWhitespace(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	existingKey := "existing-key-with-whitespace"
	if err := os.WriteFile(keyPath, []byte("  "+existingKey+"  \n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key != existingKey {
		t.Errorf("key = %q, want %q", key, existingKey)
	}
}

// TestLoadOrCreateAPIKey_CreatesParentDirectories verifies that
// LoadOrCreateAPIKey creates parent directories if they do not exist.
func TestLoadOrCreateAPIKey_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "nested", "deep", "dir", "api-key")

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	if key == "" {
		t.Error("key should not be empty")
	}

	// Parent directory should exist with 0700 permissions
	parentDir := filepath.Dir(keyPath)
	info, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", parentDir, err)
	}
	if !info.IsDir() {
		t.Errorf("%q should be a directory", parentDir)
	}

	// File should be readable
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", keyPath, err)
	}
	if len(data) == 0 {
		t.Error("file should not be empty")
	}
}

// TestLoadOrCreateAPIKey_EmptyFileGeneratesNewKey verifies that an empty
// file causes a new key to be generated and written.
func TestLoadOrCreateAPIKey_EmptyFileGeneratesNewKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// Create an empty file
	if err := os.WriteFile(keyPath, []byte(""), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}
}

// TestLoadOrCreateAPIKey_WhitespaceOnlyFileGeneratesNewKey verifies that a
// file containing only whitespace causes a new key to be generated.
func TestLoadOrCreateAPIKey_WhitespaceOnlyFileGeneratesNewKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	// Create a file with only whitespace
	if err := os.WriteFile(keyPath, []byte("   \n\t  \n"), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	key, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateAPIKey() error = %v", err)
	}

	expectedLen := apiKeyLength * 2
	if len(key) != expectedLen {
		t.Errorf("len(key) = %d, want %d", len(key), expectedLen)
	}
}

// TestLoadOrCreateAPIKey_Idempotent verifies that calling LoadOrCreateAPIKey
// twice returns the same key.
func TestLoadOrCreateAPIKey_Idempotent(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "api-key")

	key1, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("first LoadOrCreateAPIKey() error = %v", err)
	}

	key2, err := LoadOrCreateAPIKey(keyPath)
	if err != nil {
		t.Fatalf("second LoadOrCreateAPIKey() error = %v", err)
	}

	if key1 != key2 {
		t.Errorf("keys differ: first = %q, second = %q", key1, key2)
	}
}

// TestHandleAuthToken_SameOrigin verifies that same-origin requests receive
// the API token in the response.
func TestHandleAuthToken_SameOrigin(t *testing.T) {
	apiKey := "test-api-key-for-same-origin"
	handler := handleAuthToken(apiKey)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["token"] != apiKey {
		t.Errorf("token = %q, want %q", resp["token"], apiKey)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
}

// TestHandleAuthToken_CrossOriginRejected verifies that cross-origin requests
// are rejected with 403 Forbidden.
func TestHandleAuthToken_CrossOriginRejected(t *testing.T) {
	apiKey := "test-api-key-secret"
	handler := handleAuthToken(apiKey)

	crossSiteValues := []string{"cross-site", "same-site", "none"}

	for _, secFetchSite := range crossSiteValues {
		t.Run(secFetchSite, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
			req.Header.Set("Sec-Fetch-Site", secFetchSite)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
			}

			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			if resp["error"] != "cross-origin requests not allowed" {
				t.Errorf("error = %q, want %q", resp["error"], "cross-origin requests not allowed")
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
		})
	}
}

// TestHandleAuthToken_NoSecFetchSiteHeader verifies that requests without the
// Sec-Fetch-Site header (non-browser clients like curl) are allowed through.
func TestHandleAuthToken_NoSecFetchSiteHeader(t *testing.T) {
	apiKey := "test-api-key-for-curl"
	handler := handleAuthToken(apiKey)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	// No Sec-Fetch-Site header
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["token"] != apiKey {
		t.Errorf("token = %q, want %q", resp["token"], apiKey)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
}

// TestHandleAuthToken_DoesNotLeakTokenOnCrossOrigin verifies that the token
// value is not present in the response body when cross-origin is rejected.
func TestHandleAuthToken_DoesNotLeakTokenOnCrossOrigin(t *testing.T) {
	apiKey := "super-secret-key-12345"
	handler := handleAuthToken(apiKey)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()
	var resp map[string]string
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if _, hasToken := resp["token"]; hasToken {
		t.Error("response should not contain 'token' field for cross-origin request")
	}
}

// TestNewAuthMiddleware_QueryParamWithWrongKey verifies that a wrong token
// in the query parameter is rejected.
func TestNewAuthMiddleware_QueryParamWithWrongKey(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "correct-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/events?token=wrong-key", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["error"] != "invalid token" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid token")
	}
}

// TestNewAuthMiddleware_PreservesHandlerResponse verifies that when
// authentication succeeds, the downstream handler's response is preserved.
func TestNewAuthMiddleware_PreservesHandlerResponse(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "test-key",
	}

	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "custom-value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"created": true}`))
	})

	middleware := NewAuthMiddleware(config)
	handler := middleware(customHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if got := w.Header().Get("X-Custom-Header"); got != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "custom-value")
	}
	if got := w.Body.String(); got != `{"created": true}` {
		t.Errorf("body = %q, want %q", got, `{"created": true}`)
	}
}

// TestHandleAuthTokenDisabled_Returns404JSON verifies that when auth is disabled
// the handler returns a 404 JSON response (not HTML from SPA catch-all).
func TestHandleAuthTokenDisabled_Returns404JSON(t *testing.T) {
	handler := handleAuthTokenDisabled()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/token", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["error"] != "authentication not enabled" {
		t.Errorf("error = %q, want %q", resp["error"], "authentication not enabled")
	}
}

// TestNewAuthMiddleware_OptionsOnProtectedRoute verifies that OPTIONS requests
// pass through even on protected API routes when auth is enabled.
func TestNewAuthMiddleware_OptionsOnProtectedRoute(t *testing.T) {
	config := AuthConfig{
		Enabled: true,
		APIKey:  "test-key",
	}

	middleware := NewAuthMiddleware(config)
	handler := middleware(testHandler())

	paths := []string{"/api/issues", "/api/stats", "/api/terminal/ws"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}
			if w.Body.String() != "OK" {
				t.Errorf("body = %q, want %q", w.Body.String(), "OK")
			}
		})
	}
}
