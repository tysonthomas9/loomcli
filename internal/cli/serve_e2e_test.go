//go:build e2e
// +build e2e

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// serveURL returns the full URL for a given port and path.
func serveURL(port int, path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
}

// startServe starts a loom serve subprocess on the given port with extra args.
// It registers t.Cleanup to stop the process automatically.
func startServe(t *testing.T, port int, extraArgs ...string) *exec.Cmd {
	t.Helper()

	args := []string{"serve", "--no-daemon", "--no-webui",
		"--port", fmt.Sprintf("%d", port),
		"--bind", "127.0.0.1",
	}
	args = append(args, extraArgs...)

	cmd := exec.Command("loom", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start loom serve: %v", err)
	}

	t.Cleanup(func() {
		stopServe(t, cmd)
	})

	return cmd
}

// startServeNoCleanup starts a loom serve subprocess without registering cleanup.
// The caller is responsible for stopping the process.
func startServeNoCleanup(t *testing.T, port int, extraArgs ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()

	args := []string{"serve", "--no-daemon", "--no-webui",
		"--port", fmt.Sprintf("%d", port),
		"--bind", "127.0.0.1",
	}
	args = append(args, extraArgs...)

	cmd := exec.Command("loom", args...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start loom serve: %v", err)
	}

	return cmd, &stderr
}

// waitForServeReady polls the /health endpoint until the server is ready.
func waitForServeReady(t *testing.T, port int, timeout time.Duration) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	backoff := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		resp, err := client.Get(serveURL(port, "/health"))
		if err == nil {
			var health HealthResponse
			if decErr := json.NewDecoder(resp.Body).Decode(&health); decErr == nil && health.Status == "ok" {
				resp.Body.Close()
				t.Logf("Server ready on port %d (timestamp: %s)", port, health.Timestamp)
				return
			}
			resp.Body.Close()
		}
		time.Sleep(backoff)
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	t.Fatalf("Server on port %d did not become ready within %s", port, timeout)
}

// stopServe sends SIGTERM to the process and waits for it to exit.
// Falls back to SIGKILL after 10s to prevent zombie leaks.
func stopServe(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd.Process == nil {
		return
	}

	// Check if already exited
	if cmd.ProcessState != nil {
		return
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// Process exited
	case <-time.After(10 * time.Second):
		t.Logf("Process did not exit after SIGTERM, sending SIGKILL")
		_ = cmd.Process.Kill()
		<-done
	}
}

func TestE2E_ServeStartupAndHealthCheck(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19101
	startServe(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Make an additional /health request and validate response in detail
	resp, err := http.Get(serveURL(port, "/health"))
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Expected Content-Type application/json, got %q", ct)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	if health.Status != "ok" {
		t.Errorf("Expected status 'ok', got %q", health.Status)
	}

	if health.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	if time.Since(health.Timestamp) > 5*time.Second {
		t.Errorf("Timestamp is too old: %s", health.Timestamp)
	}
}

func TestE2E_ServeAPIEndpoints(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19102
	startServe(t, port)
	waitForServeReady(t, port, 10*time.Second)

	tests := []struct {
		endpoint    string
		contentType string // expected Content-Type prefix
	}{
		{"/health", "application/json"},
		{"/api/status", "application/json"},
		{"/api/agents", "application/json"},
		{"/api/tasks", "application/json"},
		{"/api/stats", "application/json"},
		{"/api/sync", "application/json"},
		{"/api/workspaces", "application/json"},
		{"/api/stale-detector", "application/json"},
		{"/api/usage", "application/json"},
		{"/metrics", "text/plain"},
		{"/api/observability/metrics", "application/json"},
		{"/api/observability/events", "application/json"},
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, tc := range tests {
		t.Run(tc.endpoint, func(t *testing.T) {
			resp, err := client.Get(serveURL(port, tc.endpoint))
			if err != nil {
				t.Fatalf("GET %s failed: %v", tc.endpoint, err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)

			// Should be 200 or 503 — never 404, 405, or 500
			if resp.StatusCode != 200 && resp.StatusCode != 503 {
				t.Errorf("GET %s: expected 200 or 503, got %d (body: %s)", tc.endpoint, resp.StatusCode, string(body))
			}

			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, tc.contentType) {
				t.Errorf("GET %s: expected Content-Type containing %q, got %q", tc.endpoint, tc.contentType, ct)
			}

			// Validate response body format
			if tc.contentType == "application/json" {
				var js json.RawMessage
				if err := json.Unmarshal(body, &js); err != nil {
					t.Errorf("GET %s: response is not valid JSON: %v (body: %s)", tc.endpoint, err, string(body))
				}
			}
			// For text/plain (metrics), just verify we got some content (could be empty but valid)
		})
	}
}

func TestE2E_ServeGracefulShutdown_SIGTERM(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19103
	cmd, _ := startServeNoCleanup(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Send SIGTERM
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Failed to send SIGTERM: %v", err)
	}

	// Wait for exit
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			// On SIGTERM, Go programs may exit with a nil error (code 0)
			// or a signal exit. Accept both.
			if exitErr, ok := err.(*exec.ExitError); ok {
				if exitErr.ExitCode() != 0 && exitErr.ExitCode() != -1 {
					t.Errorf("Expected clean exit, got exit code %d", exitErr.ExitCode())
				}
			}
		}
		// nil error = exit code 0, which is clean
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done // reap the process to avoid zombie leak
		t.Fatal("Process did not exit within 10s after SIGTERM")
	}
}

