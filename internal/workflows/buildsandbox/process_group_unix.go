//go:build darwin || linux || freebsd || openbsd || netbsd

package buildsandbox

import (
	"os/exec"
	"syscall"
)

func prepareCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killProcessGroup(pid int)     { _ = syscall.Kill(-pid, syscall.SIGKILL) }
