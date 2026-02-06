package cli

import "testing"

func TestCodexBackendName(t *testing.T) {
	b := &CodexBackend{}
	if got := b.Name(); got != "codex" {
		t.Errorf("expected 'codex', got %q", got)
	}
}

func TestCodexBackendRegistered(t *testing.T) {
	// After init(), the Codex backend should be registered
	backendMu.RLock()
	b, ok := backends["codex"]
	backendMu.RUnlock()

	if !ok {
		t.Fatal("expected 'codex' backend to be registered via init()")
	}
	if _, isCodexBackend := b.(*CodexBackend); !isCodexBackend {
		t.Fatalf("expected *CodexBackend, got %T", b)
	}
}
