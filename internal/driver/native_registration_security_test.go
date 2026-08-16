package driver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestStageFlueDriverBundleRejectsDriverIDPathTraversalBeforeFilesystemStaging(t *testing.T) {
	for _, driverID := range []string{"../escape", "nested/escape", `nested\escape`, "driver:redis", ".", ".."} {
		t.Run(driverID, func(t *testing.T) {
			workDir := t.TempDir()
			dist := filepath.Join(workDir, "dist")
			if err := os.MkdirAll(dist, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := StageFlueDriverBundle(RegisterFlueOptions{
				WorkspaceKey: "TEST",
				WorkDir:      workDir,
				DistPath:     dist,
				DriverName:   "demo",
				DriverID:     driverID,
			})
			if !errors.Is(err, persistence.ErrInvalid) {
				t.Fatalf("StageFlueDriverBundle(%q) error = %v, want ErrInvalid", driverID, err)
			}
			if _, statErr := os.Stat(filepath.Join(workDir, ".loom")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unsafe id %q staged filesystem content: stat err=%v", driverID, statErr)
			}
		})
	}
}
