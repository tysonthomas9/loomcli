package workspace_test

import (
	"errors"
	"fmt"
	"testing"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

func TestCodeString(t *testing.T) {
	tests := []struct {
		code workspacemodule.Code
		want string
	}{
		{workspacemodule.AlreadyExists, "AlreadyExists"},
		{workspacemodule.PathNotFound, "PathNotFound"},
		{workspacemodule.NotGitRepo, "NotGitRepo"},
		{workspacemodule.GitFailed, "GitFailed"},
		{workspacemodule.ConfigFailed, "ConfigFailed"},
		{workspacemodule.SecurityViolation, "SecurityViolation"},
		{workspacemodule.Code(99), "Unknown"},
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
	codes := []workspacemodule.Code{
		workspacemodule.AlreadyExists,
		workspacemodule.PathNotFound,
		workspacemodule.NotGitRepo,
		workspacemodule.GitFailed,
		workspacemodule.ConfigFailed,
		workspacemodule.SecurityViolation,
	}
	seen := make(map[workspacemodule.Code]bool)
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate iota value %d for code %s", c, c)
		}
		seen[c] = true
	}
}

func TestNewCreateErrorWithNilCause(t *testing.T) {
	err := workspacemodule.NewCreateError(workspacemodule.AlreadyExists, "workspace foo exists", nil)
	if err.Code != workspacemodule.AlreadyExists {
		t.Errorf("Code = %v, want AlreadyExists", err.Code)
	}
	if err.Message != "workspace foo exists" {
		t.Errorf("Message = %q, want %q", err.Message, "workspace foo exists")
	}
	if err.Cause != nil {
		t.Errorf("Cause = %v, want nil", err.Cause)
	}
}

func TestNewCreateErrorWithCause(t *testing.T) {
	cause := fmt.Errorf("permission denied")
	err := workspacemodule.NewCreateError(workspacemodule.PathNotFound, "cannot access /tmp/ws", cause)
	if err.Code != workspacemodule.PathNotFound {
		t.Errorf("Code = %v, want PathNotFound", err.Code)
	}
	if err.Cause != cause {
		t.Errorf("Cause = %v, want %v", err.Cause, cause)
	}
}

func TestErrorFormatWithoutCause(t *testing.T) {
	err := workspacemodule.NewCreateError(workspacemodule.NotGitRepo, "not a repo", nil)
	want := "workspaceerrors [NotGitRepo]: not a repo"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorFormatWithCause(t *testing.T) {
	cause := fmt.Errorf("exit status 128")
	err := workspacemodule.NewCreateError(workspacemodule.GitFailed, "clone failed", cause)
	want := "workspaceerrors [GitFailed]: clone failed: exit status 128"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestUnwrapNilCause(t *testing.T) {
	err := workspacemodule.NewCreateError(workspacemodule.ConfigFailed, "write failed", nil)
	if got := err.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
}

func TestUnwrapNonNilCause(t *testing.T) {
	cause := fmt.Errorf("disk full")
	err := workspacemodule.NewCreateError(workspacemodule.ConfigFailed, "write failed", cause)
	if got := err.Unwrap(); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestErrorsAs(t *testing.T) {
	orig := workspacemodule.NewCreateError(workspacemodule.SecurityViolation, "path escape", nil)
	wrapped := fmt.Errorf("create workspace: %w", orig)

	var target *workspacemodule.CreateError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As failed to find *CreateError through fmt.Errorf wrap")
	}
	if target.Code != workspacemodule.SecurityViolation {
		t.Errorf("target.Code = %v, want SecurityViolation", target.Code)
	}
}

func TestErrorsIsWithSentinel(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := workspacemodule.NewCreateError(workspacemodule.GitFailed, "op failed", sentinel)
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is failed to find sentinel through Unwrap chain")
	}
}

func TestErrorsIsNilCause(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := workspacemodule.NewCreateError(workspacemodule.GitFailed, "op failed", nil)
	if errors.Is(err, sentinel) {
		t.Error("errors.Is should return false when cause is nil")
	}
}

func TestErrorInterfaceSatisfaction(t *testing.T) {
	var _ error = (*workspacemodule.CreateError)(nil)
	var _ error = workspacemodule.NewCreateError(workspacemodule.AlreadyExists, "test", nil)
}
