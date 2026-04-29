package workspacemgr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// captureSlogForRun swaps the default slog logger for a text handler writing
// to a buffer for the duration of fn, then restores the original. Returns the
// captured log output. (Local copy — the one in workspaceinfo_test.go has the
// same shape but lives in a different test file; redeclaring as a separate
// helper keeps these tests self-contained.)
func captureSlogForRun(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(orig)
	fn()
	return buf.String()
}

// setBdSyncTimeout temporarily overrides the package-level bdSyncTimeout for
// the duration of the test. Cleanup restores the original value.
func setBdSyncTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := bdSyncTimeout
	bdSyncTimeout = d
	t.Cleanup(func() { bdSyncTimeout = orig })
}

// newDepsWithExecCtx returns a *cli.Deps whose ExecCtx field is the supplied
// mock. Other fields are left at their zero values; runBdRepoSync only reads
// deps.ExecCtx so the rest is irrelevant.
func newDepsWithExecCtx(mock *clitest.MockExecContextRunner) *cli.Deps {
	return &cli.Deps{ExecCtx: mock}
}

func TestRunBdRepoSync_Success(t *testing.T) {
	mock := &clitest.MockExecContextRunner{
		Result: cli.CommandResult{Err: nil},
	}
	deps := newDepsWithExecCtx(mock)

	logOutput := captureSlogForRun(t, func() {
		runBdRepoSync(deps, "alpha", "/tmp/wsdir")
	})

	if len(mock.Calls) != 1 {
		t.Fatalf("expected exactly 1 ExecCtx.Run call, got %d", len(mock.Calls))
	}
	call := mock.Calls[0]
	if call.Name != "bd" {
		t.Errorf("call.Name = %q, want %q", call.Name, "bd")
	}
	if call.Dir != "/tmp/wsdir" {
		t.Errorf("call.Dir = %q, want %q", call.Dir, "/tmp/wsdir")
	}
	wantArgs := []string{"repo", "sync"}
	if !clitest.SlicesEqual(call.Args, wantArgs) {
		t.Errorf("call.Args = %v, want %v", call.Args, wantArgs)
	}

	if strings.Contains(logOutput, "bd repo sync failed") {
		t.Errorf("expected no 'bd repo sync failed' log on success, got: %s", logOutput)
	}
}

func TestRunBdRepoSync_NonZeroExit(t *testing.T) {
	mock := &clitest.MockExecContextRunner{
		Result: cli.CommandResult{Err: errors.New("exit status 1")},
	}
	deps := newDepsWithExecCtx(mock)

	logOutput := captureSlogForRun(t, func() {
		runBdRepoSync(deps, "alpha", "/tmp/wsdir")
	})

	if len(mock.Calls) != 1 {
		t.Fatalf("expected exactly 1 ExecCtx.Run call, got %d", len(mock.Calls))
	}

	if !strings.Contains(logOutput, "bd repo sync failed") {
		t.Errorf("expected log containing 'bd repo sync failed', got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected WARN level log line, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "timeout=") {
		t.Errorf("expected log to include 'timeout=' field for operator correlation, got: %s", logOutput)
	}
	// Count exactly one occurrence of the failure message.
	if got := strings.Count(logOutput, "bd repo sync failed"); got != 1 {
		t.Errorf("expected exactly 1 'bd repo sync failed' log line, got %d: %s", got, logOutput)
	}
}

func TestRunBdRepoSync_Timeout(t *testing.T) {
	// Override the package-level timeout to something short so the test runs
	// quickly. setBdSyncTimeout restores the production value via t.Cleanup.
	setBdSyncTimeout(t, 100*time.Millisecond)

	mock := &clitest.MockExecContextRunner{
		RunFunc: func(ctx context.Context, dir, name string, args ...string) cli.CommandResult {
			<-ctx.Done()
			return cli.CommandResult{Err: ctx.Err()}
		},
	}
	deps := newDepsWithExecCtx(mock)

	done := make(chan string, 1)
	start := time.Now()
	go func() {
		out := captureSlogForRun(t, func() {
			runBdRepoSync(deps, "alpha", "/tmp/wsdir")
		})
		done <- out
	}()

	var logOutput string
	select {
	case logOutput = <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("runBdRepoSync did not return within 1s; elapsed=%s", time.Since(start))
	}

	if !strings.Contains(logOutput, "bd repo sync failed") {
		t.Errorf("expected log containing 'bd repo sync failed' on timeout, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "timeout=") {
		t.Errorf("expected log to include 'timeout=' field on timeout path (AC#2), got: %s", logOutput)
	}
}

func TestRunBdRepoSync_DeadlineOnContext(t *testing.T) {
	var (
		mu          sync.Mutex
		gotDeadline time.Time
		gotOK       bool
	)

	mock := &clitest.MockExecContextRunner{
		RunFunc: func(ctx context.Context, dir, name string, args ...string) cli.CommandResult {
			d, ok := ctx.Deadline()
			mu.Lock()
			gotDeadline = d
			gotOK = ok
			mu.Unlock()
			return cli.CommandResult{Err: nil}
		},
	}
	deps := newDepsWithExecCtx(mock)

	before := time.Now()
	runBdRepoSync(deps, "alpha", "/tmp/wsdir")
	after := time.Now()

	mu.Lock()
	defer mu.Unlock()

	if !gotOK {
		t.Fatal("expected ctx.Deadline() ok=true, got false")
	}

	// At the moment of the call (somewhere between `before` and `after`),
	// the deadline should be approximately bdSyncTimeout in the future.
	// Allow ±1s slack.
	earliestExpected := before.Add(bdSyncTimeout).Add(-1 * time.Second)
	latestExpected := after.Add(bdSyncTimeout).Add(1 * time.Second)
	if gotDeadline.Before(earliestExpected) || gotDeadline.After(latestExpected) {
		t.Errorf("deadline = %s, want within [%s, %s] (bdSyncTimeout = %s)",
			gotDeadline, earliestExpected, latestExpected, bdSyncTimeout)
	}
}

func TestRunBdRepoSync_CancelReleasesContext(t *testing.T) {
	var (
		mu       sync.Mutex
		captured context.Context //nolint:containedctx // captured for assertion only
	)

	mock := &clitest.MockExecContextRunner{
		RunFunc: func(ctx context.Context, dir, name string, args ...string) cli.CommandResult {
			mu.Lock()
			captured = ctx
			mu.Unlock()
			return cli.CommandResult{Err: nil}
		},
	}
	deps := newDepsWithExecCtx(mock)

	runBdRepoSync(deps, "alpha", "/tmp/wsdir")

	mu.Lock()
	ctx := captured
	mu.Unlock()

	if ctx == nil {
		t.Fatal("expected captured context, got nil")
	}

	// After runBdRepoSync returns, defer cancel() should have closed Done().
	select {
	case <-ctx.Done():
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected ctx.Done() to be closed after runBdRepoSync returns (deferred cancel)")
	}
}

func TestBdSyncTimeoutValue(t *testing.T) {
	if bdSyncTimeout != 60*time.Second {
		t.Errorf("bdSyncTimeout = %s, want %s (locks production value against accidental change)",
			bdSyncTimeout, 60*time.Second)
	}
}
