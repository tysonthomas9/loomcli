//go:build unix

package localworkspace

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureGitNetworkCancellation keeps Git and its transport descendants in
// one process group so cancellation terminates the complete anonymous read.
func configureGitNetworkCancellation(cmd *exec.Cmd) {
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
