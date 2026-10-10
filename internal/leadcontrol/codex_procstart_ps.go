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

// childPIDs lists pid's direct children.
func childPIDs(pid int) []int {
	// #nosec G204 -- fixed pgrep invocation with a numeric pid.
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	var children []int
	for _, f := range strings.Fields(string(out)) {
		if child, err := strconv.Atoi(f); err == nil {
			children = append(children, child)
		}
	}
	return children
}
