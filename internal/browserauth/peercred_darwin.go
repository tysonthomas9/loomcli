//go:build darwin

package browserauth

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func peerCredentialsSupported() bool { return true }

// peerUID reads the connecting process's effective UID via LOCAL_PEERCRED.
func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("peer credentials: not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		cred    *unix.Xucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return cred.Uid, nil
}
