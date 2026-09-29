//go:build windows

package browserauth

import (
	"context"
	"errors"
	"log/slog"
)

var errOperatorSocketUnsupported = errors.New("local operator socket is not supported on Windows")

// OperatorSocketServer is unavailable on Windows; the local operator bridge
// stays disabled and browser operator routes return a setup error.
type OperatorSocketServer struct{}

// ListenOperatorSocket always fails on Windows.
func ListenOperatorSocket(string, *OperatorSessionRegistry, WorkspaceValidator, *slog.Logger) (*OperatorSocketServer, error) {
	return nil, errOperatorSocketUnsupported
}

// Path returns "".
func (*OperatorSocketServer) Path() string { return "" }

// Close is a no-op.
func (*OperatorSocketServer) Close() error { return nil }

// CallOperatorSocket always fails on Windows.
func CallOperatorSocket(context.Context, string, SocketRequest) (SocketResponse, error) {
	return SocketResponse{}, errOperatorSocketUnsupported
}
