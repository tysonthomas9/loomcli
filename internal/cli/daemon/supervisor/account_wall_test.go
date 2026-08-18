package supervisor

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newAccountWallSupervisor builds a supervisor whose restart policy carries an
// explicit account-wall cooldown (nil ⇒ package default).
func newAccountWallSupervisor(cooldownSeconds *int) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{
				Daemon: cfgpkg.DaemonSettings{
					RestartPolicy: cfgpkg.RestartPolicy{AccountWallCooldown: cooldownSeconds},
				},
				Backend: "codex",
			}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
}

func newAccountWallAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "heron", Role: "task", Backend: "codex"},
		RoleConfig: cfgpkg.RoleConfig{Description: "test"},
	}
}

// TestGateAccountWall_ParksAgentAndPreservesBudget is the point of the whole
// gate: a wall another agent hit must stop this one BEFORE it claims a task,
// without spending any of its restart budget.
func TestGateAccountWall_ParksAgentAndPreservesBudget(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2

	s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrBilling),
		Message: "credit balance too low",
	})

	err := s.gateAccountWall(ap)
	if !errors.Is(err, ErrAccountWall) {
		t.Fatalf("gateAccountWall() = %v, want ErrAccountWall", err)
	}
	if ap.StopReason != StopReasonAccountWall {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonAccountWall)
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (a park must not erode the budget)", ap.RestartCount)
	}
	if ap.LastError == nil {
		t.Fatal("LastError must be populated")
	}
	if !ap.LastError.Class.IsClass(wrapper.ErrBilling) {
		t.Errorf("LastError.Class = %v, want Billing", ap.LastError.Class)
	}
	if ap.LastError.Message != "credit balance too low" {
		t.Errorf("LastError.Message = %q, want the recorded wall message", ap.LastError.Message)
	}
}

// TestGateAccountWall_ExpiredWallClearsPark covers the recovery branch: at
// expiry the agent resumes on its own, with its parked state cleared.
func TestGateAccountWall_ExpiredWallClearsPark(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonAccountWall
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth)}

	s.WallUntil = time.Now().Add(-time.Second)
	s.WallClass = agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	if err := s.gateAccountWall(ap); err != nil {
		t.Fatalf("gateAccountWall() = %v, want nil once the wall expired", err)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want cleared", ap.StopReason)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v, want cleared", ap.LastError)
	}
}

// TestGateAccountWall_NoWallIsNoOp guards the common case: with no wall armed
// the gate must not touch an agent that was never parked.
func TestGateAccountWall_NoWallIsNoOp(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonRateLimited

	if err := s.gateAccountWall(ap); err != nil {
		t.Fatalf("gateAccountWall() = %v, want nil", err)
	}
	if ap.StopReason != StopReasonRateLimited {
		t.Errorf("StopReason = %q, want it left alone", ap.StopReason)
	}
}

func TestRecordAccountWall(t *testing.T) {
	authOutcome := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	t.Run("non-wall class records nothing", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrTransient), &agenterr.AgentError{})
		if _, _, ok := s.accountWallActive(); ok {
			t.Error("a transient error must not park the fleet")
		}
	})

	t.Run("domain outcome records nothing", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), nil)
		if _, _, ok := s.accountWallActive(); ok {
			t.Error("NoWork must not park the fleet")
		}
	})

	t.Run("cooldown 0 disables the gate", func(t *testing.T) {
		zero := 0
		s := newAccountWallSupervisor(&zero)
		s.recordAccountWall(authOutcome, &agenterr.AgentError{Message: "bad key"})
		if _, _, ok := s.accountWallActive(); ok {
			t.Error("account_wall_cooldown: 0 must record nothing")
		}
	})

	t.Run("default cooldown", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(authOutcome, nil)
		remaining, _, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining <= defaultAccountWallCooldown-time.Minute || remaining > defaultAccountWallCooldown {
			t.Errorf("remaining = %s, want ~%s", remaining, defaultAccountWallCooldown)
		}
	})

	t.Run("RetryAfter hint wins over the default", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), &agenterr.AgentError{
			RetryAfter: 90 * time.Second,
		})
		remaining, _, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining > 90*time.Second || remaining < 80*time.Second {
			t.Errorf("remaining = %s, want ~90s from the RetryAfter hint", remaining)
		}
	})

	t.Run("absurd RetryAfter is capped", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(authOutcome, &agenterr.AgentError{RetryAfter: 24 * time.Hour})
		remaining, _, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining > maxAccountWallCooldown {
			t.Errorf("remaining = %s, want capped at %s", remaining, maxAccountWallCooldown)
		}
	})

	t.Run("never shortens a live wall", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(authOutcome, &agenterr.AgentError{RetryAfter: 30 * time.Minute, Message: "long"})
		s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
			RetryAfter: time.Minute, Message: "short",
		})
		remaining, message, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining < 29*time.Minute {
			t.Errorf("remaining = %s, want the longer wall preserved", remaining)
		}
		if message != "long" {
			t.Errorf("message = %q, want the longer wall's message preserved", message)
		}
	})

	t.Run("extends when the new expiry is later", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordAccountWall(authOutcome, &agenterr.AgentError{RetryAfter: time.Minute})
		s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
			RetryAfter: 40 * time.Minute, Message: "billing",
		})
		remaining, message, _ := s.accountWallActive()
		if remaining < 39*time.Minute {
			t.Errorf("remaining = %s, want the wall extended", remaining)
		}
		if message != "billing" {
			t.Errorf("message = %q, want the later wall's message", message)
		}
	})
}

