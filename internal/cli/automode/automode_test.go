package automode

import (
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

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// setTmuxRemainOnExit sets remain-on-exit globally so tmux panes stay alive
// even when the loom command exits (loom is not installed in CI environments).
// It starts a keepalive tmux session if no server is running (the tmux server
// exits when the last session is destroyed, so we need our own).
// The original setting and keepalive session are cleaned up via t.Cleanup.
func setTmuxRemainOnExit(t *testing.T) {
	t.Helper()

	// Ensure a tmux server is running. If no server exists, "tmux setw -g" fails silently.
	// Start a keepalive session that sleeps - this guarantees a server for our global setting.
	keepalive := fmt.Sprintf("loom-test-keepalive-%d", os.Getpid())
	if err := exec.Command("tmux", "has-session", "-t", keepalive).Run(); err != nil { //nolint:norawexec
		out, err := exec.Command("tmux", "new-session", "-d", "-s", keepalive, "sleep", "300").CombinedOutput() //nolint:norawexec
		if err != nil {
			t.Skipf("failed to start tmux keepalive session: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		t.Cleanup(func() {
			exec.Command("tmux", "kill-session", "-t", keepalive).Run() //nolint:norawexec
		})
	}

	// remain-on-exit is a window option, so use `setw -g` to set the global
	// default for new windows. Plain `set -g` is interpreted inconsistently
	// across tmux versions and may silently no-op on the window-option
	// namespace, leaving panes free to die the moment their command exits.
	origRemain, _ := exec.Command("tmux", "show", "-gv", "remain-on-exit").Output()                          //nolint:norawexec
	if out, err := exec.Command("tmux", "setw", "-g", "remain-on-exit", "on").CombinedOutput(); err != nil { //nolint:norawexec
		t.Skipf("failed to set tmux remain-on-exit: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	// Verify the setting actually applied. Without it the tests that assert
	// a session is alive right after a quickly-failing command will flake.
	if got, _ := exec.Command("tmux", "show", "-wgv", "remain-on-exit").Output(); strings.TrimSpace(string(got)) != "on" { //nolint:norawexec
		t.Skipf("tmux remain-on-exit not honored by this server (got %q)", strings.TrimSpace(string(got)))
	}
	t.Cleanup(func() {
		val := strings.TrimSpace(string(origRemain))
		if val == "" || val == "off" {
			exec.Command("tmux", "setw", "-g", "remain-on-exit", "off").Run() //nolint:norawexec
		} else {
			exec.Command("tmux", "setw", "-g", "remain-on-exit", val).Run() //nolint:norawexec
		}
	})
}

func TestHasAvailablePlanningTasks(t *testing.T) {
	tests := []struct {
		name        string
		readyOutput string
		readyErr    error
		want        bool
		wantErr     bool
	}{
		{
			name: "has task needing planning (no design)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task has design - not needing planning",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			want: false,
		},
		{
			name: "include tasks with needs-revision label (revision task)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "existing design", Labels: []string{"needs-revision"}},
			}),
			want: true,
		},
		{
			name: "skip in_progress tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip review tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "In review", Status: "review", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip epics",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			want: false,
		},
		{
			name:        "empty list",
			readyOutput: "[]",
			want:        false,
		},
		{
			name: "mixed - one valid task",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Has design and needs-revision", Status: "open", Design: "plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Work on me", Status: "open", Design: ""},
				{ID: "T-3", Title: "Has design", Status: "open", Design: "Already planned"},
			}),
			want: true,
		},
		{
			name: "blocked tasks excluded by backend (not in ready results)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-0", Title: "Blocker", Status: "open", Design: "has design"},
			}),
			want: false, // T-0 has design so not available for planning; blocked T-1 not returned by backend
		},
		{
			name: "parent-child dependency does not block planning",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task with parent-child dep", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task with design and parent-child dep not needing planning",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task with deps and design", Status: "open", Design: "Approved plan"},
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultIssueBackend()
			t.Cleanup(resetDefaultIssueBackend)
			mock := NewMockIssueBackend()
			var issues []backend.IssueData
			if tt.readyOutput != "" {
				json.Unmarshal([]byte(tt.readyOutput), &issues)
			}
			if tt.readyErr != nil {
				mock.ReadyErr = tt.readyErr
				mock.ListErr = tt.readyErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultIssueBackend(mock)

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
		name        string
		readyOutput string
		readyErr    error
		want        bool
		wantErr     bool
	}{
		{
			name: "has task with design - ready for implementation",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Implementation plan here"},
			}),
			want: true,
		},
		{
			name: "task has no design - not ready for implementation",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip tasks with needs-revision label even with design",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
			}),
			want: false,
		},
		{
			name: "skip in_progress tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip review tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "In review", Status: "review", Design: "Has design"},
			}),
			want: false,
		},
		{
			name: "skip epics even with design",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: "Has design"},
			}),
			want: false,
		},
		{
			name:        "empty list",
			readyOutput: "[]",
			want:        false,
		},
		{
			name: "mixed - one valid task with design",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Has needs-revision label", Status: "open", Design: "Has design", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "No design yet", Status: "open", Design: ""},
				{ID: "T-3", Title: "Ready to implement", Status: "open", Design: "Detailed plan"},
			}),
			want: true,
		},
		{
			name: "blocked tasks excluded by backend (not in ready results)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-0", Title: "Blocker", Status: "open"},
			}),
			want: false, // T-0 has no design so not available for implementation; blocked T-1 not returned by backend
		},
		{
			name: "parent-child dependency does not block implementation",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Ready with parent-child dep", Status: "open", Design: "Implementation plan"},
			}),
			want: true,
		},
		{
			name: "task with parent-child dep but no design not ready",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Not ready with deps", Status: "open", Design: ""},
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDefaultIssueBackend()
			t.Cleanup(resetDefaultIssueBackend)
			mock := NewMockIssueBackend()
			var issues []backend.IssueData
			if tt.readyOutput != "" {
				json.Unmarshal([]byte(tt.readyOutput), &issues)
			}
			if tt.readyErr != nil {
				mock.ReadyErr = tt.readyErr
				mock.ListErr = tt.readyErr
			} else {
				mock.ReadyResult = issues
				mock.ListResult = issues
			}
			setDefaultIssueBackend(mock)

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// waitForTmuxSession waits for a tmux session to be visible to the server.
// Under CI load, the server's `has-session` query can return false right
// after startTmuxSession returns successfully; this poll closes the race.
func waitForTmuxSession(sessionName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tmuxSessionExists(sessionName) {
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
	tmpDir := t.TempDir()
	signalFile := filepath.Join(tmpDir, "received-sigint")
	readyFile := filepath.Join(tmpDir, "trap-installed")

	// Trap SIGINT to write a signal file, then announce that the trap is
	// installed via readyFile before the long sleep starts. The test waits
	// for readyFile below so it cannot race ahead and send Ctrl+C before
	// the trap has been registered — under CI load, shell startup can take
	// >100ms, and a too-early C-c hits the default SIGINT handler and
	// kills the shell before the trap fires.
	trapScript := fmt.Sprintf(`trap 'echo received > %s; exit 0' INT; touch %s; sleep 30`, signalFile, readyFile)

	// Create tmux session running the trap script
	err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sh", "-c", trapScript).Run() //nolint:norawexec
	if err != nil {
		t.Fatalf("Failed to create tmux session: %v", err)
	}

	// Wait until the shell has installed the trap and is in `sleep`.
	if !waitForFile(readyFile, 10*time.Second) {
		t.Fatal("Timeout waiting for trap-installed marker — shell never reached `sleep`")
	}

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	_, err := getPaneState("nonexistent-test-session-12345")
	if err == nil {
		t.Error("expected error for non-existent session")
	}
}

func TestPaneState_Fields(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Should not panic for non-existent file
	var offset int64 = 0
	streamRemainingLogContent("/nonexistent/path/to/file.log", &offset)

	// Offset should remain 0
	if offset != 0 {
		t.Errorf("offset = %d, want 0 for non-existent file", offset)
	}
}

func TestStreamRemainingLogContent_ReadsIncrementalContent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set a task
	err = UpdateLockTask(tmpDir, "loom-123", "Test Task")
	if err != nil {
		t.Fatalf("UpdateLockTask failed: %v", err)
	}

	if !agentClaimedTask(tmpDir, "", nil) {
		t.Error("agentClaimedTask() = false, want true when TaskID is set")
	}
}

func TestAgentClaimedTask_WithoutTaskID(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	tmpDir := t.TempDir()

	// No lock file — daemon never ran or failed before writing lock. No progress.
	if agentClaimedTask(tmpDir, "", nil) {
		t.Error("agentClaimedTask() = true, want false when lock file doesn't exist (no progress)")
	}
}

func TestAgentClaimedTask_AfterClear(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Set task, then clear it
	UpdateLockTask(tmpDir, "loom-123", "Test Task")
	ClearLockTaskID(tmpDir)

	if agentClaimedTask(tmpDir, "", nil) {
		t.Error("agentClaimedTask() = true, want false after ClearLockTaskID")
	}
}

func TestAgentClaimedTask_ClearThenReclaim(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	err := AcquireLock(tmpDir, "plan", "falcon")
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer ReleaseLock(tmpDir)

	// Simulate auto-mode cycle: clear → agent claims new task
	UpdateLockTask(tmpDir, "loom-old", "Old Task")
	ClearLockTaskID(tmpDir)

	if agentClaimedTask(tmpDir, "", nil) {
		t.Error("after clear: agentClaimedTask() should be false")
	}

	UpdateLockTask(tmpDir, "loom-new", "New Task")

	if !agentClaimedTask(tmpDir, "", nil) {
		t.Error("after reclaim: agentClaimedTask() should be true")
	}

	// Verify it's the new task
	info, _ := ReadLockFile(tmpDir)
	if info.TaskID != "loom-new" {
		t.Errorf("Expected TaskID 'loom-new', got '%s'", info.TaskID)
	}
}

// ============================================================================
// Tmux Auto Mode Lock Lifecycle Tests
// ============================================================================

// Simulates the tmux auto mode cycle where the daemon exits without claiming
// a task (e.g. no plannable tasks found). The lock file should remain on disk
// with an empty TaskID so the parent correctly detects no progress.
func TestTmuxCycle_DaemonExitsWithoutClaimingTask(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	tmpDir := t.TempDir()

	// Parent removes any old lock (start of cycle)
	lockPath := filepath.Join(tmpDir, LockFileName)
	_ = os.Remove(lockPath)

	// Daemon acquires lock
	if err := AcquireLock(tmpDir, "plan", "falcon"); err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// Daemon (Claude) claims a task via loom claim
	if err := UpdateLockTask(tmpDir, "loom-abc", "Implement feature"); err != nil {
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Same input should always produce the same hash
	hash1 := workspaceHash("/some/path")
	hash2 := workspaceHash("/some/path")

	if hash1 != hash2 {
		t.Errorf("workspaceHash not deterministic: got %q and %q", hash1, hash2)
	}
}

func TestWorkspaceHash_Length(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// Verify against a pre-computed sha256 value to ensure the implementation
	// matches: sha256("/some/path")[:8] hex-encoded
	hash := workspaceHash("/some/path")
	expected := "eda6cf0b63f1a1d2"

	if hash != expected {
		t.Errorf("workspaceHash(%q) = %q, want %q", "/some/path", hash, expected)
	}
}

func TestWorkspaceHash_EmptyString(t *testing.T) {
	t.Parallel()
	// Empty string should still produce a valid 16-char hex hash
	hash := workspaceHash("")

	if len(hash) != 16 {
		t.Errorf("workspaceHash(%q) length = %d, want 16", "", len(hash))
	}
}

func TestStreamRemainingLogContent_HandlesLogTruncation(t *testing.T) {
	t.Parallel()
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
	// Setup temp directory with lock file
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock issue-store ready to return tasks (so loop would continue without shutdown)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Available task", Status: "open", Design: "Has design"},
			}),
		}
	}})

	// Track if Claude was invoked
	claudeInvoked := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Mock issue-store ready to always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Track Claude invocations
	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", claudeInvocations), "Mock Task")
		return nil
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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

	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", claudeInvocations), "Mock Task")
		return nil
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		return nil
	})

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
		CustomTaskCheck: func() (bool, error) {
			return false, nil // No tasks
		},
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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

	// The agent may be invoked even when no tasks are reported, since
	// the loop always tries a task session; shutdown or consecutive-no-progress
	// terminates the loop.
}

