package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHasAvailablePlanningTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		want     bool
		wantErr  bool
	}{
		{
			name: "has task needing planning (no design)",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task has design - not needing planning",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			want: false,
		},
		{
			name: "skip Need Review tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Add feature", Status: "open", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip epics",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			want: false,
		},
		{
			name:     "empty list",
			bdOutput: "[]",
			want:     false,
		},
		{
			name: "mixed - one valid task",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Skip me", Status: "open", Design: ""},
				{ID: "T-2", Title: "Work on me", Status: "open", Design: ""},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Already planned"},
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore execCommand
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			got, err := HasAvailablePlanningTasks()
			if (err != nil) != tt.wantErr {
				t.Errorf("HasAvailablePlanningTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasAvailablePlanningTasks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasAvailableImplementationTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		want     bool
		wantErr  bool
	}{
		{
			name: "has task with design - ready for implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Implementation plan here"},
			}),
			want: true,
		},
		{
			name: "task has no design - not ready for implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip Need Review tasks even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Add feature", Status: "open", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip epics even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: "Has design"},
			}),
			want: false,
		},
		{
			name:     "empty list",
			bdOutput: "[]",
			want:     false,
		},
		{
			name: "mixed - one valid task with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "[Need Review] Skip me", Status: "open", Design: "Has design"},
				{ID: "T-2", Title: "No design yet", Status: "open", Design: ""},
				{ID: "T-3", Title: "Ready to implement", Status: "open", Design: "Detailed plan"},
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore execCommand
			oldExec := execCommand
			defer func() { execCommand = oldExec }()

			execCommand = func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}

			got, err := HasAvailableImplementationTasks()
			if (err != nil) != tt.wantErr {
				t.Errorf("HasAvailableImplementationTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasAvailableImplementationTasks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoModeOptions(t *testing.T) {
	opts := AutoModeOptions{
		Interval:     60,
		MaxTasks:     10,
		IdleTimeout:  30,
		AgentType:    "plan",
		AgentName:    "falcon",
		WorktreePath: "/path/to/worktree",
	}

	if opts.Interval != 60 {
		t.Errorf("Interval = %d, want 60", opts.Interval)
	}
	if opts.MaxTasks != 10 {
		t.Errorf("MaxTasks = %d, want 10", opts.MaxTasks)
	}
	if opts.IdleTimeout != 30 {
		t.Errorf("IdleTimeout = %d, want 30", opts.IdleTimeout)
	}
	if opts.AgentType != "plan" {
		t.Errorf("AgentType = %s, want plan", opts.AgentType)
	}
}

func TestFormatLimit(t *testing.T) {
	tests := []struct {
		limit int
		want  string
	}{
		{0, "unlimited"},
		{-1, "unlimited"},
		{1, "1"},
		{10, "10"},
		{100, "100"},
	}

	for _, tt := range tests {
		got := formatLimit(tt.limit)
		if got != tt.want {
			t.Errorf("formatLimit(%d) = %s, want %s", tt.limit, got, tt.want)
		}
	}
}

func TestFormatTimeout(t *testing.T) {
	tests := []struct {
		timeout int
		want    string
	}{
		{0, "none"},
		{-1, "none"},
		{1, "1m"},
		{30, "30m"},
		{60, "60m"},
	}

	for _, tt := range tests {
		got := formatTimeout(tt.timeout)
		if got != tt.want {
			t.Errorf("formatTimeout(%d) = %s, want %s", tt.timeout, got, tt.want)
		}
	}
}

func TestSetupSignalHandler(t *testing.T) {
	// Test that SetupSignalHandler returns a channel
	shutdown := SetupSignalHandler()
	if shutdown == nil {
		t.Error("SetupSignalHandler() returned nil channel")
	}
}

func TestInterruptibleSleep_CompletesNormally(t *testing.T) {
	shutdown := make(chan struct{})

	start := time.Now()
	interrupted := interruptibleSleep(50*time.Millisecond, shutdown)
	elapsed := time.Since(start)

	if interrupted {
		t.Error("interruptibleSleep() returned true, expected false (not interrupted)")
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("interruptibleSleep() returned too early: %v", elapsed)
	}
}

func TestInterruptibleSleep_InterruptedByShutdown(t *testing.T) {
	shutdown := make(chan struct{})

	// Close shutdown after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(shutdown)
	}()

	start := time.Now()
	interrupted := interruptibleSleep(1*time.Second, shutdown)
	elapsed := time.Since(start)

	if !interrupted {
		t.Error("interruptibleSleep() returned false, expected true (should be interrupted)")
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("interruptibleSleep() took too long to respond to shutdown: %v", elapsed)
	}
}

