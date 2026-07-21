package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

type blockingContextGitRunner struct {
	canceled chan struct{}
}

func (runner *blockingContextGitRunner) Run(string, ...string) cli.CommandResult {
	return cli.CommandResult{Err: errors.New("non-context git execution is forbidden")}
}

func (runner *blockingContextGitRunner) RunContext(ctx context.Context, _ string, _ ...string) cli.CommandResult {
	<-ctx.Done()
	close(runner.canceled)
	return cli.CommandResult{Err: ctx.Err()}
}

func (runner *blockingContextGitRunner) RunWithOutput(string, ...string) error {
	return errors.New("streaming git execution is unsupported")
}

func TestRunMonitorGitCancelsTimedOutCommand(t *testing.T) {
	runner := &blockingContextGitRunner{canceled: make(chan struct{})}
	deps := &cli.Deps{Git: runner}

	_, err := runMonitorGitWithTimeout(deps, "/repo", time.Millisecond, "status")
	if err == nil || !strings.Contains(err.Error(), "timed out") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runMonitorGitWithTimeout() error = %v, want wrapped deadline error", err)
	}
	select {
	case <-runner.canceled:
	default:
		t.Fatal("context-aware git runner did not observe cancellation before return")
	}
}
