package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// setTmuxRemainOnExit sets remain-on-exit globally so tmux panes stay alive
// even when the loom command exits (loom is not installed in CI environments).
// It starts a keepalive tmux session if no server is running (the tmux server
// exits when the last session is destroyed, so we need our own).
// The original setting and keepalive session are cleaned up via t.Cleanup.
func setTmuxRemainOnExit(t *testing.T) {
	t.Helper()

	// Ensure a tmux server is running. If no server exists, "tmux set -g" fails silently.
	// Start a keepalive session that sleeps - this guarantees a server for our global setting.
	keepalive := fmt.Sprintf("loom-test-keepalive-%d", os.Getpid())
	if err := exec.Command("tmux", "has-session", "-t", keepalive).Run(); err != nil { //nolint:norawexec
		exec.Command("tmux", "new-session", "-d", "-s", keepalive, "sleep", "300").Run() //nolint:norawexec
		t.Cleanup(func() {
			exec.Command("tmux", "kill-session", "-t", keepalive).Run() //nolint:norawexec
		})
	}

	origRemain, _ := exec.Command("tmux", "show", "-gv", "remain-on-exit").Output() //nolint:norawexec
	exec.Command("tmux", "set", "-g", "remain-on-exit", "on").Run()                 //nolint:norawexec
	t.Cleanup(func() {
		val := strings.TrimSpace(string(origRemain))
		if val == "" || val == "off" {
			exec.Command("tmux", "set", "-g", "remain-on-exit", "off").Run() //nolint:norawexec
		} else {
			exec.Command("tmux", "set", "-g", "remain-on-exit", val).Run() //nolint:norawexec
		}
	})
}

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
			name: "include tasks with needs-revision label (revision task)",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "existing design", Labels: []string{"needs-revision"}},
			}),
			want: true,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip review tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "In review", Status: "review", Design: ""},
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
				{ID: "T-1", Title: "Has design and needs-revision", Status: "open", Design: "plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Work on me", Status: "open", Design: ""},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Already planned"},
			}),
			want: true,
		},
		{
			name: "skip task with blocks dependency",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Blocker", Status: "open", Design: "has design"},
				{ID: "T-1", Title: "Task with deps", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: false,
		},
		{
			name: "parent-child dependency does not block planning",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task with parent-child dep", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "parent-child", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: true,
		},
		{
			name: "task with design and parent-child dep not needing planning",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task with deps and design", Status: "open", Design: "Approved plan", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "parent-child", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			var issues []BdIssue
			if tt.bdOutput != "" {
				json.Unmarshal([]byte(tt.bdOutput), &issues)
			}
			if tt.bdErr != nil {
				mock.ReadyErr = tt.bdErr
				mock.ListErr = tt.bdErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultTracker(mock)

			got, err := HasAvailablePlanningTasks("", "")
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
			name: "skip tasks with needs-revision label even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
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
			name: "skip review tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "In review", Status: "review", Design: "Has design"},
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
				{ID: "T-1", Title: "Has needs-revision label", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "No design yet", Status: "open", Design: ""},
				{ID: "T-3", Title: "Ready to implement", Status: "open", Design: "Detailed plan"},
			}),
			want: true,
		},
		{
			name: "skip task with blocks dependency even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Blocker", Status: "open"},
				{ID: "T-1", Title: "Blocked with design", Status: "open", Design: "Implementation plan", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: false,
		},
		{
			name: "parent-child dependency does not block implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Ready with parent-child dep", Status: "open", Design: "Implementation plan", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "parent-child", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: true,
		},
		{
			name: "task with parent-child dep but no design not ready",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Not ready with deps", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "parent-child", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			var issues []BdIssue
			if tt.bdOutput != "" {
				json.Unmarshal([]byte(tt.bdOutput), &issues)
			}
			if tt.bdErr != nil {
				mock.ReadyErr = tt.bdErr
				mock.ListErr = tt.bdErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultTracker(mock)

			got, err := HasAvailableImplementationTasks("", "")
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
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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

func TestSetupSignalHandler_StopsSignalDelivery(t *testing.T) {
	// Verify that SetupSignalHandler closes the shutdown channel on SIGINT
	// and that signal.Stop is called (freeing the signal for re-registration).
	shutdown := SetupSignalHandler()

	// Send SIGINT to ourselves.
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := p.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Signal: %v", err)
	}

	// The shutdown channel should be closed promptly.
	select {
	case <-shutdown:
		// expected
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown channel was not closed after SIGINT")
	}

	// After the handler runs, signal.Stop should have been called on the
	// internal channel.  Verify by registering a *new* listener for SIGINT
	// and confirming it receives the signal independently (i.e. the old
	// registration is no longer consuming it).
	verifyCh := make(chan os.Signal, 1)
	signal.Notify(verifyCh, syscall.SIGINT)
	defer signal.Stop(verifyCh)

	if err := p.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Signal (second): %v", err)
	}

	select {
	case sig := <-verifyCh:
		if sig != syscall.SIGINT {
			t.Errorf("expected SIGINT, got %v", sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new signal listener did not receive SIGINT; signal.Stop may not have been called")
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
	t.Run("reflects_system_state", func(t *testing.T) {
		result := IsTmuxAvailable()
		_, lookupErr := exec.LookPath("tmux")
		expected := lookupErr == nil
		if result != expected {
			t.Errorf("IsTmuxAvailable()=%v but LookPath err=%v", result, lookupErr)
		}
	})

	t.Run("false_when_overridden", func(t *testing.T) {
		orig := IsTmuxAvailable
		IsTmuxAvailable = func() bool { return false }
		t.Cleanup(func() { IsTmuxAvailable = orig })

		if IsTmuxAvailable() {
			t.Error("expected false when overridden")
		}
	})
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
	if exec.Command("tmux", "-V").Run() != nil { //nolint:norawexec
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-ctrlc-%d", os.Getpid())
	signalFile := filepath.Join(t.TempDir(), "received-sigint")

	// Create a script that writes to a file when it receives SIGINT
	// The script traps SIGINT and writes before exiting
	trapScript := fmt.Sprintf(`trap 'echo received > %s; exit 0' INT; sleep 30`, signalFile)

	// Create tmux session running the trap script
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sh", "-c", trapScript).Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("Failed to create tmux session: %v", err)
	}

	// Give the script time to set up the trap
	time.Sleep(100 * time.Millisecond)

	// Call cleanupTmuxSession - should send Ctrl+C then kill
	cleanupTmuxSession(sessionName)

	// Wait for signal file with generous timeout to handle CPU pressure from parallel runs
	if !waitForFile(signalFile, 10*time.Second) {
		t.Fatal("Timeout waiting for SIGINT signal file - Ctrl+C was not sent before kill")
	}

	// Verify session is actually gone
	if exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil { //nolint:norawexec
		t.Error("Session still exists after cleanup")
		// Clean up manually
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	}
}

func TestRunAutoModeTmux_MaxTasksZero(t *testing.T) {
	// Test early exit when shutdown is signaled immediately
	shutdown := make(chan struct{})
	close(shutdown) // Signal shutdown immediately

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0, // unlimited
		IdleTimeout:  0,
		AgentType:    "plan",
		AgentName:    "test",
		WorktreePath: t.TempDir(),
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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

	if !agentClaimedTask(tmpDir, "", nil) {
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
	if agentClaimedTask(tmpDir, "", nil) {
		t.Error("agentClaimedTask() = true, want false when TaskID is empty")
	}
}

func TestAgentClaimedTask_NoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	// No lock file — daemon never ran or failed before writing lock. No progress.
	if agentClaimedTask(tmpDir, "", nil) {
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

	if agentClaimedTask(tmpDir, "", nil) {
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

	if agentClaimedTask(tmpDir, "", nil) {
		t.Error("after clear: agentClaimedTask() should be false")
	}

	UpdateLockTask(tmpDir, "bd-new", "New Task")

	if !agentClaimedTask(tmpDir, "", nil) {
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
	if agentClaimedTask(tmpDir, "", nil) {
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
	if !agentClaimedTask(tmpDir, "", nil) {
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
	if agentClaimedTask(tmpDir, "", nil) {
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
		if agentClaimedTask(tmpDir, "", nil) {
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
	if agentClaimedTask(tmpDir, "", nil) {
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	// Setup temp directory with lock file
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to return tasks (so loop would continue without shutdown)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Available task", Status: "open", Design: "Has design"},
			}),
		}
	}})

	// Track if Claude was invoked
	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	close(shutdown) // Close immediately

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Track Claude invocations
	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", claudeInvocations), "Mock Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     3, // Limit to 3 tasks
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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

func TestRunAutoModeLoop_WithoutTmux(t *testing.T) {
	// Verify RunAutoModeLoop works when tmux is unavailable (JSON streaming fallback).
	orig := IsTmuxAvailable
	IsTmuxAvailable = func() bool { return false }
	t.Cleanup(func() { IsTmuxAvailable = orig })

	if IsTmuxAvailable() {
		t.Fatal("IsTmuxAvailable should be false")
	}

	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", claudeInvocations), "Mock Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     2,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - completed without tmux
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks (without tmux)")
	}

	if claudeInvocations != 2 {
		t.Errorf("Claude was invoked %d times, want 2", claudeInvocations)
	}
}

func TestRunAutoModeLoop_GracefulShutdownNoTasks(t *testing.T) {
	// This test verifies graceful shutdown when no tasks are available.
	// Note: Testing actual IdleTimeout would require waiting 1+ minutes,
	// so we test the shutdown-during-idle path instead.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock bd ready to return NO tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "[]"}
	}})

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  1, // Set but won't be reached - we'll shutdown first
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	checkCount := 0
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		checkCount++
		return CommandResult{Stdout: "[]"} // No tasks
	}})

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
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
		Interval:     0, // Will be interrupted by shutdown
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return tasks initially, then no tasks to stop
	callCount := 0
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		callCount++
		if callCount <= 2 { // First two calls return task
			return CommandResult{
				Stdout: mustJSON([]BdIssue{
					{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
				}),
			}
		}
		return CommandResult{Stdout: "[]"} // No more tasks
	}})

	promptsReceived := []string{}
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		promptsReceived = append(promptsReceived, prompt)
		return nil
	}

	shutdown := make(chan struct{})
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1, // Only run 1 task
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Always return error
	errorCount := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		errorCount++
		return fmt.Errorf("simulated error %d", errorCount)
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0, // Short interval for test
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITHOUT design (needs planning)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Needs planning", Status: "open", Design: ""},
			}),
		}
	}})

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, "mock-plan-1", "Mock Plan Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "plan", // Plan agent
		AgentName:    "planner",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	expectedPrompt := GeneratePlanningPrompt("planner", nil, "")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Plan agent did not receive planning prompt")
	}
}

