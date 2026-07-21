//go:build darwin

package local

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const processExecutableInspectionSupported = true

var processExecutablePathFn = processExecutablePath

func processExecutablePath(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid process ID %d", pid)
	}
	// macOS does not expose /proc/<pid>/exe. `comm` returns the executable
	// path without including arguments, and -ww prevents path truncation.
	output, err := exec.Command("/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", "comm=").Output() //nolint:gosec,norawexec // fixed system binary; pid is an integer
	if err != nil {
		return "", fmt.Errorf("inspect process %d executable: %w", pid, err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("inspect process %d executable: empty result", pid)
	}
	return path, nil
}
