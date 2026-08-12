package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/runtimectx"
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
	_, span := startSpan(runtimectx.RootContext(), "service.Sessions.SumTranscriptUsage")
	defer span.End()

	// #nosec G304 — transcriptPath comes from Claude's hook payload (trusted)
	f, err := os.Open(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return TokenUsage{}, nil
		}
		recordErr(span, err)
		return TokenUsage{}, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	seen := make(map[string]TokenUsage)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024) // 1MB buffer matching existing pattern
	for scanner.Scan() {
		accumulateClaudeUsage(seen, scanner.Bytes())
	}

	total := sumClaudeUsage(seen)
	if err := scanner.Err(); err != nil {
		recordErr(span, err)
		return total, fmt.Errorf("scan transcript: %w", err)
	}

	return total, nil
}

// accumulateClaudeUsage parses one Claude transcript line and, when it is an
// assistant message carrying a message ID, records that message's usage keyed by
// ID. Last write wins: Claude emits snapshot updates with increasing counts for
// the same message, so the final line for an ID holds its total. Non-assistant,
// ID-less, and unparseable lines are ignored. Shared by the streaming
// SumTranscriptUsage (hook path) and the in-memory extractClaudeMessageUsage
// (finalize recovery) so both agree on how raw Claude usage is summed.
func accumulateClaudeUsage(seen map[string]TokenUsage, line []byte) {
	if len(line) == 0 {
		return
	}
	var entry claudeTranscriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return // skip corrupt lines gracefully
	}
	if entry.Type != "assistant" || entry.Message.ID == "" {
		return
	}
	u := entry.Message.Usage
	seen[entry.Message.ID] = TokenUsage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
	}
}

// sumClaudeUsage totals the per-message usage collected by accumulateClaudeUsage.
func sumClaudeUsage(seen map[string]TokenUsage) TokenUsage {
	var total TokenUsage
	for _, u := range seen {
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheReadTokens += u.CacheReadTokens
		total.CacheWriteTokens += u.CacheWriteTokens
	}
	return total
}
