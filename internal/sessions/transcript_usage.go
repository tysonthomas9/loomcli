package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// TokenUsage holds aggregated token counts from a Claude transcript.
type TokenUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// claudeTranscriptEntry is the minimal structure needed to extract usage from
// Claude Code's transcript JSONL file. Only fields we need are parsed.
// The message field is at the top level for type=assistant entries.
type claudeTranscriptEntry struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// SumTranscriptUsage reads a Claude Code transcript JSONL file and sums token
// usage across all assistant messages. Duplicate message IDs are deduplicated
// by keeping the last occurrence (Claude writes snapshot updates with
// increasing token counts for the same message). Returns zero usage and nil
// error if the file does not exist (graceful degradation).
//
// On I/O errors mid-scan, returns partial results alongside the error.
// Callers that need exact totals should check the error before using the result.
func SumTranscriptUsage(transcriptPath string) (TokenUsage, error) {
	// #nosec G304 — transcriptPath comes from Claude's hook payload (trusted)
	f, err := os.Open(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return TokenUsage{}, nil
		}
		return TokenUsage{}, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	type msgUsage struct {
		input      int64
		output     int64
		cacheRead  int64
		cacheWrite int64
	}
	seen := make(map[string]msgUsage)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024) // 1MB buffer matching existing pattern
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry claudeTranscriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip corrupt lines gracefully
		}

		if entry.Type != "assistant" {
			continue
		}

		msgID := entry.Message.ID
		if msgID == "" {
			continue
		}

		u := entry.Message.Usage
		seen[msgID] = msgUsage{
			input:      u.InputTokens,
			output:     u.OutputTokens,
			cacheRead:  u.CacheReadInputTokens,
			cacheWrite: u.CacheCreationInputTokens,
		}
	}

	var total TokenUsage
	for _, u := range seen {
		total.InputTokens += u.input
		total.OutputTokens += u.output
		total.CacheReadTokens += u.cacheRead
		total.CacheWriteTokens += u.cacheWrite
	}

	if err := scanner.Err(); err != nil {
		return total, fmt.Errorf("scan transcript: %w", err)
	}

	return total, nil
}
