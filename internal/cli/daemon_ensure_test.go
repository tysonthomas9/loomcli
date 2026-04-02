package cli

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureBdDaemonRunning(t *testing.T) {
	t.Parallel()

	t.Run("daemon already running", func(t *testing.T) {
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
	})

	t.Run("daemon not running, start succeeds, becomes ready", func(t *testing.T) {
		t.Parallel()
		deps, _, execR, _, _ := NewTestDeps(t)
		var statusCalls atomic.Int32

		execR.RunFunc = func(dir, name string, args ...string) CommandResult {
			if len(args) >= 2 && args[1] == "status" {
				n := statusCalls.Add(1)
				if n <= 1 {
					return CommandResult{Err: fmt.Errorf("not running")}
				}
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
	})

	t.Run("daemon not running, start fails", func(t *testing.T) {
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
	})

	t.Run("daemon not running, start succeeds, never becomes ready", func(t *testing.T) {
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
	})

	t.Run("status returns invalid JSON", func(t *testing.T) {
		t.Parallel()
		deps, _, execR, _, _ := NewTestDeps(t)
		var startCalled atomic.Bool
		execR.RunFunc = func(dir, name string, args ...string) CommandResult {
			if len(args) >= 2 && args[1] == "status" {
				return CommandResult{Stdout: "not json"}
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
			t.Error("expected start to be called when status returns invalid JSON")
		}
	})
}

func TestIsDaemonRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result CommandResult
		want   bool
	}{
		{
			name:   "running",
			result: CommandResult{Stdout: `{"status":"running","pid":123}`},
			want:   true,
		},
		{
			name:   "not running status",
			result: CommandResult{Stdout: `{"status":"stopped","pid":0}`},
			want:   false,
		},
		{
			name:   "command error",
			result: CommandResult{Err: fmt.Errorf("exit status 1")},
			want:   false,
		},
		{
			name:   "invalid json",
			result: CommandResult{Stdout: "garbage"},
			want:   false,
		},
		{
			name:   "empty stdout",
			result: CommandResult{},
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

func TestEnsureIssueBackendRunning(t *testing.T) {
	// Not parallel: subtests use t.Setenv which is incompatible with t.Parallel.

	t.Run("fleetdb returns false nil immediately", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")
		deps, _, _, _, _ := NewTestDeps(t)

		started, err := EnsureIssueBackendRunning(deps, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if started {
			t.Error("expected started=false for fleet-db backend")
		}
	})

	t.Run("fleetdb does not call execCommand", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")
		deps, _, execR, _, _ := NewTestDeps(t)
		execR.RunFunc = func(dir, name string, args ...string) CommandResult {
			t.Fatalf("execCommand should not be called for fleet-db backend: %s %v", name, args)
			return CommandResult{}
		}

		_, err := EnsureIssueBackendRunning(deps, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("beads delegates to EnsureBdDaemonRunning", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		deps, _, execR, _, _ := NewTestDeps(t)

		// Mock: daemon already running
		execR.RunFunc = func(dir, name string, args ...string) CommandResult {
			if len(args) >= 2 && args[1] == "status" {
				return CommandResult{
					Stdout: `{"status":"running","pid":9876}`,
				}
			}
			t.Fatalf("unexpected command: %s %v", name, args)
			return CommandResult{}
		}

		started, err := EnsureIssueBackendRunning(deps, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if started {
			t.Error("expected started=false when daemon already running")
		}
	})

	t.Run("beads with daemon not running delegates start", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		deps, _, execR, _, _ := NewTestDeps(t)

		var statusCalls atomic.Int32
		execR.RunFunc = func(dir, name string, args ...string) CommandResult {
			if len(args) >= 2 && args[1] == "status" {
				n := statusCalls.Add(1)
				if n <= 1 {
					return CommandResult{Err: fmt.Errorf("not running")}
				}
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

		started, err := EnsureIssueBackendRunning(deps, 2*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("expected started=true when we started the daemon via beads path")
		}
	})
}
