package webui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// sessionServiceImpl is the concrete implementation of SessionService.
type sessionServiceImpl struct {
	sessStore *sessions.Store
	histStore *sessionhistory.Store
}

// NewSessionService creates a new SessionService implementation.
func NewSessionService(sessStore *sessions.Store, histStore *sessionhistory.Store) SessionService {
	return &sessionServiceImpl{sessStore: sessStore, histStore: histStore}
}

func (s *sessionServiceImpl) ListTaskSessions(_ context.Context, taskID string) ([]SessionListItem, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}

	records, err := s.sessStore.SessionsByTask(taskID)
	if err != nil {
		logger.Error("failed to list sessions", "task_id", taskID, "err", err)
		return nil, service.ErrInternal("failed to list sessions", err)
	}

	items := make([]SessionListItem, 0, len(records))
	for _, rec := range records {
		item := SessionListItem{
			SessionRecord: rec,
			IsActive:      rec.Status == sessions.StatusRunning,
		}
		if entries, err := s.sessStore.LoadTranscript(rec.SessionID); err == nil && len(entries) > 0 {
			item.HasTranscript = true
		}
		if diff, err := s.sessStore.ReadDiff(rec.SessionID); err == nil && diff != "" {
			item.HasDiff = true
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *sessionServiceImpl) GetSession(_ context.Context, taskID, sessionID string) (*SessionDetailData, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}

	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
		}
		logger.Error("failed to load session", "session_id", sessionID, "err", err)
		return nil, service.ErrInternal("failed to load session", err)
	}

	// Enforce task ownership — session must belong to the requested task.
	if meta.TaskID != taskID {
		return nil, service.ErrNotFound("session not found")
	}

	return &SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}, nil
}

func (s *sessionServiceImpl) GetSessionTranscript(_ context.Context, taskID, sessionID string) ([]sessions.TranscriptEntry, error) {
	if s.sessStore == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}

	// Enforce task ownership
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
		}
		logger.Error("failed to load session metadata", "session_id", sessionID, "err", err)
		return nil, service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return nil, service.ErrNotFound("session not found")
	}

	entries, loadErr := s.sessStore.LoadTranscript(sessionID)
	if loadErr != nil {
		logger.Error("failed to load transcript", "session_id", sessionID, "err", loadErr)
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}

	// Ensure empty array instead of null in JSON output.
	if entries == nil {
		entries = []sessions.TranscriptEntry{}
	}
	return entries, nil
}

func (s *sessionServiceImpl) GetSessionDiff(_ context.Context, taskID, sessionID string) (string, error) {
	if s.sessStore == nil {
		return "", service.ErrUnavailable("session store not available")
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return "", service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", service.ErrValidation("invalid session ID")
	}

	// Enforce task ownership
	meta, err := s.sessStore.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", service.ErrNotFound("session not found")
		}
		logger.Error("failed to load session metadata", "session_id", sessionID, "err", err)
		return "", service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return "", service.ErrNotFound("session not found")
	}

	diff, diffErr := s.sessStore.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			return "", service.ErrNotFound("diff not found")
		}
		logger.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
		return "", service.ErrInternal("failed to read diff", diffErr)
	}
	return diff, nil
}

func (s *sessionServiceImpl) ListSessionHistory(ctx context.Context, wsID, issueID string) ([]sessionhistory.SessionRecord, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to list session history", "issue_id", issueID, "err", err)
		return nil, service.ErrInternal("failed to list session history", err)
	}
	return records, nil
}

func (s *sessionServiceImpl) GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*SessionScrollbackResult, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, service.ErrValidation("record ID is required")
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to get session history for scrollback", "issue_id", issueID, "err", err)
		return nil, service.ErrInternal("failed to get session history", err)
	}

	var found *sessionhistory.SessionRecord
	for i := range records {
		if records[i].ID == recordID {
			found = &records[i]
			break
		}
	}
	if found == nil {
		return nil, service.ErrNotFound("session record not found")
	}

	if found.ScrollbackPath == "" {
		return nil, service.ErrNotFound("no scrollback available for this session")
	}

	// Validate scrollback path security
	homeDir, _ := os.UserHomeDir()
	expectedPrefix := filepath.Clean(homeDir+"/.loom/session-scrollback") + string(os.PathSeparator)
	cleanPath := filepath.Clean(found.ScrollbackPath)
	if !strings.HasPrefix(cleanPath+string(os.PathSeparator), expectedPrefix) {
		return nil, service.ErrValidation("invalid scrollback path")
	}

	f, err := os.Open(cleanPath) //nolint:gosec // path cleaned and prefix-validated above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("scrollback file not found")
		}
		logger.Error("failed to open scrollback file", "path", found.ScrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		logger.Error("failed to read scrollback file", "path", found.ScrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}

	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}

	return &SessionScrollbackResult{Content: text, Lines: lines}, nil
}
