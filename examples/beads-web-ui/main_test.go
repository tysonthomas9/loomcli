package main

import (
	"fmt"
	"net"
	"testing"
)

// TestFindAvailablePort_FirstPortFree tests that findAvailablePort returns the first port
// when it is available.
func TestFindAvailablePort_FirstPortFree(t *testing.T) {
	// Use a high port number to avoid conflicts with common services
	startPort := 59100

	listener, port, err := findAvailablePort(startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	if port != startPort {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d", startPort, port, startPort)
	}
}

// TestFindAvailablePort_FallbackToNextPort tests that findAvailablePort falls back
// to the next port when the first one is in use.
func TestFindAvailablePort_FallbackToNextPort(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59200

	// Occupy the first port
	addr := fmt.Sprintf(":%d", startPort)
	occupier, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to occupy port %d: %v", startPort, err)
	}
	defer occupier.Close()

	// Now findAvailablePort should return the next port
	listener, port, err := findAvailablePort(startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	want := startPort + 1
	if port != want {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d (first port is occupied)", startPort, port, want)
	}
}

// TestFindAvailablePort_FallbackMultiplePorts tests that findAvailablePort correctly
// skips multiple occupied ports.
func TestFindAvailablePort_FallbackMultiplePorts(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59300

	// Occupy the first three ports
	var occupiers []net.Listener
	for i := 0; i < 3; i++ {
		addr := fmt.Sprintf(":%d", startPort+i)
		occupier, err := net.Listen("tcp", addr)
		if err != nil {
			// Clean up already created listeners
			for _, l := range occupiers {
				l.Close()
			}
			t.Fatalf("failed to occupy port %d: %v", startPort+i, err)
		}
		occupiers = append(occupiers, occupier)
	}
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()

	// Now findAvailablePort should return the fourth port
	listener, port, err := findAvailablePort(startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	want := startPort + 3
	if port != want {
		t.Errorf("findAvailablePort(%d, 5) = %d, want %d (first 3 ports are occupied)", startPort, port, want)
	}
}

// TestFindAvailablePort_AllPortsInUse tests that findAvailablePort returns an error
// when all ports in the range are occupied.
func TestFindAvailablePort_AllPortsInUse(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59400
	maxAttempts := 3

	// Occupy all ports in the range
	var occupiers []net.Listener
	for i := 0; i < maxAttempts; i++ {
		addr := fmt.Sprintf(":%d", startPort+i)
		occupier, err := net.Listen("tcp", addr)
		if err != nil {
			// Clean up already created listeners
			for _, l := range occupiers {
				l.Close()
			}
			t.Fatalf("failed to occupy port %d: %v", startPort+i, err)
		}
		occupiers = append(occupiers, occupier)
	}
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()

	// Now findAvailablePort should return an error
	listener, port, err := findAvailablePort(startPort, maxAttempts)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, %d) = %d, want error (all ports occupied)", startPort, maxAttempts, port)
	}

	// Verify the error message mentions the port range
	wantErrSubstr := fmt.Sprintf("no available port found in range %d-%d", startPort, startPort+maxAttempts-1)
	if err != nil && err.Error() != wantErrSubstr {
		t.Errorf("findAvailablePort error = %q, want %q", err.Error(), wantErrSubstr)
	}
}

// TestFindAvailablePort_SingleAttempt tests findAvailablePort with maxAttempts=1.
func TestFindAvailablePort_SingleAttempt(t *testing.T) {
	// Use a high port number to avoid conflicts
	startPort := 59500

	// Test 1: Port is free
	listener, port, err := findAvailablePort(startPort, 1)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 1) failed: %v", startPort, err)
	}
	listener.Close() // Close immediately to reuse port in next test

	if port != startPort {
		t.Errorf("findAvailablePort(%d, 1) = %d, want %d", startPort, port, startPort)
	}

	// Test 2: Port is occupied - should fail immediately
	occupier, err := net.Listen("tcp", fmt.Sprintf(":%d", startPort))
	if err != nil {
		t.Fatalf("failed to occupy port %d: %v", startPort, err)
	}
	defer occupier.Close()

	listener, port, err = findAvailablePort(startPort, 1)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, 1) = %d, want error (port occupied with single attempt)", startPort, port)
	}
}

// TestFindAvailablePort_ZeroAttempts tests findAvailablePort with maxAttempts=0.
func TestFindAvailablePort_ZeroAttempts(t *testing.T) {
	startPort := 59600

	// With zero attempts, should immediately return error
	listener, port, err := findAvailablePort(startPort, 0)
	if err == nil {
		listener.Close()
		t.Errorf("findAvailablePort(%d, 0) = %d, want error (zero attempts)", startPort, port)
	}
}

// TestFindAvailablePort_ListenerIsUsable tests that the returned listener is actually
// usable and the port remains bound.
func TestFindAvailablePort_ListenerIsUsable(t *testing.T) {
	startPort := 59700

	listener, port, err := findAvailablePort(startPort, 5)
	if err != nil {
		t.Fatalf("findAvailablePort(%d, 5) failed: %v", startPort, err)
	}
	defer listener.Close()

	// Verify the listener is holding the port by trying to bind again
	addr := fmt.Sprintf(":%d", port)
	duplicate, err := net.Listen("tcp", addr)
	if err == nil {
		duplicate.Close()
		t.Errorf("port %d should be held by returned listener, but was able to bind again", port)
	}
}
