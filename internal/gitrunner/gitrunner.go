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

// Exec runs the git executable with terminal prompts turned off.
type Exec struct{}

// Run implements Runner. The error names the command and holds its output.
func (Exec) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git executable; callers pass git arguments.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
