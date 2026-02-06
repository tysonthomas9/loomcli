package main

import (
	"bytes"
	"os/exec"
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

func defaultExecCommand(dir, name string, args ...string) CommandResult {
	cmd := exec.Command(name, args...)
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
