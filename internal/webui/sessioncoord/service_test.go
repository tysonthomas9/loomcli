package sessioncoord

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func TestSessionControlPlaneReadErrorMapsArtifactsUnavailable(t *testing.T) {
	err := sessionControlPlaneReadError("artifact query failed", artifactsmodule.ErrUnavailable)
	assertServiceErrorKind(t, err, apperrors.KindUnavailable)
}

func TestSessionServiceListTaskSessionsUsesControlPlane(t *testing.T) {
	st := memstore.New()
	finishedAt := time.Now().UTC()
	exitCode := 0
	status := domain.AgentSessionCompleted
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode

	created, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sess-1",
		AgentID:      "worker-1",
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
		Phase:        "implementation",
		Metadata: map[string]string{
			"backend":           "localdogfood",
			"transcript_path":   "/tmp/sess-1/transcript.jsonl",
			"runtime_strategy":  "local-cli-codex",
			"delivery":          "patch_back",
			"patch_back_status": "applied",
			"logs_ref":          "artifact://logs-1",
			"local_branch":      "loom/task-1",
			"head_sha":          "abc123",
			"github_pr_url":     "https://github.com/acme/widgets/pull/1",
			"files_changed":     "1",
			"lines_added":       "2",
			"lines_removed":     "3",
			"files_touched":     "file.txt",
		},
	})
	if err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", created.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ExitCode:   &exitCodePtr,
	}); err != nil {
		t.Fatalf("complete control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.SessionID != "sess-1" || item.TaskID != "TASK-1" || item.AgentName != "worker-1" {
		t.Fatalf("item identity = %+v", item.SessionRecord)
	}
	if item.Status != sessions.StatusCompleted || item.IsActive {
		t.Fatalf("item status/isActive = %q/%v, want completed/false", item.Status, item.IsActive)
	}
	if item.Backend != "localdogfood" || !item.HasTranscript {
		t.Fatalf("item backend/transcript = %q/%v", item.Backend, item.HasTranscript)
	}
	if item.RuntimeStrategy != "local-cli-codex" || item.DeliveryMode != "patch_back" || item.PatchBackStatus != "applied" ||
		item.LogsRef != "artifact://logs-1" || item.LocalBranch != "loom/task-1" || item.HeadSHA != "abc123" ||
		item.GitHubPRURL != "https://github.com/acme/widgets/pull/1" {
		t.Fatalf("execution evidence = %+v", item)
	}
	if item.FilesChanged != 1 || item.LinesAdded != 2 || item.LinesRemoved != 3 || len(item.FilesTouched) != 1 || item.FilesTouched[0] != "file.txt" {
		t.Fatalf("item diff stats = %+v", item.SessionRecord)
	}
}

func TestSessionServiceExecutionTaskRunOwnsBatchHistoryAndTranscript(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	runtimeDir := t.TempDir()
	transcriptBody := []byte(`{"seq":1,"timestamp":"2026-06-09T12:00:00Z","role":"assistant","type":"text","text":"done"}` + "\n")
	patchBody := []byte("diff --git a/file.txt b/file.txt\n+done\n")
	createFinalizedArtifact(t, st, "transcript-task-run-1", "", "TASK-FLUE-1", "task-run-1", "transcript", "application/x-ndjson", transcriptBody)
	createFinalizedArtifact(t, st, "patch-task-run-1", "", "TASK-FLUE-1", "task-run-1", "patch", "text/x-diff", patchBody)
	run := createRunningExecutionTaskRun(t, st, "task-run-1", "TASK-FLUE-1", map[string]string{
		"runtime": "flue",
	})
	run = finishExecutionTaskRun(t, st, run, map[string]string{
		"runtime":           "flue",
		"transcript_ref":    "artifact://transcript-task-run-1",
		"patch_artifact_id": "patch-task-run-1",
		"scheduler_attempt": "2",
		"runtime_strategy":  "local-cli-codex",
		"delivery":          "patch_back",
		"patch_back_status": "applied",
		"files_changed":     "1",
		"lines_added":       "1",
		"files_touched":     "file.txt",
	})

	// Historical stacks may still contain the old Interaction shadow. Its
	// conflicting lifecycle must not duplicate or override the TaskRun row.
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "flue-task-run-1", AgentID: "shadow-worker",
		Kind: domain.AgentSessionKindTask, TaskID: "TASK-FLUE-1", Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"task_run_id": "task-run-1"},
	}); err != nil {
		t.Fatalf("create historical shadow session: %v", err)
	}

	// A stale local session may also reuse the former Flue shadow ID. It must
	// never override the canonical TaskRun lifecycle, transcript, or patch.
	localStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("create local session store: %v", err)
	}
	if err := os.MkdirAll(localStore.SessionDir("flue-task-run-1"), 0o700); err != nil {
		t.Fatalf("create local collision directory: %v", err)
	}
	if err := localStore.SaveMetadata("flue-task-run-1", &sessions.SessionMetadata{
		SessionRecord: sessions.SessionRecord{
			SchemaVersion:    sessions.CurrentSchemaVersion,
			SessionID:        "flue-task-run-1",
			TaskID:           "TASK-FLUE-1",
			AgentName:        "wrong-local-worker",
			Backend:          "flue",
			Status:           sessions.StatusRunning,
			StartedAt:        time.Now().UTC(),
			TranscriptFormat: sessions.TranscriptFormatCanonical,
		},
	}); err != nil {
		t.Fatalf("write local collision metadata: %v", err)
	}
	localTranscript := []byte(`{"seq":1,"timestamp":"2026-06-09T12:00:00Z","role":"assistant","type":"text","text":"wrong local transcript"}` + "\n")
	if err := os.WriteFile(localStore.NativeTranscriptPath("flue-task-run-1"), localTranscript, 0o600); err != nil {
		t.Fatalf("write local collision transcript: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localStore.SessionDir("flue-task-run-1"), "diff.patch"), []byte("wrong local diff\n"), 0o600); err != nil {
		t.Fatalf("write local collision diff: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-FLUE-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want exactly one Execution-backed attempt", items)
	}
	item := items[0]
	if item.SessionID != "flue-task-run-1" || item.AgentName != run.WorkerProfileID ||
		item.Status != sessions.StatusCompleted || item.IsActive ||
		!item.HasTranscript || !item.HasDiff || item.Backend != "flue" ||
		item.AttemptNum != 2 || item.InputTokens != 11 || item.OutputTokens != 17 ||
		item.RuntimeStrategy != "local-cli-codex" || item.DeliveryMode != "patch_back" ||
		item.PatchBackStatus != "applied" || item.FilesChanged != 1 {
		t.Fatalf("execution-backed item = %+v", item)
	}
	detail, err := svc.GetSession(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if detail.SessionID != "flue-task-run-1" || detail.AgentName != run.WorkerProfileID ||
		detail.Status != sessions.StatusCompleted || detail.IsActive {
		t.Fatalf("execution-backed detail = %+v", detail)
	}
	events, err := svc.GetSessionTranscript(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Role != "assistant" || events[0].Text != "done" {
		t.Fatalf("events = %+v, want assistant transcript", events)
	}
	diff, err := svc.GetSessionDiff(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	if err != nil {
		t.Fatalf("GetSessionDiff: %v", err)
	}
	if diff != string(patchBody) {
		t.Fatalf("diff = %q, want %q", diff, patchBody)
	}
}

func TestSessionRecordFromTaskRunMapsExecutionLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		status     domain.TaskRunStatus
		wantStatus sessions.SessionStatus
		wantActive bool
		wantExit   int
	}{
		{name: "queued", status: domain.TaskRunQueued, wantStatus: sessions.StatusRunning, wantActive: true},
		{name: "running", status: domain.TaskRunRunning, wantStatus: sessions.StatusRunning, wantActive: true},
		{name: "completed", status: domain.TaskRunCompleted, wantStatus: sessions.StatusCompleted},
		{name: "failed", status: domain.TaskRunFailed, wantStatus: sessions.StatusFailed, wantExit: 1},
		{name: "cancelled", status: domain.TaskRunCancelled, wantStatus: sessions.StatusAborted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &domain.TaskRun{
				WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
				WorkerProfileID: "worker-profile", RunnerKind: "flue-workflow",
				Status: tt.status, CreatedAt: time.Now().UTC(),
			}
			record := sessionRecordFromTaskRun(run)
			if record.SessionID != "flue-task-run-1" || record.TaskID != "TASK-1" ||
				record.AgentName != "worker-profile" || record.Status != tt.wantStatus ||
				record.ExitCode != tt.wantExit || isActiveTaskRun(run.Status) != tt.wantActive {
				t.Fatalf("mapping = record %+v active=%v", record, isActiveTaskRun(run.Status))
			}
		})
	}
}