func TestRunAutoModeLoop_NoTasks(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	checkCount := 0

	agentInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		agentInvocations++
		return nil
	})

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
		CustomTaskCheck: func() (bool, error) {
			checkCount++
			return false, nil // No tasks
		},
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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

	// The agent gets invoked even when no tasks are reported (the loop
	// always tries a task session), but should exit quickly via the
	// consecutive-no-progress detection since no tasks are claimed.
}

func TestRunAutoModeLoop_TaskExecution(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	promptsReceived := []string{}
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		promptsReceived = append(promptsReceived, prompt)
		// Simulate the agent claiming a task by writing a TaskID to the lock file
		_ = UpdateLockTask(workDir, "T-1", "Test task")
		return nil
	})

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
		CustomTaskCheck: func() (bool, error) {
			return true, nil // Always has tasks
		},
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Always return tasks
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Always return error
	errorCount := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		errorCount++
		return fmt.Errorf("simulated error %d", errorCount)
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITHOUT design (needs planning)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Needs planning", Status: "open", Design: ""},
			}),
		}
	}})

	var receivedPrompt string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, "mock-plan-1", "Mock Plan Task")
		return nil
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-plan-prompt-for-" + name
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

	// Verify that plan prompt was generated
	expectedPrompt := "test-plan-prompt-for-planner"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Plan agent did not receive planning prompt")
	}
}

