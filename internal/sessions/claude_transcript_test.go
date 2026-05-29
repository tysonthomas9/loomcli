package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeClaudeTranscript(t *testing.T, home, workDir, uuid string, mod time.Time, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", encodeClaudeCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	p := filepath.Join(dir, uuid+".jsonl")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
}

func TestSyncLatestClaudeTranscript_ByUUID(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	workDir := "/work/tree/agent"
	want := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	writeClaudeTranscript(t, home, workDir, "uuid-1111", time.Now(), want)
	// A decoy file the UUID match must ignore.
	writeClaudeTranscript(t, home, workDir, "uuid-2222", time.Now(), "DECOY\n")

	got, err := store.SyncLatestClaudeTranscript(sid, workDir, "uuid-1111", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("SyncLatestClaudeTranscript: %v", err)
	}
	if filepath.Base(got) != "uuid-1111.jsonl" {
		t.Fatalf("resolved %q, want uuid-1111.jsonl", got)
	}
	data, err := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != want {
		t.Errorf("transcript content = %q, want %q", data, want)
	}
}

func TestSyncLatestClaudeTranscript_FallbackNewestAfterSince(t *testing.T) {
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	workDir := "/work/tree/agent"
	since := time.Now()
	// Pre-session file (older than the skew margin) must be ignored; the
	// newest file written after the session start wins.
	writeClaudeTranscript(t, home, workDir, "old", since.Add(-10*time.Minute), "OLD\n")
	writeClaudeTranscript(t, home, workDir, "new", since.Add(2*time.Minute), "NEW\n")

	got, err := store.SyncLatestClaudeTranscript(sid, workDir, "", since)
	if err != nil {
		t.Fatalf("SyncLatestClaudeTranscript: %v", err)
	}
	if filepath.Base(got) != "new.jsonl" {
		t.Errorf("matched %q, want new.jsonl", got)
	}
	data, _ := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
	if string(data) != "NEW\n" {
		t.Errorf("content = %q, want NEW", data)
	}
}

func TestSyncLatestClaudeTranscript_MissingIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	got, err := store.SyncLatestClaudeTranscript(sid, "/work/tree/agent", "missing", time.Now())
	if err != nil {
		t.Fatalf("want nil err, got %v", err)
	}
	if got != "" {
		t.Errorf("want empty path, got %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(sessDir, NativeTranscriptFile)); !os.IsNotExist(statErr) {
		t.Errorf("expected no transcript written, stat err=%v", statErr)
	}
}