func TestRunAutoModeLoop_TaskAgentType(t *testing.T) {
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITH design (ready for implementation)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Ready to implement", Status: "open", Design: "Design here"},
			}),
		}
	}})

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, "mock-task-1", "Mock Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task", // Task agent
		AgentName:    "worker",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	expectedPrompt := GenerateTaskPrompt("worker", nil, "", "claude")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Task agent did not receive task prompt")
	}
}

func TestRunAutoModeLoop_ErrorRecovery(t *testing.T) {
	// Test that a successful task resets the error counter
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Pattern: error, error, success, error, error, error (should exit on 6th)
	callNum := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		// Errors on calls 1, 2, 4, 5, 6
		// Success on call 3
		if callNum == 3 {
			// Simulate task claiming so the loop counts this as progress
			UpdateLockTask(workDir, "mock-recovery", "Mock Recovery Task")
			return nil // Success resets error counter
		}
		return fmt.Errorf("error %d", callNum)
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// bd ready returns an error
	bdErrorCount := 0
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		bdErrorCount++
		return CommandResult{Err: fmt.Errorf("bd error")}
	}})

	claudeInvoked := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	}

	shutdown := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
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
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
	if exec.Command("tmux", "-V").Run() != nil { //nolint:norawexec
		t.Skip("tmux not available")
	}

	// Create a simple tmux session that runs long enough to query
	sessionName := fmt.Sprintf("loom-test-panestate-%d", os.Getpid())

	// Create session with a command that sleeps briefly (enough time to query)
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep", "5").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("Failed to create test session: %v", err)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
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
	if exec.Command("tmux", "-V").Run() != nil { //nolint:norawexec
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-start-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
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
	if exec.Command("tmux", "-V").Run() != nil { //nolint:norawexec
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-kill-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	// Create an existing session with the same name
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep", "60").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("Failed to create initial session: %v", err)
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Verify initial session exists
	if !tmuxSessionExists(sessionName) {
		t.Fatal("Initial session should exist")
	}

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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

func TestStartTmuxSession_QuotesShellMetachars(t *testing.T) {
	// Skip if tmux is not available
	if exec.Command("tmux", "-V").Run() != nil { //nolint:norawexec
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	// Create a temp dir whose name contains shell metacharacters
	baseDir := t.TempDir()
	metaDir := filepath.Join(baseDir, "test dir;echo pwned")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatalf("failed to create metachar dir: %v", err)
	}

	sessionName := fmt.Sprintf("loom-test-quote-%d", os.Getpid())
	logFile := filepath.Join(baseDir, "test.log")

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: metaDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Verify session was created
	if !tmuxSessionExists(sessionName) {
		t.Fatal("Session was not created")
	}

	// Retrieve the command string that tmux used to start the pane
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to get pane start command: %v", err)
	}
	paneCmd := strings.TrimSpace(string(out))

	// The worktree path must appear single-quoted in the command string,
	// which proves shellQuote() was applied. Without quoting, the semicolon
	// would split the command and "echo pwned" would execute separately.
	quoted := shellQuote(metaDir)
	if !strings.Contains(paneCmd, quoted) {
		t.Errorf("pane command does not contain properly quoted path\nwant substring: %s\ngot command:    %s", quoted, paneCmd)
	}

	// Also verify the agent type is quoted
	quotedAgent := shellQuote(opts.AgentType)
	if !strings.Contains(paneCmd, quotedAgent) {
		t.Errorf("pane command does not contain properly quoted agent type\nwant substring: %s\ngot command:    %s", quotedAgent, paneCmd)
	}
}

// ============================================================================
// HasAnyAvailableTasks Tests
// ============================================================================

func TestHasAnyAvailableTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		want     bool
		wantErr  bool
	}{
		{
			name: "task with no design - available",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task with design - available",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			want: true,
		},
		{
			name: "task with needs-revision label - available (for HasAny)",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "", Labels: []string{"needs-revision"}},
			}),
			want: true,
		},
		{
			name: "skip in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip review tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "In review", Status: "review", Design: ""},
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
			name: "mixed - epic + valid task",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Valid task with revision", Status: "open", Design: "plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
				{ID: "T-3", Title: "Valid task", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "skip task with blocks dependency",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Blocker", Status: "open", IssueType: "epic"},
				{ID: "T-1", Title: "Blocked task", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: false,
		},
		{
			name: "parent-child dependency does not block",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Child task", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "EPIC-1", Type: "parent-child", CreatedAt: "2025-01-01T00:00:00Z", CreatedBy: "user1"},
				}},
			}),
			want: true,
		},
		{
			name:    "bd command error",
			bdErr:   fmt.Errorf("bd error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.bdOutput, Err: tt.bdErr}
			}})

			got, err := HasAnyAvailableTasks("", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("HasAnyAvailableTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("HasAnyAvailableTasks() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ============================================================================
// hasOpenBlockers Tests
// ============================================================================

// TestHasOpenBlockers is kept for backward compatibility but delegates to
// HasUnclosedBlockers (now in taskfilter.go). Comprehensive tests
// are in taskfilter_test.go TestHasUnclosedBlockers.
func TestHasOpenBlockers(t *testing.T) {
	unclosedIDs := map[string]bool{"T-0": true}
	got := HasUnclosedBlockers(
		[]Dependency{{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks"}},
		unclosedIDs,
	)
	if !got {
		t.Error("expected unclosed blocker to be detected")
	}
}

// ============================================================================
// AutoModeOptions Custom Fields Tests
// ============================================================================

func TestAutoModeOptions_CustomFields(t *testing.T) {
	promptCalled := false
	checkCalled := false

	opts := AutoModeOptions{
		Interval:     60,
		MaxTasks:     10,
		IdleTimeout:  30,
		AgentType:    "task",
		AgentName:    "falcon",
		WorktreePath: "/path/to/worktree",
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomPromptGen: func(agentName string, ws *WorkspaceConfig) string {
			promptCalled = true
			return "custom prompt for " + agentName
		},
		CustomTaskCheck: func() (bool, error) {
			checkCalled = true
			return true, nil
		},
	}

	// Verify custom prompt gen works
	result := opts.CustomPromptGen("falcon", nil)
	if !promptCalled {
		t.Error("CustomPromptGen was not called")
	}
	if result != "custom prompt for falcon" {
		t.Errorf("CustomPromptGen returned %q, want %q", result, "custom prompt for falcon")
	}

	// Verify custom task check works
	available, err := opts.CustomTaskCheck()
	if !checkCalled {
		t.Error("CustomTaskCheck was not called")
	}
	if err != nil {
		t.Errorf("CustomTaskCheck returned error: %v", err)
	}
	if !available {
		t.Error("CustomTaskCheck returned false, want true")
	}
}

// ============================================================================
// RunAutoModeLoop Custom Prompt/Task Check Tests
// ============================================================================

func TestRunAutoModeLoop_CustomPromptGen(t *testing.T) {
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// execCommand should NOT be called for task checks when CustomTaskCheck is set
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "[]"}
	}})

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming
		UpdateLockTask(workDir, "mock-custom-1", "Mock Custom Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "custom-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomPromptGen: func(agentName string, ws *WorkspaceConfig) string {
			return "custom prompt for " + agentName
		},
		CustomTaskCheck: func() (bool, error) {
			return true, nil
		},
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

	// Verify custom prompt was used (not default task prompt)
	expectedPrompt := "custom prompt for custom-agent"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Received prompt %q, want %q", receivedPrompt, expectedPrompt)
	}

	// Verify it's NOT the default task prompt
	defaultTaskPrompt := GenerateTaskPrompt("custom-agent", nil, "", "claude")
	if receivedPrompt == defaultTaskPrompt {
		t.Error("Received default task prompt instead of custom prompt")
	}
}

