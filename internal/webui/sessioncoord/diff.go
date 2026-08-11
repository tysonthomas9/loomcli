package sessioncoord

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
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
	// As with detail and transcript, prefer the canonical TaskRun artifact over
	// any local interaction session that happens to reuse the route ID.
	if run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID); runErr == nil {
		return s.executionTaskRunDiff(ctx, wsID, run)
	} else if !serviceErrorNotFound(runErr) {
		return "", runErr
	}
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		if !sessionStoreAllowsControlPlaneFallback(err) {
			return "", err
		}
		return s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
	}

	diff, diffErr := store.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			cpDiff, cpErr := s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
			if cpErr == nil {
				return cpDiff, nil
			}
			if serviceErrorNotFound(cpErr) {
				return "", apperrors.ErrNotFound("diff not found")
			}
			return "", cpErr
		}
		logger.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
		return "", apperrors.ErrInternal("failed to read diff", diffErr)
	}
	if diff == "" {
		if cpDiff, cpErr := s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID); cpErr == nil {
			return cpDiff, nil
		}
	}
	return diff, nil
}

func (s *sessionServiceImpl) controlPlaneSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return s.executionTaskRunDiff(ctx, wsID, run)
	}
	if !serviceErrorNotFound(runErr) {
		return "", runErr
	}
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return "", err
	}
	artifactID := ""
	if rec.Metadata != nil {
		artifactID = controlPlaneDiffArtifactRef(rec.Metadata)
	}
	if artifactID == "" && rec.Metadata != nil {
		artifactID, err = s.diffArtifactIDForTaskRun(ctx, wsID, rec.Metadata["task_run_id"])
		if err != nil {
			return "", err
		}
	}
	if artifactID == "" {
		return "", apperrors.ErrNotFound("diff not found")
	}
	data, err := s.readOwnedTaskRunArtifact(ctx, wsID, rec, artifactID, "patch")
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", apperrors.ErrNotFound("diff not found")
		}
		if errors.Is(err, artifactsmodule.ErrContentUnavailable) {
			return "", apperrors.ErrUnavailable("diff content is temporarily unavailable")
		}
		return "", sessionControlPlaneReadError(
			"failed to read diff",
			err,
		)
	}
	return string(data), nil
}

func controlPlaneDiffArtifactRef(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	return normalizeArtifactRef(firstNonEmptySessionValue(
		metadata["patch_artifact_id"],
		metadata["diff_artifact_id"],
		metadata["patch_ref"],
		metadata["diff_ref"],
	))
}

func normalizeArtifactRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "artifact://") {
		ref = strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
	}
	return ref
}

func (s *sessionServiceImpl) diffArtifactIDForTaskRun(ctx context.Context, wsID, taskRunID string) (string, error) {
	taskRunID = strings.TrimSpace(taskRunID)
	if taskRunID == "" || s.artifacts == nil {
		return "", nil
	}
	artifactValues, err := s.artifacts.ListArtifacts(ctx, artifactsmodule.SearchQuery{
		WorkspaceKey: wsID,
		Filter: artifactsmodule.SearchFilter{
			OwnerType: artifactsmodule.OwnerTaskRun, OwnerID: taskRunID,
			Type: "patch", DurableStatus: artifactsmodule.StatusFinalized, Limit: 1,
		},
	})
	if err != nil {
		return "", sessionControlPlaneReadError(
			"failed to list patch artifacts",
			err,
		)
	}
	for _, artifact := range artifactValues {
		if artifact != nil && strings.TrimSpace(artifact.ArtifactID) != "" {
			return artifact.ArtifactID, nil
		}
	}
	return "", nil
}
