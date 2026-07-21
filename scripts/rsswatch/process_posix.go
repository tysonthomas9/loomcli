//go:build darwin || linux

package main

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func readProcessTable() (map[int]processSample, error) {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessTable(output)
}
