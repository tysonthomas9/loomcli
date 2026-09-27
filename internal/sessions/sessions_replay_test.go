//go:build sessionsreplay

package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// X2: a healed terminal outcome must not be replaced by a late local finalize.
func TestSessionsReplayX2TerminalOnce(t *testing.T) {
	store := createTestStore(t)
	session := createTestSession(t, store, "worker", "codex")
	if err := session.Finalize(FinalizeOptions{TaskID: "task-1", ExitCode: 1}); err != nil {
		t.Fatal(err)
	}
	_ = session.Finalize(FinalizeOptions{TaskID: "task-1", ExitCode: 0})
	record, err := store.LoadMetadata(session.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusFailed {
		t.Fatalf("X2: terminal status changed to %q; first failed outcome must win", record.Status)
	}
}

// #175: a canonical transcript reaching the Go persistence sink may still
// contain a secret, regardless of whether the leaf normally redacts it.
func TestSessionsReplay175CanonicalSinkRedaction(t *testing.T) {
	const secret = "sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA"
	store, dir := newStoreWithSession(t, "20260417-120000-codex-aaaa-0123abcd")
	source := filepath.Join(t.TempDir(), "source.jsonl")
	content := []byte(`{"role":"assistant","type":"text","text":"key is ` + secret + `"}` + "\n")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncNativeTranscript("20260417-120000-codex-aaaa-0123abcd", source, TranscriptFormatCanonical); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, NativeTranscriptFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), secret) {
		t.Fatal("#175: canonical transcript sink persisted an unredacted secret")
	}
}
