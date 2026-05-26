//go:build unix

package supervisor

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func init() {
	procInspector = processInspector{
		List: psListProcesses,
		CWD:  lsofProcessCWD,
	}
}

// psListProcesses runs `ps -A -o pid,ppid,pgid` and parses the output.
// Works on macOS (BSD ps) and Linux (procps ps): both accept the headerless
// `=` suffix form and the listed columns.
func psListProcesses() ([]procInfo, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=").Output() //nolint:gosec // G204: fixed args
	if err != nil {
		return nil, fmt.Errorf("ps -A: %w", err)
	}
	var procs []procInfo
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		procs = append(procs, procInfo{PID: pid, PPID: ppid, PGID: pgid})
	}
	return procs, nil
}

// lsofProcessCWD returns the cwd of pid using `lsof -p <pid> -d cwd -F n -a`.
// Output is a series of records, each line prefixed by a field type. We look
// for the `n` (name) line and return its value. Returns "" on failure.
func lsofProcessCWD(pid int) (string, error) {
	if pid <= 1 {
		return "", nil
	}
	out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-d", "cwd", "-F", "n", "-a").Output() //nolint:gosec // G204: pid is int
	if err != nil {
		// lsof exits non-zero when the process is gone or when the fd has no
		// match. Treat both as "no cwd available" rather than propagating.
		return "", nil
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimSpace(line[1:]), nil
		}
	}
	return "", nil
}
