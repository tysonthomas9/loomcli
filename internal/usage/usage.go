// Package usage tallies token consumption and estimated cost for one agent run:
// a Collector that de-duplicates the agent CLI's streamed usage events, per-agent-backend
// pricing tiers, and a flock-serialized append-only .loom/usage.jsonl store with
// filtered queries and purge. Fed by internal/cli/backends; read by `loom usage`.
package usage

import (
	"os"
	"strconv"
	"time"
)

// SessionUsage records token consumption and estimated cost for one agent session.
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
}

// PricingTier holds per-million-token pricing for a backend.
type PricingTier struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// DefaultPricing maps backend names to their default pricing tiers.
// Defaults to Claude Sonnet 4 rates ($3/$15 per MTok input/output).
var DefaultPricing = map[string]PricingTier{
	"claude": {
		InputPerMTok:      3.0,
		OutputPerMTok:     15.0,
		CacheReadPerMTok:  0.30,
		CacheWritePerMTok: 3.75,
	},
	"codex": {
		InputPerMTok:  2.50,
		OutputPerMTok: 10.0,
	},
	"opencode": {
		InputPerMTok:  3.0,
		OutputPerMTok: 15.0,
	},
}

// EstimateCost computes estimated cost in USD from token counts and a pricing tier.
func EstimateCost(tier PricingTier, u SessionUsage) float64 {
	const mtok = 1_000_000.0
	cost := float64(u.InputTokens) / mtok * tier.InputPerMTok
	cost += float64(u.OutputTokens) / mtok * tier.OutputPerMTok
	cost += float64(u.CacheReadTokens) / mtok * tier.CacheReadPerMTok
	cost += float64(u.CacheWriteTokens) / mtok * tier.CacheWritePerMTok
	return cost
}

// ResolvePricing returns the pricing tier for the given backend, applying
// env var overrides (LOOM_COST_PER_MTOK_INPUT, LOOM_COST_PER_MTOK_OUTPUT)
// if set.
func ResolvePricing(backend string) PricingTier {
	tier, ok := DefaultPricing[backend]
	if !ok {
		tier = DefaultPricing["claude"] // fallback to Claude rates
	}

	if v := os.Getenv("LOOM_COST_PER_MTOK_INPUT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			tier.InputPerMTok = f
		}
	}
	if v := os.Getenv("LOOM_COST_PER_MTOK_OUTPUT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			tier.OutputPerMTok = f
		}
	}
	return tier
}
