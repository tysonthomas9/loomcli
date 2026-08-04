//go:build unix || linux || darwin

package lockfile

import (
	"errors"
	"syscall"
)

// IsProcessRunning checks if a process with the given PID is running.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false // Invalid PID (0 would signal our process group, not a specific process)
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
