package skillse2e_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

var unsafeWorkspaceFilePaths = registry.Scenario{
	ID:       "unsafe-workspace-file-paths",
	Behavior: "unsafe provider-neutral paths are rejected before materialization",
	Owner:    registry.OwnerLoom,
	Seam:     registry.SeamLoomDomain,
	Cases: []registry.EdgeCase{{
		ID: 4, Behavior: "absolute, traversal, NUL, control, and unsafe separators are rejected",
		Rationale: "an exhaustive public domain table exercises every canonical unsafe path class",
	}},
}

func TestWorkspaceFilePathsRejectEveryUnsafeClass(t *testing.T) {
	unsafeWorkspaceFilePaths.Covers(t)
	paths := []string{
		"", "/absolute", "../outside", "nested/../outside", "nested/./file",
		"nested//file", "nested/", "nul\x00byte", "control\nbyte", `back\slash`,
		"~/home", "C:/drive", ":colon", "colon:name",
	}
	for _, filePath := range paths {
		t.Run(fmt.Sprintf("%q", filePath), func(t *testing.T) {
			err := domain.ValidateWorkspaceFilePath(filePath)
			if err == nil || !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("ValidateWorkspaceFilePath(%q) = %v, want ErrInvalid", filePath, err)
			}
		})
	}
}

var reservedWorkspaceFileNames = registry.Scenario{
	ID:       "reserved-workspace-file-names",
	Behavior: "platform-reserved device names are rejected before materialization",
	Owner:    registry.OwnerLoom,
	Seam:     registry.SeamLoomDomain,
	Cases: []registry.EdgeCase{{
		ID: 8, Behavior: "CON, NUL, COM1-9, and LPT1-9 are rejected case-insensitively with extensions",
		Rationale: "the public path validator is exercised across the complete reserved-name matrix",
	}},
}

func TestWorkspaceFilePathsRejectEveryReservedDeviceName(t *testing.T) {
	reservedWorkspaceFileNames.Covers(t)
	names := []string{"CON", "nul.txt"}
	for index := 1; index <= 9; index++ {
		names = append(names, fmt.Sprintf("CoM%d.log", index), fmt.Sprintf("lPt%d.bin", index))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			filePath := "nested/" + name
			err := domain.ValidateWorkspaceFilePath(filePath)
			if err == nil || !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("ValidateWorkspaceFilePath(%q) = %v, want ErrInvalid", filePath, err)
			}
		})
	}
}

var workspaceFileIdentityVectors = registry.Scenario{
	ID:       "workspace-file-identity-vectors",
	Behavior: "workspace-file identity distinguishes paths and materialization metadata without duplicating bytes",
	Owner:    registry.OwnerLoom,
	Seam:     registry.SeamLoomDomain,
	Cases: []registry.EdgeCase{
		{ID: 20, Behavior: "same bytes reused under different paths produce distinct tree identities", Rationale: "literal file and tree revision vectors hold blob identity fixed while changing only the path"},
		{ID: 21, Behavior: "same bytes with different media type and executable metadata produce distinct file and tree identities", Rationale: "literal revision vectors hold the blob, hash, size, and path fixed while changing materialization metadata"},
	},
}

func TestWorkspaceFileIdentityLiteralVectors(t *testing.T) {
	workspaceFileIdentityVectors.Covers(t)
	base := domain.WorkspaceFile{
		Path:        "docs/a.md",
		BlobRef:     "blob_shared_bytes",
		ContentHash: "sha256:ee392e7ce57b7406be2939363d0c2acfd7116af1a8085876355e605a342dfa13",
		SizeBytes:   13,
		MediaType:   "text/plain",
	}
	differentPath := base
	differentPath.Path = "docs/b.md"
	differentMetadata := base
	differentMetadata.MediaType = "application/octet-stream"
	differentMetadata.Executable = true

	vectors := []struct {
		name     string
		file     domain.WorkspaceFile
		wantFile string
		wantTree string
	}{
		{name: "base", file: base, wantFile: "wff1_5Q9_dQLOlcMLfDUGg1QIfg4oyPvTDOfb5Wr3wEtQpjs", wantTree: "wft1_TTaFMFgXKcvIV70-SCy0eroN5AMKfKPY9Z_oVC7frHA"},
		{name: "different path", file: differentPath, wantFile: "wff1_5Q9_dQLOlcMLfDUGg1QIfg4oyPvTDOfb5Wr3wEtQpjs", wantTree: "wft1_ELo5_RprP5-qsh6pASi3iXIeMnf-1V_uxvZTCAh7KnU"},
		{name: "different metadata", file: differentMetadata, wantFile: "wff1_gqg9dxzN4jgFEo3JNnxB7jUFJWils2eSvceauJRHm-M", wantTree: "wft1_B77I1rJfsdfibV0Z1Q2ohdpoF8egbcK0XoJZYOURu-Q"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			if got := domain.WorkspaceFileRevision(vector.file); got != vector.wantFile {
				t.Errorf("file revision = %q, want %q", got, vector.wantFile)
			}
			if got := domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{vector.file}); got != vector.wantTree {
				t.Errorf("tree revision = %q, want %q", got, vector.wantTree)
			}
		})
	}
	if got, want := domain.WorkspaceFileRevision(base), domain.WorkspaceFileRevision(differentPath); got != want {
		t.Fatalf("same bytes and metadata under different paths: revisions %q and %q", got, want)
	}
	if got, other := domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{base}), domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{differentPath}); got == other {
		t.Fatalf("different paths produced the same tree revision %q", got)
	}
	if got, other := domain.WorkspaceFileRevision(base), domain.WorkspaceFileRevision(differentMetadata); got == other {
		t.Fatalf("different materialization metadata produced the same file revision %q", got)
	}
	if got, other := domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{base}), domain.WorkspaceFileTreeRevision([]domain.WorkspaceFile{differentMetadata}); got == other {
		t.Fatalf("different materialization metadata produced the same tree revision %q", got)
	}
}
