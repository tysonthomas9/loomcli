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
