package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStoreWithSession(t *testing.T, sessionID string) (*Store, string) {
	t.Helper()
	runtimeDir := t.TempDir()
	store, err := NewStore(runtimeDir)
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

	if err := store.SyncNativeTranscript(sid, src, TranscriptFormatRaw); err != nil {
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

func TestSyncNativeTranscriptBytes_CopiesContent(t *testing.T) {
	const sid = "20260417-120000-opencode-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)
	payload := []byte(`{"info":{"id":"ses_123"},"messages":[]}` + "\n")

	if err := store.SyncNativeTranscriptBytes(sid, payload); err != nil {
		t.Fatalf("SyncNativeTranscriptBytes: %v", err)
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
	if err := store.SyncNativeTranscript(sid, "", TranscriptFormatRaw); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSyncNativeTranscript_MissingSrcIsNoop(t *testing.T) {
	const sid = "20260417-120000-claude-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	if err := store.SyncNativeTranscript(sid, "/nonexistent/x.jsonl", TranscriptFormatRaw); err != nil {
		t.Errorf("want nil for missing src, got %v", err)
	}
}

func TestSyncNativeTranscript_RejectsPathTraversal(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SyncNativeTranscript("../evil", "/tmp/doesnt-matter", TranscriptFormatRaw); err == nil {
		t.Errorf("want error for traversal, got nil")
	}
}

func TestSyncNativeTranscript_RejectsMissingSession(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	src := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := store.SyncNativeTranscript("nonexistent-session", src, TranscriptFormatRaw); err == nil {
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
	if err := store.SyncNativeTranscript(sid, src, TranscriptFormatRaw); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Append to src and sync again.
	if err := os.WriteFile(src, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatalf("rewrite src: %v", err)
	}
	if err := store.SyncNativeTranscript(sid, src, TranscriptFormatRaw); err != nil {
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

// TestSyncNativeTranscript_RedactsRawNotCanonical pins the single-redaction policy:
// a raw backend stream is redacted here (its only redaction), while a canonical
// stream is left untouched because the TS leaf already redacted it at the source —
// re-redacting would be a duplicate pass. The raw branch also proves the chosen
// secret is one the redactor actually catches, so the canonical branch's "survives"
// assertion can't pass for the wrong reason.
func TestSyncNativeTranscript_RedactsRawNotCanonical(t *testing.T) {
	const secret = "sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA" // Anthropic key shape the redactor catches
	payload := []byte(`{"role":"assistant","type":"text","text":"key is ` + secret + `"}` + "\n")

	read := func(t *testing.T, sid, format string) string {
		t.Helper()
		store, sessDir := newStoreWithSession(t, sid)
		src := filepath.Join(t.TempDir(), "src.jsonl")
		if err := os.WriteFile(src, payload, 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if err := store.SyncNativeTranscript(sid, src, format); err != nil {
			t.Fatalf("SyncNativeTranscript(%s): %v", format, err)
		}
		got, err := os.ReadFile(filepath.Join(sessDir, NativeTranscriptFile))
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		return string(got)
	}

	if raw := read(t, "20260417-120000-codex-aaaa-0123abcd", TranscriptFormatRaw); strings.Contains(raw, secret) {
		t.Errorf("raw transcript must be redacted; secret leaked through: %s", raw)
	}
	if canon := read(t, "20260417-120000-codex-bbbb-0123abcd", TranscriptFormatCanonical); !strings.Contains(canon, secret) {
		t.Errorf("canonical transcript must NOT be re-redacted (leaf already did); want the bytes verbatim, got: %s", canon)
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
