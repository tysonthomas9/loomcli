package sessions

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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

func TestFindLatestCodexRolloutMatchesWorkDirAndSyncs(t *testing.T) {
	codeHome := t.TempDir()
	sessionsRoot := filepath.Join(codeHome, "sessions")
	dayDir := filepath.Join(sessionsRoot, "2026", "05", "05")
	mustMkdir(t, dayDir)
	workDir := t.TempDir()
	otherDir := t.TempDir()
	since := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	oldPath := filepath.Join(dayDir, "rollout-old.jsonl")
	writeRollout(t, oldPath, workDir)
	setModTime(t, oldPath, since.Add(10*time.Second))
	bestPath := filepath.Join(dayDir, "rollout-best.jsonl")
	writeRollout(t, bestPath, workDir)
	setModTime(t, bestPath, since.Add(20*time.Second))
	otherPath := filepath.Join(dayDir, "rollout-other.jsonl")
	writeRollout(t, otherPath, otherDir)
	setModTime(t, otherPath, since.Add(30*time.Second))
	ignored := filepath.Join(dayDir, "notes.jsonl")
	if err := os.WriteFile(ignored, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write ignored: %v", err)
	}

	got, err := findLatestCodexRollout(sessionsRoot, workDir, since)
	if err != nil {
		t.Fatalf("findLatestCodexRollout: %v", err)
	}
	if got != bestPath {
		t.Fatalf("findLatestCodexRollout = %q, want %q", got, bestPath)
	}
	if !codexRolloutMatchesWorkDir(bestPath, workDir) || codexRolloutMatchesWorkDir(bestPath, otherDir) {
		t.Fatalf("codexRolloutMatchesWorkDir classification failed")
	}
	if codexRolloutMatchesWorkDir(filepath.Join(dayDir, "missing.jsonl"), workDir) {
		t.Fatalf("missing rollout matched workdir")
	}
	if !sameCleanPath(filepath.Join(workDir, "."), workDir) || sameCleanPath("", workDir) {
		t.Fatalf("sameCleanPath classification failed")
	}

	t.Setenv("CODEX_HOME", codeHome)
	store, sessDir := newStoreWithSession(t, "20260417-120000-codex-abcd-0123abcd")
	synced, err := store.SyncLatestCodexRollout("20260417-120000-codex-abcd-0123abcd", workDir, since)
	if err != nil {
		t.Fatalf("SyncLatestCodexRollout: %v", err)
	}
	if synced != bestPath {
		t.Fatalf("synced path = %q, want %q", synced, bestPath)
	}
	data, err := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
	if err != nil {
		t.Fatalf("read synced transcript: %v", err)
	}
	if !strings.Contains(string(data), "session_meta") {
		t.Fatalf("synced transcript = %q, want rollout content", data)
	}
}

func TestCodexSessionsRootMissingAndFindLatestNoMatches(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if got := codexSessionsRoot(); got != "" {
		t.Fatalf("codexSessionsRoot without sessions dir = %q, want empty", got)
	}
	root := t.TempDir()
	if got, err := findLatestCodexRollout(root, t.TempDir(), time.Now()); err != nil || got != "" {
		t.Fatalf("findLatestCodexRollout no matches = %q, %v; want empty", got, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeRollout(t *testing.T, path, workDir string) {
	t.Helper()
	payload := `{"type":"other","payload":{}}` + "\n" +
		`{"type":"session_meta","payload":{"cwd":` + strconv.Quote(workDir) + `}}` + "\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write rollout %s: %v", path, err)
	}
}

func setModTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
