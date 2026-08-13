package cleanup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupSessionsIsExplicitBoundedAndDoesNotCreateArchive(t *testing.T) {
	runtimeDir := t.TempDir()
	purged, err := cleanupSessions(t.Context(), runtimeDir, time.Hour, false)
	if err != nil || purged != 0 {
		t.Fatalf("empty cleanupSessions = %d, %v", purged, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("cleanup created retired archive: %v", err)
	}

	root := filepath.Join(runtimeDir, "sessions")
	old := filepath.Join(root, "old-session")
	fresh := filepath.Join(root, "fresh-session")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fresh, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	preview, err := cleanupSessions(t.Context(), runtimeDir, 24*time.Hour, true)
	if err != nil || preview != 1 {
		t.Fatalf("preview cleanupSessions = %d, %v", preview, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("preview removed old archive: %v", err)
	}
	removed, err := cleanupSessions(t.Context(), runtimeDir, 24*time.Hour, false)
	if err != nil || removed != 1 {
		t.Fatalf("cleanupSessions = %d, %v", removed, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old archive remains: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh archive removed: %v", err)
	}
}
