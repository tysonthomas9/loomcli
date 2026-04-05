package cli

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

func TestNewDaemon_NilConfig(t *testing.T) {
	_, err := NewDaemon(nil, "/tmp", nil, nil)
	if err == nil {
		t.Error("expected error for nil config, got nil")
	}
}

func TestNewDaemon_EmptyAgents(t *testing.T) {
	cfg := &DaemonConfig{
		Agents: []AgentEntry{},
	}
	_, err := NewDaemon(cfg, "/tmp", nil, nil)
	if err == nil {
		t.Error("expected error for empty agents, got nil")
	}
}

func TestDaemon_GetMaxRetries_Default(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}
	if got := d.getMaxRetries(); got != 3 {
		t.Errorf("getMaxRetries() = %d, want 3 (default)", got)
	}
}

func TestDaemon_GetMaxRetries_Custom(t *testing.T) {
	val := 10
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &val,
				},
			},
		},
	}
	if got := d.getMaxRetries(); got != 10 {
		t.Errorf("getMaxRetries() = %d, want 10", got)
	}
}

func TestDaemon_GetBackoffInitial_Default(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}
	if got := d.getBackoffInitial(); got != 2 {
		t.Errorf("getBackoffInitial() = %d, want 2 (default)", got)
	}
}

func TestDaemon_GetBackoffInitial_Custom(t *testing.T) {
	val := 5
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					BackoffInitial: &val,
				},
			},
		},
	}
	if got := d.getBackoffInitial(); got != 5 {
		t.Errorf("getBackoffInitial() = %d, want 5", got)
	}
}

func TestDaemon_GetBackoffMax_Default(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}
	if got := d.getBackoffMax(); got != 300 {
		t.Errorf("getBackoffMax() = %d, want 300 (default)", got)
	}
}

func TestDaemon_GetBackoffMax_Custom(t *testing.T) {
	val := 600
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					BackoffMax: &val,
				},
			},
		},
	}
	if got := d.getBackoffMax(); got != 600 {
		t.Errorf("getBackoffMax() = %d, want 600", got)
	}
}

func TestDaemon_ComputeBackoff(t *testing.T) {
	initial := 2
	maxBack := 300
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					BackoffInitial: &initial,
					BackoffMax:     &maxBack,
				},
			},
		},
	}

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
		ap := &AgentProcess{restartCount: tc.restartCount}
		got := d.computeBackoff(ap)
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

func TestDaemon_ComputeBackoff_OverflowProtection(t *testing.T) {
	initial := 2
	maxBack := 300
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					BackoffInitial: &initial,
					BackoffMax:     &maxBack,
				},
			},
		},
	}

	// Very high restart count should be capped and not overflow
	ap := &AgentProcess{restartCount: 100}
	got := d.computeBackoff(ap)
	if got != 300*time.Second {
		t.Errorf("computeBackoff(restart=100) = %v, want %v (capped)", got, 300*time.Second)
	}
}

func TestDaemon_ShouldRestart_SuccessfulLongRun(t *testing.T) {
	maxRetries := 3
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 0,
		lastStart:    time.Now().Add(-5 * time.Minute), // ran for 5 minutes
		restartCount: 2,
	}

	if !d.shouldRestart(ap) {
		t.Error("shouldRestart should return true for successful long run")
	}

	// Restart count should be reset to 0 after successful long run
	if ap.restartCount != 0 {
		t.Errorf("restartCount should be reset to 0, got %d", ap.restartCount)
	}
}

func TestDaemon_ShouldRestart_MaxRetriesExceeded(t *testing.T) {
	maxRetries := 3
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 1,
		lastStart:    time.Now(),
		restartCount: 3, // Already at max
	}

	if d.shouldRestart(ap) {
		t.Error("shouldRestart should return false when max retries exceeded")
	}
}

func TestDaemon_ShouldRestart_BelowMax(t *testing.T) {
	maxRetries := 5
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 1,
		lastStart:    time.Now(),
		restartCount: 2,
	}

	if !d.shouldRestart(ap) {
		t.Error("shouldRestart should return true when below max retries")
	}
}

func TestDaemon_AgentCount(t *testing.T) {
	d := &Daemon{
		agents: []*AgentProcess{
			{entry: AgentEntry{Worktree: "a"}},
			{entry: AgentEntry{Worktree: "b"}},
			{entry: AgentEntry{Worktree: "c"}},
		},
	}

	if got := d.AgentCount(); got != 3 {
		t.Errorf("AgentCount() = %d, want 3", got)
	}
}

func TestDaemon_Agents_Snapshot(t *testing.T) {
	d := &Daemon{
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "alpha", Role: "plan"},
				worktreePath: "/path/alpha",
				pid:          12345,
				restartCount: 2,
				lastExitCode: 0,
			},
			{
				entry:        AgentEntry{Worktree: "beta", Role: "task"},
				worktreePath: "/path/beta",
				pid:          0,
				restartCount: 0,
			},
		},
	}

	statuses := d.Agents()

	if len(statuses) != 2 {
		t.Fatalf("len(Agents()) = %d, want 2", len(statuses))
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

func TestDaemon_StopAgent_NilProcess(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		// cmd is nil, pid is 0
	}

	// Should be a no-op without panicking
	d.stopAgent(ap, 5*time.Second)
}

