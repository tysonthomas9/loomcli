package misc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleBuildInfo(t *testing.T) {
	dir := t.TempDir()
	writeBuildInfoFile(t, filepath.Join(dir, "index.html"), "<div id=\"root\"></div>")
	writeBuildInfoFile(t, filepath.Join(dir, ".build-meta"), `{"built_at":"2026-06-19T00:00:00Z","git_hash":"abc123"}`)

	rec := httptest.NewRecorder()
	HandleBuildInfo(dir, "build123").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/build-info", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["frontend_hash"] == "" {
		t.Fatal("frontend_hash is empty")
	}
	if body["build"] != "build123" {
		t.Fatalf("build = %q, want build123", body["build"])
	}
	if body["git_hash"] != "abc123" {
		t.Fatalf("git_hash = %q, want abc123", body["git_hash"])
	}
	if body["built_at"] != "2026-06-19T00:00:00Z" {
		t.Fatalf("built_at = %q", body["built_at"])
	}
}

func writeBuildInfoFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
