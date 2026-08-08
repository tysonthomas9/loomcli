package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func newTestSupervisorWithConfig(cfg *config.DaemonConfig) *Supervisor {
	s := &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
	}
	return s
}

func spawnFailureTestConfig(maxRetries int) *config.DaemonConfig {
	return &config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: config.IntPtr(maxRetries),
			},
		},
	}
}

// TestMarkSpawnFailure_SetsSyntheticExitState verifies markSpawnFailure records
// a spawn failure as a synthetic exit: exit code -1, a non-nil SpawnFailure
// error, and no NoWork flag. It must not touch RestartCount; counting belongs
// to shouldRestart.
func TestMarkSpawnFailure_SetsSyntheticExitState(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{Backend: "claude"})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Backend: "claude"},
		RestartCount: 2,
	}

	s.markSpawnFailure(ap, errors.New(`exec: "claude": executable file not found in $PATH`))

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastExitCode != -1 {
		t.Errorf("LastExitCode = %d, want -1", ap.LastExitCode)
	}
	if ap.LastError == nil {
		t.Fatal("LastError = nil, want non-nil SpawnFailure error")
	}
	if ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Errorf("LastError.Class = %v, want SpawnFailure", ap.LastError.Class)
	}
	if ap.LastNoWork {
		t.Error("LastNoWork = true, want false")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (markSpawnFailure must not count)", ap.RestartCount)
	}
}

// TestSpawnFailure_CountsOnceAndRespectsMaxRetries simulates the supervise loop
// for repeated spawn failures, starting from a clean prior run. Each failure
// must count exactly once and, after maxRetries, the agent enters error.
func TestSpawnFailure_CountsOnceAndRespectsMaxRetries(t *testing.T) {
	const maxRetries = 3
	s := newTestSupervisorWithConfig(spawnFailureTestConfig(maxRetries))
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "wt"},
		// Prior run exited cleanly: the dangerous stale state for the reset branch.
		LastExitCode: 0,
		LastError:    nil,
		RestartCount: 0,
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		s.markSpawnFailure(ap, errors.New("spawn failed"))
		if !s.shouldRestart(ap) {
			t.Fatalf("attempt %d: shouldRestart = false, want true (<= maxRetries)", attempt)
		}
		ap.Mu.Lock()
		count := ap.RestartCount
		ap.Mu.Unlock()
		if count != attempt {
			t.Fatalf("attempt %d: RestartCount = %d, want %d", attempt, count, attempt)
		}
	}

	s.markSpawnFailure(ap, errors.New("spawn failed"))
	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true after budget exhausted, want false")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != maxRetries+1 {
		t.Errorf("RestartCount = %d, want %d", ap.RestartCount, maxRetries+1)
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
}

func TestRealSubprocessExitExhaustsMaxRetriesIntoError(t *testing.T) {
	const maxRetries = 0
	binDir := t.TempDir()
	loomPath := filepath.Join(binDir, "loom")
	if err := os.WriteFile(loomPath, []byte("#!/bin/sh\necho 'transient crash from real subprocess'\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write loom shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "loom-config"))
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_WORKSPACE_ID", "")

	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			LogDir:    t.TempDir(),
			EventsDir: t.TempDir(),
			RestartPolicy: config.RestartPolicy{
				MaxRetries:     config.IntPtr(maxRetries),
				BackoffInitial: config.IntPtr(0),
				BackoffMax:     config.IntPtr(0),
			},
		},
	})
	s.ProjectDir = t.TempDir()
	s.Concurrency = NewConcurrencyTracker(nil)
	s.EmitEvent = func(events.Event) {}

	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "real-fail", Role: "plan"},
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}

	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("failed to acquire concurrency slot")
	}
	if err := s.spawnAgent(ap); err != nil {
		s.Concurrency.Release(ap.Entry.Role)
		t.Fatalf("spawnAgent: %v", err)
	}
	exitCode := s.waitForAgent(ap)
	s.classifyAgentExit(ap, exitCode)
	s.Concurrency.Release(ap.Entry.Role)

	ap.Mu.Lock()
	recordedExitCode := ap.LastExitCode
	lastErr := ap.LastError
	ap.Mu.Unlock()
	if recordedExitCode != 1 {
		t.Fatalf("LastExitCode = %d, want 1 from real subprocess", recordedExitCode)
	}
	if lastErr == nil {
		t.Fatal("LastError = nil, want classified subprocess failure")
	}

	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true after real subprocess exhausted maxRetries=0, want false")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 1 {
		t.Errorf("RestartCount = %d, want 1", ap.RestartCount)
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
	if _, ok := s.StoppedAgents["real-fail"]; !ok {
		t.Fatal("agent was not marked stopped for explicit resume")
	}
}

