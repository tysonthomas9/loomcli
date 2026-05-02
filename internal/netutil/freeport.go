// Package netutil holds small networking helpers shared between the
// parity test harness, the embedded-fleet-db bootstrap path, and any
// other local-process spawning code that needs the same primitives.
package netutil

import (
	"fmt"
	"net"
	"strconv"
)

// PickFreeLoopbackPort asks the kernel for an unused TCP port on
// 127.0.0.1, closes the listener, and returns both the host:port string
// and the bare integer port for callers that need either form.
//
// There's a tiny TOCTOU window where another process could claim the
// port between the close and the eventual bind. Acceptable for
// dev/test harnesses; callers expecting strict ownership should
// retry on bind failure.
func PickFreeLoopbackPort() (host string, port int, err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return "", 0, fmt.Errorf("netutil: unexpected listener addr type %T", l.Addr())
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port)), addr.Port, nil
}