func TestSessionRecordFromTaskRunAttributesManagedAgent(t *testing.T) {
	run := &domain.TaskRun{
		TaskRunID:       "task-run-managed",
		TaskID:          "TASK-MANAGED",
		WorkerProfileID: "loom-serve-task-worker-1",
		RunnerKind:      "flue-workflow",
		Status:          domain.TaskRunRunning,
		CreatedAt:       time.Now().UTC(),
		Input: json.RawMessage(`{
			"loomAgentPolicy": {
				"version": 1,
				"agentServiceId": "agt-planner-1",
				"roleName": "plan",
				"backend": "codex"
			}
		}`),
	}

	record := sessionRecordFromTaskRun(run)
	if record.AgentName != "agt-planner-1" {
		t.Fatalf("agent name = %q, want managed product agent", record.AgentName)
	}
}

func TestSessionServiceExecutionTaskHistoryDoesNotDependOnInteractionSessionList(t *testing.T) {
	st := memstore.New()
	if _, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-independent", TaskID: "TASK-INDEPENDENT",
		WorkerProfileID: "worker-profile", Runner: "local-task-runner",
		Status: domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	wrapped := &interactionListFailureStore{
		Store:           st,
		artifactQueries: st.ArtifactQueries(),
		sessions: &interactionListFailureAgentSessions{
			AgentSessionStore: st.AgentSessions(),
		},
	}
	items, err := NewSessionService(wrapped, nil).ListTaskSessions(t.Context(), "WS", "TASK-INDEPENDENT")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 || items[0].SessionID != "task-run-independent" || !items[0].IsActive {
		t.Fatalf("Execution-backed items = %+v", items)
	}
}

type interactionListFailureStore struct {
	store.Store
	sessions        store.AgentSessionStore
	artifactQueries artifactsmodule.QueryStore
}

func (s *interactionListFailureStore) AgentSessions() store.AgentSessionStore {
	return s.sessions
}

func (s *interactionListFailureStore) ArtifactQueries() artifactsmodule.QueryStore {
	return s.artifactQueries
}

type interactionListFailureAgentSessions struct {
	store.AgentSessionStore
}

func (s *interactionListFailureAgentSessions) List(
	context.Context,
	string,
	store.AgentSessionFilter,
) ([]*domain.AgentSession, error) {
	return nil, errors.New("interaction session list unavailable")
}

func TestSessionServiceDaemonSessionOwnedTranscriptArtifactReadContent(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	body := []byte(`{"seq":1,"timestamp":"2026-07-28T12:00:00Z","role":"assistant","type":"text","text":"daemon transcript"}` + "\n")
	finalized, err := st.SeedArtifact(ctx, artifactsmodule.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "transcript-daemon-session",
		SessionID:     "daemon-session",
		TaskID:        "TASK-DAEMON",
		OwnerType:     artifactsmodule.OwnerSession,
		OwnerID:       "daemon-session",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: artifactsmodule.StatusFinalized,
		// AgentID is deliberately empty to cover artifacts written before the
		// daemon began stamping the already session-bound agent identity.
	}, body)
	if err != nil {
		t.Fatalf("create daemon transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "daemon-session",
		AgentID:      "daemon-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-DAEMON",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"transcript_ref": "artifact://" + finalized.ArtifactID,
		},
	}); err != nil {
		t.Fatalf("create daemon session: %v", err)
	}

	svc := NewSessionService(st, nil)
	events, err := svc.GetSessionTranscript(ctx, "WS", "TASK-DAEMON", "daemon-session")
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "daemon transcript" {
		t.Fatalf("events = %+v", events)
	}
	agentTranscripts := svc.(AgentSessionTranscriptService)
	events, err = agentTranscripts.GetAgentSessionTranscript(ctx, "WS", "daemon-agent", "daemon-session")
	if err != nil {
		t.Fatalf("GetAgentSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "daemon transcript" {
		t.Fatalf("agent events = %+v", events)
	}
}

