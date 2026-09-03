package driver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	SessionFinalizedByBridge = "task_run_session_reconciler"
	SessionFinalizedByLoop   = "session_reconciliation_loop"
	SessionFinalizedByStale  = "stale_task_run_recovery"

	sessionErrorUnclosed  = "agent_session_unclosed"
	sessionErrorCancelled = "driver_cancelled"
	sessionTranscriptType = "agent-transcript"

	defaultTaskRunSessionReconcileLimit = 500
)

// TaskRunSessionReconciler settles invocation sessions left open when a task
// run reaches an outcome. Store discovery is authoritative; OpenRegistry only
// supplies serve-hosted live visibility while an invocation is still running.
type TaskRunSessionReconciler struct {
	Store        store.Store
	OpenRegistry *TaskRunSessionOpenRegistry
}

// TaskRunSessionReconcileResult reports the non-terminal sessions observed
// before reconciliation. Errors are surfaced to callers for recording, never
// as a reason to change the task-run outcome.
type TaskRunSessionReconcileResult struct {
	Unclosed          int
	Settled           int
	RegistryVisible   int
	TranscriptSalvage int
}

// ReconcileBridge runs before the task worker persists its terminal TaskRun.
// It refuses stale bridge ownership, so a prior attempt cannot settle a newer
// claim's sessions.
func (r *TaskRunSessionReconciler) ReconcileBridge(ctx context.Context, req TaskExecRequest, result TaskExecResult, execErr error) (TaskRunSessionReconcileResult, error) {
	run, err := r.currentBridgeRun(ctx, req)
	if err != nil {
		return TaskRunSessionReconcileResult{}, err
	}
	anticipated := bridgeTaskRunOutcome(result, execErr)
	outcome := taskRunSessionOutcome(anticipated, SessionFinalizedByBridge)
	if r.OpenRegistry != nil {
		runContext := store.SessionRunContext{
			WorkspaceKey: req.WorkspaceKey, TaskRunID: req.TaskRunID,
			Attempt: store.TaskRunClaimAttempt(run), FencingToken: req.FencingToken,
		}
		visible := r.OpenRegistry.Live(runContext)
		defer r.OpenRegistry.Forget(runContext)
		return r.reconcile(ctx, run, anticipated.Status, outcome, SessionFinalizedByBridge, "", len(visible))
	}
	return r.reconcile(ctx, run, anticipated.Status, outcome, SessionFinalizedByBridge, "", 0)
}

// ReconcileTerminalTaskRun is the terminal-parent backstop used by the serve
// loop and the stale-task recovery pass. The supplied TaskRun is rechecked
// against every session's attempt and fencing stamp before Finalize is called.
func (r *TaskRunSessionReconciler) ReconcileTerminalTaskRun(ctx context.Context, run *domain.TaskRun, finalizedBy, sweptBy string) (TaskRunSessionReconcileResult, error) {
	if run == nil || !run.Status.IsTerminal() {
		return TaskRunSessionReconcileResult{}, fmt.Errorf("terminal task run required: %w", domain.ErrInvalid)
	}
	return r.reconcile(ctx, run, run.Status, taskRunSessionOutcome(run, finalizedBy), finalizedBy, sweptBy, 0)
}

func (r *TaskRunSessionReconciler) currentBridgeRun(ctx context.Context, req TaskExecRequest) (*domain.TaskRun, error) {
	if r == nil || r.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	run, err := r.Store.TaskRuns().Get(ctx, req.WorkspaceKey, req.TaskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run for session reconciliation: %w", err)
	}
	if run.Status.IsTerminal() || run.FencingToken != req.FencingToken || run.LeaseID != req.LeaseID || run.NodeID != req.NodeID {
		return nil, fmt.Errorf("task run %q bridge ownership changed: %w", req.TaskRunID, domain.ErrNotOwner)
	}
	return run, nil
}

