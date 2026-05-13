package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// requireIntPtrD is a test helper to check pointer int values in daemon tests.
func requireIntPtrD(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

// makeSupervisorConfig creates a DaemonConfig with defaults for testing.
func makeSupervisorConfig(agents []cfgpkg.AgentEntry, roles map[string]cfgpkg.RoleConfig) *cfgpkg.DaemonConfig {
	return &cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			PIDFile: ".loom/s.Pid",
			LogDir:  ".loom/logs",
			RestartPolicy: cfgpkg.RestartPolicy{
				MaxRetries:     cfgpkg.IntPtr(3),
				BackoffInitial: cfgpkg.IntPtr(2),
				BackoffMax:     cfgpkg.IntPtr(300),
			},
		},
		Roles:  roles,
		Agents: agents,
	}
}

func TestNewDaemon(t *testing.T) {
	t.Skip("TestNewDaemon requires NewDaemon which lives in daemon package")
}

func TestComputeBackoff(t *testing.T) {
	// Create a daemon with known config values
	config := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
		nil,
	)

	// Override restart policy for predictable testing
	config.Daemon.RestartPolicy.BackoffInitial = cfgpkg.IntPtr(2)
	config.Daemon.RestartPolicy.BackoffMax = cfgpkg.IntPtr(300)

	cfg := config
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

	t.Run("restartCount=0 returns initial backoff", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 0}
		backoff := s.computeBackoff(ap)

		// initial * 2^0 = 2 * 1 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=1 returns 4s", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 1}
		backoff := s.computeBackoff(ap)

		// initial * 2^1 = 2 * 2 = 4s
		want := 4 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=5 returns 64s", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 5}
		backoff := s.computeBackoff(ap)

		// initial * 2^5 = 2 * 32 = 64s
		want := 64 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("large restartCount is capped at BackoffMax", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 20}
		backoff := s.computeBackoff(ap)

		// 2 * 2^20 = 2097152s, should be capped at 300s
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=7 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 7}
		backoff := s.computeBackoff(ap)

		// 2 * 2^7 = 256s, which is < 300s, so not capped
		want := 256 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=8 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{RestartCount: 8}
		backoff := s.computeBackoff(ap)

		// 2 * 2^8 = 512s > 300s, should be capped
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited uses rate_limit_backoff as initial", func(t *testing.T) {
		ap := &AgentProcess{
			RateRetryCount: 0,
			LastError:      &agenterr.AgentError{Class: agenterr.RateLimited},
		}
		backoff := s.computeBackoff(ap)

		// default rate_limit_backoff=30 * 2^0 = 30s
		want := 30 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited respects RetryAfter hint", func(t *testing.T) {
		ap := &AgentProcess{
			RateRetryCount: 0,
			LastError: &agenterr.AgentError{
				Class:      agenterr.RateLimited,
				RetryAfter: 60 * time.Second,
			},
		}
		backoff := s.computeBackoff(ap)

		// RetryAfter (60s) > computed (30s), so use RetryAfter
		want := 60 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited RetryAfter capped at rate_limit_max_wait", func(t *testing.T) {
		ap := &AgentProcess{
			RateRetryCount: 0,
			LastError: &agenterr.AgentError{
				Class:      agenterr.RateLimited,
				RetryAfter: 600 * time.Second, // 10 minutes
			},
		}
		backoff := s.computeBackoff(ap)

		// Default rate_limit_max_wait=300s, so cap at 300s
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("rate limited exponential growth", func(t *testing.T) {
		ap := &AgentProcess{
			RateRetryCount: 2,
			LastError:      &agenterr.AgentError{Class: agenterr.RateLimited},
		}
		backoff := s.computeBackoff(ap)

		// 30 * 2^2 = 120s
		want := 120 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("timeout uses timeout_backoff as initial", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 0,
			LastError:    &agenterr.AgentError{Class: agenterr.Timeout},
		}
		backoff := s.computeBackoff(ap)

		// default timeout_backoff=5 * 2^0 = 5s
		want := 5 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("transient uses standard backoff_initial", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 0,
			LastError:    &agenterr.AgentError{Class: agenterr.Transient},
		}
		backoff := s.computeBackoff(ap)

		// default backoff_initial=2 * 2^0 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("nil lastError uses standard backoff", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 1,
			LastError:    nil,
		}
		backoff := s.computeBackoff(ap)

		// standard: 2 * 2^1 = 4s
		want := 4 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("NoWork uses fixed no_work_backoff", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 5, // should be irrelevant
			LastError:    &agenterr.AgentError{Class: agenterr.NoWork},
		}
		backoff := s.computeBackoff(ap)

		// default no_work_backoff=30s (fixed, no exponential)
		want := 30 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})
}

