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

func TestResolveLegacyPath_EmptyName(t *testing.T) {
	// Empty name should return cwd
	path, err := resolveLegacyPath("")
	if err != nil {
		t.Fatalf("resolveLegacyPath(%q) error = %v", "", err)
	}
	if path == "" {
		t.Error("resolveLegacyPath(\"\") should return non-empty path (cwd)")
	}
}

func TestResolveLegacyPath_AbsoluteExisting(t *testing.T) {
	tmpDir := t.TempDir()
	path, err := resolveLegacyPath(tmpDir)
	if err != nil {
		t.Fatalf("resolveLegacyPath(%q) error = %v", tmpDir, err)
	}
	if path != tmpDir {
		t.Errorf("resolveLegacyPath(%q) = %q, want %q", tmpDir, path, tmpDir)
	}
}

func TestResolveLegacyPath_AbsoluteNonExisting(t *testing.T) {
	_, err := resolveLegacyPath("/nonexistent/path/should/fail")
	if err == nil {
		t.Error("resolveLegacyPath for non-existing absolute path should error")
	}
}

func TestResolveLegacyPath_PathTraversal(t *testing.T) {
	_, err := resolveLegacyPath("../../../etc/passwd")
	if err == nil {
		t.Error("resolveLegacyPath with path traversal should error")
	}
}
