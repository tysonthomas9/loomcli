//go:build darwin

package rpc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// getPeerUID returns the UID of the peer process connected via a Unix socket.
// Uses LOCAL_PEERCRED to retrieve the xucred struct on macOS/Darwin.
func getPeerUID(conn net.Conn) (uint32, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errNoPeerCred
	}

	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("getting syscall conn: %w", err)
	}

	var uid uint32
	var credErr error

	err = raw.Control(func(fd uintptr) {
		xucred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credErr = fmt.Errorf("LOCAL_PEERCRED: %w", err)
			return
		}
		uid = xucred.Uid
	})
	if err != nil {
		return 0, fmt.Errorf("fd control: %w", err)
	}
	if credErr != nil {
		return 0, credErr
	}

	return uid, nil
}