func TestShouldRestart(t *testing.T) {
	t.Run("successful run (exit 0, long runtime) resets counter and returns true", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 2,                                // had previous restarts
			LastExitCode: 0,                                // successful exit
			LastStart:    time.Now().Add(-2 * time.Minute), // ran for >1 minute
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for successful long run")
		}
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.RestartCount)
		}
	})

	t.Run("successful short run resets counter", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 1,
			LastExitCode: 0,                                 // successful exit
			LastStart:    time.Now().Add(-30 * time.Second), // ran for <1 minute
			LastError:    nil,                               // clean success
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		// Counter should be reset — clean success always resets regardless of runtime
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.RestartCount)
		}
	})

	t.Run("failed run (non-zero exit) increments counter and returns true if under limit", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 1,
			LastExitCode: 1, // non-zero exit
			LastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.RestartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.RestartCount)
		}
	})

	t.Run("counter exceeds maxRetries returns false", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 3, // at limit
			LastExitCode: 1, // non-zero exit
			LastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := s.shouldRestart(ap)
		// After increment, count becomes 4 which exceeds maxRetries of 3
		if result {
			t.Error("shouldRestart() = true, want false (counter exceeds max)")
		}
		if ap.RestartCount != 4 {
			t.Errorf("restartCount = %d, want 4", ap.RestartCount)
		}
	})

	t.Run("counter at exactly maxRetries still allows restart", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 2, // one below limit
			LastExitCode: 1, // non-zero exit
			LastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := s.shouldRestart(ap)
		// After increment, count becomes 3 which equals maxRetries
		if !result {
			t.Error("shouldRestart() = false, want true (counter at max)")
		}
		if ap.RestartCount != 3 {
			t.Errorf("restartCount = %d, want 3", ap.RestartCount)
		}
	})

	t.Run("maxRetries=0 means no retries allowed", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(0)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 0,
			LastExitCode: 1, // non-zero exit
			LastStart:    time.Now().Add(-2 * time.Minute),
		}

		result := s.shouldRestart(ap)
		// After increment, count becomes 1 which exceeds maxRetries of 0
		if result {
			t.Error("shouldRestart() = true, want false (maxRetries=0)")
		}
	})

	t.Run("fatal error (AuthFailure) stops immediately", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount: 0,
			LastExitCode: 1,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    &agenterr.AgentError{Class: agenterr.AuthFailure, Message: "invalid API key"},
		}

		result := s.shouldRestart(ap)
		if result {
			t.Error("shouldRestart() = true, want false for AuthFailure (fatal)")
		}
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should not be incremented for fatal)", ap.RestartCount)
		}
	})

	t.Run("fatal error (BillingError) stops immediately", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount: 0,
			LastExitCode: 1,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    &agenterr.AgentError{Class: agenterr.BillingError, Message: "payment required"},
		}

		result := s.shouldRestart(ap)
		if result {
			t.Error("shouldRestart() = true, want false for BillingError (fatal)")
		}
	})

	t.Run("rate limited with no_count=true does not count toward retries", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		// RateLimitNoCount defaults to true (nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount: 2,
			LastExitCode: 1,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for RateLimited with no_count=true")
		}
		if ap.RestartCount != 2 {
			t.Errorf("restartCount = %d, want 2 (should be unchanged for rate-limited)", ap.RestartCount)
		}
		if ap.RateRetryCount != 1 {
			t.Errorf("rateRetryCount = %d, want 1", ap.RateRetryCount)
		}
	})

	t.Run("rate limited with no_count=false counts normally", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		config.Daemon.RestartPolicy.RateLimitNoCount = cfgpkg.BoolPtr(false)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount: 2,
			LastExitCode: 1,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true (count 3 <= maxRetries 3)")
		}
		if ap.RestartCount != 3 {
			t.Errorf("restartCount = %d, want 3 (should be incremented for rate-limited with no_count=false)", ap.RestartCount)
		}
	})

	t.Run("successful run resets rateRetryCount", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount:   2,
			RateRetryCount: 5,
			LastExitCode:   0,
			LastStart:      time.Now().Add(-2 * time.Minute),
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for successful long run")
		}
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.RestartCount)
		}
		if ap.RateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset)", ap.RateRetryCount)
		}
	})

	t.Run("timeout error counts toward retries", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount: 1,
			LastExitCode: 137,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    &agenterr.AgentError{Class: agenterr.Timeout, Message: "connection timed out"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.RestartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.RestartCount)
		}
	})

	t.Run("NoWork does not count toward retries and always restarts", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(0) // even with 0 retries allowed
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount:   3,
			RateRetryCount: 2,
			LastExitCode:   0,
			LastStart:      time.Now().Add(-10 * time.Second), // short run
			LastError:      &agenterr.AgentError{Class: agenterr.NoWork, Message: "no claimable tasks"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for NoWork (always restart)")
		}
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset for NoWork)", ap.RestartCount)
		}
		if ap.RateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset for NoWork)", ap.RateRetryCount)
		}
	})

	t.Run("NoWork on fallback backend preserves currentBackendIdx", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan", FallbackBackends: []string{"codex"}}},
			nil,
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			CurrentBackendIdx: 1, // already on fallback backend
			RestartCount:      1,
			LastExitCode:      0,
			LastStart:         time.Now().Add(-10 * time.Second),
			LastError:         &agenterr.AgentError{Class: agenterr.NoWork, Message: "no claimable tasks"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true for NoWork")
		}
		if ap.CurrentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1 (NoWork should not reset failover state)", ap.CurrentBackendIdx)
		}
	})

	t.Run("NoWork on fallback periodically retries primary backend", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan", FallbackBackends: []string{"codex"}}},
			nil,
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			CurrentBackendIdx: 1,
			NoWorkCount:       2,
			LastExitCode:      0,
			LastError:         &agenterr.AgentError{Class: agenterr.NoWork, Message: "no claimable tasks"},
		}

		if !s.shouldRestart(ap) {
			t.Fatal("shouldRestart() = false, want true for NoWork")
		}
		if ap.CurrentBackendIdx != 0 {
			t.Errorf("currentBackendIdx = %d, want 0 after repeated NoWork on fallback", ap.CurrentBackendIdx)
		}
	})

	t.Run("nil lastError counts toward retries normally", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			RestartCount: 1,
			LastExitCode: 1,
			LastStart:    time.Now().Add(-2 * time.Minute),
			LastError:    nil, // no classification available
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.RestartCount != 2 {
			t.Errorf("restartCount = %d, want 2", ap.RestartCount)
		}
	})

	t.Run("non-rate error resets rateRetryCount", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test"},
			RestartCount:   1,
			RateRetryCount: 5,
			LastExitCode:   1,
			LastStart:      time.Now().Add(-2 * time.Minute),
			LastError:      &agenterr.AgentError{Class: agenterr.Transient, Message: "server error"},
		}

		result := s.shouldRestart(ap)
		if !result {
			t.Error("shouldRestart() = false, want true")
		}
		if ap.RateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset on non-rate error)", ap.RateRetryCount)
		}
	})
}

