package backends

import "testing"

func TestBackendProviderMetadataCapture_DirectFields(t *testing.T) {
	var capture backendProviderMetadataCapture
	capture.Clear("codex")
	capture.IngestLine("\x04\b\b" + `{"type":"thread.started","thread_id":"thread-123","model":"gpt-5"}`)
	capture.IngestLine(`{"type":"turn.completed","response_id":"resp-1"}`)

	if got := capture.LastSessionID(); got != "thread-123" {
		t.Fatalf("LastSessionID() = %q, want thread-123", got)
	}
	meta := capture.Metadata()
	if meta["provider"] != "codex" {
		t.Fatalf("provider metadata = %#v, want provider=codex", meta)
	}
	if meta["provider_session_id"] != "thread-123" || meta["provider_model"] != "gpt-5" {
		t.Fatalf("provider metadata = %#v, want session and model", meta)
	}
	if meta["json_event_count"] != 2 {
		t.Fatalf("provider metadata = %#v, want json_event_count=2", meta)
	}
	eventTypes, ok := meta["event_types"].([]string)
	if !ok || len(eventTypes) != 2 || eventTypes[0] != "thread.started" || eventTypes[1] != "turn.completed" {
		t.Fatalf("event_types = %#v, want ordered unique event types", meta["event_types"])
	}
	ids, ok := meta["ids"].(map[string]string)
	if !ok || ids["thread_id"] != "thread-123" || ids["response_id"] != "resp-1" {
		t.Fatalf("ids = %#v, want provider IDs", meta["ids"])
	}
}

func TestBackendProviderMetadataCapture_NestedFields(t *testing.T) {
	var capture backendProviderMetadataCapture
	capture.Clear("opencode")
	capture.IngestLine(`{"type":"session","session":{"id":"sess-456"},"response":{"model":"provider/model"}}`)

	if got := capture.LastSessionID(); got != "sess-456" {
		t.Fatalf("LastSessionID() = %q, want sess-456", got)
	}
	meta := capture.Metadata()
	if meta["provider_session_id"] != "sess-456" || meta["provider_model"] != "provider/model" {
		t.Fatalf("provider metadata = %#v, want nested session and model", meta)
	}
}

func TestBackendProviderMetadataCapture_Clear(t *testing.T) {
	var capture backendProviderMetadataCapture
	capture.Clear("gemini")
	capture.IngestLine(`{"type":"session","session_id":"sess-1"}`)
	capture.Clear("gemini")

	if got := capture.LastSessionID(); got != "" {
		t.Fatalf("LastSessionID() after clear = %q, want empty", got)
	}
	if meta := capture.Metadata(); meta != nil {
		t.Fatalf("Metadata() after clear = %#v, want nil", meta)
	}
}
