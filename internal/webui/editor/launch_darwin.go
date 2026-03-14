//go:build darwin

package editor

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// LaunchEditor opens a file/workspace in the given detected editor.
// It is fire-and-forget: the editor process is detached and we do not wait for it.
func LaunchEditor(de DetectedEditor, targets []string) error {
	var cmd *exec.Cmd

	switch de.Method {
	case "cli":
		cmd = newCommandFn(de.ResolvedPath, targets...)
	case "app":
		args := append([]string{"-a", de.ResolvedPath}, targets...)
		cmd = newCommandFn("open", args...)
	default:
		return fmt.Errorf("editor %s: unknown launch method %q", de.ID, de.Method)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("editor %s: failed to launch: %w", de.ID, err)
	}

	// Reap the child process to prevent zombies.
	go cmd.Wait() //nolint:errcheck

	return nil
}
