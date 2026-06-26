package svcimpl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/eventstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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
			"backend":         "localdogfood",
			"transcript_path": "/tmp/sess-1/transcript.jsonl",
			"files_changed":   "1",
			"lines_added":     "2",
			"lines_removed":   "3",
			"files_touched":   "file.txt",
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
	if item.FilesChanged != 1 || item.LinesAdded != 2 || item.LinesRemoved != 3 || len(item.FilesTouched) != 1 || item.FilesTouched[0] != "file.txt" {
		t.Fatalf("item diff stats = %+v", item.SessionRecord)
	}
}

func TestSessionServiceCloudControlPlaneTranscriptArtifactReadContent(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	transcriptBody := []byte(`{"seq":1,"timestamp":"2026-06-09T12:00:00Z","role":"assistant","type":"text","text":"done"}` + "\n")
	createFinalizedArtifact(t, st, "transcript-task-run-1", "flue-task-run-1", "TASK-FLUE-1", "task-run-1", "transcript", "application/x-ndjson", transcriptBody)
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
			"runtime":        "flue",
			"task_run_id":    "task-run-1",
			"driver_run_id":  "driver-run-1",
			"flue_session":   "flue-task-run-1",
			"flue_harness":   "task-agent",
			"transcript_ref": "artifact://transcript-task-run-1",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-FLUE-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 || !items[0].HasTranscript || items[0].Backend != "flue" {
		t.Fatalf("items = %+v, want flue session with transcript", items)
	}
	events, err := svc.GetSessionTranscript(ctx, "WS", "TASK-FLUE-1", "flue-task-run-1")
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Role != "assistant" || events[0].Text != "done" {
		t.Fatalf("events = %+v, want assistant transcript", events)
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

func TestSessionServiceControlPlaneDiffFallsBackToPatchArtifactByTaskRun(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	patchBody := []byte("diff --git a/fallback.txt b/fallback.txt\n+fallback\n")
	createFinalizedArtifact(t, st, "patch-task-run-fallback", "flue-task-run-2", "TASK-FLUE-2", "task-run-2", "patch", "text/x-diff", patchBody)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-2",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FLUE-2",
		Status:       domain.AgentSessionCompleted,
		Phase:        "implementation",
		Metadata: map[string]string{
			"runtime":     "flue",
			"task_run_id": "task-run-2",
		},
	}); err != nil {
		t.Fatalf("create flue control-plane session: %v", err)
	}

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
	assertServiceErrorKind(t, err, service.KindNotFound)
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
	assertServiceErrorKind(t, err, service.KindNotFound)
}

func TestSessionServiceLocalDiffMissingFallsBackToControlPlaneArtifact(t *testing.T) {
	ctx := t.Context()
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
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
	sessStore, err := sessions.NewStore(runtimeDir)
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
	assertServiceError(t, err, service.KindNotFound, "diff not found")
}

func createFinalizedArtifact(t *testing.T, st *memstore.Store, artifactID, sessionID, taskID, ownerID, artifactType, mimeType string, body []byte) {
	t.Helper()
	if _, err := st.Artifacts().Create(t.Context(), store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    artifactID,
		SessionID:     sessionID,
		TaskID:        taskID,
		OwnerType:     "task_run",
		OwnerID:       ownerID,
		Type:          artifactType,
		MIMEType:      mimeType,
		DurableStatus: "declared",
	}); err != nil {
		t.Fatalf("create %s artifact: %v", artifactType, err)
	}
	uploaded, err := st.Artifacts().UploadContent(t.Context(), "WS", artifactID, store.ArtifactContentUpload{Body: bytes.NewReader(body), MIMEType: mimeType})
	if err != nil {
		t.Fatalf("upload %s artifact: %v", artifactType, err)
	}
	if _, err := st.Artifacts().Finalize(t.Context(), "WS", artifactID, store.ArtifactFinalize{ContentHash: &uploaded.ContentHash}); err != nil {
		t.Fatalf("finalize %s artifact: %v", artifactType, err)
	}
}

func assertServiceErrorKind(t *testing.T, err error, want service.ErrorKind) {
	t.Helper()
	assertServiceError(t, err, want, "")
}

func assertServiceError(t *testing.T, err error, want service.ErrorKind, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var svcErr *service.ServiceError
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

	sessStore, err := sessions.NewStore(runtimeDir)
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
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {
				Path:  workspacePath,
				Repos: map[string]string{"source-repo": repoPath},
			},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(repoPath)
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
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := t.TempDir()
	runtimeDir := t.TempDir()

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: workspacePath},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(runtimeDir)
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
		t.Fatalf("create session: %v", err)
	}
	sessionID := sess.SessionID()
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-3", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	es := eventstore.Open(sessStore.SessionDir(sessionID))
	if err := es.AppendEnvelope(hwtranscript.EventEnvelope{
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
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want ServiceError", err, err)
	}
	if svcErr.Kind != service.KindInternal {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, service.KindInternal)
	}
}
