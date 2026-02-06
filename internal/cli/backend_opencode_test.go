package cli

import "testing"

func TestOpenCodeBackendName(t *testing.T) {
	b := &OpenCodeBackend{}
	if got := b.Name(); got != "opencode" {
		t.Errorf("expected 'opencode', got %q", got)
	}
}

func TestOpenCodeBackendRegistered(t *testing.T) {
	// After init(), the OpenCode backend should be registered
	backendMu.RLock()
	b, ok := backends["opencode"]
	backendMu.RUnlock()

	if !ok {
		t.Fatal("expected 'opencode' backend to be registered via init()")
	}
	if _, isOpenCodeBackend := b.(*OpenCodeBackend); !isOpenCodeBackend {
		t.Fatalf("expected *OpenCodeBackend, got %T", b)
	}
}
