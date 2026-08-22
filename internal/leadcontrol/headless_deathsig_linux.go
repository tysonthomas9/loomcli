//go:build linux

package leadcontrol

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal asks the kernel to SIGKILL the turn process if this
// process dies first (SIGKILL from a supervisor skips every Go-side cleanup).
func setParentDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
