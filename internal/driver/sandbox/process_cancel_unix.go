//go:build unix

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ConfigureProcessTreeCancellation isolates a launcher and every subprocess it
// creates in one process group. Task-run deadlines belong to Loom, so a timeout
// must terminate the complete runner tree rather than leave backend CLIs and
// validation commands running after FleetDB has failed the attempt.
func ConfigureProcessTreeCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return cmd.Process.Kill()
		}
		return nil
	}
}
