package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEnsureDaemonForWorkspace_ContextCancelled(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	// Mock: daemon start succeeds but status never reports running.
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := ensureDaemonForWorkspace(ctx, t.TempDir(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("error should wrap context.Canceled, got: %v", err)
	}
}

func TestEnsureDaemonForWorkspace_ContextDeadlineExceeded(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	// Mock: daemon start succeeds but status never reports running.
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Give the context time to expire before calling.
	time.Sleep(5 * time.Millisecond)

	err := ensureDaemonForWorkspace(ctx, t.TempDir(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error from expired context deadline")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention 'cancelled', got: %v", err)
	}
}

func TestEnsureDaemonForWorkspace_TimeoutFallback(t *testing.T) {
	oldExec := execCommand
	defer func() { execCommand = oldExec }()

	// Mock: daemon start succeeds but status never reports running.
	execCommand = func(dir, name string, args ...string) CommandResult {
		if len(args) >= 2 && args[1] == "start" {
			return CommandResult{}
		}
		if len(args) >= 2 && args[1] == "status" {
			return CommandResult{Err: fmt.Errorf("not running")}
		}
		return CommandResult{}
	}

	err := ensureDaemonForWorkspace(context.Background(), t.TempDir(), 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error should mention 'did not become ready', got: %v", err)
	}
}

func TestDefaultDaemonStartupTimeout(t *testing.T) {
	if defaultDaemonStartupTimeout != 30*time.Second {
		t.Errorf("defaultDaemonStartupTimeout = %v, want 30s", defaultDaemonStartupTimeout)
	}
}
