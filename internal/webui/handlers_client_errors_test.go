package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func newTestClientErrorLimiter() *clientErrorLimiter {
	// 10 req/min, burst 10
	return newClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
}

func postClientError(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/client-errors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHandleClientErrors_Success(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	body := `{"type":"global-error","message":"Uncaught TypeError","stack":"at foo (app.js:1)","url":"http://localhost/","line":1,"col":5}`
	rec := postClientError(handler, body)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestHandleClientErrors_MinimalPayload(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	body := `{"type":"test","message":"some error"}`
	rec := postClientError(handler, body)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestHandleClientErrors_InvalidJSON(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	rec := postClientError(handler, `{not json}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_MissingType(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	rec := postClientError(handler, `{"type":"","message":"some error"}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_MissingMessage(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	rec := postClientError(handler, `{"type":"test","message":""}`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_TypeTooLong(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	longType := strings.Repeat("a", 51)
	body := `{"type":"` + longType + `","message":"err"}`
	rec := postClientError(handler, body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_MessageTooLong(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	longMsg := strings.Repeat("x", 4097)
	body := `{"type":"test","message":"` + longMsg + `"}`
	rec := postClientError(handler, body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_OversizedBody(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	// Create body larger than 16KB
	huge := strings.Repeat("x", 17*1024)
	body := `{"type":"test","message":"` + huge + `"}`
	rec := postClientError(handler, body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleClientErrors_RateLimit(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	body := `{"type":"test","message":"err"}`

	// First 10 should succeed (burst=10)
	for i := 0; i < 10; i++ {
		rec := postClientError(handler, body)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: expected 204, got %d", i+1, rec.Code)
		}
	}

	// 11th should be rate limited
	rec := postClientError(handler, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestHandleClientErrors_DifferentIPsGetOwnBuckets(t *testing.T) {
	limiter := newTestClientErrorLimiter()
	defer limiter.stop()
	handler := handleClientErrors(limiter)

	body := `{"type":"test","message":"err"}`

	// Exhaust bucket for IP 10.0.0.1
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/client-errors", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("ip1 request %d: expected 204, got %d", i+1, rec.Code)
		}
	}

	// Different IP should still work
	req := httptest.NewRequest(http.MethodPost, "/api/client-errors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.2:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("ip2: expected 204, got %d", rec.Code)
	}
}

func TestClientErrorLimiter_Cleanup(t *testing.T) {
	// TTL must be >= 1s since lastSeen uses Unix seconds precision.
	// Cleanup interval 500ms ensures at least one cleanup runs within the wait.
	limiter := newClientErrorLimiter(rate.Limit(10), 10, 500*time.Millisecond, 1*time.Second)
	defer limiter.stop()

	// Create an entry
	limiter.allow("1.2.3.4")

	// Verify entry exists
	if _, ok := limiter.clients.Load("1.2.3.4"); !ok {
		t.Fatal("expected entry to exist after allow()")
	}

	// Wait for TTL (1s) to expire and cleanup (500ms interval) to run.
	// Use 3s to avoid flakiness under CI load.
	time.Sleep(3 * time.Second)

	if _, ok := limiter.clients.Load("1.2.3.4"); ok {
		t.Error("expected entry to be evicted after TTL")
	}
}

// TestIsPublicRoute_ClientErrors and TestIsExcludedFromRateLimit_ClientErrors
// moved to internal/webui/server/middleware/ package tests.
