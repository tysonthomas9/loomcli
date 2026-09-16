package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureSessionRecordsBackend pins the backend stamp. The backend is the
// only thing that tells LoadNativeEvents which parser a raw provider stream
// needs; without it the dispatcher falls through to the Claude parser, and a
// codex rollout then yields zero events with no error.
func TestEnsureSessionRecordsBackend(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := store.EnsureSession("lead-fresh", "codex"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	meta, err := store.LoadMetadata("lead-fresh")
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.Backend != "codex" {
		t.Fatalf("backend = %q, want codex", meta.Backend)
	}
}

// TestEnsureSessionFillsMissingBackendOnly covers the two existing-metadata
// cases: fill a blank backend in, but never overwrite what the session's real
// owner already recorded.
func TestEnsureSessionFillsMissingBackendOnly(t *testing.T) {
	t.Run("fills a blank backend", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		if err := store.EnsureSession("lead-blank", ""); err != nil {
			t.Fatalf("first EnsureSession: %v", err)
		}
		if err := store.EnsureSession("lead-blank", "codex"); err != nil {
			t.Fatalf("second EnsureSession: %v", err)
		}
		meta, err := store.LoadMetadata("lead-blank")
		if err != nil {
			t.Fatalf("LoadMetadata: %v", err)
		}
		if meta.Backend != "codex" {
			t.Fatalf("backend = %q, want codex", meta.Backend)
		}
	})

	t.Run("preserves the owner's metadata", func(t *testing.T) {
		store, err := NewStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		sess, err := store.CreateSession(CreateOptions{AgentName: "worker", Backend: "claude", Prompt: "do the thing"})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := store.EnsureSession(sess.SessionID(), "codex"); err != nil {
			t.Fatalf("EnsureSession: %v", err)
		}
		meta, err := store.LoadMetadata(sess.SessionID())
		if err != nil {
			t.Fatalf("LoadMetadata: %v", err)
		}
		if meta.Backend != "claude" || meta.AgentName != "worker" {
			t.Fatalf("metadata = %+v, want the owner's claude/worker record untouched", meta.SessionRecord)
		}
		prompt, err := os.ReadFile(filepath.Join(store.SessionDir(sess.SessionID()), "prompt.txt"))
		if err != nil || string(prompt) != "do the thing" {
			t.Fatalf("prompt = %q, err %v; want the owner's prompt preserved", string(prompt), err)
		}
	})
}
