//go:build linux

package leadcontrol

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// processStartTime is the process's start time in clock ticks since boot
// (/proc/<pid>/stat field 22), which with the pid identifies one process.
func processStartTime(pid int) (string, error) {
	// #nosec G304 -- a /proc path built from a pid.
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return "", errors.New("malformed /proc stat")
	}
	// After "pid (comm) " the fields start at field 3 (state).
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) < 20 {
		return "", errors.New("malformed /proc stat")
	}
	return fields[19], nil
}