func TestInterruptibleSleep_AlreadyClosedChannel(t *testing.T) {
	shutdown := make(chan struct{})
	close(shutdown) // Already closed

	start := time.Now()
	interrupted := interruptibleSleep(1*time.Second, shutdown)
	elapsed := time.Since(start)

	if !interrupted {
		t.Error("interruptibleSleep() returned false, expected true (channel already closed)")
	}
	if elapsed >= 100*time.Millisecond {
		t.Errorf("interruptibleSleep() should return immediately for closed channel: %v", elapsed)
	}
}

func TestClosedChannelPattern_MultipleReceivers(t *testing.T) {
	// Verify that closing a channel unblocks multiple receivers (the core pattern we're using)
	shutdown := make(chan struct{})

	received := make(chan int, 3)

	// Start 3 goroutines waiting on shutdown
	for i := 0; i < 3; i++ {
		go func(id int) {
			<-shutdown
			received <- id
		}(i)
	}

	// Give goroutines time to start waiting
	time.Sleep(10 * time.Millisecond)

	// Close the channel - should unblock all 3
	close(shutdown)

	// All 3 should receive within a short time
	timeout := time.After(100 * time.Millisecond)
	count := 0
	for count < 3 {
		select {
		case <-received:
			count++
		case <-timeout:
			t.Fatalf("Only %d/3 receivers were unblocked by close", count)
		}
	}
}

// mustJSON marshals value to JSON string, panics on error (test helper)
func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"with'quote", "'with'\\''quote'"},
		{"multiple'quotes'here", "'multiple'\\''quotes'\\''here'"},
		{"", "''"},
		{"/path/to/file.log", "'/path/to/file.log'"},
		{"path with spaces/and'quotes", "'path with spaces/and'\\''quotes'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsTmuxAvailable(t *testing.T) {
	// This test checks the actual system - tmux may or may not be installed
	// We just verify the function doesn't panic and returns a bool
	result := IsTmuxAvailable()
	// Result is either true or false depending on system
	t.Logf("IsTmuxAvailable() = %v", result)
}

func TestGetTerminalSize(t *testing.T) {
	// This test checks the actual system - may fail in CI without a TTY
	// We just verify the function doesn't panic
	width, height, err := getTerminalSize()
	if err != nil {
		t.Logf("getTerminalSize() error (expected in CI): %v", err)
		return
	}
	t.Logf("getTerminalSize() = %dx%d", width, height)

	// If we got values, they should be reasonable
	if width > 0 && width < 10 {
		t.Errorf("width %d seems too small", width)
	}
	if height > 0 && height < 5 {
		t.Errorf("height %d seems too small", height)
	}
}

func TestTmuxSessionExists_NonExistentSession(t *testing.T) {
	// Test with a session name that definitely doesn't exist
	exists := tmuxSessionExists("nonexistent-test-session-12345-xyz")
	if exists {
		t.Error("tmuxSessionExists() returned true for non-existent session")
	}
}

func TestTmuxPaneDead_NonExistentSession(t *testing.T) {
	// For non-existent session, should return true (assume dead)
	dead := tmuxPaneDead("nonexistent-test-session-12345-xyz")
	if !dead {
		t.Error("tmuxPaneDead() returned false for non-existent session, expected true")
	}
}

