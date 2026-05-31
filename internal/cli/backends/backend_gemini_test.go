package backends

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestGeminiBackendName(t *testing.T) {
	t.Parallel()
	b := &GeminiBackend{}
	if got := b.Name(); got != "gemini" {
		t.Errorf("expected 'gemini', got %q", got)
	}
}

func TestGeminiBackendInvokeNonInteractive(t *testing.T) {
	// Not parallel: mutates global geminiNonInteractiveInvoker.
	var called bool
	var gotShutdown <-chan struct{}
	installGeminiNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		if workDir != "/work" || prompt != "task" || agentName != "agent2" {
			t.Fatalf("non-interactive invocation = workDir=%q prompt=%q agent=%q", workDir, prompt, agentName)
		}
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &GeminiBackend{}
	err := b.InvokeNonInteractive("/work", "task", "agent2", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected geminiNonInteractiveInvoker to be called")
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestGeminiBackendInvokeNonInteractiveResumed(t *testing.T) {
	// Not parallel: mutates global geminiNonInteractiveResumedInvoker.
	var called bool
	var gotWorkDir, gotPrompt, gotAgentName, gotProviderSessionID string
	var gotShutdown <-chan struct{}
	installGeminiNonInteractiveResumedMock(t, func(workDir, prompt, agentName, providerSessionID string, shutdown <-chan struct{}, _ *usage.Collector) error {
		called = true
		gotWorkDir = workDir
		gotPrompt = prompt
		gotAgentName = agentName
		gotProviderSessionID = providerSessionID
		gotShutdown = shutdown
		return nil
	})

	shutdown := make(chan struct{})
	b := &GeminiBackend{}
	err := b.InvokeNonInteractiveResumed("/work", "follow-up task", "agent2", "gemini-session-123", shutdown, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected geminiNonInteractiveResumedInvoker to be called")
	}
	if gotWorkDir != "/work" || gotPrompt != "follow-up task" || gotAgentName != "agent2" || gotProviderSessionID != "gemini-session-123" {
		t.Fatalf("resumed invocation = workDir=%q prompt=%q agent=%q session=%q", gotWorkDir, gotPrompt, gotAgentName, gotProviderSessionID)
	}
	if gotShutdown == nil {
		t.Error("expected shutdown channel to be passed, got nil")
	}
}

func TestBuildGeminiNonInteractiveArgs(t *testing.T) {
	t.Parallel()
	got := buildGeminiNonInteractiveArgs("", "do the thing")
	want := []string{"--approval-mode=yolo", "-p", "do the thing", "-o", "stream-json"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("buildGeminiNonInteractiveArgs() = %+v, want %+v", got, want)
	}

	got = buildGeminiNonInteractiveArgs("gemini-session-123", "continue the thing")
	want = []string{"--approval-mode=yolo", "--resume", "gemini-session-123", "-p", "continue the thing", "-o", "stream-json"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("buildGeminiNonInteractiveArgs(resume) = %+v, want %+v", got, want)
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
