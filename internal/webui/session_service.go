package webui

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// SessionService defines business logic for session audit trail operations.
// Handlers call this interface and map returned errors to HTTP responses.
type SessionService interface {
	// ListTaskSessions returns all sessions for a given task with computed fields.
	ListTaskSessions(ctx context.Context, taskID string) ([]SessionListItem, error)

	// GetSession returns metadata for a single session, enforcing task ownership.
	GetSession(ctx context.Context, taskID, sessionID string) (*SessionDetailData, error)

	// GetSessionTranscript returns transcript entries for a session, enforcing task ownership.
	GetSessionTranscript(ctx context.Context, taskID, sessionID string) ([]sessions.TranscriptEntry, error)

	// GetSessionDiff returns the diff.patch content for a session as plain text.
	GetSessionDiff(ctx context.Context, taskID, sessionID string) (string, error)

	// ListSessionHistory returns session history records for an issue.
	ListSessionHistory(ctx context.Context, wsID, issueID string) ([]sessionhistory.SessionRecord, error)

	// GetSessionScrollback returns scrollback content for a completed session.
	GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*SessionScrollbackResult, error)
}

// Compile-time check that sessionServiceImpl satisfies SessionService.
var _ SessionService = (*sessionServiceImpl)(nil)

// SessionScrollbackResult contains scrollback file content.
type SessionScrollbackResult struct {
	Content string
	Lines   int
}
