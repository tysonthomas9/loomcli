package entity

import (
	"fmt"
	"time"
)

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

// Session status constants.
const (
	SessionRunning   SessionStatus = "running"
	SessionCompleted SessionStatus = "completed"
	SessionFailed    SessionStatus = "failed"
	SessionAborted   SessionStatus = "aborted"
)

// IsValid checks if the session status value is valid.
// Empty string is valid (unset status).
func (s SessionStatus) IsValid() bool {
	switch s {
	case SessionRunning, SessionCompleted, SessionFailed, SessionAborted, "":
		return true
	}
	return false
}

// TokenUsage holds token consumption counts and estimated cost.
// Reusable across domain types — not Session-specific.
type TokenUsage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// IsZero returns true if all token counts and estimated cost are zero.
func (t TokenUsage) IsZero() bool {
	return t.InputTokens == 0 && t.OutputTokens == 0 &&
		t.CacheReadTokens == 0 && t.CacheWriteTokens == 0 &&
		t.EstimatedCostUSD == 0
}

// Total returns the sum of all token count fields.
func (t TokenUsage) Total() int64 {
	return t.InputTokens + t.OutputTokens + t.CacheReadTokens + t.CacheWriteTokens
}

// DiffStats summarizes the git diff for a session.
// Reusable across domain types — not Session-specific.
type DiffStats struct {
	FilesChanged int `json:"files_changed"`
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

// IsZero returns true if all counts are zero.
func (d DiffStats) IsZero() bool {
	return d.FilesChanged == 0 && d.LinesAdded == 0 && d.LinesRemoved == 0
}

// TotalLines returns the total churn (lines added + lines removed).
func (d DiffStats) TotalLines() int {
	return d.LinesAdded + d.LinesRemoved
}

// Session represents an agent session as a first-class domain entity.
// Fields are organized into logical groups for maintainability.
type Session struct {
	// ===== Core Identification =====
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	EpicID    string `json:"epic_id,omitempty"`

	// ===== Agent Context =====
	AgentName string `json:"agent_name"`
	Backend   string `json:"backend"`
	Model     string `json:"model,omitempty"`
	Phase     string `json:"phase,omitempty"`

	// ===== Timing =====
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	DurationS float64    `json:"duration_s,omitempty"`

	// ===== Outcome =====
	Status     SessionStatus `json:"status"`
	ExitCode   int           `json:"exit_code"`
	ErrorClass string        `json:"error_class,omitempty"`

	// ===== Token Usage =====
	TokenUsage TokenUsage `json:"token_usage"`

	// ===== Diff Statistics =====
	DiffStats DiffStats `json:"diff_stats"`

	// ===== Retry Context =====
	AttemptNum int `json:"attempt_num"`
}

// Validate checks if the session has valid field values.
func (s *Session) Validate() error {
	if s.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if s.AgentName == "" {
		return fmt.Errorf("agent_name is required")
	}
	if s.Backend == "" {
		return fmt.Errorf("backend is required")
	}
	if !s.Status.IsValid() {
		return fmt.Errorf("invalid session status: %s", s.Status)
	}
	if s.StartedAt.IsZero() {
		return fmt.Errorf("started_at is required")
	}
	if s.EndedAt != nil && s.EndedAt.Before(s.StartedAt) {
		return fmt.Errorf("ended_at must not be before started_at")
	}
	if s.DurationS < 0 {
		return fmt.Errorf("duration_s must not be negative")
	}
	if s.AttemptNum < 0 {
		return fmt.Errorf("attempt_num must not be negative")
	}
	if s.Phase != "" && s.Phase != "planning" && s.Phase != "implementation" {
		return fmt.Errorf("phase must be 'planning' or 'implementation' (got %q)", s.Phase)
	}
	return nil
}

// SetDefaults applies default values for unset fields.
func (s *Session) SetDefaults() {
	if s.Status == "" {
		s.Status = SessionRunning
	}
	if s.AttemptNum == 0 {
		s.AttemptNum = 1
	}
}

// IsActive returns true if the session status is running.
func (s *Session) IsActive() bool {
	return s.Status == SessionRunning
}

// IsTerminal returns true if the session is in a final state.
func (s *Session) IsTerminal() bool {
	switch s.Status {
	case SessionCompleted, SessionFailed, SessionAborted:
		return true
	}
	return false
}

// Succeeded returns true if the session completed successfully (status completed and exit code 0).
func (s *Session) Succeeded() bool {
	return s.Status == SessionCompleted && s.ExitCode == 0
}
