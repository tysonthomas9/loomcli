package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimitMiddleware_PassesNormalRequests(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        10,
		ReadBurst:       10,
		MutateRate:      5,
		MutateBurst:     5,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "OK" {
		t.Errorf("body = %q, want %q", got, "OK")
	}
}

func TestRateLimitMiddleware_BlocksExcessiveRequests(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        1,
		ReadBurst:       5,
		MutateRate:      1,
		MutateBurst:     5,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(testHandler())

	// First 5 requests should pass (burst allows 5)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	// 6th request should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("6th request: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// Verify JSON error body
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if got := body["error"]; got != "rate limit exceeded" {
		t.Errorf("error = %q, want %q", got, "rate limit exceeded")
	}
	if _, ok := body["retry_after"]; !ok {
		t.Error("response body missing retry_after field")
	}

	// Verify Retry-After header is present
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header should be set")
	}
}

func TestRateLimitMiddleware_PerIPIsolation(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        1,
		ReadBurst:       2,
		MutateRate:      1,
		MutateBurst:     2,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(testHandler())

	// Exhaust IP A's burst
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("IP A request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}

	// IP A should now be rate limited
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("IP A exhausted: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// IP B should still be allowed
	req = httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("IP B: status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRateLimitMiddleware_HealthCheckExcluded(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        1,
		ReadBurst:       1,
		MutateRate:      1,
		MutateBurst:     1,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(testHandler())

	paths := []string{"/health", "/api/health"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			// Send many requests to the health endpoint — none should be limited
			for i := 0; i < 50; i++ {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.RemoteAddr = "10.0.0.1:12345"
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				if w.Code != http.StatusOK {
					t.Errorf("request %d to %s: status = %d, want %d", i+1, path, w.Code, http.StatusOK)
					return
				}
			}
		})
	}
}

func TestRateLimitMiddleware_MutateVsReadRates(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        10,
		ReadBurst:       10,
		MutateRate:      1,
		MutateBurst:     1,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(testHandler())

	// POST (mutating) — burst of 1, so second request should be blocked
	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first POST: status = %d, want %d", w.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("second POST: status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// GET (read) from the same IP should still be allowed (separate limiter, burst=10)
	for i := 0; i < 10; i++ {
		req = httptest.NewRequest(http.MethodGet, "/api/issues", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET request %d: status = %d, want %d", i+1, w.Code, http.StatusOK)
		}
	}
}

func TestRateLimitMiddleware_429ResponseFormat(t *testing.T) {
	rl, mw := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        2,
		ReadBurst:       1,
		MutateRate:      2,
		MutateBurst:     1,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})
	defer rl.Stop()

	handler := mw(testHandler())

	// Exhaust the burst
	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Trigger 429
	req = httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	// Check Content-Type
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	// Check Retry-After header
	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After header should be set")
	}
	if retryAfter != "1" {
		t.Errorf("Retry-After = %q, want %q (ceil(1/2) = 1)", retryAfter, "1")
	}

	// Check JSON body structure
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if got, ok := body["error"].(string); !ok || got != "rate limit exceeded" {
		t.Errorf("error = %v, want %q", body["error"], "rate limit exceeded")
	}
	if got, ok := body["retry_after"].(float64); !ok || got != 1 {
		t.Errorf("retry_after = %v, want %v", body["retry_after"], 1)
	}
}

func TestRateLimiter_CleanupEvictsStaleEntries(t *testing.T) {
	rl, _ := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        10,
		ReadBurst:       10,
		MutateRate:      10,
		MutateBurst:     10,
		CleanupInterval: 1 * time.Millisecond,
		EntryTTL:        1 * time.Millisecond,
	})
	defer rl.Stop()

	// Manually add an entry via getOrCreate
	entry := rl.getOrCreate("192.168.1.100")
	// Set lastSeen to a time far in the past to ensure it's stale
	entry.lastSeen.Store(time.Now().Add(-time.Hour).Unix())

	// Verify the entry exists
	if _, ok := rl.clients.Load("192.168.1.100"); !ok {
		t.Fatal("entry should exist before eviction")
	}

	// Manually trigger eviction (don't rely on timing of the background goroutine)
	rl.evictStale()

	// Verify the entry was evicted
	if _, ok := rl.clients.Load("192.168.1.100"); ok {
		t.Error("stale entry should have been evicted")
	}
}

func TestRateLimiter_Stop(t *testing.T) {
	rl, _ := NewRateLimitMiddleware(RateLimitConfig{
		ReadRate:        10,
		ReadBurst:       10,
		MutateRate:      10,
		MutateBurst:     10,
		CleanupInterval: time.Hour,
		EntryTTL:        time.Hour,
	})

	// Stop should return without blocking
	done := make(chan struct{})
	go func() {
		rl.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — Stop returned
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds")
	}
}

func TestIsExcludedFromRateLimit(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/api/health", true},
		{"/api/issues", false},
		{"/", false},
		{"/healthcheck", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExcludedFromRateLimit(tt.path)
			if got != tt.want {
				t.Errorf("isExcludedFromRateLimit(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsMutatingMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodOptions, false},
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodDelete, true},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := isMutatingMethod(tt.method)
			if got != tt.want {
				t.Errorf("isMutatingMethod(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}

func TestDefaultRateLimitConfig(t *testing.T) {
	cfg := DefaultRateLimitConfig()

	if cfg.ReadRate != rate.Limit(100) {
		t.Errorf("ReadRate = %v, want %v", cfg.ReadRate, rate.Limit(100))
	}
	if cfg.ReadBurst != 200 {
		t.Errorf("ReadBurst = %d, want %d", cfg.ReadBurst, 200)
	}
	if cfg.MutateRate != rate.Limit(20) {
		t.Errorf("MutateRate = %v, want %v", cfg.MutateRate, rate.Limit(20))
	}
	if cfg.MutateBurst != 40 {
		t.Errorf("MutateBurst = %d, want %d", cfg.MutateBurst, 40)
	}
	if cfg.CleanupInterval != 5*time.Minute {
		t.Errorf("CleanupInterval = %v, want %v", cfg.CleanupInterval, 5*time.Minute)
	}
	if cfg.EntryTTL != 10*time.Minute {
		t.Errorf("EntryTTL = %v, want %v", cfg.EntryTTL, 10*time.Minute)
	}
}
