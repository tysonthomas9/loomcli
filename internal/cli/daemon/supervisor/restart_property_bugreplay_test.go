//go:build daemon_bugreplay

package supervisor

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// A fixed default makes failures reproducible; DAEMON_PROPERTY_SEED selects a
// different run and is always printed with the failing sequence.
func propertySeed(t *testing.T) int64 {
	t.Helper()
	seed := int64(760)
	if value := os.Getenv("DAEMON_PROPERTY_SEED"); value != "" {
		var err error
		seed, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("DAEMON_PROPERTY_SEED: %v", err)
		}
	}
	t.Logf("seed=%d", seed)
	return seed
}

type restartEvent uint8

const (
	retryCrash restartEvent = iota
	idleNoWork
	credentialWall
	backendMissing
	claimHold
)

func (e restartEvent) String() string {
	return [...]string{"crash", "no-work", "rate-limit", "backend-missing", "claim-hold"}[e]
}

func setRestartEvent(ap *AgentProcess, e restartEvent) {
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrUnknown)}
	switch e {
	case idleNoWork:
		ap.LastError.Class = agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome)
	case credentialWall:
		ap.LastError.Class = agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)
	case backendMissing:
		ap.LastError.Class = agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome)
	case claimHold:
		ap.LastError.Class = agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome)
	}
}

// #760/N2: an idle poll must not give a previously failing agent fresh retry
// credit. The shortest counterexample is [crash, no-work]. Generated tails
// exercise the same invariant after other uncounted events and block cycles.
func TestPropertyRestartBudgetNotRefundedByNoWork(t *testing.T) {
	seed := propertySeed(t)
	rng := rand.New(rand.NewSource(seed))
	const iterations = 200
	const depth = 24
	states := make(map[string]struct{})
	executed, maxDepth := 0, 0
	defer func() { t.Logf("iterations=%d max_depth=%d distinct_states=%d", executed, maxDepth, len(states)) }()
	maxRetries := 3
	s := newTestSupervisorWithConfig(&config.DaemonConfig{Daemon: config.DaemonSettings{
		RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries},
	}})
	for iteration := 0; iteration < iterations; iteration++ {
		executed++
		ap := &AgentProcess{LastStart: time.Now()}
		trace := make([]restartEvent, 0, depth)
		for step := 0; step < depth; step++ {
			e := restartEvent(rng.Intn(5))
			// Exercise the minimal mechanism on every run, with generated
			// interleavings before and after it.
			if step == depth-2 {
				e = retryCrash
			}
			if step == depth-1 {
				e = idleNoWork
			}
			before := ap.RestartCount
			setRestartEvent(ap, e)
			restarted := s.shouldRestart(ap)
			trace = append(trace, e)
			if len(trace) > maxDepth {
				maxDepth = len(trace)
			}
			states[fmt.Sprintf("%d/%d/%s/%t", ap.RestartCount, ap.BlockCount, ap.StopReason, restarted)] = struct{}{}
			if e == idleNoWork && ap.RestartCount < before {
				minimal := &AgentProcess{}
				setRestartEvent(minimal, retryCrash)
				s.shouldRestart(minimal)
				charged := minimal.RestartCount
				setRestartEvent(minimal, idleNoWork)
				s.shouldRestart(minimal)
				t.Fatalf("#760 seed=%d iteration=%d step=%d trace=%v: no-work refunded %d -> %d; minimal [crash no-work] %d -> %d", seed, iteration, step, trace, before, ap.RestartCount, charged, minimal.RestartCount)
			}
			if !restarted {
				break
			}
		}
	}
}

