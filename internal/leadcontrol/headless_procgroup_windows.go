//go:build windows

package leadcontrol

import "os/exec"

func setTurnProcessGroup(*exec.Cmd) {}

func terminateTurnProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
