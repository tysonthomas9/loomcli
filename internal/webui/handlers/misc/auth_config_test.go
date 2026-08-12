package misc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestHandleAuthConfig_ExternalMode(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	handler := handleAuthConfig("https://auth.example.com", limiter)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp authConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Mode != "oidc" {
		t.Errorf("expected mode 'oidc', got %q", resp.Mode)
	}
	// When auth proxy is active, auth_url is empty so the frontend uses
	// window.location.origin (same-origin proxy at /api/auth/*).
	if resp.AuthURL != "" {
		t.Errorf("expected empty auth_url for proxy mode, got %q", resp.AuthURL)
	}
}

func TestHandleAuthConfig_NoneMode(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	handler := handleAuthConfig("", limiter)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp authConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Mode != "open" {
		t.Errorf("expected mode 'open', got %q", resp.Mode)
	}
}

func TestHandleAuthConfig_CacheControl(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	for _, extAuthURL := range []string{"", "https://auth.example.com"} {
		handler := handleAuthConfig(extAuthURL, limiter)

		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		cc := rr.Header().Get("Cache-Control")
		if cc != "no-store" {
			t.Errorf("extAuthURL=%q: expected Cache-Control 'no-store', got %q", extAuthURL, cc)
		}
	}
}

func TestHandleAuthConfig_ContentType(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	handler := handleAuthConfig("", limiter)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

func TestHandleAuthConfig_AuthURLOmittedWhenNone(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	handler := handleAuthConfig("", limiter)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var raw map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := raw["auth_url"]; ok {
		t.Error("auth_url key should not be present when mode is 'none'")
	}
}

func TestHandleAuthConfig_RateLimitEnforced(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(10), 2, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()

	handler := handleAuthConfig("", limiter)

	// First 2 requests should succeed (burst=2)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// Third request should be rate-limited
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	if ra := rr.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestAuthConfigLimiter_AllowAndEvict(t *testing.T) {
	limiter := newAuthConfigLimiter(rate.Limit(5), 2, 100*time.Millisecond, 50*time.Millisecond)
	defer limiter.Stop()

	// allow() should return true within burst
	if !limiter.allow("192.168.1.1") {
		t.Error("first request should be allowed")
	}
	if !limiter.allow("192.168.1.1") {
		t.Error("second request should be allowed (within burst)")
	}

	// Exhaust burst
	if limiter.allow("192.168.1.1") {
		t.Error("third request should be denied (burst exhausted)")
	}

	// Different IP should still be allowed
	if !limiter.allow("192.168.1.2") {
		t.Error("different IP should be allowed")
	}

	// Wait for eviction (TTL=50ms, cleanup interval=100ms)
	time.Sleep(200 * time.Millisecond)

	// After eviction, entry should be gone — new entry gets fresh burst
	if !limiter.allow("192.168.1.1") {
		t.Error("request should be allowed after eviction")
	}
}

func TestNormalizeBackendName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"fleet", "fleet"},
		{"fleet-db", "fleet"},
		{"fleetdb", "fleet"},
		{"fleet-workspace", "fleet"},
		{"api", "api"},
		{"agent-ipc", "agent-ipc"},
		{"something-new", "something-new"},
	}
	for _, c := range cases {
		if got := normalizeBackendName(c.in); got != c.want {
			t.Errorf("normalizeBackendName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHandleAuthConfig_DoesNotUseLegacyBackendEnvFallback(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()
	handler := HandleAuthConfig("", limiter, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp authConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkItemsAdapter != "" {
		t.Errorf("WorkItemsAdapter = %q, want empty without composed provider", resp.WorkItemsAdapter)
	}
}

func TestHandleAuthConfig_UsesNarrowBackendNameProvider(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "api")

	limiter := newAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)
	defer limiter.Stop()
	handler := HandleAuthConfig("", limiter, func(context.Context) string { return "fleet-db" })

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp authConfigResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkItemsAdapter != "fleet" {
		t.Errorf("WorkItemsAdapter = %q, want %q", resp.WorkItemsAdapter, "fleet")
	}
}
