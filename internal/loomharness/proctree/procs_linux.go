package proctree

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// processes reads /proc: PID -> parent and exact start time (clock ticks
// since boot). Zombies are left out.
func processes() map[int]process {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	procs := map[int]process{}
	for _, e := range ents {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			if p, ok := stat(pid); ok {
				procs[pid] = p
			}
		}
	}
	return procs
}

// startOf is pid's exact start time now; false when it does not run.
func startOf(pid int) (int64, bool) {
	p, ok := stat(pid)
	return p.start, ok
}

// stat parses /proc/<pid>/stat: the fields after the parenthesized command
// are state, ppid, ... and starttime is the 20th of them.
func stat(pid int) (process, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return process{}, false
	}
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
	if len(f) < 20 || f[0] == "Z" {
		return process{}, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	start, err2 := strconv.ParseInt(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return process{}, false
	}
	return process{ppid: ppid, start: start}, true
}

// exited blocks until pid, a child of this process, has exited, without
// reaping it (waitid WNOWAIT).
func exited(pid int) error {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
