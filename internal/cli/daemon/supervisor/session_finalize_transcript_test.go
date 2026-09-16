package supervisor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// transcriptFinalizeFixture stands up an isolated runtime dir, a codex rollout
// the finalize sync will mirror, and a control-plane session record, so a test
// can drive finalizeAgentSession and inspect the uploaded transcript artifact.
type transcriptFinalizeFixture struct {
	store    *memstore.Store
	sess     *sessions.Session
	sup      *Supervisor
	ap       *AgentProcess
	rollout  string
	sessPath string
}

func newTranscriptFinalizeFixture(t *testing.T, rolloutExtra []string) *transcriptFinalizeFixture {
	t.Helper()
	runtimeDir := t.TempDir()
	codexHome := t.TempDir()
	worktree := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("CODEX_HOME", codexHome)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   backendnames.Codex,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	now := time.Now()
	rolloutDir := filepath.Join(codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("create rollout directory: %v", err)
	}
	meta, err := json.Marshal(map[string]any{
		"type":    "session_meta",
		"payload": map[string]any{"cwd": worktree},
	})
	if err != nil {
		t.Fatalf("marshal rollout metadata: %v", err)
	}
	body := make([]byte, 0, len(meta)+1)
	body = append(append(body, meta...), '\n')
	for _, line := range rolloutExtra {
		body = append(body, []byte(line+"\n")...)
	}
	rollout := filepath.Join(rolloutDir, "rollout-transcript-truth.jsonl")
	if err := os.WriteFile(rollout, body, 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sess.SessionID(),
		AgentID:      "worker-1",
		TaskID:       "task-1",
		Status:       domain.AgentSessionRunning,
		Phase:        "implementation",
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
	return &transcriptFinalizeFixture{
		store: st,
		sess:  sess,
		sup:   newControlPlaneTestSupervisor(st),
		ap: &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "worker-1"},
			RoleConfig:     cfgpkg.RoleConfig{Backend: backendnames.Codex},
			WorktreePath:   worktree,
			AssignedTaskID: "task-1",
			Session:        sess,
			AgentSessionID: sess.SessionID(),
		},
		rollout:  rollout,
		sessPath: sessStore.NativeTranscriptPath(sess.SessionID()),
	}
}

// uploadedTranscript returns the artifact content the finalize path uploaded,
// failing the test when no transcript_ref was recorded.
func (f *transcriptFinalizeFixture) uploadedTranscript(t *testing.T) []byte {
	t.Helper()
	record, err := f.store.AgentSessions().Get(t.Context(), "WS", f.sess.SessionID())
	if err != nil {
		t.Fatalf("get completed session: %v", err)
	}
	if ref := record.Metadata["transcript_ref"]; ref == "" {
		t.Fatalf("transcript_ref = %q, want uploaded artifact reference", ref)
	}
	reader, ok := f.store.Artifacts().(store.ArtifactContentReader)
	if !ok {
		t.Fatal("test artifact store does not support content reads")
	}
	data, err := reader.ReadContent(t.Context(), "WS", "transcript-"+f.sess.SessionID())
	if err != nil {
		t.Fatalf("read uploaded transcript: %v", err)
	}
	return data
}

// The Go leaf writes no transcript until finalize mirrors the provider rollout,
// so reading only before finalize uploads nothing at all.
func TestFinalizeAgentSessionReadsTranscriptAfterFinalize(t *testing.T) {
	f := newTranscriptFinalizeFixture(t, nil)
	f.sup.finalizeAgentSession(f.ap, 0)
	if data := f.uploadedTranscript(t); len(data) == 0 {
		t.Fatal("uploaded transcript is empty")
	}
}

// The claude Go leaf mirrors the transcript live, so the pre-finalize read can
// succeed on a partial file that finalize then completes. The upload must carry
// the completed transcript, not the partial one it happened to catch first.
func TestFinalizeAgentSessionPrefersCompletedTranscript(t *testing.T) {
	f := newTranscriptFinalizeFixture(t, []string{
		`{"type":"event_msg","payload":{"type":"agent_message","message":"completed-by-finalize"}}`,
	})
	partial := []byte("{\"type\":\"partial\"}\n")
	if err := os.WriteFile(f.sessPath, partial, 0o600); err != nil {
		t.Fatalf("seed partial transcript: %v", err)
	}

	f.sup.finalizeAgentSession(f.ap, 0)

	data := f.uploadedTranscript(t)
	if !bytes.Contains(data, []byte("completed-by-finalize")) {
		t.Fatalf("uploaded transcript = %q, want the finalize-completed transcript", data)
	}
	if len(data) <= len(partial) {
		t.Fatalf("uploaded %d bytes, want more than the %d-byte partial read", len(data), len(partial))
	}
}