func TestRunAutoModeLoop_CustomFieldsFallback(t *testing.T) {
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITH design (ready for implementation via default task check)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Ready to implement", Status: "open", Design: "Design here"},
			}),
		}
	}})

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming
		UpdateLockTask(workDir, "mock-fallback-1", "Mock Fallback Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "fallback-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		// Only set CustomPromptGen, NOT CustomTaskCheck — CustomPromptGen works independently
		CustomPromptGen: func(agentName string, ws *WorkspaceConfig) string {
			return "custom prompt for " + agentName
		},
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

	// CustomPromptGen is decoupled from CustomTaskCheck, so the custom prompt
	// should be used even when CustomTaskCheck is nil (default task check used).
	customPrompt := "custom prompt for fallback-agent"
	if receivedPrompt != customPrompt {
		t.Errorf("Received prompt %q, want custom prompt %q", receivedPrompt, customPrompt)
	}
}

func TestRunAutoModeLoop_CustomTaskCheckOnlyFallback(t *testing.T) {
	// When only CustomTaskCheck is set (not CustomPromptGen), CustomTaskCheck is used
	// for task availability, and the default AgentType-based prompt gen is used.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return tasks (needed for default HasAvailableImplementationTasks fallback)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	var receivedPrompt string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		UpdateLockTask(workDir, "mock-taskcheck-1", "Mock Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "taskcheck-agent",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		// Only set CustomTaskCheck, NOT CustomPromptGen — should fall back to AgentType
		CustomTaskCheck: func() (bool, error) {
			return true, nil
		},
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

	// Should receive default task prompt since CustomPromptGen is nil
	expectedPrompt := GenerateTaskPrompt("taskcheck-agent", nil, "", "claude")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Received prompt %q, want default task prompt", receivedPrompt)
	}
}

// ============================================================================
// GetAvailable* Tests
// ============================================================================

func TestGetAvailablePlanningTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		wantIDs  []string
		wantErr  bool
	}{
		{
			name: "returns task needing planning (no design)",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with needs-revision label",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "existing design", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "excludes task with design and no revision label",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			wantIDs: nil,
		},
		{
			name: "skips in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name:     "empty list returns empty slice",
			bdOutput: "[]",
			wantIDs:  nil,
		},
		{
			name: "multiple valid tasks returns all",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Has design no revision", Status: "open", Design: "Plan"},
				{ID: "T-3", Title: "Needs revision", Status: "open", Design: "Old plan", Labels: []string{"needs-revision"}},
				{ID: "T-4", Title: "Also no design", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1", "T-3", "T-4"},
		},
		{
			name: "skips task with blocks dependency when blocker is open",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Blocked task", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks"},
				}},
			}),
			wantIDs: []string{"T-0"},
		},
		{
			name: "does not skip task when blocker is resolved",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Was blocked", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-CLOSED", Type: "blocks"},
				}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "mixed blocked and unblocked returns only unblocked",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Unblocked", Status: "open", Design: ""},
				{ID: "T-2", Title: "Blocked", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-2", DependsOnID: "T-0", Type: "blocks"},
				}},
				{ID: "T-3", Title: "Also unblocked", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0", "T-1", "T-3"},
		},
		{
			name:    "bd error propagates",
			bdErr:   fmt.Errorf("bd error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			var issues []BdIssue
			if tt.bdOutput != "" {
				json.Unmarshal([]byte(tt.bdOutput), &issues)
			}
			if tt.bdErr != nil {
				mock.ReadyErr = tt.bdErr
				mock.ListErr = tt.bdErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultTracker(mock)

			got, err := GetAvailablePlanningTasks("", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAvailablePlanningTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAvailablePlanningTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAvailablePlanningTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestGetAvailableImplementationTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		wantIDs  []string
		wantErr  bool
	}{
		{
			name: "returns task with design ready for implementation",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Implementation plan"},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "excludes task without design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "excludes task with needs-revision label",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
			}),
			wantIDs: nil,
		},
		{
			name: "skips in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: "Has design"},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics even with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: "Has design"},
			}),
			wantIDs: nil,
		},
		{
			name:     "empty list returns empty slice",
			bdOutput: "[]",
			wantIDs:  nil,
		},
		{
			name: "multiple valid tasks returns all",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Has design", Status: "open", Design: "Plan A"},
				{ID: "T-3", Title: "Needs revision", Status: "open", Design: "Old plan", Labels: []string{"needs-revision"}},
				{ID: "T-4", Title: "Also has design", Status: "open", Design: "Plan B"},
			}),
			wantIDs: []string{"T-2", "T-4"},
		},
		{
			name: "skips task with blocks dependency when blocker is open",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Blocked with design", Status: "open", Design: "Plan", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks"},
				}},
			}),
			wantIDs: nil,
		},
		{
			name: "does not skip task when blocker is resolved",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Was blocked with design", Status: "open", Design: "Plan", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-CLOSED", Type: "blocks"},
				}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "mixed blocked and unblocked returns only unblocked",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Unblocked with design", Status: "open", Design: "Plan A"},
				{ID: "T-2", Title: "Blocked with design", Status: "open", Design: "Plan B", Dependencies: []Dependency{
					{IssueID: "T-2", DependsOnID: "T-0", Type: "blocks"},
				}},
				{ID: "T-3", Title: "Also unblocked", Status: "open", Design: "Plan C"},
			}),
			wantIDs: []string{"T-1", "T-3"},
		},
		{
			name:    "bd error propagates",
			bdErr:   fmt.Errorf("bd error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			var issues []BdIssue
			if tt.bdOutput != "" {
				json.Unmarshal([]byte(tt.bdOutput), &issues)
			}
			if tt.bdErr != nil {
				mock.ReadyErr = tt.bdErr
				mock.ListErr = tt.bdErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultTracker(mock)

			got, err := GetAvailableImplementationTasks("", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAvailableImplementationTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAvailableImplementationTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAvailableImplementationTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestGetAnyAvailableTasks(t *testing.T) {
	tests := []struct {
		name     string
		bdOutput string
		bdErr    error
		wantIDs  []string
		wantErr  bool
	}{
		{
			name: "returns task without design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with design",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "returns task with needs-revision label",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "skips in_progress tasks",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name: "skips epics",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			wantIDs: nil,
		},
		{
			name:     "empty list returns empty slice",
			bdOutput: "[]",
			wantIDs:  nil,
		},
		{
			name: "multiple valid tasks returns all",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "No design", Status: "open", Design: ""},
				{ID: "T-2", Title: "Big Epic", Status: "open", IssueType: "epic"},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Plan"},
				{ID: "T-4", Title: "In progress", Status: "in_progress", Design: ""},
				{ID: "T-5", Title: "Revision needed", Status: "open", Labels: []string{"needs-revision"}},
			}),
			wantIDs: []string{"T-1", "T-3", "T-5"},
		},
		{
			name: "skips task with blocks dependency when blocker is open",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Blocked task", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-0", Type: "blocks"},
				}},
			}),
			wantIDs: []string{"T-0"},
		},
		{
			name: "does not skip task when blocker is resolved",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Was blocked", Status: "open", Design: "", Dependencies: []Dependency{
					{IssueID: "T-1", DependsOnID: "T-CLOSED", Type: "blocks"},
				}},
			}),
			wantIDs: []string{"T-1"},
		},
		{
			name: "mixed blocked and unblocked returns only unblocked",
			bdOutput: mustJSON([]BdIssue{
				{ID: "T-0", Title: "Open blocker", Status: "open", Design: ""},
				{ID: "T-1", Title: "Unblocked", Status: "open", Design: ""},
				{ID: "T-2", Title: "Blocked", Status: "open", Design: "Plan", Dependencies: []Dependency{
					{IssueID: "T-2", DependsOnID: "T-0", Type: "blocks"},
				}},
				{ID: "T-3", Title: "Also unblocked", Status: "open", Design: ""},
			}),
			wantIDs: []string{"T-0", "T-1", "T-3"},
		},
		{
			name:    "bd error propagates",
			bdErr:   fmt.Errorf("bd error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			var issues []BdIssue
			if tt.bdOutput != "" {
				json.Unmarshal([]byte(tt.bdOutput), &issues)
			}
			if tt.bdErr != nil {
				mock.ReadyErr = tt.bdErr
				mock.ListErr = tt.bdErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultTracker(mock)

			got, err := GetAnyAvailableTasks("", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAnyAvailableTasks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			gotIDs := make([]string, len(got))
			for i, issue := range got {
				gotIDs[i] = issue.ID
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Errorf("GetAnyAvailableTasks() returned %d tasks %v, want %d tasks %v", len(gotIDs), gotIDs, len(tt.wantIDs), tt.wantIDs)
				return
			}
			for i, id := range gotIDs {
				if id != tt.wantIDs[i] {
					t.Errorf("GetAnyAvailableTasks()[%d].ID = %s, want %s", i, id, tt.wantIDs[i])
				}
			}
		})
	}
}

