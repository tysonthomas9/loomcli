//go:build linux

package backends

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TCGETS