func TestRunAutoModeLoop_TaskAgentType(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Return a task WITH design (ready for implementation)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Ready to implement", Status: "open", Design: "Design here"},
			}),
		}
	}})

	var receivedPrompt string
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		receivedPrompt = prompt
		// Simulate task claiming by writing a TaskID to the lock file
		UpdateLockTask(workDir, "mock-task-1", "Mock Task")
		return nil
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-task-prompt-for-" + name
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

	// Verify that task prompt was generated
	expectedPrompt := "test-task-prompt-for-worker"
	if receivedPrompt != expectedPrompt {
		t.Errorf("Task agent did not receive task prompt")
	}
}

func TestRunAutoModeLoop_ErrorRecovery(t *testing.T) {
	// Test that a successful task resets the error counter
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	// Pattern: error, error, success, error, error, error (should exit on 6th)
	callNum := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		callNum++
		// Errors on calls 1, 2, 4, 5, 6
		// Success on call 3
		if callNum == 3 {
			// Simulate task claiming so the loop counts this as progress
			UpdateLockTask(workDir, "mock-recovery", "Mock Recovery Task")
			return nil // Success resets error counter
		}
		return fmt.Errorf("error %d", callNum)
	})

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
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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

func TestRunAutoModeLoop_ReadyQueryError(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// CustomTaskCheck returns an error (simulating issue-store error)
	readyErrorCount := 0

	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		return nil
	})

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
		CustomTaskCheck: func() (bool, error) {
			readyErrorCount++
			return false, fmt.Errorf("issue-store error")
		},
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
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
		t.Fatal("RunAutoModeLoop did not exit")
	}

	// Should have tried task check
	if readyErrorCount == 0 {
		t.Error("Should have attempted task check")
	}

	// A failed scan exits through the work-scan-failure path; it must not be
	// treated as idle and start an agent invocation with no verified work.
	if claudeInvocations != 0 {
		t.Errorf("Agent invocations = %d, want 0 after ready-query failure", claudeInvocations)
	}
}

