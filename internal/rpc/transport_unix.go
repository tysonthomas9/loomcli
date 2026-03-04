//go:build !windows

package rpc

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

func listenRPC(socketPath string) (net.Listener, error) {
	return net.Listen("unix", socketPath)
}

func dialRPC(socketPath string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", socketPath, timeout)
}

func endpointExists(socketPath string) bool {
	cleanPath := filepath.Clean(socketPath)
	_, err := os.Stat(cleanPath)
	return err == nil
}
