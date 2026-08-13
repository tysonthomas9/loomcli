package sessioncoord

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

var _ SessionService = (*sessionServiceImpl)(nil)
var _ AgentSessionTranscriptService = (*sessionServiceImpl)(nil)

func sessionControlPlaneReadError(message string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, artifactsmodule.ErrNotFound):
		return apperrors.ErrNotFound(message)
	case errors.Is(err, domain.ErrRateLimited):
		return apperrors.NewServiceError(apperrors.KindRateLimited, message, err)
	case errors.Is(err, domain.ErrUnavailable), errors.Is(err, artifactsmodule.ErrUnavailable):
		return apperrors.ErrUnavailable(message)
	default:
		return apperrors.ErrInternal(message, err)
	}
}

type sessionServiceImpl struct {
	store     ProjectionReader
	captures  runcapture.API
	histStore HistoryReader
}

type ProjectionReader interface {
	storeadapter.WorkspaceTopologyReader
	TaskRuns() store.TaskRunStore
	AgentSessions() store.AgentSessionStore
}

func NewSessionService(
	st ProjectionReader,
	histStore HistoryReader,
	captures runcapture.API,
) SessionService {
	return &sessionServiceImpl{store: st, captures: captures, histStore: histStore}
}

func (s *sessionServiceImpl) ListTaskSessions(ctx context.Context, wsID, taskID string) ([]SessionListItem, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, apperrors.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}
	return s.controlPlaneTaskSessions(ctx, wsID, taskID)
}

func firstNonEmptySessionValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *sessionServiceImpl) controlPlaneTaskSessions(ctx context.Context, wsID, taskID string) ([]SessionListItem, error) {
	if s.store == nil || s.store.TaskRuns() == nil || s.store.AgentSessions() == nil {
		return nil, apperrors.ErrUnavailable("run capture archive is unavailable")
	}
	taskRuns, err := s.store.TaskRuns().List(ctx, wsID, store.TaskRunFilter{TaskID: taskID})
	if err != nil {
		return nil, sessionControlPlaneReadError("failed to list task runs", err)
	}
	records, interactionErr := s.store.AgentSessions().List(ctx, wsID, store.AgentSessionFilter{TaskID: taskID})
	if interactionErr != nil && len(taskRuns) == 0 {
		return nil, sessionControlPlaneReadError("failed to list interaction sessions", interactionErr)
	}
	items := make([]SessionListItem, 0, len(taskRuns)+len(records))
	representedSessionIDs := make(map[string]struct{}, len(taskRuns))
	for _, run := range taskRuns {
		if run == nil || run.WorkspaceKey != wsID || run.TaskID != taskID {
			continue
		}
		item := newSessionListItem(sessionRecordFromTaskRun(run), isActiveTaskRun(run.Status))
		s.fillExecutionTaskRunEvidence(ctx, &item, run)
		items = append(items, item)
		representedSessionIDs[item.SessionID] = struct{}{}
	}
	for _, rec := range records {
		if rec == nil || rec.WorkspaceKey != wsID {
			continue
		}
		if _, represented := representedSessionIDs[rec.SessionID]; represented {
			continue
		}
		item := newSessionListItem(sessionRecordFromAgentSession(rec), isActiveAgentSession(rec.Status))
		fillExecutionEvidence(&item, rec.Metadata)
		s.fillInteractionEvidence(ctx, &item, rec)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt.After(items[j].StartedAt) })
	return items, nil
}

func newSessionListItem(record SessionRecordView, active bool) SessionListItem {
	return SessionListItem{
		SessionRecordView:        record,
		IsActive:                 active,
		TranscriptEvidenceStatus: string(runcapture.EvidenceMissing),
		DiffEvidenceStatus:       string(runcapture.EvidenceMissing),
	}
}

func fillExecutionEvidence(item *SessionListItem, metadata map[string]string) {
	if item == nil || metadata == nil {
		return
	}
	item.RuntimeStrategy = metadata["runtime_strategy"]
	item.DeliveryMode = metadata["delivery"]
	item.PatchBackStatus = metadata["patch_back_status"]
	item.LogsRef = metadata["logs_ref"]
	item.LocalBranch = metadata["local_branch"]
	item.HeadSHA = firstNonEmptySessionValue(metadata["head_sha"], metadata["github_head_sha"], metadata["patch_back_head_sha"])
	item.GitHubBranch = metadata["github_branch"]
	item.GitHubPRURL = metadata["github_pr_url"]
}

func (s *sessionServiceImpl) fillInteractionEvidence(ctx context.Context, item *SessionListItem, rec *domain.AgentSession) {
	if item == nil || rec == nil {
		return
	}
	if s.captures == nil {
		return
	}
	capture, err := s.captures.Get(ctx, runcapture.Query{
		WorkspaceKey: rec.WorkspaceKey, OwnerKind: runcapture.OwnerInteraction,
		OwnerID: rec.SessionID, AgentID: rec.AgentID, WorkItemID: item.TaskID,
	})
	if err != nil || capture == nil {
		if err != nil && !errors.Is(err, runcapture.ErrNotFound) {
			item.TranscriptEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
			item.DiffEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
		}
		return
	}
	for _, evidence := range capture.Evidence {
		applySessionEvidence(item, evidence)
	}
}