func TestHasAvailableDelegatesToGet(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	issues := []BdIssue{
		{ID: "T-1", Title: "No design", Status: "open", Design: ""},
		{ID: "T-2", Title: "Has design", Status: "open", Design: "Plan"},
	}
	mock := NewMockTracker()
	mock.ReadyResult = issues
	mock.ListResult = issues
	setDefaultTracker(mock)

	// HasAvailablePlanningTasks should return true (T-1 has no design)
	hasPlan, err := HasAvailablePlanningTasks("", "")
	if err != nil {
		t.Fatalf("HasAvailablePlanningTasks() error = %v", err)
	}
	if !hasPlan {
		t.Error("HasAvailablePlanningTasks() = false, want true")
	}

	// HasAvailableImplementationTasks should return true (T-2 has design)
	hasImpl, err := HasAvailableImplementationTasks("", "")
	if err != nil {
		t.Fatalf("HasAvailableImplementationTasks() error = %v", err)
	}
	if !hasImpl {
		t.Error("HasAvailableImplementationTasks() = false, want true")
	}

	// HasAnyAvailableTasks should return true (both T-1 and T-2 are open)
	hasAny, err := HasAnyAvailableTasks("", "")
	if err != nil {
		t.Fatalf("HasAnyAvailableTasks() error = %v", err)
	}
	if !hasAny {
		t.Error("HasAnyAvailableTasks() = false, want true")
	}

	// Verify Get* returns correct counts
	planTasks, _ := GetAvailablePlanningTasks("", "")
	if len(planTasks) != 1 || planTasks[0].ID != "T-1" {
		t.Errorf("GetAvailablePlanningTasks() = %v, want [T-1]", planTasks)
	}

	implTasks, _ := GetAvailableImplementationTasks("", "")
	if len(implTasks) != 1 || implTasks[0].ID != "T-2" {
		t.Errorf("GetAvailableImplementationTasks() = %v, want [T-2]", implTasks)
	}

	anyTasks, _ := GetAnyAvailableTasks("", "")
	if len(anyTasks) != 2 {
		t.Errorf("GetAnyAvailableTasks() returned %d tasks, want 2", len(anyTasks))
	}
}

func TestRunAutoModeLoop_CodexPlanAgentType(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_PlanAgentType but with codex backend.
	// Verifies that when codex is the active backend, RunAutoModeLoop dispatches
	// to codexNonInteractiveInvoker instead of claudeNonInteractiveInvoker.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	// Save and restore codex invoker
	oldCodex := codexNonInteractiveInvoker
	t.Cleanup(func() { codexNonInteractiveInvoker = oldCodex })

	// Track that claude was NOT called
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() { claudeNonInteractiveInvoker = oldClaude })
	claudeCalled := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	}

	// Mock codex invoker to capture args
	var receivedPrompt, receivedWorkDir, receivedAgentName string
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedWorkDir = workDir
		receivedPrompt = prompt
		receivedAgentName = agentName
		UpdateLockTask(workDir, "mock-codex-plan-1", "Mock Codex Plan Task")
		return nil
	}

	// Mock execCommand for bd ready (return task needing planning)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Needs planning", Status: "open", Design: ""},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "plan",
		AgentName:    "codex-planner",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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

	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
	if receivedPrompt == "" {
		t.Error("Codex invoker should have been called")
	}
	// Verify planning prompt was generated
	expectedPrompt := GeneratePlanningPrompt("codex-planner", nil, "")
	if receivedPrompt != expectedPrompt {
		t.Errorf("Codex did not receive planning prompt")
	}
	if receivedWorkDir != tmpDir {
		t.Errorf("Codex received workDir %q, want %q", receivedWorkDir, tmpDir)
	}
	if receivedAgentName != "codex-planner" {
		t.Errorf("Codex received agentName %q, want %q", receivedAgentName, "codex-planner")
	}
}