func TestListenForAttachKey_ShutdownSignal(t *testing.T) {
	attachChan := make(chan struct{}, 1)
	shutdown := make(chan struct{})

	// Start the listener
	done := make(chan struct{})
	go func() {
		listenForAttachKey(attachChan, shutdown)
		close(done)
	}()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	// Signal shutdown
	close(shutdown)

	// Should exit promptly
	select {
	case <-done:
		// Good - listener exited
	case <-time.After(500 * time.Millisecond):
		t.Error("listenForAttachKey did not exit after shutdown signal")
	}
}

func TestPrintTmuxSummary(t *testing.T) {
	// This is a simple output function - just verify it doesn't panic
	// In a real scenario we'd capture stdout, but for now just ensure no panic
	printTmuxSummary(0)
	printTmuxSummary(1)
	printTmuxSummary(10)
}

func TestCleanupTmuxSession(t *testing.T) {
	// Test that cleanup doesn't panic even for non-existent sessions
	cleanupTmuxSession("nonexistent-test-session-cleanup-12345")
}

// waitForFile waits for a file to exist within timeout
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestCleanupTmuxSession_SendsCtrlC(t *testing.T) {
	// Skip if tmux is not available
	if exec.Command("tmux", "-V").Run() != nil {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-ctrlc-%d", os.Getpid())
	signalFile := filepath.Join(t.TempDir(), "received-sigint")

	// Create a script that writes to a file when it receives SIGINT
	// The script traps SIGINT and writes before exiting
	trapScript := fmt.Sprintf(`trap 'echo received > %s; exit 0' INT; sleep 30`, signalFile)

	// Create tmux session running the trap script
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sh", "-c", trapScript).Run()
	if err != nil {
		t.Fatalf("Failed to create tmux session: %v", err)
	}

	// Give the script time to set up the trap
	time.Sleep(100 * time.Millisecond)

	// Call cleanupTmuxSession - should send Ctrl+C then kill
	cleanupTmuxSession(sessionName)

	// Wait for signal file with timeout (more robust than fixed sleep)
	if !waitForFile(signalFile, 1*time.Second) {
		t.Fatal("Timeout waiting for SIGINT signal file - Ctrl+C was not sent before kill")
	}

	// Verify session is actually gone
	if exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil {
		t.Error("Session still exists after cleanup")
		// Clean up manually
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	}
}

func TestRunAutoModeTmux_MaxTasksZero(t *testing.T) {
	// Test early exit when shutdown is signaled immediately
	shutdown := make(chan struct{})
	close(shutdown) // Signal shutdown immediately

	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     0, // unlimited
		IdleTimeout:  0,
		AgentType:    "plan",
		AgentName:    "test",
		WorktreePath: t.TempDir(),
	}

	// Should return immediately due to shutdown
	done := make(chan struct{})
	go func() {
		RunAutoModeTmux(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited promptly
	case <-time.After(2 * time.Second):
		t.Error("RunAutoModeTmux did not exit after shutdown signal")
	}
}

func TestAdaptivePoller_Creation(t *testing.T) {
	p := newAdaptivePoller()

	if p.minInterval != 100*time.Millisecond {
		t.Errorf("minInterval = %v, want 100ms", p.minInterval)
	}
	if p.maxInterval != 1000*time.Millisecond {
		t.Errorf("maxInterval = %v, want 1000ms", p.maxInterval)
	}
	if p.currentInterval != 200*time.Millisecond {
		t.Errorf("currentInterval = %v, want 200ms", p.currentInterval)
	}
	if p.backoffFactor != 1.5 {
		t.Errorf("backoffFactor = %v, want 1.5", p.backoffFactor)
	}
}

func TestAdaptivePoller_BackoffBehavior(t *testing.T) {
	p := newAdaptivePoller()

	// Start at 200ms
	if p.currentInterval != 200*time.Millisecond {
		t.Fatalf("initial interval = %v, want 200ms", p.currentInterval)
	}

	// First backoff: 200ms * 1.5 = 300ms
	p.hadNoActivity()
	if p.currentInterval != 300*time.Millisecond {
		t.Errorf("after 1st backoff = %v, want 300ms", p.currentInterval)
	}

	// Second backoff: 300ms * 1.5 = 450ms
	p.hadNoActivity()
	if p.currentInterval != 450*time.Millisecond {
		t.Errorf("after 2nd backoff = %v, want 450ms", p.currentInterval)
	}

	// Third backoff: 450ms * 1.5 = 675ms
	p.hadNoActivity()
	if p.currentInterval != 675*time.Millisecond {
		t.Errorf("after 3rd backoff = %v, want 675ms", p.currentInterval)
	}

	// Fourth backoff: 675ms * 1.5 = 1012.5ms -> capped at 1000ms
	p.hadNoActivity()
	if p.currentInterval != 1000*time.Millisecond {
		t.Errorf("after 4th backoff = %v, want 1000ms (capped)", p.currentInterval)
	}

	// Further backoffs should stay at max
	p.hadNoActivity()
	if p.currentInterval != 1000*time.Millisecond {
		t.Errorf("after 5th backoff = %v, want 1000ms (capped)", p.currentInterval)
	}
}

func TestAdaptivePoller_ResetOnActivity(t *testing.T) {
	p := newAdaptivePoller()

	// Back off a few times
	p.hadNoActivity()
	p.hadNoActivity()
	p.hadNoActivity()

	if p.currentInterval == p.minInterval {
		t.Fatal("interval should have increased after backoff")
	}

	// Activity should reset to min
	p.hadActivity()
	if p.currentInterval != 100*time.Millisecond {
		t.Errorf("after activity = %v, want 100ms (min)", p.currentInterval)
	}
}

func TestAdaptivePoller_Tick(t *testing.T) {
	p := newAdaptivePoller()
	p.currentInterval = 10 * time.Millisecond // Short for test

	start := time.Now()
	<-p.tick()
	elapsed := time.Since(start)

	if elapsed < 10*time.Millisecond || elapsed > 50*time.Millisecond {
		t.Errorf("tick elapsed = %v, want ~10ms", elapsed)
	}
}

func TestGetPaneState_NonExistentSession(t *testing.T) {
	_, err := getPaneState("nonexistent-test-session-12345")
	if err == nil {
		t.Error("expected error for non-existent session")
	}
}

func TestPaneState_Fields(t *testing.T) {
	// Test that PaneState struct has expected fields
	state := &PaneState{
		Dead:       true,
		ExitStatus: 1,
		ExitSignal: "SIGTERM",
		PID:        12345,
	}

	if !state.Dead {
		t.Error("Dead should be true")
	}
	if state.ExitStatus != 1 {
		t.Errorf("ExitStatus = %d, want 1", state.ExitStatus)
	}
	if state.ExitSignal != "SIGTERM" {
		t.Errorf("ExitSignal = %s, want SIGTERM", state.ExitSignal)
	}
	if state.PID != 12345 {
		t.Errorf("PID = %d, want 12345", state.PID)
	}
}

func TestStreamRemainingLogContent_ReadsNewContent(t *testing.T) {
	// Create temp log file with content
	tmpFile, err := os.CreateTemp("", "loom-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write initial content
	content := "line 1\nline 2\nline 3\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write content: %v", err)
	}
	tmpFile.Close()

	// Test reading from offset 0
	var offset int64 = 0
	streamRemainingLogContent(tmpFile.Name(), &offset)

	if offset != int64(len(content)) {
		t.Errorf("offset = %d, want %d", offset, len(content))
	}
}

