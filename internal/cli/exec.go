package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	envfilterpkg "github.com/tysonthomas9/loomcli/internal/cli/envfilter"
)

// CommandResult represents the output of a command execution
type CommandResult struct {
	Stdout string
	Stderr string
	Err    error
}

func defaultExecCommand(dir, name string, args ...string) CommandResult {
	cmd := exec.Command(name, args...) //nolint:gosec // G204 — caller controls command name
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

func defaultExecCommandContext(ctx context.Context, dir, name string, args ...string) CommandResult {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204 — caller controls command name
	cmd.Dir = dir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

// RunGit is the deps-aware helper for running git commands.
func RunGit(deps *Deps, dir string, args ...string) (string, error) {
	result := deps.Git.Run(dir, args...)
	if result.Err != nil {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), result.Stderr)
	}
	return result.Stdout, nil
}

// RunGitOutput is the deps-aware helper for running git commands with output streaming.
func RunGitOutput(deps *Deps, dir string, args ...string) error {
	return deps.Git.RunWithOutput(dir, args...)
}

// RunGitCommand executes a git command in the specified directory using defaultDeps.
func RunGitCommand(dir string, args ...string) (string, error) {
	return RunGit(ensureDefaultDeps(), dir, args...)
}

func defaultRunGitWithOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec // G204 — args from internal callers
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return RunGitOutput(ensureDefaultDeps(), dir, args...)
}

// FilteredEnv returns os.Environ() filtered through the subprocess allowlist.
func FilteredEnv() []string {
	return SelfBinPathEnv(envfilterpkg.FilteredEnv())
}

// FilterEnv filters an environment slice through the subprocess allowlist.
func FilterEnv(env []string) []string {
	return envfilterpkg.FilterEnv(env)
}
