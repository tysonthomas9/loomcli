package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fallbackTestMux builds a throwaway mux with a single GET route, wrapped by
// JSONFallbackMux.
func fallbackTestMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("streamed"))
	})
	mux.HandleFunc("GET /dir/{$}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("dir"))
	})
	return JSONFallbackMux(mux)
}

// TestJSONFallbackMux_MatchedRouteUntouched is the SSE/streaming guard: a
// request that matches a pattern must be served with the original
// ResponseWriter, so status, headers and body all pass through verbatim.
func TestJSONFallbackMux_MatchedRouteUntouched(t *testing.T) {
	rr := httptest.NewRecorder()
	fallbackTestMux().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/a", nil))

	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusTeapot)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if got := rr.Body.String(); got != "streamed" {
		t.Errorf("body = %q, want %q", got, "streamed")
	}
}

func TestJSONFallbackMux_UnmatchedPathIsJSON404(t *testing.T) {
	rr := httptest.NewRecorder()
	fallbackTestMux().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	if body["error"] != "not found" {
		t.Errorf("error = %q, want %q", body["error"], "not found")
	}
}

// TestJSONFallbackMux_MethodMismatchIsJSON405 guards the regression that a bare
// mux.Handle("/", ...) fallback would introduce: Go only synthesizes 405 when
// no node matches at all, so registering a catch-all pattern would turn every
// wrong-method request into a 404.
func TestJSONFallbackMux_MethodMismatchIsJSON405(t *testing.T) {
	rr := httptest.NewRecorder()
	fallbackTestMux().ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/a", nil))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if allow := rr.Header().Get("Allow"); allow == "" {
		t.Error("Allow header is empty, want the methods Go's mux computed")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, rr.Body.String())
	}
	if body["error"] != "method not allowed" {
		t.Errorf("error = %q, want %q", body["error"], "method not allowed")
	}
}

// TestJSONFallbackMux_TrailingSlashRedirect verifies that Go's built-in
// trailing-slash redirect still happens: mux.Handler reports a non-empty
// pattern for it, so it takes the normal ServeHTTP path instead of the JSON
// fallback. The exact 3xx code is Go's to choose, so this asserts the wrapper
// produces byte-for-byte what the bare mux would.
func TestJSONFallbackMux_TrailingSlashRedirect(t *testing.T) {
	bare := http.NewServeMux()
	bare.HandleFunc("GET /dir/{$}", func(http.ResponseWriter, *http.Request) {})
	want := httptest.NewRecorder()
	bare.ServeHTTP(want, httptest.NewRequest(http.MethodGet, "/dir", nil))

	rr := httptest.NewRecorder()
	fallbackTestMux().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/dir", nil))

	if rr.Code != want.Code {
		t.Fatalf("status = %d, want %d (bare mux)", rr.Code, want.Code)
	}
	if rr.Code < 300 || rr.Code > 399 {
		t.Fatalf("status = %d, want a redirect", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/dir/" {
		t.Errorf("Location = %q, want /dir/", loc)
	}
}

// TestJSONFallbackMux_LargeBodyOnUnmatchedRoute covers the "broken pipe"
// finding: a client streaming a body larger than MaxRequestBody to a route with
// no handler must still get the JSON envelope back.
func TestJSONFallbackMux_LargeBodyOnUnmatchedRoute(t *testing.T) {
	srv := httptest.NewServer(fallbackTestMux())
	t.Cleanup(srv.Close)

	body := strings.NewReader(strings.Repeat("x", 2<<20))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/nope", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed (broken pipe regression?): %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, raw)
	}
	if decoded["error"] != "not found" {
		t.Errorf("error = %q, want %q", decoded["error"], "not found")
	}
}

func TestJSONNotFound(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whatever", strings.NewReader("payload"))
	JSONNotFound(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["error"] != "not found" {
		t.Errorf("error = %q, want %q", body["error"], "not found")
	}
}

func TestDrainBody(t *testing.T) {
	t.Run("consumes the body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello"))
		DrainBody(req)
		rest, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("reading remainder: %v", err)
		}
		if len(rest) != 0 {
			t.Errorf("%d bytes left unread, want 0", len(rest))
		}
	})

	t.Run("stops at MaxRequestBody", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("y", MaxRequestBody+512)))
		DrainBody(req)
		rest, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("reading remainder: %v", err)
		}
		if len(rest) != 512 {
			t.Errorf("%d bytes left unread, want 512", len(rest))
		}
	})

	t.Run("nil body is a no-op", func(t *testing.T) {
		DrainBody(&http.Request{})
		DrainBody(nil)
	})
}
