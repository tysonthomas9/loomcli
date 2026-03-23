package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestHandleCSPReport_ValidReport(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	body := `{"csp-report":{"document-uri":"http://localhost:8080/","violated-directive":"script-src","blocked-uri":"inline","source-file":"http://localhost:8080/app.js","line-number":42}}`
	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/csp-report")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleCSPReport_ValidJSON(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	body := `{"csp-report":{"document-uri":"http://localhost:8080/","violated-directive":"script-src","blocked-uri":"inline"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleCSPReport_MalformedJSON(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/csp-report")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCSPReport_WrongContentType(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	body := `{"csp-report":{"document-uri":"http://localhost:8080/"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleCSPReport_EmptyBody(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/csp-report")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCSPReport_RateLimit(t *testing.T) {
	// burst=5 so first 5 requests succeed, 6th should be rate limited
	limiter := newCSPReportLimiter(rate.Limit(0.1), 5, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	body := `{"csp-report":{"document-uri":"http://localhost:8080/","violated-directive":"script-src"}}`

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/csp-report")
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want %d", i+1, w.Code, http.StatusNoContent)
		}
	}

	// 6th request should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/csp-report")
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("missing Retry-After header")
	}
}

func TestHandleCSPReport_RateLimitDifferentIPs(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(0.1), 1, time.Hour, time.Hour)
	defer limiter.stop()
	handler := handleCSPReport(limiter)

	body := `{"csp-report":{"document-uri":"http://localhost:8080/","violated-directive":"script-src"}}`

	// First IP uses its burst
	req1 := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/csp-report")
	req1.RemoteAddr = "10.0.0.1:12345"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusNoContent {
		t.Fatalf("IP1 first request: status = %d, want %d", w1.Code, http.StatusNoContent)
	}

	// First IP exhausted, should be rate limited
	req1b := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req1b.Header.Set("Content-Type", "application/csp-report")
	req1b.RemoteAddr = "10.0.0.1:12345"
	w1b := httptest.NewRecorder()
	handler.ServeHTTP(w1b, req1b)
	if w1b.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second request: status = %d, want %d", w1b.Code, http.StatusTooManyRequests)
	}

	// Second IP should still work
	req2 := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/csp-report")
	req2.RemoteAddr = "10.0.0.2:12345"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Errorf("IP2 first request: status = %d, want %d", w2.Code, http.StatusNoContent)
	}
}

func TestCSPReportLimiter_Cleanup(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(1), 1, time.Hour, time.Hour)
	defer limiter.stop()

	// Create an entry
	limiter.allow("192.168.1.1")

	// Verify entry exists
	if _, ok := limiter.clients.Load("192.168.1.1"); !ok {
		t.Fatal("entry should exist after allow()")
	}

	// Backdate the entry's lastSeen so evictStale considers it stale
	v, _ := limiter.clients.Load("192.168.1.1")
	v.(*cspReportLimiterEntry).lastSeen.Store(time.Now().Add(-2 * time.Hour).Unix())

	limiter.evictStale()

	// Entry should be evicted
	if _, ok := limiter.clients.Load("192.168.1.1"); ok {
		t.Error("entry should have been evicted after TTL")
	}
}

func TestIsPublicRoute_CSPReport(t *testing.T) {
	if !isPublicRoute(http.MethodPost, "/api/csp-report") {
		t.Error("POST /api/csp-report should be a public route")
	}
	if isPublicRoute(http.MethodGet, "/api/csp-report") {
		t.Error("GET /api/csp-report should not be a public route")
	}
}

func TestIsExcludedFromRateLimit_CSPReport(t *testing.T) {
	if !isExcludedFromRateLimit("/api/csp-report") {
		t.Error("/api/csp-report should be excluded from global rate limit")
	}
}

func TestHandleCSPReport_OversizedFieldsTruncated(t *testing.T) {
	limiter := newCSPReportLimiter(rate.Limit(10), 20, time.Hour, time.Hour)
	defer limiter.stop()

	// Capture slog output
	var buf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(oldLogger)

	handler := handleCSPReport(limiter)

	// Use a single oversized URI field (3000 bytes) to stay within 10KB body limit
	// while exceeding the 2048-byte per-field limit.
	oversizedURI := strings.Repeat("A", 3000)
	oversizedDirective := strings.Repeat("B", 1000)
	body := fmt.Sprintf(`{"csp-report":{"document-uri":"%s","violated-directive":"%s","blocked-uri":"short","source-file":"short"}}`,
		oversizedURI, oversizedDirective)

	req := httptest.NewRequest(http.MethodPost, "/api/csp-report", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/csp-report")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Parse logged JSON and verify truncation
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log entry: %v\nraw: %s", err, buf.String())
	}

	checkLen := func(field string, maxLen int) {
		t.Helper()
		val, ok := logEntry[field].(string)
		if !ok {
			t.Errorf("field %q not found in log", field)
			return
		}
		if len(val) > maxLen {
			t.Errorf("field %q length = %d, want <= %d", field, len(val), maxLen)
		}
	}

	checkLen("document_uri", 2048)
	checkLen("violated_directive", 512)

	// blocked_uri and source_file are "short" in this test, so just verify they exist
	if _, ok := logEntry["blocked_uri"].(string); !ok {
		t.Error("field \"blocked_uri\" not found in log")
	}
	if _, ok := logEntry["source_file"].(string); !ok {
		t.Error("field \"source_file\" not found in log")
	}
}
