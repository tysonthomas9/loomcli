//go:build unix || darwin || linux

package driver

import (
	"os/exec"
	"syscall"
	"time"
)

var taskRunnerKillDelay = 5 * time.Second

func configureTaskRunnerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	cmd.WaitDelay = taskRunnerKillDelay
}

func terminateTaskRunnerGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
