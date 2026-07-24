//go:build unix

package localworkspace

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func requireGitCredentialProcessIsolation() error {
	return nil
}

// configureGitNetworkCancellation keeps git and its transport/credential
// helper descendants in one process group. On context cancellation, killing
// the group prevents a child from retaining the ephemeral credential
// environment after the git leader exits.
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
