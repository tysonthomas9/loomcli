package backends

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/harness"
)

// wrapperRunFn is the seam tests use to swap harness.RunWithRetry for
// a fake. Production code initializes wrapperRun to harness.RunWithRetry;
// integration tests can replace it to inject scripted results without
// spawning real subprocesses.
type wrapperRunFn func(ctx context.Context, cfg wrapper.Config, p harness.RetryPolicy) (wrapper.Result, error)

var wrapperRun wrapperRunFn = harness.RunWithRetry

// harnessInvocation describes a single wrapper-based backend run.
type harnessInvocation struct {
	BinaryName  string // e.g. "claude", "codex" — used for lookup and error messages
	Args        []string
	WorkDir     string
	Env         []string
	Prompt      string
	HarnessName string // wrapper.Config.Harness; "" picks the generic classifier
	LineHandler func(string)
	RetryPolicy harness.RetryPolicy

	// Finalize, when non-nil, replaces the default Result→error
	// mapping. It receives the wrapper's terminal Result, any
	// wrapper-level err (PTY / binary lookup / config), and the
	// accumulated outputTail. Backends with custom failure semantics
	// (e.g. OpenCode surfacing a "type: error" stream event after a
	// nominally clean exit) implement this hook.
	Finalize func(res wrapper.Result, runErr error, outputTail string) error
}

// runHarness is the common driver for non-interactive backend
// invocations. It resolves the binary on PATH, spawns it under
// harness.RunWithRetry, pipes the wrapper's Stdout into the line
// handler scanner, and translates the terminal Result back into the
// InvocationError taxonomy via wrapWrapperResult.
//
// Returns nil on StatusIdle. Other statuses (including post-retry
// StatusRetryLater and StatusAPIError) map to *InvocationError;
// StatusInterrupted maps to a context.Canceled-wrapped error.
//
// shutdown is the legacy backend shutdown signal. Closing it cancels
// the context the wrapper observes; the wrapper sends SIGTERM and
// escalates to SIGKILL after its WaitDelay (default 5s).
func runHarness(parent context.Context, shutdown <-chan struct{}, inv harnessInvocation) error {
	binaryPath, err := exec.LookPath(inv.BinaryName)
	if err != nil {
		return wrapInvocationError(fmt.Errorf("%s not found on PATH: %w", inv.BinaryName, err), "")
	}

	ctx, cancel := contextFromShutdown(parent, shutdown)
	defer cancel()

	pr, pw := io.Pipe()
	scanDone := make(chan string, 1)
	go func() {
		defer pr.Close()
		scanDone <- scanStreamOutput(pr, inv.LineHandler)
	}()

	res, runErr := wrapperRun(ctx, wrapper.Config{
		BinaryPath: binaryPath,
		Args:       inv.Args,
		WorkingDir: inv.WorkDir,
		Env:        inv.Env,
		Stdin:      io.NopCloser(strings.NewReader(inv.Prompt)),
		Stdout:     pw,
		Harness:    inv.HarnessName,
	}, inv.RetryPolicy)
	_ = pw.Close()
	outputTail := <-scanDone

	if inv.Finalize != nil {
		return inv.Finalize(res, runErr, outputTail)
	}
	if runErr != nil {
		return wrapInvocationError(runErr, outputTail)
	}
	return wrapWrapperResult(res, outputTail)
}

// contextFromShutdown derives a cancellable child context that fires
// when either the parent context is cancelled or the legacy shutdown
// channel is closed. Returns the original parent's cancel func when
// shutdown is nil so the wrapper's lifecycle is unchanged.
func contextFromShutdown(parent context.Context, shutdown <-chan struct{}) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if shutdown == nil {
		return ctx, cancel
	}
	go func() {
		select {
		case <-shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
