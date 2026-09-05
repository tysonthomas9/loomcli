package domain

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestWorkspaceFileRevisionMatchesFleetContract(t *testing.T) {
	t.Parallel()
	registry.MarkEvidence(t, 18, 31)
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
	second := WorkspaceFile{
		Path:        "b",
		BlobRef:     "blob_b",
		ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("b"))),
		SizeBytes:   1,
		MediaType:   "text/plain",
	}
	forward := WorkspaceFileTreeRevision([]WorkspaceFile{file, second})
	reversed := WorkspaceFileTreeRevision([]WorkspaceFile{second, file})
	if forward != reversed {
		t.Fatalf("manifest ordering changed tree revision: forward=%q reversed=%q", forward, reversed)
	}
}

func TestWorkspaceFilePathIsGeneric(t *testing.T) {
	t.Parallel()
	if err := ValidateWorkspaceFilePath("SKILL.md"); err != nil {
		t.Fatalf("generic workspace-file path rejected SKILL.md: %v", err)
	}
}
