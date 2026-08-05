//go:build !unix

package localworkspace

import (
	"fmt"
	"os/exec"
)

// requireGitCredentialProcessIsolation fails closed on platforms where the Go
// standard library cannot terminate git's credential and transport process
// tree. Anonymous git remains available; private credential use must wait for
// a platform-native job/process-group implementation.
func requireGitCredentialProcessIsolation() error {
	return fmt.Errorf("credentialed git is unavailable: process-tree isolation is unsupported on this platform")
}

// configureGitNetworkCancellation retains exec.CommandContext's direct-child
// cancellation outside Unix. Go has no portable process-tree kill primitive;
// WaitDelay still bounds pipe cleanup, but a helper that deliberately detaches
// itself may briefly outlive git. The credential is never in argv or config,
// and stock git helpers are covered by the bounded cancellation regression
// test on supported CI platforms.
func configureGitNetworkCancellation(_ *exec.Cmd) {}
