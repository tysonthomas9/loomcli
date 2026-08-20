package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/webui"
)

// startBootstrapTestServer boots a real Server (through the same StartServer
// path production uses) so requests exercise the ACTUAL app.mux routing --
// including the /api/ JSON-404 catch-all -- not a module mux in isolation.
func startBootstrapTestServer(t *testing.T, bootstrapEnabled bool) string {
	t.Helper()
	port := grabEphemeralPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- StartServer(ctx, webui.ServerConfig{
			Port:                 port,
			BindAddress:          "127.0.0.1",
			PoolSize:             1,
			ShutdownTimeout:      time.Second,
			MaxPortAttempts:      5,
			LeadBootstrapEnabled: bootstrapEnabled,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down within timeout")
		}
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 50; i++ {
		resp, err := client.Get(base + "/api/health")
		if err == nil {
			resp.Body.Close()
			return base
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server did not become ready within timeout")
	return ""
}

// TestBootstrapLoomRouteReachableThroughAppMux is the regression test for the
// integration bug where the route was mounted on the workspace sub-mux and thus
// unreachable at /api/lead/bootstrap/loom. It drives the real router.
func TestBootstrapLoomRouteReachableThroughAppMux(t *testing.T) {
	base := startBootstrapTestServer(t, true)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(base + placement.BootstrapLoomPath)
	if err != nil {
		t.Fatalf("GET %s: %v", placement.BootstrapLoomPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (route must be reachable on app.mux)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q, want application/octet-stream", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("served empty body, want the running binary")
	}
	// The served bytes must be serve's own running binary (the test binary here).
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	want, err := os.ReadFile(exe) //nolint:gosec // test reads its own executable
	if err != nil {
		t.Fatalf("read running binary: %v", err)
	}
	if len(body) != len(want) {
		t.Fatalf("served %d bytes, want the running binary (%d bytes)", len(body), len(want))
	}
}

// TestBootstrapLoomDisabledFallsThroughToCatchAll proves the disabled path is a
// mux miss handled by the /api/ JSON-404 catch-all, never the handler.
func TestBootstrapLoomDisabledFallsThroughToCatchAll(t *testing.T) {
	base := startBootstrapTestServer(t, false)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(base + placement.BootstrapLoomPath)
	if err != nil {
		t.Fatalf("GET %s: %v", placement.BootstrapLoomPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when bootstrap disabled", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json (the /api/ catch-all)", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := string(body); got != `{"error":"not found"}` {
		t.Fatalf("body = %q, want the /api/ JSON-404 catch-all body", got)
	}
}
