package httpclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverAuthMode_None(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AuthMode{Mode: "none"})
	}))
	defer srv.Close()

	// Suppress device flow stderr output.
	stderr = io.Discard
	defer func() { stderr = os.Stderr }()

	c, err := New(Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer c.Close()

	if c.authMode.Mode != "none" {
		t.Errorf("expected mode 'none', got %q", c.authMode.Mode)
	}
	if c.authMode.AuthURL != "" {
		t.Errorf("expected empty auth_url, got %q", c.authMode.AuthURL)
	}
}

func TestDiscoverAuthMode_External(t *testing.T) {
	// Auth service mock — must be created first so its URL can be embedded in /api/config.
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/device/code" {
			json.NewEncoder(w).Encode(DeviceCodeResponse{
				DeviceCode:      "test-device-code",
				UserCode:        "TEST-1234",
				VerificationURI: "https://auth.example.com/device",
				ExpiresIn:       300,
				Interval:        1,
			})
			return
		}
		if r.URL.Path == "/api/auth/device/token" {
			json.NewEncoder(w).Encode(deviceTokenResponse{ //nolint:gosec // G117: test value, not a real secret
				AccessToken: "test-jwt-token",
				TokenType:   "bearer",
				ExpiresIn:   900,
			})
			return
		}
		t.Errorf("unexpected auth path: %s", r.URL.Path)
	}))
	defer authSrv.Close()

	// Loom server mock — returns auth_url pointing at the auth mock.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(AuthMode{Mode: "external", AuthURL: authSrv.URL})
			return
		}
		t.Errorf("unexpected server path: %s", r.URL.Path)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	stderr = io.Discard
	defer func() { stderr = os.Stderr }()

	c, err := New(Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer c.Close()

	if c.authMode.Mode != "external" {
		t.Errorf("expected mode 'external', got %q", c.authMode.Mode)
	}
	if c.authMode.AuthURL != authSrv.URL {
		t.Errorf("expected auth_url %q, got %q", authSrv.URL, c.authMode.AuthURL)
	}
	if c.token != "test-jwt-token" {
		t.Errorf("expected token 'test-jwt-token', got %q", c.token)
	}
}

func TestDiscoverAuthMode_ServerDown(t *testing.T) {
	stderr = io.Discard
	defer func() { stderr = os.Stderr }()

	_, err := New(Config{ServerURL: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if got := err.Error(); !strings.Contains(got, "cannot reach") {
		t.Errorf("expected error containing 'cannot reach', got: %s", got)
	}
}

func TestDiscoverAuthMode_UnknownMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"mode": "saml"})
	}))
	defer srv.Close()

	stderr = io.Discard
	defer func() { stderr = os.Stderr }()

	_, err := New(Config{ServerURL: srv.URL})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if got := err.Error(); !strings.Contains(got, "unsupported auth mode") {
		t.Errorf("expected error containing 'unsupported auth mode', got: %s", got)
	}
}

