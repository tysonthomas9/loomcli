package serve

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestServeFlags_NoDaemon(t *testing.T) {
	t.Parallel()
	f := serveCmd.Flags().Lookup("no-daemon")
	if f == nil {
		t.Fatal("no-daemon flag not registered on serveCmd")
	}

	if f.DefValue != "false" {
		t.Errorf("no-daemon DefValue = %q, want %q", f.DefValue, "false")
	}

	if f.Value.Type() != "bool" {
		t.Errorf("no-daemon type = %q, want %q", f.Value.Type(), "bool")
	}
}

func TestServeFlags_NoDaemon_Default(t *testing.T) {
	t.Parallel()
	// Verify the no-daemon flag has the correct default via the Flags() API
	f := serveCmd.Flags()

	noDaemon, err := f.GetBool("no-daemon")
	if err != nil {
		t.Fatalf("failed to get no-daemon flag: %v", err)
	}
	if noDaemon != false {
		t.Errorf("no-daemon default = %v, want %v", noDaemon, false)
	}
}

func TestServeIssueBackendEnsureDoesNotShellOut(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		t.Fatalf("serve issue backend ensure should not shell out: %s %v", name, args)
		return CommandResult{}
	}

	started, err := cli.EnsureIssueBackendRunning(deps, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started {
		t.Fatal("expected no issue backend daemon to start")
	}
}