func TestRunAutoModeLoop_ShutdownDuringBackoff(t *testing.T) {
	// Test that shutdown is respected during the error backoff sleep
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvocations++
		return fmt.Errorf("error")
	})

	shutdown := make(chan struct{})

	// Close shutdown shortly after first error (during backoff period)
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(shutdown)
	}()

	opts := AutoModeOptions{
		Interval:     5,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		// Long exponential base so the (Unknown-classified) error's backoff
		// sleep is still in progress when shutdown closes at 200ms.
		BackoffBase: 5 * time.Second,
		TaskPause:   10 * time.Millisecond,
		CustomPromptGen: func(name string, _ *WorkspaceConfig) string {
			return "test-prompt-for-" + name
		},
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

	// Poll for the session to be queryable rather than fixed-sleep — under
	// CI load the tmux server may need >100ms to register the new session.
	deadline := time.Now().Add(5 * time.Second)
	var state *PaneState
	for time.Now().Before(deadline) {
		state, err = getPaneState(sessionName)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("getPaneState failed after polling: %v", err)
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

	if !waitForTmuxSession(sessionName, 5*time.Second) {
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

	if !waitForTmuxSession(sessionName, 5*time.Second) {
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

	if !waitForTmuxSession(sessionName, 5*time.Second) {
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
		name        string
		readyOutput string
		readyErr    error
		want        bool
		wantErr     bool
	}{
		{
			name: "task with no design - available",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "task with design - available",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "Some design"},
			}),
			want: true,
		},
		{
			name: "task with needs-revision label - available (for HasAny)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "open", Design: "", Labels: []string{"needs-revision"}},
			}),
			want: true,
		},
		{
			name: "skip in_progress tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Add feature", Status: "in_progress", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip review tasks",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "In review", Status: "review", Design: ""},
			}),
			want: false,
		},
		{
			name: "skip epics",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
			}),
			want: false,
		},
		{
			name:        "empty list",
			readyOutput: "[]",
			want:        false,
		},
		{
			name: "mixed - epic + valid task",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Valid task with revision", Status: "open", Design: "plan", Labels: []string{"needs-revision"}},
				{ID: "T-2", Title: "Big Epic", Status: "open", IssueType: "epic", Design: ""},
				{ID: "T-3", Title: "Valid task", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name: "blocked tasks excluded by backend (only epics returned)",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-0", Title: "Blocker", Status: "open", IssueType: "epic"},
			}),
			want: false, // only epic returned; blocked T-1 filtered out by backend
		},
		{
			name: "parent-child dependency does not block",
			readyOutput: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Child task", Status: "open", Design: ""},
			}),
			want: true,
		},
		{
			name:     "issue-store command error",
			readyErr: fmt.Errorf("issue-store error"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
				return CommandResult{Stdout: tt.readyOutput, Err: tt.readyErr}
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

// TestHasOpenBlockers is kept for existing behavior but delegates to
// HasUnclosedBlockers (now in taskfilter.go). Comprehensive tests
// are in taskfilter_test.go TestHasUnclosedBlockers.
