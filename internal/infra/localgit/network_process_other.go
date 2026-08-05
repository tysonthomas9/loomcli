//go:build !unix

package localgit

import (
	"fmt"
	"os/exec"
)

// Credentialed Git fails closed where the standard library cannot terminate
// the complete transport and credential-helper process tree.
func requireCredentialProcessIsolation() error {
	return fmt.Errorf("credentialed git is unavailable: process-tree isolation is unsupported on this platform")
}

func configureNetworkGitCancellation(_ *exec.Cmd) {}
