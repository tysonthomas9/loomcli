package sessions

import (
	"fmt"
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

func TestCodexSessionWalkRootsUsesLocalDateForUTCBoundary(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "2026", "05", "30"))
	mustMkdir(t, filepath.Join(root, "2026", "05", "31"))
	local := time.FixedZone("PDT", -7*60*60)
	since := time.Date(2026, 5, 31, 5, 33, 41, 0, time.UTC)
	now := since.In(local).Add(10 * time.Second)

	got := codexSessionWalkRoots(root, since, now)
	want := []string{filepath.Join(root, "2026", "05", "30")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func TestCodexRolloutMatchesWorkDirResolvesSymlinkedCWD(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "private", "var", "folders")
	mustMkdir(t, realParent)
	linkParent := filepath.Join(root, "var")
	if err := os.Symlink(filepath.Join(root, "private", "var"), linkParent); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	workDirViaLink := filepath.Join(linkParent, "folders", "repo")
	realWorkDir := filepath.Join(realParent, "repo")
	mustMkdir(t, realWorkDir)
	resolvedWorkDir, err := filepath.EvalSymlinks(workDirViaLink)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}

	rolloutPath := filepath.Join(root, "rollout-test.jsonl")
	content := fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`+"\n", resolvedWorkDir)
	if err := os.WriteFile(rolloutPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	if !codexRolloutMatchesWorkDir(rolloutPath, workDirViaLink) {
		t.Fatalf("expected rollout cwd %q to match symlinked workdir %q", resolvedWorkDir, workDirViaLink)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
