package cli

import (
	"testing"
	"time"
)

func TestEnsureIssueBackendRunningNoDaemon(t *testing.T) {
	t.Parallel()

	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		t.Fatalf("issue backend ensure should not shell out: %s %v", name, args)
		return CommandResult{}
	}

	started, err := EnsureIssueBackendRunning(deps, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started {
		t.Fatal("expected no issue backend daemon to start")
	}
}
