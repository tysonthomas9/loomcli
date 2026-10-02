package proctree

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

const zombie = 5 // SZOMB in <sys/proc.h>

// processes reads the kernel process table (sysctl kern.proc.all): PID ->
// parent and exact start time in microseconds. Zombies are left out.
func processes() map[int]process {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	procs := make(map[int]process, len(kps))
	for i := range kps {
		kp := &kps[i]
		if kp.Proc.P_stat != zombie {
			procs[int(kp.Proc.P_pid)] = process{ppid: int(kp.Eproc.Ppid), start: micros(kp)}
		}
	}
	return procs
}

// startOf is pid's exact start time now; false when it does not run.
func startOf(pid int) (int64, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid || kp.Proc.P_stat == zombie {
		return 0, false
	}
	return micros(kp), true
}

func micros(kp *unix.KinfoProc) int64 {
	return kp.Proc.P_starttime.Sec*1e6 + int64(kp.Proc.P_starttime.Usec)
}

// exited blocks until pid, a child of this process, has exited, without
// reaping it (kqueue EVFILT_PROC NOTE_EXIT; darwin's wait4 ignores WNOWAIT).
func exited(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(kq) }()
	var change unix.Kevent_t
	unix.SetKevent(&change, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change.Fflags = unix.NOTE_EXIT
	for {
		out := make([]unix.Kevent_t, 1)
		_, err := unix.Kevent(kq, []unix.Kevent_t{change}, out, nil)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.ESRCH):
			return nil // already exiting: an unreaped zombie
		case err != nil:
			return err
		case out[0].Flags&unix.EV_ERROR != 0 && out[0].Data == int64(unix.ESRCH):
			return nil
		case out[0].Flags&unix.EV_ERROR != 0:
			return fmt.Errorf("kevent: errno %d", out[0].Data)
		}
		return nil
	}
}
