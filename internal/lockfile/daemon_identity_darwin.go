//go:build darwin

package lockfile

import (
	"os/exec"
	"strconv"
	"strings"
)

const daemonProcessIdentitySupported = true

func isLoomDaemonProcess(pid int) bool {
	if !IsProcessRunning(pid) {
		return false
	}
	pidArg := strconv.Itoa(pid)
	mappings, err := exec.Command("/usr/sbin/lsof", "-a", "-p", pidArg, "-d", "txt", "-Fn").Output() //nolint:gosec,norawexec // fixed binary; pid is an integer
	if err != nil || !mappedExecutableMatchesCurrent(mappings) {
		return false
	}
	command, err := exec.Command("/bin/ps", "-ww", "-p", pidArg, "-o", "args=").Output() //nolint:gosec,norawexec // fixed binary; pid is an integer
	if err != nil {
		return false
	}
	return argsAreLoomDaemon(strings.Fields(string(command)))
}

// lsof reports every text mapping, including dyld, as an n-prefixed path.
// Matching any mapping by file identity avoids trusting ps's basename-only
// comm field while remaining insensitive to mapping order.
func mappedExecutableMatchesCurrent(output []byte) bool {
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && sameExecutable(strings.TrimPrefix(line, "n")) {
			return true
		}
	}
	return false
}