// F14 and the uncounted side of #760: backend absence, credential walls and
// claim holds must preserve the charged retry count through arbitrary order.
func TestPropertyUncountedRestartEventsPreserveBudget(t *testing.T) {
	seed := propertySeed(t)
	rng := rand.New(rand.NewSource(seed + 1))
	const iterations = 200
	const depth = 24
	states := make(map[string]struct{})
	s := newTestSupervisorWithConfig(&config.DaemonConfig{})
	for iteration := 0; iteration < iterations; iteration++ {
		ap := &AgentProcess{RestartCount: 1 + rng.Intn(3), BlockCount: rng.Intn(3)}
		trace := make([]restartEvent, 0, depth)
		for step := 0; step < depth; step++ {
			e := [...]restartEvent{credentialWall, backendMissing, claimHold}[rng.Intn(3)]
			before := ap.RestartCount
			setRestartEvent(ap, e)
			restarted := s.shouldRestart(ap)
			trace = append(trace, e)
			states[fmt.Sprintf("%d/%d/%s/%t", ap.RestartCount, ap.BlockCount, ap.StopReason, restarted)] = struct{}{}
			if !restarted || ap.RestartCount != before {
				t.Fatalf("F14 seed=%d iteration=%d step=%d trace=%v: restarted=%t budget %d -> %d", seed, iteration, step, trace, restarted, before, ap.RestartCount)
			}
		}
	}
	t.Logf("iterations=%d depth=%d distinct_states=%d", iterations, depth, len(states))
}

// Backoff profile guard: generated timeout, transient, rate-limit and unknown
// exits must stay inside the selected profile's cap, even with a large hint.
func TestPropertyRestartBackoffIsBounded(t *testing.T) {
	seed := propertySeed(t)
	rng := rand.New(rand.NewSource(seed + 2))
	const iterations = 200
	classes := []wrapper.ErrorClass{wrapper.ErrUnknown, wrapper.ErrTimeout, wrapper.ErrTransient, wrapper.ErrRateLimited}
	states := make(map[string]struct{})
	for iteration := 0; iteration < iterations; iteration++ {
		max := 1 + rng.Intn(600)
		rateMax := 1 + rng.Intn(3600)
		initial := 1 + rng.Intn(max)
		cfg := &config.DaemonConfig{Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{
			BackoffInitial: &initial, BackoffMax: &max, RateLimitMaxWait: &rateMax,
		}}}
		s := newTestSupervisorWithConfig(cfg)
		class := classes[rng.Intn(len(classes))]
		count := rng.Intn(70)
		hint := time.Duration(rng.Intn(7200)) * time.Second
		ap := &AgentProcess{RestartCount: count, RateRetryCount: count,
			LastError: &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(class), RetryAfter: hint}}
		got := s.computeBackoff(ap)
		cap := time.Duration(max) * time.Second
		if class == wrapper.ErrRateLimited {
			cap = time.Duration(rateMax) * time.Second
		}
		states[fmt.Sprintf("%s/%d/%d/%d", class, count, cap/time.Second, got/time.Second)] = struct{}{}
		if got < 0 || got > cap {
			t.Fatalf("seed=%d iteration=%d class=%s count=%d initial=%d max=%d rateMax=%d hint=%v: backoff=%v outside [0,%v]", seed, iteration, class, count, initial, max, rateMax, hint, got, cap)
		}
	}
	t.Logf("iterations=%d depth=1 distinct_states=%d", iterations, len(states))
}

