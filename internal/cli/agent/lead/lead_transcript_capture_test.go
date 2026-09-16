package lead

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// writeCodexRollout drops a minimal codex rollout under CODEX_HOME whose
// session_meta cwd matches workDir, which is how the sync locates it.
func writeCodexRollout(t *testing.T, codexHome, workDir string, at time.Time) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", at.Format("2006"), at.Format("01"), at.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout: %v", err)
	}
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": workDir}})
	msg, _ := json.Marshal(map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": "lead plan"}},
		},
	})
	body := append(append(meta, '\n'), append(msg, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, "rollout-lead.jsonl"), body, 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}

// TestLeadSessionFinalizerPersistsTranscriptRef drives the finalizer the way the
// lead's deferred cleanup does and reads the control-plane record back. It
// guards the metadata hand-off in leadSessionFinalizer: computing the transcript
// metadata is worthless if the lifecycle Update never carries it.
func TestLeadSessionFinalizerPersistsTranscriptRef(t *testing.T) {
	runtimeDir := t.TempDir()
	codexHome := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("CODEX_HOME", codexHome)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	const sessionID = "lead-22222222-2222-4222-8222-222222222222"
	writeCodexRollout(t, codexHome, workDir, time.Now().Add(-time.Minute))

	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: sessionID, AgentID: "lead",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"lead_workdir": workDir, "backend": backendnames.Codex},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	stopHB := make(chan struct{})
	var wg sync.WaitGroup
	leadSessionFinalizer(&bootstrap.StoreHandle{Store: st}, "WS", sessionID, stopHB, &wg)()

	record, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get finalized session: %v", err)
	}
	if record.Status != domain.AgentSessionCompleted || record.FinishedAt == nil {
		t.Fatalf("status = %q finished_at = %v, want completed with a finish time", record.Status, record.FinishedAt)
	}
	if record.Metadata["transcript_ref"] != "artifact://transcript-"+sessionID {
		t.Fatalf("persisted metadata = %#v, want transcript_ref on the session record", record.Metadata)
	}
	if record.Metadata["transcript_format"] != sessions.TranscriptFormatRaw || record.Metadata["transcript_backend"] != backendnames.Codex {
		t.Fatalf("persisted metadata = %#v, want raw codex markers the reader dispatches on", record.Metadata)
	}
}

// TestSyncLeadNativeTranscriptPrefersHarnessRuntimeSession pins the two harness
// runtime hints. The controlled lead runtime knows the provider's own session id
// and its real start time; the AgentSession record only knows when loom created
// the row, so falling back to the record picks up whatever transcript happens to
// be newest in the project directory.
func TestSyncLeadNativeTranscriptPrefersHarnessRuntimeSession(t *testing.T) {
	t.Run("harness session id beats the newest file", func(t *testing.T) {
		runtimeDir := t.TempDir()
		claudeHome := t.TempDir()
		workDir := t.TempDir()
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
		t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
		cli.ResetWorkspaceRuntimeDirCache()
		t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

		const harnessUUID = "33333333-3333-4333-8333-333333333333"
		projectDir := claudeProjectDirForTest(claudeHome, workDir)
		writeClaudeTranscript(t, projectDir, harnessUUID+".jsonl", "wanted", time.Now().Add(-2*time.Minute))
		writeClaudeTranscript(t, projectDir, "99999999-9999-4999-8999-999999999999.jsonl", "decoy", time.Now())

		started := time.Now().Add(-5 * time.Minute)
		rec := &domain.AgentSession{
			SessionID: "lead-harness-uuid",
			StartedAt: started,
			Metadata: map[string]string{
				leadcontrol.MetadataHarnessSessionID: harnessUUID,
				leadcontrol.MetadataHarnessStartedAt: started.Format(time.RFC3339Nano),
			},
		}
		_, data := syncLeadNativeTranscript("lead-harness-uuid", workDir, backendnames.Claude, started, rec)
		if !strings.Contains(string(data), "wanted") {
			t.Fatalf("synced transcript = %q, want the harness-identified session, not the newest file", string(data))
		}
	})

	t.Run("harness start time beats the record's", func(t *testing.T) {
		runtimeDir := t.TempDir()
		claudeHome := t.TempDir()
		workDir := t.TempDir()
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
		t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
		cli.ResetWorkspaceRuntimeDirCache()
		t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

		projectDir := claudeProjectDirForTest(claudeHome, workDir)
		writeClaudeTranscript(t, projectDir, "44444444-4444-4444-8444-444444444444.jsonl", "wanted", time.Now().Add(-time.Hour))

		// The record's own StartedAt is later than every transcript on disk, so
		// the mtime cutoff rejects them all unless the harness start time wins.
		recordStart := time.Now()
		harnessStart := time.Now().Add(-2 * time.Hour)
		rec := &domain.AgentSession{
			SessionID: "lead-harness-since",
			StartedAt: recordStart,
			Metadata: map[string]string{
				leadcontrol.MetadataHarnessStartedAt: harnessStart.Format(time.RFC3339Nano),
			},
		}
		_, data := syncLeadNativeTranscript("lead-harness-since", workDir, backendnames.Claude, recordStart, rec)
		if !strings.Contains(string(data), "wanted") {
			t.Fatalf("synced transcript = %q, want the harness start time to widen the search window", string(data))
		}
	})
}

