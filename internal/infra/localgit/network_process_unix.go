//go:build unix

package localgit

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func requireCredentialProcessIsolation() error {
	return nil
}

// configureNetworkGitCancellation keeps Git and its transport/credential
// helper descendants in one process group. Context cancellation terminates
// the whole group so no child retains the ephemeral environment.
func configureNetworkGitCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return command.Process.Kill()
		}
		return nil
	}
}