func applySessionEvidence(item *SessionListItem, evidence runcapture.Evidence) {
	if item == nil {
		return
	}
	visible := evidence.State == runcapture.EvidenceFinalized || evidence.State == runcapture.EvidenceTruncated
	switch evidence.Kind {
	case artifactsmodule.EvidenceTranscript:
		item.TranscriptEvidenceStatus = string(evidence.State)
		item.TranscriptFailureClass = evidence.FailureClass
		item.HasTranscript = visible
	case artifactsmodule.EvidenceDiff:
		item.DiffEvidenceStatus = string(evidence.State)
		item.DiffFailureClass = evidence.FailureClass
		item.HasDiff = visible
	}
}

func sessionRecordFromAgentSession(rec *domain.AgentSession) SessionRecordView {
	startedAt := rec.StartedAt
	if startedAt.IsZero() {
		startedAt = rec.CreatedAt
	}
	taskID := rec.TaskID
	backend := ""
	if rec.Metadata != nil {
		if taskID == "" {
			taskID = rec.Metadata["task_id"]
		}
		backend = firstNonEmptySessionValue(rec.Metadata["backend"], rec.Metadata["runtime"])
	}
	diffMeta := decodeDiffStatsMetadata(rec.Metadata)
	out := SessionRecordView{
		SchemaVersion: 1, SessionID: rec.SessionID, TaskID: taskID, AgentName: rec.AgentID,
		Backend: backend, Phase: rec.Phase, StartedAt: startedAt, Status: sessionStatusFromAgentSession(rec.Status),
		AttemptNum: rec.Attempt, ErrorClass: rec.ErrorClass, FilesChanged: diffMeta.FilesChanged,
		LinesAdded: diffMeta.LinesAdded, LinesRemoved: diffMeta.LinesRemoved, FilesTouched: diffMeta.FilesTouched,
	}
	if rec.FinishedAt != nil {
		out.EndedAt = rec.FinishedAt
		if !startedAt.IsZero() {
			out.DurationS = rec.FinishedAt.Sub(startedAt).Seconds()
		}
	}
	if rec.ExitCode != nil {
		out.ExitCode = *rec.ExitCode
	}
	return out
}

func sessionStatusFromAgentSession(status domain.AgentSessionStatus) SessionStatus {
	switch status {
	case domain.AgentSessionCompleted:
		return StatusCompleted
	case domain.AgentSessionFailed:
		return StatusFailed
	case domain.AgentSessionCancelled, domain.AgentSessionExpired:
		return StatusAborted
	default:
		return StatusRunning
	}
}

func isActiveAgentSession(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionCompleted, domain.AgentSessionFailed, domain.AgentSessionCancelled, domain.AgentSessionExpired:
		return false
	default:
		return true
	}
}

func (s *sessionServiceImpl) GetSession(ctx context.Context, wsID, taskID, sessionID string) (*SessionDetailData, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) || sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid task or session ID")
	}
	return s.controlPlaneSession(ctx, wsID, taskID, sessionID)
}

func (s *sessionServiceImpl) controlPlaneSessionRecord(ctx context.Context, wsID, taskID, sessionID string) (*domain.AgentSession, error) {
	if s.store == nil || s.store.AgentSessions() == nil {
		return nil, apperrors.ErrNotFound("session not found")
	}
	rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
	if err != nil {
		return nil, sessionControlPlaneReadError("failed to load session", err)
	}
	if rec == nil || rec.WorkspaceKey != wsID || (rec.TaskID != taskID && (rec.Metadata == nil || rec.Metadata["task_id"] != taskID)) {
		return nil, apperrors.ErrNotFound("session not found")
	}
	return rec, nil
}

func (s *sessionServiceImpl) controlPlaneSession(ctx context.Context, wsID, taskID, sessionID string) (*SessionDetailData, error) {
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return &SessionDetailData{SessionMetadataView: SessionMetadataView{SessionRecordView: sessionRecordFromTaskRun(run)}, IsActive: isActiveTaskRun(run.Status)}, nil
	}
	if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	return &SessionDetailData{SessionMetadataView: SessionMetadataView{SessionRecordView: sessionRecordFromAgentSession(rec)}, IsActive: isActiveAgentSession(rec.Status)}, nil
}

func (s *sessionServiceImpl) GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]artifactsmodule.Event, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) || sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid task or session ID")
	}
	return s.controlPlaneSessionTranscript(ctx, wsID, taskID, sessionID)
}

func serviceErrorNotFound(err error) bool {
	var svcErr *apperrors.ServiceError
	return errors.As(err, &svcErr) && svcErr.Kind == apperrors.KindNotFound
}
