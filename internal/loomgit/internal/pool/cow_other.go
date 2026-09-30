//go:build !darwin && !linux

package pool

import "syscall"

func cloneFile(string, string) error { return syscall.ENOTSUP }
