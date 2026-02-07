//go:build !windows

package rpc

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestGetPeerUID_SameUser(t *testing.T) {
	socketPath := newTestSocketPath(t)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	// Accept in background
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			t.Logf("Accept error: %v", err)
			return
		}
		accepted <- conn
	}()

	// Dial
	clientConn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	serverConn := <-accepted
	defer serverConn.Close()

	uid, err := getPeerUID(serverConn)
	if err != nil {
		t.Fatalf("getPeerUID: %v", err)
	}

	expectedUID := uint32(os.Getuid())
	if uid != expectedUID {
		t.Errorf("getPeerUID = %d, want %d", uid, expectedUID)
	}
}

func TestGetPeerUID_NonUnixConn(t *testing.T) {
	// TCP connections should return errNoPeerCred
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	serverConn := <-accepted
	defer serverConn.Close()

	_, err = getPeerUID(serverConn)
	if err != errNoPeerCred {
		t.Errorf("getPeerUID on TCP conn: got err=%v, want errNoPeerCred", err)
	}
}

func TestServerAcceptsSameUID(t *testing.T) {
	// Start a full test server and verify same-UID connections work.
	// This is the most important test: ensuring the peer credential check
	// doesn't break normal operation.
	server, client, cleanup := setupTestServer(t)
	defer cleanup()
	_ = server // used indirectly through client

	// Ping the server — if peer credential check rejects us, this will fail
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping failed (peer cred check may be rejecting same-UID): %v", err)
	}
}
