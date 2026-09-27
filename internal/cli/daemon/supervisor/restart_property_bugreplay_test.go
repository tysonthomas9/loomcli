//go:build daemon_bugreplay

package supervisor

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
	"github.com/tysonthomas9/loomcli/internal/agenterr"
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
