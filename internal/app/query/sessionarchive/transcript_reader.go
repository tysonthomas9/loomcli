package sessionarchive

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func (s *sessionServiceImpl) controlPlaneSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	if s.captures == nil {
		return nil, queryError(ErrUnavailable, "run capture is unavailable", nil)
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
		return nil, queryError(ErrInvalid, "invalid agent or session ID", nil)
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
	if strings.HasPrefix(sessionID, execution.FlueTaskSessionIDPrefix) {
		if value := strings.TrimSpace(strings.TrimPrefix(sessionID, execution.FlueTaskSessionIDPrefix)); value != "" {
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
		return nil, queryError(ErrUnavailable, "run capture is unavailable", nil)
	}
	evidence, err := s.captures.Transcript(ctx, query)
	if err != nil {
		return nil, runCaptureReadError(err)
	}
	if evidence == nil {
		return nil, queryError(ErrInvalidPersistedState, "run capture returned no transcript projection", runcapture.ErrInvalidPersistedState)
	}
	switch evidence.Evidence.State {
	case runcapture.EvidenceFinalized, runcapture.EvidenceTruncated:
		return append([]transcript.Event(nil), evidence.Events...), nil
	case runcapture.EvidenceMissing:
		return nil, queryError(ErrNotFound, "transcript not found", nil)
	case runcapture.EvidencePending:
		return nil, queryError(ErrUnavailable, "transcript capture is still pending", nil)
	case runcapture.EvidenceCaptureFailed:
		return nil, queryError(ErrUnavailable, "transcript capture failed", nil)
	case runcapture.EvidenceContentUnavailable:
		return nil, queryError(ErrUnavailable, "transcript content is temporarily unavailable", nil)
	case runcapture.EvidenceCorrupt:
		return nil, queryError(ErrInvalidPersistedState, "transcript evidence is corrupt", runcapture.ErrEvidenceCorrupt)
	default:
		return nil, queryError(ErrInvalidPersistedState, "run capture returned an invalid evidence state", runcapture.ErrInvalidPersistedState)
	}
}

func runCaptureReadError(err error) error {
	switch {
	case errors.Is(err, runcapture.ErrInvalid):
		return queryError(ErrInvalid, "invalid run capture query", err)
	case errors.Is(err, runcapture.ErrNotFound):
		return queryError(ErrNotFound, "session not found", err)
	case errors.Is(err, runcapture.ErrUnavailable):
		return queryError(ErrUnavailable, "run capture is temporarily unavailable", err)
	default:
		return queryError(ErrInvalidPersistedState, "failed to load run capture", err)
	}
}
