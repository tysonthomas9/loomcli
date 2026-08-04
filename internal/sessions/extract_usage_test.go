package sessions

import "testing"

func TestExtractTranscriptUsage_ResultEntry(t *testing.T) {
	data := []byte(`{"role":"assistant","type":"text","text":"done"}
{"role":"system","type":"result","text":"completed","output":"{\"input_tokens\":8000,\"output_tokens\":300,\"cache_read_tokens\":12,\"cache_write_tokens\":7}"}
`)
	u := ExtractTranscriptUsage(data)
	if u.InputTokens != 8000 || u.OutputTokens != 300 || u.CacheReadTokens != 12 || u.CacheWriteTokens != 7 {
		t.Fatalf("got %+v", u)
	}
}

func TestExtractTranscriptUsage_CodexTokenCountLastWins(t *testing.T) {
	data := []byte(`{"timestamp":"t1","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":5}}}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"assistant"}}
{"timestamp":"t3","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4200,"output_tokens":880,"cached_input_tokens":2100}}}}
`)
	u := ExtractTranscriptUsage(data)
	if u.InputTokens != 4200 || u.OutputTokens != 880 || u.CacheReadTokens != 2100 {
		t.Fatalf("got %+v, want last cumulative totals", u)
	}
}

func TestExtractTranscriptUsage_TurnCompletedSums(t *testing.T) {
	data := []byte(`{"type":"turn.completed","usage":{"input_tokens":1000,"output_tokens":200,"cached_input_tokens":50}}
{"type":"turn.completed","usage":{"input_tokens":500,"output_tokens":100,"cached_input_tokens":25}}
`)
	u := ExtractTranscriptUsage(data)
	if u.InputTokens != 1500 || u.OutputTokens != 300 || u.CacheReadTokens != 75 {
		t.Fatalf("got %+v", u)
	}
}

func TestExtractTranscriptUsage_PrefersResultOverCodex(t *testing.T) {
	data := []byte(`{"timestamp":"t1","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":99,"output_tokens":9}}}}
{"role":"system","type":"result","output":"{\"input_tokens\":111,\"output_tokens\":22}"}
`)
	u := ExtractTranscriptUsage(data)
	if u.InputTokens != 111 || u.OutputTokens != 22 {
		t.Fatalf("got %+v, want result-entry usage", u)
	}
}

func TestExtractTranscriptUsage_ClaudeMessageUsageSumsAndDedupes(t *testing.T) {
	// Raw Claude Code transcript: two distinct assistant messages, and a snapshot
	// update repeating msg_001 with higher counts (last write wins per ID).
	data := []byte(`{"type":"assistant","message":{"id":"msg_001","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":20}}}
{"type":"user","message":{"role":"user"}}
{"type":"assistant","message":{"id":"msg_002","usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":30,"cache_creation_input_tokens":40}}}
{"type":"assistant","message":{"id":"msg_001","usage":{"input_tokens":150,"output_tokens":60,"cache_read_input_tokens":15,"cache_creation_input_tokens":25}}}
`)
	u := ExtractTranscriptUsage(data)
	// msg_001 (last snapshot) 150/60/15/25 + msg_002 200/80/30/40.
	if u.InputTokens != 350 || u.OutputTokens != 140 || u.CacheReadTokens != 45 || u.CacheWriteTokens != 65 {
		t.Fatalf("got %+v, want summed+deduped Claude usage", u)
	}
}

func TestExtractTranscriptUsage_PrefersResultOverClaudeMessages(t *testing.T) {
	data := []byte(`{"type":"assistant","message":{"id":"msg_001","usage":{"input_tokens":999,"output_tokens":99}}}
{"role":"system","type":"result","output":"{\"input_tokens\":111,\"output_tokens\":22}"}
`)
	u := ExtractTranscriptUsage(data)
	if u.InputTokens != 111 || u.OutputTokens != 22 {
		t.Fatalf("got %+v, want result-entry usage to win", u)
	}
}

func TestExtractTranscriptUsage_Empty(t *testing.T) {
	if !ExtractTranscriptUsage(nil).IsZero() {
		t.Fatal("nil data should be zero")
	}
	if !ExtractTranscriptUsage([]byte(`{"type":"response_item"}`)).IsZero() {
		t.Fatal("unrelated events should be zero")
	}
}
