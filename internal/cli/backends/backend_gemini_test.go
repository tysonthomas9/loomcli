package backends

import "testing"

func TestGeminiBackendName(t *testing.T) {
	t.Parallel()
	b := &GeminiBackend{}
	if got := b.Name(); got != "gemini" {
		t.Errorf("expected 'gemini', got %q", got)
	}
}

func TestGeminiBackendProviderMetadataReporter(t *testing.T) {
	b := &GeminiBackend{}
	geminiProviderMetadata.Clear("gemini")
	t.Cleanup(func() { geminiProviderMetadata.Clear("gemini") })

	geminiProviderMetadata.IngestLine(`{"type":"session","conversation_id":"gemini-conversation-1","model":"gemini-2.5-pro"}`)

	if got := b.LastSessionID("/work"); got != "gemini-conversation-1" {
		t.Fatalf("LastSessionID() = %q, want gemini-conversation-1", got)
	}
	meta := b.LastProviderMetadata("/work")
	if meta["provider"] != "gemini" || meta["provider_session_id"] != "gemini-conversation-1" || meta["provider_model"] != "gemini-2.5-pro" {
		t.Fatalf("LastProviderMetadata() = %#v, want gemini provider metadata", meta)
	}
}
