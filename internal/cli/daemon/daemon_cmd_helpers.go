package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// isolateProcessGroup creates a new process group so signals to the parent's
// group don't kill the daemon.
func isolateProcessGroup() {
	if err := syscall.Setpgid(0, 0); err != nil {
		if _, err2 := syscall.Setsid(); err2 != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not isolate process group (setpgid: %v, setsid: %v)\n", err, err2)
		}
	}
}

// prepareDaemonDirs creates the PID file and log directories.
func prepareDaemonDirs(pidFilePath, logDir string) {
	if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating PID directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating log directory: %v\n", err)
		os.Exit(1)
	}
}

// cleanupOnStartFailure cleans up resources when daemon.Start fails (os.Exit skips defers).
func cleanupOnStartFailure(pidFilePath, stateFilePath string, lockFile *os.File, lockFilePath string) {
	os.Remove(pidFilePath)
	os.Remove(stateFilePath)
	lockFile.Close()
	os.Remove(lockFilePath)
}

// statusToIcon returns a Unicode icon for the given agent status string.
func statusToIcon(status string) string {
	switch status {
	case "running":
		return "●"
	case "starting":
		return "◐"
	case "stopped":
		return "○"
	case "error":
		return "✗"
	case "failed":
		return "✗"
	default:
		return "?"
	}
}
