package webui

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusRecorderCapturesStatusAndDelegates(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr}

	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusAccepted)
	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d, want created", rec.status)
	}
	if rr.Code != http.StatusCreated {
		t.Fatalf("recorder code = %d, want created", rr.Code)
	}

	if rec.Unwrap() != rr {
		t.Fatalf("Unwrap did not return inner writer")
	}
	rec.Flush()
	if !rr.Flushed {
		t.Fatalf("Flush did not reach inner flusher")
	}
	if _, _, err := rec.Hijack(); err != http.ErrNotSupported {
		t.Fatalf("Hijack error = %v, want ErrNotSupported", err)
	}
}

func TestStatusRecorderWriteDefaultsToOK(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr}

	n, err := rec.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len("hello") {
		t.Fatalf("Write length = %d, want %d", n, len("hello"))
	}
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want OK", rec.status)
	}
	if got := rr.Body.String(); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

type hijackableResponseWriter struct {
	http.ResponseWriter
	conn net.Conn
	rw   *bufio.ReadWriter
}

func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.rw, nil
}

func TestStatusRecorderHijackDelegates(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(clientConn), bufio.NewWriter(clientConn))
	rec := &statusRecorder{ResponseWriter: &hijackableResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		conn:           serverConn,
		rw:             rw,
	}}

	gotConn, gotRW, err := rec.Hijack()
	if err != nil {
		t.Fatalf("Hijack returned error: %v", err)
	}
	if gotConn != serverConn || gotRW != rw {
		t.Fatalf("Hijack returned unexpected delegated values")
	}
}

func TestPromMetricsMiddlewareCapturesMuxRoute(t *testing.T) {
	outer, inner := PromMetricsMiddleware()
	mux := http.NewServeMux()
	mux.Handle("GET /items/{id}", inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})))
	handler := outer(mux)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/items/123", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want created", rr.Code)
	}

	metricsRR := httptest.NewRecorder()
	PromHandler().ServeHTTP(metricsRR, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := metricsRR.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("metrics Content-Encoding = %q, want uncompressed", got)
	}
	if body := metricsRR.Body.String(); !strings.Contains(body, `loom_http_requests_total{code="201",method="GET",route="/items/{id}"}`) {
		t.Fatalf("metrics body did not include captured route; body prefix: %.200q", body)
	}
}

func TestPromMetricsMiddlewareUsesUnmatchedFallback(t *testing.T) {
	outer, _ := PromMetricsMiddleware()
	handler := outer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/missing", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want no content", rr.Code)
	}

	metricsRR := httptest.NewRecorder()
	PromHandler().ServeHTTP(metricsRR, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if body := metricsRR.Body.String(); !strings.Contains(body, `loom_http_requests_total{code="204",method="DELETE",route="unmatched"}`) {
		t.Fatalf("metrics body did not include unmatched route; body prefix: %.200q", body)
	}
}

func TestPromRouteCaptureByPathAndSetPattern(t *testing.T) {
	store := &promRouteStore{}
	ctx := context.WithValue(context.Background(), promRouteCtxKey{}, store)
	req := httptest.NewRequest(http.MethodGet, "/proxy/api/v1", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	PromRouteCaptureByPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetPromRoutePattern(r.Context(), "GET /inner")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rr, req)
	if store.pattern != "/proxy/api/v1" {
		t.Fatalf("pattern = %q, want URL path override", store.pattern)
	}

	store.pattern = ""
	SetPromRoutePattern(ctx, "POST /workspace/{id}")
	if store.pattern != "/workspace/{id}" {
		t.Fatalf("pattern = %q, want method prefix stripped", store.pattern)
	}
	SetPromRoutePattern(ctx, "")
	if store.pattern != "/workspace/{id}" {
		t.Fatalf("empty pattern changed store to %q", store.pattern)
	}
	SetPromRoutePattern(context.Background(), "GET /ignored")
}
