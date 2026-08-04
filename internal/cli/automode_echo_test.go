//go:build testbackend

package cli

import (
	"errors"
	"testing"
	"time"
)

// TestAutoMode_EchoBackend_SingleInvocation verifies a single non-interactive
// invocation through the echo backend is recorded correctly.
func TestAutoMode_EchoBackend_SingleInvocation(t *testing.T) {
	env := NewEchoTestEnv(t)

	if err := env.RunNonInteractive("test prompt"); err != nil {
		t.Fatalf("RunNonInteractive: %v", err)
	}

	env.AssertInvoked(1)
	env.AssertLastPromptContains("test prompt")
}

// TestAutoMode_EchoBackend_ErrorHandler verifies that when the echo backend's
// handler returns an error, InvokeAgentNonInteractive propagates it back, and
// the invocation is still recorded.
func TestAutoMode_EchoBackend_ErrorHandler(t *testing.T) {
	env := NewEchoTestEnv(t)
	expectedErr := errors.New("simulated agent failure")
	env.Backend.SetHandler(ErrorHandler(expectedErr))

	err := env.RunNonInteractive("error prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %q, got %q", expectedErr, err)
	}

	// Invocation is still recorded even on error.
	env.AssertInvoked(1)
}

// TestAutoMode_EchoBackend_UsageCollection verifies that the echo backend's
// UsageHandler emits token-usage events that are collected by the usage.Collector.
func TestAutoMode_EchoBackend_UsageCollection(t *testing.T) {
	env := NewEchoTestEnv(t)
	env.Backend.SetHandler(UsageHandler(1000, 500))

	if err := env.RunNonInteractive("usage prompt"); err != nil {
		t.Fatalf("RunNonInteractive: %v", err)
	}

	// Finalize the collector to extract accumulated usage.
	now := time.Now()
	su := env.Collector.Finalize("test-task", "test-epic", now, now, 0)

	if su.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", su.InputTokens)
	}
	if su.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", su.OutputTokens)
	}
}

// TestAutoMode_EchoBackend_SequenceHandler verifies that SequenceHandler cycles
// through multiple handlers across successive invocations.
func TestAutoMode_EchoBackend_SequenceHandler(t *testing.T) {
	env := NewEchoTestEnv(t)

	env.Backend.SetHandler(SequenceHandler(
		DefaultEchoHandler,
		UsageHandler(100, 50),
		DefaultEchoHandler,
	))

	for i := 0; i < 3; i++ {
		if err := env.RunNonInteractive("seq prompt"); err != nil {
			t.Fatalf("RunNonInteractive call %d: %v", i+1, err)
		}
	}

	env.AssertInvoked(3)
}
