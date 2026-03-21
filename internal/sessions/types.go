package sessions

import "time"

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

const (
	StatusRunning   SessionStatus = "running"
	StatusCompleted SessionStatus = "completed"
	StatusFailed    SessionStatus = "failed"
	StatusAborted   SessionStatus = "aborted"
)

// SessionRecord is the index entry in sessions/index.jsonl.
// Contains all queryable fields. One record per agent run.
type SessionRecord struct {
	// Identity
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"` // populated at Finalize, not CreateSession (agent claims task mid-session)
	EpicID    string `json:"epic_id,omitempty"`

	// Agent context
	AgentName string `json:"agent_name"`
	Backend   string `json:"backend"`
	Model     string `json:"model,omitempty"`
	Phase     string `json:"phase,omitempty"` // "planning" or "implementation"

	// Timing
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	DurationS float64    `json:"duration_s,omitempty"`

	// Outcome
	Status   SessionStatus `json:"status"`
	ExitCode int           `json:"exit_code"`

	// Token usage
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`

	// Diff stats
	FilesChanged int      `json:"files_changed"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	FilesTouched []string `json:"files_touched,omitempty"`

	// Retry context
	AttemptNum int    `json:"attempt_num"`
	ErrorClass string `json:"error_class,omitempty"`
}

// SessionMetadata is the mutable state in sessions/<id>/metadata.json.
// Written by parent process at create (status=running) and finalize.
type SessionMetadata struct {
	SessionRecord
	LastError string `json:"last_error,omitempty"`
}

// TranscriptEntry is one line in sessions/<id>/transcript.jsonl.
// Written by hook handlers via flocked append.
type TranscriptEntry struct {
	Seq       int       `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Role      string    `json:"role"`                // "user", "assistant", "tool"
	Type      string    `json:"type"`                // "text", "tool_use", "tool_result"
	Content   string    `json:"content,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
	Raw       string    `json:"raw,omitempty"` // original backend event
}

// CreateOptions holds parameters for creating a new session.
type CreateOptions struct {
	AgentName  string `json:"agent_name"`
	Backend    string `json:"backend"`
	EpicID     string `json:"epic_id,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	AttemptNum int    `json:"attempt_num"`
}

// FinalizeOptions holds parameters for closing a session.
type FinalizeOptions struct {
	TaskID       string    `json:"task_id"`
	ExitCode     int       `json:"exit_code"`
	DiffStats    DiffStats `json:"diff_stats"`
	FilesTouched []string  `json:"files_touched,omitempty"`
}

// DiffStats summarizes the git diff for a session.
type DiffStats struct {
	FilesChanged int `json:"files_changed"`
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

// Filter specifies query criteria for listing sessions.
type Filter struct {
	TaskID    string        `json:"task_id,omitempty"`
	EpicID    string        `json:"epic_id,omitempty"`
	AgentName string        `json:"agent_name,omitempty"`
	Backend   string        `json:"backend,omitempty"`
	Status    SessionStatus `json:"status,omitempty"`
	Since     time.Time     `json:"since,omitempty"`
	Until     time.Time     `json:"until,omitempty"`
}
