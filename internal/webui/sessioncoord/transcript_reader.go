package sessioncoord

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func (s *sessionServiceImpl) controlPlaneSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	if s.captures == nil {
		return nil, apperrors.ErrUnavailable("run capture is unavailable")
	}
	return s.runCaptureTaskTranscript(ctx, wsID, taskID, sessionID)
}

func (s *sessionServiceImpl) GetAgentSessionTranscript(
	ctx context.Context,
	wsID, agentID, sessionID string,
) ([]transcript.Event, error) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" || sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid agent or session ID")
	}
	return s.runCaptureTranscript(ctx, runcapture.Query{
		WorkspaceKey: wsID, OwnerKind: runcapture.OwnerInteraction,
		OwnerID: sessionID, AgentID: agentID,
	})
}

func (s *sessionServiceImpl) runCaptureTaskTranscript(
	ctx context.Context,
	workspace, workItemID, sessionID string,
) ([]transcript.Event, error) {
	candidates := []string{strings.TrimSpace(sessionID)}
	if strings.HasPrefix(sessionID, domain.FlueTaskSessionIDPrefix) {
		if value := strings.TrimSpace(strings.TrimPrefix(sessionID, domain.FlueTaskSessionIDPrefix)); value != "" {
			candidates = append(candidates, value)
		}
	}
	for _, ownerID := range candidates {
		events, err := s.runCaptureTranscript(ctx, runcapture.Query{
			WorkspaceKey: workspace, OwnerKind: runcapture.OwnerExecution,
			OwnerID: ownerID, WorkItemID: workItemID,
		})
		if err == nil {
			return events, nil
		}
		if !serviceErrorNotFound(err) {
			return nil, err
		}
	}
	return s.runCaptureTranscript(ctx, runcapture.Query{
		WorkspaceKey: workspace, OwnerKind: runcapture.OwnerInteraction,
		OwnerID: sessionID, WorkItemID: workItemID,
	})
}

func (s *sessionServiceImpl) runCaptureTranscript(ctx context.Context, query runcapture.Query) ([]transcript.Event, error) {
	if s.captures == nil {
		return nil, apperrors.ErrUnavailable("run capture is unavailable")
	}
	evidence, err := s.captures.Transcript(ctx, query)
	if err != nil {
		return nil, runCaptureReadError(err)
	}
	if evidence == nil {
		return nil, apperrors.ErrInternal("run capture returned no transcript projection", runcapture.ErrInvalidPersistedState)
	}
	switch evidence.Evidence.State {
	case runcapture.EvidenceFinalized, runcapture.EvidenceTruncated:
		return append([]transcript.Event(nil), evidence.Events...), nil
	case runcapture.EvidenceMissing:
		return nil, apperrors.ErrNotFound("transcript not found")
	case runcapture.EvidencePending:
		return nil, apperrors.ErrUnavailable("transcript capture is still pending")
	case runcapture.EvidenceCaptureFailed:
		return nil, apperrors.ErrUnavailable("transcript capture failed")
	case runcapture.EvidenceContentUnavailable:
		return nil, apperrors.ErrUnavailable("transcript content is temporarily unavailable")
	case runcapture.EvidenceCorrupt:
		return nil, apperrors.ErrInternal("transcript evidence is corrupt", runcapture.ErrEvidenceCorrupt)
	default:
		return nil, apperrors.ErrInternal("run capture returned an invalid evidence state", runcapture.ErrInvalidPersistedState)
	}
}

func runCaptureReadError(err error) error {
	switch {
	case errors.Is(err, runcapture.ErrInvalid):
		return apperrors.ErrValidation("invalid run capture query")
	case errors.Is(err, runcapture.ErrNotFound):
		return apperrors.ErrNotFound("session not found")
	case errors.Is(err, runcapture.ErrUnavailable):
		return apperrors.ErrUnavailable("run capture is temporarily unavailable")
	default:
		return apperrors.ErrInternal("failed to load run capture", err)
	}
}
