//nolint:revive // Tests use the established driver package to exercise internal seams.
package driver

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type sessionReconcilerFixture struct {
	ctx context.Context
	st  *memstore.Store
	run *domain.TaskRun
	req TaskExecRequest
}

func newSessionReconcilerFixture(t *testing.T) sessionReconcilerFixture {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	run, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42,
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	return sessionReconcilerFixture{ctx: ctx, st: st, run: run, req: TaskExecRequest{
		WorkspaceKey: "WS", TaskRunID: run.TaskRunID, TaskID: run.TaskID,
		NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: "lease-token", FencingToken: run.FencingToken,
	}}
}

func (f sessionReconcilerFixture) open(t *testing.T, invocationKey string) store.SessionRef {
	t.Helper()
	ref, err := f.st.AgentSessions().Open(f.ctx, store.SessionRunContext{
		WorkspaceKey: f.run.WorkspaceKey, TaskRunID: f.run.TaskRunID,
		Attempt: 1, FencingToken: f.run.FencingToken,
	}, store.SessionDescriptor{InvocationKey: invocationKey, Backend: "codex", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Open(%s): %v", invocationKey, err)
	}
	return ref
}

func TestTaskRunSessionReconcilerOutcomeMappings(t *testing.T) {
	tests := []struct {
		name       string
		result     TaskExecResult
		wantStatus domain.AgentSessionStatus
		wantClass  string
	}{
		{"completed", TaskExecResult{Status: domain.TaskRunCompleted}, domain.AgentSessionFailed, sessionErrorUnclosed},
		{"cancelled", TaskExecResult{Status: domain.TaskRunCancelled}, domain.AgentSessionCancelled, sessionErrorCancelled},
		{"failed", TaskExecResult{Status: domain.TaskRunFailed, ErrorClass: "runner_failed", ErrorMessage: "boom"}, domain.AgentSessionFailed, sessionErrorUnclosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSessionReconcilerFixture(t)
			ref := f.open(t, "agent")
			got, err := (&TaskRunSessionReconciler{Store: f.st}).ReconcileBridge(f.ctx, f.req, tt.result, nil)
			if err != nil || got.Unclosed != 1 || got.Settled != 1 || got.RegistryVisible != 0 {
				t.Fatalf("ReconcileBridge = %+v, %v", got, err)
			}
			assertReconciledSession(t, f.st, ref, tt.wantStatus, tt.wantClass, SessionFinalizedByBridge)
			session, _ := f.st.AgentSessions().Get(f.ctx, "WS", ref.SessionID)
			if session.Metadata["task_run_status"] != string(tt.result.Status) {
				t.Fatalf("task_run_status = %q, want anticipated %q", session.Metadata["task_run_status"], tt.result.Status)
			}
			if tt.name == "failed" {
				if session.Metadata["task_run_error_class"] != "runner_failed" || session.Metadata["task_run_error_message"] != "boom" {
					t.Fatalf("run error metadata = %+v", session.Metadata)
				}
			}
		})
	}
}

func TestTaskRunSessionReconcilerLeavesClosedAndSettlesRemainder(t *testing.T) {
	f := newSessionReconcilerFixture(t)
	closed := f.open(t, "closed")
	open := f.open(t, "open")
	if _, err := f.st.AgentSessions().Finalize(f.ctx, closed, store.SessionOutcome{
		Status: domain.AgentSessionCompleted, Summary: "leaf closed",
	}); err != nil {
		t.Fatalf("leaf Finalize: %v", err)
	}
	got, err := (&TaskRunSessionReconciler{Store: f.st}).ReconcileBridge(f.ctx, f.req, TaskExecResult{Status: domain.TaskRunCompleted}, nil)
	if err != nil || got.Unclosed != 1 || got.Settled != 1 {
		t.Fatalf("ReconcileBridge = %+v, %v", got, err)
	}
	assertReconciledSession(t, f.st, open, domain.AgentSessionFailed, sessionErrorUnclosed, SessionFinalizedByBridge)
	leaf, _ := f.st.AgentSessions().Get(f.ctx, "WS", closed.SessionID)
	if leaf.Status != domain.AgentSessionCompleted || leaf.Summary != "leaf closed" {
		t.Fatalf("leaf session changed: %+v", leaf)
	}
}