func TestDoInjectsAuthHeader(t *testing.T) {
	var gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	c := &Client{
		serverURL:   backend.URL,
		authMode:    &AuthMode{Mode: "external", AuthURL: "https://auth.example.com"},
		token:       "my-test-jwt",
		tokenExpiry: time.Now().Add(10 * time.Minute),
		httpClient:  http.DefaultClient,
	}

	req, _ := http.NewRequest("GET", backend.URL+"/api/agents", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "Bearer my-test-jwt" {
		t.Errorf("expected 'Bearer my-test-jwt', got %q", gotAuth)
	}
}

func TestDoNoAuthWhenModeNone(t *testing.T) {
	var gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	c := &Client{
		serverURL:  backend.URL,
		authMode:   &AuthMode{Mode: "none"},
		httpClient: http.DefaultClient,
	}

	req, _ := http.NewRequest("GET", backend.URL+"/api/agents", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestTokenCacheRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	serverURL := "https://loom.example.com:8080"
	token := "cached-jwt-token"
	expiry := time.Now().Add(15 * time.Minute)

	if err := saveCachedToken(serverURL, token, expiry); err != nil {
		t.Fatalf("saveCachedToken error: %v", err)
	}

	loaded, _, err := loadCachedToken(serverURL)
	if err != nil {
		t.Fatalf("loadCachedToken error: %v", err)
	}
	if loaded != token {
		t.Errorf("expected %q, got %q", token, loaded)
	}
}

func TestTokenCacheExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	serverURL := "https://loom.example.com:8080"
	// Expired 5 minutes ago.
	expiry := time.Now().Add(-5 * time.Minute)

	if err := saveCachedToken(serverURL, "old-token", expiry); err != nil {
		t.Fatalf("saveCachedToken error: %v", err)
	}

	loaded, _, err := loadCachedToken(serverURL)
	if err != nil {
		t.Fatalf("loadCachedToken error: %v", err)
	}
	if loaded != "" {
		t.Errorf("expected empty string for expired token, got %q", loaded)
	}
}

func TestTokenCacheClear(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	serverURL := "https://loom.example.com:8080"

	if err := saveCachedToken(serverURL, "to-clear", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("saveCachedToken error: %v", err)
	}

	if err := clearCachedToken(serverURL); err != nil {
		t.Fatalf("clearCachedToken error: %v", err)
	}

	loaded, _, err := loadCachedToken(serverURL)
	if err != nil {
		t.Fatalf("loadCachedToken error: %v", err)
	}
	if loaded != "" {
		t.Errorf("expected empty string after clear, got %q", loaded)
	}
}

func TestTokenCachePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	serverURL := "https://loom.example.com:8080"
	if err := saveCachedToken(serverURL, "perm-token", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatalf("saveCachedToken error: %v", err)
	}

	path := filepath.Join(tmpDir, "tokens", cacheKey(serverURL)+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected permissions 0600, got %04o", perm)
	}
}

func TestTokenCacheCorruption(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	serverURL := "https://loom.example.com:8080"
	dir := filepath.Join(tmpDir, "tokens")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cacheKey(serverURL)+".json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := loadCachedToken(serverURL)
	if err != nil {
		t.Fatalf("expected no error on corruption, got: %v", err)
	}
	if loaded != "" {
		t.Errorf("expected empty string for corrupted cache, got %q", loaded)
	}

	// File should have been cleaned up.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected corrupted cache file to be removed")
	}
}

func TestDo401Retry(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", tmpDir)

	stderr = io.Discard
	defer func() { stderr = os.Stderr }()

	callCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second call succeeds.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	// Mock auth service for re-authentication.
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/device/code" {
			json.NewEncoder(w).Encode(DeviceCodeResponse{
				DeviceCode:      "retry-code",
				UserCode:        "RETRY-5678",
				VerificationURI: "https://auth.example.com/device",
				ExpiresIn:       300,
				Interval:        1,
			})
			return
		}
		if r.URL.Path == "/api/auth/device/token" {
			json.NewEncoder(w).Encode(deviceTokenResponse{ //nolint:gosec // G117: test value, not a real secret
				AccessToken: "new-jwt-token",
				TokenType:   "bearer",
				ExpiresIn:   900,
			})
			return
		}
	}))
	defer authSrv.Close()

	c := &Client{
		serverURL:   backend.URL,
		authMode:    &AuthMode{Mode: "external", AuthURL: authSrv.URL},
		token:       "stale-token",
		tokenExpiry: time.Now().Add(10 * time.Minute),
		httpClient:  http.DefaultClient,
	}

	req, _ := http.NewRequest("GET", backend.URL+"/api/test", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}
	if callCount != 2 {
		t.Errorf("expected 2 backend calls, got %d", callCount)
	}
	if c.token != "new-jwt-token" {
		t.Errorf("expected token to be updated to 'new-jwt-token', got %q", c.token)
	}
}
