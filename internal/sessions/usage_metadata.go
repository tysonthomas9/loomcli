package sessions

import "strconv"

// Session-metadata keys carrying token/cost telemetry.
const (
	MetadataInputTokens     = "input_tokens"
	MetadataOutputTokens    = "output_tokens"
	MetadataCacheReadTokens = "cache_read_tokens"
	//nolint:gosec // G101: a metadata key name for cache-write token counts, not a credential.
	MetadataCacheWriteTokens = "cache_write_tokens"
	MetadataEstimatedCostUSD = "estimated_cost_usd"
)

// UsageStats is the token/cost telemetry a task run reported. It mirrors the
// usage fields on SessionRecord.
type UsageStats struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
}

// Zero reports whether no usage at all was reported.
func (u UsageStats) Zero() bool { return u == UsageStats{} }

// EncodeUsageMetadata writes usage onto control-plane session metadata, the same
// carrier EncodeDiffStatsMetadata uses. domain.AgentSession has no usage fields,
// so without this the telemetry the driver already parsed out of the runner
// result is dropped on the floor and every control-plane session renders as a
// zero-token, zero-cost run.
//
// The cost key is written only when the backend actually reported a cost —
// codex, cursor and gemini report none, and a fabricated $0 is indistinguishable
// from a real free run.
func EncodeUsageMetadata(metadata map[string]string, usage UsageStats) {
	if metadata == nil {
		return
	}
	metadata[MetadataInputTokens] = strconv.FormatInt(usage.InputTokens, 10)
	metadata[MetadataOutputTokens] = strconv.FormatInt(usage.OutputTokens, 10)
	metadata[MetadataCacheReadTokens] = strconv.FormatInt(usage.CacheReadTokens, 10)
	metadata[MetadataCacheWriteTokens] = strconv.FormatInt(usage.CacheWriteTokens, 10)
	if usage.EstimatedCostUSD != 0 {
		metadata[MetadataEstimatedCostUSD] = strconv.FormatFloat(usage.EstimatedCostUSD, 'f', -1, 64)
	}
}

// DecodeUsageMetadata reads usage back off session metadata. ok reports whether
// any usage was recorded at all, so callers can tell "this run reported no
// telemetry" from "this run genuinely used zero tokens".
func DecodeUsageMetadata(metadata map[string]string) (UsageStats, bool) {
	if metadata == nil {
		return UsageStats{}, false
	}
	present := false
	for _, key := range []string{
		MetadataInputTokens,
		MetadataOutputTokens,
		MetadataCacheReadTokens,
		MetadataCacheWriteTokens,
		MetadataEstimatedCostUSD,
	} {
		if _, ok := metadata[key]; ok {
			present = true
			break
		}
	}
	if !present {
		return UsageStats{}, false
	}
	cost, _ := strconv.ParseFloat(metadata[MetadataEstimatedCostUSD], 64)
	return UsageStats{
		InputTokens:      metadataInt64(metadata, MetadataInputTokens),
		OutputTokens:     metadataInt64(metadata, MetadataOutputTokens),
		CacheReadTokens:  metadataInt64(metadata, MetadataCacheReadTokens),
		CacheWriteTokens: metadataInt64(metadata, MetadataCacheWriteTokens),
		EstimatedCostUSD: cost,
	}, true
}

func metadataInt64(metadata map[string]string, key string) int64 {
	n, _ := strconv.ParseInt(metadata[key], 10, 64)
	return n
}
