package cli

import (
	"os"
	"strings"
	"testing"
)

// gitEnvVars lists GIT_* environment variables that can redirect git commands
// to the parent repository when running inside a git worktree.
var gitEnvVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_CEILING_DIRECTORIES",
	"GIT_COMMON_DIR",
}

// gitSafeEnv returns os.Environ() with all GIT_* environment variables that
// could redirect git commands to the parent repository removed. Use this for
// tests that create git subprocesses directly.
//
// Background: when tests run inside a git worktree, env vars like GIT_DIR and
// GIT_WORK_TREE can cause git commands (even those with cmd.Dir set to a temp
// directory) to operate on the real repo, creating junk commits on production
// branches.
func gitSafeEnv(extra ...string) []string {
	strip := make(map[string]bool, len(gitEnvVars))
	for _, k := range gitEnvVars {
		strip[k] = true
	}

	var env []string
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			env = append(env, e)
			continue
		}
		if !strip[e[:idx]] {
			env = append(env, e)
		}
	}
	return append(env, extra...)
}

// clearGitEnvVars unsets GIT_* env vars that can redirect git commands to the
// parent repo. Use this for tests that call RunGitCommand (which inherits the
// process environment). The vars are restored after the test via t.Cleanup.
func clearGitEnvVars(t *testing.T) {
	t.Helper()
	for _, k := range gitEnvVars {
		if orig, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { os.Setenv(k, orig) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
}
