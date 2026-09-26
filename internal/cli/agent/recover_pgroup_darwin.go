package agent

import "golang.org/x/sys/unix"

const darwinZombieState = 5

func processGroupHasLiveMemberPlatform(pgid int) (bool, bool) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return false, false
	}
	for i := range procs {
		if int(procs[i].Eproc.Pgid) == pgid && procs[i].Proc.P_stat != darwinZombieState {
			return true, true
		}
	}
	return false, true
}
