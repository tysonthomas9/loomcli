package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// useTempRuntimeDir points the workspace runtime directory at a scratch dir so a
// test's session store is the one the supervisor reads.
func useTempRuntimeDir(t *testing.T) string {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	return runtimeDir
}

// seedLeafTranscript writes a session with a mirrored native transcript in the
// given format, the way a daemon leaf leaves one behind.
func seedLeafTranscript(t *testing.T, runtimeDir, backend, format string) string {
	t.Helper()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{AgentName: "worker-1", Backend: backend})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	src := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(src, []byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write native transcript: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sess.SessionID(), src, format); err != nil {
		t.Fatalf("SyncNativeTranscript: %v", err)
	}
	return sess.SessionID()
}

// TestLocalTranscriptFormatReportsRecordedFormat pins the format lookup. A
// reader on another host has only this marker to go on, so guessing canonical
// over a raw provider rollout hands it events that parse cleanly and say
// nothing — which is why an unreadable session must report "" rather than a
// default.
func TestLocalTranscriptFormatReportsRecordedFormat(t *testing.T) {
	runtimeDir := useTempRuntimeDir(t)
	s := newControlPlaneTestSupervisor(memstore.New())

	rawSession := seedLeafTranscript(t, runtimeDir, "codex", sessions.TranscriptFormatRaw)
	canonicalSession := seedLeafTranscript(t, runtimeDir, "codex", sessions.TranscriptFormatCanonical)

	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{name: "go leaf mirrors the raw provider stream", sessionID: rawSession, want: sessions.TranscriptFormatRaw},
		{name: "ts leaf writes canonical events", sessionID: canonicalSession, want: sessions.TranscriptFormatCanonical},
		{name: "unknown session reports nothing", sessionID: "session-that-does-not-exist", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.localTranscriptFormat(tc.sessionID); got != tc.want {
				t.Fatalf("localTranscriptFormat = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCompleteControlPlaneAgentSessionStampsTranscriptFormat covers the daemon
// half of the reader contract: the session record must say how to parse the
// artifact it just uploaded, and must stay silent when it cannot tell.
func TestCompleteControlPlaneAgentSessionStampsTranscriptFormat(t *testing.T) {
	t.Run("stamps the format it read", func(t *testing.T) {
		runtimeDir := useTempRuntimeDir(t)
		sessionID := seedLeafTranscript(t, runtimeDir, "codex", sessions.TranscriptFormatRaw)
		st := memstore.New()
		s := newControlPlaneTestSupervisor(st)
		ap := &AgentProcess{
			Entry:      cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
			RoleConfig: cfgpkg.RoleConfig{Backend: "codex"},
		}
		s.createControlPlaneAgentSession(ap, sessionID, "task-1", "implementation", 0)
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:      sessionID,
			taskID:         "task-1",
			errClass:       "None",
			transcriptData: []byte("{}\n"),
		})

		record, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
		if err != nil {
			t.Fatalf("get completed session: %v", err)
		}
		if record.Metadata["transcript_ref"] != "artifact://transcript-"+sessionID {
			t.Fatalf("metadata = %#v, want a transcript artifact ref", record.Metadata)
		}
		if record.Metadata["transcript_format"] != sessions.TranscriptFormatRaw || record.Metadata["transcript_backend"] != "codex" {
			t.Fatalf("metadata = %#v, want raw codex markers for the reader", record.Metadata)
		}
		artifact, err := st.Artifacts().Get(t.Context(), "WS", "transcript-"+sessionID)
		if err != nil {
			t.Fatalf("get transcript artifact: %v", err)
		}
		if artifact.Metadata["transcript_format"] != sessions.TranscriptFormatRaw {
			t.Fatalf("artifact metadata = %#v, want the raw marker", artifact.Metadata)
		}
	})

	t.Run("stays silent when the format is unreadable", func(t *testing.T) {
		useTempRuntimeDir(t)
		const sessionID = "session-without-local-metadata"
		st := memstore.New()
		s := newControlPlaneTestSupervisor(st)
		ap := &AgentProcess{
			Entry:      cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
			RoleConfig: cfgpkg.RoleConfig{Backend: "codex"},
		}
		s.createControlPlaneAgentSession(ap, sessionID, "task-1", "implementation", 0)
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:      sessionID,
			taskID:         "task-1",
			errClass:       "None",
			transcriptData: []byte("{}\n"),
		})

		record, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
		if err != nil {
			t.Fatalf("get completed session: %v", err)
		}
		if _, ok := record.Metadata["transcript_format"]; ok {
			t.Fatalf("metadata = %#v, want no format marker when the local format is unknown", record.Metadata)
		}
		artifact, err := st.Artifacts().Get(t.Context(), "WS", "transcript-"+sessionID)
		if err != nil {
			t.Fatalf("get transcript artifact: %v", err)
		}
		if _, ok := artifact.Metadata["transcript_format"]; ok {
			t.Fatalf("artifact metadata = %#v, want no empty-valued format key", artifact.Metadata)
		}
	})
}
