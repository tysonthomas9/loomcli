package supervisor

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

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
		ap := &AgentProcess{
			RestartCount: 0,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
		backoff := s.computeBackoff(ap)

		// initial * 2^0 = 2 * 1 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=1 returns 4s", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 1,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
		backoff := s.computeBackoff(ap)

		// initial * 2^1 = 2 * 2 = 4s
		want := 4 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=5 returns 64s", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 5,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
		backoff := s.computeBackoff(ap)

		// initial * 2^5 = 2 * 32 = 64s
		want := 64 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("large restartCount is capped at BackoffMax", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 20,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
		backoff := s.computeBackoff(ap)

		// 2 * 2^20 = 2097152s, should be capped at 300s
		want := 300 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=7 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 7,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
		backoff := s.computeBackoff(ap)

		// 2 * 2^7 = 256s, which is < 300s, so not capped
		want := 256 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("restartCount=8 exceeds max and is capped", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 8,
			// A crash always classifies (exit != 0 -> ClassifyFromLog), so the
			// exponential arm is reached with an error attached; a bare nil
			// error now means clean success and takes the cadence floor.
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)},
		}
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
			LastError:      &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)},
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
				Class:      agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
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
				Class:      agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
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
			LastError:      &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)},
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
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)},
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
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)},
		}
		backoff := s.computeBackoff(ap)

		// default backoff_initial=2 * 2^0 = 2s
		want := 2 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})

	t.Run("nil lastError is a clean success and takes the cadence floor", func(t *testing.T) {
		// The pre-floor contract routed nil errors into the exponential arm;
		// E3 replaced it: a clean success has no failure to back off from,
		// and its only bound is the success cadence floor.
		ap := &AgentProcess{
			RestartCount: 1,
			LastError:    nil,
			LastStart:    time.Now().Add(-time.Minute),
		}
		if backoff := s.computeBackoff(ap); backoff != 0 {
			t.Errorf("computeBackoff() = %v, want 0 (floor already elapsed)", backoff)
		}
	})

	t.Run("NoWork uses fixed no_work_backoff", func(t *testing.T) {
		ap := &AgentProcess{
			RestartCount: 5, // should be irrelevant
			LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)},
		}
		backoff := s.computeBackoff(ap)

		// default no_work_backoff=30s (fixed, no exponential)
		want := 30 * time.Second
		if backoff != want {
			t.Errorf("computeBackoff() = %v, want %v", backoff, want)
		}
	})
}
