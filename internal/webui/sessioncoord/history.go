package sessioncoord

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func (s *sessionServiceImpl) ListSessionHistory(ctx context.Context, wsID, issueID string) ([]SessionHistoryItem, error) {
	if s.histStore == nil {
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to list session history", "issue_id", issueID, "err", err)
		return nil, apperrors.ErrInternal("failed to list session history", err)
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
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, apperrors.ErrValidation("record ID is required")
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to get session history for scrollback", "issue_id", issueID, "err", err)
		return nil, apperrors.ErrInternal("failed to get session history", err)
	}

	found := findSessionRecord(records, recordID)
	if found == nil {
		return nil, apperrors.ErrNotFound("session record not found")
	}

	if s.captures == nil {
		return nil, apperrors.ErrUnavailable("run capture archive is unavailable")
	}
	evidence, err := s.captures.ReadEvidence(ctx, runcapture.Query{
		WorkspaceKey: wsID,
		OwnerKind:    runcapture.OwnerInteraction,
		OwnerID:      found.ID,
		WorkItemID:   issueID,
	}, artifacts.EvidenceScrollback)
	if errors.Is(err, runcapture.ErrNotFound) {
		return nil, apperrors.ErrNotFound("no scrollback available for this session")
	}
	if errors.Is(err, runcapture.ErrUnavailable) {
		return nil, apperrors.ErrUnavailable("run capture archive is unavailable")
	}
	if err != nil {
		return nil, apperrors.ErrInternal("failed to read durable scrollback", err)
	}
	if evidence == nil || evidence.Evidence.State == runcapture.EvidenceMissing {
		return nil, apperrors.ErrNotFound("no scrollback available for this session")
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
		return nil, apperrors.ErrUnavailable("scrollback capture is not finalized")
	case runcapture.EvidenceCaptureFailed:
		return nil, apperrors.ErrUnavailable("scrollback capture failed")
	case runcapture.EvidenceContentUnavailable:
		return nil, apperrors.ErrUnavailable("scrollback content is unavailable")
	default:
		return nil, apperrors.ErrInternal("scrollback evidence is corrupt", runcapture.ErrEvidenceCorrupt)
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
