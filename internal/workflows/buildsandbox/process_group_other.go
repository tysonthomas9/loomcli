//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd

package buildsandbox

import "os/exec"

func prepareCommand(_ *exec.Cmd) {}
func killProcessGroup(_ int)     {}
