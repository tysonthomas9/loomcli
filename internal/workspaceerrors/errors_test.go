package workspaceerrors_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

func TestCodeString(t *testing.T) {
	tests := []struct {
		code workspaceerrors.Code
		want string
	}{
		{workspaceerrors.AlreadyExists, "AlreadyExists"},
		{workspaceerrors.PathNotFound, "PathNotFound"},
		{workspaceerrors.NotGitRepo, "NotGitRepo"},
		{workspaceerrors.GitFailed, "GitFailed"},
		{workspaceerrors.ConfigFailed, "ConfigFailed"},
		{workspaceerrors.SecurityViolation, "SecurityViolation"},
		{workspaceerrors.Code(99), "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.code.String(); got != tt.want {
				t.Errorf("Code(%d).String() = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestCodeDistinctValues(t *testing.T) {
	codes := []workspaceerrors.Code{
		workspaceerrors.AlreadyExists,
		workspaceerrors.PathNotFound,
		workspaceerrors.NotGitRepo,
		workspaceerrors.GitFailed,
		workspaceerrors.ConfigFailed,
		workspaceerrors.SecurityViolation,
	}
	seen := make(map[workspaceerrors.Code]bool)
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate iota value %d for code %s", c, c)
		}
		seen[c] = true
	}
}

func TestNewWithNilCause(t *testing.T) {
	err := workspaceerrors.New(workspaceerrors.AlreadyExists, "workspace foo exists", nil)
	if err.Code != workspaceerrors.AlreadyExists {
		t.Errorf("Code = %v, want AlreadyExists", err.Code)
	}
	if err.Message != "workspace foo exists" {
		t.Errorf("Message = %q, want %q", err.Message, "workspace foo exists")
	}
	if err.Cause != nil {
		t.Errorf("Cause = %v, want nil", err.Cause)
	}
}

func TestNewWithCause(t *testing.T) {
	cause := fmt.Errorf("permission denied")
	err := workspaceerrors.New(workspaceerrors.PathNotFound, "cannot access /tmp/ws", cause)
	if err.Code != workspaceerrors.PathNotFound {
		t.Errorf("Code = %v, want PathNotFound", err.Code)
	}
	if err.Cause != cause {
		t.Errorf("Cause = %v, want %v", err.Cause, cause)
	}
}

func TestErrorFormatWithoutCause(t *testing.T) {
	err := workspaceerrors.New(workspaceerrors.NotGitRepo, "not a repo", nil)
	want := "workspaceerrors [NotGitRepo]: not a repo"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorFormatWithCause(t *testing.T) {
	cause := fmt.Errorf("exit status 128")
	err := workspaceerrors.New(workspaceerrors.GitFailed, "clone failed", cause)
	want := "workspaceerrors [GitFailed]: clone failed: exit status 128"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestUnwrapNilCause(t *testing.T) {
	err := workspaceerrors.New(workspaceerrors.ConfigFailed, "write failed", nil)
	if got := err.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestUnwrapNonNilCause(t *testing.T) {
	cause := fmt.Errorf("disk full")
	err := workspaceerrors.New(workspaceerrors.ConfigFailed, "write failed", cause)
	if got := err.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestErrorsAs(t *testing.T) {
	orig := workspaceerrors.New(workspaceerrors.SecurityViolation, "path escape", nil)
	wrapped := fmt.Errorf("create workspace: %w", orig)

	var target *workspaceerrors.CreateError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As failed to find *CreateError through fmt.Errorf wrap")
	}
	if target.Code != workspaceerrors.SecurityViolation {
		t.Errorf("target.Code = %v, want SecurityViolation", target.Code)
	}
}

func TestErrorsIsWithSentinel(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := workspaceerrors.New(workspaceerrors.GitFailed, "op failed", sentinel)
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is failed to find sentinel through Unwrap chain")
	}
}

func TestErrorsIsNilCause(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := workspaceerrors.New(workspaceerrors.GitFailed, "op failed", nil)
	if errors.Is(err, sentinel) {
		t.Error("errors.Is should return false when cause is nil")
	}
}

func TestErrorInterfaceSatisfaction(t *testing.T) {
	var _ error = (*workspaceerrors.CreateError)(nil)
	var _ error = workspaceerrors.New(workspaceerrors.AlreadyExists, "test", nil)
}
