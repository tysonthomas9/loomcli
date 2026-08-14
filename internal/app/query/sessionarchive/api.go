package sessionarchive

import (
	"context"
	"time"

	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// TranscriptEvents is the canonical transcript shape exposed by WebUI session
// read ports. Keeping the transport-facing alias at the service boundary
// prevents handlers from depending directly on transcript persistence.
type TranscriptEvents = []transcript.Event

// HistoryReader is the session-history projection needed by the UI.
type HistoryReader interface {
	List(ctx context.Context, workspaceID, issueID string) ([]interaction.SessionHistoryRecord, error)
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

	// GetSessionDiff returns the diff.patch content for a session as plain text.
	GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error)

	// ListSessionHistory returns session history records for an issue.
	ListSessionHistory(ctx context.Context, wsID, issueID string) ([]SessionHistoryItem, error)

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
	SessionRecordView
	IsActive                 bool   `json:"is_active"`
	HasTranscript            bool   `json:"has_transcript"`
	HasDiff                  bool   `json:"has_diff"`
	TranscriptEvidenceStatus string `json:"transcript_evidence_status"`
	TranscriptFailureClass   string `json:"transcript_failure_class,omitempty"`
	DiffEvidenceStatus       string `json:"diff_evidence_status"`
	DiffFailureClass         string `json:"diff_failure_class,omitempty"`
	RuntimeStrategy          string `json:"runtime_strategy,omitempty"`
	DeliveryMode             string `json:"delivery,omitempty"`
	PatchBackStatus          string `json:"patch_back_status,omitempty"`
	LogsRef                  string `json:"logs_ref,omitempty"`
	LocalBranch              string `json:"local_branch,omitempty"`
	HeadSHA                  string `json:"head_sha,omitempty"`
	GitHubBranch             string `json:"github_branch,omitempty"`
	GitHubPRURL              string `json:"github_pr_url,omitempty"`
}

// SessionDetailData extends session metadata with computed UI fields.
type SessionDetailData struct {
	SessionMetadataView
	IsActive bool `json:"is_active"`
}

type SessionStatus string

const (
	StatusRunning   SessionStatus = "running"
	StatusCompleted SessionStatus = "completed"
	StatusFailed    SessionStatus = "failed"
	StatusAborted   SessionStatus = "aborted"
)

// SessionRecordView is the HTTP projection of an Execution TaskRun or
// Interaction AgentSession. It is not a persistence record.
type SessionRecordView struct {
	SchemaVersion    int           `json:"schema_version,omitempty"`
	SessionID        string        `json:"session_id"`
	TaskID           string        `json:"task_id"`
	EpicID           string        `json:"epic_id,omitempty"`
	AgentName        string        `json:"agent_name"`
	Backend          string        `json:"backend"`
	Model            string        `json:"model,omitempty"`
	Phase            string        `json:"phase,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	EndedAt          *time.Time    `json:"ended_at,omitempty"`
	DurationS        float64       `json:"duration_s,omitempty"`
	Status           SessionStatus `json:"status"`
	ExitCode         int           `json:"exit_code"`
	InputTokens      int64         `json:"input_tokens"`
	OutputTokens     int64         `json:"output_tokens"`
	CacheReadTokens  int64         `json:"cache_read_tokens"`
	CacheWriteTokens int64         `json:"cache_write_tokens"`
	EstimatedCostUSD float64       `json:"estimated_cost_usd"`
	FilesChanged     int           `json:"files_changed"`
	LinesAdded       int           `json:"lines_added"`
	LinesRemoved     int           `json:"lines_removed"`
	FilesTouched     []string      `json:"files_touched,omitempty"`
	AttemptNum       int           `json:"attempt_num"`
	ErrorClass       string        `json:"error_class,omitempty"`
}

type SessionMetadataView struct {
	SessionRecordView
	LastError string `json:"last_error,omitempty"`
}

// SessionHistoryItem enriches Interaction's terminal audit record with the
// Artifacts-owned durable scrollback state. It is a read projection only;
// neither Interaction persistence nor the WebUI can mint evidence state.
type SessionHistoryItem struct {
	interaction.SessionHistoryRecord
	ScrollbackEvidenceStatus string `json:"scrollback_evidence_status"`
	ScrollbackFailureClass   string `json:"scrollback_failure_class,omitempty"`
}

// SessionScrollbackResult is the read projection for one completed session's
// captured terminal output.
type SessionScrollbackResult struct {
	Content string
	Lines   int
}
