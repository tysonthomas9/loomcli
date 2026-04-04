package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// mockBDRunner implements BDRunner for testing cliBeadsAdapter.
type mockBDRunner struct {
	calls []mockBDRunnerCall
	fn    func(dir string, args ...string) CommandResult
}

type mockBDRunnerCall struct {
	Dir  string
	Args []string
}

func (m *mockBDRunner) Run(dir string, args ...string) CommandResult {
	m.calls = append(m.calls, mockBDRunnerCall{Dir: dir, Args: args})
	if m.fn != nil {
		return m.fn(dir, args...)
	}
	return CommandResult{}
}

func TestCliBeadsAdapter_ClaimIssue_Success(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", 0)
	if err != nil {
		t.Fatalf("ClaimIssue() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	got := strings.Join(runner.calls[0].Args, " ")
	want := "update T-10 --claim"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCliBeadsAdapter_ClaimIssue_EmptyID(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not be called for empty ID")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindValidation)
	}
	if msg := err.Error(); !strings.Contains(msg, "id must not be empty") {
		t.Errorf("error message = %q, want it to contain %q", msg, "id must not be empty")
	}
}

func TestCliBeadsAdapter_ClaimIssue_NegativeTTL(t *testing.T) {
	runner := &mockBDRunner{}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", time.Duration(-1))
	if err == nil {
		t.Fatal("expected error for negative lockTTL")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not be called for negative TTL")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *backend.BackendError, got %T", err)
	}
	if be.Kind != backend.KindValidation {
		t.Errorf("BackendError.Kind = %q, want %q", be.Kind, backend.KindValidation)
	}
	if msg := err.Error(); !strings.Contains(msg, "lockTTL must not be negative") {
		t.Errorf("error message = %q, want it to contain %q", msg, "lockTTL must not be negative")
	}
}

func TestCliBeadsAdapter_ClaimIssue_RunnerError(t *testing.T) {
	runner := &mockBDRunner{
		fn: func(_ string, _ ...string) CommandResult {
			return CommandResult{
				Err:    fmt.Errorf("exit status 1"),
				Stderr: "already claimed by other-agent",
			}
		},
	}
	a := newCliBeadsAdapter(runner, "/tmp/test")

	err := a.ClaimIssue(context.Background(), "T-10", 0)
	if err == nil {
		t.Fatal("expected error for runner failure")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("error should contain 'already claimed', got %q", err.Error())
	}
}
