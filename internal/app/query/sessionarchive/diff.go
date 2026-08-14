package sessionarchive

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

func (s *sessionServiceImpl) GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return "", queryError(ErrInvalid, "invalid task ID", nil)
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", queryError(ErrInvalid, "invalid session ID", nil)
	}
	return s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
}

func (s *sessionServiceImpl) controlPlaneSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	if s.captures == nil {
		return "", queryError(ErrUnavailable, "run capture is unavailable", nil)
	}
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return s.runCaptureDiff(ctx, runcapture.Query{
			WorkspaceKey: wsID, OwnerKind: runcapture.OwnerExecution,
			OwnerID: run.TaskRunID, WorkItemID: run.WorkItemID,
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
		return "", queryError(ErrInvalidPersistedState, "run capture returned no diff projection", runcapture.ErrInvalidPersistedState)
	}
	switch evidence.Evidence.State {
	case runcapture.EvidenceFinalized, runcapture.EvidenceTruncated:
		return string(evidence.Content), nil
	case runcapture.EvidenceMissing:
		return "", queryError(ErrNotFound, "diff not found", nil)
	case runcapture.EvidencePending:
		return "", queryError(ErrUnavailable, "diff capture is still pending", nil)
	case runcapture.EvidenceCaptureFailed:
		return "", queryError(ErrUnavailable, "diff capture failed", nil)
	case runcapture.EvidenceContentUnavailable:
		return "", queryError(ErrUnavailable, "diff content is temporarily unavailable", nil)
	case runcapture.EvidenceCorrupt:
		return "", queryError(ErrInvalidPersistedState, "diff evidence is corrupt", runcapture.ErrEvidenceCorrupt)
	default:
		return "", queryError(ErrInvalidPersistedState, "run capture returned an invalid evidence state", runcapture.ErrInvalidPersistedState)
	}
}
