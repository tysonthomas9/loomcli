//go:build linux

package leadcontrol

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listProcesses returns `ps -o pid=,ppid=,pgid=,command=` style lines read
// from /proc, so the reaper also works in images without procps.
func listProcesses() (string, error) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		stat, errStat := os.ReadFile(filepath.Join("/proc", d.Name(), "stat"))
		cmdline, errCmd := os.ReadFile(filepath.Join("/proc", d.Name(), "cmdline"))
		end := strings.LastIndexByte(string(stat), ')')
		if errStat != nil || errCmd != nil || end < 0 {
			continue
		}
		// After "pid (comm) ": state ppid pgrp ...
		fields := strings.Fields(string(stat)[end+1:])
		if len(fields) < 3 {
			continue
		}
		command := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		_, _ = fmt.Fprintf(&b, "%d %s %s %s\n", pid, fields[1], fields[2], command)
	}
	return b.String(), nil
}
