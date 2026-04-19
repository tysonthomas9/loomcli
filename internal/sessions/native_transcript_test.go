package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func newStoreWithSession(t *testing.T, sessionID string) (*Store, string) {
	t.Helper()
	beadsDir := t.TempDir()
	store, err := NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sessDir := filepath.Join(store.Dir(), sessionID)
	if err := os.MkdirAll(sessDir, sessDirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return store, sessDir
}

func TestSyncNativeTranscript_CopiesContent(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	src := filepath.Join(t.TempDir(), "native.jsonl")
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"hi"}}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := store.SyncNativeTranscript(sid, src); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestSyncNativeTranscript_EmptySrcPathIsNoop(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	if err := store.SyncNativeTranscript(sid, ""); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSyncNativeTranscript_MissingSrcIsNoop(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	if err := store.SyncNativeTranscript(sid, "/nonexistent/x.jsonl"); err != nil {
		t.Errorf("want nil for missing src, got %v", err)
	}
}

func TestSyncNativeTranscript_RejectsPathTraversal(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SyncNativeTranscript("../evil", "/tmp/doesnt-matter"); err == nil {
		t.Errorf("want error for traversal, got nil")
	}
}

func TestSyncNativeTranscript_RejectsMissingSession(t *testing.T) {
	beadsDir := t.TempDir()
	store, err := NewStore(beadsDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	src := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript("nonexistent-session", src); err == nil {
		t.Errorf("want error for missing session, got nil")
	}
}

func TestSyncNativeTranscript_OverwritesOnChange(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)

	src := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(src, []byte("line1\n"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript(sid, src); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Append to src and sync again.
	if err := os.WriteFile(src, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatalf("rewrite src: %v", err)
	}
	if err := store.SyncNativeTranscript(sid, src); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "line1\nline2\n" {
		t.Errorf("got %q, want %q", got, "line1\nline2\n")
	}
}

func TestNativeTranscriptPath(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	want := filepath.Join(store.Dir(), sid, NativeTranscriptFile)
	if got := store.NativeTranscriptPath(sid); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
