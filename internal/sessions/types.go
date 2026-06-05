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

// CurrentSchemaVersion is the version that new sessions are written with.
// Existing sessions without a version are implicitly version 0.
const CurrentSchemaVersion = 1

// SessionRecord is the index entry in sessions/index.jsonl.
// Contains all queryable fields. One record per agent run.
type SessionRecord struct {
	// Schema
	SchemaVersion int `json:"schema_version,omitempty"`

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

	// Runtime captures non-local execution metadata. nil for ordinary local
	// worktree runs; set when the agent ran in a remote sandbox (e.g. a Flue
	// Daytona-per-task run whose changes were patch-synced back).
	Runtime *RuntimeMetadata `json:"runtime,omitempty"`
}

// RuntimeMetadata records where and how an agent run executed when it differs
// from loom's default local-worktree model. See
// docs/design/flue-daytona-runtime-proposal.md (Phase 2, "Session metadata").
type RuntimeMetadata struct {
	Provider     string `json:"provider,omitempty"`      // sandbox provider, e.g. "daytona"
	SandboxID    string `json:"sandbox_id,omitempty"`    // remote sandbox identifier
	RemoteCwd    string `json:"remote_cwd,omitempty"`    // working dir inside the sandbox
	BaseRef      string `json:"base_ref,omitempty"`      // commit the sandbox was hydrated to
	SyncStrategy string `json:"sync_strategy,omitempty"` // "patch-back" | "branch-push" | "none"
	Cleanup      string `json:"cleanup,omitempty"`       // "deleted" (success) | "retained" (failure/leak)
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
	Role      string    `json:"role"` // "user", "assistant", "tool"
	Type      string    `json:"type"` // "text", "tool_use", "tool_result"
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
	Phase      string `json:"phase,omitempty"` // "planning" or "implementation"
	Prompt     string `json:"prompt,omitempty"`
	AttemptNum int    `json:"attempt_num"`
}

// FinalizeOptions holds parameters for closing a session.
type FinalizeOptions struct {
	TaskID       string    `json:"task_id"`
	ExitCode     int       `json:"exit_code"`
	DiffStats    DiffStats `json:"diff_stats"`
	FilesTouched []string  `json:"files_touched,omitempty"`
	DiffPatch    string    `json:"diff_patch,omitempty"` // raw diff to write to diff.patch

	// Token usage (optional — populated from usage.Collector if available)
	InputTokens      int64   `json:"input_tokens,omitempty"`
	OutputTokens     int64   `json:"output_tokens,omitempty"`
	CacheReadTokens  int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64   `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`

	// Error context
	ErrorClass string `json:"error_class,omitempty"`

	// Runtime is optional non-local execution metadata (e.g. Daytona sandbox).
	Runtime *RuntimeMetadata `json:"runtime,omitempty"`
}

// DiffStats summarizes the git diff for a session.
type DiffStats struct {
	FilesChanged int `json:"files_changed"`
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

// NormalizeAfterLoad applies schema migrations to a SessionMetadata loaded
// from disk. Call this after json.Unmarshal to ensure in-memory data is
// up-to-date with the current schema version.
func (m *SessionMetadata) NormalizeAfterLoad() {
	normalizeRecord(&m.SessionRecord)
}

// normalizeRecord migrates a SessionRecord to the current schema version.
// It is idempotent and does not downgrade future-version records.
func normalizeRecord(rec *SessionRecord) {
	if rec.SchemaVersion >= CurrentSchemaVersion {
		return // already current or future version
	}
	// Future migrations go here as version-gated blocks:
	// if rec.SchemaVersion < 2 { ... migrate v1→v2 ... }

	// Stamp current version after all migrations.
	rec.SchemaVersion = CurrentSchemaVersion
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
