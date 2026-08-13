package sessioncoord

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func (s *sessionServiceImpl) GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return "", apperrors.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", apperrors.ErrValidation("invalid session ID")
	}
	return s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
}

func (s *sessionServiceImpl) controlPlaneSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	if s.captures == nil {
		return "", apperrors.ErrUnavailable("run capture is unavailable")
	}
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return s.runCaptureDiff(ctx, runcapture.Query{
			WorkspaceKey: wsID, OwnerKind: runcapture.OwnerExecution,
			OwnerID: run.TaskRunID, WorkItemID: run.TaskID,
		})
	}
	if !serviceErrorNotFound(runErr) {
		return "", runErr
	}
	record, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return "", err
	}
	if taskRunID := strings.TrimSpace(record.Metadata["task_run_id"]); taskRunID != "" {
		return s.runCaptureDiff(ctx, runcapture.Query{
			WorkspaceKey: wsID, OwnerKind: runcapture.OwnerExecution,
			OwnerID: taskRunID, WorkItemID: taskID,
		})
	}
	return s.runCaptureDiff(ctx, runcapture.Query{
		WorkspaceKey: wsID, OwnerKind: runcapture.OwnerInteraction,
		OwnerID: record.SessionID, AgentID: record.AgentID, WorkItemID: taskID,
	})
}

func (s *sessionServiceImpl) runCaptureDiff(ctx context.Context, query runcapture.Query) (string, error) {
	evidence, err := s.captures.ReadEvidence(ctx, query, artifactsmodule.EvidenceDiff)
	if err != nil {
		return "", runCaptureReadError(err)
	}
	if evidence == nil {
		return "", apperrors.ErrInternal("run capture returned no diff projection", runcapture.ErrInvalidPersistedState)
	}
	switch evidence.Evidence.State {
	case runcapture.EvidenceFinalized, runcapture.EvidenceTruncated:
		return string(evidence.Content), nil
	case runcapture.EvidenceMissing:
		return "", apperrors.ErrNotFound("diff not found")
	case runcapture.EvidencePending:
		return "", apperrors.ErrUnavailable("diff capture is still pending")
	case runcapture.EvidenceCaptureFailed:
		return "", apperrors.ErrUnavailable("diff capture failed")
	case runcapture.EvidenceContentUnavailable:
		return "", apperrors.ErrUnavailable("diff content is temporarily unavailable")
	case runcapture.EvidenceCorrupt:
		return "", apperrors.ErrInternal("diff evidence is corrupt", runcapture.ErrEvidenceCorrupt)
	default:
		return "", apperrors.ErrInternal("run capture returned an invalid evidence state", runcapture.ErrInvalidPersistedState)
	}
}
