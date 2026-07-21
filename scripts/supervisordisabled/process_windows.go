//go:build windows

package main

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroup(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