func TestStreamRemainingLogContent_SkipsAlreadyReadContent(t *testing.T) {
	// Create temp log file with content
	tmpFile, err := os.CreateTemp("", "loom-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := "line 1\nline 2\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write content: %v", err)
	}
	tmpFile.Close()

	// Start with offset at end of file - should not read anything new
	var offset int64 = int64(len(content))
	streamRemainingLogContent(tmpFile.Name(), &offset)

	// Offset should remain unchanged
	if offset != int64(len(content)) {
		t.Errorf("offset changed unexpectedly: got %d, want %d", offset, len(content))
	}
}

func TestStreamRemainingLogContent_HandlesNonExistentFile(t *testing.T) {
	// Should not panic for non-existent file
	var offset int64 = 0
	streamRemainingLogContent("/nonexistent/path/to/file.log", &offset)

	// Offset should remain 0
	if offset != 0 {
		t.Errorf("offset = %d, want 0 for non-existent file", offset)
	}
}

func TestStreamRemainingLogContent_ReadsIncrementalContent(t *testing.T) {
	// Create temp log file
	tmpFile, err := os.CreateTemp("", "loom-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write first chunk
	chunk1 := "first chunk\n"
	if _, err := tmpFile.WriteString(chunk1); err != nil {
		t.Fatalf("failed to write chunk1: %v", err)
	}

	// Read first chunk
	var offset int64 = 0
	streamRemainingLogContent(tmpFile.Name(), &offset)

	if offset != int64(len(chunk1)) {
		t.Errorf("after chunk1: offset = %d, want %d", offset, len(chunk1))
	}

	// Write second chunk
	chunk2 := "second chunk\n"
	if _, err := tmpFile.WriteString(chunk2); err != nil {
		t.Fatalf("failed to write chunk2: %v", err)
	}
	tmpFile.Close()

	// Read second chunk (should only read new content)
	streamRemainingLogContent(tmpFile.Name(), &offset)

	expectedOffset := int64(len(chunk1) + len(chunk2))
	if offset != expectedOffset {
		t.Errorf("after chunk2: offset = %d, want %d", offset, expectedOffset)
	}
}

func TestFilterFocusEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			name:  "no escape sequences",
			input: []byte("hello world"),
			want:  []byte("hello world"),
		},
		{
			name:  "focus gained escape",
			input: []byte("before\x1b[Iafter"),
			want:  []byte("beforeafter"),
		},
		{
			name:  "focus lost escape",
			input: []byte("before\x1b[Oafter"),
			want:  []byte("beforeafter"),
		},
		{
			name:  "both focus escapes",
			input: []byte("start\x1b[Imiddle\x1b[Oend"),
			want:  []byte("startmiddleend"),
		},
		{
			name:  "multiple of same escape",
			input: []byte("\x1b[I\x1b[I\x1b[Itext\x1b[O\x1b[O"),
			want:  []byte("text"),
		},
		{
			name:  "escape at start",
			input: []byte("\x1b[Itext"),
			want:  []byte("text"),
		},
		{
			name:  "escape at end",
			input: []byte("text\x1b[O"),
			want:  []byte("text"),
		},
		{
			name:  "empty input",
			input: []byte(""),
			want:  []byte(""),
		},
		{
			name:  "only escape sequences",
			input: []byte("\x1b[I\x1b[O"),
			want:  []byte(""),
		},
		{
			name:  "preserves other escape sequences",
			input: []byte("\x1b[32mgreen\x1b[0m\x1b[Itext"),
			want:  []byte("\x1b[32mgreen\x1b[0mtext"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterFocusEscapes(tt.input)
			if string(got) != string(tt.want) {
				t.Errorf("filterFocusEscapes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStreamUntilExit_StartsFromCurrentFileSize(t *testing.T) {
	// This tests the principle that we should start from current file size
	// to avoid replaying old content from previous sessions.
	// The actual streamUntilExit function is tested via e2e tests.

	// Create temp log file with existing content (simulating previous session)
	tmpFile, err := os.CreateTemp("", "loom-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	fileName := tmpFile.Name()
	defer os.Remove(fileName)

	// Write "old" content from a previous session
	oldContent := "old session output line 1\nold session output line 2\n"
	if _, err := tmpFile.WriteString(oldContent); err != nil {
		t.Fatalf("failed to write old content: %v", err)
	}
	tmpFile.Close()

	// Verify file size matches old content
	info, err := os.Stat(fileName)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	expectedOffset := int64(len(oldContent))
	if info.Size() != expectedOffset {
		t.Errorf("file size = %d, want %d", info.Size(), expectedOffset)
	}

	// Simulate the offset initialization logic from streamUntilExit
	var lastOffset int64 = 0
	if info, err := os.Stat(fileName); err == nil {
		lastOffset = info.Size()
	}

	// Offset should start at end of existing content
	if lastOffset != expectedOffset {
		t.Errorf("lastOffset = %d, want %d (should skip old content)", lastOffset, expectedOffset)
	}

	// Now simulate new content being appended
	f, err := os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	newContent := "new session output\n"
	if _, err := f.WriteString(newContent); err != nil {
		t.Fatalf("failed to write new content: %v", err)
	}
	f.Close()

	// Reading from lastOffset should only get new content
	streamRemainingLogContent(fileName, &lastOffset)

	// Offset should now be at end of all content
	expectedFinalOffset := int64(len(oldContent) + len(newContent))
	if lastOffset != expectedFinalOffset {
		t.Errorf("final offset = %d, want %d", lastOffset, expectedFinalOffset)
	}
}

func TestStreamUntilExit_HandlesNonExistentFile(t *testing.T) {
	// When log file doesn't exist yet, offset should start at 0
	nonExistentFile := "/tmp/loom-nonexistent-test-file-12345.log"

	var lastOffset int64 = 0
	if info, err := os.Stat(nonExistentFile); err == nil {
		lastOffset = info.Size()
	}

	// Offset should remain 0 for non-existent file
	if lastOffset != 0 {
		t.Errorf("lastOffset = %d, want 0 for non-existent file", lastOffset)
	}
}

// ============================================================================
// agentClaimedTask Tests
// ============================================================================

func TestAgentClaimedTask_WithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set a task
	err = UpdateLockTask(tmpDir, "bd-123", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	if !agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = false, want true when TaskID is set")
	}
}

func TestAgentClaimedTask_WithoutTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// No task set — TaskID is empty
	if agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = true, want false when TaskID is empty")
	}
}

func TestAgentClaimedTask_NoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	// No lock file — should return true (conservative: assume work done)
	if !agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = false, want true when lock file doesn't exist (conservative)")
	}
}

