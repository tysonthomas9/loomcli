package misc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// newWorkerAPITestServer mounts the worker API on a fresh mux behind the given
// shared secret.
func newWorkerAPITestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	tmpDir := t.TempDir()
	SetupWorkerAPIRoutes(mux, token,
		func(_, _ string) string { return tmpDir },
		func(_ string) string { return tmpDir },
		func(_, _ string) string { return filepath.Join(tmpDir, "a.log") },
		nil)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func doWorkerRequest(t *testing.T, ts *httptest.Server, method, path, auth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestWorkerAPIUnknownPathReturnsJSONNotFound verifies the worker sub-mux
// answers unmatched paths with the JSON error envelope rather than Go's
// built-in text/plain 404.
func TestWorkerAPIUnknownPathReturnsJSONNotFound(t *testing.T) {
	ts := newWorkerAPITestServer(t, "secret")
	// A path no worker pattern matches at all: /{id} would match DELETE and
	// /{id}/state POST, so go one segment deeper with an unknown leaf.
	resp := doWorkerRequest(t, ts, http.MethodGet, "/api/internal/workers/w1/made-up", "Bearer secret")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, raw)
	}
	if body["error"] != "not found" {
		t.Errorf("error = %q, want %q", body["error"], "not found")
	}
}

// TestWorkerAPIMethodMismatchReturnsJSON405 verifies the wrapper preserves the
// 405 the worker sub-mux would otherwise emit in text/plain.
func TestWorkerAPIMethodMismatchReturnsJSON405(t *testing.T) {
	ts := newWorkerAPITestServer(t, "secret")
	resp := doWorkerRequest(t, ts, http.MethodGet, "/api/internal/workers/register", "Bearer secret")

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow == "" {
		t.Error("Allow header is empty, want the methods Go's mux computed")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v (body=%q)", err, raw)
	}
	if body["error"] != "method not allowed" {
		t.Errorf("error = %q, want %q", body["error"], "method not allowed")
	}
}

// TestWorkerAPIUnknownPathStillRequiresAuth verifies the JSON fallback sits
// INSIDE the auth middleware, so an unknown worker path is not enumerable
// without the shared secret.
func TestWorkerAPIUnknownPathStillRequiresAuth(t *testing.T) {
	tests := []struct {
		name  string
		token string
		auth  string
		want  int
	}{
		{"no token configured", "", "Bearer secret", http.StatusServiceUnavailable},
		{"missing header", "secret", "", http.StatusUnauthorized},
		{"wrong token", "secret", "Bearer nope", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := newWorkerAPITestServer(t, tc.token)
			resp := doWorkerRequest(t, ts, http.MethodGet, "/api/internal/workers/w1/made-up", tc.auth)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
