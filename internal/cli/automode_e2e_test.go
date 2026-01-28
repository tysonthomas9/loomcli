//go:build e2e
// +build e2e

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// skipIfNoTmux skips the test if tmux is not available
func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}
}

// uniqueSessionName generates a unique session name for tests
func uniqueSessionName(t *testing.T) string {
	return fmt.Sprintf("loom-e2e-test-%d-%d", os.Getpid(), time.Now().UnixNano()%10000)
}

// waitForSession waits for tmux session to exist or not exist
func waitForSession(name string, shouldExist bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists := tmuxSessionExists(name)
		if exists == shouldExist {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestE2E_TmuxSessionLifecycle(t *testing.T) {
	skipIfNoTmux(t)

	sessionName := uniqueSessionName(t)

	// Create session running a simple command that exits
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "echo hello && sleep 1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Session should exist
	if !waitForSession(sessionName, true, 2*time.Second) {
		t.Fatal("Session did not start")
	}

	// Wait for session to exit naturally
	if !waitForSession(sessionName, false, 5*time.Second) {
		cleanupTmuxSession(sessionName)
		t.Fatal("Session did not exit within timeout")
	}
}

func TestE2E_TmuxOutputStreaming(t *testing.T) {
	skipIfNoTmux(t)

	sessionName := uniqueSessionName(t)

	// Create session that outputs specific text
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		"echo 'TEST_OUTPUT_MARKER' && sleep 2")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer cleanupTmuxSession(sessionName)

	// Wait for session to start
	time.Sleep(500 * time.Millisecond)

	// Capture pane output
	out, err := exec.Command("tmux", "capture-pane", "-t", sessionName, "-p").Output()
	if err != nil {
		t.Fatalf("Failed to capture pane: %v", err)
	}

	if !strings.Contains(string(out), "TEST_OUTPUT_MARKER") {
		t.Errorf("Expected output to contain 'TEST_OUTPUT_MARKER', got: %s", out)
	}
}

func TestE2E_TmuxSessionCleanupOnShutdown(t *testing.T) {
	skipIfNoTmux(t)

	sessionName := uniqueSessionName(t)

	// Create a long-running session
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 60")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists
	if !waitForSession(sessionName, true, 2*time.Second) {
		t.Fatal("Session did not start")
	}

	// Cleanup (simulating what happens on Ctrl+C)
	cleanupTmuxSession(sessionName)

	// Verify session is gone
	if !waitForSession(sessionName, false, 2*time.Second) {
		t.Error("Session was not cleaned up")
	}
}

func TestE2E_TmuxLogFileCreated(t *testing.T) {
	skipIfNoTmux(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, ".loom", "logs")
	logFile := filepath.Join(logDir, "test-agent.log")

	sessionName := uniqueSessionName(t)

	// Create log directory
	os.MkdirAll(logDir, 0755)

	// Create session that waits before outputting (so pipe-pane can be set up first)
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		"sleep 1 && echo 'LOG_TEST_CONTENT' && sleep 1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer cleanupTmuxSession(sessionName)

	// Setup logging immediately after session creation
	quotedPath := shellQuote(logFile)
	exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", "cat >> "+quotedPath).Run()

	// Wait for output to be written (sleep 1 + echo + sleep 1)
	time.Sleep(3 * time.Second)

	// Check log file exists and contains output
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "LOG_TEST_CONTENT") {
		t.Errorf("Log file should contain 'LOG_TEST_CONTENT', got: %s", content)
	}
}

func TestE2E_StartTmuxSession(t *testing.T) {
	skipIfNoTmux(t)

	sessionName := uniqueSessionName(t)

	// Kill any existing session
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	// Create detached session with simple command
	if err := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		"echo 'Session started' && sleep 2").Run(); err != nil {
		t.Fatalf("tmux new-session failed: %v", err)
	}
	defer cleanupTmuxSession(sessionName)

	// Verify session exists
	if !tmuxSessionExists(sessionName) {
		t.Error("Session should exist after creation")
	}

	// Wait for it to complete
	time.Sleep(3 * time.Second)

	// Should have exited
	if tmuxSessionExists(sessionName) {
		t.Error("Session should have exited")
	}
}
