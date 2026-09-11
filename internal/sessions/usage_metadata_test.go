package sessions

import "testing"

func TestEncodeDecodeUsageMetadataRoundTrip(t *testing.T) {
	meta := map[string]string{}
	EncodeUsageMetadata(meta, UsageStats{
		InputTokens:      1200,
		OutputTokens:     340,
		CacheReadTokens:  56,
		CacheWriteTokens: 7,
		EstimatedCostUSD: 0.0125,
	})

	got, ok := DecodeUsageMetadata(meta)
	if !ok {
		t.Fatal("DecodeUsageMetadata reported no usage after encoding some")
	}
	if got.InputTokens != 1200 || got.OutputTokens != 340 {
		t.Fatalf("tokens = %d/%d, want 1200/340", got.InputTokens, got.OutputTokens)
	}
	if got.CacheReadTokens != 56 || got.CacheWriteTokens != 7 {
		t.Fatalf("cache tokens = %d/%d, want 56/7", got.CacheReadTokens, got.CacheWriteTokens)
	}
	if got.EstimatedCostUSD != 0.0125 {
		t.Fatalf("cost = %v, want 0.0125", got.EstimatedCostUSD)
	}
}

// "No usage recorded" must be distinguishable from "usage was zero" — otherwise
// a session that never reported telemetry renders as a real zero-cost run.
func TestDecodeUsageMetadataReportsAbsence(t *testing.T) {
	if _, ok := DecodeUsageMetadata(nil); ok {
		t.Fatal("nil metadata reported usage")
	}
	if _, ok := DecodeUsageMetadata(map[string]string{"backend": "codex"}); ok {
		t.Fatal("metadata without usage keys reported usage")
	}
	meta := map[string]string{}
	EncodeUsageMetadata(meta, UsageStats{})
	got, ok := DecodeUsageMetadata(meta)
	if !ok {
		t.Fatal("an explicitly recorded zero usage must still report present")
	}
	if got != (UsageStats{}) {
		t.Fatalf("usage = %+v, want zero", got)
	}
}

// A backend that reports tokens but no cost (codex, cursor, gemini) must not be
// given a fabricated $0 — the cost key is simply absent.
func TestEncodeUsageMetadataOmitsUnknownCost(t *testing.T) {
	meta := map[string]string{}
	EncodeUsageMetadata(meta, UsageStats{InputTokens: 10, OutputTokens: 5})
	if _, present := meta[MetadataEstimatedCostUSD]; present {
		t.Fatalf("cost key present for a backend that reported none: %+v", meta)
	}
	got, ok := DecodeUsageMetadata(meta)
	if !ok || got.InputTokens != 10 || got.EstimatedCostUSD != 0 {
		t.Fatalf("decoded = %+v ok=%v, want tokens with zero cost", got, ok)
	}
}
