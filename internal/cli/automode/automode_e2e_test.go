//go:build e2e
// +build e2e

package automode

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
	os.MkdirAll(logDir, 0700)

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

func TestE2E_LogFileSkipsOldContent(t *testing.T) {
	skipIfNoTmux(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, ".loom", "logs")
	logFile := filepath.Join(logDir, "test-skip-old.log")

	// Create log directory and pre-populate with "old" content
	os.MkdirAll(logDir, 0700)
	oldContent := "OLD_SESSION_LINE_1\nOLD_SESSION_LINE_2\nOLD_SESSION_LINE_3\n"
	if err := os.WriteFile(logFile, []byte(oldContent), 0600); err != nil {
		t.Fatalf("Failed to write old content: %v", err)
	}

	// Get the size of old content (this is where new streaming should start)
	oldSize := int64(len(oldContent))

	sessionName := uniqueSessionName(t)

	// Create session that outputs new content
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		"sleep 0.5 && echo 'NEW_SESSION_MARKER' && sleep 1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer cleanupTmuxSession(sessionName)

	// Setup logging (appends to existing file)
	quotedPath := shellQuote(logFile)
	exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", "cat >> "+quotedPath).Run()

	// Simulate the offset initialization from streamUntilExit
	var lastOffset int64 = 0
	if info, err := os.Stat(logFile); err == nil {
		lastOffset = info.Size()
	}

	// Offset should start at end of old content
	if lastOffset != oldSize {
		t.Errorf("Initial offset = %d, want %d (should skip old content)", lastOffset, oldSize)
	}

	// Wait for new output
	time.Sleep(2 * time.Second)

	// Read only new content (from lastOffset)
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	newContent := string(content[lastOffset:])

	// New content should contain the marker
	if !strings.Contains(newContent, "NEW_SESSION_MARKER") {
		t.Errorf("New content should contain 'NEW_SESSION_MARKER', got: %s", newContent)
	}

	// New content should NOT contain old session lines
	if strings.Contains(newContent, "OLD_SESSION_LINE") {
		t.Errorf("New content should NOT contain old session content, got: %s", newContent)
	}
}

func TestE2E_SilenceDetectionAfterSignal(t *testing.T) {
	skipIfNoTmux(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, ".loom", "logs")
	logFile := filepath.Join(logDir, "test-silence.log")
	signalFile := filepath.Join(tmpDir, ".loom", "task-complete")

	// Create directories
	os.MkdirAll(logDir, 0700)

	sessionName := uniqueSessionName(t)

	// Create session that waits before outputting so pipe-pane can be set up first.
	// Without the initial sleep, BEFORE_SIGNAL would echo before pipe-pane is active.
	script := fmt.Sprintf(`
		sleep 1
		echo 'BEFORE_SIGNAL'
		sleep 0.5
		touch %s
		echo 'AFTER_SIGNAL_1'
		sleep 0.3
		echo 'AFTER_SIGNAL_2'
		sleep 5
	`, signalFile)

	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sh", "-c", script)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer cleanupTmuxSession(sessionName)

	// Setup logging immediately after session creation (before script's initial sleep ends)
	quotedPath := shellQuote(logFile)
	exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", "cat >> "+quotedPath).Run()

	// Wait for signal file to be created
	deadline := time.Now().Add(5 * time.Second)
	signalReceived := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(signalFile); err == nil {
			signalReceived = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !signalReceived {
		t.Fatal("Signal file was never created")
	}

	// Remove signal file (as streamUntilExit would)
	os.Remove(signalFile)

	// Simulate silence detection loop (wait for 3s of silence or 10s max)
	const silenceTimeout = 3 * time.Second
	const maxWait = 10 * time.Second
	lastActivity := time.Now()
	loopDeadline := time.Now().Add(maxWait)
	var lastOffset int64 = 0

	for time.Now().Before(loopDeadline) {
		prevOffset := lastOffset
		streamRemainingLogContent(logFile, &lastOffset)
		if lastOffset > prevOffset {
			lastActivity = time.Now()
		} else if time.Since(lastActivity) >= silenceTimeout {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Read final content
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	contentStr := string(content)

	// Should have captured content before AND after signal
	if !strings.Contains(contentStr, "BEFORE_SIGNAL") {
		t.Error("Should contain content before signal")
	}
	if !strings.Contains(contentStr, "AFTER_SIGNAL_1") {
		t.Error("Should contain first output after signal")
	}
	if !strings.Contains(contentStr, "AFTER_SIGNAL_2") {
		t.Error("Should contain second output after signal (silence detection should wait)")
	}
}