// TestSpawnFailure_AfterNonZeroExitNotDoubleCounted verifies that a spawn
// failure following a non-zero exit is counted exactly once and is classified
// as SpawnFailure rather than inheriting the previous run's stale error.
func TestSpawnFailure_AfterNonZeroExitNotDoubleCounted(t *testing.T) {
	s := newTestSupervisorWithConfig(spawnFailureTestConfig(5))
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "wt"},
		// Stale state from a prior non-zero exit that was already counted once.
		LastExitCode: 1,
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient), Message: "stale"},
		RestartCount: 1,
	}

	s.markSpawnFailure(ap, errors.New("spawn failed"))

	ap.Mu.Lock()
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		ap.Mu.Unlock()
		t.Fatalf("LastError = %v, want SpawnFailure", ap.LastError)
	}
	ap.Mu.Unlock()

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want true (under maxRetries)")
	}

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (single increment, no double-count)", ap.RestartCount)
	}
}

func TestSupervisor_GetMaxRetries_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getMaxRetries(); got != 3 {
		t.Errorf("getMaxRetries() = %d, want 3 (default)", got)
	}
}

func TestSupervisor_GetMaxRetries_Custom(t *testing.T) {
	val := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &val,
			},
		},
	})
	if got := s.getMaxRetries(); got != 10 {
		t.Errorf("getMaxRetries() = %d, want 10", got)
	}
}

func TestSupervisor_GetBackoffInitial_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getBackoffInitial(); got != 2 {
		t.Errorf("getBackoffInitial() = %d, want 2 (default)", got)
	}
}

func TestSupervisor_GetBackoffInitial_Custom(t *testing.T) {
	val := 5
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &val,
			},
		},
	})
	if got := s.getBackoffInitial(); got != 5 {
		t.Errorf("getBackoffInitial() = %d, want 5", got)
	}
}

func TestSupervisor_GetBackoffMax_Default(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	if got := s.getBackoffMax(); got != 300 {
		t.Errorf("getBackoffMax() = %d, want 300 (default)", got)
	}
}

func TestSupervisor_GetBackoffMax_Custom(t *testing.T) {
	val := 600
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffMax: &val,
			},
		},
	})
	if got := s.getBackoffMax(); got != 600 {
		t.Errorf("getBackoffMax() = %d, want 600", got)
	}
}

func TestSupervisor_ComputeBackoff(t *testing.T) {
	initial := 2
	maxBack := 300
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &initial,
				BackoffMax:     &maxBack,
			},
		},
	})

	tests := []struct {
		restartCount int
		expect       time.Duration
	}{
		{0, 2 * time.Second},     // 2 * 2^0 = 2
		{1, 4 * time.Second},     // 2 * 2^1 = 4
		{2, 8 * time.Second},     // 2 * 2^2 = 8
		{3, 16 * time.Second},    // 2 * 2^3 = 16
		{10, 2048 * time.Second}, // 2 * 2^10 = 2048 but capped to 300... wait 2048 < 300? no, 2048 > 300
	}

	for _, tc := range tests {
		ap := &AgentProcess{RestartCount: tc.restartCount}
		got := s.computeBackoff(ap)
		// For large restart counts, result is capped at maxBackoff
		if tc.restartCount >= 8 {
			// 2 * 2^8 = 512 > 300, should be capped
			if got != 300*time.Second {
				t.Errorf("computeBackoff(restart=%d) = %v, want %v (capped)", tc.restartCount, got, 300*time.Second)
			}
		} else {
			if got != tc.expect {
				t.Errorf("computeBackoff(restart=%d) = %v, want %v", tc.restartCount, got, tc.expect)
			}
		}
	}
}

