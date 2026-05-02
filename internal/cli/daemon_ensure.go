package cli

import (
	"encoding/json"
	"fmt"
	"time"
)

// DaemonStatus represents the JSON output of "bd daemon status --json"
type DaemonStatus struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

// EnsureIssueBackendRunning dispatches to the appropriate backend daemon.
// For fleet-db, the server lifecycle is managed by daemon_cmd.go, so this is a no-op.
func EnsureIssueBackendRunning(deps *Deps, timeout time.Duration) (bool, error) {
	if IsFleetActive() {
		return false, nil
	}
	if IsFleetDBActive() {
		return false, nil
	}
	return EnsureBdDaemonRunning(deps, timeout)
}

// EnsureBdDaemonRunning checks if the bd daemon is running and starts it if not.
// Returns (true, nil) if we started the daemon, (false, nil) if it was already running,
// or (false, err) if the daemon could not be started or did not become ready in time.
func EnsureBdDaemonRunning(deps *Deps, timeout time.Duration) (bool, error) {
	if isDaemonRunning(deps) {
		return false, nil
	}

	result := deps.Exec.Run(GetBeadsDir(), "bd", "daemon", "start")
	if result.Err != nil {
		return false, fmt.Errorf("failed to start bd daemon: %w", result.Err)
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(timeout)

	for {
		select {
		case <-deadline:
			return false, fmt.Errorf("daemon did not become ready within %s", timeout)
		case <-ticker.C:
			if isDaemonRunning(deps) {
				return true, nil
			}
		}
	}
}

// isDaemonRunning checks if the bd daemon is currently running.
func isDaemonRunning(deps *Deps) bool {
	result := deps.Exec.Run(GetBeadsDir(), "bd", "daemon", "status", "--json")
	if result.Err != nil {
		return false
	}

	var status DaemonStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return false
	}

	return status.Status == "running"
}