func TestRunAutoModeLoop_CodexMaxTasks(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_MaxTasksLimit but with codex backend.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	oldCodex := codexNonInteractiveInvoker
	t.Cleanup(func() { codexNonInteractiveInvoker = oldCodex })

	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() { claudeNonInteractiveInvoker = oldClaude })
	claudeCalled := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	}

	codexInvocations := 0
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		codexInvocations++
		UpdateLockTask(workDir, fmt.Sprintf("mock-codex-%d", codexInvocations), "Mock Codex Task")
		return nil
	}

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     3,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	if codexInvocations != 3 {
		t.Errorf("Codex was invoked %d times, want 3", codexInvocations)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

func TestRunAutoModeLoop_CodexConsecutiveErrors(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_ConsecutiveErrors but with codex backend.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	oldCodex := codexNonInteractiveInvoker
	t.Cleanup(func() { codexNonInteractiveInvoker = oldCodex })

	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() { claudeNonInteractiveInvoker = oldClaude })
	claudeCalled := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	}

	errorCount := 0
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		errorCount++
		return fmt.Errorf("codex simulated error %d", errorCount)
	}

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after consecutive errors")
	}

	if errorCount != 3 {
		t.Errorf("Expected 3 consecutive codex errors, got %d", errorCount)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

func TestRunAutoModeLoop_CodexErrorRecovery(t *testing.T) {
	// Mirrors TestRunAutoModeLoop_ErrorRecovery but with codex backend.
	// Pattern: error, error, success (with task claim), error, error, error → exits after 6.
	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	oldCodex := codexNonInteractiveInvoker
	t.Cleanup(func() { codexNonInteractiveInvoker = oldCodex })

	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() { claudeNonInteractiveInvoker = oldClaude })
	claudeCalled := false
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeCalled = true
		return nil
	}

	callNum := 0
	codexNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		if callNum == 3 {
			UpdateLockTask(workDir, "mock-codex-recovery", "Mock Codex Recovery Task")
			return nil // Success resets error counter
		}
		return fmt.Errorf("codex error %d", callNum)
	}

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "codex-worker",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
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
		t.Errorf("Expected 6 codex invocations, got %d", callNum)
	}
	if claudeCalled {
		t.Error("Claude invoker should NOT be called when codex is active")
	}
}

func TestStartTmuxSession_CodexBackend_NoTermDumb(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	resetBackendState(t)
	RegisterBackend(&CodexBackend{})
	if err := SetBackend("codex"); err != nil {
		t.Fatalf("SetBackend('codex') failed: %v", err)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	sessionName := fmt.Sprintf("loom-test-codex-%d", os.Getpid())
	// Clean up tmux session after test
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "plan",
		AgentName:    "codex-test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	setTmuxRemainOnExit(t)

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Give tmux a moment to set up
	time.Sleep(300 * time.Millisecond)

	// Capture the tmux pane start command
	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to get tmux pane start command: %v", err)
	}
	paneCmd := strings.TrimSpace(string(out))

	// Command should NOT contain "TERM=dumb" for codex
	if strings.Contains(paneCmd, "TERM=dumb") {
		t.Errorf("Codex backend should NOT have TERM=dumb prefix, got: %s", paneCmd)
	}
	// Command should contain --backend 'codex' (or --backend codex)
	if !strings.Contains(paneCmd, "--backend") || !strings.Contains(paneCmd, "codex") {
		t.Errorf("Codex backend should have --backend codex flag, got: %s", paneCmd)
	}
	// Command should contain --daemon-mode
	if !strings.Contains(paneCmd, "--daemon-mode") {
		t.Errorf("Command should contain --daemon-mode, got: %s", paneCmd)
	}
}

func TestStartTmuxSession_ClaudeBackend_HasTermDumb(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	resetBackendState(t)
	RegisterBackend(&ClaudeBackend{})
	if err := SetBackend("claude"); err != nil {
		t.Fatalf("SetBackend('claude') failed: %v", err)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	sessionName := fmt.Sprintf("loom-test-claude-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "plan",
		AgentName:    "claude-test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	setTmuxRemainOnExit(t)

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Give tmux a moment to set up
	time.Sleep(300 * time.Millisecond)

	out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to get tmux pane start command: %v", err)
	}
	paneCmd := strings.TrimSpace(string(out))

	// Command SHOULD contain "TERM=dumb" for claude
	if !strings.Contains(paneCmd, "TERM=dumb") {
		t.Errorf("Claude backend should have TERM=dumb prefix, got: %s", paneCmd)
	}
	// Command should always contain --backend flag (explicitly propagated to subprocess)
	if !strings.Contains(paneCmd, "--backend") {
		t.Errorf("Command should contain --backend flag, got: %s", paneCmd)
	}
	// Command should contain --daemon-mode
	if !strings.Contains(paneCmd, "--daemon-mode") {
		t.Errorf("Command should contain --daemon-mode, got: %s", paneCmd)
	}
}

// TestGetAvailablePlanningTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's ReadyOpts.
func TestGetAvailablePlanningTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "T-123",
			wantParentID: "T-123",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-1", Title: "Task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			_, err := GetAvailablePlanningTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAvailablePlanningTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
			if capturedOpts.Limit != 100 {
				t.Errorf("ReadyOpts.Limit = %d, want 100", capturedOpts.Limit)
			}
		})
	}
}

// TestGetAvailableImplementationTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's ReadyOpts.
func TestGetAvailableImplementationTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-456",
			wantParentID: "EPIC-456",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-2", Title: "Task with design", Status: "open", Design: "Implementation plan"}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			_, err := GetAvailableImplementationTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAvailableImplementationTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
			if capturedOpts.Limit != 100 {
				t.Errorf("ReadyOpts.Limit = %d, want 100", capturedOpts.Limit)
			}
		})
	}
}

// TestGetAnyAvailableTasks_WithParentID verifies that when a non-empty parentID
// is provided, it is passed through to the tracker's ReadyOpts.
func TestGetAnyAvailableTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-789",
			wantParentID: "EPIC-789",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-3", Title: "Any task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			_, err := GetAnyAvailableTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("GetAnyAvailableTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
			if capturedOpts.Limit != 100 {
				t.Errorf("ReadyOpts.Limit = %d, want 100", capturedOpts.Limit)
			}
		})
	}
}

// TestHasAvailablePlanningTasks_WithParentID verifies that HasAvailablePlanningTasks
// properly passes the parentID through to the tracker's ReadyOpts.
func TestHasAvailablePlanningTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-100",
			wantParentID: "EPIC-100",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-1", Title: "Task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			got, err := HasAvailablePlanningTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAvailablePlanningTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAvailablePlanningTasks(%q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestHasAvailableImplementationTasks_WithParentID verifies that HasAvailableImplementationTasks
// properly passes the parentID through to the tracker's ReadyOpts.
func TestHasAvailableImplementationTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-200",
			wantParentID: "EPIC-200",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-2", Title: "Task with design", Status: "open", Design: "Implementation plan"}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			got, err := HasAvailableImplementationTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAvailableImplementationTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAvailableImplementationTasks(%q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestHasAnyAvailableTasks_WithParentID verifies that HasAnyAvailableTasks
// properly passes the parentID through to the tracker's ReadyOpts.
func TestHasAnyAvailableTasks_WithParentID(t *testing.T) {
	tests := []struct {
		name         string
		parentID     string
		wantParentID string
	}{
		{
			name:         "with parent ID passes to tracker",
			parentID:     "EPIC-300",
			wantParentID: "EPIC-300",
		},
		{
			name:         "empty parent ID passes empty to tracker",
			parentID:     "",
			wantParentID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultTracker()
			t.Cleanup(resetDefaultTracker)
			mock := NewMockTracker()
			issues := []BdIssue{{ID: "T-3", Title: "Any task", Status: "open", Design: ""}}
			mock.ReadyResult = issues
			mock.ListResult = issues
			var capturedOpts ReadyOpts
			mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
				capturedOpts = opts
				return issues, nil
			}
			setDefaultTracker(mock)

			got, err := HasAnyAvailableTasks(tt.parentID, "")
			if err != nil {
				t.Fatalf("HasAnyAvailableTasks(%q) unexpected error: %v", tt.parentID, err)
			}

			if !got {
				t.Errorf("HasAnyAvailableTasks(%q) = false, want true", tt.parentID)
			}

			if capturedOpts.ParentID != tt.wantParentID {
				t.Errorf("ReadyOpts.ParentID = %q, want %q", capturedOpts.ParentID, tt.wantParentID)
			}
		})
	}
}

