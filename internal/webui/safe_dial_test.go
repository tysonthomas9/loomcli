package webui

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsPrivateIPAndSafeDialValidation(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"169.254.1.1", true},
		{"224.0.0.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"8.8.8.8", false},
	}
	for _, tt := range tests {
		if got := isPrivateIP(net.ParseIP(tt.ip)); got != tt.want {
			t.Fatalf("isPrivateIP(%s) = %t, want %t", tt.ip, got, tt.want)
		}
	}

	dial := SafeDialContext(false)
	if _, err := dial(context.Background(), "tcp", "missing-port"); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Fatalf("invalid address err = %v", err)
	}
	if _, err := dial(context.Background(), "tcp", "10.0.0.1:80"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("private IP err = %v", err)
	}
}

func TestSafeDialAllowsLoopbackAndAllowPrivate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		accepted <- conn
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := SafeDialContext(false)(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial: %v", err)
	}
	defer conn.Close()
	select {
	case serverConn := <-accepted:
		_ = serverConn.Close()
	case <-ctx.Done():
		t.Fatal("listener did not accept loopback dial")
	}

	if SafeDialContext(true) == nil {
		t.Fatal("allow-private dialer is nil")
	}
}
