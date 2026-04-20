package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"
)

// recordingJWKSHandler is an http.Handler that records the paths it receives
// and responds with an empty (but valid) JWKS body.
type recordingJWKSHandler struct {
	mu        sync.Mutex
	paths     []string
	callCount atomic.Int64
}

func (h *recordingJWKSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.paths = append(h.paths, r.URL.Path)
	h.mu.Unlock()
	h.callCount.Add(1)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"keys":[]}`)
}

func (h *recordingJWKSHandler) snapshotPaths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.paths))
	copy(out, h.paths)
	return out
}

// waitForCall waits up to d for at least n calls to the recording handler.
func (h *recordingJWKSHandler) waitForCall(t *testing.T, n int64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if h.callCount.Load() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d JWKS fetch(es); got %d", n, h.callCount.Load())
}

// newDiscardLogger returns a logger that discards all output so tests don't
// spam the console when the JWKS fetch races with cache initialisation.
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestInitExtAuth_EmptyURL_ReturnsNil verifies that initExtAuth returns nil
// middleware and cleanup when ExtAuthURL is empty, regardless of whether
// ExtAuthJWKSURL is set (the override has no effect without a base auth URL).
func TestInitExtAuth_EmptyURL_ReturnsNil(t *testing.T) {
	cases := []struct {
		name    string
		jwksURL string
	}{
		{name: "both empty", jwksURL: ""},
		{name: "jwks override set but no auth URL", jwksURL: "https://example.com/jwks"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := webui.ServerConfig{
				ExtAuthURL:     "",
				ExtAuthJWKSURL: tc.jwksURL,
				Logger:         newDiscardLogger(),
			}
			mw, cleanup := initExtAuth(cfg)
			if mw != nil {
				t.Fatalf("expected nil middleware when ExtAuthURL is empty, got %T", mw)
			}
			if cleanup != nil {
				t.Fatalf("expected nil cleanup when ExtAuthURL is empty, got non-nil")
			}
		})
	}
}

// TestInitExtAuth_DerivedJWKSURL verifies that when ExtAuthJWKSURL is empty,
// the JWKS cache fetches from ExtAuthURL + "/api/auth/jwks".
func TestInitExtAuth_DerivedJWKSURL(t *testing.T) {
	h := &recordingJWKSHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cfg := webui.ServerConfig{
		ExtAuthURL:     srv.URL, // e.g. http://127.0.0.1:PORT
		ExtAuthJWKSURL: "",
		Logger:         newDiscardLogger(),
	}

	// Route the helper's logger to io.Discard to avoid noisy test output.
	origLogger := logger
	logger = newDiscardLogger()
	t.Cleanup(func() { logger = origLogger })

	mw, cleanup := initExtAuth(cfg)
	if mw == nil {
		t.Fatal("expected non-nil middleware when ExtAuthURL is set")
	}
	t.Cleanup(func() {
		if cleanup != nil {
			cleanup()
		}
	})

	// NewJWKSCache performs a synchronous initial fetch, so the call should
	// already be recorded by the time initExtAuth returns.
	h.waitForCall(t, 1, 2*time.Second)

	paths := h.snapshotPaths()
	if len(paths) == 0 {
		t.Fatal("expected at least one JWKS fetch")
	}
	for _, p := range paths {
		if p != "/api/auth/jwks" {
			t.Fatalf("expected derived JWKS path %q, got %q", "/api/auth/jwks", p)
		}
	}
}

// TestInitExtAuth_OverrideJWKSURL verifies that when ExtAuthJWKSURL is set,
// the JWKS cache fetches from the override URL rather than the derived
// ExtAuthURL + "/api/auth/jwks" path.
func TestInitExtAuth_OverrideJWKSURL(t *testing.T) {
	// authSrv simulates the auth service base URL; it should NOT be hit.
	authCallCount := atomic.Int64{}
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCallCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"keys":[]}`)
	}))
	t.Cleanup(authSrv.Close)

	// jwksSrv is the override endpoint and should receive the fetch.
	h := &recordingJWKSHandler{}
	jwksSrv := httptest.NewServer(h)
	t.Cleanup(jwksSrv.Close)

	overrideURL := jwksSrv.URL + "/custom/jwks/path"

	cfg := webui.ServerConfig{
		ExtAuthURL:     authSrv.URL,
		ExtAuthJWKSURL: overrideURL,
		Logger:         newDiscardLogger(),
	}

	origLogger := logger
	logger = newDiscardLogger()
	t.Cleanup(func() { logger = origLogger })

	mw, cleanup := initExtAuth(cfg)
	if mw == nil {
		t.Fatal("expected non-nil middleware when ExtAuthURL is set")
	}
	t.Cleanup(func() {
		if cleanup != nil {
			cleanup()
		}
	})

	h.waitForCall(t, 1, 2*time.Second)

	paths := h.snapshotPaths()
	if len(paths) == 0 {
		t.Fatal("expected at least one JWKS fetch to the override server")
	}
	for _, p := range paths {
		if p != "/custom/jwks/path" {
			t.Fatalf("expected override JWKS path %q, got %q", "/custom/jwks/path", p)
		}
	}

	// The auth base URL should NOT receive any JWKS traffic when the override
	// is set. Allow a brief window in case of races, but the derived endpoint
	// (/api/auth/jwks on authSrv) should never be hit.
	if got := authCallCount.Load(); got != 0 {
		t.Fatalf("expected zero fetches to auth base URL when override is set, got %d", got)
	}
}

// TestInitExtAuth_OverrideNotSubstringOfAuthURL is a sanity check that the
// override URL is truly independent of ExtAuthURL — if the override URL
// contains a totally different host/path, the JWKS fetch still targets it.
func TestInitExtAuth_OverrideNotSubstringOfAuthURL(t *testing.T) {
	h := &recordingJWKSHandler{}
	jwksSrv := httptest.NewServer(h)
	t.Cleanup(jwksSrv.Close)

	// ExtAuthURL points at an address that will fail if hit (non-routable
	// loopback port we never listen on). If the override takes effect we
	// never actually try to contact it.
	cfg := webui.ServerConfig{
		ExtAuthURL:     "http://127.0.0.1:1", // unused for JWKS fetches
		ExtAuthJWKSURL: jwksSrv.URL + "/jwks",
		Logger:         newDiscardLogger(),
	}

	origLogger := logger
	logger = newDiscardLogger()
	t.Cleanup(func() { logger = origLogger })

	mw, cleanup := initExtAuth(cfg)
	if mw == nil {
		t.Fatal("expected non-nil middleware when ExtAuthURL is set")
	}
	t.Cleanup(func() {
		if cleanup != nil {
			cleanup()
		}
	})

	h.waitForCall(t, 1, 2*time.Second)

	paths := h.snapshotPaths()
	if len(paths) == 0 {
		t.Fatal("expected JWKS fetch on override server")
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "/jwks") {
			t.Fatalf("expected path ending in /jwks, got %q", p)
		}
	}
}
