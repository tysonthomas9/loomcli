//go:build linux

package rpc

import (
	"fmt"
	"net"
	"syscall"
)

// getPeerUID returns the UID of the peer process connected via a Unix socket.
// Uses SO_PEERCRED to retrieve credentials captured at connect() time.
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
		ucred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credErr = fmt.Errorf("SO_PEERCRED: %w", err)
			return
		}
		uid = ucred.Uid
	})
	if err != nil {
		return 0, fmt.Errorf("fd control: %w", err)
	}
	if credErr != nil {
		return 0, credErr
	}

	return uid, nil
}
