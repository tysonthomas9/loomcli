package svcimpl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestSessionServiceListTaskSessionsControlPlaneUsesDiskTruth verifies that the
// control-plane list path reports HasTranscript from on-disk truth, not from the
// presence of the transcript_path metadata key (which is stamped at session
// creation, before any transcript content exists, and never cleared).
func TestSessionServiceListTaskSessionsControlPlaneUsesDiskTruth(t *testing.T) {
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create local session: %v", err)
	}
	sessionID := sess.SessionID()

	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      "worker-1",
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
		Phase:        "implementation",
		Metadata: map[string]string{
			// Stamped at creation; the file does NOT exist on disk yet.
			"transcript_path": sessStore.NativeTranscriptPath(sessionID),
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)

	// Case A: no transcript file on disk -> HasTranscript must be false, even
	// though metadata["transcript_path"] is set. (This fails before the fix.)
	items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].SessionID != sessionID {
		t.Fatalf("item SessionID = %q, want %q (control-plane path not taken?)", items[0].SessionID, sessionID)
	}
	if items[0].HasTranscript {
		t.Fatalf("HasTranscript = true, want false (no transcript on disk)")
	}

	// Case B: write a non-empty native transcript -> HasTranscript becomes true.
	srcTranscript := filepath.Join(t.TempDir(), "claude.jsonl")
	if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"session_meta\"}\n"), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sessionID, srcTranscript, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("sync native transcript: %v", err)
	}
	items, err = svc.ListTaskSessions(t.Context(), "WS", "TASK-1")
	if err != nil {
		t.Fatalf("ListTaskSessions (after sync): %v", err)
	}
	if len(items) != 1 || !items[0].HasTranscript {
		t.Fatalf("HasTranscript = %v, want true (transcript present on disk)", items[0].HasTranscript)
	}
}
