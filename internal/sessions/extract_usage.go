package sessions

import (
	"bytes"
	"encoding/json"
	"iter"
	"os"
)

// ExtractTranscriptUsage recovers token totals from a native transcript blob.
// Precedence:
//  1. TS-leaf terminal `result` entry (usage serialized in `output`)
//  2. Codex rollout last `event_msg`/`token_count` with cumulative total_token_usage
//  3. Summed Codex/stream `turn.completed` usage objects
//  4. Raw Claude Code transcript `assistant` `message.usage`, deduped by message
//     ID and summed (daemon/fleet Claude sessions store this format verbatim)
//
// Returns the zero value when the transcript has no recoverable usage.
func ExtractTranscriptUsage(data []byte) TokenUsage {
	u, _ := ExtractTranscriptUsageWithCost(data)
	return u
}

// ExtractTranscriptUsageWithCost is ExtractTranscriptUsage plus the
// provider-reported cost_usd. Only the TS leaf's terminal `result` entry reports
// cost; the Codex sources carry tokens alone, so cost is 0 there. Loom never
// estimates cost from tokens, so `estimated_cost_usd` in the transcript is
// deliberately not read.
func ExtractTranscriptUsageWithCost(data []byte) (TokenUsage, float64) {
	if len(data) == 0 {
		return TokenUsage{}, 0
	}
	// The result entry is authoritative whenever it reports anything at all;
	// an all-zero one falls through so a mixed transcript can still recover.
	if u, cost, ok := extractResultEntryUsage(data); ok && (!u.IsZero() || cost != 0) {
		return u, cost
	}
	if u := extractCodexTokenCountUsage(data); !u.IsZero() {
		return u, 0
	}
	if u := extractTurnCompletedUsage(data); !u.IsZero() {
		return u, 0
	}
	// Raw Claude Code transcripts (stored verbatim by SyncLatestClaudeTranscript)
	// carry no `result`/Codex events; recover usage from their assistant
	// message.usage records so daemon/fleet Claude sessions don't finalize at zero.
	return extractClaudeMessageUsage(data), 0
}

// extractClaudeMessageUsage sums usage from a raw Claude Code transcript's
// `assistant` `message.usage` records, deduped by message ID. Reuses the same
// per-line accumulation as the streaming SumTranscriptUsage hook path.
func extractClaudeMessageUsage(data []byte) TokenUsage {
	seen := make(map[string]TokenUsage)
	for line := range jsonLines(data) {
		accumulateClaudeUsage(seen, line)
	}
	return sumClaudeUsage(seen)
}

// TranscriptUsage recovers token usage from a session's on-disk native
// transcript. Zero when the transcript is missing, empty, or carries nothing
// recoverable — callers use it to backfill sessions whose collector finalize
// never persisted usage.
func (s *Store) TranscriptUsage(sessionID string) TokenUsage {
	data, err := os.ReadFile(s.NativeTranscriptPath(sessionID)) //nolint:gosec // session-owned path
	if err != nil || len(data) == 0 {
		return TokenUsage{}
	}
	return ExtractTranscriptUsage(data)
}

// IsZero reports whether all token fields are zero.
func (u TokenUsage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.CacheWriteTokens == 0
}

// extractResultEntryUsage returns the usage and provider-reported cost carried by
// the transcript's LAST `result` entry. ok is false when there is no such entry
// (or its `output` does not decode), which is the signal to try another source.
func extractResultEntryUsage(data []byte) (TokenUsage, float64, bool) {
	for line := range jsonLinesReverse(data) {
		// Cheap pre-filter: skip the JSON decode for lines that cannot be a
		// `result` entry (the common case in Codex/Go-leaf transcripts, which
		// have no terminal result at all).
		if !bytes.Contains(line, []byte(`"result"`)) {
			continue
		}
		var ev struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Type != "result" || ev.Output == "" {
			continue
		}
		var u struct {
			InputTokens      int64   `json:"input_tokens"`
			OutputTokens     int64   `json:"output_tokens"`
			CacheReadTokens  int64   `json:"cache_read_tokens"`
			CacheWriteTokens int64   `json:"cache_write_tokens"`
			CostUSD          float64 `json:"cost_usd"`
		}
		if json.Unmarshal([]byte(ev.Output), &u) != nil {
			return TokenUsage{}, 0, false
		}
		return TokenUsage{
			InputTokens:      u.InputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}, u.CostUSD, true
	}
	return TokenUsage{}, 0, false
}

func extractCodexTokenCountUsage(data []byte) TokenUsage {
	// The last token_count's total_token_usage is cumulative for the session, so
	// scan newest-first and return the first one found instead of walking every
	// line to keep the tail.
	for line := range jsonLinesReverse(data) {
		var ev struct {
			Type    string `json:"type"`
			Payload *struct {
				Type string `json:"type"`
				Info *struct {
					TotalTokenUsage *struct {
						InputTokens        int64 `json:"input_tokens"`
						OutputTokens       int64 `json:"output_tokens"`
						CachedInputTokens  int64 `json:"cached_input_tokens"`
						CacheCreationInput int64 `json:"cache_creation_input_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type != "event_msg" || ev.Payload == nil || ev.Payload.Type != "token_count" {
			continue
		}
		if ev.Payload.Info == nil || ev.Payload.Info.TotalTokenUsage == nil {
			continue
		}
		tu := ev.Payload.Info.TotalTokenUsage
		return TokenUsage{
			InputTokens:      tu.InputTokens,
			OutputTokens:     tu.OutputTokens,
			CacheReadTokens:  tu.CachedInputTokens,
			CacheWriteTokens: tu.CacheCreationInput,
		}
	}
	return TokenUsage{}
}

func extractTurnCompletedUsage(data []byte) TokenUsage {
	// Stream JSON turn.completed events carry per-turn usage; sum them.
	var total TokenUsage
	for line := range jsonLines(data) {
		var ev struct {
			Type  string `json:"type"`
			Usage *struct {
				InputTokens       int64 `json:"input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type != "turn.completed" || ev.Usage == nil {
			continue
		}
		total.InputTokens += ev.Usage.InputTokens
		total.OutputTokens += ev.Usage.OutputTokens
		total.CacheReadTokens += ev.Usage.CachedInputTokens
	}
	return total
}

// jsonLines yields each non-blank, whitespace-trimmed line of a JSONL blob, oldest
// first. It slices data in place rather than building a bytes.Split index over
// the whole transcript, which matters on multi-MB rollouts.
func jsonLines(data []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for len(data) > 0 {
			line := data
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				line, data = data[:i], data[i+1:]
			} else {
				data = nil
			}
			if line = bytes.TrimSpace(line); len(line) == 0 {
				continue
			}
			if !yield(line) {
				return
			}
		}
	}
}

// jsonLinesReverse is jsonLines in newest-first order, so a scan for the terminal entry
// stops at the tail instead of reading the whole transcript.
func jsonLinesReverse(data []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for len(data) > 0 {
			line := data
			if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
				line, data = data[i+1:], data[:i]
			} else {
				data = nil
			}
			if line = bytes.TrimSpace(line); len(line) == 0 {
				continue
			}
			if !yield(line) {
				return
			}
		}
	}
}