func TestTaskRunSessionReconcilerSalvagesOnlyUploadedTranscript(t *testing.T) {
	tests := []struct {
		name     string
		upload   bool
		finalize bool
		want     int
	}{
		{name: "uploaded", upload: true, want: 1},
		{name: "already-finalized", upload: true, finalize: true},
		{name: "declared-without-upload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSessionReconcilerFixture(t)
			ref := f.open(t, "agent")
			artifactID := seedTranscriptArtifact(t, f, ref, tt.upload, tt.finalize)
			got, err := (&TaskRunSessionReconciler{Store: f.st}).ReconcileBridge(f.ctx, f.req, TaskExecResult{Status: domain.TaskRunCompleted}, nil)
			if err != nil {
				t.Fatalf("ReconcileBridge: %v", err)
			}
			session, _ := f.st.AgentSessions().Get(f.ctx, "WS", ref.SessionID)
			artifact, _ := f.st.Artifacts().Get(f.ctx, "WS", artifactID)
			if tt.want == 1 && (got.TranscriptSalvage != 1 || artifact.DurableStatus != "finalized" || artifact.Metadata["transcript_partial"] != "true" || session.Metadata["transcript_ref"] != "artifact://"+artifactID || session.Metadata["transcript_partial"] != "true") {
				t.Fatalf("uploaded salvage result=%+v session=%+v artifact=%+v", got, session, artifact)
			}
			if tt.want == 0 && (got.TranscriptSalvage != 0 || artifact.Metadata["transcript_partial"] != "" || session.Metadata["transcript_ref"] != "" || session.Metadata["transcript_partial"] != "") {
				t.Fatalf("out-of-window artifact was marked partial: result=%+v session=%+v artifact=%+v", got, session, artifact)
			}
		})
	}
}

func seedTranscriptArtifact(t *testing.T, f sessionReconcilerFixture, ref store.SessionRef, upload, finalize bool) string {
	t.Helper()
	artifactID := store.TranscriptArtifactID(f.run.TaskRunID, ref.Attempt, "agent")
	_, err := f.st.Artifacts().Create(f.ctx, store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: artifactID, SessionID: ref.SessionID,
		OwnerType: "task_run", OwnerID: f.run.TaskRunID, Type: sessionTranscriptType, DurableStatus: "declared",
	})
	if err != nil {
		t.Fatalf("Create transcript artifact: %v", err)
	}
	if upload {
		if _, err := f.st.Artifacts().UploadContent(f.ctx, "WS", artifactID, store.ArtifactContentUpload{Body: bytes.NewBufferString("event\n")}); err != nil {
			t.Fatalf("Upload transcript: %v", err)
		}
	}
	if finalize {
		if _, err := f.st.Artifacts().Finalize(f.ctx, "WS", artifactID, store.ArtifactFinalize{}); err != nil {
			t.Fatalf("Finalize transcript: %v", err)
		}
	}
	return artifactID
}

func TestTaskRunSessionReconciliationLoopSettlesCurrentFenceOnly(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	_, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-loop", TaskID: "TASK-1", Status: domain.TaskRunCompleted,
		FencingToken: 99, RuntimeMetadata: map[string]string{"scheduler_attempt": "1"},
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	old := createLoopSession(t, st, "old-attempt", 1, 42)
	current := createLoopSession(t, st, "current-attempt", 2, 99)
	wrongFence := createLoopSession(t, st, "wrong-fence", 2, 100)
	counting := &countingTaskRunStore{TaskRunStore: st.TaskRuns()}
	wrapped := taskRunStoreOverride{Store: st, taskRuns: counting}
	result, err := (&TaskRunSessionReconciliationLoop{Store: wrapped, WorkspaceKey: "WS"}).RunOnce(ctx)
	if err != nil || result.Settled != 1 {
		t.Fatalf("RunOnce = %+v, %v", result, err)
	}
	if counting.gets != 1 {
		t.Fatalf("TaskRun Get calls = %d, want 1 for sessions sharing a parent", counting.gets)
	}
	oldSession, _ := st.AgentSessions().Get(ctx, "WS", old.SessionID)
	if oldSession.Status != domain.AgentSessionRunning {
		t.Fatalf("old attempt session changed: %+v", oldSession)
	}
	wrongFenceSession, _ := st.AgentSessions().Get(ctx, "WS", wrongFence.SessionID)
	if wrongFenceSession.Status != domain.AgentSessionRunning {
		t.Fatalf("wrong fence session changed: %+v", wrongFenceSession)
	}
	assertReconciledSession(t, st, current, domain.AgentSessionFailed, sessionErrorUnclosed, SessionFinalizedByLoop)
	settled, _ := st.AgentSessions().Get(ctx, "WS", current.SessionID)
	if settled.Metadata["swept_by"] != SessionFinalizedByLoop {
		t.Fatalf("swept_by metadata = %+v", settled.Metadata)
	}
}

