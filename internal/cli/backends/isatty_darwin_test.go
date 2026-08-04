//go:build darwin

package backends

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA
