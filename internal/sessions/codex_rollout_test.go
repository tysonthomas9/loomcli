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

func TestCodexSessionWalkRootsUsesLocalCalendarDateForUTCStart(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "2026", "08", "01"))
	mustMkdir(t, filepath.Join(root, "2026", "08", "02"))

	local := time.FixedZone("PDT", -7*60*60)
	// The same run is August 2 in persisted UTC metadata but August 1 in the
	// local calendar layout used by Codex.
	since := time.Date(2026, 8, 2, 4, 30, 31, 0, time.UTC)
	now := time.Date(2026, 8, 1, 21, 35, 15, 0, local)

	got := codexSessionWalkRoots(root, since, now)
	want := []string{filepath.Join(root, "2026", "08", "01")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want local-date roots %#v", got, want)
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
