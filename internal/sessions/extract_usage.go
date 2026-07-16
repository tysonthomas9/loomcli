package sessions

import (
	"bytes"
	"encoding/json"
)

// ExtractTranscriptUsage recovers token totals from a native transcript blob.
// Precedence:
//  1. TS-leaf terminal `result` entry (usage serialized in `output`)
//  2. Codex rollout last `event_msg`/`token_count` with cumulative total_token_usage
//  3. Summed Codex/stream `turn.completed` usage objects
//
// Returns the zero value when the transcript has no recoverable usage.
func ExtractTranscriptUsage(data []byte) TokenUsage {
	if len(data) == 0 {
		return TokenUsage{}
	}
	if u := extractResultEntryUsage(data); !u.IsZero() {
		return u
	}
	if u := extractCodexTokenCountUsage(data); !u.IsZero() {
		return u
	}
	return extractTurnCompletedUsage(data)
}

// IsZero reports whether all token fields are zero.
func (u TokenUsage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.CacheWriteTokens == 0
}

func extractResultEntryUsage(data []byte) TokenUsage {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
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
			InputTokens      int64 `json:"input_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			CacheReadTokens  int64 `json:"cache_read_tokens"`
			CacheWriteTokens int64 `json:"cache_write_tokens"`
		}
		if json.Unmarshal([]byte(ev.Output), &u) != nil {
			return TokenUsage{}
		}
		return TokenUsage{
			InputTokens:      u.InputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}
	}
	return TokenUsage{}
}

func extractCodexTokenCountUsage(data []byte) TokenUsage {
	// Last token_count's total_token_usage is cumulative for the session.
	var last TokenUsage
	found := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
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
		last = TokenUsage{
			InputTokens:      tu.InputTokens,
			OutputTokens:     tu.OutputTokens,
			CacheReadTokens:  tu.CachedInputTokens,
			CacheWriteTokens: tu.CacheCreationInput,
		}
		found = true
	}
	if !found {
		return TokenUsage{}
	}
	return last
}

func extractTurnCompletedUsage(data []byte) TokenUsage {
	// Stream JSON turn.completed events carry per-turn usage; sum them.
	var total TokenUsage
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
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
