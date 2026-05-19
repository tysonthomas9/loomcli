package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncSubagentTranscript_CopiesContent(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	const subID = "abc123def456"
	store, sessDir := newStoreWithSession(t, sid)

	src := filepath.Join(t.TempDir(), "agent-"+subID+".jsonl")
	payload := []byte(`{"type":"user","uuid":"u1","message":{"content":"hello"}}` + "\n")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := store.SyncSubagentTranscript(sid, subID, src); err != nil {
		t.Fatalf("SyncSubagentTranscript: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(sessDir, "subagents", "agent-"+subID+".jsonl"))
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestSyncSubagentTranscript_EmptyIsNoop(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	if err := store.SyncSubagentTranscript(sid, "", "/some/path"); err != nil {
		t.Errorf("empty subID: got %v, want nil", err)
	}
	if err := store.SyncSubagentTranscript(sid, "abc", ""); err != nil {
		t.Errorf("empty srcPath: got %v, want nil", err)
	}
}

func TestSyncSubagentTranscript_RejectsBadSubagentID(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	src := filepath.Join(t.TempDir(), "x.jsonl")
	_ = os.WriteFile(src, []byte("{}"), 0o600)
	if err := store.SyncSubagentTranscript(sid, "../bad", src); err == nil {
		t.Errorf("want error for path traversal in subID, got nil")
	}
	if err := store.SyncSubagentTranscript(sid, "has spaces", src); err == nil {
		t.Errorf("want error for invalid subID chars, got nil")
	}
}

// SubagentTranscriptPath is an unvalidated primitive — it interpolates the
// ID straight into the filename. Callers must gate on SubagentIDPattern.
// This test pins the escape behavior so anyone calling the primitive
// without validation inherits a path traversal.
func TestSubagentTranscriptPath_UnvalidatedPrimitiveCanEscapeStoreDir(t *testing.T) {
	store := &Store{dir: "/var/loom/sessions"}
	got := store.SubagentTranscriptPath("sess-abc", "../../../../../../etc/passwd")
	cleaned := filepath.Clean(got)
	storePrefix := filepath.Clean("/var/loom/sessions") + "/"
	if len(cleaned) >= len(storePrefix) && cleaned[:len(storePrefix)] == storePrefix {
		t.Errorf("primitive unexpectedly stayed inside store dir; traversal blocked: %q", cleaned)
	}
}

func TestSubagentIDPattern_RejectsUnsafeInputs(t *testing.T) {
	bad := []string{
		"../../../etc/passwd",
		"..",
		".",
		"foo/bar",
		"foo\\bar",
		"foo-bar",
		"foo.jsonl",
		"",
		"foo\x00bar",
	}
	for _, s := range bad {
		if SubagentIDPattern.MatchString(s) {
			t.Errorf("SubagentIDPattern accepted %q (should reject)", s)
		}
	}
}

func TestSubagentIDPattern_AcceptsValidIDs(t *testing.T) {
	good := []string{"abc123", "deadbeef", "A1B2C3", "0"}
	for _, s := range good {
		if !SubagentIDPattern.MatchString(s) {
			t.Errorf("SubagentIDPattern rejected valid ID %q", s)
		}
	}
}

func TestListSubagentTranscripts_ReturnsEmpty(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)
	names, err := store.ListSubagentTranscripts(sid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("want empty, got %v", names)
	}
}

func TestListSubagentTranscripts_ReturnsCaptured(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, _ := newStoreWithSession(t, sid)

	for _, subID := range []string{"a111", "b222", "c333"} {
		src := filepath.Join(t.TempDir(), "agent-"+subID+".jsonl")
		if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if err := store.SyncSubagentTranscript(sid, subID, src); err != nil {
			t.Fatalf("sync %s: %v", subID, err)
		}
	}

	names, err := store.ListSubagentTranscripts(sid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("want 3, got %d: %v", len(names), names)
	}
}

func TestSyncSubagentTranscriptValidationAndMissingBranches(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)
	src := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(src, []byte(`{"message":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := store.SyncSubagentTranscript(sid+"/bad", "abc123", src); err == nil || !strings.Contains(err.Error(), "path separator") {
		t.Fatalf("invalid session ID err = %v", err)
	}
	if err := store.SyncSubagentTranscript("missing-session", "abc123", src); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing session err = %v", err)
	}
	if err := store.SyncSubagentTranscript(sid, "abc123", filepath.Join(t.TempDir(), "missing.jsonl")); err != nil {
		t.Fatalf("missing source should be no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessDir, "subagents", "agent-abc123.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("missing source created destination, stat err = %v", err)
	}
}

func TestListSubagentTranscriptsValidationAndFiltering(t *testing.T) {
	const sid = "20260417-120000-nova-abcd-0123abcd"
	store, sessDir := newStoreWithSession(t, sid)
	if _, err := store.ListSubagentTranscripts(sid + "/bad"); err == nil || !strings.Contains(err.Error(), "invalid session ID") {
		t.Fatalf("invalid session ID err = %v", err)
	}

	dir := filepath.Join(sessDir, "subagents")
	if err := os.MkdirAll(filepath.Join(dir, "agent-dir.jsonl"), 0o700); err != nil {
		t.Fatalf("mkdir fake transcript dir: %v", err)
	}
	for _, name := range []string{"agent-good123.jsonl", "agent-no-suffix.txt", "prefix-agent-bad.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	names, err := store.ListSubagentTranscripts(sid)
	if err != nil {
		t.Fatalf("ListSubagentTranscripts: %v", err)
	}
	if len(names) != 1 || names[0] != "agent-good123.jsonl" {
		t.Fatalf("filtered names = %+v, want only agent-good123.jsonl", names)
	}
}
