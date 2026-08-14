package issues

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	"github.com/tysonthomas9/loomcli/internal/infra/localredis"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

type Module interface{ Register(*http.ServeMux) }

type stubSessionService struct{}

func (*stubSessionService) ListTaskSessions(context.Context, string, string) ([]sessionarchive.SessionListItem, error) {
	return nil, nil
}
func (*stubSessionService) GetSession(context.Context, string, string, string) (*sessionarchive.SessionDetailData, error) {
	return &sessionarchive.SessionDetailData{}, nil
}
func (*stubSessionService) GetSessionTranscript(context.Context, string, string, string) ([]transcript.Event, error) {
	return nil, nil
}
func (*stubSessionService) GetSessionDiff(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*stubSessionService) ListSessionHistory(context.Context, string, string) ([]sessionarchive.SessionHistoryItem, error) {
	return nil, nil
}
func (*stubSessionService) GetSessionScrollback(context.Context, string, string, string) (*sessionarchive.SessionScrollbackResult, error) {
	return &sessionarchive.SessionScrollbackResult{}, nil
}

type testSessionService struct {
	stubSessionService
	history *localredis.SessionHistoryStore
}

func NewSessionService(_ any, historyStore *localredis.SessionHistoryStore) sessionarchive.SessionService {
	return &testSessionService{history: historyStore}
}

func (s *testSessionService) ListSessionHistory(ctx context.Context, workspaceID, issueID string) ([]sessionarchive.SessionHistoryItem, error) {
	if s.history == nil {
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}
	records, err := s.history.List(ctx, workspaceID, issueID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to list session history", err)
	}
	items := make([]sessionarchive.SessionHistoryItem, 0, len(records))
	for _, record := range records {
		items = append(items, sessionarchive.SessionHistoryItem{
			SessionHistoryRecord: record, ScrollbackEvidenceStatus: "content_unavailable",
		})
	}
	return items, nil
}

func (s *testSessionService) GetSessionScrollback(ctx context.Context, workspaceID, issueID, recordID string) (*sessionarchive.SessionScrollbackResult, error) {
	if s.history == nil {
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := interaction.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, apperrors.ErrValidation("record ID is required")
	}
	records, err := s.history.List(ctx, workspaceID, issueID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to get session history", err)
	}
	var found *interaction.SessionHistoryRecord
	for index := range records {
		if records[index].ID == recordID {
			found = &records[index]
			break
		}
	}
	if found == nil {
		return nil, apperrors.ErrNotFound("session record not found")
	}
	return nil, apperrors.ErrNotFound("no scrollback available for this session")
}
