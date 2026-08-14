package dto

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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

	// Token usage is null when no source reported usage. A numeric zero is only
	// emitted when usage was reported and the field itself was zero.
	InputTokens      *int64   `json:"input_tokens"`
	OutputTokens     *int64   `json:"output_tokens"`
	CacheReadTokens  *int64   `json:"cache_read_tokens"`
	CacheWriteTokens *int64   `json:"cache_write_tokens"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`

	// Diff stats (flat) — no omitempty: zero means "no changes"
	FilesChanged int      `json:"files_changed"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	FilesTouched []string `json:"files_touched,omitempty"` // Truly optional

	// Retry context
	AttemptNum int    `json:"attempt_num"` // No omitempty: 0 is valid
	ErrorClass string `json:"error_class,omitempty"`

	// Computed fields (populated by handlers)
	IsActive      bool                    `json:"is_active"`
	HasTranscript bool                    `json:"has_transcript"`
	HasDiff       bool                    `json:"has_diff"`
	Evidence      service.SessionEvidence `json:"evidence"`

	// Detail-only field — omitted in list view
	LastError string `json:"last_error,omitempty"`
}

// SessionResponseFromListItem maps the persistence/service model to its
// explicit wire contract, including unavailable usage as null.
func SessionResponseFromListItem(item service.SessionListItem) SessionResponse {
	return sessionResponse(item.SessionRecord, item.IsActive, item.HasTranscript, item.HasDiff, "", item.Evidence)
}

// SessionResponseFromDetail maps a detail result to the shared session wire contract.
func SessionResponseFromDetail(detail service.SessionDetailData) SessionResponse {
	return sessionResponse(detail.SessionRecord, detail.IsActive, detail.HasTranscript, detail.HasDiff, detail.LastError, detail.Evidence)
}

func sessionResponse(rec sessions.SessionRecord, isActive, hasTranscript, hasDiff bool, lastError string, evidence service.SessionEvidence) SessionResponse {
	response := SessionResponse{
		SessionID: rec.SessionID, TaskID: rec.TaskID, EpicID: rec.EpicID,
		AgentName: rec.AgentName, Backend: rec.Backend, Model: rec.Model, Phase: rec.Phase,
		StartedAt: rec.StartedAt, EndedAt: rec.EndedAt, DurationS: rec.DurationS,
		Status: string(rec.Status), ExitCode: rec.ExitCode,
		FilesChanged: rec.FilesChanged, LinesAdded: rec.LinesAdded, LinesRemoved: rec.LinesRemoved, FilesTouched: rec.FilesTouched,
		AttemptNum: rec.AttemptNum, ErrorClass: rec.ErrorClass,
		IsActive: isActive, HasTranscript: hasTranscript, HasDiff: hasDiff,
		LastError: lastError, Evidence: evidence,
	}
	if evidence.UsageStatus != "unavailable" {
		response.InputTokens = &rec.InputTokens
		response.OutputTokens = &rec.OutputTokens
		response.CacheReadTokens = &rec.CacheReadTokens
		response.CacheWriteTokens = &rec.CacheWriteTokens
		response.EstimatedCostUSD = &rec.EstimatedCostUSD
	}
	return response
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