// failingSessionUpdates makes every AgentSession write fail, standing in for a
// transient fleet-db outage.
type failingSessionUpdates struct {
	*memstore.Store
}

func (f *failingSessionUpdates) AgentSessions() store.AgentSessionStore {
	return &failingSessionStore{AgentSessionStore: f.Store.AgentSessions()}
}

type failingSessionStore struct {
	store.AgentSessionStore
}

func (f *failingSessionStore) Update(context.Context, string, string, store.AgentSessionUpdate) (*domain.AgentSession, error) {
	return nil, errors.New("fleet-db unavailable")
}

// alwaysAdopting reports every session as already existing, so registration
// takes the adopt path whatever session id it generates.
type alwaysAdopting struct {
	*memstore.Store
}

func (a *alwaysAdopting) AgentSessions() store.AgentSessionStore {
	return &alwaysAdoptingSessions{AgentSessionStore: a.Store.AgentSessions()}
}

type alwaysAdoptingSessions struct {
	store.AgentSessionStore
}

func (a *alwaysAdoptingSessions) Create(context.Context, store.AgentSessionCreate) (*domain.AgentSession, error) {
	return nil, domain.ErrAlreadyExists
}

func (a *alwaysAdoptingSessions) Update(context.Context, string, string, store.AgentSessionUpdate) (*domain.AgentSession, error) {
	return nil, errors.New("fleet-db unavailable")
}

// TestCreateLeadSessionSurvivesAdoptFailure pins the best-effort contract on the
// adopt path. Registration is what starts the heartbeat, arms the finalizer, and
// exports the orchestrator session id every child agent inherits. Stamping audit
// metadata onto an already-existing session is not allowed to cost the run any
// of that, so a failed stamp is a warning, not an error.
func TestCreateLeadSessionSurvivesAdoptFailure(t *testing.T) {
	t.Run("a failed metadata stamp is not an error", func(t *testing.T) {
		st := &failingSessionUpdates{Store: memstore.New()}
		if _, err := st.Store.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
			WorkspaceKey: "WS", SessionID: "lead-adopt-flaky", AgentID: "lead",
			Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
			Metadata: map[string]string{"source": "web-terminal"},
		}); err != nil {
			t.Fatalf("create existing session: %v", err)
		}
		if err := createLeadSession(t.Context(), &bootstrap.StoreHandle{Store: st}, "WS", "lead-adopt-flaky", "lead", "/work/lead"); err != nil {
			t.Fatalf("createLeadSession = %v, want nil so registration still activates", err)
		}
	})

	t.Run("registration still exports orchestrator linkage", func(t *testing.T) {
		runtimeDir := t.TempDir()
		t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
		t.Setenv(envAgentName, "lead")
		t.Setenv(envOrchestratorSessionID, "")
		if err := os.Unsetenv(envOrchestratorSessionID); err != nil {
			t.Fatalf("unset %s: %v", envOrchestratorSessionID, err)
		}
		cli.ResetWorkspaceRuntimeDirCache()
		t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

		st := &alwaysAdopting{Store: memstore.New()}
		reg := registerLeadOrchestratorSessionOn(t.Context(), &bootstrap.StoreHandle{Store: st}, "WS", "/work/lead")
		t.Cleanup(reg.Finalize)

		if reg.SessionID == "" {
			t.Fatal("registration returned no session, so the heartbeat and finalizer never armed")
		}
		if got := os.Getenv(envOrchestratorSessionID); got != reg.SessionID {
			t.Fatalf("%s = %q, want %q so child agents attribute back to this lead", envOrchestratorSessionID, got, reg.SessionID)
		}
	})
}

// claudeProjectDirForTest mirrors how Claude Code names its per-cwd transcript
// directory: every non-alphanumeric byte of the working directory becomes a dash.
func claudeProjectDirForTest(claudeHome, workDir string) string {
	return filepath.Join(claudeHome, "projects", regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(workDir, "-"))
}

func writeClaudeTranscript(t *testing.T, projectDir, name, marker string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	line, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": marker}}},
	})
	path := filepath.Join(projectDir, name)
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("write claude transcript: %v", err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}
