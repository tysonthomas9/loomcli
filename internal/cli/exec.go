package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// CommandResult represents the output of a command execution
type CommandResult struct {
	Stdout string
	Stderr string
	Err    error
}

// commandExecutor is the function type for executing commands
type commandExecutor func(dir, name string, args ...string) CommandResult

// execCommand is the package-level executor (swappable for tests)
var execCommand commandExecutor = defaultExecCommand

// lookPath is the package-level LookPath function (swappable for tests)
var lookPath = exec.LookPath

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

// execCommandWithTimeout runs a command with a deadline.
// Uses exec.CommandContext to kill the subprocess on timeout, preventing
// goroutine and OS process leaks when git hangs (e.g., index lock).
// Falls back to the goroutine wrapper when execCommand has been swapped
// for testing, so test mocks still work.
func execCommandWithTimeout(timeout time.Duration, dir, name string, args ...string) CommandResult {
	// If execCommand was swapped for tests, route through the mock
	if isCustomExecutor() {
		ch := make(chan CommandResult, 1)
		go func() {
			ch <- execCommand(dir, name, args...)
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case result := <-ch:
			return result
		case <-timer.C:
			return CommandResult{
				Err:    context.DeadlineExceeded,
				Stderr: fmt.Sprintf("command timed out after %v", timeout),
			}
		}
	}

	// Production path: CommandContext kills the child process on timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204 — caller controls command name
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return CommandResult{
			Stdout: stdout.String(),
			Stderr: fmt.Sprintf("command timed out after %v", timeout),
			Err:    context.DeadlineExceeded,
		}
	}
	return CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

// isCustomExecutor reports whether execCommand has been replaced (e.g., by tests).
func isCustomExecutor() bool {
	// Compare function pointers via fmt — Go doesn't allow direct comparison of funcs.
	return fmt.Sprintf("%p", execCommand) != fmt.Sprintf("%p", commandExecutor(defaultExecCommand))
}
