package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// GitExecError wraps an error from a `git` invocation with the args and
// captured stderr text so substring matchers (and structured callers via
// errors.As) can inspect what git actually said. Returned by
// defaultRunGitWithOutput on failure.
type GitExecError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *GitExecError) Error() string {
	return fmt.Sprintf("git %s: %s: %v", strings.Join(e.Args, " "), e.Stderr, e.Err)
}

func (e *GitExecError) Unwrap() error { return e.Err }

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
	return RunGit(defaultDeps, dir, args...)
}

func defaultRunGitWithOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec // G204 — args from internal callers
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	if err := cmd.Run(); err != nil {
		return &GitExecError{
			Args:   append([]string(nil), args...),
			Stderr: strings.TrimRight(stderrBuf.String(), "\n"),
			Err:    err,
		}
	}
	return nil
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return RunGitOutput(defaultDeps, dir, args...)
}
