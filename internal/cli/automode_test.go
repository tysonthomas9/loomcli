package cli

import (
	"encoding/json"
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