func TestSessionServiceControlPlaneArtifactRefsRequireExactTaskRunOwnership(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	createFinalizedArtifact(
		t, st, "transcript-other-run", "other-session", "TASK-FLUE-1",
		"task-run-other", "transcript", "application/x-ndjson", []byte("{}\n"),
	)
	createFinalizedArtifact(
		t, st, "patch-other-run", "other-session", "TASK-FLUE-1",
		"task-run-other", "patch", "text/x-diff", []byte("diff --git a/a b/a\n"),
	)
	run := createRunningExecutionTaskRun(t, st, "task-run-1", "TASK-FLUE-1", map[string]string{
		"runtime":        "flue",
		"transcript_ref": "file:///etc/passwd",
		"patch_ref":      "http://127.0.0.1/private",
	})

	svc := NewSessionService(st, nil)
	_, err := svc.GetSessionTranscript(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
	_, err = svc.GetSessionDiff(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)

	metadata := map[string]string{
		"transcript_ref":    "artifact://transcript-other-run",
		"patch_artifact_id": "patch-other-run",
	}
	if _, err := st.TaskRuns().Heartbeat(ctx, "WS", run.TaskRunID, store.TaskRunHeartbeat{
		NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: "token-" + run.TaskRunID,
		FencingToken: run.FencingToken, RuntimeMetadata: metadata, HeartbeatAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("update execution task run: %v", err)
	}
	_, err = svc.GetSessionTranscript(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
	_, err = svc.GetSessionDiff(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
}

func TestSessionServicePathlessWorkspaceTranscriptFallsBackToControlPlaneArtifact(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := t.Context()
	st := memstore.New()
	createPathlessWorkspace(t, st)

	transcriptBody := []byte(`{"seq":1,"timestamp":"2026-06-09T12:00:00Z","role":"assistant","type":"text","text":"durable transcript"}` + "\n")
	createFinalizedArtifact(t, st, "transcript-pathless", "flue-pathless", "TASK-PATHLESS", "task-run-pathless", "transcript", "application/x-ndjson", transcriptBody)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-pathless",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-PATHLESS",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"transcript_ref": "artifact://transcript-pathless",
			"task_run_id":    "task-run-pathless",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	unavailableRuntimeDir := unavailableSessionRuntimeDir(t)
	events, err := NewSessionServiceWithRuntimeDir(st, nil, unavailableRuntimeDir).
		GetSessionTranscript(ctx, "WS", "TASK-PATHLESS", "flue-pathless")
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "durable transcript" {
		t.Fatalf("events = %+v, want durable control-plane transcript", events)
	}
}

func TestSessionServiceTranscriptUsesRuntimeStoreBeforeWorkspaceTopology(t *testing.T) {
	ctx := t.Context()
	rootDir := t.TempDir()
	runtimeDir := filepath.Join(rootDir, "workspaces", "LOCALMODE")
	targetRuntimeDir := filepath.Join(rootDir, "workspaces", "WS")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("create active runtime directory: %v", err)
	}
	runtimeStore, err := sessions.NewStore(t.Context(), targetRuntimeDir)
	if err != nil {
		t.Fatalf("new target runtime session store: %v", err)
	}
	sess, err := runtimeStore.CreateSession(sessions.CreateOptions{
		AgentName: "runtime-agent",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create target runtime session: %v", err)
	}
	sourceTranscript := filepath.Join(t.TempDir(), "codex.jsonl")
	if err := os.WriteFile(sourceTranscript, []byte(`{"type":"assistant","uuid":"runtime-1","message":{"content":[{"type":"text","text":"from runtime"}]}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := runtimeStore.SyncNativeTranscript(sess.SessionID(), sourceTranscript, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("sync target runtime transcript: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-RUNTIME-1", ExitCode: 0}); err != nil {
		t.Fatalf("finalize target runtime session: %v", err)
	}

	baseStore := memstore.New()
	countingStore := &workspaceAccessCountingStore{
		Store: baseStore, artifactQueries: baseStore.ArtifactQueries(),
	}
	svc := NewSessionServiceWithRuntimeDir(countingStore, nil, runtimeDir)
	events, err := svc.GetSessionTranscript(ctx, "WS", "TASK-RUNTIME-1", sess.SessionID())
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "from runtime" {
		t.Fatalf("events = %+v, want the runtime transcript", events)
	}
	if countingStore.workspaceCalls != 0 {
		t.Fatalf("workspace topology lookups = %d, want 0", countingStore.workspaceCalls)
	}
}

func TestSessionServiceAgentTranscriptEnforcesOwnership(t *testing.T) {
	ctx := t.Context()
	rootDir := t.TempDir()
	runtimeDir := filepath.Join(rootDir, "workspaces", "LOCALMODE")
	workspaceRuntimeDir := filepath.Join(rootDir, "workspaces", "WS")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatalf("create active runtime directory: %v", err)
	}
	localStore, err := sessions.NewStore(t.Context(), workspaceRuntimeDir)
	if err != nil {
		t.Fatalf("new workspace session store: %v", err)
	}
	sess, err := localStore.CreateSession(sessions.CreateOptions{
		AgentName: "advanced-planner",
		Backend:   "codex",
		Phase:     "planning",
	})
	if err != nil {
		t.Fatalf("create local supervised session: %v", err)
	}
	sourceTranscript := filepath.Join(t.TempDir(), "codex.jsonl")
	if err := os.WriteFile(sourceTranscript, []byte(
		`{"timestamp":"2026-08-01T08:40:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"local plan"}]}}`+"\n",
	), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := localStore.SyncNativeTranscript(sess.SessionID(), sourceTranscript, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("sync native transcript: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-LOCAL-1", ExitCode: 0}); err != nil {
		t.Fatalf("finalize local supervised session: %v", err)
	}
	activeStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new active workspace session store: %v", err)
	}
	activeSession, err := activeStore.CreateSession(sessions.CreateOptions{
		AgentName: "advanced-planner",
		Backend:   "codex",
		Phase:     "planning",
	})
	if err != nil {
		t.Fatalf("create active workspace session: %v", err)
	}
	if err := activeSession.Finalize(sessions.FinalizeOptions{TaskID: "TASK-OTHER-1", ExitCode: 0}); err != nil {
		t.Fatalf("finalize active workspace session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(memstore.New(), nil, runtimeDir)
	transcripts := svc.(AgentSessionTranscriptService)
	events, err := transcripts.GetAgentSessionTranscript(ctx, "WS", "advanced-planner", sess.SessionID())
	if err != nil {
		t.Fatalf("GetAgentSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "local plan" {
		t.Fatalf("events = %+v, want local plan", events)
	}
	_, err = transcripts.GetAgentSessionTranscript(ctx, "WS", "another-agent", sess.SessionID())
	assertServiceError(t, err, apperrors.KindNotFound, "session not found")
	_, err = transcripts.GetAgentSessionTranscript(ctx, "OTHER", "advanced-planner", sess.SessionID())
	assertServiceError(t, err, apperrors.KindNotFound, "session not found")
	_, err = transcripts.GetAgentSessionTranscript(ctx, "WS", "advanced-planner", activeSession.SessionID())
	assertServiceError(t, err, apperrors.KindNotFound, "session not found")
}

func TestSiblingWorkspaceRuntimeDirRequiresDirectWorkspaceSibling(t *testing.T) {
	rootDir := t.TempDir()
	workspaceRuntimeDir := filepath.Join(rootDir, "workspaces", "LOCALMODE")
	want := filepath.Join(rootDir, "workspaces", "WS")

	tests := []struct {
		name       string
		runtimeDir string
		wsID       string
		want       string
		ok         bool
	}{
		{name: "direct workspace sibling", runtimeDir: workspaceRuntimeDir, wsID: "WS", want: want, ok: true},
		{name: "nested workspace id", runtimeDir: workspaceRuntimeDir, wsID: "WS/other"},
		{name: "traversal workspace id", runtimeDir: workspaceRuntimeDir, wsID: "../WS"},
		{name: "dot workspace id", runtimeDir: workspaceRuntimeDir, wsID: "."},
		{name: "empty runtime directory", wsID: "WS"},
		{name: "non-workspaces parent", runtimeDir: filepath.Join(rootDir, "runtime", "LOCALMODE"), wsID: "WS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := siblingWorkspaceRuntimeDir(tt.runtimeDir, tt.wsID)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("siblingWorkspaceRuntimeDir(%q, %q) = (%q, %v), want (%q, %v)", tt.runtimeDir, tt.wsID, got, ok, tt.want, tt.ok)
			}
		})
	}
}

type workspaceAccessCountingStore struct {
	store.Store
	artifactQueries artifactsmodule.QueryStore
	workspaceCalls  int
}

func (s *workspaceAccessCountingStore) Workspaces() store.WorkspaceStore {
	s.workspaceCalls++
	return s.Store.Workspaces()
}

func (s *workspaceAccessCountingStore) ArtifactQueries() artifactsmodule.QueryStore {
	return s.artifactQueries
}

func TestSessionServiceAgentSessionTranscriptUsesAgentOwnership(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	transcriptBody := []byte(
		`{"seq":1,"timestamp":"2026-07-24T12:00:00Z","role":"user","type":"text","text":"Review this."}` + "\n" +
			`{"seq":2,"timestamp":"2026-07-24T12:00:01Z","role":"assistant","type":"text","text":"Looks good."}` + "\n",
	)
	finalized, err := st.SeedArtifact(ctx, artifactsmodule.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "transcript-interactive-1",
		AgentID:       "local-review",
		SessionID:     "interactive-1",
		OwnerType:     artifactsmodule.OwnerSession,
		OwnerID:       "interactive-1",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: artifactsmodule.StatusFinalized,
	}, transcriptBody)
	if err != nil {
		t.Fatalf("create interactive transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "interactive-1",
		AgentID:      "local-review",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionCompleted,
		// Fleet's interaction transcript command stores the canonical artifact
		// ID directly. The reader also retains artifact:// compatibility for
		// transcripts persisted by older writers.
		Metadata: map[string]string{"transcript_ref": finalized.ArtifactID},
	}); err != nil {
		t.Fatalf("create interactive agent session: %v", err)
	}

	svc, ok := NewSessionService(st, nil).(AgentSessionTranscriptService)
	if !ok {
		t.Fatal("session service does not implement AgentSessionTranscriptService")
	}
	events, err := svc.GetAgentSessionTranscript(ctx, "WS", "local-review", "interactive-1")
	if err != nil {
		t.Fatalf("GetAgentSessionTranscript: %v", err)
	}
	if len(events) != 2 || events[0].Role != "user" || events[0].Text != "Review this." ||
		events[1].Role != "assistant" || events[1].Text != "Looks good." {
		t.Fatalf("events = %+v", events)
	}

	_, err = svc.GetAgentSessionTranscript(ctx, "WS", "another-agent", "interactive-1")
	assertServiceError(t, err, apperrors.KindNotFound, "session not found")
}

func TestSessionServiceTranscriptPreservesControlPlaneReadFailures(
	t *testing.T,
) {
	tests := []struct {
		name     string
		err      error
		wantKind apperrors.ErrorKind
	}{
		{
			name: "unavailable",
			err: errors.Join(
				domain.ErrUnavailable,
				errors.New("connection refused"),
			),
			wantKind: apperrors.KindUnavailable,
		},
		{
			name: "rate limited",
			err: errors.Join(
				domain.ErrRateLimited,
				errors.New("HTTP 429"),
			),
			wantKind: apperrors.KindRateLimited,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := memstore.New()
			wrapped := &sessionReadErrorStore{
				Store:           base,
				artifactQueries: base.ArtifactQueries(),
				sessions: &agentSessionReadErrorStore{
					AgentSessionStore: base.AgentSessions(),
					err:               test.err,
				},
				taskRuns: &taskRunReadErrorStore{
					TaskRunStore: base.TaskRuns(),
					err:          test.err,
				},
			}
			serviceImpl := NewSessionService(wrapped, nil)
			agentTranscripts, ok := serviceImpl.(AgentSessionTranscriptService)
			if !ok {
				t.Fatal(
					"session service does not implement AgentSessionTranscriptService",
				)
			}
			_, err := agentTranscripts.GetAgentSessionTranscript(
				t.Context(),
				"WS",
				"local-review",
				"interactive-1",
			)
			assertServiceErrorKind(t, err, test.wantKind)

			_, err = serviceImpl.GetSessionTranscript(
				t.Context(),
				"WS",
				"TASK-1",
				"task-run-1",
			)
			assertServiceErrorKind(t, err, test.wantKind)
		})
	}
}

type sessionReadErrorStore struct {
	store.Store
	sessions        store.AgentSessionStore
	taskRuns        store.TaskRunStore
	artifactQueries artifactsmodule.QueryStore
}

func (wrapped *sessionReadErrorStore) AgentSessions() store.AgentSessionStore {
	return wrapped.sessions
}

func (wrapped *sessionReadErrorStore) TaskRuns() store.TaskRunStore {
	return wrapped.taskRuns
}

func (wrapped *sessionReadErrorStore) ArtifactQueries() artifactsmodule.QueryStore {
	return wrapped.artifactQueries
}

type agentSessionReadErrorStore struct {
	store.AgentSessionStore
	err error
}

func (wrapped *agentSessionReadErrorStore) Get(
	context.Context,
	string,
	string,
) (*domain.AgentSession, error) {
	return nil, wrapped.err
}

type taskRunReadErrorStore struct {
	store.TaskRunStore
	err error
}

func (wrapped *taskRunReadErrorStore) Get(
	context.Context,
	string,
	string,
) (*domain.TaskRun, error) {
	return nil, wrapped.err
}

func TestSessionServiceAgentTranscriptPreservesManagedContentFailureKind(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	finalized, err := st.SeedArtifact(ctx, artifactsmodule.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    "transcript-interactive-errors",
		AgentID:       "local-review",
		SessionID:     "interactive-errors",
		OwnerType:     artifactsmodule.OwnerSession,
		OwnerID:       "interactive-errors",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: artifactsmodule.StatusFinalized,
	}, []byte(`{"seq":1,"timestamp":"2026-07-24T12:00:00Z","role":"assistant","type":"text","text":"saved"}`+"\n"))
	if err != nil {
		t.Fatalf("create transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "interactive-errors",
		AgentID:      "local-review",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionCompleted,
		Metadata:     map[string]string{"transcript_ref": "artifact://" + finalized.ArtifactID},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for _, tc := range []struct {
		name     string
		readErr  error
		wantKind apperrors.ErrorKind
	}{
		{
			name:     "content plane unavailable",
			readErr:  artifactsmodule.ErrContentUnavailable,
			wantKind: apperrors.KindUnavailable,
		},
		{
			name:     "managed content missing",
			readErr:  domain.ErrNotFound,
			wantKind: apperrors.KindNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifactQueries := &transcriptContentErrorArtifactStore{
				QueryStore: st.ArtifactQueries(),
				err:        tc.readErr,
			}
			wrapped := &transcriptContentErrorStore{Store: st, artifacts: artifactQueries}
			svc := NewSessionService(wrapped, nil).(AgentSessionTranscriptService)
			_, err := svc.GetAgentSessionTranscript(ctx, "WS", "local-review", "interactive-errors")
			assertServiceErrorKind(t, err, tc.wantKind)
		})
	}
}

type transcriptContentErrorStore struct {
	store.Store
	artifacts artifactsmodule.QueryStore
}

func (s *transcriptContentErrorStore) ArtifactQueries() artifactsmodule.QueryStore {
	return s.artifacts
}

type transcriptContentErrorArtifactStore struct {
	artifactsmodule.QueryStore
	err error
}

func (s *transcriptContentErrorArtifactStore) ReadArtifactContent(context.Context, string, string) ([]byte, error) {
	return nil, s.err
}

func TestSessionServiceAgentTranscriptRejectsUnfinalizedArtifact(t *testing.T) {
	for _, status := range []string{"declared", "uploading"} {
		t.Run(status, func(t *testing.T) {
			ctx := t.Context()
			st := memstore.New()
			const (
				artifactID = "transcript-unfinalized"
				sessionID  = "interactive-unfinalized"
			)
			content := []byte(nil)
			if status == "uploading" {
				content = []byte(`{"seq":1,"timestamp":"2026-07-24T12:00:00Z","role":"assistant","type":"text","text":"not durable"}` + "\n")
			}
			if _, err := st.SeedArtifact(ctx, artifactsmodule.Artifact{
				WorkspaceKey:  "WS",
				ArtifactID:    artifactID,
				AgentID:       "local-review",
				SessionID:     sessionID,
				OwnerType:     artifactsmodule.OwnerSession,
				OwnerID:       sessionID,
				Type:          "transcript",
				MIMEType:      "application/x-ndjson",
				DurableStatus: artifactsmodule.DurableStatus(status),
			}, content); err != nil {
				t.Fatalf("create %s transcript artifact: %v", status, err)
			}
			if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
				WorkspaceKey: "WS",
				SessionID:    sessionID,
				AgentID:      "local-review",
				Kind:         domain.AgentSessionKindOrchestration,
				Status:       domain.AgentSessionCompleted,
				Metadata:     map[string]string{"transcript_ref": "artifact://" + artifactID},
			}); err != nil {
				t.Fatalf("create interactive session: %v", err)
			}

			svc := NewSessionService(st, nil).(AgentSessionTranscriptService)
			_, err := svc.GetAgentSessionTranscript(ctx, "WS", "local-review", sessionID)
			assertServiceError(t, err, apperrors.KindNotFound, "transcript content is no longer available")
		})
	}
}

func TestSessionServiceTaskTranscriptAndDiffPreserveManagedContentFailureKind(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	createFinalizedArtifact(
		t, st, "transcript-task-errors", "task-session-errors", "TASK-ERRORS",
		"task-run-errors", "transcript", "application/x-ndjson", []byte("{}\n"),
	)
	createFinalizedArtifact(
		t, st, "patch-task-errors", "task-session-errors", "TASK-ERRORS",
		"task-run-errors", "patch", "text/x-diff", []byte("diff --git a/a b/a\n"),
	)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-session-errors",
		AgentID:      "task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-ERRORS",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"transcript_ref":    "artifact://transcript-task-errors",
			"patch_artifact_id": "patch-task-errors",
			"task_run_id":       "task-run-errors",
		},
	}); err != nil {
		t.Fatalf("create task session: %v", err)
	}

	for _, tc := range []struct {
		name     string
		readErr  error
		wantKind apperrors.ErrorKind
	}{
		{
			name:     "content plane unavailable",
			readErr:  artifactsmodule.ErrContentUnavailable,
			wantKind: apperrors.KindUnavailable,
		},
		{
			name:     "managed content missing",
			readErr:  domain.ErrNotFound,
			wantKind: apperrors.KindNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifactQueries := &transcriptContentErrorArtifactStore{
				QueryStore: st.ArtifactQueries(),
				err:        tc.readErr,
			}
			wrapped := &transcriptContentErrorStore{Store: st, artifacts: artifactQueries}
			svc := NewSessionService(wrapped, nil)

			_, transcriptErr := svc.GetSessionTranscript(ctx, "WS", "TASK-ERRORS", "task-session-errors")
			assertServiceErrorKind(t, transcriptErr, tc.wantKind)
			_, diffErr := svc.GetSessionDiff(ctx, "WS", "TASK-ERRORS", "task-session-errors")
			assertServiceErrorKind(t, diffErr, tc.wantKind)
		})
	}
}