func TestTaskRunSessionReconciliationLoopHonorsLimit(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for _, runID := range []string{"task-run-limit-1", "task-run-limit-2"} {
		if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
			WorkspaceKey: "WS", TaskRunID: runID, TaskID: "TASK-1", Status: domain.TaskRunCompleted,
			FencingToken: 99,
		}); err != nil {
			t.Fatalf("Create task run %q: %v", runID, err)
		}
		createSessionForRun(t, st, runID, "session-"+runID, 1, 99)
	}
	result, err := (&TaskRunSessionReconciliationLoop{Store: st, WorkspaceKey: "WS", Limit: 1}).RunOnce(ctx)
	if err != nil || result.Settled != 1 {
		t.Fatalf("RunOnce = %+v, %v", result, err)
	}
	remaining, err := st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{NonTerminal: true})
	if err != nil || len(remaining) != 1 {
		t.Fatalf("remaining sessions = %+v, %v; want one deferred to next tick", remaining, err)
	}
}

func createLoopSession(t *testing.T, st *memstore.Store, id string, attempt int, fence int64) store.SessionRef {
	t.Helper()
	return createSessionForRun(t, st, "task-run-loop", id, attempt, fence)
}

func createSessionForRun(t *testing.T, st *memstore.Store, taskRunID, id string, attempt int, fence int64) store.SessionRef {
	t.Helper()
	session, err := st.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: id, AgentID: "codex", TaskRunID: taskRunID,
		InvocationKey: id, Status: domain.AgentSessionRunning, Attempt: attempt,
		Metadata: map[string]string{store.SessionMetadataFencingToken: stringInt64(fence)},
	})
	if err != nil {
		t.Fatalf("Create loop session: %v", err)
	}
	return store.SessionRef{WorkspaceKey: "WS", SessionID: session.SessionID, Attempt: attempt}
}

type taskRunStoreOverride struct {
	store.Store
	taskRuns store.TaskRunStore
}

func (s taskRunStoreOverride) TaskRuns() store.TaskRunStore { return s.taskRuns }

type countingTaskRunStore struct {
	store.TaskRunStore
	gets int
}

func (s *countingTaskRunStore) Get(ctx context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error) {
	s.gets++
	return s.TaskRunStore.Get(ctx, workspaceKey, taskRunID)
}

type fixedTaskRunGetStore struct {
	store.TaskRunStore
	run *domain.TaskRun
}

func (s fixedTaskRunGetStore) Get(context.Context, string, string) (*domain.TaskRun, error) {
	return s.run, nil
}

func TestTaskRunSessionReconcilerRefusesChangedBridgeOwnership(t *testing.T) {
	f := newSessionReconcilerFixture(t)
	newAttempt := *f.run
	newAttempt.NodeID = "node-2"
	newAttempt.LeaseID = "lease-2"
	newAttempt.FencingToken = 84
	newAttempt.RuntimeMetadata = map[string]string{"scheduler_attempt": "1"}
	ref := createSessionForRun(t, f.st, f.run.TaskRunID, "attempt-2-live", 2, newAttempt.FencingToken)
	override := taskRunStoreOverride{
		Store: f.st,
		taskRuns: fixedTaskRunGetStore{
			TaskRunStore: f.st.TaskRuns(),
			run:          &newAttempt,
		},
	}
	_, err := (&TaskRunSessionReconciler{Store: override}).ReconcileBridge(
		f.ctx, f.req, TaskExecResult{Status: domain.TaskRunCompleted}, nil,
	)
	if !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("ReconcileBridge error = %v, want ErrNotOwner", err)
	}
	session, getErr := f.st.AgentSessions().Get(f.ctx, "WS", ref.SessionID)
	if getErr != nil || session.Status != domain.AgentSessionRunning {
		t.Fatalf("new attempt session changed: session=%+v err=%v", session, getErr)
	}
}

func TestTaskRunSessionOpenRegistryIsRunAndFenceScoped(t *testing.T) {
	registry := NewTaskRunSessionOpenRegistry()
	run := store.SessionRunContext{WorkspaceKey: "WS", TaskRunID: "run-1", Attempt: 1, FencingToken: 42}
	registry.Record(run, store.SessionRef{WorkspaceKey: "WS", SessionID: "session-1", Attempt: 1})
	otherFence := run
	otherFence.FencingToken = 43
	registry.Record(otherFence, store.SessionRef{WorkspaceKey: "WS", SessionID: "session-2", Attempt: 1})
	if got := registry.Live(run); len(got) != 1 || got[0].SessionID != "session-1" {
		t.Fatalf("Live = %+v, want session-1 only", got)
	}
	registry.Forget(run)
	if got := registry.Live(run); len(got) != 0 {
		t.Fatalf("Live after Forget = %+v, want empty", got)
	}
	if got := registry.Live(otherFence); len(got) != 1 || got[0].SessionID != "session-2" {
		t.Fatalf("other fence after Forget = %+v, want session-2", got)
	}
}