func TestResolveRoleConfig(t *testing.T) {
	t.Run("built-in role plan returns valid config", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/tmp", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		rc, err := s.resolveRoleConfig("plan", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(plan) error = %v", err)
		}
		if rc.Description == "" {
			t.Error("Description is empty, want non-empty for built-in role")
		}
		if !strings.Contains(rc.Description, "plan") {
			t.Errorf("Description = %q, want contains 'plan'", rc.Description)
		}
		if rc.TaskFilter != "needs_plan" {
			t.Errorf("TaskFilter = %q, want needs_plan", rc.TaskFilter)
		}
	})

	t.Run("built-in role task returns valid config", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/tmp", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		rc, err := s.resolveRoleConfig("task", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(task) error = %v", err)
		}
		if rc.Description == "" {
			t.Error("Description is empty, want non-empty for built-in role")
		}
		if !strings.Contains(rc.Description, "task") {
			t.Errorf("Description = %q, want contains 'task'", rc.Description)
		}
		if rc.TaskFilter != "has_design" {
			t.Errorf("TaskFilter = %q, want has_design", rc.TaskFilter)
		}
	})

	t.Run("built-in role with prompt_file returns explicit error", func(t *testing.T) {
		config := makeSupervisorConfig(
			nil,
			map[string]cfgpkg.RoleConfig{
				"plan": {
					Description: "custom planner",
					PromptFile:  "prompts/plan.md",
				},
			},
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/tmp", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		_, err := s.resolveRoleConfig("plan", 0)
		if err == nil {
			t.Fatal("resolveRoleConfig(plan) error = nil, want explicit built-in prompt_file error")
		}
		if !strings.Contains(err.Error(), "built-in role") || !strings.Contains(err.Error(), "prompt_file") {
			t.Fatalf("error = %q, want built-in role prompt_file message", err.Error())
		}
	})

	t.Run("custom role with valid prompt_file works", func(t *testing.T) {
		tmpDir := t.TempDir()
		promptFile := filepath.Join(tmpDir, "prompt.md")
		if err := os.WriteFile(promptFile, []byte("test prompt"), 0644); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			nil,
			map[string]cfgpkg.RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "prompt.md",
					TaskFilter:  "review",
				},
			},
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: tmpDir, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		rc, err := s.resolveRoleConfig("reviewer", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(reviewer) error = %v", err)
		}
		if rc.Description != "Code reviewer" {
			t.Errorf("Description = %q, want %q", rc.Description, "Code reviewer")
		}
		if rc.TaskFilter != "review" {
			t.Errorf("TaskFilter = %q, want %q", rc.TaskFilter, "review")
		}
		// PromptFile should be resolved to absolute path
		if !filepath.IsAbs(rc.PromptFile) {
			t.Errorf("PromptFile = %q, want absolute path", rc.PromptFile)
		}
		if rc.PromptFile != promptFile {
			t.Errorf("PromptFile = %q, want %q", rc.PromptFile, promptFile)
		}
	})

	t.Run("custom role in config without prompt_file returns error", func(t *testing.T) {
		config := makeSupervisorConfig(
			nil,
			map[string]cfgpkg.RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					// missing PromptFile
				},
			},
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/tmp", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		_, err := s.resolveRoleConfig("reviewer", 0)
		if err == nil {
			t.Fatal("expected error for missing prompt_file")
		}
		if !strings.Contains(err.Error(), "missing prompt_file") {
			t.Errorf("error = %q, want contains 'missing prompt_file'", err.Error())
		}
	})

	t.Run("custom role not found in config returns error", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil) // no custom roles
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/tmp", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		_, err := s.resolveRoleConfig("unknown_role", 0)
		if err == nil {
			t.Fatal("expected error for unknown role")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("custom role with non-existent prompt file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()

		config := makeSupervisorConfig(
			nil,
			map[string]cfgpkg.RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  "nonexistent.md",
				},
			},
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: tmpDir, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		_, err := s.resolveRoleConfig("reviewer", 0)
		if err == nil {
			t.Fatal("expected error for non-existent prompt file")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q, want contains 'not found'", err.Error())
		}
	})

	t.Run("custom role with absolute prompt path works", func(t *testing.T) {
		tmpDir := t.TempDir()
		promptFile := filepath.Join(tmpDir, "prompt.md")
		if err := os.WriteFile(promptFile, []byte("test prompt"), 0644); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			nil,
			map[string]cfgpkg.RoleConfig{
				"reviewer": {
					Description: "Code reviewer",
					PromptFile:  promptFile, // absolute path
				},
			},
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, ProjectDir: "/different/dir", Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		rc, err := s.resolveRoleConfig("reviewer", 0)
		if err != nil {
			t.Fatalf("resolveRoleConfig(reviewer) error = %v", err)
		}
		if rc.PromptFile != promptFile {
			t.Errorf("PromptFile = %q, want %q", rc.PromptFile, promptFile)
		}
	})
}

func TestBuiltInRoles(t *testing.T) {
	t.Run("plan is a built-in role", func(t *testing.T) {
		if !BuiltInRoles["plan"] {
			t.Error("BuiltInRoles[plan] = false, want true")
		}
	})

	t.Run("task is a built-in role", func(t *testing.T) {
		if !BuiltInRoles["task"] {
			t.Error("BuiltInRoles[task] = false, want true")
		}
	})

	t.Run("unknown role is not built-in", func(t *testing.T) {
		if BuiltInRoles["reviewer"] {
			t.Error("BuiltInRoles[reviewer] = true, want false")
		}
	})
}

func TestClassifyAgentExit_WatchdogIdleNoTaskIsNoWork(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "agent.log")
	if err := os.WriteFile(logPath, []byte("request timed out while idle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan", Backend: "codex"}},
		nil,
	)
	cfg := config
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "test", Role: "plan", Backend: "codex"},
		WorktreePath: tmpDir,
		LogFilePath:  logPath,
		StopReason:   StopReasonWatchdog,
	}

	s.classifyAgentExit(ap, 137)

	if ap.LastError == nil || ap.LastError.Class != agenterr.NoWork {
		t.Fatalf("LastError = %#v, want NoWork for watchdog-stopped idle agent", ap.LastError)
	}
	if !ap.LastNoWork {
		t.Fatal("LastNoWork = false, want true")
	}
}

func TestAgentProcess(t *testing.T) {
	t.Run("initial state has zero values", func(t *testing.T) {
		ap := &AgentProcess{
			Entry: cfgpkg.AgentEntry{Worktree: "test", Role: "plan"},
		}

		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0", ap.RestartCount)
		}
		if ap.Pid != 0 {
			t.Errorf("pid = %d, want 0", ap.Pid)
		}
		if ap.LastExitCode != 0 {
			t.Errorf("lastExitCode = %d, want 0", ap.LastExitCode)
		}
		if ap.Cmd != nil {
			t.Error("cmd != nil, want nil")
		}
	})
}

func TestDaemonAgents(t *testing.T) {
	t.Skip("TestDaemonAgents requires NewDaemon which lives in daemon package")
}

