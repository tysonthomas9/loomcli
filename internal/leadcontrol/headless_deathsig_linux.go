//go:build linux

package leadcontrol

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal asks the kernel to SIGTERM the turn process if this
// process dies first (SIGKILL from a supervisor skips every Go-side
// cleanup). SIGTERM rather than SIGKILL so a wrapper binary that forked the
// real agent can forward it; the wrapper escalates to SIGKILL itself.
func setParentDeathSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}