// TestStartTmuxSession_WithParentID verifies that when ParentID is set in AutoModeOptions,
// the --parent flag is included in the loom command passed to tmux.
func TestStartTmuxSession_WithParentID(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping")
	}

	tests := []struct {
		name           string
		parentID       string
		wantParentFlag bool
	}{
		{
			name:           "with parent ID includes --parent flag",
			parentID:       "EPIC-999",
			wantParentFlag: true,
		},
		{
			name:           "empty parent ID excludes --parent flag",
			parentID:       "",
			wantParentFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logFile := filepath.Join(tmpDir, "test.log")

			sessionName := fmt.Sprintf("loom-test-parent-%d-%d", os.Getpid(), time.Now().UnixNano())
			// Clean up tmux session after test
			t.Cleanup(func() {
				exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
			})

			opts := AutoModeOptions{
				AgentType:    "plan",
				AgentName:    "test-agent",
				WorktreePath: tmpDir,
				ParentID:     tt.parentID,
				BackoffBase:  10 * time.Millisecond,
				TaskPause:    10 * time.Millisecond,
			}

			setTmuxRemainOnExit(t)

			err := startTmuxSession(sessionName, opts, logFile)
			if err != nil {
				t.Fatalf("startTmuxSession failed: %v", err)
			}

			// Give tmux a moment to set up
			time.Sleep(300 * time.Millisecond)

			out, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_start_command}").Output() //nolint:norawexec
			if err != nil {
				t.Fatalf("failed to get tmux pane start command: %v", err)
			}
			paneCmd := strings.TrimSpace(string(out))

			if tt.wantParentFlag {
				// The parentID may be shell-quoted, so check for either format
				expectedFlag1 := fmt.Sprintf("--parent %s", tt.parentID)
				expectedFlag2 := fmt.Sprintf("--parent '%s'", tt.parentID)
				if !strings.Contains(paneCmd, expectedFlag1) && !strings.Contains(paneCmd, expectedFlag2) {
					t.Errorf("Expected command to contain --parent flag with %q, got: %s", tt.parentID, paneCmd)
				}
			} else if strings.Contains(paneCmd, "--parent") {
				t.Errorf("Expected command NOT to contain --parent flag, got: %s", paneCmd)
			}
		})
	}
}

// ============================================================================
// fetchReadyIssues Tests (via MockIssueTracker)
// ============================================================================

func TestFetchReadyIssues_EmptyResult(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	mock.ReadyResult = []BdIssue{}
	setDefaultTracker(mock)

	issues, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("fetchReadyIssues() returned %d issues, want 0", len(issues))
	}
}

func TestFetchReadyIssues_ReturnsTrackerResult(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	mock.ReadyResult = []BdIssue{
		{ID: "T-1", Title: "First", Status: "open"},
		{ID: "T-2", Title: "Second", Status: "open", Design: "plan"},
		{ID: "T-3", Title: "Third", Status: "open", IssueType: "epic"},
	}
	setDefaultTracker(mock)

	issues, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("fetchReadyIssues() returned %d issues, want 3", len(issues))
	}
	if issues[0].ID != "T-1" || issues[1].ID != "T-2" || issues[2].ID != "T-3" {
		t.Errorf("fetchReadyIssues() returned unexpected issue IDs")
	}
}

func TestFetchReadyIssues_TrackerError(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	mock.ReadyErr = fmt.Errorf("command failed")
	setDefaultTracker(mock)

	_, err := fetchReadyIssues("", "")
	if err == nil {
		t.Fatal("fetchReadyIssues() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to check ready tasks") {
		t.Errorf("fetchReadyIssues() error = %v, want to contain 'failed to check ready tasks'", err)
	}
}

func TestFetchReadyIssues_PassesParentID(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := fetchReadyIssues("epic-123", "")
	if err != nil {
		t.Fatalf("fetchReadyIssues() unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-123" {
		t.Errorf("ReadyOpts.ParentID = %q, want epic-123", capturedOpts.ParentID)
	}
}

// ============================================================================
// fetchReadyIssues - Repo Label Filtering Tests (via MockIssueTracker)
// ============================================================================

func TestFetchReadyIssues_PassesRepoLabel(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := fetchReadyIssues("", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.Labels) != 1 || capturedOpts.Labels[0] != "repo:frontend" {
		t.Errorf("ReadyOpts.Labels = %v, want [repo:frontend]", capturedOpts.Labels)
	}
}

func TestFetchReadyIssues_NoRepoLabel(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.Labels) != 0 {
		t.Errorf("ReadyOpts.Labels = %v, want nil/empty", capturedOpts.Labels)
	}
}

func TestFetchReadyIssues_PassesBothFilters(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := fetchReadyIssues("E-1", "backend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "E-1" {
		t.Errorf("ReadyOpts.ParentID = %q, want E-1", capturedOpts.ParentID)
	}
	if len(capturedOpts.Labels) != 1 || capturedOpts.Labels[0] != "repo:backend" {
		t.Errorf("ReadyOpts.Labels = %v, want [repo:backend]", capturedOpts.Labels)
	}
}

// ============================================================================
// fetchReadyIssues - Source Repos Filtering Tests (via MockIssueTracker)
// ============================================================================

func TestFetchReadyIssues_PassesSourceRepos(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "repo-a,repo-b")

	_, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.SourceRepos) != 2 || capturedOpts.SourceRepos[0] != "repo-a" || capturedOpts.SourceRepos[1] != "repo-b" {
		t.Errorf("ReadyOpts.SourceRepos = %v, want [repo-a repo-b]", capturedOpts.SourceRepos)
	}
}

func TestFetchReadyIssues_SourceReposWithParent(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "repo-a")

	_, err := fetchReadyIssues("epic-123", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-123" {
		t.Errorf("ReadyOpts.ParentID = %q, want epic-123", capturedOpts.ParentID)
	}
	if len(capturedOpts.SourceRepos) != 1 || capturedOpts.SourceRepos[0] != "repo-a" {
		t.Errorf("ReadyOpts.SourceRepos = %v, want [repo-a]", capturedOpts.SourceRepos)
	}
}

func TestFetchReadyIssues_NoSourceRepos(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ReadyOpts
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)
	t.Setenv("LOOM_SOURCE_REPOS", "")

	_, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedOpts.SourceRepos) != 0 {
		t.Errorf("ReadyOpts.SourceRepos = %v, want nil/empty", capturedOpts.SourceRepos)
	}
}

// ============================================================================
// fetchUnclosedIssueIDs Tests (via MockIssueTracker)
// ============================================================================

func TestFetchUnclosedIssueIDs_NoStatusFilter(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	var capturedOpts ListOpts
	mock.ListFunc = func(_ context.Context, opts ListOpts) ([]BdIssue, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultTracker(mock)

	_, err := fetchUnclosedIssueIDs()
	if err != nil {
		t.Fatalf("fetchUnclosedIssueIDs() unexpected error: %v", err)
	}
	// CRITICAL: verify no implicit status filter
	if capturedOpts.Status != "" {
		t.Errorf("ListOpts.Status = %q, want empty (all statuses)", capturedOpts.Status)
	}
	if capturedOpts.Limit != 500 {
		t.Errorf("ListOpts.Limit = %d, want 500", capturedOpts.Limit)
	}
}

func TestFetchUnclosedIssueIDs_IncludesAllNonClosed(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	mock.ListResult = []BdIssue{
		{ID: "open-1", Status: "open"},
		{ID: "ip-1", Status: "in_progress"},
		{ID: "review-1", Status: "review"},
		{ID: "closed-1", Status: "closed"},
	}
	setDefaultTracker(mock)

	got, err := fetchUnclosedIssueIDs()
	if err != nil {
		t.Fatalf("fetchUnclosedIssueIDs() unexpected error: %v", err)
	}
	if !got["open-1"] {
		t.Error("expected open-1 in unclosed set")
	}
	if !got["ip-1"] {
		t.Error("expected ip-1 in unclosed set")
	}
	if !got["review-1"] {
		t.Error("expected review-1 in unclosed set")
	}
	if got["closed-1"] {
		t.Error("closed-1 should not be in unclosed set")
	}
}

func TestFetchUnclosedIssueIDs_TrackerError(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)
	mock := NewMockTracker()
	mock.ListErr = fmt.Errorf("list failed")
	setDefaultTracker(mock)

	_, err := fetchUnclosedIssueIDs()
	if err == nil {
		t.Fatal("fetchUnclosedIssueIDs() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to list issues") {
		t.Errorf("fetchUnclosedIssueIDs() error = %v, want to contain 'failed to list issues'", err)
	}
}

// ============================================================================
// RunAutoModeLoop - ConsecutiveNoProgress Tests
// ============================================================================

func TestRunAutoModeLoop_ConsecutiveNoProgress(t *testing.T) {
	// Test that the no-progress path is entered when agent exits without claiming a task.
	// The no-progress backoff is 30s/60s/120s which makes testing 3 full iterations slow,
	// so we verify one iteration and then shutdown during the backoff.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Agent succeeds but does NOT claim a task (no UpdateLockTask call)
	shutdown := make(chan struct{})
	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdownCh <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Don't write a TaskID — simulates agent that exits without claiming work.
		// After first invocation, send shutdown during the backoff to exit promptly.
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(shutdown)
		}()
		return nil
	}

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  500 * time.Millisecond, // Long enough for 100ms shutdown to arrive during backoff
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited after shutdown interrupted the backoff
	case <-time.After(10 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit after no-progress + shutdown")
	}

	// Agent should have been invoked exactly once before shutdown interrupted the backoff
	if claudeInvocations != 1 {
		t.Errorf("Expected 1 Claude invocation, got %d", claudeInvocations)
	}
}

