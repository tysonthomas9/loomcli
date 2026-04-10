package workspace

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's ~/.loom/config.yaml so the resolver
	// uses legacy mode instead of workspace mode. Without this, tests fail
	// because GetBeadsDir() and the workspace resolver pick up the host
	// workspace config instead of the test fixtures.
	tmpCfg, err := os.MkdirTemp("", "loom-workspace-test-config-*")
	if err == nil {
		os.Setenv("LOOM_CONFIG_DIR", tmpCfg)
		defer os.RemoveAll(tmpCfg)
	}
	os.Exit(m.Run())
}
