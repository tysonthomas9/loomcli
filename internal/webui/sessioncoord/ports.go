package sessioncoord

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// TranscriptEvents is the canonical transcript shape exposed by WebUI session
// read ports. Keeping the transport-facing alias at the service boundary
// prevents handlers from depending directly on transcript persistence.
type TranscriptEvents = []transcript.Event

// HistoryReader is the session-history projection needed by the UI.
type HistoryReader interface {
	List(ctx context.Context, workspaceID, issueID string) ([]SessionRecord, error)
}

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
	GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) (TranscriptEvents, error)

	// GetSessionSubagentTranscript returns events for a single captured
	// subagent transcript within a session. Enforces task ownership.
	GetSessionSubagentTranscript(ctx context.Context, wsID, taskID, sessionID, subagentID string) (TranscriptEvents, error)

	// ListSessionSubagents returns the IDs of subagents captured for a session,
	// in the order PostToolUse recorded them (filename sort).
	ListSessionSubagents(ctx context.Context, wsID, taskID, sessionID string) ([]string, error)

	// GetSessionDiff returns the diff.patch content for a session as plain text.
	GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error)

	// ListSessionHistory returns session history records for an issue.
	ListSessionHistory(ctx context.Context, wsID, issueID string) ([]SessionRecord, error)

	// GetSessionScrollback returns scrollback content for a completed session.
	GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*SessionScrollbackResult, error)
}

// AgentSessionTranscriptService is the narrow read port used by the unified
// agent history surface. Interactive sessions are not necessarily task-owned,
// so their transcript authorization is the {workspace, agent, session}
// relationship recorded by AgentSession.
type AgentSessionTranscriptService interface {
	GetAgentSessionTranscript(ctx context.Context, wsID, agentID, sessionID string) (TranscriptEvents, error)
}

// SessionListItem extends a session record with computed UI fields.
type SessionListItem struct {
	sessions.SessionRecord
	IsActive        bool   `json:"is_active"`
	HasTranscript   bool   `json:"has_transcript"`
	HasDiff         bool   `json:"has_diff"`
	RuntimeStrategy string `json:"runtime_strategy,omitempty"`
	DeliveryMode    string `json:"delivery,omitempty"`
	PatchBackStatus string `json:"patch_back_status,omitempty"`
	LogsRef         string `json:"logs_ref,omitempty"`
	LocalBranch     string `json:"local_branch,omitempty"`
	HeadSHA         string `json:"head_sha,omitempty"`
	GitHubBranch    string `json:"github_branch,omitempty"`
	GitHubPRURL     string `json:"github_pr_url,omitempty"`
}

// SessionDetailData extends session metadata with computed UI fields.
type SessionDetailData struct {
	sessions.SessionMetadata
	IsActive bool `json:"is_active"`
}

// SessionRecord keeps WebUI callers source-compatible with Interaction's
// canonical terminal audit projection.
type SessionRecord = interaction.SessionHistoryRecord

// SessionScrollbackResult is the read projection for one completed session's
// captured terminal output.
type SessionScrollbackResult struct {
	Content string
	Lines   int
}

// ValidateSessionHistoryIssueID keeps WebUI callers source-compatible with
// Interaction's canonical validation policy.
func ValidateSessionHistoryIssueID(id string) error {
	return interaction.ValidateSessionHistoryIssueID(id)
}
