package backends

import (
	"context"
	"io"
	"strings"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/harness"
)

// wrapperRunFn is the seam tests use to swap harness.RunWithRetry for
// a fake. Production code initializes wrapperRun to harness.RunWithRetry;
// integration tests can replace it to inject scripted results without
// spawning real subprocesses.
type wrapperRunFn func(ctx context.Context, cfg hwharness.Config, p harness.RetryPolicy) (hwharness.Result, error)

var wrapperRun wrapperRunFn = harness.RunWithRetry

// harnessInvocation describes a single wrapper-based backend run.
type harnessInvocation struct {
	BinaryName  string // e.g. "claude", "codex" — used for lookup and error messages
	Args        []string
	WorkDir     string
	Env         []string
	Prompt      string
	HarnessName string // wrapper.Config.Harness; "" picks the generic classifier
	Effort      string // wrapper.Config.Effort; "" leaves the harness default
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
// invocations. It spawns the configured binary under
// harness.RunWithRetry, pipes the wrapper's Stdout into the line
// handler scanner, and translates the terminal Result back into the
// InvocationError taxonomy via wrapWrapperResult.
//
// The wrapper owns binary resolution: BinaryPath is passed through as
// inv.BinaryName and the wrapper surfaces missing-binary cases as
// wrapper.StatusBinaryNotFound / wrapper.ErrBinaryNotFound, which
// wrapWrapperResult / wrapInvocationError translate into a
// "backend binary not on PATH" InvocationError that agenterr
// classifies as BackendUnavailable (fixes LOOM-4).
//
// Returns nil on StatusIdle. Other statuses (including post-retry
// StatusRetryLater and StatusAPIError) map to *InvocationError;
// StatusInterrupted maps to a context.Canceled-wrapped error.
//
// shutdown is the legacy backend shutdown signal. Closing it cancels
// the context the wrapper observes; the wrapper sends SIGTERM and
// escalates to SIGKILL after its WaitDelay (default 5s).
func runHarness(parent context.Context, shutdown <-chan struct{}, inv harnessInvocation) error {
	ctx, cancel := contextFromShutdown(parent, shutdown)
	defer cancel()

	pr, pw := io.Pipe()
	scanDone := make(chan string, 1)
	go func() {
		defer pr.Close()
		scanDone <- scanStreamOutput(pr, inv.LineHandler)
	}()

	// Auto-attach the daemon activity observer when the caller hasn't set
	// one explicitly. In standalone mode (no daemon socket) this is a
	// no-op; under daemon supervision it ticks wrapper.Snapshot.LastOutputAt
	// over IPC so the daemon can surface per-agent liveness in the UI.
	if inv.RetryPolicy.OnActivity == nil {
		inv.RetryPolicy.OnActivity = nil
	}

	hwCfg := hwharness.Config{
		Wrapper: wrapper.Config{
			BinaryPath: inv.BinaryName,
			Args:       inv.Args,
			WorkingDir: inv.WorkDir,
			Env:        inv.Env,
			Stdin:      io.NopCloser(strings.NewReader(inv.Prompt)),
			Stdout:     pw,
			Harness:    inv.HarnessName,
			Effort:     inv.Effort,
		},
	}
	res, runErr := wrapperRun(ctx, hwCfg, inv.RetryPolicy)
	_ = pw.Close()
	outputTail := <-scanDone

	// res.Result is the embedded wrapper.Result; the result-mapping helpers below
	// keep their wrapper.Result contract.
	if inv.Finalize != nil {
		return inv.Finalize(res.Result, runErr, outputTail)
	}
	if runErr != nil {
		return wrapInvocationError(runErr, outputTail)
	}
	return wrapWrapperResult(res.Result, outputTail)
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
