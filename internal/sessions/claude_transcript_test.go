package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEncodeClaudeCWD pins the encoding to Claude Code's actual project-dir
// convention with hardcoded expectations — every non-alphanumeric char becomes
// '-'. The other tests place fixtures via encodeClaudeCWD itself, so they stay
// self-consistent under any encoding and cannot catch a convention drift; this
// one does. The first case is the real-world regression: a path under ~/.loom
// must encode the dot, or the project dir is never found and the transcript is
// empty for every fleet-mode run.
func TestEncodeClaudeCWD(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"/Users/oleh/.loom/workspaces/WEB-EXTRACTOR-NEW/worktrees/tree-clustering/stacy-planner",
			"-Users-oleh--loom-workspaces-WEB-EXTRACTOR-NEW-worktrees-tree-clustering-stacy-planner",
		},
		// Dot-free path is unchanged — guards against over-replacement.
		{"/Users/oleh/Work/aether", "-Users-oleh-Work-aether"},
		// Existing hyphens are preserved 1:1 (no collapsing of "--").
		{"/x/a-b/c", "-x-a-b-c"},
		// Multiple dots each map to a hyphen.
		{"/a/.b.c/d", "-a--b-c-d"},
	}
	for _, tc := range cases {
		if got := encodeClaudeCWD(tc.in); got != tc.want {
			t.Errorf("encodeClaudeCWD(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
		}
	}
}

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
