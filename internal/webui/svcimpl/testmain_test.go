package svcimpl

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's real ~/.loom so state-cache writes
	// never touch the user's workspace path registry (LOOMDEV-14). The
	// bootstrap.LoomDir testing guard also protects this, but an explicit
	// LOOM_CONFIG_DIR keeps subprocess-spawning tests safe too.
	tmpCfg := ""
	if os.Getenv("LOOM_CONFIG_DIR") == "" {
		if dir, err := os.MkdirTemp("", "loom-test-config-*"); err == nil {
			os.Setenv("LOOM_CONFIG_DIR", dir)
			tmpCfg = dir
		}
	}
	code := m.Run()
	if tmpCfg != "" {
		os.RemoveAll(tmpCfg)
	}
	os.Exit(code)
}