func TestSupervisor_ComputeBackoff_OverflowProtection(t *testing.T) {
	initial := 2
	maxBack := 300
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				BackoffInitial: &initial,
				BackoffMax:     &maxBack,
			},
		},
	})

	// Very high restart count should be capped and not overflow
	ap := &AgentProcess{RestartCount: 100}
	got := s.computeBackoff(ap)
	if got != 300*time.Second {
		t.Errorf("computeBackoff(restart=100) = %v, want %v (capped)", got, 300*time.Second)
	}
}

func TestSupervisor_ShouldRestart_SuccessfulLongRun(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 0,
		LastStart:    time.Now().Add(-5 * time.Minute), // ran for 5 minutes
		RestartCount: 2,
	}

	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true for successful long run")
	}

	// Restart count should be reset to 0 after successful long run
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount should be reset to 0, got %d", ap.RestartCount)
	}
}

func TestSupervisor_ShouldRestart_MaxRetriesExceeded_ErrorsAndBlocksTask(t *testing.T) {
	maxRetries := 3
	mock := clitest.NewMockIssueBackend()
	control := memstore.New()
	ctx := context.Background()
	if _, err := control.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := control.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST",
		Name:         "wt",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Backend: "claude",
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})
	s.IssueBackend = mock
	s.ControlStore = control
	s.WorkspaceID = "TEST"

	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "wt", Role: "task"},
		LastExitCode:   1,
		LastStart:      time.Now(),
		LastError:      &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient), Message: "backend crashed"},
		RestartCount:   3, // Already at max; the next failure exhausts the budget
		AssignedTaskID: "TASK-1",
	}

	if s.shouldRestart(ap) {
		t.Error("shouldRestart should return false when the budget is exhausted")
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
	if ap.RestartCount != maxRetries+1 {
		t.Errorf("RestartCount = %d, want %d (preserved for observability)", ap.RestartCount, maxRetries+1)
	}
	if _, ok := s.StoppedAgents["wt"]; !ok {
		t.Fatal("agent was not marked stopped for explicit resume")
	}
	agent, err := control.Agents().Get(ctx, "TEST", "wt")
	if err != nil {
		t.Fatalf("get control-plane agent: %v", err)
	}
	if agent.State != domain.AgentStateError {
		t.Fatalf("control-plane agent state = %q, want %q", agent.State, domain.AgentStateError)
	}

	var update *backend.UpdateParams
	var updateID string
	var comment *backend.CommentAddParams
	for _, call := range mock.Calls {
		switch call.Method {
		case "Update":
			updateID = call.Args[0].(string)
			params := call.Args[1].(backend.UpdateParams)
			update = &params
		case "AddComment":
			params := call.Args[0].(backend.CommentAddParams)
			comment = &params
		}
	}
	if update == nil {
		t.Fatal("IssueBackend.Update was not called")
	}
	if updateID != "TASK-1" {
		t.Fatalf("Update issue id = %q, want TASK-1", updateID)
	}
	if update.Status == nil || *update.Status != "blocked" {
		t.Fatalf("Update.Status = %v, want blocked", update.Status)
	}
	if update.Assignee == nil || *update.Assignee != "wt" {
		t.Fatalf("Update.Assignee = %v, want wt", update.Assignee)
	}
	if comment == nil {
		t.Fatal("IssueBackend.AddComment was not called")
	}
	if comment.IssueID != "TASK-1" || comment.Author != "loom-daemon" {
		t.Fatalf("comment target/author = %q/%q, want TASK-1/loom-daemon", comment.IssueID, comment.Author)
	}
	if !strings.Contains(comment.Text, "stopped with error") || !strings.Contains(comment.Text, "Automatic retries are stopped") {
		t.Fatalf("comment text does not explain terminal error: %q", comment.Text)
	}
	if strings.Contains(strings.ToLower(comment.Text), "park") {
		t.Fatalf("comment text should not use parked wording: %q", comment.Text)
	}
}

