package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLatestHarnessSessionID_Claude covers the resolution the daemon supervisor
// depends on: after the worker is reaped there is no in-process session id left,
// so the id has to come back out of the transcript Claude Code wrote for itself.
func TestLatestHarnessSessionID_Claude(t *testing.T) {
	const (
		older = "aaaaaaaa-1111-2222-3333-444444444444"
		newer = "bbbbbbbb-1111-2222-3333-444444444444"
	)
	workDir := "/tmp/loom-harness-id/falcon"
	start := time.Now().Add(-10 * time.Minute)

	tests := []struct {
		name    string
		hint    string
		workDir string
		since   time.Time
		want    string
	}{
		{
			name:    "no hint falls back to the newest transcript since session start",
			workDir: workDir,
			since:   start,
			want:    newer,
		},
		{
			name:    "a carried session id wins over the mtime scan",
			hint:    older,
			workDir: workDir,
			since:   start,
			want:    older,
		},
		{
			name:    "a hint with no file on disk falls back rather than trusting it",
			hint:    "cccccccc-1111-2222-3333-444444444444",
			workDir: workDir,
			since:   start,
			want:    newer,
		},
		{
			name:    "nothing written since the cutoff yields no id",
			workDir: workDir,
			since:   time.Now().Add(2 * time.Hour),
			want:    "",
		},
		{
			name:    "a working directory with no project dir yields no id",
			workDir: "/tmp/loom-harness-id/never-ran",
			since:   start,
			want:    "",
		},
		{
			name:    "an empty working directory yields no id",
			workDir: "",
			since:   start,
			want:    "",
		},
	}

	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	projectDir := filepath.Join(configDir, "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	writeStamped(t, filepath.Join(projectDir, older+".jsonl"), time.Now().Add(-5*time.Minute))
	writeStamped(t, filepath.Join(projectDir, newer+".jsonl"), time.Now().Add(-1*time.Minute))
	// A non-transcript neighbor must never be mistaken for a session.
	writeStamped(t, filepath.Join(projectDir, "notes.txt"), time.Now())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LatestHarnessSessionID("claude", tc.workDir, tc.hint, tc.since); got != tc.want {
				t.Errorf("LatestHarnessSessionID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLatestHarnessSessionID_Codex pins the filename split for Codex, whose
// rollouts are named rollout-<timestamp>-<uuid>.jsonl — the timestamp carries
// hyphens of its own, so only the UUID shape identifies the boundary.
func TestLatestHarnessSessionID_Codex(t *testing.T) {
	const sessionID = "dddddddd-1111-2222-3333-444444444444"
	workDir := t.TempDir()

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	root := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	// Codex indexes by session id, so the resolver matches on the rollout's own
	// session_meta cwd rather than on a path convention.
	body := `{"type":"session_meta","payload":{"cwd":"` + workDir + `"}}` + "\n"
	rollout := filepath.Join(root, "rollout-2026-08-02T10-15-00-"+sessionID+".jsonl")
	if err := os.WriteFile(rollout, []byte(body), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	got := LatestHarnessSessionID("codex", workDir, "", time.Now().Add(-time.Hour))
	if got != sessionID {
		t.Errorf("LatestHarnessSessionID = %q, want %q", got, sessionID)
	}

	// A rollout belonging to a different worktree must not be attributed here.
	if got := LatestHarnessSessionID("codex", filepath.Join(workDir, "other"), "", time.Now().Add(-time.Hour)); got != "" {
		t.Errorf("LatestHarnessSessionID for a foreign workdir = %q, want \"\"", got)
	}
}

// TestLatestHarnessSessionID_UnknownBackend keeps the resolver honest about
// backends that keep no readable transcript at all.
func TestLatestHarnessSessionID_UnknownBackend(t *testing.T) {
	for _, backend := range []string{"", "opencode", "echo", "external"} {
		if got := LatestHarnessSessionID(backend, "/tmp/whatever", "", time.Time{}); got != "" {
			t.Errorf("LatestHarnessSessionID(%q) = %q, want \"\"", backend, got)
		}
	}
}

func writeStamped(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
