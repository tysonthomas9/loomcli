package cli

import (
	"testing"
)

func TestValidateWorktreeName_Valid(t *testing.T) {
	validNames := []string{"alpha", "my-worktree", "wt_123", "feature"}
	for _, name := range validNames {
		if err := validateWorktreeName(name); err != nil {
			t.Errorf("validateWorktreeName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateWorktreeName_PathTraversal(t *testing.T) {
	invalidNames := []string{"..", "../secrets", "../../etc"}
	for _, name := range invalidNames {
		if err := validateWorktreeName(name); err == nil {
			t.Errorf("validateWorktreeName(%q) = nil, want error", name)
		}
	}
}

func TestValidateWorktreeName_CurrentDir(t *testing.T) {
	// "." resolves to the worktrees parent directory itself, so it must be rejected
	if err := validateWorktreeName("."); err == nil {
		t.Errorf("validateWorktreeName(%q) = nil, want error", ".")
	}
}