func TestSupervisor_ShouldRestart_BelowMax(t *testing.T) {
	maxRetries := 5
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		RestartCount: 2,
	}

	if !s.shouldRestart(ap) {
		t.Error("shouldRestart should return true when below max retries")
	}
}

// TestSupervisor_ShouldRestart_BackendUnavailable_PreservesBudget is the
// regression guard for LOOM-4: a missing backend CLI detected at runtime must
// keep the agent retrying (it recovers when the binary returns) without eroding
// max_retries, so it never escalates to a permanent failure.
func TestSupervisor_ShouldRestart_BackendUnavailable_PreservesBudget(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome)},
		RestartCount: maxRetries, // already at the limit; a generic error would now fail
	}

	// Repeated BackendUnavailable exits must keep restarting without eroding the
	// budget or tripping the max-retries stop reason.
	for i := 0; i < 5; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should keep restarting on BackendUnavailable (iteration %d)", i)
		}
		if ap.RestartCount != maxRetries {
			t.Fatalf("RestartCount = %d after BackendUnavailable, want %d (budget must not erode)", ap.RestartCount, maxRetries)
		}
		if ap.StopReason == StopReasonMaxRetries {
			t.Fatalf("StopReason set to max-retries on BackendUnavailable (iteration %d)", i)
		}
	}

	// Backoff is the fixed recheck interval, not exponential off RestartCount.
	if got := s.computeBackoff(ap); got != backendUnavailableRecheckInterval {
		t.Errorf("computeBackoff = %v, want %v (fixed recheck)", got, backendUnavailableRecheckInterval)
	}
}

// TestSupervisor_ShouldRestart_Exhaustion_StopsUntilExplicitResume verifies the
// retry-burst contract: failures count up to maxRetries, the exhausting failure
// enters an error state, and no fresh automatic burst is granted.
func TestSupervisor_ShouldRestart_Exhaustion_StopsUntilExplicitResume(t *testing.T) {
	maxRetries := 2
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
	}

	// Failures within budget count up and keep retrying.
	for i := 1; i <= maxRetries; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("iteration %d: shouldRestart = false, want true (within budget)", i)
		}
		if ap.StopReason == StopReasonMaxRetries {
			t.Fatalf("iteration %d: max-retries stop too early (RestartCount=%d, max=%d)", i, ap.RestartCount, maxRetries)
		}
	}

	// The exhausting failure enters error and stops automatic retries.
	if s.shouldRestart(ap) {
		t.Fatal("exhausting failure: shouldRestart = true, want false")
	}
	if ap.StopReason != StopReasonMaxRetries {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetries)
	}
	if ap.RestartCount != maxRetries+1 {
		t.Fatalf("RestartCount = %d, want %d", ap.RestartCount, maxRetries+1)
	}
}

