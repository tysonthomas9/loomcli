//go:build daemon_bugreplay

package supervisor

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// Bug-replay fault tests, group "claims" (claim/ownership/lease, bucket C).
// Catalogue row #766; no model invariant (bucket C). The local invariant is
// "losing a claim race to a live sibling is an idle non-fault": it never
// charges the restart or block budgets and never stops the supervisor.
//
// v5 @ 1c6dabfc8: internal/agentpolicy/policy.go:151 maps LockConflictOutcome
// to a counted Retry with a Block budget, so shouldRestart walks the agent
// through applyCountedRestart -> applyMaxRetriesBlock -> StopReasonFastFail.
// Expected: FAIL on v5, PASS on the #766 head (259bf12b5).

func claimsLockConflictAgent() *AgentProcess {
	return &AgentProcess{
		LastExitCode: 0,
		LastStart:    time.Now(),
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome)},
	}
}

func claimsSupervisorWithMaxRetries(maxRetries int) *Supervisor {
	return newTestSupervisorWithConfig(&config.DaemonConfig{
		Daemon: config.DaemonSettings{RestartPolicy: config.RestartPolicy{MaxRetries: &maxRetries}},
	})
}

// TestBugReplay_PR766_LockConflictNeverStopsSupervisor replays the PUPPET-623
// symptom: surplus siblings that keep losing claim races must stay alive.
func TestBugReplay_PR766_LockConflictNeverStopsSupervisor(t *testing.T) {
	const maxRetries = 2
	s := claimsSupervisorWithMaxRetries(maxRetries)
	ap := claimsLockConflictAgent()

	// Well past max_retries * (block budget + 1), the v5 fast-fail point.
	for cycle := 1; cycle <= (maxRetries+1)*4+5; cycle++ {
		if !s.shouldRestart(ap) {
			t.Fatalf("cycle %d: shouldRestart = false, StopReason = %q; a lost claim race must not stop the supervisor",
				cycle, ap.StopReason)
		}
		if ap.StopReason == StopReasonMaxRetriesBlocked || ap.StopReason == StopReasonFastFail {
			t.Fatalf("cycle %d: StopReason = %q; lock contention must not escalate", cycle, ap.StopReason)
		}
	}
}

// TestBugReplay_PR766_LockConflictDoesNotChargeBudget checks one cycle from an
// already exhausted budget: RestartCount and BlockCount must not grow.
// (Whether RestartCount is refunded is bug #760, not this one, so the check
// is "not incremented", not "unchanged".)
func TestBugReplay_PR766_LockConflictDoesNotChargeBudget(t *testing.T) {
	const maxRetries = 2
	s := claimsSupervisorWithMaxRetries(maxRetries)
	ap := claimsLockConflictAgent()
	ap.RestartCount = maxRetries

	if !s.shouldRestart(ap) {
		t.Fatalf("shouldRestart = false, StopReason = %q; want restart", ap.StopReason)
	}
	if ap.RestartCount > maxRetries {
		t.Errorf("RestartCount = %d, want <= %d: a lost claim race was charged to the restart budget",
			ap.RestartCount, maxRetries)
	}
	if ap.BlockCount != 0 {
		t.Errorf("BlockCount = %d, want 0: a lost claim race was charged to the block budget", ap.BlockCount)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want empty (alive and idle)", ap.StopReason)
	}
}
