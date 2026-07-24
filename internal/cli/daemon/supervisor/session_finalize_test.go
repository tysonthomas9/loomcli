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
	if u.CostUSD != 0.42 {
		t.Errorf("cost = %v, want 0.42", u.CostUSD)
	}
}

// TestExtractLeafUsage_RawStreamHasNoUsage proves unrelated raw backend stream
// lines (no result / token_count / turn.completed) still yield zero usage.
func TestExtractLeafUsage_RawStreamHasNoUsage(t *testing.T) {
	data := []byte(`{"type":"response_item","payload":{"role":"assistant"}}` + "\n")
	if u := extractLeafUsage(data); u != (leafUsage{}) {
		t.Errorf("raw stream must yield zero usage, got %+v", u)
	}
}

func TestExtractLeafUsage_CodexTokenCount(t *testing.T) {
	data := []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4200,"output_tokens":880,"cached_input_tokens":100}}}}` + "\n")
	u := extractLeafUsage(data)
	if u.InputTokens != 4200 || u.OutputTokens != 880 || u.CacheReadTokens != 100 {
		t.Fatalf("got %+v", u)
	}
}

// TestExtractLeafUsage_IgnoresEstimatedCost pins that only the provider-reported
// cost_usd is carried through; a leaf's estimated_cost_usd is never persisted.
func TestExtractLeafUsage_IgnoresEstimatedCost(t *testing.T) {
	data := []byte(`{"role":"system","type":"result","output":"{\"input_tokens\":10,\"estimated_cost_usd\":2.5}"}` + "\n")
	if u := extractLeafUsage(data); u.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 (no estimate fallback)", u.CostUSD)
	}
}
