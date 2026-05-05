package sessions

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCodexSessionWalkRootsUsesRelevantDateDirs(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "2026", "05", "04"))
	mustMkdir(t, filepath.Join(root, "2026", "05", "05"))
	mustMkdir(t, filepath.Join(root, "2026", "04", "30"))

	since := time.Date(2026, 5, 5, 0, 0, 30, 0, time.UTC)
	now := time.Date(2026, 5, 5, 0, 5, 0, 0, time.UTC)

	got := codexSessionWalkRoots(root, since, now)
	want := []string{
		filepath.Join(root, "2026", "05", "04"),
		filepath.Join(root, "2026", "05", "05"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func TestCodexSessionWalkRootsFallsBackForFlatLayout(t *testing.T) {
	root := t.TempDir()
	since := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	got := codexSessionWalkRoots(root, since, since.Add(time.Minute))
	want := []string{root}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func TestCodexSessionWalkRootsDoesNotScanWholeDateLayoutWhenCurrentDirMissing(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "2026", "04", "30"))
	since := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	got := codexSessionWalkRoots(root, since, since.Add(time.Minute))
	if len(got) != 0 {
		t.Fatalf("roots = %#v, want none", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
