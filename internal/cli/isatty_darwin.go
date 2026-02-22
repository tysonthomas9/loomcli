//go:build darwin

package cli

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA
