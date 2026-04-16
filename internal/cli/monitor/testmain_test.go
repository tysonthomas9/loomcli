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

	// Strip GIT_* env vars that can redirect git subprocesses to the outer repo
	// when tests run inside a worktree or under a git hook.
	for _, k := range []string{
		"GIT_DIR",
		"GIT_WORK_TREE",
		"GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_CEILING_DIRECTORIES",
		"GIT_COMMON_DIR",
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
