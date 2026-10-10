// Package gitrunner runs git commands with a context. Packages that run git
// take a Runner, so the composition root chooses the real one and tests can
// pass a fake.
package gitrunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs one git command in dir and returns its trimmed combined output.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// EnvRunner is a Runner that can also add environment variables, such as
// GIT_INDEX_FILE for a private index.
type EnvRunner interface {
	Runner
	RunEnv(ctx context.Context, dir string, env []string, args ...string) (string, error)
}

// Exec runs the git executable with terminal prompts turned off.
type Exec struct{}

// Run implements Runner: Output with the success output trimmed and no
// output on error.
func (Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return Exec{}.RunEnv(ctx, dir, nil, args...)
}

// RunEnv implements EnvRunner: Run with env added to git's environment.
func (Exec) RunEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, err := output(ctx, dir, env, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Output runs git in dir with terminal prompts turned off and returns its raw
// combined output, on error too. The error names the command and holds the
// trimmed output. It is the one git subprocess core that Exec and
// localworkspace share.
func Output(ctx context.Context, dir string, args ...string) (string, error) {
	return output(ctx, dir, nil, args...)
}

// output is Output with env added to git's environment.
func output(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git executable; callers pass git arguments.
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
