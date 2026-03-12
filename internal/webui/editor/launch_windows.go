//go:build windows

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
	resolvedPath := os.ExpandEnv(de.ResolvedPath)

	var cmd *exec.Cmd

	switch de.Method {
	case "cli":
		cmd = newCommandFn(resolvedPath, targets...)
	case "app":
		args := []string{"/c", "start", ""}
		args = append(args, resolvedPath)
		args = append(args, targets...)
		cmd = newCommandFn("cmd", args...)
	default:
		return fmt.Errorf("editor %s: unknown launch method %q", de.ID, de.Method)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
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