func TestRunAutoModeLoop_NoProgressCounterResetOnSuccess(t *testing.T) {
	// Verify that claiming a task after no-progress resets the counter.
	// We test: no-progress → success (claim) → shutdown during next no-progress backoff.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	shutdown := make(chan struct{})
	callNum := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdownCh <-chan struct{}, _ *usage.Collector) error {
		callNum++
		if callNum == 1 {
			// First call: no progress (don't claim)
			return nil
		}
		if callNum == 2 {
			// Second call: claim a task → resets counter
			UpdateLockTask(workDir, "mock-progress", "Mock Task")
			return nil
		}
		// Third call: no progress again, signal shutdown so backoff is interrupted
		close(shutdown)
		return nil
	}

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have been called 3 times: no-progress(backoff) → claim(pause) → no-progress(shutdown)
	if callNum != 3 {
		t.Errorf("Expected 3 Claude invocations, got %d", callNum)
	}
}

// ============================================================================
// NoProgressBackoff Calculation Tests
// ============================================================================

func TestNoProgressBackoff_Calculation(t *testing.T) {
	tests := []struct {
		consecutiveNoProgress int
		expectedBackoff       time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second}, // Capped
		{4, 120 * time.Second}, // Still capped
		{5, 120 * time.Second}, // Still capped
	}

	for _, tt := range tests {
		backoff := time.Duration(30<<(tt.consecutiveNoProgress-1)) * time.Second
		if backoff > 120*time.Second {
			backoff = 120 * time.Second
		}
		if backoff != tt.expectedBackoff {
			t.Errorf("noProgress=%d: backoff=%v, want %v", tt.consecutiveNoProgress, backoff, tt.expectedBackoff)
		}
	}
}

// ============================================================================
// AutoModeState Field Tests
// ============================================================================

func TestAutoModeState_Fields(t *testing.T) {
	now := time.Now()
	state := AutoModeState{
		TasksCompleted:        5,
		ConsecutiveErrors:     2,
		ConsecutiveNoProgress: 1,
		LastTaskTime:          now,
		IdleStartTime:         now.Add(-time.Minute),
		ShouldExit:            true,
		ExitReason:            "test reason",
	}

	if state.TasksCompleted != 5 {
		t.Errorf("TasksCompleted = %d, want 5", state.TasksCompleted)
	}
	if state.ConsecutiveErrors != 2 {
		t.Errorf("ConsecutiveErrors = %d, want 2", state.ConsecutiveErrors)
	}
	if state.ConsecutiveNoProgress != 1 {
		t.Errorf("ConsecutiveNoProgress = %d, want 1", state.ConsecutiveNoProgress)
	}
	if !state.LastTaskTime.Equal(now) {
		t.Errorf("LastTaskTime = %v, want %v", state.LastTaskTime, now)
	}
	if !state.IdleStartTime.Equal(now.Add(-time.Minute)) {
		t.Errorf("IdleStartTime not set correctly")
	}
	if !state.ShouldExit {
		t.Error("ShouldExit = false, want true")
	}
	if state.ExitReason != "test reason" {
		t.Errorf("ExitReason = %q, want %q", state.ExitReason, "test reason")
	}
}

// ============================================================================
// streamUntilExit - Log File Rotation Tests
// ============================================================================

func TestStreamUntilExit_LogFileRotation(t *testing.T) {
	// Test the truncation detection logic used in streamUntilExit's polling loop
	tmpFile, err := os.CreateTemp("", "loom-rotation-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	fileName := tmpFile.Name()
	defer os.Remove(fileName)

	// Write initial content
	initialContent := "first session output that is long enough\n"
	if _, err := tmpFile.WriteString(initialContent); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}
	tmpFile.Close()

	// Record the offset as if we've read all content
	var lastOffset int64 = int64(len(initialContent))

	// Now truncate and write shorter content (simulates log rotation)
	if err := os.WriteFile(fileName, []byte("new\n"), 0644); err != nil {
		t.Fatalf("failed to truncate and rewrite: %v", err)
	}

	// Use streamRemainingLogContent which handles truncation
	streamRemainingLogContent(fileName, &lastOffset)

	// After truncation handling, offset should be at end of new content
	if lastOffset != 4 { // len("new\n")
		t.Errorf("lastOffset after rotation = %d, want 4", lastOffset)
	}
}

// ============================================================================
// streamUntilExit - Shutdown During Stream (requires tmux)
// ============================================================================

func TestStreamUntilExit_ShutdownDuringStream(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-shutdown-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a long-running tmux session
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 300").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	// Create a log file
	tmpFile, err := os.CreateTemp("", "loom-stream-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	// Send shutdown after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - returned promptly after shutdown
	case <-time.After(5 * time.Second):
		t.Error("streamUntilExit did not return after shutdown signal")
	}
}

// ============================================================================
// streamUntilExit - Session Exit Detection (requires tmux)
// ============================================================================

func TestStreamUntilExit_SessionExitDetection(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-exit-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a tmux session with a short-lived command
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "echo done && sleep 0.5").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "loom-stream-exit-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected session exit
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect session exit")
	}
}

// ============================================================================
// streamUntilExit - Signal File Detection (requires tmux)
// ============================================================================

func TestStreamUntilExit_SignalFileDetection(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-stream-signal-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a long-running tmux session
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 300").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "loom-stream-signal-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	// Use a temp dir as the worktree path so the signal file path is deterministic.
	// Resolve symlinks to match what streamUntilExit does internally
	// (macOS: /var/folders → /private/var/folders).
	worktreePath := t.TempDir()
	if absPath, err := filepath.Abs(worktreePath); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			worktreePath = resolved
		}
	}

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	// Create the signal file after a short delay using the resolved path
	go func() {
		time.Sleep(500 * time.Millisecond)
		signalFile := GetSignalFilePath(worktreePath)
		signalDir := filepath.Dir(signalFile)
		os.MkdirAll(signalDir, 0700)
		os.WriteFile(signalFile, []byte("done"), 0644)
	}()

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, worktreePath, attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected signal file
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect signal file")
	}
}

// ============================================================================
// RunAutoModeTmux - No Tasks Available Tests
// ============================================================================

func TestRunAutoModeTmux_NoTasks(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	shutdown := make(chan struct{})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-no-tasks",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomTaskCheck: func() (bool, error) {
			return false, nil // No tasks
		},
	}

	// Send shutdown after a short delay to exit the idle wait loop
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		RunAutoModeTmux(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - exited without creating a session
	case <-time.After(5 * time.Second):
		t.Error("RunAutoModeTmux did not exit with no tasks")
	}
}

