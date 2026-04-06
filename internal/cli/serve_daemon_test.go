//go:build ignore

package cli

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestServeEnsureDaemon_AlreadyRunning(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{
				Stdout: `{"status":"running","pid":1234}`,
			}
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return CommandResult{}
	}

	started, err := EnsureBdDaemonRunning(deps, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started {
		t.Error("expected started=false when daemon already running")
	}
}

func TestServeEnsureDaemon_NotRunning_StartSucceeds(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	var statusCalls atomic.Int32

	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "status" {
			n := statusCalls.Add(1)
			if n <= 1 {
				// First status call: daemon not running
				return CommandResult{Err: fmt.Errorf("not running")}
			}
			// Subsequent status calls: daemon is now running
			return CommandResult{
				Stdout: `{"status":"running","pid":5678}`,
			}
		}
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return CommandResult{}
	}

	started, err := EnsureBdDaemonRunning(deps, 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !started {
		t.Error("expected started=true when we started the daemon")
	}
}

func TestServeEnsureDaemon_Timeout(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return CommandResult{}
	}

	started, err := EnsureBdDaemonRunning(deps, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if started {
		t.Error("expected started=false on timeout")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("expected timeout message, got: %v", err)
	}
}

func TestServeIsDaemonRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result CommandResult
		want   bool
	}{
		{
			name:   "running daemon returns true",
			result: CommandResult{Stdout: `{"status":"running","pid":123}`},
			want:   true,
		},
		{
			name:   "stopped daemon returns false",
			result: CommandResult{Stdout: `{"status":"stopped","pid":0}`},
			want:   false,
		},
		{
			name:   "command error returns false",
			result: CommandResult{Err: fmt.Errorf("exit status 1")},
			want:   false,
		},
		{
			name:   "invalid json returns false",
			result: CommandResult{Stdout: "not json"},
			want:   false,
		},
		{
			name:   "empty response returns false",
			result: CommandResult{},
			want:   false,
		},
		{
			name:   "unknown status returns false",
			result: CommandResult{Stdout: `{"status":"starting","pid":99}`},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, _, execR, _, _ := NewTestDeps(t)
			execR.RunFunc = func(dir, name string, args ...string) CommandResult {
				return tt.result
			}

			got := isDaemonRunning(deps)
			if got != tt.want {
				t.Errorf("isDaemonRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServeEnsureDaemon_StartFails(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{
				Stderr: "bd: command not found",
				Err:    fmt.Errorf("exit status 127"),
			}
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return CommandResult{}
	}

	started, err := EnsureBdDaemonRunning(deps, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when start fails")
	}
	if started {
		t.Error("expected started=false when start fails")
	}
	if !strings.Contains(err.Error(), "failed to start bd daemon") {
		t.Errorf("expected error to mention 'failed to start bd daemon', got: %v", err)
	}
}

func TestServeEnsureDaemon_StatusCallsExpectedCommand(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	var capturedName string
	var capturedArgs []string

	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		capturedName = name
		capturedArgs = args
		return CommandResult{
			Stdout: `{"status":"running","pid":999}`,
		}
	}

	_, _ = EnsureBdDaemonRunning(deps, 100*time.Millisecond)

	// isDaemonRunning should call "bd daemon status --json"
	if capturedName != "bd" {
		t.Errorf("expected command name = %q, got %q", "bd", capturedName)
	}
	expectedArgs := []string{"daemon", "status", "--json"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("args[%d] = %q, want %q", i, capturedArgs[i], arg)
		}
	}
}

func TestServeEnsureDaemon_InvalidJSON_TriggersStart(t *testing.T) {
	t.Parallel()
	deps, _, execR, _, _ := NewTestDeps(t)
	var startCalled atomic.Bool
	execR.RunFunc = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Stdout: "invalid json response"}
		}
		if len(args) >= 2 && args[1] == "start" {
			startCalled.Store(true)
			return CommandResult{Err: fmt.Errorf("fail")}
		}
		return CommandResult{}
	}

	_, err := EnsureBdDaemonRunning(deps, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when start fails")
	}
	if !startCalled.Load() {
		t.Error("expected start to be called when status returns invalid JSON (daemon treated as not running)")
	}
}
