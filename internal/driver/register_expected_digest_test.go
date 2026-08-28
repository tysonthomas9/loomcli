//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func expectedDigestRegistration(t *testing.T, root, expected string) (*RegisterFlueResult, error) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	return RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey:           "TEST",
		WorkDir:                root,
		DistPath:               "dist",
		DriverName:             "epic-runner",
		Activate:               true,
		CreatedBy:              "tester",
		ExpectedArtifactDigest: expected,
	})
}

// The packaged lane verifies the dist before registering; the digest it
// verified must be the digest of what gets staged.
func TestRegisterFlueDriverExpectedArtifactDigestMatches(t *testing.T) {
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")
	want, err := DigestDirectory(filepath.Join(root, "dist"))
	if err != nil {
		t.Fatalf("DigestDirectory: %v", err)
	}

	result, err := expectedDigestRegistration(t, root, want)
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if result.Version.Manifest["artifact_digest"] != want {
		t.Fatalf("artifact_digest = %q, want %q", result.Version.Manifest["artifact_digest"], want)
	}
}

func TestRegisterFlueDriverExpectedArtifactDigestMismatchFailsAndStagesNothing(t *testing.T) {
	root := t.TempDir()
	writeFlueDist(t, root, "epic-runner", "one")

	_, err := expectedDigestRegistration(t, root, "sha256:"+strings.Repeat("0", 64))
	if !errors.Is(err, ErrStagedArtifactDigestMismatch) {
		t.Fatalf("err = %v, want ErrStagedArtifactDigestMismatch", err)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid class", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".loom", "drivers"))
	if err != nil {
		t.Fatalf("read drivers root: %v", err)
	}
	for _, entry := range entries {
		t.Fatalf("drivers root must be empty after a digest mismatch, found %q", entry.Name())
	}
}
