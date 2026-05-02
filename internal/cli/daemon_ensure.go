package cli

import "time"

// EnsureIssueBackendRunning is retained for serve startup call sites. FleetDB
// owns issue storage directly, so there is no external issue backend daemon to
// launch or stop.
func EnsureIssueBackendRunning(_ *Deps, _ time.Duration) (bool, error) {
	return false, nil
}
