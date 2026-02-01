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

	// No lock file — daemon never ran or failed before writing lock. No progress.
	if agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = true, want false when lock file doesn't exist (no progress)")
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

// ============================================================================
// Tmux Auto Mode Lock Lifecycle Tests
// ============================================================================

// Simulates the tmux auto mode cycle where the daemon exits without claiming
// a task (e.g. no plannable tasks found). The lock file should remain on disk
// with an empty TaskID so the parent correctly detects no progress.
func TestTmuxCycle_DaemonExitsWithoutClaimingTask(t *testing.T) {
	tmpDir := t.TempDir()

	// Parent removes any old lock (start of cycle)
	lockPath := filepath.Join(tmpDir, LockFileName)
	_ = os.Remove(lockPath)

	// Daemon acquires lock (simulating daemon start)
	if err := AcquireLock(tmpDir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// Daemon exits WITHOUT calling loom claim — TaskID stays empty.
	// In the fix, daemon does NOT call ReleaseLock (no defer).
	// Lock file remains on disk.

	// Parent checks if task was claimed
	if agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = true, want false when daemon didn't claim a task")
	}

	// Verify lock file still exists on disk
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file should still exist after daemon exit (no defer ReleaseLock)")
	}

	// Cleanup
	os.Remove(lockPath)
}

// Simulates the tmux auto mode cycle where the daemon claims a task.
// The lock file should remain with a TaskID so the parent detects progress.
func TestTmuxCycle_DaemonClaimsTask(t *testing.T) {
	tmpDir := t.TempDir()

	// Parent removes any old lock (start of cycle)
	lockPath := filepath.Join(tmpDir, LockFileName)
	_ = os.Remove(lockPath)

	// Daemon acquires lock
	if err := AcquireLock(tmpDir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// Daemon (Claude) claims a task via loom claim
	if err := UpdateLockTask(tmpDir, "bd-abc", "Implement feature"); err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	// Daemon exits — lock stays (no defer ReleaseLock)

	// Parent checks if task was claimed
	if !agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = false, want true when daemon claimed a task")
	}

	// Parent removes lock before next cycle
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("Failed to remove lock before next cycle: %v", err)
	}

	// Next daemon can acquire a fresh lock
	if err := AcquireLock(tmpDir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock failed on next cycle: %v", err)
	}

	// Fresh lock has empty TaskID
	if agentClaimedTask(tmpDir) {
		t.Error("Fresh lock should have empty TaskID")
	}

	// Cleanup
	os.Remove(lockPath)
}

// Simulates consecutive no-progress cycles in tmux auto mode.
// After 3 cycles where the daemon doesn't claim a task, auto mode should exit.
func TestTmuxCycle_ConsecutiveNoProgress(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, LockFileName)
	consecutiveNoProgress := 0

	for cycle := 0; cycle < 3; cycle++ {
		// Parent removes old lock
		_ = os.Remove(lockPath)

		// Daemon acquires lock
		if err := AcquireLock(tmpDir, "plan", "falcon"); err != nil {
			t.Fatalf("Cycle %d: AcquireLock failed: %v", cycle, err)
		}

		// Daemon exits without claiming (no loom claim called)
		// Lock stays on disk (no defer ReleaseLock)

		// Parent checks progress
		if agentClaimedTask(tmpDir) {
			t.Errorf("Cycle %d: agentClaimedTask() = true, want false", cycle)
		} else {
			consecutiveNoProgress++
		}
	}

	if consecutiveNoProgress != 3 {
		t.Errorf("Expected 3 consecutive no-progress, got %d", consecutiveNoProgress)
	}

	// Cleanup
	os.Remove(lockPath)
}

