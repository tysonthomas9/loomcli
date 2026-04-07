package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// truncateDiff is a backward-compatible alias for TruncateDiff.
func truncateDiff(diff string, maxBytes int) string { return TruncateDiff(diff, maxBytes) }

// gitEnvVars lists GIT_* environment variables that can redirect git commands.
var gitEnvVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_CEILING_DIRECTORIES",
	"GIT_COMMON_DIR",
}

// clearGitEnvVars unsets GIT_* env vars and restores them after the test.
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

// gitSafeEnv returns os.Environ() with all GIT_* redirect variables removed.
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

// captureSingleRepoDiff runs git diff HEAD and truncates for checkpoint tests.
func captureSingleRepoDiff(repoPath string, maxBytes int) string {
	cmd := exec.Command("git", "diff", "HEAD") //nolint:gosec //nolint:norawexec
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return TruncateDiff(strings.TrimSpace(string(output)), maxBytes)
}
