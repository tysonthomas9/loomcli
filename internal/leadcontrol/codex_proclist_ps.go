//go:build unix && !linux

package leadcontrol

import "os/exec"

func listProcesses() (string, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,pgid=,command=").Output()
	return string(out), err
}
