//go:build unix

package bootstrap

import "syscall"

// newDetachedSysProcAttr puts the child in its own process group so a
// signal sent to the loom CLI's group does not double-fire to the
// fleet-db subprocess (Stop sends SIGINT explicitly and waits).
func newDetachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