func TestGetOutputTimeout(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 900 {
			t.Errorf("getOutputTimeout() = %d, want 900", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(600)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 600 {
			t.Errorf("getOutputTimeout() = %d, want 600", got)
		}
	})

	t.Run("returns 0 when disabled", func(t *testing.T) {
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(0)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 0 {
			t.Errorf("getOutputTimeout() = %d, want 0", got)
		}
	})

	t.Run("env var override beats unset config", func(t *testing.T) {
		t.Setenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS", "42")
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 42 {
			t.Errorf("getOutputTimeout() = %d, want 42 (env override)", got)
		}
	})

	t.Run("env var beats configured value", func(t *testing.T) {
		t.Setenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS", "42")
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(600)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 42 {
			t.Errorf("getOutputTimeout() = %d, want 42 (env beats config)", got)
		}
	})

	t.Run("invalid env var falls through to config", func(t *testing.T) {
		t.Setenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS", "not-a-number")
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(600)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 600 {
			t.Errorf("getOutputTimeout() = %d, want 600 (invalid env should fall through)", got)
		}
	})

	t.Run("empty env var falls through to default", func(t *testing.T) {
		t.Setenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS", "")
		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
			nil,
		)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		got := s.GetOutputTimeout()
		if got != 900 {
			t.Errorf("getOutputTimeout() = %d, want 900 (empty env should fall through)", got)
		}
	})
}

func TestCheckAgentHealth_Watchdog(t *testing.T) {
	t.Run("does not kill agent with recent output", func(t *testing.T) {
		// Create a temporary log file with recent modification time
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("recent output\n"), 0600); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:       cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:         99999999, // fake PID that won't exist
			LogFilePath: logPath,
			LastStart:   time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		// Should not panic or kill anything — agent has recent output
		s.checkAgentHealth()

		// Agent should still have its PID (not killed)
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (should not be killed)", pid)
		}
	})

	t.Run("kills agent with stale output", func(t *testing.T) {
		// Create a temporary log file with old modification time
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("old output\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// Set modification time to 20 minutes ago
		oldTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:       cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:         99999999, // fake PID — stopAgent will fail gracefully
			LogFilePath: logPath,
			LastStart:   time.Now().Add(-25 * time.Minute),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		// This will try to stop the agent (stopAgent handles non-existent PIDs gracefully)
		s.checkAgentHealth()
		// We can't easily assert stopAgent was called since the PID doesn't exist,
		// but the code path should execute without panic
	})

	t.Run("skips watchdog when disabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("old output\n"), 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(0) // disabled

		ap := &AgentProcess{
			Entry:       cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:         99999999,
			LogFilePath: logPath,
			LastStart:   time.Now().Add(-25 * time.Minute),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Agent should still have its PID (watchdog disabled, so no kill)
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (watchdog disabled, should not kill)", pid)
		}
	})

	t.Run("uses lastStart when log not yet written", func(t *testing.T) {
		// Agent just spawned, log file exists but hasn't been written to
		tmpDir := t.TempDir()
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte{}, 0600); err != nil {
			t.Fatal(err)
		}
		// Log file mtime is before lastStart (file was created before agent started)
		oldTime := time.Now().Add(-30 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(300) // 5 minutes

		// lastStart is 2 minutes ago — well within timeout
		ap := &AgentProcess{
			Entry:       cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:         99999999,
			LogFilePath: logPath,
			LastStart:   time.Now().Add(-2 * time.Minute),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Should NOT be killed — lastStart (2 min ago) is within 5 min timeout
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (should use lastStart, not stale log mtime)", pid)
		}
	})

	t.Run("does not kill agent with fresh transcript", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create transcript file with recent mtime (tier 1 — should take precedence)
		txPath := filepath.Join(tmpDir, "transcript.jsonl")
		if err := os.WriteFile(txPath, []byte("recent transcript\n"), 0600); err != nil {
			t.Fatal(err)
		}

		// Create log file with OLD mtime (tier 2 — should be ignored because transcript is fresh)
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("old log output\n"), 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999, // fake PID that won't exist
			LogFilePath:    logPath,
			TranscriptPath: txPath,
			LastStart:      time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Agent should still have its PID (transcript is fresh, takes precedence over stale log)
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (fresh transcript should prevent kill)", pid)
		}
	})

	t.Run("kills agent with stale transcript and stale log", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldTime := time.Now().Add(-20 * time.Minute)

		// Create transcript file with OLD mtime
		txPath := filepath.Join(tmpDir, "transcript.jsonl")
		if err := os.WriteFile(txPath, []byte("stale transcript\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(txPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		// Create log file with OLD mtime
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("stale log\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999, // fake PID — stopAgent will fail gracefully
			LogFilePath:    logPath,
			TranscriptPath: txPath,
			LastStart:      time.Now().Add(-25 * time.Minute),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		// This will try to stop the agent (stopAgent handles non-existent PIDs gracefully).
		// Both transcript and log are stale, so the watchdog should trigger.
		s.checkAgentHealth()
		// The code path executes stopAgent without panic — PID doesn't exist so it's a no-op.
	})

	t.Run("falls back to log when transcript path is empty", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create log file with recent mtime
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("recent log output\n"), 0600); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999,
			LogFilePath:    logPath,
			TranscriptPath: "", // empty — should fall back to log
			LastStart:      time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Agent should NOT be killed — log is fresh and transcript path is empty (fallback works)
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (fresh log fallback should prevent kill)", pid)
		}
	})

	t.Run("falls back to log when transcript file does not exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create log file with recent mtime
		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("recent log output\n"), 0600); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999,
			LogFilePath:    logPath,
			TranscriptPath: filepath.Join(tmpDir, "nonexistent-transcript.jsonl"), // does not exist
			LastStart:      time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Agent should NOT be killed — transcript doesn't exist, falls back to fresh log
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (nonexistent transcript should fall back to fresh log)", pid)
		}
	})

	t.Run("uses latest activity when transcript is stale but log is fresh", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldTime := time.Now().Add(-20 * time.Minute)

		txPath := filepath.Join(tmpDir, "transcript.jsonl")
		if err := os.WriteFile(txPath, []byte("startup hook\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(txPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		logPath := filepath.Join(tmpDir, "task-test.log")
		if err := os.WriteFile(logPath, []byte("fresh stdout\n"), 0600); err != nil {
			t.Fatal(err)
		}
		freshTime := time.Now()
		if err := os.Chtimes(logPath, freshTime, freshTime); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start sleep: %v", err)
		}
		done := make(chan struct{})
		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Cmd:            cmd,
			Pid:            cmd.Process.Pid,
			LogFilePath:    logPath,
			TranscriptPath: txPath,
			LastStart:      time.Now().Add(-25 * time.Minute),
		}
		go func() {
			_ = cmd.Wait()
			ap.Mu.Lock()
			ap.Pid = 0
			ap.Cmd = nil
			ap.Mu.Unlock()
			close(done)
		}()
		t.Cleanup(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("sleep process did not exit")
			}
		})

		s := &Supervisor{EmitEvent: func(events.Event) {}}
		s.checkWatchdog(ap, 60, logPath, ap.LastStart, ap.Entry.Worktree)

		select {
		case <-done:
			t.Fatal("watchdog killed agent even though the log file had newer activity")
		default:
		}
	})

	t.Run("does not kill when no activity sources exist", func(t *testing.T) {
		tmpDir := t.TempDir()

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999,
			LogFilePath:    filepath.Join(tmpDir, "nonexistent.log"),              // does not exist
			TranscriptPath: filepath.Join(tmpDir, "nonexistent-transcript.jsonl"), // does not exist
			LastStart:      time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Agent should NOT be killed — no activity sources exist, so watchdog has nothing to check
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (no activity sources should not trigger kill)", pid)
		}
	})

	t.Run("uses lastStart floor for transcript mtime", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create transcript file with mtime BEFORE lastStart
		txPath := filepath.Join(tmpDir, "transcript.jsonl")
		if err := os.WriteFile(txPath, []byte("old transcript\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// Set transcript mtime to 1 hour ago (well before lastStart)
		oldTime := time.Now().Add(-1 * time.Hour)
		if err := os.Chtimes(txPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		config := makeSupervisorConfig(
			[]cfgpkg.AgentEntry{{Worktree: "test", Role: "task"}},
			nil,
		)
		config.Daemon.RestartPolicy.OutputTimeout = cfgpkg.IntPtr(60) // 60 seconds

		// lastStart is 30 seconds ago — within 60s timeout
		// Transcript mtime is 1 hour ago, but lastStart floor should override
		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: "test", Role: "task"},
			Pid:            99999999,
			LogFilePath:    "", // no log — only transcript tier
			TranscriptPath: txPath,
			LastStart:      time.Now().Add(-30 * time.Second),
		}

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
			Agents:         []*AgentProcess{ap},
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}

		s.checkAgentHealth()

		// Should NOT be killed — lastStart (30s ago) is used as floor and is within 60s timeout
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()
		if pid != 99999999 {
			t.Errorf("pid = %d, want 99999999 (lastStart floor should prevent kill)", pid)
		}
	})
}

func TestGetRateLimitBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitBackoff(); got != 30 {
			t.Errorf("getRateLimitBackoff() = %d, want 30", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitBackoff = cfgpkg.IntPtr(60)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitBackoff(); got != 60 {
			t.Errorf("getRateLimitBackoff() = %d, want 60", got)
		}
	})
}

func TestGetRateLimitMaxWait(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitMaxWait(); got != 300 {
			t.Errorf("getRateLimitMaxWait() = %d, want 300", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitMaxWait = cfgpkg.IntPtr(600)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitMaxWait(); got != 600 {
			t.Errorf("getRateLimitMaxWait() = %d, want 600", got)
		}
	})
}

func TestGetRateLimitNoCount(t *testing.T) {
	t.Run("returns default true when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitNoCount(); !got {
			t.Errorf("getRateLimitNoCount() = %v, want true", got)
		}
	})

	t.Run("returns false when configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Daemon.RestartPolicy.RateLimitNoCount = cfgpkg.BoolPtr(false)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getRateLimitNoCount(); got {
			t.Errorf("getRateLimitNoCount() = %v, want false", got)
		}
	})
}

func TestGetTimeoutBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getTimeoutBackoff(); got != 5 {
			t.Errorf("getTimeoutBackoff() = %d, want 5", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Daemon.RestartPolicy.TimeoutBackoff = cfgpkg.IntPtr(10)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getTimeoutBackoff(); got != 10 {
			t.Errorf("getTimeoutBackoff() = %d, want 10", got)
		}
	})
}

func TestGetNoWorkBackoff(t *testing.T) {
	t.Run("returns default when not configured", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getNoWorkBackoff(); got != 30 {
			t.Errorf("getNoWorkBackoff() = %d, want 30", got)
		}
	})

	t.Run("returns configured value", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Daemon.RestartPolicy.NoWorkBackoff = cfgpkg.IntPtr(45)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}
		if got := s.getNoWorkBackoff(); got != 45 {
			t.Errorf("getNoWorkBackoff() = %d, want 45", got)
		}
	})
}

func TestGetEffectiveBackend(t *testing.T) {
	t.Run("index 0 returns primary backend", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Backend = "global"
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			CurrentBackendIdx: 0,
		}

		got := s.GetEffectiveBackend(ap)
		if got != "claude" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "claude")
		}
	})

	t.Run("index 0 falls back to config backend", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		config.Backend = "codex"
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Backend: "", FallbackBackends: []string{"opencode"}},
			CurrentBackendIdx: 0,
		}

		got := s.GetEffectiveBackend(ap)
		if got != "codex" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "codex")
		}
	})

	t.Run("index 1 returns first fallback", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			CurrentBackendIdx: 1,
		}

		got := s.GetEffectiveBackend(ap)
		if got != "codex" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "codex")
		}
	})

	t.Run("index 2 returns second fallback", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
			CurrentBackendIdx: 2,
		}

		got := s.GetEffectiveBackend(ap)
		if got != "opencode" {
			t.Errorf("getEffectiveBackend() = %q, want %q", got, "opencode")
		}
	})

	t.Run("index beyond fallbacks returns primary", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Backend: "claude", FallbackBackends: []string{"codex"}},
			CurrentBackendIdx: 5,
		}

		got := s.GetEffectiveBackend(ap)
		if got != "claude" {
			t.Errorf("getEffectiveBackend() = %q, want %q (should return primary when beyond fallbacks)", got, "claude")
		}
	})
}

