package agentpolicy

import (
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// TestDecide_Golden pins the disposition for every Outcome — this table IS
// the policy contract. Changes here are deliberate behavior changes.
func TestDecide_Golden(t *testing.T) {
	cases := []struct {
		name string
		in   agenterr.Outcome
		want Disposition
	}{
		// harness-output classes
		{"auth → stop-fatal", agenterr.OutcomeFromHarness(wrapper.ErrAuth),
			Disposition{Decision: StopFatal}},
		{"billing → stop-fatal", agenterr.OutcomeFromHarness(wrapper.ErrBilling),
			Disposition{Decision: StopFatal}},
		{"model-not-found → failover, fast-fail on exhaustion (never park)", agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound),
			Disposition{Decision: Failover, Backoff: BPDefault, OnExhaustion: FastFail}},
		{"context-overflow → fast-fail", agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow),
			Disposition{Decision: FastFail}},
		{"rate-limited → uncounted, failover-after-3", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
			Disposition{Decision: RetryUncounted, Backoff: BPRateLimit, HonorHint: true, FailoverAfter: 3, OnExhaustion: RetryUncounted}},
		{"timeout → retry/park (uncapped)", agenterr.OutcomeFromHarness(wrapper.ErrTimeout),
			Disposition{Decision: Retry, Backoff: BPTimeout, HonorHint: true, OnExhaustion: Park, ParkBudget: 0}},
		{"transient → retry/park (uncapped)", agenterr.OutcomeFromHarness(wrapper.ErrTransient),
			Disposition{Decision: Retry, Backoff: BPDefault, HonorHint: true, OnExhaustion: Park, ParkBudget: 0}},
		{"unknown → retry/park (capped)", agenterr.OutcomeFromHarness(wrapper.ErrUnknown),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Park, ParkBudget: defaultParkBudget}},
		// loom-domain outcomes
		{"no-work → uncounted poll", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
			Disposition{Decision: RetryUncounted, Backoff: BPNoWork}},
		{"backend-unavailable → park/recheck", agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome),
			Disposition{Decision: Park, Backoff: BPBackendUnavailable}},
		{"lock-conflict → retry/park (capped)", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Park, ParkBudget: defaultParkBudget}},
		{"spawn-failure → retry/park (capped)", agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Park, ParkBudget: defaultParkBudget}},
		// zero value (clean) — defensive conservative restart
		{"zero outcome → conservative retry", agenterr.Outcome{},
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Park, ParkBudget: defaultParkBudget}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.in); got != tc.want {
				t.Fatalf("Decide(%s) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDecide_DeterministicNeverParks is the headline behavior guard (the bug
// behind PR #124's review): genuinely-deterministic classes must NOT land in
// an unbounded park — they fast-fail (directly, or after a capped park / after
// backends are exhausted).
func TestDecide_DeterministicNeverParks(t *testing.T) {
	for _, c := range []wrapper.ErrorClass{wrapper.ErrModelNotFound, wrapper.ErrContextOverflow} {
		d := Decide(agenterr.OutcomeFromHarness(c))
		if d.Decision == Park {
			t.Errorf("%v: Decision = Park, want a terminal/failover decision", c)
		}
		if d.OnExhaustion == Park && d.ParkBudget == 0 {
			t.Errorf("%v: escalates to an UNBOUNDED park, want bounded/fast-fail", c)
		}
	}
	// Unknown may park, but only with a finite budget that escalates.
	u := Decide(agenterr.OutcomeFromHarness(wrapper.ErrUnknown))
	if u.OnExhaustion == Park && u.ParkBudget <= 0 {
		t.Errorf("Unknown parks unbounded (ParkBudget=%d), want a finite cap", u.ParkBudget)
	}
}
