package domain

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestWorkspaceFileRevisionMatchesFleetContract(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("a"))
	file := WorkspaceFile{
		Path:        "a",
		BlobRef:     "blob_a",
		ContentHash: fmt.Sprintf("sha256:%x", sum),
		SizeBytes:   1,
		MediaType:   "application/octet-stream",
	}
	if got, want := WorkspaceFileRevision(file), "wff1_chyzldiVKHA_D325FYBkALYCVRlzaFT78-eUBmyAh34"; got != want {
		t.Fatalf("file revision = %q, want %q", got, want)
	}
	if got, want := WorkspaceFileTreeRevision([]WorkspaceFile{file}), "wft1_HFNKQjMCiWc3gGwoosocPTj84xuxSO9ZYc2n14Hcg2E"; got != want {
		t.Fatalf("tree revision = %q, want %q", got, want)
	}
}

func TestWorkspaceFilePathIsGeneric(t *testing.T) {
	t.Parallel()
	if err := ValidateWorkspaceFilePath("SKILL.md"); err != nil {
		t.Fatalf("generic workspace-file path rejected SKILL.md: %v", err)
	}
}
