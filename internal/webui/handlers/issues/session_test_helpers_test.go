package issues

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

type Module interface {
	Register(*http.ServeMux)
}

type stubSessionService struct{}

func (*stubSessionService) ListTaskSessions(context.Context, string, string) ([]sessioncoord.SessionListItem, error) {
	return nil, nil
}

func (*stubSessionService) GetSession(context.Context, string, string, string) (*sessioncoord.SessionDetailData, error) {
	return &sessioncoord.SessionDetailData{}, nil
}

func (*stubSessionService) GetSessionTranscript(context.Context, string, string, string) ([]transcript.Event, error) {
	return nil, nil
}

func (*stubSessionService) ListSessionSubagents(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

func (*stubSessionService) GetSessionSubagentTranscript(context.Context, string, string, string, string) ([]transcript.Event, error) {
	return nil, nil
}

func (*stubSessionService) GetSessionDiff(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (*stubSessionService) ListSessionHistory(context.Context, string, string) ([]sessioncoord.SessionRecord, error) {
	return nil, nil
}

func (*stubSessionService) GetSessionScrollback(context.Context, string, string, string) (*sessioncoord.SessionScrollbackResult, error) {
	return &sessioncoord.SessionScrollbackResult{}, nil
}

type testSessionService struct {
	sessions *sessions.Store
	history  *localredis.SessionHistoryStore
}

func NewSessionService(sessionStore *sessions.Store, historyStore *localredis.SessionHistoryStore) sessioncoord.SessionService {
	return &testSessionService{sessions: sessionStore, history: historyStore}
}

func (s *testSessionService) ListTaskSessions(_ context.Context, _, taskID string) ([]sessioncoord.SessionListItem, error) {
	if s.sessions == nil {
		return nil, apperrors.ErrUnavailable("session store not available")
	}
	records, err := s.sessions.SessionsByTask(taskID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to list sessions", err)
	}
	items := make([]sessioncoord.SessionListItem, 0, len(records))
	for _, record := range records {
		items = append(items, sessioncoord.SessionListItem{SessionRecord: record, IsActive: record.Status == sessions.StatusRunning})
	}
	return items, nil
}

func (s *testSessionService) GetSession(_ context.Context, _, _, sessionID string) (*sessioncoord.SessionDetailData, error) {
	if s.sessions == nil {
		return nil, apperrors.ErrUnavailable("session store not available")
	}
	metadata, err := s.sessions.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, apperrors.ErrNotFound("session not found")
		}
		return nil, apperrors.ErrInternal("failed to load session", err)
	}
	return &sessioncoord.SessionDetailData{SessionMetadata: *metadata, IsActive: metadata.Status == sessions.StatusRunning}, nil
}

func (s *testSessionService) GetSessionTranscript(_ context.Context, _, _, sessionID string) ([]transcript.Event, error) {
	if s.sessions == nil {
		return nil, apperrors.ErrUnavailable("session store not available")
	}
	events, err := s.sessions.LoadNativeEvents(sessionID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to load transcript", err)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (*testSessionService) ListSessionSubagents(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}

func (*testSessionService) GetSessionSubagentTranscript(context.Context, string, string, string, string) ([]transcript.Event, error) {
	return nil, nil
}

func (s *testSessionService) GetSessionDiff(_ context.Context, _, _, sessionID string) (string, error) {
	if s.sessions == nil {
		return "", apperrors.ErrUnavailable("session store not available")
	}
	diff, err := s.sessions.ReadDiff(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", apperrors.ErrNotFound("diff not found")
		}
		return "", apperrors.ErrInternal("failed to read diff", err)
	}
	return diff, nil
}

func (s *testSessionService) ListSessionHistory(ctx context.Context, workspaceID, issueID string) ([]sessioncoord.SessionRecord, error) {
	if s.history == nil {
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessioncoord.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}
	records, err := s.history.List(ctx, workspaceID, issueID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to list session history", err)
	}
	return records, nil
}

func (s *testSessionService) GetSessionScrollback(ctx context.Context, workspaceID, issueID, recordID string) (*sessioncoord.SessionScrollbackResult, error) {
	if s.history == nil {
		return nil, apperrors.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessioncoord.ValidateSessionHistoryIssueID(issueID); err != nil {
		return nil, apperrors.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, apperrors.ErrValidation("record ID is required")
	}
	records, err := s.history.List(ctx, workspaceID, issueID)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to get session history", err)
	}
	var found *sessioncoord.SessionRecord
	for index := range records {
		if records[index].ID == recordID {
			found = &records[index]
			break
		}
	}
	if found == nil {
		return nil, apperrors.ErrNotFound("session record not found")
	}
	if found.ScrollbackPath == "" {
		return nil, apperrors.ErrNotFound("no scrollback available for this session")
	}
	home, _ := os.UserHomeDir()
	expectedPrefix := filepath.Clean(home+"/.loom/session-scrollback") + string(os.PathSeparator)
	path := filepath.Clean(found.ScrollbackPath)
	if !strings.HasPrefix(path+string(os.PathSeparator), expectedPrefix) {
		return nil, apperrors.ErrValidation("invalid scrollback path")
	}
	file, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, apperrors.ErrNotFound("scrollback file not found")
		}
		return nil, apperrors.ErrInternal("failed to read scrollback", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, apperrors.ErrInternal("failed to read scrollback", err)
	}
	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return &sessioncoord.SessionScrollbackResult{Content: text, Lines: lines}, nil
}
