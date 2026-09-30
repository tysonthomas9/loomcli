//go:build !unix && !darwin && !linux

package driver

import (
	"os"
	"os/exec"
	"time"
)

var taskRunnerKillDelay = 5 * time.Second

func configureTaskRunnerProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = taskRunnerKillDelay
}

func terminateTaskRunnerGroup(_ *exec.Cmd) {}
