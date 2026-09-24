//go:build !darwin && !linux && !windows

package browserauth

import (
	"errors"
	"net"
)

// Other Unix platforms fail closed: without peer credentials the local
// operator socket is never created.
func peerCredentialsSupported() bool { return false }

func peerUID(net.Conn) (uint32, error) {
	return 0, errors.New("peer credentials unsupported on this platform")
}
