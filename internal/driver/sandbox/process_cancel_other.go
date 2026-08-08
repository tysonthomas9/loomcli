//go:build !unix

package sandbox

import "os/exec"

// ConfigureProcessTreeCancellation retains exec.CommandContext's direct-child
// cancellation on non-Unix platforms. Unix is the packaged/runtime deployment
// target and provides process-group ownership for the complete runner tree.
func ConfigureProcessTreeCancellation(_ *exec.Cmd) {}
