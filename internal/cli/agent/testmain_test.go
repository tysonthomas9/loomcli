package agent

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's ~/.loom/config.yaml so the resolver
	// uses no workspace config instead of workspace mode. Without this, tests that
	// create temp worktree directories fail because the resolver discovers
	// the real repo config instead of the test fixtures.
	tmpCfg, err := os.MkdirTemp("", "loom-agent-test-config-*")
	if err == nil {
		os.Setenv("LOOM_CONFIG_DIR", tmpCfg)
		defer os.RemoveAll(tmpCfg)
	}
	os.Exit(m.Run())
}