// Verifies that if the daemon crashes before even creating the lock file,
// the parent correctly detects no progress (returns false, not true).
func TestTmuxCycle_DaemonCrashesBeforeAcquiringLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Parent removes old lock
	lockPath := filepath.Join(tmpDir, LockFileName)
	_ = os.Remove(lockPath)

	// Daemon crashes before AcquireLock — no lock file created

	// Parent checks progress — lock file doesn't exist
	if agentClaimedTask(tmpDir) {
		t.Error("agentClaimedTask() = true, want false when daemon crashed before acquiring lock")
	}
}

// ============================================================================
// workspaceHash Tests
// ============================================================================

func TestWorkspaceHash_Deterministic(t *testing.T) {
	// Same input should always produce the same hash
	hash1 := workspaceHash("/some/path")
	hash2 := workspaceHash("/some/path")

	if hash1 != hash2 {
		t.Errorf("workspaceHash not deterministic: got %q and %q", hash1, hash2)
	}
}

func TestWorkspaceHash_Length(t *testing.T) {
	// Should return a 16-character hex string (8 bytes = 16 hex chars)
	hash := workspaceHash("/some/path")

	if len(hash) != 16 {
		t.Errorf("workspaceHash(%q) length = %d, want 16", "/some/path", len(hash))
	}

	// Verify all characters are valid hex
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("workspaceHash(%q) contains non-hex char %q in %q", "/some/path", string(c), hash)
			break
		}
	}
}

func TestWorkspaceHash_DifferentPaths(t *testing.T) {
	// Different paths should produce different hashes
	tests := []struct {
		path1 string
		path2 string
	}{
		{"/path/to/worktree1", "/path/to/worktree2"},
		{"/a", "/b"},
		{"/home/user/project", "/home/user/other"},
		{"", "/nonempty"},
	}

	for _, tt := range tests {
		hash1 := workspaceHash(tt.path1)
		hash2 := workspaceHash(tt.path2)
		if hash1 == hash2 {
			t.Errorf("workspaceHash(%q) == workspaceHash(%q) = %q, want different hashes",
				tt.path1, tt.path2, hash1)
		}
	}
}

func TestWorkspaceHash_KnownValue(t *testing.T) {
	// Verify against a pre-computed sha256 value to ensure the implementation
	// matches: sha256("/some/path")[:8] hex-encoded
	hash := workspaceHash("/some/path")
	expected := "eda6cf0b63f1a1d2"

	if hash != expected {
		t.Errorf("workspaceHash(%q) = %q, want %q", "/some/path", hash, expected)
	}
}

func TestWorkspaceHash_EmptyString(t *testing.T) {
	// Empty string should still produce a valid 16-char hex hash
	hash := workspaceHash("")

	if len(hash) != 16 {
		t.Errorf("workspaceHash(%q) length = %d, want 16", "", len(hash))
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

// setupLockFile creates a lock file for the current process in the given directory
// This is needed because UpdateLockState requires a valid lock file with matching PID
func setupLockFile(t *testing.T, dir string) {
	t.Helper()
	lockInfo := LockInfo{
		PID:       os.Getpid(),
		Command:   "test",
		AgentName: "test-agent",
		StartedAt: time.Now(),
	}
	data, err := json.Marshal(lockInfo)
	if err != nil {
		t.Fatalf("failed to marshal lock info: %v", err)
	}
	lockPath := filepath.Join(dir, LockFileName)
	if err := os.WriteFile(lockPath, data, 0644); err != nil {
		t.Fatalf("failed to write lock file: %v", err)
	}
	// Ensure lock file is cleaned up after test
	t.Cleanup(func() {
		os.Remove(lockPath)
	})
}

func TestRunAutoModeLoop_ShutdownImmediately(t *testing.T) {
	// Save and restore mocks
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	// Setup temp directory with lock file
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to return tasks (so loop would continue without shutdown)
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Available task", Status: "open", Design: "Has design"},
			}),
		}
	}

	// Track if Claude was invoked
	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	close(shutdown) // Close immediately

	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	// Run loop - should exit immediately due to shutdown
	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited promptly
	case <-time.After(2 * time.Second):
		t.Error("RunAutoModeLoop did not exit after shutdown signal")
	}

	// Claude should NOT have been invoked
	if claudeInvoked {
		t.Error("Claude was invoked despite immediate shutdown")
	}
}