// TestGetAccountWallCooldown_EnvOverride: the env var wins over config, which
// is what lets an integration test arm and expire a wall in seconds.
func TestGetAccountWallCooldown_EnvOverride(t *testing.T) {
	cfgSeconds := 60
	s := newAccountWallSupervisor(&cfgSeconds)
	if got := s.getAccountWallCooldown(); got != time.Minute {
		t.Fatalf("config cooldown = %s, want 1m", got)
	}
	t.Setenv("LOOM_DAEMON_ACCOUNT_WALL_COOLDOWN_SECONDS", "5")
	if got := s.getAccountWallCooldown(); got != 5*time.Second {
		t.Errorf("env cooldown = %s, want 5s", got)
	}
}

// TestApplyFatalStop_RecordsAccountWall wires the fatal path to the gate: a
// billing wall stopping one agent must park the rest.
func TestApplyFatalStop_RecordsAccountWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrBilling),
		Message: "insufficient credits",
	}

	if s.shouldRestart(ap) {
		t.Fatal("a billing wall must stop the agent")
	}
	if ap.StopReason != StopReasonFatalError {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonFatalError)
	}
	remaining, message, ok := s.accountWallActive()
	if !ok {
		t.Fatal("applyFatalStop must record the account wall")
	}
	if remaining <= 0 || message != "insufficient credits" {
		t.Errorf("wall = (%s, %q), want a live wall carrying the error message", remaining, message)
	}
}

// TestRateLimitUncountedPath_RecordsAccountWall covers the class that
// otherwise retries forever, per agent: a usage-limit wall.
func TestRateLimitUncountedPath_RecordsAccountWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
		Message: "usage limit reached; resets at 3pm",
	}

	if !s.shouldRestart(ap) {
		t.Fatal("a rate limit still retries uncounted for the agent that hit it")
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (uncounted retry)", ap.RestartCount)
	}
	if _, _, ok := s.accountWallActive(); !ok {
		t.Error("the RetryUncounted rate-limit path must record the account wall")
	}
}

// TestAccountWall_ConcurrentAccess exercises the WallMu contract under -race.
func TestAccountWall_ConcurrentAccess(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	outcome := agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.recordAccountWall(outcome, &agenterr.AgentError{
				RetryAfter: time.Duration(i+1) * time.Minute,
				Message:    "wall",
			})
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.accountWallActive()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.gateAccountWall(newAccountWallAgent()) //nolint:errcheck // exercising the lock, not the verdict
		}()
	}
	wg.Wait()

	if _, _, ok := s.accountWallActive(); !ok {
		t.Error("wall must be armed after concurrent records")
	}
}

// TestShouldRestart_ParkedAgentKeepsSupervising is the self-recovery contract:
// a parked agent must stay in the supervise loop with its budget intact. If it
// fell through to the fatal path instead, a wall recorded by one agent would
// permanently kill every other agent's supervise goroutine.
func TestShouldRestart_ParkedAgentKeepsSupervising(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2

	s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrBilling), Message: "no credits",
	})
	if err := s.gateAccountWall(ap); !errors.Is(err, ErrAccountWall) {
		t.Fatalf("gateAccountWall() = %v, want ErrAccountWall", err)
	}

	if !s.shouldRestart(ap) {
		t.Fatal("a parked agent must keep supervising, not stop")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (park must not erode the budget)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonAccountWall {
		t.Errorf("StopReason = %q, want it to stay %q while parked", ap.StopReason, StopReasonAccountWall)
	}
}

// TestComputeBackoff_ParkedAgentSleepsTheWall: the park sleeps out the wall
// rather than an exponential backoff keyed on someone else's error, and drops
// to zero the moment the wall lifts.
func TestComputeBackoff_ParkedAgentSleepsTheWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonAccountWall
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrBilling)}

	s.recordAccountWall(agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		RetryAfter: 10 * time.Minute,
	})
	got := s.computeBackoff(ap)
	if got > 10*time.Minute || got < 9*time.Minute {
		t.Errorf("computeBackoff() = %s, want ~10m (the remaining wall)", got)
	}

	s.WallUntil = time.Now().Add(-time.Second)
	if got := s.computeBackoff(ap); got != 0 {
		t.Errorf("computeBackoff() = %s after expiry, want 0", got)
	}
}
