//go:build !unix

package bootstrap

import "syscall"

// newDetachedSysProcAttr is a no-op outside Unix-like systems.
func newDetachedSysProcAttr() *syscall.SysProcAttr { return nil }
