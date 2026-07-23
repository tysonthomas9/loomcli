//go:build linux

package lockfile

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

const daemonProcessIdentitySupported = true

func isLoomDaemonProcess(pid int) bool {
	if !IsProcessRunning(pid) {
		return false
	}
	procDir := "/proc/" + strconv.Itoa(pid)
	executable, err := os.Readlink(procDir + "/exe")
	if err != nil || !sameExecutable(strings.TrimSuffix(executable, " (deleted)")) {
		return false
	}
	raw, err := os.ReadFile(procDir + "/cmdline") //nolint:gosec // pid is an integer
	if err != nil {
		return false
	}
	fields := bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0})
	args := make([]string, len(fields))
	for i := range fields {
		args[i] = string(fields[i])
	}
	return argsAreLoomDaemon(args)
}