// #711/#520: a supervisor kill cannot become an agent fault because of the
// exit code or the last log line. Shutdown also covers a drain whose reason
// has not yet been written but whose shutdown channel is already closed.
func TestPropertySupervisorKillsAreBlameless(t *testing.T) {
	seed := propertySeed(t)
	rng := rand.New(rand.NewSource(seed + 3))
	const iterations = 100
	const depth = 8
	logPath := filepath.Join(t.TempDir(), "agent.log")
	reasons := []StopReason{StopReasonShutdown, StopReasonManualStop, StopReasonConfigRemoved, ""}
	outputs := []string{"authentication failed", "connection timed out", "ordinary task output"}
	states := make(map[string]struct{})
	executed, maxDepth := 0, 0
	defer func() { t.Logf("iterations=%d max_depth=%d distinct_states=%d", executed, maxDepth, len(states)) }()
	for iteration := 0; iteration < iterations; iteration++ {
		executed++
		trace := make([]string, 0, depth)
		for step := 0; step < depth; step++ {
			reason := reasons[rng.Intn(len(reasons))]
			exitCode := [...]int{137, 143, 1}[rng.Intn(3)]
			output := outputs[rng.Intn(len(outputs))]
			if err := os.WriteFile(logPath, []byte(output), 0600); err != nil {
				t.Fatal(err)
			}
			s := newTestSupervisorWithConfig(&config.DaemonConfig{})
			ap := &AgentProcess{WorktreePath: t.TempDir(), LogFilePath: logPath,
				StopReason: reason, StopCh: make(chan struct{})}
			ap.Entry.Backend = "claude"
			ap.AssignedTaskID = "task-1"
			if reason == "" {
				close(s.Shutdown) // drain signal arrives before StopReason
			}
			s.classifyAgentExit(ap, exitCode)
			trace = append(trace, fmt.Sprintf("%q/%d/%q", reason, exitCode, output))
			if len(trace) > maxDepth {
				maxDepth = len(trace)
			}
			if ap.LastError == nil {
				t.Fatalf("#711 seed=%d iteration=%d step=%d trace=%v: nonzero supervisor kill has no classified outcome", seed, iteration, step, trace)
			}
			outcome := ap.LastError.Class
			decision := agentpolicy.Decide(outcome).Decision
			eligible := agentpolicy.QuarantineEligible(outcome)
			states[fmt.Sprintf("%q/%d/%s/%s/%t", reason, exitCode, outcome, decision, eligible)] = struct{}{}
			if decision != agentpolicy.RetryUncounted || eligible {
				t.Fatalf("#711/#520 seed=%d iteration=%d step=%d trace=%v: outcome=%s decision=%s quarantine=%t", seed, iteration, step, trace, outcome, decision, eligible)
			}
		}
	}
}

// #760/#134/F15: after max_retries+1 counted failures without success,
// the supervisor must stop or visibly park. The property leaves that policy
// choice open; intervening idle polls may not postpone both outcomes forever.
func TestPropertyCountedFailuresReachBound(t *testing.T) {
	seed := propertySeed(t)
	rng := rand.New(rand.NewSource(seed + 4))
	const iterations = 200
	states := make(map[string]struct{})
	executed, maxDepth := 0, 0
	defer func() { t.Logf("iterations=%d max_depth=%d distinct_states=%d", executed, maxDepth, len(states)) }()
	for iteration := 0; iteration < iterations; iteration++ {
		executed++
		budget := 1 + rng.Intn(5)
		s := newTestSupervisorWithConfig(&config.DaemonConfig{Daemon: config.DaemonSettings{
			RestartPolicy: config.RestartPolicy{MaxRetries: &budget},
		}})
		ap := &AgentProcess{}
		trace := make([]restartEvent, 0, 2*(budget+1))
		bounded := false
		for failure := 0; failure <= budget; failure++ {
			setRestartEvent(ap, retryCrash)
			restarted := s.shouldRestart(ap)
			trace = append(trace, retryCrash)
			states[fmt.Sprintf("budget=%d,count=%d,blocks=%d,stop=%s", budget, ap.RestartCount, ap.BlockCount, ap.StopReason)] = struct{}{}
			if !restarted || ap.StopReason == StopReasonMaxRetriesBlocked {
				bounded = true
				break
			}
			if failure < budget {
				// Generate intervening non-successes. Include a no-work
				// poll after the first failure so the #760 mechanism is
				// exercised in every sequence.
				e := [...]restartEvent{idleNoWork, credentialWall, backendMissing, claimHold}[rng.Intn(4)]
				if failure == 0 {
					e = idleNoWork
				}
				setRestartEvent(ap, e)
				s.shouldRestart(ap)
				trace = append(trace, e)
			}
		}
		if len(trace) > maxDepth {
			maxDepth = len(trace)
		}
		if !bounded {
			t.Fatalf("#760 seed=%d iteration=%d budget=%d trace=%v: %d counted failures reached neither stop nor block (count=%d)", seed, iteration, budget, trace, budget+1, ap.RestartCount)
		}
	}
}
