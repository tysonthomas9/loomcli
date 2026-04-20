package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// discoverHashedJS returns the filename of a hashed JS asset from the embedded
// frontend. Filenames contain Vite build hashes, so tests discover them at
// runtime rather than hard-coding. Skips the test when the embedded dist is a
// stub (e.g. CI's Node-free decoupling smoke) — asset-specific behavior is
// exercised by the real-build frontend tests.
func discoverHashedJS(t *testing.T) string {
	t.Helper()
	assets, err := fs.Sub(FrontendFS, "frontend/dist/assets")
	if err != nil {
		t.Skipf("frontend/dist/assets not embedded (stub build): %v", err)
	}
	entries, err := fs.ReadDir(assets, ".")
	if err != nil {
		t.Skipf("ReadDir(frontend/dist/assets): %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".js") {
			return name
		}
	}
	t.Skip("no hashed .js asset found — stub embedded frontend")
	return ""
}

func TestIsStaticAsset(t *testing.T) {
	tests := map[string]bool{
		"/assets/index-abc.js":   true,
		"/assets/index-abc.css":  true,
		"/assets/index.js.map":   true,
		"/logo.png":              true,
		"/favicon.ico":           true,
		"/manifest.webmanifest":  true,
		"/fonts/Inter.woff2":     true,
		"/settings":              false,
		"/ws/abc/issues/foo":     false,
		"/":                      false,
		"/some/path/without/ext": false,
		"/capitalized.JS":        false, // path.Ext is case-sensitive in lookups
	}
	for urlPath, want := range tests {
		if got := IsStaticAsset(urlPath); got != want {
			t.Errorf("IsStaticAsset(%q) = %v, want %v", urlPath, got, want)
		}
	}
}

func TestShouldCache(t *testing.T) {
	tests := map[string]bool{
		"/":                                false,
		"/index.html":                      false,
		"/assets/index-abc123.js":          true,
		"/assets/nested/file.woff2":        true,
		"/fonts/inter-v18-latin-500.woff2": true,
		"/favicon.ico":                     false,
		"/some/other.js":                   false,
	}
	for urlPath, want := range tests {
		if got := ShouldCache(urlPath); got != want {
			t.Errorf("ShouldCache(%q) = %v, want %v", urlPath, got, want)
		}
	}
}

func TestSetCacheHeaders_Cached(t *testing.T) {
	rr := httptest.NewRecorder()
	SetCacheHeaders(rr, true)
	got := rr.Header().Get("Cache-Control")
	want := "public, max-age=31536000, immutable"
	if got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if rr.Header().Get("Pragma") != "" {
		t.Errorf("cached response should not set Pragma, got %q", rr.Header().Get("Pragma"))
	}
}

func TestSetCacheHeaders_NoCache(t *testing.T) {
	rr := httptest.NewRecorder()
	SetCacheHeaders(rr, false)
	if got, want := rr.Header().Get("Cache-Control"), "no-cache, no-store, must-revalidate"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := rr.Header().Get("Pragma"), "no-cache"; got != want {
		t.Errorf("Pragma = %q, want %q", got, want)
	}
	if got, want := rr.Header().Get("Expires"), "0"; got != want {
		t.Errorf("Expires = %q, want %q", got, want)
	}
}

func TestFrontendHandler_ServesIndex(t *testing.T) {
	h := FrontendHandler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<!DOCTYPE html") {
		t.Errorf("expected HTML doctype in body, got: %q", rr.Body.String()[:min(100, rr.Body.Len())])
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("root should use no-cache, got %q", got)
	}
}

func TestFrontendHandler_SPAFallback(t *testing.T) {
	h := FrontendHandler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("SPA fallback status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<!DOCTYPE html") {
		t.Error("SPA fallback should serve index.html")
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("SPA fallback should use no-cache, got %q", got)
	}
}

func TestFrontendHandler_HashedAssetImmutableCache(t *testing.T) {
	h := FrontendHandler()
	asset := discoverHashedJS(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/"+asset, nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (asset %q)", rr.Code, asset)
	}
	if got, want := rr.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

func TestFrontendHandler_StaticMiss404(t *testing.T) {
	h := FrontendHandler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent.js", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("static miss status = %d, want 404", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "<!DOCTYPE html") {
		t.Error("static miss must not return index.html (would break JS module loading)")
	}
}

func TestFrontendHandler_RejectsAPIPaths(t *testing.T) {
	h := FrontendHandler()

	// Also covers the double-slash bypass: path.Clean("//api/foo") → "/api/foo",
	// and the check runs AFTER clean, so this must be rejected just like /api/foo.
	cases := []string{"/api/nonexistent", "//api/config"}
	for _, p := range cases {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Errorf("%s: Content-Type = %q, want application/json", p, got)
		}
		if !strings.Contains(rr.Body.String(), `"error"`) {
			t.Errorf("%s: body = %q, want JSON error", p, rr.Body.String())
		}
	}
}

func TestLogFrontendBuildMeta_ReadsEmbeddedFile(t *testing.T) {
	// Smoke test: just ensure the function doesn't panic and can find the
	// embedded .build-meta file. The actual log output is verified by
	// integration with slog.Default in a real server run.
	LogFrontendBuildMeta()
}
