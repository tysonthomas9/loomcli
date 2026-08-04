//go:build testbackend

package cli

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// EchoTestEnv provides a self-contained test environment that uses the
// EchoBackend for integration testing without spawning real agent processes.
type EchoTestEnv struct {
	T         *testing.T
	WorkDir   string
	Backend   *EchoBackend
	shutdown  chan struct{}
	Collector *usage.Collector
}

// NewEchoTestEnv creates and returns a configured EchoTestEnv.
// It sets LOOM_BACKEND=echo, retrieves the registered echo backend,
// resets it, and creates a temporary working directory.
func NewEchoTestEnv(t *testing.T) *EchoTestEnv {
	t.Helper()

	t.Setenv("LOOM_BACKEND", "echo")
	if err := SetBackend("echo"); err != nil {
		t.Fatalf("SetBackend(echo): %v", err)
	}

	b, ok := GetBackendByName("echo")
	if !ok {
		t.Fatal("echo backend not registered; build with -tags testbackend")
	}

	echo, ok := b.(*EchoBackend)
	if !ok {
		t.Fatalf("echo backend has unexpected type %T", b)
	}
	echo.Reset()

	return &EchoTestEnv{
		T:        t,
		WorkDir:  t.TempDir(),
		Backend:  echo,
		shutdown: make(chan struct{}),
	}
}

// RunNonInteractive invokes the echo backend through InvokeAgentNonInteractive,
// creating a fresh usage.Collector for each call. The collector is stored on
// the env for later inspection.
func (e *EchoTestEnv) RunNonInteractive(prompt string) error {
	e.T.Helper()
	e.Collector = usage.NewCollector("echo", "test-agent")
	return InvokeAgentNonInteractive(e.WorkDir, prompt, "test-agent", e.shutdown, e.Collector)
}

// AssertInvoked checks that the backend was invoked exactly n times.
func (e *EchoTestEnv) AssertInvoked(n int) {
	e.T.Helper()
	got := len(e.Backend.Invocations())
	if got != n {
		e.T.Errorf("expected %d invocations, got %d", n, got)
	}
}

// AssertLastPromptContains checks that the last invocation's Prompt contains substr.
func (e *EchoTestEnv) AssertLastPromptContains(substr string) {
	e.T.Helper()
	invs := e.Backend.Invocations()
	if len(invs) == 0 {
		e.T.Fatal("no invocations recorded")
	}
	last := invs[len(invs)-1]
	if !strings.Contains(last.Prompt, substr) {
		e.T.Errorf("last prompt %q does not contain %q", last.Prompt, substr)
	}
}
