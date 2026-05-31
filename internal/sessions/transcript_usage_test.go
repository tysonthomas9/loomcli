package sessions

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSumTranscriptUsage_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(txPath, []byte(""), 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheWriteTokens != 0 {
		t.Errorf("expected zero usage, got %+v", usage)
	}
	if usage.CostUSD != 0 || usage.Model != "" {
		t.Errorf("expected empty cost/model, got %+v", usage)
	}
}

func TestSumTranscriptUsage_SingleAssistant(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	line := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-8","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":200,"cache_creation_input_tokens":300}}}` + "\n"

	if err := os.WriteFile(txPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 200 {
		t.Errorf("CacheReadTokens = %d, want 200", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 300 {
		t.Errorf("CacheWriteTokens = %d, want 300", usage.CacheWriteTokens)
	}
	if usage.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8", usage.Model)
	}
}

func TestSumTranscriptUsage_ResultCost(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	lines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50}}}
{"type":"result","cost_usd":0.0123}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.CostUSD != 0.0123 {
		t.Errorf("CostUSD = %f, want 0.0123", usage.CostUSD)
	}
}

func TestSumTranscriptUsage_CodexTokenCount(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "rollout.jsonl")

	lines := `{"type":"turn_context","payload":{"model":"gpt-5.5"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":13797,"cached_input_tokens":4480,"output_tokens":28,"reasoning_output_tokens":17,"total_tokens":13825}}}}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 13797 {
		t.Errorf("InputTokens = %d, want 13797", usage.InputTokens)
	}
	if usage.OutputTokens != 28 {
		t.Errorf("OutputTokens = %d, want 28", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 4480 {
		t.Errorf("CacheReadTokens = %d, want 4480", usage.CacheReadTokens)
	}
	if usage.Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5", usage.Model)
	}
}

func TestSumTranscriptUsage_OpenCodeExport(t *testing.T) {
	data := []byte(`{
  "info": {"id":"ses_123"},
  "messages": [
    {
      "info": {"id":"msg_user","role":"user","time":{"created":1700000000000}},
      "parts": [{"type":"text","text":"hello"}]
    },
    {
      "info": {
        "id":"msg_assistant_1",
        "role":"assistant",
        "modelID":"anthropic/claude-sonnet-4",
        "time":{"created":1700000001000},
        "tokens":{"input":11,"output":7,"cache":{"read":3,"write":2}},
        "cost":0.0012
      },
      "parts": [{"type":"text","text":"first"}]
    },
    {
      "info": {
        "id":"msg_assistant_2",
        "role":"assistant",
        "modelID":"anthropic/claude-sonnet-4",
        "time":{"created":1700000002000},
        "tokens":{"input":13,"output":5,"cache":{"read":4,"write":1}},
        "cost":0.0023
      },
      "parts": [{"type":"text","text":"second"}]
    }
  ]
}`)

	usage, err := SumTranscriptUsageBytes(data)
	if err != nil {
		t.Fatalf("SumTranscriptUsageBytes: %v", err)
	}
	if usage.InputTokens != 24 {
		t.Errorf("InputTokens = %d, want 24", usage.InputTokens)
	}
	if usage.OutputTokens != 12 {
		t.Errorf("OutputTokens = %d, want 12", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 7 {
		t.Errorf("CacheReadTokens = %d, want 7", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 3 {
		t.Errorf("CacheWriteTokens = %d, want 3", usage.CacheWriteTokens)
	}
	if math.Abs(usage.CostUSD-0.0035) > 0.0000001 {
		t.Errorf("CostUSD = %f, want 0.0035", usage.CostUSD)
	}
	if usage.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("Model = %q, want anthropic/claude-sonnet-4", usage.Model)
	}
}

func TestSumTranscriptUsage_MultipleMessages(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	lines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":20}}}
{"type":"assistant","message":{"id":"msg_002","role":"assistant","content":[],"usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":30,"cache_creation_input_tokens":40}}}
{"type":"assistant","message":{"id":"msg_003","role":"assistant","content":[],"usage":{"input_tokens":150,"output_tokens":60,"cache_read_input_tokens":5,"cache_creation_input_tokens":0}}}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 450 {
		t.Errorf("InputTokens = %d, want 450 (100+200+150)", usage.InputTokens)
	}
	if usage.OutputTokens != 190 {
		t.Errorf("OutputTokens = %d, want 190 (50+80+60)", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 45 {
		t.Errorf("CacheReadTokens = %d, want 45 (10+30+5)", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 60 {
		t.Errorf("CacheWriteTokens = %d, want 60 (20+40+0)", usage.CacheWriteTokens)
	}
}

func TestSumTranscriptUsage_DuplicateMessageIDs(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	// msg_001 appears twice — last occurrence (500 input) should win
	lines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":20}}}
{"type":"assistant","message":{"id":"msg_002","role":"assistant","content":[],"usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":500,"output_tokens":150,"cache_read_input_tokens":30,"cache_creation_input_tokens":40}}}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// msg_001 last occurrence: 500 input, 150 output, 30 cache_read, 40 cache_write
	// msg_002: 200 input, 80 output, 0 cache_read, 0 cache_write
	// total: 700, 230, 30, 40
	if usage.InputTokens != 700 {
		t.Errorf("InputTokens = %d, want 700 (500+200)", usage.InputTokens)
	}
	if usage.OutputTokens != 230 {
		t.Errorf("OutputTokens = %d, want 230 (150+80)", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens = %d, want 30 (30+0)", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 40 {
		t.Errorf("CacheWriteTokens = %d, want 40 (40+0)", usage.CacheWriteTokens)
	}
}

func TestSumTranscriptUsage_NonAssistantSkipped(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	// Only the assistant entry should be counted; user/system/tool entries are skipped.
	lines := `{"type":"user","message":{"id":"msg_u01","role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","message":{"id":"msg_a01","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"tool","message":{"id":"msg_t01","role":"tool","content":[]}}
{"type":"system","message":{"id":"msg_s01","content":[]}}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", usage.OutputTokens)
	}
}

func TestSumTranscriptUsage_MissingFile(t *testing.T) {
	usage, err := SumTranscriptUsage("/nonexistent/path/transcript.jsonl")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheWriteTokens != 0 {
		t.Errorf("expected zero usage for missing file, got %+v", usage)
	}
}

func TestSumTranscriptUsage_CorruptLine(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	// Entirely corrupt content — should return zero usage, nil error.
	content := "this is not json at all\n{broken json!!\n"
	if err := os.WriteFile(txPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("expected zero usage for all-corrupt file, got %+v", usage)
	}
}

func TestSumTranscriptUsage_MixedValidCorrupt(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")

	lines := `{"type":"assistant","message":{"id":"msg_001","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
this line is corrupt
{"type":"assistant","message":{"id":"msg_002","role":"assistant","content":[],"usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}
{also broken
{"type":"assistant","message":{"id":"msg_003","role":"assistant","content":[],"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`
	if err := os.WriteFile(txPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	usage, err := SumTranscriptUsage(txPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Valid lines: msg_001(100,50), msg_002(200,80), msg_003(50,20)
	if usage.InputTokens != 350 {
		t.Errorf("InputTokens = %d, want 350 (100+200+50)", usage.InputTokens)
	}
	if usage.OutputTokens != 150 {
		t.Errorf("OutputTokens = %d, want 150 (50+80+20)", usage.OutputTokens)
	}
	if usage.CacheReadTokens != 10 {
		t.Errorf("CacheReadTokens = %d, want 10", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 5 {
		t.Errorf("CacheWriteTokens = %d, want 5", usage.CacheWriteTokens)
	}
}
