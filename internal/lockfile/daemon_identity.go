package lockfile

import (
	"os"
	"strings"
)

// DaemonProcessIdentitySupported reports whether this host can inspect both
// process executable and argv strongly enough to distinguish a Loom daemon
// from a reused PID.
func DaemonProcessIdentitySupported() bool {
	return daemonProcessIdentitySupported
}

// IsLoomDaemonProcess reports whether pid is a live process running this Loom
// executable with daemon as its first positional command.
func IsLoomDaemonProcess(pid int) bool {
	return isLoomDaemonProcess(pid)
}

// argsAreLoomDaemon finds the first positional argument after Loom's root
// flags and requires it to be the daemon subcommand. This accepts valid forms
// such as `loom --workspace ACME daemon` without accepting a later argument
// such as `loom serve ... daemon`.
func argsAreLoomDaemon(args []string) bool {
	for i := 1; i < len(args); {
		arg := args[i]
		if arg == "daemon" {
			return true
		}
		if arg == "--" {
			return i+1 < len(args) && args[i+1] == "daemon"
		}
		if arg == "-o" {
			if i+1 >= len(args) {
				return false
			}
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-o=") {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name, _, hasValue := strings.Cut(arg, "=")
			if !isRootStringFlag(name) {
				return false
			}
			if hasValue {
				i++
				continue
			}
			if i+1 >= len(args) {
				return false
			}
			i += 2
			continue
		}
		return false
	}
	return false
}

func isRootStringFlag(name string) bool {
	switch name {
	case "--backend", "--log-format", "--log-output", "--output", "--server", "--workspace":
		return true
	default:
		return false
	}
}

func sameExecutable(actual string) bool {
	expected, err := os.Executable()
	if err != nil {
		return false
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		return false
	}
	actualInfo, err := os.Stat(actual)
	if err != nil {
		return false
	}
	return os.SameFile(expectedInfo, actualInfo)
}