func TestDaemon_WaitForAgent_NilCmd(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{},
	}

	ap := &AgentProcess{
		entry: AgentEntry{Worktree: "test"},
		// cmd is nil
	}

	exitCode := d.waitForAgent(ap)
	if exitCode != -1 {
		t.Errorf("waitForAgent with nil cmd = %d, want -1", exitCode)
	}
}

func TestDaemon_ResolveRoleConfig_BuiltIn(t *testing.T) {
	d := &Daemon{
		config:     &DaemonConfig{},
		projectDir: "/tmp",
	}

	// Built-in roles should resolve without error
	for _, role := range []string{"plan", "task"} {
		rc, err := d.resolveRoleConfig(role, 0)
		if err != nil {
			t.Errorf("resolveRoleConfig(%q) error = %v", role, err)
		}
		if rc.Description == "" {
			t.Errorf("resolveRoleConfig(%q) description should not be empty", role)
		}
	}
}

func TestDaemon_ResolveRoleConfig_UnknownRole(t *testing.T) {
	d := &Daemon{
		config:     &DaemonConfig{},
		projectDir: "/tmp",
	}

	_, err := d.resolveRoleConfig("nonexistent", 0)
	if err == nil {
		t.Error("expected error for unknown role, got nil")
	}
}

func TestDaemon_ShouldRestart_NoWorkCount_Increments(t *testing.T) {
	maxRetries := 3
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 1,
		lastStart:    time.Now(),
		lastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Three consecutive NoWork exits
	for i := 1; i <= 3; i++ {
		if !d.shouldRestart(ap) {
			t.Fatalf("shouldRestart should return true for NoWork (iteration %d)", i)
		}
		if ap.noWorkCount != i {
			t.Errorf("noWorkCount = %d after %d NoWork exits, want %d", ap.noWorkCount, i, i)
		}
	}
}

func TestDaemon_ShouldRestart_NoWorkCount_ResetOnCleanSuccess(t *testing.T) {
	maxRetries := 3
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 1,
		lastStart:    time.Now(),
		lastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Two NoWork exits
	d.shouldRestart(ap)
	d.shouldRestart(ap)
	if ap.noWorkCount != 2 {
		t.Fatalf("noWorkCount = %d, want 2 after two NoWork exits", ap.noWorkCount)
	}

	// Clean success exit
	ap.lastExitCode = 0
	ap.lastError = nil
	d.shouldRestart(ap)

	if ap.noWorkCount != 0 {
		t.Errorf("noWorkCount = %d, want 0 after clean success", ap.noWorkCount)
	}
}

func TestDaemon_ShouldRestart_NoWorkCount_ResetOnError(t *testing.T) {
	maxRetries := 10
	d := &Daemon{
		config: &DaemonConfig{
			Daemon: DaemonSettings{
				RestartPolicy: RestartPolicy{
					MaxRetries: &maxRetries,
				},
			},
		},
	}

	ap := &AgentProcess{
		lastExitCode: 1,
		lastStart:    time.Now(),
		lastError:    &agenterr.AgentError{Class: agenterr.NoWork},
	}

	// Two NoWork exits
	d.shouldRestart(ap)
	d.shouldRestart(ap)
	if ap.noWorkCount != 2 {
		t.Fatalf("noWorkCount = %d, want 2 after two NoWork exits", ap.noWorkCount)
	}

	// Timeout error
	ap.lastError = &agenterr.AgentError{Class: agenterr.Timeout}
	d.shouldRestart(ap)

	if ap.noWorkCount != 0 {
		t.Errorf("noWorkCount = %d, want 0 after Timeout error", ap.noWorkCount)
	}
}

func TestDaemon_Agents_Snapshot_NewFields(t *testing.T) {
	backoffTime := time.Now().Add(30 * time.Second)
	d := &Daemon{
		agents: []*AgentProcess{
			{
				entry:        AgentEntry{Worktree: "alpha", Role: "plan"},
				worktreePath: "/path/alpha",
				pid:          12345,
				noWorkCount:  5,
				backoffUntil: backoffTime,
				lastError:    &agenterr.AgentError{Class: agenterr.RateLimited},
			},
		},
	}

	statuses := d.Agents()

	if len(statuses) != 1 {
		t.Fatalf("len(Agents()) = %d, want 1", len(statuses))
	}

	s := statuses[0]
	if s.NoWorkCount != 5 {
		t.Errorf("NoWorkCount = %d, want 5", s.NoWorkCount)
	}
	if !s.BackoffUntil.Equal(backoffTime) {
		t.Errorf("BackoffUntil = %v, want %v", s.BackoffUntil, backoffTime)
	}
	if s.LastErrorClass != "RateLimited" {
		t.Errorf("LastErrorClass = %q, want %q", s.LastErrorClass, "RateLimited")
	}
	if s.RemoteBranch != "origin/main" {
		t.Errorf("RemoteBranch = %q, want %q", s.RemoteBranch, "origin/main")
	}
}
