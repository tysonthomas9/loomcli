//go:build unix

package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func init() {
	procInspector = processInspector{
		List: psListProcesses,
		CWD:  lsofProcessCWD,
		CWDs: lsofProcessCWDs,
	}
}

const (
	psListTimeout  = 2 * time.Second
	lsofCWDTimeout = 2 * time.Second
)

// psListProcesses runs `ps -A -o pid,ppid,pgid` and parses the output.
// Works on macOS (BSD ps) and Linux (procps ps): both accept the headerless
// `=` suffix form and the listed columns.
func psListProcesses() ([]procInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,pgid=") //nolint:gosec // G204: fixed args
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ps -A timed out")
	}
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
	cwds, err := lsofProcessCWDs([]int{pid})
	if err != nil {
		return "", err
	}
	return cwds[pid], nil
}

// lsofProcessCWDs returns cwd paths for many pids with one bounded lsof call.
// Calling lsof once per orphan candidate can overload macOS process inspection
// when a startup sweep runs on a machine with many PPID==1 processes.
func lsofProcessCWDs(pids []int) (map[int]string, error) {
	cwds := make(map[int]string)
	ids := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 1 {
			ids = append(ids, strconv.Itoa(pid))
		}
	}
	if len(ids) == 0 {
		return cwds, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), lsofCWDTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-p", strings.Join(ids, ","), "-d", "cwd", "-F", "pn", "-a") //nolint:gosec // G204: pids are ints
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return cwds, nil
	}

	currentPID := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, parseErr := strconv.Atoi(strings.TrimSpace(line[1:]))
			if parseErr == nil {
				currentPID = pid
			}
		case strings.HasPrefix(line, "n") && currentPID > 1:
			if cwd := strings.TrimSpace(line[1:]); cwd != "" {
				cwds[currentPID] = cwd
			}
		}
	}
	if err != nil {
		// lsof exits non-zero when some processes vanish or have no cwd match.
		// Treat that as best-effort and return whatever stdout contained.
		return cwds, nil
	}
	return cwds, nil
}