func TestRunAutoModeLoop_MaxTasksLimit(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to always return tasks
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}

	// Track Claude invocations
	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvocations++
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     3, // Limit to 3 tasks
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	if claudeInvocations != 3 {
		t.Errorf("Claude was invoked %d times, want 3", claudeInvocations)
	}
}

func TestRunAutoModeLoop_GracefulShutdownNoTasks(t *testing.T) {
	// This test verifies graceful shutdown when no tasks are available.
	// Note: Testing actual IdleTimeout would require waiting 1+ minutes,
	// so we test the shutdown-during-idle path instead.
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to return NO tasks
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "[]"}
	}

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     0,
		IdleTimeout:  1, // Set but won't be reached - we'll shutdown first
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	if claudeInvoked {
		t.Error("Claude should not be invoked when no tasks available")
	}
}

func TestRunAutoModeLoop_NoTasks(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	checkCount := 0
	execCommand = func(dir, name string, args ...string) CommandResult {
		checkCount++
		return CommandResult{Stdout: "[]"} // No tasks
	}

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})

	// Close shutdown after multiple poll cycles
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     1, // Will be interrupted by shutdown
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have checked for tasks at least once
	if checkCount == 0 {
		t.Error("Should have checked for available tasks")
	}

	// Claude should not be invoked with no tasks
	if claudeInvoked {
		t.Error("Claude should not be invoked when no tasks")
	}
}

func TestRunAutoModeLoop_TaskExecution(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return tasks initially, then no tasks to stop
	callCount := 0
	execCommand = func(dir, name string, args ...string) CommandResult {
		callCount++
		if callCount <= 2 { // First two calls return task
			return CommandResult{
				Stdout: mustJSON([]BdIssue{
					{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
				}),
			}
		}
		return CommandResult{Stdout: "[]"} // No more tasks
	}

	promptsReceived := []string{}
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		promptsReceived = append(promptsReceived, prompt)
		return nil
	}

	shutdown := make(chan struct{})
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     1, // Only run 1 task
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-agent",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	if len(promptsReceived) != 1 {
		t.Errorf("Expected 1 prompt, got %d", len(promptsReceived))
	}
}

func TestRunAutoModeLoop_ConsecutiveErrors(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}

	// Always return error
	errorCount := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		errorCount++
		return fmt.Errorf("simulated error %d", errorCount)
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1, // Short interval for test
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - should exit after 3 consecutive errors
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after consecutive errors")
	}

	// Should have tried exactly 3 times before exiting
	if errorCount != 3 {
		t.Errorf("Expected 3 consecutive errors, got %d", errorCount)
	}
}

func TestRunAutoModeLoop_PlanAgentType(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITHOUT design (needs planning)
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Needs planning", Status: "open", Design: ""},
			}),
		}
	}

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		receivedPrompt = prompt
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "plan", // Plan agent
		AgentName:    "planner",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Verify that plan prompt was generated
	expectedPrompt := GeneratePlanningPrompt("planner")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Plan agent did not receive planning prompt")
	}
}

func TestRunAutoModeLoop_TaskAgentType(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITH design (ready for implementation)
	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Ready to implement", Status: "open", Design: "Design here"},
			}),
		}
	}

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		receivedPrompt = prompt
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task", // Task agent
		AgentName:    "worker",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Verify that task prompt was generated
	expectedPrompt := GenerateTaskPrompt("worker")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Task agent did not receive task prompt")
	}
}

