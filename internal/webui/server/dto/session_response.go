package dto

import "time"

// SessionResponse is the typed API response for a single session.
// Uses string (not entity/sessions types) for Status/Phase to decouple the
// wire format from domain types.
// Used for both list and detail views — handlers populate the fields they have.
type SessionResponse struct {
	// Identity
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
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
	Status   string `json:"status"`    // "running", "completed", "failed", "aborted"
	ExitCode int    `json:"exit_code"` // No omitempty: 0 is valid (success)

	// Token usage (flat) — no omitempty: zero values are meaningful
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`

	// Diff stats (flat) — no omitempty: zero means "no changes"
	FilesChanged int      `json:"files_changed"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	FilesTouched []string `json:"files_touched,omitempty"` // Truly optional

	// Retry context
	AttemptNum int    `json:"attempt_num"` // No omitempty: 0 is valid
	ErrorClass string `json:"error_class,omitempty"`

	// Computed fields (populated by handlers)
	IsActive      bool   `json:"is_active"`
	HasTranscript bool   `json:"has_transcript"`
	HasDiff       bool   `json:"has_diff"`
	GitHubPRURL   string `json:"github_pr_url,omitempty"`

	// Detail-only field — omitted in list view
	LastError string `json:"last_error,omitempty"`
}

// TranscriptEntry is the API representation of a single transcript entry.
type TranscriptEntry struct {
	Seq       int       `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Role      string    `json:"role"` // "user", "assistant", "system", "tool"
	Type      string    `json:"type"` // "text", "tool_use", "tool_result"
	Content   string    `json:"content,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
	Raw       string    `json:"raw,omitempty"`
}
