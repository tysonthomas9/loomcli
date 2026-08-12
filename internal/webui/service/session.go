package service

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

// SessionService defines business logic for session audit trail operations.
// Handlers call this interface and map returned errors to HTTP responses.
type SessionService interface {
	// ListTaskSessions returns all sessions for a given task with computed fields.
	ListTaskSessions(ctx context.Context, wsID, taskID string) ([]SessionListItem, error)

	// GetSession returns metadata for a single session, enforcing task ownership.
	GetSession(ctx context.Context, wsID, taskID, sessionID string) (*SessionDetailData, error)

	// GetSessionTranscript returns the canonical backend-agnostic event stream
	// for a session, parsed from the captured native JSONL transcript. Enforces
	// task ownership.
	GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error)

	// GetSessionSubagentTranscript returns events for a single captured
	// subagent transcript within a session. Enforces task ownership.
	GetSessionSubagentTranscript(ctx context.Context, wsID, taskID, sessionID, subagentID string) ([]transcript.Event, error)

	// ListSessionSubagents returns the IDs of subagents captured for a session,
	// in the order PostToolUse recorded them (filename sort).
	ListSessionSubagents(ctx context.Context, wsID, taskID, sessionID string) ([]string, error)

	// GetSessionDiff returns the diff.patch content for a session as plain text.
	GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error)

	// ListSessionHistory returns session history records for an issue.
	ListSessionHistory(ctx context.Context, wsID, issueID string) ([]sessionhistory.SessionRecord, error)

	// GetSessionScrollback returns scrollback content for a completed session.
	GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*SessionScrollbackResult, error)
}

// SessionListItem extends a session record with computed UI fields.
type SessionListItem struct {
	sessions.SessionRecord
	IsActive      bool   `json:"is_active"`
	HasTranscript bool   `json:"has_transcript"`
	HasDiff       bool   `json:"has_diff"`
	GitHubPRURL   string `json:"github_pr_url,omitempty"`
}

// SessionDetailData extends session metadata with computed UI fields.
type SessionDetailData struct {
	sessions.SessionMetadata
	IsActive bool `json:"is_active"`
}

// SessionScrollbackResult contains scrollback file content.
type SessionScrollbackResult struct {
	Content string
	Lines   int
}
