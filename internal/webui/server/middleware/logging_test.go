package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogRecordsNonHealthRequests(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := RequestLog(logger)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	req := httptest.NewRequest(http.MethodPost, "/api/work", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	logLine := buf.String()
	for _, want := range []string{`"method":"POST"`, `"path":"/api/work"`, `"status":201`, `"ip":"192.0.2.1"`} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log missing %s: %s", want, logLine)
		}
	}
}

func TestRequestLogSkipsHealthAndRecorderCapabilities(t *testing.T) {
	var buf bytes.Buffer
	mw := RequestLog(slog.New(slog.NewTextHandler(&buf, nil)))
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if buf.Len() != 0 {
		t.Fatalf("health request was logged: %q", buf.String())
	}
	if !isHealthCheckPath("/health") || !isHealthCheckPath("/api/health") || isHealthCheckPath("/api/status") {
		t.Fatal("isHealthCheckPath mapping mismatch")
	}

	rec := &responseRecorder{ResponseWriter: httptest.NewRecorder(), statusCode: http.StatusOK}
	if rec.Unwrap() == nil {
		t.Fatal("Unwrap returned nil")
	}
	rec.Flush()
	if _, _, err := rec.Hijack(); err == nil {
		t.Fatal("Hijack should fail when underlying writer does not support it")
	}
	if _, err := rec.Write([]byte("ok")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.statusCode != http.StatusOK || !rec.written {
		t.Fatalf("recorder after Write = %+v", rec)
	}
	rec.WriteHeader(http.StatusAccepted)
	if rec.statusCode != http.StatusOK {
		t.Fatalf("late WriteHeader changed status to %d", rec.statusCode)
	}
}