func TestSessionServiceControlPlaneDiffArtifactReadContent(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	patchBody := []byte("diff --git a/file.txt b/file.txt\n+changed\n")
	createFinalizedArtifact(t, st, "patch-task-run-1", "flue-task-run-1", "TASK-FLUE-1", "task-run-1", "patch", "text/x-diff", patchBody)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey:    "WS",
		SessionID:       "flue-task-run-1",
		AgentID:         "flue-task-agent",
		Kind:            domain.AgentSessionKindTask,
		TaskID:          "TASK-FLUE-1",
		ParentSessionID: "lead-session-1",
		Status:          domain.AgentSessionCompleted,
		Phase:           "implementation",
		Metadata: map[string]string{
			"runtime":           "flue",
			"task_run_id":       "task-run-1",
			"patch_artifact_id": "patch-task-run-1",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-FLUE-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 || !items[0].HasDiff {
		t.Fatalf("items = %+v, want control-plane session with diff", items)
	}
	diff, err := svc.GetSessionDiff(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	if err != nil {
		t.Fatalf("GetSessionDiff: %v", err)
	}
	if diff != string(patchBody) {
		t.Fatalf("diff = %q, want %q", diff, patchBody)
	}
}

func TestSessionServicePathlessWorkspaceDiffFallsBackToControlPlaneArtifact(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := t.Context()
	st := memstore.New()
	createPathlessWorkspace(t, st)

	patchBody := []byte("diff --git a/docs.md b/docs.md\n+durable patch\n")
	createFinalizedArtifact(t, st, "patch-pathless", "flue-pathless", "TASK-PATHLESS", "task-run-pathless", "patch", "text/x-diff", patchBody)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-pathless",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-PATHLESS",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"patch_artifact_id": "patch-pathless",
			"task_run_id":       "task-run-pathless",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

	unavailableRuntimeDir := unavailableSessionRuntimeDir(t)
	diff, err := NewSessionServiceWithRuntimeDir(st, nil, unavailableRuntimeDir).
		GetSessionDiff(ctx, "WS", "TASK-PATHLESS", "flue-pathless")
	if err != nil {
		t.Fatalf("GetSessionDiff: %v", err)
	}
	if diff != string(patchBody) {
		t.Fatalf("diff = %q, want %q", diff, patchBody)
	}
}

func TestSessionServicePathlessWorkspaceFallbackPreservesValidationAndOwnership(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := t.Context()
	st := memstore.New()
	createPathlessWorkspace(t, st)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-pathless",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-PATHLESS",
		Status:       domain.AgentSessionCompleted,
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	_, err := svc.GetSessionTranscript(ctx, "WS", "bad/task", "flue-pathless")
	assertServiceErrorKind(t, err, apperrors.KindValidation)
	_, err = svc.GetSessionDiff(ctx, "WS", "bad/task", "flue-pathless")
	assertServiceErrorKind(t, err, apperrors.KindValidation)
	_, err = svc.GetSessionTranscript(ctx, "WS", "TASK-OTHER", "flue-pathless")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
	_, err = svc.GetSessionDiff(ctx, "WS", "TASK-OTHER", "flue-pathless")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
}

func TestSessionStoreAllowsControlPlaneFallbackOnlyForLocalAbsence(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "session not found", err: apperrors.ErrNotFound("session not found"), want: true},
		{name: "local stores unavailable", err: apperrors.ErrInternal("no session stores available", errNoUsableSessionStores), want: true},
		{name: "validation", err: apperrors.ErrValidation("invalid session ID"), want: false},
		{name: "forbidden", err: apperrors.ErrForbidden("forbidden"), want: false},
		{name: "unrelated internal", err: apperrors.ErrInternal("failed to resolve session stores", errors.New("fleet unavailable")), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionStoreAllowsControlPlaneFallback(tt.err); got != tt.want {
				t.Fatalf("sessionStoreAllowsControlPlaneFallback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSessionServiceControlPlaneDiffFallsBackToPatchArtifactByTaskRun(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	patchBody := []byte("diff --git a/fallback.txt b/fallback.txt\n+fallback\n")
	createFinalizedArtifact(t, st, "patch-task-run-fallback", "", "TASK-FLUE-2", "task-run-2", "patch", "text/x-diff", patchBody)
	createRunningExecutionTaskRun(t, st, "task-run-2", "TASK-FLUE-2", map[string]string{"runtime": "flue"})

	diff, err := NewSessionService(st, nil).GetSessionDiff(ctx, "WS", "TASK-FLUE-2", "flue-task-run-2")
	if err != nil {
		t.Fatalf("GetSessionDiff: %v", err)
	}
	if diff != string(patchBody) {
		t.Fatalf("diff = %q, want %q", diff, patchBody)
	}
}

func TestSessionServiceControlPlaneDiffRejectsWrongTask(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	createFinalizedArtifact(t, st, "patch-task-run-1", "flue-task-run-1", "TASK-FLUE-1", "task-run-1", "patch", "text/x-diff", []byte("diff --git a/file.txt b/file.txt\n"))
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-1",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FLUE-1",
		Status:       domain.AgentSessionCompleted,
		Phase:        "implementation",
		Metadata: map[string]string{
			"task_run_id":       "task-run-1",
			"patch_artifact_id": "patch-task-run-1",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

	_, err := NewSessionService(st, nil).GetSessionDiff(ctx, "WS", "TASK-OTHER", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
}

func TestSessionServiceControlPlaneDiffMissingPatchArtifact(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-1",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FLUE-1",
		Status:       domain.AgentSessionCompleted,
		Phase:        "implementation",
		Metadata: map[string]string{
			"task_run_id":       "task-run-1",
			"patch_artifact_id": "missing-patch",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

	_, err := NewSessionService(st, nil).GetSessionDiff(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, apperrors.KindNotFound)
}

func TestSessionServiceLocalDiffMissingFallsBackToControlPlaneArtifact(t *testing.T) {
	ctx := t.Context()
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-FLUE-1", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	st := memstore.New()
	patchBody := []byte("diff --git a/local.txt b/local.txt\n+from artifact\n")
	createFinalizedArtifact(t, st, "patch-task-run-1", sess.SessionID(), "TASK-FLUE-1", "task-run-1", "patch", "text/x-diff", patchBody)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sess.SessionID(),
		AgentID:      "worker-1",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FLUE-1",
		Status:       domain.AgentSessionCompleted,
		Phase:        "implementation",
		Metadata: map[string]string{
			"task_run_id":       "task-run-1",
			"patch_artifact_id": "patch-task-run-1",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	diff, err := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir).GetSessionDiff(ctx, "WS", "TASK-FLUE-1", sess.SessionID())
	if err != nil {
		t.Fatalf("GetSessionDiff: %v", err)
	}
	if diff != string(patchBody) {
		t.Fatalf("diff = %q, want %q", diff, patchBody)
	}
}

func TestSessionServiceLocalDiffMissingWithoutControlPlaneReturnsDiffNotFound(t *testing.T) {
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-FLUE-1", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	_, err = NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir).GetSessionDiff(t.Context(), "WS", "TASK-FLUE-1", sess.SessionID())
	assertServiceError(t, err, apperrors.KindNotFound, "diff not found")
}

func createRunningExecutionTaskRun(
	t *testing.T,
	st *memstore.Store,
	taskRunID, taskID string,
	metadata map[string]string,
) *domain.TaskRun {
	t.Helper()
	run, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{
		WorkspaceKey:    "WS",
		TaskRunID:       taskRunID,
		TaskID:          taskID,
		WorkerProfileID: "worker-profile",
		Runner:          "local-task-runner",
		RunnerKind:      "flue-workflow",
		Status:          domain.TaskRunRunning,
		NodeID:          "node-1",
		LeaseID:         "lease-" + taskRunID,
		LeaseToken:      "token-" + taskRunID,
		FencingToken:    41,
		RunnerPlacement: domain.TaskRunPlacement{Provider: "local", RunnerID: "task-worker"},
		RuntimeMetadata: metadata,
	})
	if err != nil {
		t.Fatalf("create execution task run %q: %v", taskRunID, err)
	}
	return run
}

func finishExecutionTaskRun(
	t *testing.T,
	st *memstore.Store,
	run *domain.TaskRun,
	metadata map[string]string,
) *domain.TaskRun {
	t.Helper()
	exitCode := 0
	finishedAt := time.Now().UTC()
	finished, err := st.TaskRuns().Finish(t.Context(), run.WorkspaceKey, run.TaskRunID, store.TaskRunFinish{
		NodeID:           run.NodeID,
		LeaseID:          run.LeaseID,
		LeaseToken:       "token-" + run.TaskRunID,
		FencingToken:     run.FencingToken,
		Status:           domain.TaskRunCompleted,
		ExitCode:         &exitCode,
		LogsRef:          "artifact://logs-" + run.TaskRunID,
		ArtifactsRef:     "artifacts://" + run.TaskRunID,
		InputTokens:      11,
		OutputTokens:     17,
		CacheReadTokens:  5,
		CacheWriteTokens: 2,
		EstimatedCostUSD: 0.125,
		RuntimeMetadata:  metadata,
		FinishedAt:       finishedAt,
	})
	if err != nil {
		t.Fatalf("finish execution task run %q: %v", run.TaskRunID, err)
	}
	return finished
}

func createFinalizedArtifact(t *testing.T, st *memstore.Store, artifactID, sessionID, taskID, ownerID, artifactType, mimeType string, body []byte) {
	t.Helper()
	if _, err := st.SeedArtifact(t.Context(), artifactsmodule.Artifact{
		WorkspaceKey:  "WS",
		ArtifactID:    artifactID,
		SessionID:     sessionID,
		TaskID:        taskID,
		OwnerType:     artifactsmodule.OwnerTaskRun,
		OwnerID:       ownerID,
		Type:          artifactType,
		MIMEType:      mimeType,
		DurableStatus: artifactsmodule.StatusFinalized,
	}, body); err != nil {
		t.Fatalf("finalize %s artifact: %v", artifactType, err)
	}
}

func createPathlessWorkspace(t *testing.T, st *memstore.Store) {
	t.Helper()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(t.Context(), store.RepoCreate{WorkspaceKey: "WS", Name: "source-repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
}

func unavailableSessionRuntimeDir(t *testing.T) string {
	t.Helper()
	notDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write non-directory runtime parent: %v", err)
	}
	return filepath.Join(notDirectory, "runtime")
}

func assertServiceErrorKind(t *testing.T, err error, want apperrors.ErrorKind) {
	t.Helper()
	assertServiceError(t, err, want, "")
}

func assertServiceError(t *testing.T, err error, want apperrors.ErrorKind, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want ServiceError kind %q", err, err, want)
	}
	if svcErr.Kind != want {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, want)
	}
	if wantMessage != "" && svcErr.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", svcErr.Message, wantMessage)
	}
}

func TestSessionServiceListTaskSessionsEnrichesControlPlaneWithLocalUsage(t *testing.T) {
	ctx := t.Context()
	runtimeDir := t.TempDir()

	sessStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-usage",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srcTranscript := filepath.Join(t.TempDir(), "claude.jsonl")
	if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sess.SessionID(), srcTranscript, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("sync native transcript: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:           "TASK-USAGE-1",
		ExitCode:         0,
		InputTokens:      12,
		OutputTokens:     1349,
		CacheReadTokens:  323621,
		CacheWriteTokens: 10687,
		EstimatedCostUSD: 0.15743355,
		DiffStats:        sessions.DiffStats{FilesChanged: 1, LinesAdded: 1},
		FilesTouched:     []string{"local-mode-agent-output.txt"},
		DiffPatch:        "diff --git a/local-mode-agent-output.txt b/local-mode-agent-output.txt\n",
	}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	st := memstore.New()
	finishedAt := time.Now().UTC()
	exitCode := 0
	status := domain.AgentSessionCompleted
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sess.SessionID(),
		AgentID:      "worker-usage",
		TaskID:       "TASK-USAGE-1",
		Status:       domain.AgentSessionRunning,
		Phase:        "implementation",
		Metadata: map[string]string{
			"backend":         "claude",
			"transcript_path": "/runtime/session/transcript.jsonl",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
	if _, err := st.AgentSessions().Update(ctx, "WS", sess.SessionID(), store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ExitCode:   &exitCodePtr,
	}); err != nil {
		t.Fatalf("complete control-plane session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-USAGE-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.InputTokens != 12 || item.OutputTokens != 1349 || item.CacheReadTokens != 323621 || item.CacheWriteTokens != 10687 {
		t.Fatalf("usage = in:%d out:%d read:%d write:%d", item.InputTokens, item.OutputTokens, item.CacheReadTokens, item.CacheWriteTokens)
	}
	if item.EstimatedCostUSD != 0.15743355 {
		t.Fatalf("cost = %v, want 0.15743355", item.EstimatedCostUSD)
	}
	if item.FilesChanged != 1 || item.LinesAdded != 1 || len(item.FilesTouched) != 1 || item.FilesTouched[0] != "local-mode-agent-output.txt" {
		t.Fatalf("diff stats = %+v", item.SessionRecord)
	}
	if !item.HasTranscript || !item.HasDiff {
		t.Fatalf("transcript/diff flags = %v/%v, want true/true", item.HasTranscript, item.HasDiff)
	}
	if item.Status != sessions.StatusCompleted || item.ExitCode != 0 || item.IsActive {
		t.Fatalf("control-plane lifecycle changed unexpectedly: status=%q exit=%d active=%v", item.Status, item.ExitCode, item.IsActive)
	}
}

func TestSessionServiceListTaskSessionsFallsBackToFileStores(t *testing.T) {
	loomConfigDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomConfigDir)

	ctx := t.Context()
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "repos", "source-repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS", Name: "source-repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["WS"] = bootstrap.WorkspaceLocalState{
			Path:  workspacePath,
			Repos: map[string]string{"source-repo": repoPath},
		}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(t.Context(), repoPath)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "file-agent",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srcTranscript := filepath.Join(t.TempDir(), "codex.jsonl")
	if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"session_meta\"}\n"), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sess.SessionID(), srcTranscript, sessions.TranscriptFormatRaw); err != nil {
		t.Fatalf("sync native transcript: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:    "TASK-2",
		ExitCode:  0,
		DiffPatch: "diff --git a/file.txt b/file.txt\n",
		DiffStats: sessions.DiffStats{FilesChanged: 1, LinesAdded: 1},
	}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-2")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.SessionID != sess.SessionID() || item.AgentName != "file-agent" || item.Backend != "codex" {
		t.Fatalf("item identity = %+v", item.SessionRecord)
	}
	if !item.HasTranscript || !item.HasDiff {
		t.Fatalf("transcript/diff flags = %v/%v, want true/true", item.HasTranscript, item.HasDiff)
	}
}

func TestSessionServiceListTaskSessionsSearchesRuntimeDir(t *testing.T) {
	// Isolate the state cache to a temp config dir — bootstrap.SaveStateCache
	// REPLACES the whole state.json, so without this it clobbers the developer's
	// real ~/.loom/state.json (wiping local workspace path entries).
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := t.Context()
	workspacePath := t.TempDir()
	runtimeDir := t.TempDir()

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["WS"] = bootstrap.WorkspaceLocalState{Path: workspacePath}
		return nil
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new runtime session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "desktopqa",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "DESKTOP-QA-3", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS", "DESKTOP-QA-3")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].SessionID != sess.SessionID() || items[0].TaskID != "DESKTOP-QA-3" || items[0].AgentName != "desktopqa" {
		t.Fatalf("item identity = %+v", items[0].SessionRecord)
	}
}

