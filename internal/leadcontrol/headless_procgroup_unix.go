//go:build !windows

package leadcontrol

import (
	"os/exec"
	"syscall"
)

// setTurnProcessGroup puts the turn process in its own process group so a
// wrapper binary and everything it forks can be signaled together.
func setTurnProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateTurnProcessGroup sends SIGTERM to the turn's process group. It is
// the exec.Cmd Cancel hook; exec escalates to SIGKILL on the direct child
// after cmd.WaitDelay.
func terminateTurnProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