func TestTryFallbackBackend(t *testing.T) {
	t.Run("ModelNotFound triggers immediate failover", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", Backend: "claude", FallbackBackends: []string{"codex"}},
			CurrentBackendIdx: 0,
			RestartCount:      2,
			RateRetryCount:    1,
			LastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := s.tryFallbackBackend(ap)
		if !result {
			t.Error("tryFallbackBackend() = false, want true for ModelNotFound")
		}
		if ap.CurrentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1", ap.CurrentBackendIdx)
		}
		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset)", ap.RestartCount)
		}
		if ap.RateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset)", ap.RateRetryCount)
		}
	})

	t.Run("RateLimited with rateRetryCount <= 3 does not trigger failover", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			CurrentBackendIdx: 0,
			RateRetryCount:    2,
			LastError:         &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for RateLimited with count <= 3")
		}
		if ap.CurrentBackendIdx != 0 {
			t.Errorf("currentBackendIdx = %d, want 0 (unchanged)", ap.CurrentBackendIdx)
		}
	})

	t.Run("RateLimited with rateRetryCount > 3 triggers failover", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			CurrentBackendIdx: 0,
			RateRetryCount:    4,
			LastError:         &agenterr.AgentError{Class: agenterr.RateLimited, Message: "rate limited"},
		}

		result := s.tryFallbackBackend(ap)
		if !result {
			t.Error("tryFallbackBackend() = false, want true for RateLimited with count > 3")
		}
		if ap.CurrentBackendIdx != 1 {
			t.Errorf("currentBackendIdx = %d, want 1", ap.CurrentBackendIdx)
		}
	})

	t.Run("no fallback backends configured returns false", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:     cfgpkg.AgentEntry{Worktree: "test"},
			LastError: &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false (no fallback backends)")
		}
	})

	t.Run("all backends exhausted returns false", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex", "opencode"}},
			CurrentBackendIdx: 2, // already on last fallback (total 3 backends)
			LastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "model not found"},
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false (all backends exhausted)")
		}
	})

	t.Run("AuthFailure does not trigger failover", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:     cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			LastError: &agenterr.AgentError{Class: agenterr.AuthFailure, Message: "invalid API key"},
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for AuthFailure")
		}
	})

	t.Run("Transient does not trigger failover", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:     cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			LastError: &agenterr.AgentError{Class: agenterr.Transient, Message: "server error"},
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for Transient")
		}
	})

	t.Run("nil lastError returns false", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:     cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			LastError: nil,
		}

		result := s.tryFallbackBackend(ap)
		if result {
			t.Error("tryFallbackBackend() = true, want false for nil lastError")
		}
	})

	t.Run("failover resets restartCount and rateRetryCount", func(t *testing.T) {
		config := makeSupervisorConfig(nil, nil)
		cfg := config
		s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", FallbackBackends: []string{"codex"}},
			CurrentBackendIdx: 0,
			RestartCount:      5,
			RateRetryCount:    3,
			LastError:         &agenterr.AgentError{Class: agenterr.ModelNotFound, Message: "not found"},
		}

		s.tryFallbackBackend(ap)

		if ap.RestartCount != 0 {
			t.Errorf("restartCount = %d, want 0 (should be reset on failover)", ap.RestartCount)
		}
		if ap.RateRetryCount != 0 {
			t.Errorf("rateRetryCount = %d, want 0 (should be reset on failover)", ap.RateRetryCount)
		}
	})
}

func TestShouldRestart_ResetsBackendOnSuccess(t *testing.T) {
	config := makeSupervisorConfig(
		[]cfgpkg.AgentEntry{{Worktree: "test", Role: "plan"}},
		nil,
	)
	config.Daemon.RestartPolicy.MaxRetries = cfgpkg.IntPtr(3)
	cfg := config
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

	ap := &AgentProcess{
		RestartCount:      1,
		RateRetryCount:    2,
		CurrentBackendIdx: 2, // on second fallback
		LastExitCode:      0,
		LastStart:         time.Now().Add(-2 * time.Minute),
	}

	result := s.shouldRestart(ap)
	if !result {
		t.Error("shouldRestart() = false, want true for successful long run")
	}
	if ap.CurrentBackendIdx != 0 {
		t.Errorf("currentBackendIdx = %d, want 0 (should reset to primary on success)", ap.CurrentBackendIdx)
	}
}

func TestBuildCommand_UsesEffectiveBackend(t *testing.T) {
	config := makeSupervisorConfig(nil, nil)
	config.Backend = "claude"
	cfg := config
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg }, Shutdown: make(chan struct{}), StoppedAgents: make(map[string]struct{}), Agents: make([]*AgentProcess, 0), EmitEvent: func(events.Event) {}}

	t.Run("uses primary backend at index 0", func(t *testing.T) {
		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
			WorktreePath:      "/tmp/test",
			CurrentBackendIdx: 0,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--backend claude") {
			t.Errorf("buildCommand args = %q, want to contain '--backend claude'", args)
		}
	})

	t.Run("uses fallback backend at index 1", func(t *testing.T) {
		ap := &AgentProcess{
			Entry:             cfgpkg.AgentEntry{Worktree: "test", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
			WorktreePath:      "/tmp/test",
			CurrentBackendIdx: 1,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--backend codex") {
			t.Errorf("buildCommand args = %q, want to contain '--backend codex'", args)
		}
	})
}

func TestAgents_IncludesCurrentBackend(t *testing.T) {
	config := makeSupervisorConfig(nil, nil)
	config.Backend = "claude"
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return config },
		Agents: []*AgentProcess{
			{
				Entry:             cfgpkg.AgentEntry{Worktree: "alpha", Role: "plan", Backend: "claude", FallbackBackends: []string{"codex"}},
				WorktreePath:      "/path/alpha",
				CurrentBackendIdx: 0,
			},
			{
				Entry:             cfgpkg.AgentEntry{Worktree: "beta", Role: "task", Backend: "claude", FallbackBackends: []string{"codex", "opencode"}},
				WorktreePath:      "/path/beta",
				CurrentBackendIdx: 2,
			},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}

	statuses := s.GetAgents()

	if statuses[0].CurrentBackend != "claude" {
		t.Errorf("statuses[0].CurrentBackend = %q, want %q", statuses[0].CurrentBackend, "claude")
	}
	if statuses[1].CurrentBackend != "opencode" {
		t.Errorf("statuses[1].CurrentBackend = %q, want %q", statuses[1].CurrentBackend, "opencode")
	}
}

// TestBuildCommand_BackendResolutionChain verifies the per-agent > per-role > project > global precedence.
func TestBuildCommand_BackendResolutionChain(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("per-agent backend wins over per-role and project", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig {
				return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: "global-backend"}
			},
			ProjectDir:    tmpDir,
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
			EmitEvent:     func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan", Backend: "agent-backend"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "agent-backend" {
			t.Errorf("backend = %q, want %q (per-agent should win)", foundBackend, "agent-backend")
		}
	})

	t.Run("per-role backend wins when per-agent is empty", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig {
				return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: "global-backend"}
			},
			ProjectDir:    tmpDir,
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
			EmitEvent:     func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"}, // no Backend
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent", Backend: "role-backend"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "role-backend" {
			t.Errorf("backend = %q, want %q (per-role should win)", foundBackend, "role-backend")
		}
	})

	t.Run("project backend used when per-agent and per-role are empty", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig {
				return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}, Backend: "project-backend"}
			},
			ProjectDir:    tmpDir,
			Shutdown:      make(chan struct{}),
			StoppedAgents: make(map[string]struct{}),
			EmitEvent:     func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"}, // no Backend
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		foundBackend := ""
		for i, arg := range cmd.Args {
			if arg == "--backend" && i+1 < len(cmd.Args) {
				foundBackend = cmd.Args[i+1]
			}
		}
		if foundBackend != "project-backend" {
			t.Errorf("backend = %q, want %q (project should be used)", foundBackend, "project-backend")
		}
	})

	t.Run("no backend flag when all levels are empty", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} }, // no Backend
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		for _, arg := range cmd.Args {
			if arg == "--backend" {
				t.Error("--backend should not be present when all levels are empty")
			}
		}
	})
}

