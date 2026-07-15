package frontendassets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashDirDeterministicAndChangesWithAssets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<div id=\"root\"></div>")
	writeFile(t, filepath.Join(dir, "assets", "app.js"), "console.log('v1')")
	writeFile(t, filepath.Join(dir, ".build-meta"), `{"built_at":"now","git_hash":"abc"}`)

	first, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir() error = %v", err)
	}
	second, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir() second error = %v", err)
	}
	if first != second {
		t.Fatalf("HashDir() is not deterministic: %q != %q", first, second)
	}

	writeFile(t, filepath.Join(dir, "assets", "app.js"), "console.log('v2')")
	changed, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir() after change error = %v", err)
	}
	if changed == first {
		t.Fatal("HashDir() did not change after asset content changed")
	}
}

func TestReadBuildInfoIncludesMetaWhenPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<div id=\"root\"></div>")
	writeFile(t, filepath.Join(dir, ".build-meta"), `{"built_at":"2026-06-19T00:00:00Z","git_hash":"abc123"}`)

	info := ReadBuildInfo(dir, "build123")
	if info.FrontendHash == "" {
		t.Fatal("ReadBuildInfo() FrontendHash is empty")
	}
	if info.Build != "build123" {
		t.Fatalf("Build = %q, want build123", info.Build)
	}
	if info.GitHash != "abc123" {
		t.Fatalf("GitHash = %q, want abc123", info.GitHash)
	}
	if info.BuiltAt != "2026-06-19T00:00:00Z" {
		t.Fatalf("BuiltAt = %q", info.BuiltAt)
	}
}

func TestHashDirRequiresIndex(t *testing.T) {
	_, err := HashDir(t.TempDir())
	if err == nil {
		t.Fatal("HashDir() error = nil, want missing index error")
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