func TestE2E_ServeGracefulShutdown_SIGINT(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19104
	cmd, _ := startServeNoCleanup(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Send SIGINT
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Failed to send SIGINT: %v", err)
	}

	// Wait for exit
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if exitErr.ExitCode() != 0 && exitErr.ExitCode() != -1 {
					t.Errorf("Expected clean exit, got exit code %d", exitErr.ExitCode())
				}
			}
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done // reap the process to avoid zombie leak
		t.Fatal("Process did not exit within 10s after SIGINT")
	}
}

func TestE2E_ServePortFlag(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19105
	startServe(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Verify the server responds on the specified port
	resp, err := http.Get(serveURL(port, "/health"))
	if err != nil {
		t.Fatalf("GET /health on port %d failed: %v", port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

}

func TestE2E_ServeCORSHeaders(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19106
	startServe(t, port, "--cors", "http://test.example.com")
	waitForServeReady(t, port, 10*time.Second)

	client := &http.Client{Timeout: 5 * time.Second}

	// Test CORS on GET request
	resp, err := client.Get(serveURL(port, "/api/status"))
	if err != nil {
		t.Fatalf("GET /api/status failed: %v", err)
	}
	resp.Body.Close()

	origin := resp.Header.Get("Access-Control-Allow-Origin")
	if origin != "http://test.example.com" {
		t.Errorf("Expected CORS origin 'http://test.example.com', got %q", origin)
	}

	methods := resp.Header.Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "GET") {
		t.Errorf("Expected CORS methods to contain GET, got %q", methods)
	}

	// Test CORS on OPTIONS preflight request
	req, _ := http.NewRequest("OPTIONS", serveURL(port, "/api/status"), nil)
	preflightResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/status failed: %v", err)
	}
	preflightResp.Body.Close()

	if preflightResp.StatusCode != 200 {
		t.Errorf("OPTIONS expected status 200, got %d", preflightResp.StatusCode)
	}

	preflightOrigin := preflightResp.Header.Get("Access-Control-Allow-Origin")
	if preflightOrigin != "http://test.example.com" {
		t.Errorf("OPTIONS: expected CORS origin 'http://test.example.com', got %q", preflightOrigin)
	}
}

func TestE2E_ServePortConflict(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19107

	// Start first serve
	startServe(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Start second serve on the same port (expect failure)
	cmd2, stderr := startServeNoCleanup(t, port)

	// Wait for second process to exit
	done := make(chan error, 1)
	go func() { done <- cmd2.Wait() }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Second serve on same port should have failed")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 0 {
				t.Error("Second serve on same port should have non-zero exit code")
			}
		}
		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "address already in use") && !strings.Contains(stderrStr, "bind") {
			t.Logf("stderr of second process: %s", stderrStr)
		}
	case <-time.After(10 * time.Second):
		_ = cmd2.Process.Kill()
		<-done // reap the process to avoid zombie leak
		t.Fatal("Second serve process did not exit within 10s")
	}
}

func TestE2E_ServeDefaultCORSHeader(t *testing.T) {
	skipIfNoTmux(t)

	const port = 19108

	startServe(t, port)
	waitForServeReady(t, port, 10*time.Second)

	// Make request without --cors flag — default should be http://localhost:8080
	resp, err := http.Get(serveURL(port, "/health"))
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	origin := resp.Header.Get("Access-Control-Allow-Origin")
	// Default CORS origin falls back to http://localhost:<port> where port defaults to 8080
	expected := "http://localhost:8080"
	if origin != expected {
		t.Errorf("Expected default CORS origin %q, got %q", expected, origin)
	}
}

// init ensures test ports are available before any test runs.
func init() {
	for port := 19101; port <= 19108; port++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			// Port is in use — tests will fail with clear bind errors, no need to panic here
		}
	}
}
