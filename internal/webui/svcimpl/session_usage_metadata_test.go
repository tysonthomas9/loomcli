package svcimpl

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Usage recorded on a control-plane session must reach the SessionRecord the
// Runs tab renders; otherwise a real run still displays as zero tokens / $0.
func TestSessionRecordFromAgentSessionSurfacesUsage(t *testing.T) {
	rec := &domain.AgentSession{
		SessionID: "flue-task-run-1",
		AgentID:   "flue-task-agent",
		TaskID:    "TASK-1",
		Status:    domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"input_tokens":       "1200",
			"output_tokens":      "340",
			"cache_read_tokens":  "56",
			"cache_write_tokens": "7",
			"estimated_cost_usd": "0.0125",
		},
	}

	out := sessionRecordFromAgentSession(rec)
	if out.InputTokens != 1200 || out.OutputTokens != 340 {
		t.Fatalf("tokens = %d/%d, want 1200/340", out.InputTokens, out.OutputTokens)
	}
	if out.CacheReadTokens != 56 || out.CacheWriteTokens != 7 {
		t.Fatalf("cache tokens = %d/%d, want 56/7", out.CacheReadTokens, out.CacheWriteTokens)
	}
	if out.EstimatedCostUSD != 0.0125 {
		t.Fatalf("cost = %v, want 0.0125", out.EstimatedCostUSD)
	}
}
