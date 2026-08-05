//go:build !unix

package localworkspace

import (
	"os/exec"
)

// Outside Unix, anonymous Git uses exec.CommandContext's direct-child
// cancellation. Credentialed Git is owned by infra/localgit and fails closed
// there when process-tree isolation is unavailable.
func configureGitNetworkCancellation(_ *exec.Cmd) {}
