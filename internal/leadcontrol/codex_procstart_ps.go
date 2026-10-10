//go:build unix && !linux

package leadcontrol

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// processStartTime is the process's start time as ps reports it, which with
// the pid identifies one process.
func processStartTime(pid int) (string, error) {
	// #nosec G204 -- fixed ps invocation with a numeric pid.
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	start := strings.TrimSpace(string(out))
	if err != nil || start == "" {
		return "", errors.New("process not found")
	}
	return start, nil
}