func TestRunAutoModeTmux_TaskCheckError(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	tmpDir := t.TempDir()
	shutdown := make(chan struct{})

	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test-err",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
		CustomTaskCheck: func() (bool, error) {
			return false, fmt.Errorf("simulated task check error")
		},
	}

	// The error path in RunAutoModeTmux uses interruptibleSleep, so shutdown
	// will be honored immediately without waiting for the full 5s backoff.
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(shutdown)
	}()

	done := make(chan struct{})
	go func() {
		RunAutoModeTmux(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - handled error and exited
	case <-time.After(5 * time.Second):
		t.Error("RunAutoModeTmux did not exit after task check error")
	}
}

// ============================================================================
// RunAutoModeLoop - Lock State Transitions
// ============================================================================

func TestRunAutoModeLoop_LockStateTransitions(t *testing.T) {
	// Verify UpdateLockState is called with StateIdle at loop start, StateActive
	// before agent invocation, and StateIdle after agent completes.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// In the mock invoker: read lock file and verify State == StateActive
	var stateBeforeAgent string
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		info, err := ReadLockFile(workDir)
		if err != nil {
			t.Errorf("ReadLockFile failed during agent invocation: %v", err)
		} else {
			stateBeforeAgent = info.State
		}
		// Simulate claiming a task so the loop counts progress
		UpdateLockTask(workDir, "mock-1", "Mock Task")
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     1,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	// Verify state was active during agent invocation
	if stateBeforeAgent != StateActive {
		t.Errorf("State during agent invocation = %q, want %q", stateBeforeAgent, StateActive)
	}

	// After RunAutoModeLoop returns: verify state is idle
	info, err := ReadLockFile(tmpDir)
	if err != nil {
		t.Fatalf("ReadLockFile failed after loop: %v", err)
	}
	if info.State != StateIdle {
		t.Errorf("State after loop = %q, want %q", info.State, StateIdle)
	}
}

// ============================================================================
// RunAutoModeLoop - ClearsTaskIDBeforeEachSession
// ============================================================================

func TestRunAutoModeLoop_ClearsTaskIDBeforeEachSession(t *testing.T) {
	// Verify ClearLockTaskID is called before each agent invocation, so
	// leftover task IDs from previous sessions don't cause false progress.
	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	callNum := 0
	taskIDOnEntry := make([]string, 0, 2)
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		// Read the current TaskID to see if it was cleared before invocation
		info, err := ReadLockFile(workDir)
		if err != nil {
			t.Errorf("ReadLockFile failed on call %d: %v", callNum, err)
		} else {
			taskIDOnEntry = append(taskIDOnEntry, info.TaskID)
		}
		// Claim a task to simulate progress
		UpdateLockTask(workDir, fmt.Sprintf("task-%d", callNum), fmt.Sprintf("Task %d", callNum))
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     2,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after max tasks")
	}

	if callNum != 2 {
		t.Fatalf("Expected 2 invocations, got %d", callNum)
	}

	// Both invocations should see an empty TaskID (cleared before each session)
	for i, tid := range taskIDOnEntry {
		if tid != "" {
			t.Errorf("Invocation %d: TaskID on entry = %q, want empty (ClearLockTaskID should have cleared it)", i+1, tid)
		}
	}
}

// ============================================================================
// startTmuxSession - Terminal Dimensions
// ============================================================================

func TestStartTmuxSession_PassesTerminalDimensions(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-dims-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Give tmux a moment to set up
	time.Sleep(300 * time.Millisecond)

	// Query the window dimensions from the created tmux session
	out, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_width} #{window_height}").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to get window dimensions: %v", err)
	}

	dims := strings.TrimSpace(string(out))
	var width, height int
	n, err := fmt.Sscanf(dims, "%d %d", &width, &height)
	if err != nil || n != 2 {
		t.Fatalf("failed to parse tmux window dimensions: %q (err=%v)", dims, err)
	}

	// Verify dimensions are reasonable (> 0)
	if width <= 0 || height <= 0 {
		t.Errorf("tmux window dimensions should be positive: width=%d, height=%d", width, height)
	}

	// If we can get the terminal size, verify the tmux window matches
	if termWidth, termHeight, termErr := getTerminalSize(); termErr == nil && termWidth > 0 && termHeight > 0 {
		if width != termWidth {
			t.Errorf("tmux window width = %d, terminal width = %d (should match)", width, termWidth)
		}
		if height != termHeight {
			t.Errorf("tmux window height = %d, terminal height = %d (should match)", height, termHeight)
		}
	}
}

// ============================================================================
// startTmuxSession - Pipe-Pane and Focus Events
// ============================================================================

func TestStartTmuxSession_PipePaneAndFocusEvents(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	// remain-on-exit keeps the pane alive even when loom exits (not installed in CI)
	setTmuxRemainOnExit(t)

	tmpDir := t.TempDir()
	sessionName := fmt.Sprintf("loom-test-pipe-%d", os.Getpid())
	logFile := filepath.Join(tmpDir, "test.log")

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	opts := AutoModeOptions{
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	err := startTmuxSession(sessionName, opts, logFile)
	if err != nil {
		t.Fatalf("startTmuxSession failed: %v", err)
	}

	// Verify focus-events is off (prevents ^[[I and ^[[O in output)
	out, err := exec.Command("tmux", "show-options", "-t", sessionName, "focus-events").Output() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to query focus-events: %v", err)
	}

	focusEvents := strings.TrimSpace(string(out))
	if !strings.Contains(focusEvents, "off") {
		t.Errorf("focus-events should be off, got: %q", focusEvents)
	}
}

// ============================================================================
// streamUntilExit - Zombie Session Cleanup (requires tmux)
// ============================================================================

func TestStreamUntilExit_ZombieSessionCleanup(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := fmt.Sprintf("loom-test-zombie-%d", os.Getpid())
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:norawexec
	})

	// Create a tmux session with a short-lived command. Use "sleep 2" to give
	// us time to set remain-on-exit before the command exits.
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep 2").Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("failed to create tmux session: %v", err)
	}
	// Set remain-on-exit BEFORE the command exits so session becomes zombie
	if setErr := exec.Command("tmux", "set", "-t", sessionName, "remain-on-exit", "on").Run(); setErr != nil { //nolint:norawexec
		t.Fatalf("failed to set remain-on-exit: %v", setErr)
	}

	// Wait for command to exit (pane becomes dead)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tmuxPaneDead(sessionName) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !tmuxPaneDead(sessionName) {
		t.Fatal("pane did not die within timeout")
	}

	// Session should still exist (zombie state)
	if !tmuxSessionExists(sessionName) {
		t.Fatal("session should still exist in zombie state")
	}

	tmpFile, err := os.CreateTemp("", "loom-zombie-test-*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	logFile := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logFile)

	shutdown := make(chan struct{})
	attachChan := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		streamUntilExit(sessionName, logFile, t.TempDir(), attachChan, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - detected zombie session and cleaned up
	case <-time.After(10 * time.Second):
		close(shutdown)
		t.Error("streamUntilExit did not detect zombie session")
	}

	// Session should have been cleaned up
	if tmuxSessionExists(sessionName) {
		t.Error("zombie session should have been cleaned up")
	}
}

// ============================================================================
// RunAutoModeLoop - Three Consecutive No-Progress Exits
// ============================================================================

func TestRunAutoModeLoop_ThreeConsecutiveNoProgressExits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test in short mode (requires ~90s for backoff waits)")
	}

	oldClaude := claudeNonInteractiveInvoker
	t.Cleanup(func() {
		claudeNonInteractiveInvoker = oldClaude
	})

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]BdIssue{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Agent succeeds but does NOT claim a task (no UpdateLockTask call)
	claudeInvocations := 0
	claudeNonInteractiveInvoker = func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Don't write a TaskID — simulates agent that exits without claiming work
		return nil
	}

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good - should have exited after 3 consecutive no-progress sessions
	case <-time.After(30 * time.Second):
		close(shutdown)
		t.Fatal("RunAutoModeLoop did not exit after 3 consecutive no-progress sessions")
	}

	// Should have been invoked exactly 3 times before exiting
	if claudeInvocations != 3 {
		t.Errorf("Expected 3 Claude invocations, got %d", claudeInvocations)
	}
}
