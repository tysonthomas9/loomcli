package supervisor

import (
	"strings"
	"testing"
)

// TestExtractLeafUsage_ParsesResultEntryUsage proves the supervisor recovers the
// TS leaf's token/cost usage from the terminal `result` entry's `output` field —
// the channel that fixes the daemon session-metadata tokens=0 finding (the reaped
// worker's collector-aware finalize never runs, so the supervisor sources usage here).
func TestExtractLeafUsage_ParsesResultEntryUsage(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"role":"system","type":"session_meta","text":"local-cli-codex session"}`,
		`{"role":"assistant","type":"text","text":"done"}`,
		`{"role":"system","type":"result","text":"completed","output":"{\"input_tokens\":8000,\"output_tokens\":300,\"cache_read_tokens\":12,\"cache_write_tokens\":7,\"cost_usd\":0.42}"}`,
	}, "\n") + "\n")

	u := extractLeafUsage(data)
	if u.InputTokens != 8000 || u.OutputTokens != 300 || u.CacheReadTokens != 12 || u.CacheWriteTokens != 7 {
		t.Fatalf("token mismatch: %+v", u)
	}
	if u.cost() != 0.42 {
		t.Errorf("cost = %v, want 0.42", u.cost())
	}
}

// TestExtractLeafUsage_RawStreamHasNoUsage proves the Go leaf's raw backend stream
// (no canonical `result` entry) yields zero usage — i.e. this change can't regress
// the Go leaf, only populate the TS leaf.
func TestExtractLeafUsage_RawStreamHasNoUsage(t *testing.T) {
	data := []byte(`{"type":"response_item","payload":{"role":"assistant"}}` + "\n")
	if u := extractLeafUsage(data); u != (leafUsage{}) {
		t.Errorf("raw stream must yield zero usage, got %+v", u)
	}
}

// TestLeafUsageCost_PrefersCostOverEstimate pins the cost() precedence: the
// backend-reported cost_usd wins; estimated_cost_usd is the fallback.
func TestLeafUsageCost_PrefersCostOverEstimate(t *testing.T) {
	if got := (leafUsage{CostUSD: 1.5, EstimatedCostUSD: 9.9}).cost(); got != 1.5 {
		t.Errorf("cost() = %v, want 1.5 (cost_usd wins)", got)
	}
	if got := (leafUsage{EstimatedCostUSD: 2.5}).cost(); got != 2.5 {
		t.Errorf("cost() = %v, want 2.5 (estimated fallback)", got)
	}
}