// TestSupervisor_ShouldRestart_Fatal_StillStops is the critical
// regression guard: a fatal error (auth/billing) must stop the agent even when
// the budget is well past exhausted.
func TestSupervisor_ShouldRestart_Fatal_StillStops(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})

	for _, class := range []agenterr.Outcome{
		agenterr.OutcomeFromHarness(wrapper.ErrAuth),
		agenterr.OutcomeFromHarness(wrapper.ErrBilling),
	} {
		ap := &AgentProcess{
			Entry:        config.AgentEntry{Worktree: "wt"},
			LastExitCode: 1,
			LastStart:    time.Now(),
			LastError:    &agenterr.AgentError{Class: class},
			RestartCount: maxRetries + 5, // well past the budget
		}
		if s.shouldRestart(ap) {
			t.Errorf("%v: shouldRestart = true, want false (fatal must stop)", class)
		}
		if ap.StopReason != StopReasonFatalError {
			t.Errorf("%v: StopReason = %q, want %q", class, ap.StopReason, StopReasonFatalError)
		}
	}
}

// TestSupervisor_Error_PreservesObservability verifies the durable signals
// survive a max-retries error: status still carries the terminal reason and the
// error class that caused it.
func TestSupervisor_Error_PreservesObservability(t *testing.T) {
	maxRetries := 1
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "wt", Role: "plan"},
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		RestartCount: maxRetries, // next failure exhausts
	}
	if s.shouldRestart(ap) {
		t.Fatal("shouldRestart = true, want false (error)")
	}

	s.Agents = []*AgentProcess{ap}
	statuses := s.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}
	st := statuses[0]
	if st.StopReason != StopReasonMaxRetries {
		t.Errorf("status.StopReason = %q, want %q", st.StopReason, StopReasonMaxRetries)
	}
	if st.LastErrorClass == "" {
		t.Error("status.LastErrorClass is empty, want the class that caused the error")
	}
}

func TestSupervisor_AgentCount(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.Agents = []*AgentProcess{
		{Entry: config.AgentEntry{Worktree: "a"}},
		{Entry: config.AgentEntry{Worktree: "b"}},
		{Entry: config.AgentEntry{Worktree: "c"}},
	}

	if got := s.AgentCount(); got != 3 {
		t.Errorf("AgentCount() = %d, want 3", got)
	}
}

func TestSupervisor_GetAgents_Snapshot(t *testing.T) {
	cfg := &config.DaemonConfig{}
	s := newTestSupervisorWithConfig(cfg)
	s.Agents = []*AgentProcess{
		{
			Entry:        config.AgentEntry{Worktree: "alpha", Role: "plan"},
			WorktreePath: "/path/alpha",
			Pid:          12345,
			RestartCount: 2,
			LastExitCode: 0,
		},
		{
			Entry:        config.AgentEntry{Worktree: "beta", Role: "task"},
			WorktreePath: "/path/beta",
			Pid:          0,
			RestartCount: 0,
		},
	}

	statuses := s.GetAgents()

	if len(statuses) != 2 {
		t.Fatalf("len(GetAgents()) = %d, want 2", len(statuses))
	}

	if statuses[0].Worktree != "alpha" {
		t.Errorf("statuses[0].Worktree = %q, want %q", statuses[0].Worktree, "alpha")
	}
	if statuses[0].Role != "plan" {
		t.Errorf("statuses[0].Role = %q, want %q", statuses[0].Role, "plan")
	}
	if statuses[0].PID != 12345 {
		t.Errorf("statuses[0].PID = %d, want 12345", statuses[0].PID)
	}
	if statuses[0].RestartCount != 2 {
		t.Errorf("statuses[0].RestartCount = %d, want 2", statuses[0].RestartCount)
	}

	if statuses[1].Worktree != "beta" {
		t.Errorf("statuses[1].Worktree = %q, want %q", statuses[1].Worktree, "beta")
	}
	if statuses[1].PID != 0 {
		t.Errorf("statuses[1].PID = %d, want 0 (not running)", statuses[1].PID)
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_Increments(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
	}

	// Three consecutive NoWork exits
	for i := 1; i <= 3; i++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("shouldRestart should return true for NoWork (iteration %d)", i)
		}
		if ap.NoWorkCount != i {
			t.Errorf("NoWorkCount = %d after %d NoWork exits, want %d", ap.NoWorkCount, i, i)
		}
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_ResetOnCleanSuccess(t *testing.T) {
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
	}

	// Two NoWork exits
	s.shouldRestart(ap)
	s.shouldRestart(ap)
	if ap.NoWorkCount != 2 {
		t.Fatalf("NoWorkCount = %d, want 2 after two NoWork exits", ap.NoWorkCount)
	}

	// Clean success exit
	ap.LastExitCode = 0
	ap.LastError = nil
	s.shouldRestart(ap)

	if ap.NoWorkCount != 0 {
		t.Errorf("NoWorkCount = %d, want 0 after clean success", ap.NoWorkCount)
	}
}