func TestTaskRunSessionReconcilerRegistryIsAdvisoryAndCleanedUp(t *testing.T) {
	f := newSessionReconcilerFixture(t)
	registry := NewTaskRunSessionOpenRegistry()
	run := store.SessionRunContext{
		WorkspaceKey: "WS", TaskRunID: f.run.TaskRunID, Attempt: 1, FencingToken: f.run.FencingToken,
	}
	registry.Record(run, store.SessionRef{WorkspaceKey: "WS", SessionID: "registry-only", Attempt: 1})
	got, err := (&TaskRunSessionReconciler{Store: f.st, OpenRegistry: registry}).ReconcileBridge(
		f.ctx, f.req, TaskExecResult{Status: domain.TaskRunCompleted}, nil,
	)
	if err != nil || got.RegistryVisible != 1 || got.Unclosed != 0 || got.Settled != 0 {
		t.Fatalf("ReconcileBridge = %+v, %v", got, err)
	}
	if live := registry.Live(run); len(live) != 0 {
		t.Fatalf("registry not cleaned after reconciliation: %+v", live)
	}
}

func TestHostBridgeAddsBreadcrumbWithoutChangingRunOutcome(t *testing.T) {
	f := newSessionReconcilerFixture(t)
	ref := f.open(t, "agent")
	executor := HostBridgeTaskExecutor{
		Store: f.st, WorktreePath: t.TempDir(), Command: hostBridgeHelperCommand(t, "success", "", ""),
		SessionReconciler: &TaskRunSessionReconciler{Store: f.st},
	}
	result, err := executor.ExecuteTask(f.ctx, f.req)
	if err != nil || result.Status != domain.TaskRunCompleted || result.RuntimeMetadata["unclosed_sessions"] != "1" {
		t.Fatalf("ExecuteTask = %+v, %v", result, err)
	}
	run, getErr := f.st.TaskRuns().Get(f.ctx, "WS", f.run.TaskRunID)
	if getErr != nil || run.RuntimeMetadata["bridge_task_plane"] != "true" {
		t.Fatalf("bridge task-plane marker: run=%+v err=%v", run, getErr)
	}
	assertReconciledSession(t, f.st, ref, domain.AgentSessionFailed, sessionErrorUnclosed, SessionFinalizedByBridge)
}

type failingSessionUpdateStore struct {
	store.Store
	sessions store.AgentSessionStore
}

func (s failingSessionUpdateStore) AgentSessions() store.AgentSessionStore { return s.sessions }

type failingSessionList struct{ store.AgentSessionStore }

func (s failingSessionList) List(context.Context, string, store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	return nil, errors.New("list sessions failed")
}

func TestHostBridgeRecordsButDoesNotPromoteReconcilerError(t *testing.T) {
	f := newSessionReconcilerFixture(t)
	override := failingSessionUpdateStore{
		Store: f.st, sessions: failingSessionList{AgentSessionStore: f.st.AgentSessions()},
	}
	result, err := (HostBridgeTaskExecutor{
		Store: override, WorktreePath: t.TempDir(), Command: hostBridgeHelperCommand(t, "success", "", ""),
		SessionReconciler: &TaskRunSessionReconciler{Store: override},
	}).ExecuteTask(f.ctx, f.req)
	if err != nil || result.Status != domain.TaskRunCompleted {
		t.Fatalf("ExecuteTask promoted reconcile error: result=%+v err=%v", result, err)
	}
	if result.RuntimeMetadata["unclosed_sessions"] != "0" || result.RuntimeMetadata["session_reconcile_error"] == "" {
		t.Fatalf("reconcile error metadata = %+v", result.RuntimeMetadata)
	}
}

func assertReconciledSession(t *testing.T, st store.Store, ref store.SessionRef, status domain.AgentSessionStatus, errorClass, finalizedBy string) {
	t.Helper()
	session, err := st.AgentSessions().Get(context.Background(), ref.WorkspaceKey, ref.SessionID)
	if err != nil {
		t.Fatalf("Get reconciled session: %v", err)
	}
	if session.Status != status || session.ErrorClass != errorClass || session.Metadata["finalized_by"] != finalizedBy || session.Summary == "" {
		t.Fatalf("reconciled session = %+v", session)
	}
}

func stringInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
