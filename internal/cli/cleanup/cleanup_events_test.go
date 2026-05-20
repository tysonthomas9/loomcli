package cleanup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestEventFileRe_Matches(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantDay string
	}{
		{"standard", "events-2025-01-15.jsonl", true, "2025-01-15"},
		{"rotation_1", "events-2025-01-15.jsonl.1", true, "2025-01-15"},
		{"rotation_2", "events-2025-01-15.jsonl.2", true, "2025-01-15"},
		{"rotation_99", "events-2025-03-20.jsonl.99", true, "2025-03-20"},
		{"bad_date", "events-bad-date.jsonl", false, ""},
		{"no_prefix", "2025-01-15.jsonl", false, ""},
		{"wrong_ext", "events-2025-01-15.txt", false, ""},
		{"dir_name", "events-2025-01-15", false, ""},
		{"empty", "", false, ""},
		{"random", "something.jsonl", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := eventFileRe.FindStringSubmatch(tt.input)
			gotOK := matches != nil
			if gotOK != tt.wantOK {
				t.Errorf("eventFileRe match %q = %v, want %v", tt.input, gotOK, tt.wantOK)
			}
			if gotOK && matches[1] != tt.wantDay {
				t.Errorf("eventFileRe date for %q = %q, want %q", tt.input, matches[1], tt.wantDay)
			}
		})
	}
}

func TestPurgeEventFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	purged, err := purgeEventFiles(dir, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0", purged)
	}
}

func TestPurgeEventFiles_NonexistentDir(t *testing.T) {
	purged, err := purgeEventFiles("/nonexistent/path/events", 24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0", purged)
	}
}

func TestPurgeEventFiles_DeletesOldFiles(t *testing.T) {
	dir := t.TempDir()

	// Create event files: 2 old (60 days ago, 45 days ago), 1 recent (2 days ago).
	oldDate1 := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	oldDate2 := time.Now().UTC().AddDate(0, 0, -45).Format("2006-01-02")
	recentDate := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")

	for _, dateStr := range []string{oldDate1, oldDate2, recentDate} {
		fname := "events-" + dateStr + ".jsonl"
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := purgeEventFiles(dir, 30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 2 {
		t.Errorf("purged = %d, want 2 (two old files)", purged)
	}

	// Verify old files are gone, recent remains.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("remaining files = %d, want 1", len(entries))
	}
	want := "events-" + recentDate + ".jsonl"
	if entries[0].Name() != want {
		t.Errorf("remaining file = %q, want %q", entries[0].Name(), want)
	}
}

func TestPurgeEventFiles_RotationBackups(t *testing.T) {
	dir := t.TempDir()

	oldDate := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	base := "events-" + oldDate + ".jsonl"
	rot1 := base + ".1"
	rot2 := base + ".2"

	for _, fname := range []string{base, rot1, rot2} {
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := purgeEventFiles(dir, 30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 3 {
		t.Errorf("purged = %d, want 3 (base + 2 rotation files)", purged)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("remaining files = %d, want 0", len(entries))
	}
}

func TestPurgeEventFiles_TodayNeverDeleted(t *testing.T) {
	dir := t.TempDir()

	todayStr := time.Now().UTC().Format("2006-01-02")
	fname := "events-" + todayStr + ".jsonl"
	if err := os.WriteFile(filepath.Join(dir, fname), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Even with 0 retention (purge everything), today's file is kept.
	purged, err := purgeEventFiles(dir, 0, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 (today is never deleted)", purged)
	}

	if _, err := os.Stat(filepath.Join(dir, fname)); err != nil {
		t.Errorf("today's file should still exist: %v", err)
	}
}

func TestPurgeEventFiles_BadDateSkipped(t *testing.T) {
	dir := t.TempDir()

	// Create a file that does not match the regex at all.
	if err := os.WriteFile(filepath.Join(dir, "events-bad-date.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also create a file with valid date format to ensure normal processing works.
	oldDate := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	if err := os.WriteFile(filepath.Join(dir, "events-"+oldDate+".jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	purged, err := purgeEventFiles(dir, 30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1 (bad-date file should be skipped)", purged)
	}

	// bad-date file should still exist.
	if _, err := os.Stat(filepath.Join(dir, "events-bad-date.jsonl")); err != nil {
		t.Errorf("events-bad-date.jsonl should still exist: %v", err)
	}
}

func TestPurgeEventFiles_DryRun(t *testing.T) {
	dir := t.TempDir()

	oldDate := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	base := "events-" + oldDate + ".jsonl"
	rot1 := base + ".1"

	for _, fname := range []string{base, rot1} {
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Dry-run should count but not delete.
	purged, err := purgeEventFiles(dir, 30*24*time.Hour, true)
	if err != nil {
		t.Fatalf("purgeEventFiles dry-run: %v", err)
	}
	if purged != 2 {
		t.Errorf("dry-run purged = %d, want 2", purged)
	}

	// Files should still exist.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("dry-run: remaining files = %d, want 2 (files not deleted)", len(entries))
	}
}

func TestPurgeEventFiles_AllRecentKept(t *testing.T) {
	dir := t.TempDir()

	// Create event files within retention window.
	for i := 1; i <= 5; i++ {
		dateStr := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		fname := "events-" + dateStr + ".jsonl"
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := purgeEventFiles(dir, 30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 (all within retention)", purged)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 5 {
		t.Errorf("remaining files = %d, want 5", len(entries))
	}
}

func TestCleanupEventsResolvesDefaultEventsDir(t *testing.T) {
	runtimeDir := t.TempDir()
	eventsDir := filepath.Join(runtimeDir, ".loom", "events")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("mkdir events: %v", err)
	}
	oldDate := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	oldPath := filepath.Join(eventsDir, "events-"+oldDate+".jsonl")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(runtimeDir, "config"))
	t.Setenv("LOOM_WORKSPACE", "")
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	if got := resolveEventsDir(); got != eventsDir {
		t.Fatalf("resolveEventsDir = %q, want %q", got, eventsDir)
	}
	purged, err := cleanupEvents(30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("cleanupEvents: %v", err)
	}
	if purged != 1 {
		t.Fatalf("cleanupEvents purged = %d, want 1", purged)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old event still exists or unexpected stat err: %v", err)
	}
}

func TestPurgeEventFiles_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()

	// Create a subdirectory matching the event pattern name.
	oldDate := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	subDir := filepath.Join(dir, "events-"+oldDate+".jsonl")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	purged, err := purgeEventFiles(dir, 30*24*time.Hour, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 (directories are skipped)", purged)
	}

	// Directory should still exist.
	if _, err := os.Stat(subDir); err != nil {
		t.Errorf("directory should still exist: %v", err)
	}
}

func TestPurgeEventFiles_NonEventFilesIgnored(t *testing.T) {
	dir := t.TempDir()

	// Create non-event files.
	for _, fname := range []string{"config.yaml", "readme.txt", "data.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := purgeEventFiles(dir, 0, false)
	if err != nil {
		t.Fatalf("purgeEventFiles: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0 (non-event files should be ignored)", purged)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("remaining files = %d, want 3", len(entries))
	}
}