func TestRunAutoModeLoop_ErrorRecovery(t *testing.T) {
	// Test that a successful task resets the error counter
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}

	// Pattern: error, error, success, error, error, error (should exit on 6th)
	callNum := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		callNum++
		// Errors on calls 1, 2, 4, 5, 6
		// Success on call 3
		if callNum == 3 {
			return nil // Success resets error counter
		}
		return fmt.Errorf("error %d", callNum)
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should exit after 6 calls (2 errors, 1 success, 3 consecutive errors)
	if callNum != 6 {
		t.Errorf("Expected 6 Claude invocations, got %d", callNum)
	}
}

func TestRunAutoModeLoop_BdCommandError(t *testing.T) {
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// bd ready returns an error
	bdErrorCount := 0
	execCommand = func(dir, name string, args ...string) CommandResult {
		bdErrorCount++
		return CommandResult{Err: fmt.Errorf("bd error")}
	}

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     1,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have tried bd command
	if bdErrorCount == 0 {
		t.Error("Should have attempted bd command")
	}

	// Claude should not be invoked when bd fails
	if claudeInvoked {
		t.Error("Claude should not be invoked when bd command fails")
	}
}

func TestRunAutoModeLoop_ShutdownDuringBackoff(t *testing.T) {
	// Test that shutdown is respected during the error backoff sleep
	oldExec := execCommand
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		execCommand = oldExec
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	execCommand = func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}

	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
		claudeInvocations++
		return fmt.Errorf("error")
	}

	shutdown := make(chan struct{})

	// Close shutdown shortly after first error (during backoff period)
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     5, // Long backoff to ensure shutdown happens during wait
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		// Should exit quickly, not wait full 5-second backoff
		if elapsed >= 3*time.Second {
			t.Errorf("Loop did not respect shutdown during backoff (took %v)", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have invoked Claude exactly once before shutdown during backoff
	if claudeInvocations != 1 {
		t.Errorf("Expected 1 Claude invocation before shutdown, got %d", claudeInvocations)
	}
}

func TestGetPaneState_ParsesCorrectly(t *testing.T) {
	// Skip if tmux is not available
	if exec.Command("tmux", "-V").Run() != nil {
		t.Skip("tmux not available")
	}

	// Create a simple tmux session that runs long enough to query
	sessionName := fmt.Sprintf("loom-test-panestate-%d", os.Getpid())

	// Create session with a command that sleeps briefly (enough time to query)
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep", "5").Run()
	if err != nil {
		t.Fatalf("Failed to create test session: %v", err)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	})

	// Give session time to start
	time.Sleep(100 * time.Millisecond)

	// Get pane state while session is running
	state, err := getPaneState(sessionName)
	if err != nil {
		t.Fatalf("getPaneState failed: %v", err)
	}

	// Command is still running, so pane should NOT be dead
	if state.Dead {
		t.Error("Expected pane to be alive while command is running")
	}
	if state.PID <= 0 {
		t.Errorf("Expected valid PID, got %d", state.PID)
	}
	t.Logf("PaneState: Dead=%v, ExitStatus=%d, ExitSignal=%q, PID=%d",
		state.Dead, state.ExitStatus, state.ExitSignal, state.PID)
}

func TestStartTmuxSession_Success(t *testing.T) {
	// Skip if tmux is not available
	if exec.Command("tmux", "-V").Run() != nil {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-start-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	})

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Verify session was created
	if !tmuxSessionExists(sessionName) {
		t.Error("Session was not created")
	}
}

func TestStartTmuxSession_KillsExisting(t *testing.T) {
	// Skip if tmux is not available
	if exec.Command("tmux", "-V").Run() != nil {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-kill-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	// Create an existing session with the same name
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep", "60").Run()
	if err != nil {
		t.Fatalf("Failed to create initial session: %v", err)
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	})

	// Verify initial session exists
	if !tmuxSessionExists(sessionName) {
		t.Fatal("Initial session should exist")
	}

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
	}

	// Start new session - should kill and replace the existing one
	err = startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Verify session still exists (the new one)
	if !tmuxSessionExists(sessionName) {
		t.Error("New session should exist after replacing old one")
	}
}

