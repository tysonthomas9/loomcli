package skillse2e_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var generatedWorkspaceFileContract = registry.Scenario{
	ID:       "generated-workspace-file-contract",
	Behavior: "Fleet's generated contract owns every shared limit and both revision algorithms",
	Cases:    []registry.EdgeCase{{ID: 16}, {ID: 32}},
}

func TestGeneratedWorkspaceFileContractOwnsLimitsAndRevisionEncoding(t *testing.T) {
	generatedWorkspaceFileContract.Covers(t)
	registry.MarkEvidence(t, 16, 32)
	if got, want := domain.WorkspaceFileContractSourceSHA256, "d76f2e909edcce2c036a1c0247e67ad1903033dc8989163e6db67daf44901914"; got != want {
		t.Fatalf("generated source digest = %q, want reviewed Fleet contract %q", got, want)
	}

	limits := map[string]int{
		"workspace files": domain.MaxWorkspaceFiles, "path bytes": domain.MaxWorkspaceFilePathLength,
		"path segment bytes": domain.MaxWorkspaceFilePathSegmentLength,
		"blob ref bytes":     domain.MaxWorkspaceFileBlobRefLength, "media type bytes": domain.MaxWorkspaceFileMediaTypeLength,
		"skill name characters": domain.MaxSkillNameLength, "skill description characters": domain.MaxSkillDescriptionCharacters,
		"skill document bytes": domain.MaxSkillContentBytes, "skill bundled files": domain.MaxSkillFiles,
		"skill file bytes": domain.MaxSkillFileBytes, "skill aggregate bytes": domain.MaxSkillFilesTotalBytes,
		"skill provenance bytes": domain.MaxSkillProvenanceLength,
	}
	want := map[string]int{
		"workspace files": 257, "path bytes": 256, "path segment bytes": 255,
		"blob ref bytes": 256, "media type bytes": 255, "skill name characters": 64,
		"skill description characters": 1024, "skill document bytes": 100000,
		"skill bundled files": 256, "skill file bytes": 131072,
		"skill aggregate bytes": 1000000, "skill provenance bytes": 256,
	}
	for name, value := range limits {
		if value != want[name] {
			t.Errorf("%s = %d, want %d", name, value, want[name])
		}
	}

	file := domain.WorkspaceFile{
		Path: "docs/a.md", BlobRef: "blob_shared_bytes",
		ContentHash: "sha256:ee392e7ce57b7406be2939363d0c2acfd7116af1a8085876355e605a342dfa13",
		SizeBytes:   13, MediaType: "text/plain",
	}
	if got, want := domain.WorkspaceFileRevision(file), "wff1_5Q9_dQLOlcMLfDUGg1QIfg4oyPvTDOfb5Wr3wEtQpjs"; got != want {
		t.Errorf("file revision = %q, want literal contract vector %q", got, want)
	}
	if got, want := domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{file}), "wft1_TTaFMFgXKcvIV70-SCy0eroN5AMKfKPY9Z_oVC7frHA"; got != want {
		t.Errorf("tree revision = %q, want literal contract vector %q", got, want)
	}
}