func TestSessionServiceEventStoreSubagentsAreDiscoverable(t *testing.T) {
	t.Setenv("LOOM_SERVE_FROM_EVENTSTORE", "1")
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(t.Context(), runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-1",
		Backend:   "claude",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionID := sess.SessionID()
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-3", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	if err := sessStore.AppendEnvelope(sessionID, hwtranscript.EventEnvelope{
		RunID: "run-1", Harness: "claude", HarnessSessionID: "agent-789", ParentSessionID: "parent-native",
		Event: hwtranscript.Event{
			Seq: 0, Timestamp: time.Unix(2, 0), Role: "assistant", Type: "text",
			Text: "subagent from event store", Source: hwtranscript.SourceFile, NativeID: "msg:sub-1",
		},
	}); err != nil {
		t.Fatalf("append eventstore subagent: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir)
	ids, err := svc.ListSessionSubagents(t.Context(), "WS", "TASK-3", sessionID)
	if err != nil {
		t.Fatalf("ListSessionSubagents: %v", err)
	}
	if len(ids) != 1 || ids[0] != "agent-789" {
		t.Fatalf("subagent ids = %v, want [agent-789]", ids)
	}
	events, err := svc.GetSessionSubagentTranscript(t.Context(), "WS", "TASK-3", sessionID, "agent-789")
	if err != nil {
		t.Fatalf("GetSessionSubagentTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "subagent from event store" {
		t.Fatalf("subagent events = %+v", events)
	}
}

func TestReadScrollbackFileReturnsInternalWhenHomeDirUnavailable(t *testing.T) {
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) {
		return "", errors.New("home unavailable")
	}
	t.Cleanup(func() { userHomeDir = oldUserHomeDir })

	_, err := readScrollbackFile("/.loom/session-scrollback/session.log")
	if err == nil {
		t.Fatal("expected error")
	}
	var svcErr *apperrors.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want ServiceError", err, err)
	}
	if svcErr.Kind != apperrors.KindInternal {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, apperrors.KindInternal)
	}
}
