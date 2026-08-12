//go:build !linux && !darwin

package lockfile

// Unsupported hosts cannot use state-only PID metadata as liveness evidence.
// Held locks retain their historical live-PID fallback in the caller because
// the lock, rather than this helper, is the ownership authority.
const daemonProcessIdentitySupported = false

func isLoomDaemonProcess(int) bool {
	return false
}
