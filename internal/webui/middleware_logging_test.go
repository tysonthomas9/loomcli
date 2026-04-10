package webui

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// logEntry holds parsed fields from a JSON slog line.
type logEntry struct {
	Msg        string  `json:"msg"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Status     int     `json:"status"`
	DurationMS float64 `json:"duration_ms"`
	IP         string  `json:"ip"`
}

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

func parseLogEntry(t *testing.T, buf *bytes.Buffer) logEntry {
	t.Helper()
	var entry logEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse log entry: %v\nraw: %s", err, buf.String())
	}
	return entry
}

func TestRequestLogMiddleware_NormalRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := parseLogEntry(t, &buf)

	if entry.Msg != "http request" {
		t.Errorf("msg = %q, want %q", entry.Msg, "http request")
	}
	if entry.Method != "GET" {
		t.Errorf("method = %q, want %q", entry.Method, "GET")
	}
	if entry.Path != "/api/issues" {
		t.Errorf("path = %q, want %q", entry.Path, "/api/issues")
	}
	if entry.Status != 200 {
		t.Errorf("status = %d, want %d", entry.Status, 200)
	}
	if entry.DurationMS < 0 {
		t.Errorf("duration_ms = %f, want >= 0", entry.DurationMS)
	}
}

func TestRequestLogMiddleware_HealthEndpointsSkipped(t *testing.T) {
	for _, path := range []string{"/health", "/api/health"} {
		t.Run(path, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestLogger(&buf)

			handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if buf.Len() != 0 {
				t.Errorf("expected no log output for %s, got: %s", path, buf.String())
			}
		})
	}
}

func TestRequestLogMiddleware_Non200Status(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := parseLogEntry(t, &buf)
	if entry.Status != 404 {
		t.Errorf("status = %d, want %d", entry.Status, 404)
	}
}

func TestRequestLogMiddleware_POSTMethod(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/issues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := parseLogEntry(t, &buf)
	if entry.Method != "POST" {
		t.Errorf("method = %q, want %q", entry.Method, "POST")
	}
	if entry.Status != 201 {
		t.Errorf("status = %d, want %d", entry.Status, 201)
	}
}

func TestRequestLogMiddleware_ImplicitOK(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write body without calling WriteHeader — Go defaults to 200
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := parseLogEntry(t, &buf)
	if entry.Status != 200 {
		t.Errorf("status = %d, want %d", entry.Status, 200)
	}
}

func TestRequestLogMiddleware_NilLogger(t *testing.T) {
	// Should not panic when logger is nil
	handler := NewRequestLogMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRWRecorder_Unwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := newRWRecorder(inner)

	unwrapped := rec.Unwrap()
	if unwrapped != inner {
		t.Error("Unwrap() did not return the underlying ResponseWriter")
	}
}

func TestRWRecorder_ImplementsFlusher(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := newRWRecorder(inner)

	flusher, ok := interface{}(rec).(http.Flusher)
	if !ok {
		t.Fatal("rwRecorder does not implement http.Flusher")
	}
	// Should not panic
	flusher.Flush()
	if !inner.Flushed {
		t.Error("Flush() was not delegated to underlying writer")
	}
}

func TestRequestLogMiddleware_ClientIP(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	handler := NewRequestLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := parseLogEntry(t, &buf)
	if entry.IP != "10.0.0.1" {
		t.Errorf("ip = %q, want %q", entry.IP, "10.0.0.1")
	}
}