func TestSupervisor_ShouldRestart_NoWorkCount_ResetOnError(t *testing.T) {
	maxRetries := 10
	s := newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{
				MaxRetries: &maxRetries,
			},
		},
	})

	ap := &AgentProcess{
		LastExitCode: 1,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
	}

	// Two NoWork exits
	s.shouldRestart(ap)
	s.shouldRestart(ap)
	if ap.NoWorkCount != 2 {
		t.Fatalf("NoWorkCount = %d, want 2 after two NoWork exits", ap.NoWorkCount)
	}

	// Timeout error
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}
	s.shouldRestart(ap)

	if ap.NoWorkCount != 0 {
		t.Errorf("NoWorkCount = %d, want 0 after Timeout error", ap.NoWorkCount)
	}
}

func TestSupervisor_GetAgents_Snapshot_NewFields(t *testing.T) {
	backoffTime := time.Now().Add(30 * time.Second)
	cfg := &config.DaemonConfig{}
	s := newTestSupervisorWithConfig(cfg)
	s.Agents = []*AgentProcess{
		{
			Entry:        config.AgentEntry{Worktree: "alpha", Role: "plan"},
			WorktreePath: "/path/alpha",
			Pid:          12345,
			NoWorkCount:  5,
			BackoffUntil: backoffTime,
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)},
		},
	}

	statuses := s.GetAgents()

	if len(statuses) != 1 {
		t.Fatalf("len(GetAgents()) = %d, want 1", len(statuses))
	}

	st := statuses[0]
	if st.NoWorkCount != 5 {
		t.Errorf("NoWorkCount = %d, want 5", st.NoWorkCount)
	}
	if !st.BackoffUntil.Equal(backoffTime) {
		t.Errorf("BackoffUntil = %v, want %v", st.BackoffUntil, backoffTime)
	}
	if st.LastErrorClass != "RateLimited" {
		t.Errorf("LastErrorClass = %q, want %q", st.LastErrorClass, "RateLimited")
	}
	if st.RemoteBranch != "origin/main" {
		t.Errorf("RemoteBranch = %q, want %q", st.RemoteBranch, "origin/main")
	}
}

func TestSupervisor_WaitForAgent_NilCmd(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})

	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "test"},
		// Cmd is nil
	}

	exitCode := s.waitForAgent(ap)
	if exitCode != -1 {
		t.Errorf("waitForAgent with nil cmd = %d, want -1", exitCode)
	}
}

func TestSupervisor_ResolveRoleConfig_BuiltIn(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.ProjectDir = "/tmp"

	// Built-in roles should resolve without error
	for _, role := range []string{"plan", "task"} {
		rc, err := s.resolveRoleConfig(role, 0)
		if err != nil {
			t.Errorf("resolveRoleConfig(%q) error = %v", role, err)
		}
		if rc.Description == "" {
			t.Errorf("resolveRoleConfig(%q) description should not be empty", role)
		}
	}
}

func TestSupervisor_ResolveRoleConfig_UnknownRole(t *testing.T) {
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	s.ProjectDir = "/tmp"

	_, err := s.resolveRoleConfig("nonexistent", 0)
	if err == nil {
		t.Error("expected error for unknown role, got nil")
	}
}
