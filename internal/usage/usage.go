package usage

import (
	"time"
)

// SessionUsage records token consumption and optional provider-reported cost
// for one agent session. EstimatedCostUSD is a pass-through for backend
// cost_usd when available; Loom does not fabricate cost from tokens.
type SessionUsage struct {
	AgentName        string    `json:"agent_name"`
	Backend          string    `json:"backend"`
	TaskID           string    `json:"task_id,omitempty"`
	EpicID           string    `json:"epic_id,omitempty"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	ExitCode         int       `json:"exit_code"`
	Model            string    `json:"model,omitempty"`
	SessionID        string    `json:"session_id,omitempty"`

	// Status and DurationS carry the richer outcome data recorded by the
	// session index (sessions/index.jsonl). Both are omitempty so records
	// from the legacy usage.jsonl ledger still round-trip unchanged.
	Status    string  `json:"status,omitempty"`
	DurationS float64 `json:"duration_s,omitempty"`
}
