package usage

import (
	"math"
	"testing"
	"time"
)

func TestEstimateCost_ClaudeDefaults(t *testing.T) {
	tier := DefaultPricing["claude"]
	u := SessionUsage{
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  500_000,
		CacheWriteTokens: 200_000,
	}
	got := EstimateCost(tier, u)
	// $3 input + $15 output + $0.15 cache read + $0.75 cache write = $18.90
	want := 18.90
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost = %f, want %f", got, want)
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	tier := DefaultPricing["claude"]
	u := SessionUsage{}
	got := EstimateCost(tier, u)
	if got != 0 {
		t.Errorf("EstimateCost with zero tokens = %f, want 0", got)
	}
}

func TestEstimateCost_CodexPricing(t *testing.T) {
	tier := DefaultPricing["codex"]
	u := SessionUsage{
		InputTokens:  2_000_000,
		OutputTokens: 500_000,
	}
	got := EstimateCost(tier, u)
	// $5.0 input + $5.0 output = $10.0
	want := 10.0
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost = %f, want %f", got, want)
	}
}

func TestResolvePricing_Defaults(t *testing.T) {
	tier := ResolvePricing("claude")
	if tier.InputPerMTok != 3.0 {
		t.Errorf("InputPerMTok = %f, want 3.0", tier.InputPerMTok)
	}
	if tier.OutputPerMTok != 15.0 {
		t.Errorf("OutputPerMTok = %f, want 15.0", tier.OutputPerMTok)
	}
}

func TestResolvePricing_UnknownBackend(t *testing.T) {
	tier := ResolvePricing("unknown-backend")
	// Falls back to Claude rates
	if tier.InputPerMTok != DefaultPricing["claude"].InputPerMTok {
		t.Errorf("unknown backend should fall back to claude pricing")
	}
}

func TestResolvePricing_EnvOverrides(t *testing.T) {
	t.Setenv("LOOM_COST_PER_MTOK_INPUT", "5.5")
	t.Setenv("LOOM_COST_PER_MTOK_OUTPUT", "20.0")

	tier := ResolvePricing("claude")
	if tier.InputPerMTok != 5.5 {
		t.Errorf("InputPerMTok = %f, want 5.5", tier.InputPerMTok)
	}
	if tier.OutputPerMTok != 20.0 {
		t.Errorf("OutputPerMTok = %f, want 20.0", tier.OutputPerMTok)
	}
	// Cache pricing should remain default
	if tier.CacheReadPerMTok != DefaultPricing["claude"].CacheReadPerMTok {
		t.Errorf("CacheReadPerMTok should remain default")
	}
}

func TestResolvePricing_InvalidEnvIgnored(t *testing.T) {
	t.Setenv("LOOM_COST_PER_MTOK_INPUT", "not-a-number")

	tier := ResolvePricing("claude")
	if tier.InputPerMTok != DefaultPricing["claude"].InputPerMTok {
		t.Errorf("invalid env var should be ignored, got %f", tier.InputPerMTok)
	}
}

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
