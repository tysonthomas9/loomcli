package sessionarchive

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

func (s *sessionServiceImpl) ListSessionHistory(ctx context.Context, wsID, issueID string) ([]SessionHistoryItem, error) {
	if s.histStore == nil {
		return nil, queryError(ErrUnavailable, "session history not available", nil)
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, queryError(ErrInvalid, err.Error(), nil)
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to list session history", "issue_id", issueID, "err", err)
		return nil, queryError(ErrInvalidPersistedState, "failed to list session history", err)
	}
	items := make([]SessionHistoryItem, 0, len(records))
	for _, record := range records {
		item := SessionHistoryItem{
			SessionHistoryRecord: record, ScrollbackEvidenceStatus: string(runcapture.EvidenceMissing),
		}
		if s.captures == nil {
			item.ScrollbackEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
			items = append(items, item)
			continue
		}
		capture, captureErr := s.captures.Get(ctx, runcapture.Query{
			WorkspaceKey: wsID, OwnerKind: runcapture.OwnerInteraction,
			OwnerID: record.ID, WorkItemID: issueID,
		})
		if captureErr != nil {
			if !errors.Is(captureErr, runcapture.ErrNotFound) {
				item.ScrollbackEvidenceStatus = string(runcapture.EvidenceContentUnavailable)
			}
			items = append(items, item)
			continue
		}
		for _, evidence := range capture.Evidence {
			if evidence.Kind == artifacts.EvidenceScrollback {
				item.ScrollbackEvidenceStatus = string(evidence.State)
				item.ScrollbackFailureClass = evidence.FailureClass
				break
			}
		}
		items = append(items, item)
	}
	return items, nil
}

//nolint:funlen // Authorization and every durable evidence state stay explicit at this read boundary.
func (s *sessionServiceImpl) GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*SessionScrollbackResult, error) {
	if s.histStore == nil {
		return nil, queryError(ErrUnavailable, "session history not available", nil)
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, queryError(ErrInvalid, err.Error(), nil)
	}
	if recordID == "" {
		return nil, queryError(ErrInvalid, "record ID is required", nil)
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to get session history for scrollback", "issue_id", issueID, "err", err)
		return nil, queryError(ErrInvalidPersistedState, "failed to get session history", err)
	}

	found := findSessionRecord(records, recordID)
	if found == nil {
		return nil, queryError(ErrNotFound, "session record not found", nil)
	}

	if s.captures == nil {
		return nil, queryError(ErrUnavailable, "run capture archive is unavailable", nil)
	}
	evidence, err := s.captures.ReadEvidence(ctx, runcapture.Query{
		WorkspaceKey: wsID,
		OwnerKind:    runcapture.OwnerInteraction,
		OwnerID:      found.ID,
		WorkItemID:   issueID,
	}, artifacts.EvidenceScrollback)
	if errors.Is(err, runcapture.ErrNotFound) {
		return nil, queryError(ErrNotFound, "no scrollback available for this session", err)
	}
	if errors.Is(err, runcapture.ErrUnavailable) {
		return nil, queryError(ErrUnavailable, "run capture archive is unavailable", err)
	}
	if err != nil {
		return nil, queryError(ErrInvalidPersistedState, "failed to read durable scrollback", err)
	}
	if evidence == nil || evidence.Evidence.State == runcapture.EvidenceMissing {
		return nil, queryError(ErrNotFound, "no scrollback available for this session", nil)
	}
	switch evidence.Evidence.State {
	case runcapture.EvidenceFinalized, runcapture.EvidenceTruncated:
		text := string(evidence.Content)
		lines := 0
		if text != "" {
			lines = strings.Count(text, "\n") + 1
		}
		return &SessionScrollbackResult{Content: text, Lines: lines}, nil
	case runcapture.EvidencePending:
		return nil, queryError(ErrUnavailable, "scrollback capture is not finalized", nil)
	case runcapture.EvidenceCaptureFailed:
		return nil, queryError(ErrUnavailable, "scrollback capture failed", nil)
	case runcapture.EvidenceContentUnavailable:
		return nil, queryError(ErrUnavailable, "scrollback content is unavailable", nil)
	default:
		return nil, queryError(ErrInvalidPersistedState, "scrollback evidence is corrupt", runcapture.ErrEvidenceCorrupt)
	}
}

// findSessionRecord returns the record with the given ID, or nil if not found.
func findSessionRecord(records []interaction.SessionHistoryRecord, id string) *interaction.SessionHistoryRecord {
	for i := range records {
		if records[i].ID == id {
			return &records[i]
		}
	}
	return nil
}
