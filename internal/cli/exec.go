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

// execCommandWithTimeout runs a command through execCommand with a deadline.
// Routes through the mockable execCommand variable so tests that swap it
// automatically get the timeout variant too.
func execCommandWithTimeout(timeout time.Duration, dir, name string, args ...string) CommandResult {
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
