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

// TestCodexSessionWalkRootsSpansBothZones pins the timezone hazard: `since` is
// a session's UTC StartedAt while `now` is local and codex names its day
// directories in local time, so a single-zone (or worse, mixed-zone) span can
// miss the rollout entirely. Both the local-named and the UTC-named day must be
// walked, in chronological order.
func TestCodexSessionWalkRootsSpansBothZones(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "2026", "07", "25"))
	mustMkdir(t, filepath.Join(root, "2026", "07", "26"))

	// 00:00:55 local on the 26th in UTC+2 is 22:00:55Z on the 25th: codex files
	// this rollout under 2026/07/26, the UTC date says 2026/07/25.
	plusTwo := time.FixedZone("UTC+2", 2*60*60)
	since := time.Date(2026, 7, 25, 22, 0, 55, 0, time.UTC)
	now := time.Date(2026, 7, 26, 0, 5, 0, 0, plusTwo)

	got := codexSessionWalkRoots(root, since, now)
	want := []string{
		filepath.Join(root, "2026", "07", "25"),
		filepath.Join(root, "2026", "07", "26"),
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
