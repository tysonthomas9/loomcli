//go:build linux

package editor

import (
	"fmt"
	"os"
	"syscall"
)

// LaunchEditor opens a file/workspace in the given detected editor.
// It is fire-and-forget: the editor process is detached and we do not wait for it.
func LaunchEditor(de DetectedEditor, targets []string) error {
	if de.Method != "cli" && de.Method != "app" {
		return fmt.Errorf("editor %s: unknown launch method %q", de.ID, de.Method)
	}

	cmd := newCommandFn(de.ResolvedPath, targets...)
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