// TestBuildCommand_ToolConstraintEnvVars verifies LOOM_ALLOWED_TOOLS, LOOM_DENIED_TOOLS,
// and LOOM_READ_ONLY are set in cmd.Env when role has constraints.
func TestBuildCommand_ToolConstraintEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("allowed tools env var set", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent", AllowedTools: []string{"read", "grep", "glob"}},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_ALLOWED_TOOLS=read,grep,glob" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_ALLOWED_TOOLS=read,grep,glob not found in cmd.Env")
		}
	})

	t.Run("denied tools env var set", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent", DeniedTools: []string{"bash", "write", "edit"}},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_DENIED_TOOLS=bash,write,edit" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_DENIED_TOOLS=bash,write,edit not found in cmd.Env")
		}
	})

	t.Run("read-only env var set", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent", ReadOnly: true},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_READ_ONLY=1" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_READ_ONLY=1 not found in cmd.Env")
		}
	})

	t.Run("all constraint env vars set together", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig: cfgpkg.RoleConfig{
				Description:  "Built-in plan agent",
				AllowedTools: []string{"read"},
				DeniedTools:  []string{"write"},
				ReadOnly:     true,
			},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		foundAllowed, foundDenied, foundReadOnly := false, false, false
		for _, env := range cmd.Env {
			switch {
			case env == "LOOM_ALLOWED_TOOLS=read":
				foundAllowed = true
			case env == "LOOM_DENIED_TOOLS=write":
				foundDenied = true
			case env == "LOOM_READ_ONLY=1":
				foundReadOnly = true
			}
		}
		if !foundAllowed {
			t.Error("LOOM_ALLOWED_TOOLS not found")
		}
		if !foundDenied {
			t.Error("LOOM_DENIED_TOOLS not found")
		}
		if !foundReadOnly {
			t.Error("LOOM_READ_ONLY not found")
		}
	})
}

