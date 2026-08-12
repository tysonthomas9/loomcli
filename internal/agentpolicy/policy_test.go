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
		{"model-not-found → failover, fast-fail on exhaustion (never block)", agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound),
			Disposition{Decision: Failover, Backoff: BPDefault, OnExhaustion: FastFail}},
		{"context-overflow → fast-fail", agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow),
			Disposition{Decision: FastFail}},
		{"rate-limited → uncounted, failover-after-3", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
			Disposition{Decision: RetryUncounted, Backoff: BPRateLimit, HonorHint: true, FailoverAfter: 3, OnExhaustion: RetryUncounted}},
		{"timeout → retry/block (uncapped)", agenterr.OutcomeFromHarness(wrapper.ErrTimeout),
			Disposition{Decision: Retry, Backoff: BPTimeout, HonorHint: true, OnExhaustion: Block, BlockBudget: 0}},
		{"transient → retry/block (uncapped)", agenterr.OutcomeFromHarness(wrapper.ErrTransient),
			Disposition{Decision: Retry, Backoff: BPDefault, HonorHint: true, OnExhaustion: Block, BlockBudget: 0}},
		{"unknown → retry/block (capped)", agenterr.OutcomeFromHarness(wrapper.ErrUnknown),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
		// loom-domain outcomes
		{"no-work → uncounted poll", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
			Disposition{Decision: RetryUncounted, Backoff: BPNoWork}},
		{"backend-unavailable → block/recheck", agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome),
			Disposition{Decision: Block, Backoff: BPBackendUnavailable}},
		{"lock-conflict → retry/block (capped)", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
		{"spawn-failure → retry/block (capped)", agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
		{"completion-hook-failure → retry/block (capped)", agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome),
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
		// zero value (clean) — defensive conservative restart
		{"zero outcome → conservative retry", agenterr.Outcome{},
			Disposition{Decision: Retry, Backoff: BPDefault, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.in); got != tc.want {
				t.Fatalf("Decide(%s) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestQuarantineEligible pins the task-quarantine eligibility for every
// Outcome the supervisor can observe — like TestDecide_Golden, this table IS
// the contract; changes are deliberate behavior changes.
func TestQuarantineEligible(t *testing.T) {
	cases := []struct {
		name string
		in   agenterr.Outcome
		want bool
	}{
		// harness-output classes
		{"none → not eligible (clean)", agenterr.OutcomeFromHarness(wrapper.ErrNone), false},
		{"rate-limited → not eligible (backend-wide)", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), false},
		{"auth → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrAuth), false},
		{"billing → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrBilling), false},
		{"model-not-found → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound), false},
		{"context-overflow → eligible (task boomerangs across siblings)", agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow), true},
		{"timeout → eligible (137 watchdog kill)", agenterr.OutcomeFromHarness(wrapper.ErrTimeout), true},
		{"transient → eligible (143 watchdog kill)", agenterr.OutcomeFromHarness(wrapper.ErrTransient), true},
		{"unknown → eligible (-1 signal death)", agenterr.OutcomeFromHarness(wrapper.ErrUnknown), true},
		// loom-domain outcomes: coordination signals, never task-fault
		{"no-work → not eligible", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), false},
		{"lock-conflict → not eligible", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), false},
		{"spawn-failure → not eligible", agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome), false},
		{"backend-unavailable → not eligible", agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome), false},
		{"completion-hook-failure → not eligible (supervisor write fault, not task fault)", agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome), false},
		// zero value (clean success)
		{"zero outcome → not eligible", agenterr.Outcome{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := QuarantineEligible(tc.in); got != tc.want {
				t.Fatalf("QuarantineEligible(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDecide_DeterministicNeverBlocks is the headline behavior guard (the bug
// behind PR #124's review): genuinely-deterministic classes must NOT land in
// an unbounded block — they fast-fail (directly, or after a capped block / after
// backends are exhausted).
func TestDecide_DeterministicNeverBlocks(t *testing.T) {
	for _, c := range []wrapper.ErrorClass{wrapper.ErrModelNotFound, wrapper.ErrContextOverflow} {
		d := Decide(agenterr.OutcomeFromHarness(c))
		if d.Decision == Block {
			t.Errorf("%v: Decision = Block, want a terminal/failover decision", c)
		}
		if d.OnExhaustion == Block && d.BlockBudget == 0 {
			t.Errorf("%v: escalates to an UNBOUNDED block, want bounded/fast-fail", c)
		}
	}
	// Unknown may block, but only with a finite budget that escalates.
	u := Decide(agenterr.OutcomeFromHarness(wrapper.ErrUnknown))
	if u.OnExhaustion == Block && u.BlockBudget <= 0 {
		t.Errorf("Unknown blocks unbounded (BlockBudget=%d), want a finite cap", u.BlockBudget)
	}
}
