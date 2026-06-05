package backends

import (
	"strings"
	"testing"
)

func TestStreamFlueSSE_StreamsTextThenIdle(t *testing.T) {
	sse := "event: text_delta\n" +
		"data: {\"type\":\"text_delta\",\"text\":\"Hello \"}\n\n" +
		"event: text_delta\n" +
		"data: {\"type\":\"text_delta\",\"text\":\"world\"}\n\n" +
		"event: idle\n" +
		"data: {\"type\":\"idle\"}\n\n"
	var out strings.Builder
	if err := streamFlueSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "Hello world") {
		t.Fatalf("streamed text = %q, want prefix %q", got, "Hello world")
	}
}

func TestStreamFlueSSE_FallsBackToOperationText(t *testing.T) {
	sse := "event: operation\n" +
		"data: {\"type\":\"operation\",\"operationKind\":\"prompt\",\"result\":{\"text\":\"Final answer.\"}}\n\n" +
		"event: idle\n" +
		"data: {\"type\":\"idle\"}\n\n"
	var out strings.Builder
	if err := streamFlueSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Final answer.") {
		t.Fatalf("expected fallback to operation result text, got %q", out.String())
	}
}

func TestStreamFlueSSE_ErrorEvent(t *testing.T) {
	sse := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"bad_thing\",\"message\":\"it broke\"}}\n\n"
	var out strings.Builder
	err := streamFlueSSE(strings.NewReader(sse), &out)
	if err == nil || !strings.Contains(err.Error(), "it broke") {
		t.Fatalf("expected error containing 'it broke', got %v", err)
	}
}

func TestStreamFlueSSE_NonSSEErrorEnvelope(t *testing.T) {
	// When the agent isn't registered, flue returns a raw JSON error body.
	body := "{\"error\":{\"type\":\"agent_not_found\",\"message\":\"Agent \\\"lead\\\" is not registered.\"}}\n"
	var out strings.Builder
	err := streamFlueSSE(strings.NewReader(body), &out)
	if err == nil || !strings.Contains(err.Error(), "agent_not_found") {
		t.Fatalf("expected agent_not_found error, got %v", err)
	}
}

func TestLeadInstanceID(t *testing.T) {
	a := leadInstanceID("/home/user/repoA")
	b := leadInstanceID("/home/user/repoB")
	if a == b {
		t.Fatal("different workdirs should yield different instance ids")
	}
	if a != leadInstanceID("/home/user/repoA") {
		t.Fatal("instance id must be stable for the same workdir")
	}
	if !strings.HasPrefix(a, "ws-") {
		t.Fatalf("instance id %q should start with ws-", a)
	}
	// Short enough that the codex affinity key (id::harness::session) stays
	// well under the 64-char prompt_cache_key cap.
	if len(a) > 20 {
		t.Fatalf("instance id %q unexpectedly long", a)
	}
}
