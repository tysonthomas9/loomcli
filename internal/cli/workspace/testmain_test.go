package workspace

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

func TestMain(m *testing.M) {
	// Isolate all tests from the host's ~/.loom/config.yaml so the resolver
	// uses no workspace config instead of workspace mode. Without this, tests fail
	// because GetWorkspaceRuntimeDir() and the workspace resolver pick up the host
	// workspace config instead of the test fixtures.
	tmpCfg, err := os.MkdirTemp("", "loom-workspace-test-config-*")
	if err == nil {
		os.Setenv("LOOM_CONFIG_DIR", tmpCfg)
		defer os.RemoveAll(tmpCfg)
	}

	// Strip GIT_* env vars that can redirect git subprocesses. When these
	// tests run under a git hook (e.g. pre-push), the parent git process
	// sets GIT_DIR / GIT_WORK_TREE pointing at the outer loomcli repo.
	// Those vars take precedence over cmd.Dir, so our test's `git worktree
	// add` inside /tmp would silently register worktrees in the outer repo,
	// leaving stale branch refs that collide with later test runs.
	for _, k := range clitest.GitEnvVars {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
