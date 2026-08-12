package backends

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// TestDefaultClaudeInvoker_NoTTYFallsBackToNonInteractive pins the daemon-mode
// guard: without a controlling TTY (the `go test` process has none), the
// interactive invoker must route to the non-interactive path instead of
// launching the TUI against inherited pipes, where it renders nothing and the
// supervisor watchdog kills the silent run. Mirrors codex's guard; before the
// guard existed, custom daemon roles on the claude backend died this way.
func TestDefaultClaudeInvoker_NoTTYFallsBackToNonInteractive(t *testing.T) {
	called := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
		called = true
		if workDir != "/tmp/wd" || prompt != "do the thing" || agentName != "critic" {
			t.Fatalf("fallback got (%q, %q, %q)", workDir, prompt, agentName)
		}
		if shutdown == nil {
			t.Fatal("fallback must receive a non-nil shutdown channel")
		}
		return nil
	})

	if err := defaultClaudeInvoker("/tmp/wd", "do the thing", "critic"); err != nil {
		t.Fatalf("defaultClaudeInvoker() error = %v", err)
	}
	if !called {
		t.Fatal("expected the non-interactive fallback to be invoked when stdin is not a TTY")
	}
}
