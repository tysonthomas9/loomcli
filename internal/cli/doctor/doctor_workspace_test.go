package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// setupUnionWorkspace points unionWorkspacePath at a temp dir holding the given
// integration.yaml (none when empty), restoring the seam on cleanup. It
// overrides the seam rather than standing up a workspace in the global loom
// config: resolving one for real needs a fleet-db binary on PATH, which the
// repo gate's clean environment does not have.
func setupUnionWorkspace(t *testing.T, integrationYAML string) string {
	t.Helper()
	dir := t.TempDir()
	orig := unionWorkspacePath
	unionWorkspacePath = func() string { return dir }
	t.Cleanup(func() { unionWorkspacePath = orig })
	if integrationYAML != "" {
		path := filepath.Join(dir, "integration.yaml")
		if err := os.WriteFile(path, []byte(integrationYAML), 0o600); err != nil {
			t.Fatalf("write integration.yaml: %v", err)
		}
	}
	return dir
}
