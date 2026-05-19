package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestCORSMiddlewareBranches(t *testing.T) {
	nextCalls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusAccepted)
	})

	disabled := CORS(CORSConfig{})(next)
	rr := httptest.NewRecorder()
	disabled.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api", nil))
	if rr.Code != http.StatusAccepted || nextCalls != 1 {
		t.Fatalf("disabled CORS code=%d nextCalls=%d, want passthrough", rr.Code, nextCalls)
	}

	mw := CORS(CORSConfig{Enabled: true, AllowedOrigins: []string{"http://allowed.test/"}})
	req := httptest.NewRequest(http.MethodOptions, "/api", nil)
	req.Header.Set("Origin", "http://allowed.test/")
	rr = httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || rr.Header().Get("Access-Control-Allow-Origin") != "http://allowed.test/" {
		t.Fatalf("allowed preflight code=%d headers=%v", rr.Code, rr.Header())
	}

	req = httptest.NewRequest(http.MethodOptions, "/api", nil)
	req.Header.Set("Origin", "http://blocked.test")
	rr = httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("blocked preflight code=%d, want forbidden", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://same.test/api", nil)
	req.Host = "same.test"
	req.Header.Set("Origin", "http://same.test")
	rr = httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("same-origin code=%d, want accepted", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "null")
	rr = httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("null origin code=%d, want forbidden", rr.Code)
	}

	if !isSameOrigin("http://example.test", "example.test") || isSameOrigin("://bad", "example.test") {
		t.Fatalf("isSameOrigin classification failed")
	}
}

func TestRateLimitMiddlewareAndCleanup(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	cfg.ReadRate = rate.Every(time.Hour)
	cfg.ReadBurst = 1
	cfg.MutateRate = rate.Every(time.Hour)
	cfg.MutateBurst = 1
	cfg.CleanupInterval = 10 * time.Millisecond
	cfg.EntryTTL = 10 * time.Millisecond
	rl, mw := RateLimit(cfg)
	defer rl.Stop()
	rl.Stop()

	nextCalls := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || nextCalls != 1 {
		t.Fatalf("excluded health code=%d nextCalls=%d, want passthrough", rr.Code, nextCalls)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	req.RemoteAddr = "203.0.113.9:12345"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("first mutating request code=%d, want no content", rr.Code)
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" || !strings.Contains(rr.Body.String(), "rate limit exceeded") {
		t.Fatalf("limited response code=%d headers=%v body=%s", rr.Code, rr.Header(), rr.Body.String())
	}

	entry := rl.getOrCreate("198.51.100.1")
	entry.lastSeen.Store(time.Now().Add(-time.Hour).Unix())
	rl.evictStale()
	if _, ok := rl.clients.Load("198.51.100.1"); ok {
		t.Fatalf("stale limiter entry was not evicted")
	}
	if !isMutatingMethod(http.MethodPatch) || isMutatingMethod(http.MethodGet) {
		t.Fatalf("isMutatingMethod classification failed")
	}
	if !isExcludedFromRateLimit("/api/client-errors") || isExcludedFromRateLimit("/api/issues") {
		t.Fatalf("isExcludedFromRateLimit classification failed")
	}
}
