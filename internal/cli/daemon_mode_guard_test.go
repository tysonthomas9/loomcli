package cli_test

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// recordingBackend reports which invoker was reached.
type recordingBackend struct {
	interactive    int
	nonInteractive int
}

func (b *recordingBackend) Name() string { return "recording" }

func (b *recordingBackend) InvokeInteractive(_, _, _ string) error {
	b.interactive++
	return nil
}

func (b *recordingBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	b.nonInteractive++
	return nil
}

func withRecordingBackend(t *testing.T) *recordingBackend {
	t.Helper()
	cli.TestingResetBackendState(t)
	b := &recordingBackend{}
	cli.RegisterBackend(b)
	if err := cli.SetBackend(b.Name()); err != nil {
		t.Fatalf("SetBackend: %v", err)
	}
	cli.SetDaemonMode(false)
	t.Cleanup(func() { cli.SetDaemonMode(false) })
	return b
}

// A daemon-supervised process has no controlling TTY, so an interactive
// backend renders its TUI into nothing and the process exits 0 having done no
// work. The supervisor reads that as a clean run, fires completion hooks, and
// advances the pipeline on a phantom. Refusing here is what turns that into a
// visible failure at the first spawn.
func TestInvokeAgent_RefusedInDaemonMode(t *testing.T) {
	b := withRecordingBackend(t)
	cli.SetDaemonMode(true)

	err := cli.InvokeAgent("/tmp", "prompt", "agent")
	if err == nil {
		t.Fatal("interactive invocation must be refused under --daemon-mode")
	}
	if !errors.Is(err, cli.ErrInteractiveInDaemonMode) {
		t.Fatalf("err = %v, want ErrInteractiveInDaemonMode", err)
	}
	if b.interactive != 0 {
		t.Fatalf("the backend must never be reached; interactive calls = %d", b.interactive)
	}
}

// The refusal is specific to the interactive path — the non-interactive
// invoker is the one daemon agents are supposed to take, and it must keep
// working under the same flag.
func TestInvokeAgentNonInteractive_AllowedInDaemonMode(t *testing.T) {
	b := withRecordingBackend(t)
	cli.SetDaemonMode(true)

	if err := cli.InvokeAgentNonInteractive("/tmp", "prompt", "agent", nil, nil); err != nil {
		t.Fatalf("non-interactive invocation must still run in daemon mode: %v", err)
	}
	if b.nonInteractive != 1 {
		t.Fatalf("non-interactive calls = %d, want 1", b.nonInteractive)
	}
}

// Outside daemon mode nothing changes: `loom lead` and other terminal agents
// keep their interactive session.
func TestInvokeAgent_UnchangedOutsideDaemonMode(t *testing.T) {
	b := withRecordingBackend(t)

	if err := cli.InvokeAgent("/tmp", "prompt", "agent"); err != nil {
		t.Fatalf("interactive invocation outside daemon mode: %v", err)
	}
	if b.interactive != 1 {
		t.Fatalf("interactive calls = %d, want 1", b.interactive)
	}
	if cli.InDaemonMode() {
		t.Fatal("InDaemonMode must stay false when nothing set it")
	}
}