func (r *TaskRunSessionReconciler) reconcile(ctx context.Context, run *domain.TaskRun, anticipatedStatus domain.TaskRunStatus, outcome store.SessionOutcome, finalizedBy, sweptBy string, visible int) (TaskRunSessionReconcileResult, error) {
	if r == nil || r.Store == nil {
		return TaskRunSessionReconcileResult{}, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	attempt := store.TaskRunClaimAttempt(run)
	sessions, err := r.Store.AgentSessions().List(ctx, run.WorkspaceKey, store.AgentSessionFilter{
		TaskRunID: run.TaskRunID, Attempt: &attempt, NonTerminal: true,
	})
	if err != nil {
		return TaskRunSessionReconcileResult{}, fmt.Errorf("list open task-run sessions: %w", err)
	}
	out := TaskRunSessionReconcileResult{Unclosed: len(sessions), RegistryVisible: visible}
	var errs []error
	for _, session := range sessions {
		if !sessionMatchesTaskRunFence(session, run, attempt) {
			continue
		}
		settled, salvaged, err := r.finalizeSession(ctx, session, run, anticipatedStatus, outcome, finalizedBy, sweptBy)
		if settled {
			out.Settled++
		}
		if salvaged {
			out.TranscriptSalvage++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func sessionMatchesTaskRunFence(session *domain.AgentSession, run *domain.TaskRun, attempt int) bool {
	if session == nil || run == nil || session.TaskRunID != run.TaskRunID || session.Attempt != attempt {
		return false
	}
	fence := strconv.FormatInt(run.FencingToken, 10)
	return session.Metadata[store.SessionMetadataFencingToken] == fence
}

func (r *TaskRunSessionReconciler) finalizeSession(ctx context.Context, session *domain.AgentSession, run *domain.TaskRun, anticipatedStatus domain.TaskRunStatus, outcome store.SessionOutcome, finalizedBy, sweptBy string) (bool, bool, error) {
	transcriptRef, salvaged, salvageErr := r.salvageTranscript(ctx, session, run)
	outcome = reconcilerOutcome(outcome, run, anticipatedStatus, finalizedBy, sweptBy, transcriptRef, salvaged)
	_, finalizeErr := r.Store.AgentSessions().Finalize(ctx, store.SessionRef{
		WorkspaceKey: run.WorkspaceKey, SessionID: session.SessionID, Attempt: session.Attempt,
	}, outcome)
	settled := finalizeErr == nil || errors.Is(finalizeErr, domain.ErrConflict)
	if settled {
		finalizeErr = nil
	}
	if finalizeErr != nil {
		finalizeErr = fmt.Errorf("finalize agent session %q: %w", session.SessionID, finalizeErr)
	}
	return settled, salvaged, errors.Join(salvageErr, finalizeErr)
}

func reconcilerOutcome(outcome store.SessionOutcome, run *domain.TaskRun, anticipatedStatus domain.TaskRunStatus, finalizedBy, sweptBy, transcriptRef string, salvaged bool) store.SessionOutcome {
	metadata := map[string]string{
		"finalized_by":           finalizedBy,
		"task_run_id":            run.TaskRunID,
		"task_run_status":        string(anticipatedStatus),
		"task_run_attempt":       strconv.Itoa(store.TaskRunClaimAttempt(run)),
		"task_run_fencing_token": strconv.FormatInt(run.FencingToken, 10),
	}
	for key, value := range outcome.Metadata {
		metadata[key] = value
	}
	if sweptBy != "" {
		metadata["swept_by"] = sweptBy
	}
	if salvaged {
		metadata["transcript_partial"] = "true"
		outcome.TranscriptRef = transcriptRef
	}
	outcome.Metadata = metadata
	return outcome
}

func (r *TaskRunSessionReconciler) salvageTranscript(ctx context.Context, session *domain.AgentSession, run *domain.TaskRun) (string, bool, error) {
	if session.InvocationKey == "" {
		return "", false, nil
	}
	artifactID := store.TranscriptArtifactID(run.TaskRunID, session.Attempt, session.InvocationKey)
	artifact, err := r.Store.Artifacts().Get(ctx, run.WorkspaceKey, artifactID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get transcript artifact %q: %w", artifactID, err)
	}
	if artifact.Type != sessionTranscriptType || artifact.OwnerType != "task_run" || artifact.OwnerID != run.TaskRunID || artifact.DurableStatus != "uploading" {
		return "", false, nil
	}
	metadata := cloneStringMap(artifact.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["transcript_partial"] = "true"
	summary := "partial transcript salvaged after task-run session close was missed"
	if _, err := r.Store.Artifacts().Finalize(ctx, run.WorkspaceKey, artifactID, store.ArtifactFinalize{Summary: &summary, Metadata: &metadata}); err != nil {
		return "", false, fmt.Errorf("finalize transcript artifact %q: %w", artifactID, err)
	}
	return "artifact://" + artifactID, true, nil
}

func bridgeTaskRunOutcome(result TaskExecResult, execErr error) *domain.TaskRun {
	completion := normalizeTaskExecCompletion(result, execErr)
	return &domain.TaskRun{
		Status: completion.Status, ErrorClass: completion.ErrorClass, ErrorMessage: completion.ErrorMessage,
	}
}

func taskRunSessionOutcome(run *domain.TaskRun, finalizedBy string) store.SessionOutcome {
	metadata := map[string]string{}
	if run.ErrorClass != "" {
		metadata["task_run_error_class"] = run.ErrorClass
	}
	if run.ErrorMessage != "" {
		metadata["task_run_error_message"] = run.ErrorMessage
	}
	switch run.Status {
	case domain.TaskRunCancelled:
		return store.SessionOutcome{Status: domain.AgentSessionCancelled, ErrorClass: sessionErrorCancelled, Summary: "task run cancelled before agent session closed", Metadata: metadata}
	case domain.TaskRunCompleted:
		return store.SessionOutcome{Status: domain.AgentSessionFailed, ErrorClass: sessionErrorUnclosed, Summary: "task run completed before agent session closed", Metadata: metadata}
	default:
		summary := "task run failed before agent session closed"
		if finalizedBy == SessionFinalizedByStale {
			return store.SessionOutcome{Status: domain.AgentSessionFailed, ErrorClass: staleTaskRunErrorClass, Summary: "task run became stale before agent session closed", Metadata: metadata}
		}
		return store.SessionOutcome{Status: domain.AgentSessionFailed, ErrorClass: sessionErrorUnclosed, Summary: summary, Metadata: metadata}
	}
}

// TaskRunSessionReconciliationLoop is the serve-side terminal-parent
// backstop. It catches crashes after a TaskRun was persisted terminal but
// before its bridge reconciler had a chance to settle the leaf sessions.
type TaskRunSessionReconciliationLoop struct {
	Store        store.Store
	WorkspaceKey string
	Limit        int
}

// SessionReconciliationLoopResult aggregates one server backstop pass.
type SessionReconciliationLoopResult struct {
	Observed int
	Settled  int
	Skipped  int
}

func (l *TaskRunSessionReconciliationLoop) RunOnce(ctx context.Context) (*SessionReconciliationLoopResult, error) {
	if l == nil || l.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaces, err := resolveSweepWorkspaces(ctx, l.Store, l.WorkspaceKey, "session reconciliation")
	if err != nil {
		return nil, err
	}
	out := &SessionReconciliationLoopResult{}
	remaining := l.limit()
	var errs []error
	for _, workspace := range workspaces {
		if remaining == 0 {
			break
		}
		scanned, err := l.reconcileWorkspace(ctx, workspace, remaining, out)
		remaining -= scanned
		if err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func (l *TaskRunSessionReconciliationLoop) reconcileWorkspace(ctx context.Context, workspace string, limit int, out *SessionReconciliationLoopResult) (int, error) {
	sessions, err := l.Store.AgentSessions().List(ctx, workspace, store.AgentSessionFilter{
		NonTerminal: true, Limit: limit,
	})
	if err != nil {
		return 0, fmt.Errorf("list open sessions in workspace %q: %w", workspace, err)
	}
	cache := make(map[string]taskRunCacheEntry)
	var errs []error
	for _, session := range sessions {
		if err := l.reconcileSession(ctx, workspace, session, cache, out); err != nil {
			errs = append(errs, err)
		}
	}
	return len(sessions), errors.Join(errs...)
}

type taskRunCacheEntry struct {
	run *domain.TaskRun
	err error
}

func (l *TaskRunSessionReconciliationLoop) reconcileSession(ctx context.Context, workspace string, session *domain.AgentSession, cache map[string]taskRunCacheEntry, out *SessionReconciliationLoopResult) error {
	if session == nil || strings.TrimSpace(session.TaskRunID) == "" {
		out.Skipped++
		return nil
	}
	run, err := l.cachedTaskRun(ctx, workspace, session.TaskRunID, cache)
	if errors.Is(err, domain.ErrNotFound) {
		out.Skipped++
		return nil
	}
	if err != nil {
		return fmt.Errorf("get task run %q for session reconciliation: %w", session.TaskRunID, err)
	}
	if !run.Status.IsTerminal() || !sessionMatchesTaskRunFence(session, run, store.TaskRunClaimAttempt(run)) {
		out.Skipped++
		return nil
	}
	out.Observed++
	reconciler := TaskRunSessionReconciler{Store: l.Store}
	outcome := taskRunSessionOutcome(run, SessionFinalizedByLoop)
	settled, _, err := reconciler.finalizeSession(
		ctx, session, run, run.Status, outcome, SessionFinalizedByLoop, SessionFinalizedByLoop,
	)
	if settled {
		out.Settled++
	}
	return err
}

func (l *TaskRunSessionReconciliationLoop) cachedTaskRun(ctx context.Context, workspace, taskRunID string, cache map[string]taskRunCacheEntry) (*domain.TaskRun, error) {
	if entry, ok := cache[taskRunID]; ok {
		return entry.run, entry.err
	}
	run, err := l.Store.TaskRuns().Get(ctx, workspace, taskRunID)
	cache[taskRunID] = taskRunCacheEntry{run: run, err: err}
	return run, err
}

func (l *TaskRunSessionReconciliationLoop) limit() int {
	if l.Limit > 0 {
		return l.Limit
	}
	return defaultTaskRunSessionReconcileLimit
}
