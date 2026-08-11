//go:build unix

package leadcontrol

import (
	"os/exec"
	"syscall"
)

func configureCodexAppServerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateCodexAppServerProcess(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != syscall.ESRCH {
		return err
	}
	return nil
}

func killCodexAppServerProcess(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
		return err
	}
	return nil
}

func codexAppServerProcessGroupAlive(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, 0) == nil
}
