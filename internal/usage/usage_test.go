package usage

import (
	"testing"
	"time"
)

func TestSessionUsage_Fields(t *testing.T) {
	now := time.Now()
	u := SessionUsage{
		AgentName:        "falcon",
		Backend:          "claude",
		TaskID:           "task-1",
		EpicID:           "epic-1",
		InputTokens:      100,
		OutputTokens:     200,
		CacheReadTokens:  50,
		CacheWriteTokens: 25,
		EstimatedCostUSD: 0.01,
		StartedAt:        now,
		EndedAt:          now.Add(time.Minute),
		ExitCode:         0,
		Model:            "claude-sonnet-4",
	}
	if u.AgentName != "falcon" {
		t.Error("AgentName field")
	}
	if u.ExitCode != 0 {
		t.Error("ExitCode field")
	}
	if u.Model != "claude-sonnet-4" {
		t.Error("Model field")
	}
}
