package svcimpl

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

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

func (s *sessionServiceImpl) GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*service.SessionScrollbackResult, error) {
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

	found := findSessionRecord(records, recordID)
	if found == nil {
		return nil, service.ErrNotFound("session record not found")
	}

	if found.ScrollbackPath == "" {
		return nil, service.ErrNotFound("no scrollback available for this session")
	}

	return readScrollbackFile(found.ScrollbackPath)
}

// findSessionRecord returns the record with the given ID, or nil if not found.
func findSessionRecord(records []sessionhistory.SessionRecord, id string) *sessionhistory.SessionRecord {
	for i := range records {
		if records[i].ID == id {
			return &records[i]
		}
	}
	return nil
}

// readScrollbackFile validates the path, reads the file, and returns the result.
func readScrollbackFile(scrollbackPath string) (*service.SessionScrollbackResult, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return nil, service.ErrInternal("resolve home directory", err)
	}
	if strings.TrimSpace(homeDir) == "" {
		return nil, service.ErrInternal("resolve home directory", errors.New("empty home directory"))
	}
	expectedPrefix := filepath.Clean(homeDir+"/.loom/session-scrollback") + string(os.PathSeparator)
	cleanPath := filepath.Clean(scrollbackPath)
	if !strings.HasPrefix(cleanPath+string(os.PathSeparator), expectedPrefix) {
		return nil, service.ErrValidation("invalid scrollback path")
	}

	f, err := os.Open(cleanPath) //nolint:gosec // path cleaned and prefix-validated above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("scrollback file not found")
		}
		logger.Error("failed to open scrollback file", "path", scrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		logger.Error("failed to read scrollback file", "path", scrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}

	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return &service.SessionScrollbackResult{Content: text, Lines: lines}, nil
}
