package monitor

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's ~/.loom/config.yaml so the resolver
	// uses legacy mode instead of workspace mode. Without this, tests pick
	// up real workspace state and discover unrelated agents/locks.
	tmpCfg, err := os.MkdirTemp("", "loom-monitor-test-config-*")
	if err == nil {
		os.Setenv("LOOM_CONFIG_DIR", tmpCfg)
		defer os.RemoveAll(tmpCfg)
	}
	os.Exit(m.Run())
}