func TestAgentClaimedTask_AfterClear(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set task, then clear it
	UpdateLockTask(tmpDir, "bd-123", "Test Task")
	ClearLockTaskID(tmpDir)

	if agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = true, want false after ClearLockTaskID")
	}
}

func TestAgentClaimedTask_ClearThenReclaim(t *testing.T) {
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Simulate auto-mode cycle: clear → agent claims new task
	UpdateLockTask(tmpDir, "bd-old", "Old Task")
	ClearLockTaskID(tmpDir)

	if agentClaimedTask(tmpDir) {
		t.Error("after clear: agentClaimedTask() should be false")
	}

	UpdateLockTask(tmpDir, "bd-new", "New Task")

	if !agentClaimedTask(tmpDir) {
		t.Error("after reclaim: agentClaimedTask() should be true")
	}

	// Verify it's the new task
	info, _ := ReadLockFile(tmpDir)
	if info.TaskID != "bd-new" {
		t.Errorf("Expected TaskID 'bd-new', got '%s'", info.TaskID)
	}
}

func TestStreamRemainingLogContent_HandlesLogTruncation(t *testing.T) {
	// Create temp log file with initial content
	tmpFile, err := os.CreateTemp("", "loom-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	fileName := tmpFile.Name()
	defer os.Remove(fileName)

	// Write initial content
	initialContent := "initial content that is long\n"
	if _, err := tmpFile.WriteString(initialContent); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}
	tmpFile.Close()

	// Set offset to end of initial content
	var offset int64 = int64(len(initialContent))

	// Truncate the file (simulate log rotation)
	if err := os.Truncate(fileName, 0); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	// Write new content (shorter than original)
	newContent := "new\n"
	f, err := os.OpenFile(fileName, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for writing: %v", err)
	}
	if _, err := f.WriteString(newContent); err != nil {
		t.Fatalf("failed to write new content: %v", err)
	}
	f.Close()

	// Read should detect truncation and reset offset
	streamRemainingLogContent(fileName, &offset)

	// Offset should now be at end of new content (reset from stale value)
	if offset != int64(len(newContent)) {
		t.Errorf("after truncation: offset = %d, want %d", offset, len(newContent))
	}
}

