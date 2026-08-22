//go:build !linux

package leadcontrol

import "os/exec"

// setParentDeathSignal is Linux-only; elsewhere the child is bound to the
// runtime context alone.
func setParentDeathSignal(*exec.Cmd) {}
