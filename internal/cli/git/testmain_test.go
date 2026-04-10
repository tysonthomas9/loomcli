package git

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's ~/.loom/config.yaml so the resolver
	// uses legacy mode instead of workspace mode. Without this, tests that
	// validate legacy-mode error messages or create temp worktree directories
	// fail because they pick up workspace mode from the host config.
	tmpCfg, err := os.MkdirTemp("", "loom-git-test-config-*")
	if err == nil {
		os.Setenv("LOOM_CONFIG_DIR", tmpCfg)
		defer os.RemoveAll(tmpCfg)
	}
	os.Exit(m.Run())
}
