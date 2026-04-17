package sessions

import (
	"os"
	"path/filepath"
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
