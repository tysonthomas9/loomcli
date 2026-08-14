package svcimpl

import (
	"bytes"
	"context"
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

type failingArtifactContentStore struct {
	store.ArtifactStore
}

func (s failingArtifactContentStore) ReadContent(context.Context, string, string) ([]byte, error) {
	return nil, domain.ErrConflict
}

type failingArtifactContentReadStore struct {
	store.Store
}

func (s failingArtifactContentReadStore) Artifacts() store.ArtifactStore {
	return failingArtifactContentStore{ArtifactStore: s.Store.Artifacts()}
}

type missingArtifactContentStore struct {
	store.ArtifactStore
}

func (s missingArtifactContentStore) ReadContent(context.Context, string, string) ([]byte, error) {
	return nil, domain.ErrNotFound
}

type missingArtifactContentReadStore struct {
	store.Store
}

func (s missingArtifactContentReadStore) Artifacts() store.ArtifactStore {
	return missingArtifactContentStore{ArtifactStore: s.Store.Artifacts()}
}

type taskRunListErrorStore struct {
	store.Store
	gets      *int
	listLimit *int
}

func (s taskRunListErrorStore) TaskRuns() store.TaskRunStore {
	return taskRunListErrorTaskRunStore{TaskRunStore: s.Store.TaskRuns(), gets: s.gets, listLimit: s.listLimit}
}

type taskRunListErrorTaskRunStore struct {
	store.TaskRunStore
	gets      *int
	listLimit *int
}

func (s taskRunListErrorTaskRunStore) List(_ context.Context, _ string, filter store.TaskRunFilter) ([]*domain.TaskRun, error) {
	*s.listLimit = filter.Limit
	return nil, errors.New("task run list failed")
}

func (s taskRunListErrorTaskRunStore) Get(ctx context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error) {
	*s.gets++
	return s.TaskRunStore.Get(ctx, workspaceKey, taskRunID)
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

func TestSessionServiceControlPlaneTranscriptArtifactReadFallbackOnContentError(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	transcriptBody := []byte(`{"seq":1,"timestamp":"2026-06-09T12:00:00Z","role":"assistant","type":"text","text":"from uri"}` + "\n")
	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, transcriptBody, 0o600); err != nil {
		t.Fatalf("write transcript fallback file: %v", err)
	}
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "transcript-fallback",
		SessionID:    "flue-task-run-fallback",
		TaskID:       "TASK-FALLBACK-1",
		OwnerType:    "task_run",
		OwnerID:      "task-run-fallback",
		Type:         "transcript",
		URI:          "file://" + transcriptPath,
	}); err != nil {
		t.Fatalf("create transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-fallback",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FALLBACK-1",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"runtime":        "flue",
			"task_run_id":    "task-run-fallback",
			"transcript_ref": "artifact://transcript-fallback",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionService(failingArtifactContentReadStore{Store: st}, nil)
	events, err := svc.GetSessionTranscript(ctx, "WS", "TASK-FALLBACK-1", "flue-task-run-fallback")
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if len(events) != 1 || events[0].Text != "from uri" {
		t.Fatalf("events = %+v, want URI fallback transcript", events)
	}
}

func TestSessionServiceControlPlaneTranscriptMissingUploadReturnsNotFound(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey:  "WS",
		ArtifactID:    "transcript-declared",
		SessionID:     "flue-task-run-declared",
		TaskID:        "TASK-DECLARED-1",
		OwnerType:     "task_run",
		OwnerID:       "task-run-declared",
		Type:          "transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
	}); err != nil {
		t.Fatalf("create transcript artifact: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-declared",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-DECLARED-1",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"runtime":        "flue",
			"task_run_id":    "task-run-declared",
			"transcript_ref": "artifact://transcript-declared",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionService(missingArtifactContentReadStore{Store: st}, nil)
	_, err := svc.GetSessionTranscript(ctx, "WS", "TASK-DECLARED-1", "flue-task-run-declared")
	if !serviceErrorIsNotFound(err) {
		t.Fatalf("GetSessionTranscript err = %v, want not found", err)
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

func TestSessionServiceControlPlaneProjectsTaskRunUsageAndDiffStats(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-projection",
		TaskID:       "TASK-PROJECTION-1",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	if _, err := st.TaskRuns().Complete(ctx, "WS", "task-run-projection", store.TaskRunComplete{
		CompletionID:        "complete-task-run-projection",
		Status:              domain.TaskRunCompleted,
		InputTokens:         101,
		OutputTokens:        202,
		CacheReadTokens:     303,
		CacheWriteTokens:    404,
		EstimatedCostUSD:    0.505,
		RuntimeMetadata:     map[string]string{"files_changed": "2", "lines_added": "8", "lines_removed": "3", "files_touched": "a.go\nb.go"},
		RequiredArtifactIDs: nil,
	}); err != nil {
		t.Fatalf("complete task run: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-projection",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-PROJECTION-1",
		Status:       domain.AgentSessionCompleted,
		Metadata:     map[string]string{"runtime": "flue", "task_run_id": "task-run-projection"},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-PROJECTION-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	assertProjectedTaskRunFields(t, items[0].SessionRecord)
	detail, err := svc.GetSession(ctx, "WS", "TASK-PROJECTION-1", "flue-task-run-projection")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	assertProjectedTaskRunFields(t, detail.SessionRecord)
}

func TestSessionServiceTaskRunProjectionListErrorDoesNotFanOutGets(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-list-error",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-LIST-ERROR-1",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"runtime":     "flue",
			"task_run_id": "task-run-list-error",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
	gets := 0
	listLimit := 0
	svc := NewSessionService(taskRunListErrorStore{Store: st, gets: &gets, listLimit: &listLimit}, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-LIST-ERROR-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if listLimit != 50 {
		t.Fatalf("TaskRuns.List limit = %d, want 50", listLimit)
	}
	if gets != 0 {
		t.Fatalf("TaskRuns.Get calls after list error = %d, want 0", gets)
	}
}

func assertProjectedTaskRunFields(t *testing.T, rec sessions.SessionRecord) {
	t.Helper()
	if rec.InputTokens != 101 || rec.OutputTokens != 202 || rec.CacheReadTokens != 303 || rec.CacheWriteTokens != 404 {
		t.Fatalf("usage = in:%d out:%d read:%d write:%d", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheWriteTokens)
	}
	if rec.EstimatedCostUSD != 0.505 {
		t.Fatalf("cost = %v, want 0.505", rec.EstimatedCostUSD)
	}
	if rec.FilesChanged != 2 || rec.LinesAdded != 8 || rec.LinesRemoved != 3 {
		t.Fatalf("diff stats = %+v, want task-run stats", rec)
	}
	if len(rec.FilesTouched) != 2 || rec.FilesTouched[0] != "a.go" || rec.FilesTouched[1] != "b.go" {
		t.Fatalf("files touched = %v, want [a.go b.go]", rec.FilesTouched)
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

func TestSessionServiceTaskRunAndLocalUsageMergeAgreeForListAndDetail(t *testing.T) {
	ctx := t.Context()
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker-merge",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:           "TASK-MERGE-1",
		ExitCode:         0,
		InputTokens:      12,
		OutputTokens:     34,
		CacheReadTokens:  0,
		CacheWriteTokens: 56,
	}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-merge",
		TaskID:       "TASK-MERGE-1",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	if _, err := st.TaskRuns().Complete(ctx, "WS", "task-run-merge", store.TaskRunComplete{
		CompletionID:    "complete-task-run-merge",
		Status:          domain.TaskRunCompleted,
		CacheReadTokens: 303,
	}); err != nil {
		t.Fatalf("complete task run: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sess.SessionID(),
		AgentID:      "worker-merge",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-MERGE-1",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"runtime":     "flue",
			"task_run_id": "task-run-merge",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-MERGE-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	assertMergedUsage(t, items[0].SessionRecord)
	detail, err := svc.GetSession(ctx, "WS", "TASK-MERGE-1", sess.SessionID())
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	assertMergedUsage(t, detail.SessionRecord)
}

func assertMergedUsage(t *testing.T, rec sessions.SessionRecord) {
	t.Helper()
	if rec.InputTokens != 12 || rec.OutputTokens != 34 || rec.CacheReadTokens != 303 || rec.CacheWriteTokens != 56 {
		t.Fatalf("usage = in:%d out:%d read:%d write:%d, want 12/34/303/56", rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheWriteTokens)
	}
}

func TestSessionEvidenceReportsConflictingPersistedValues(t *testing.T) {
	rec := sessions.SessionRecord{
		TaskID: "TASK-1", Status: sessions.StatusCompleted,
		InputTokens: 12, FilesChanged: 2,
	}
	local := sessions.SessionRecord{
		TaskID: "TASK-1", Status: sessions.StatusCompleted,
		InputTokens: 13, FilesChanged: 3,
	}
	conflicts := enrichSessionRecordFromLocal(&rec, local)
	evidence := sessionEvidence(rec, conflicts)
	if evidence.Status != "conflict" || evidence.UsageStatus != "conflict" {
		t.Fatalf("evidence = %+v, want usage conflict", evidence)
	}
	if len(evidence.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v, want input_tokens and files_changed", evidence.Conflicts)
	}
	if rec.InputTokens != 12 || rec.FilesChanged != 2 {
		t.Fatalf("conflicting source silently overwrote canonical values: %+v", rec)
	}
}

func TestSessionEvidenceMarksZeroUsageUnavailable(t *testing.T) {
	evidence := sessionEvidence(sessions.SessionRecord{}, nil)
	if evidence.Status != "ok" || evidence.UsageStatus != "unavailable" {
		t.Fatalf("evidence = %+v, want ok/unavailable", evidence)
	}
	if evidence.Conflicts == nil {
		t.Fatal("conflicts must serialize as [] rather than null")
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

func TestSessionServiceSharedRuntimeCannotLeakAcrossWorkspaces(t *testing.T) {
	ctx := t.Context()
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{AgentName: "agent-a", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "SHARED-TASK", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	st := memstore.New()
	for _, key := range []string{"WS-A", "WS-B"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: key, Name: key}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS-A", SessionID: sess.SessionID(), AgentID: "agent-a",
		TaskID: "SHARED-TASK", Status: domain.AgentSessionCompleted,
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS-B", "SHARED-TASK")
	if err != nil {
		t.Fatalf("ListTaskSessions WS-B: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("WS-B sessions = %+v, want none", items)
	}
	if _, err := svc.GetSession(ctx, "WS-B", "SHARED-TASK", sess.SessionID()); !serviceErrorIsNotFound(err) {
		t.Fatalf("foreign GetSession error = %v, want not found", err)
	}
	items, err = svc.ListTaskSessions(ctx, "WS-A", "SHARED-TASK")
	if err != nil || len(items) != 1 {
		t.Fatalf("WS-A sessions = %+v err=%v, want one", items, err)
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
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: sess.SessionID(), AgentID: "desktopqa",
		TaskID: "DESKTOP-QA-3", Status: domain.AgentSessionCompleted,
	}); err != nil {
		t.Fatalf("create workspace ownership record: %v", err)
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
