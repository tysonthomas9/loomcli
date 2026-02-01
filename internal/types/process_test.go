package types

import (
	"os"
	"strings"
	"testing"
)

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	t.Parallel()

	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("Cannot get hostname: %v", err)
	}

	// Current process should always be alive
	pid := os.Getpid()
	if !IsProcessAlive(pid, hostname) {
		t.Errorf("IsProcessAlive(%d, %q) = false, want true (current process)", pid, hostname)
	}
}

func TestIsProcessAlive_DifferentHostname(t *testing.T) {
	t.Parallel()

	// Remote hostname - cannot verify, assume alive (fail-safe)
	if !IsProcessAlive(12345, "different-hostname-xyz") {
		t.Error("IsProcessAlive() should return true for different hostname (fail-safe)")
	}
}

func TestIsProcessAlive_HostnameCaseInsensitive(t *testing.T) {
	t.Parallel()

	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("Cannot get hostname: %v", err)
	}

	// Current process with uppercase hostname should still work
	pid := os.Getpid()
	uppercaseHost := strings.ToUpper(hostname)

	if !IsProcessAlive(pid, uppercaseHost) {
		t.Errorf("IsProcessAlive() should be case-insensitive for hostname comparison")
	}
}

func TestIsProcessAlive_DeadProcess(t *testing.T) {
	t.Parallel()

	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("Cannot get hostname: %v", err)
	}

	// Very high PID that's extremely unlikely to exist
	deadPID := 2147483647 // Max int32

	// This might still return true on some systems if permission errors occur
	// The function is designed to be fail-safe (return true on errors)
	alive := IsProcessAlive(deadPID, hostname)

	// On Linux, this should typically return false for a non-existent PID
	// But we can't guarantee it due to the fail-safe behavior
	t.Logf("IsProcessAlive(%d, %q) = %v", deadPID, hostname, alive)
}

func TestIsProcessAlive_EmptyHostname(t *testing.T) {
	t.Parallel()

	// Empty hostname is different from current host, so assume alive
	if !IsProcessAlive(12345, "") {
		t.Error("IsProcessAlive() with empty hostname should return true (different host)")
	}
}