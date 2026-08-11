//go:build !unix

package leadcontrol

import (
	"os"
	"os/exec"
)

func configureCodexAppServerProcess(_ *exec.Cmd) {}

func terminateCodexAppServerProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}

func killCodexAppServerProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

func codexAppServerProcessGroupAlive(cmd *exec.Cmd) bool {
	return cmd.ProcessState == nil || !cmd.ProcessState.Exited()
}
