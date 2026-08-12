//go:build linux

package local

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const processExecutableInspectionSupported = true

var processExecutablePathFn = processExecutablePath

func processExecutablePath(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid process ID %d", pid)
	}
	path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return "", fmt.Errorf("inspect process %d executable: %w", pid, err)
	}
	return strings.TrimSuffix(path, " (deleted)"), nil
}
