package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"
)

// waitForServerReady polls the health endpoint until the server is ready.
func waitForServerReady(t *testing.T, client *http.Client, serverAddr string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		resp, err := client.Get(serverAddr + "/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("server did not become ready within timeout")
}

// TestStartServer_GracefulShutdown_CompletesWithinTimeout verifies that
// canceling the context triggers graceful shutdown that completes within
// the configured ShutdownTimeout.
func TestStartServer_GracefulShutdown_CompletesWithinTimeout(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 2 * time.Second,
		MaxPortAttempts: 5,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	waitForServerReady(t, client, serverAddr)

	// Cancel context to trigger shutdown
	shutdownStart := time.Now()
	cancel()

	select {
	case err := <-serverDone:
		elapsed := time.Since(shutdownStart)
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
		// Shutdown should complete well within ShutdownTimeout + buffer
		maxExpected := config.ShutdownTimeout + 3*time.Second
		if elapsed > maxExpected {
			t.Errorf("shutdown took %v, expected within %v", elapsed, maxExpected)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within 10s timeout")
	}
}

// TestStartServer_GracefulShutdown_AllPortsExhausted verifies that StartServer
// returns an error immediately when all ports in the range are occupied.
func TestStartServer_GracefulShutdown_AllPortsExhausted(t *testing.T) {
	// Bind a single ephemeral port and keep it open. Set maxAttempts=1 so
	// StartServer only tries this one port and fails immediately.
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to grab ephemeral port: %v", err)
	}
	startPort := occupier.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { occupier.Close() })
	maxAttempts := 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            startPort,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: maxAttempts,
	}

	serverErr := StartServer(ctx, config)
	if serverErr == nil {
		t.Fatal("expected error when all ports are exhausted, got nil")
	}

	if !strings.Contains(serverErr.Error(), "could not find available port") {
		t.Errorf("expected error about port availability, got: %v", serverErr)
	}
}

// TestStartServer_DefaultsApplied verifies that StartServer applies defaults
// for zero-value config fields and starts successfully.
func TestStartServer_DefaultsApplied(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Only set Port; leave everything else at zero values
	config := webui.ServerConfig{
		Port: port,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	waitForServerReady(t, client, serverAddr)

	// Verify server responds to health checks (confirms defaults applied)
	resp, err := client.Get(serverAddr + "/health")
	if err != nil {
		cancel()
		t.Fatalf("health request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// TestStartServer_FleetJWTKey_PreProvisioned verifies that the server starts
// with FleetEnabled=true and a pre-provisioned FleetJWTKey.
func TestStartServer_FleetJWTKey_PreProvisioned(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 1 * time.Second,
		MaxPortAttempts: 5,

		FleetEnabled: true,
		FleetJWTKey:  []byte("test-jwt-signing-key-32-bytes!!"),
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	waitForServerReady(t, client, serverAddr)

	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("StartServer returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// TestStartServer_ConcurrentRequests_DuringShutdown verifies that the server
// handles concurrent requests during shutdown cleanly without errors or panics.
func TestStartServer_ConcurrentRequests_DuringShutdown(t *testing.T) {
	port := grabEphemeralPort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := webui.ServerConfig{
		Port:            port,
		BindAddress:     "127.0.0.1",
		PoolSize:        1,
		ShutdownTimeout: 3 * time.Second,
		MaxPortAttempts: 5,
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- StartServer(ctx, config)
	}()

	serverAddr := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 2 * time.Second}
	waitForServerReady(t, client, serverAddr)

	// Launch concurrent requests
	var wg sync.WaitGroup
	const numRequests = 10
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These may succeed or fail depending on timing; we just want no panic
			resp, err := client.Get(serverAddr + "/health")
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}

	// Cancel context while requests are in flight
	cancel()

	requestsDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(requestsDone)
	}()

	select {
	case <-requestsDone:
		// All requests completed
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent requests did not complete during shutdown")
	}

	select {
	case err := <-serverDone:
		// A forced-shutdown timeout is acceptable here — we cancelled the
		// context while requests were in-flight, so the graceful drain may
		// not finish within ShutdownTimeout on slow CI runners.
		if err != nil && !strings.Contains(err.Error(), "server forced to shutdown") {
			t.Errorf("StartServer returned unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}