// TestBuildCommand_RoutingEnvVars verifies LOOM_ROLE_SKILLS, LOOM_ROLE_PATH_PATTERNS,
// LOOM_ROLE_MAX_PRIORITY, LOOM_ROLE_TASK_FILTER, and LOOM_ROLE are set in cmd.Env.
func TestBuildCommand_RoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	maxP := 2

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan", PathPatterns: []string{"cmd/**"}},
		RoleConfig: cfgpkg.RoleConfig{
			Description:  "Built-in plan agent",
			Skills:       []string{"go", "daemon"},
			PathPatterns: []string{"internal/**"},
			MaxPriority:  &maxP,
			TaskFilter:   "needs_plan",
		},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_ROLE_SKILLS"]; !ok || v != "go,daemon" {
		t.Errorf("LOOM_ROLE_SKILLS = %q, want %q", v, "go,daemon")
	}
	if v, ok := envMap["LOOM_ROLE_PATH_PATTERNS"]; !ok || v != "internal/**" {
		t.Errorf("LOOM_ROLE_PATH_PATTERNS = %q, want %q", v, "internal/**")
	}
	if v, ok := envMap["LOOM_ROLE_MAX_PRIORITY"]; !ok || v != "2" {
		t.Errorf("LOOM_ROLE_MAX_PRIORITY = %q, want %q", v, "2")
	}
	if v, ok := envMap["LOOM_ROLE_TASK_FILTER"]; !ok || v != "needs_plan" {
		t.Errorf("LOOM_ROLE_TASK_FILTER = %q, want %q", v, "needs_plan")
	}
	if v, ok := envMap["LOOM_ROLE"]; !ok || v != "plan" {
		t.Errorf("LOOM_ROLE = %q, want %q", v, "plan")
	}
	if v, ok := envMap["LOOM_AGENT_PATH_PATTERNS"]; !ok || v != "cmd/**" {
		t.Errorf("LOOM_AGENT_PATH_PATTERNS = %q, want %q", v, "cmd/**")
	}
}

// TestBuildCommand_NoRoutingEnvVars verifies no routing env vars are set when
// role has no routing config (existing behavior).
func TestBuildCommand_NoRoutingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"}, // no routing config
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE_SKILLS=") {
			t.Error("LOOM_ROLE_SKILLS should not be set when Skills is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_PATH_PATTERNS=") {
			t.Error("LOOM_ROLE_PATH_PATTERNS should not be set when PathPatterns is empty")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_MAX_PRIORITY=") {
			t.Error("LOOM_ROLE_MAX_PRIORITY should not be set when MaxPriority is nil")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_TASK_FILTER=") {
			t.Error("LOOM_ROLE_TASK_FILTER should not be set when TaskFilter is empty")
		}
		if strings.HasPrefix(env, "LOOM_AGENT_PATH_PATTERNS=") {
			t.Error("LOOM_AGENT_PATH_PATTERNS should not be set when AgentEntry has no PathPatterns")
		}
		if strings.HasPrefix(env, "LOOM_SOURCE_REPOS=") {
			t.Error("LOOM_SOURCE_REPOS should not be set when agent has no repo affinity")
		}
	}
	// LOOM_ROLE should always be set
	foundRole := false
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE=") {
			foundRole = true
		}
	}
	if !foundRole {
		t.Error("LOOM_ROLE should always be set")
	}
}

// TestBuildCommand_SourceReposInjected verifies LOOM_SOURCE_REPOS is set when
// the agent has Repos declared and s.Repos provides the RepoConfig mapping.
func TestBuildCommand_SourceReposInjected(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend", Groups: []string{"infra"}},
			{Name: "frontend", SourceRepoID: "src-frontend"},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", Repos: []string{"backend"}},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Backend agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}

	if v, ok := envMap["LOOM_SOURCE_REPOS"]; !ok || v != "src-backend" {
		t.Errorf("LOOM_SOURCE_REPOS = %q, want %q", v, "src-backend")
	}
}

// TestBuildCommand_SourceReposAbsentWhenEmpty verifies LOOM_SOURCE_REPOS is not
// set when the agent has no repo affinity.
func TestBuildCommand_SourceReposAbsentWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend"},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Generic agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_SOURCE_REPOS=") {
			t.Error("LOOM_SOURCE_REPOS should not be set when agent has no repo affinity")
		}
	}
}

// TestBuildCommand_NoConstraints_BackwardCompat verifies no constraint env vars
// are set when role has no tool constraints.
func TestBuildCommand_NoConstraints_BackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"}, // no constraints
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ALLOWED_TOOLS=") {
			t.Error("LOOM_ALLOWED_TOOLS should not be set when AllowedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_DENIED_TOOLS=") {
			t.Error("LOOM_DENIED_TOOLS should not be set when DeniedTools is empty")
		}
		if strings.HasPrefix(env, "LOOM_READ_ONLY=") {
			t.Error("LOOM_READ_ONLY should not be set when ReadOnly is false")
		}
	}
}

// TestBuildCommand_ErrorOnUnresolvableRepos verifies buildCommand returns an error
// when the agent declares repo affinity but all groups are unknown.
func TestBuildCommand_ErrorOnUnresolvableRepos(t *testing.T) {
	tmpDir := t.TempDir()

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Repos: []cfgpkg.RepoConfig{
			{Name: "backend", SourceRepoID: "src-backend", Groups: []string{"infra"}},
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", RepoGroups: []string{"nonexistent"}},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Agent with bad group"},
		WorktreePath: tmpDir,
	}

	_, err := s.buildCommand(ap)
	if err == nil {
		t.Fatal("expected error from buildCommand when repo groups are unresolvable, got nil")
	}
	if !strings.Contains(err.Error(), "resolve agent repos") {
		t.Errorf("error = %q, want it to contain 'resolve agent repos'", err.Error())
	}
}

// TestBuildCommand_DaemonSocketEnvVar verifies LOOM_DAEMON_SOCKET is set
// when ipcSocketPath is non-empty and omitted when empty.
func TestBuildCommand_DaemonSocketEnvVar(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("ipcSocketPath set propagates LOOM_DAEMON_SOCKET", func(t *testing.T) {
		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			IpcSocketPath:  "/tmp/test-ipc/agent-ipc.sock",
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		found := false
		for _, env := range cmd.Env {
			if env == "LOOM_DAEMON_SOCKET=/tmp/test-ipc/agent-ipc.sock" {
				found = true
			}
		}
		if !found {
			t.Error("LOOM_DAEMON_SOCKET=/tmp/test-ipc/agent-ipc.sock not found in cmd.Env")
		}
	})

	t.Run("empty ipcSocketPath omits LOOM_DAEMON_SOCKET", func(t *testing.T) {
		// Clear any inherited LOOM_DAEMON_SOCKET from parent env
		t.Setenv("LOOM_DAEMON_SOCKET", "")
		os.Unsetenv("LOOM_DAEMON_SOCKET")

		s := &Supervisor{
			ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
			ProjectDir:     tmpDir,
			Shutdown:       make(chan struct{}),
			StoppedAgents:  make(map[string]struct{}),
			EmitEvent:      func(events.Event) {},
		}
		ap := &AgentProcess{
			Entry:        cfgpkg.AgentEntry{Worktree: "hawk", Role: "task"},
			RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in task agent"},
			WorktreePath: tmpDir,
		}

		cmd, err := s.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand error: %v", err)
		}
		for _, env := range cmd.Env {
			if strings.HasPrefix(env, "LOOM_DAEMON_SOCKET=") {
				t.Errorf("LOOM_DAEMON_SOCKET should not be in cmd.Env when ipcSocketPath is empty, got: %s", env)
			}
		}
	})
}

// TestDaemonStop_ClosesConcurrencyTracker verifies that Daemon.Stop() calls
// concurrency.Close() to unblock waiters.
func TestDaemonStop_ClosesConcurrencyTracker(t *testing.T) {
	limit := 1
	ct := NewConcurrencyTracker(map[string]cfgpkg.RoleConfig{
		"task": {MaxConcurrency: &limit},
	})

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{
				Daemon: cfgpkg.DaemonSettings{
					RestartPolicy: cfgpkg.RestartPolicy{
						MaxRetries:     cfgpkg.IntPtr(0),
						BackoffInitial: cfgpkg.IntPtr(1),
						BackoffMax:     cfgpkg.IntPtr(1),
					},
				},
			}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        []*AgentProcess{},
		EmitEvent:     func(events.Event) {},
		Concurrency:   ct,
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Fill the slot
	ct.Acquire("task")

	// Try to acquire in background — should block
	acquired := make(chan bool, 1)
	go func() {
		acquired <- ct.Acquire("task")
	}()

	// Stop should close the tracker and unblock the goroutine
	s.Stop()

	result := <-acquired
	if result {
		t.Error("Acquire after Stop should return false (tracker closed)")
	}
}

func TestAgentProcess_resolveRemote(t *testing.T) {
	tests := []struct {
		name       string
		repoConfig *cfgpkg.RepoConfig
		want       string
	}{
		{"nil config", nil, "origin"},
		{"empty remote", &cfgpkg.RepoConfig{}, "origin"},
		{"custom remote", &cfgpkg.RepoConfig{Remote: "upstream"}, "upstream"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ap := &AgentProcess{RepoConfig: tc.repoConfig}
			got := ap.ResolveRemote()
			if got != tc.want {
				t.Errorf("resolveRemote() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentProcess_resolveRemoteBranch(t *testing.T) {
	tests := []struct {
		name       string
		repoConfig *cfgpkg.RepoConfig
		want       string
	}{
		{"nil config", nil, "origin/main"},
		{"empty config", &cfgpkg.RepoConfig{}, "origin/main"},
		{"custom remote and branch", &cfgpkg.RepoConfig{Remote: "upstream", DefaultBranch: "develop"}, "upstream/develop"},
		{"custom branch only", &cfgpkg.RepoConfig{DefaultBranch: "develop"}, "origin/develop"},
		{"custom remote only", &cfgpkg.RepoConfig{Remote: "upstream"}, "upstream/main"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ap := &AgentProcess{RepoConfig: tc.repoConfig}
			got := ap.ResolveRemoteBranch()
			if got != tc.want {
				t.Errorf("resolveRemoteBranch() = %q, want %q", got, tc.want)
			}
		})
	}
}
