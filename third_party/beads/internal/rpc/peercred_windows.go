//go:build windows

package rpc

import "net"

// getPeerUID is a no-op on Windows. Windows uses TCP loopback for RPC
// transport, not Unix sockets, so peer credential verification is not available.
func getPeerUID(_ net.Conn) (uint32, error) {
	return 0, errNoPeerCred
}
