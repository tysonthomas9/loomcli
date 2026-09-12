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
		{"claims-held → uncounted fixed recheck", agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome),
			Disposition{Decision: RetryUncounted, Backoff: BPClaimsHeld}},
		{"supervisor-stop → uncounted (our kill, not the agent's fault)", agenterr.OutcomeFromDomain(agenterr.SupervisorStopOutcome),
			Disposition{Decision: RetryUncounted, Backoff: BPDefault}},
		// A time-budget overrun, not a fault: counted retry (the checkpoint and
		// harness session id survive, so resume is right) on the timeout backoff
		// bucket, with a bounded block so a task that can NEVER finish inside
		// the budget surfaces as failed instead of consuming turns forever.
		{"run-turn-deadline → retry/block (capped), timeout backoff", agenterr.OutcomeFromDomain(agenterr.RunTurnDeadlineOutcome),
			Disposition{Decision: Retry, Backoff: BPTimeout, OnExhaustion: Block, BlockBudget: defaultBlockBudget}},
		{"incomplete-run → retry/block (capped)", agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome),
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

// TestQuarantineEligible pins the task-quarantine BUCKET for every Outcome the
// supervisor can observe — like TestDecide_Golden, this table IS the contract;
// changes are deliberate behavior changes. QuarantineEligible is asserted
// alongside as the derived "any bucket" answer.
func TestQuarantineEligible(t *testing.T) {
	cases := []struct {
		name string
		in   agenterr.Outcome
		want QuarantineBucket
	}{
		// harness-output classes
		{"none → not eligible (clean)", agenterr.OutcomeFromHarness(wrapper.ErrNone), QuarantineNone},
		{"rate-limited → not eligible (backend-wide)", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), QuarantineNone},
		{"auth → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrAuth), QuarantineNone},
		{"billing → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrBilling), QuarantineNone},
		{"model-not-found → not eligible (operator-actionable)", agenterr.OutcomeFromHarness(wrapper.ErrModelNotFound), QuarantineNone},
		{"context-overflow → eligible (task boomerangs across siblings)", agenterr.OutcomeFromHarness(wrapper.ErrContextOverflow), QuarantineNoProgress},
		{"timeout → eligible (137 watchdog kill)", agenterr.OutcomeFromHarness(wrapper.ErrTimeout), QuarantineNoProgress},
		{"transient → eligible (143 watchdog kill)", agenterr.OutcomeFromHarness(wrapper.ErrTransient), QuarantineNoProgress},
		{"unknown → eligible (-1 signal death)", agenterr.OutcomeFromHarness(wrapper.ErrUnknown), QuarantineNoProgress},
		// loom-domain outcomes: coordination signals, never task-fault
		{"no-work → not eligible", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), QuarantineNone},
		{"lock-conflict → not eligible", agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), QuarantineNone},
		{"spawn-failure → not eligible", agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome), QuarantineNone},
		{"backend-unavailable → not eligible", agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome), QuarantineNone},
		{"completion-hook-failure → not eligible (supervisor write fault, not task fault)", agenterr.OutcomeFromDomain(agenterr.CompletionHookFailureOutcome), QuarantineNone},
		{"claims-held → not eligible (operator quiesce, not task fault)", agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome), QuarantineNone},
		{"incomplete-run → not eligible (turn ran out, agent may be progressing)", agenterr.OutcomeFromDomain(agenterr.IncompleteRunOutcome), QuarantineNone},
		{"issue-backend-outage → not eligible (the store is down, the task is fine)", agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome), QuarantineNone},
		{"supervisor-stop → not eligible (we killed the run; the task earned nothing against it)", agenterr.OutcomeFromDomain(agenterr.SupervisorStopOutcome), QuarantineNone},
		// The ONE eligible domain outcome, and it gets its OWN bucket: loom's
		// per-turn deadline is a designed clean stop, so it must not advance the
		// no-progress crash counter — but a task that overruns every time still
		// boomerangs, so it is counted on a separate, higher threshold.
		{"run-turn-deadline → deadline bucket, NOT the no-progress one", agenterr.OutcomeFromDomain(agenterr.RunTurnDeadlineOutcome), QuarantineDeadline},
		// zero value (clean success)
		{"zero outcome → not eligible", agenterr.Outcome{}, QuarantineNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := QuarantineBucketFor(tc.in); got != tc.want {
				t.Fatalf("QuarantineBucketFor(%s) = %v, want %v", tc.in, got, tc.want)
			}
			// The thin wrapper must stay exactly "any bucket at all".
			if got, want := QuarantineEligible(tc.in), tc.want != QuarantineNone; got != want {
				t.Fatalf("QuarantineEligible(%s) = %v, want %v", tc.in, got, want)
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

// An issue-backend outage is shared by every agent in the fleet and clears on
// its own, so it must be uncounted: a counted retry escalates through Block
// into FastFail and takes the whole fleet down over infrastructure no agent
// can influence. See PUPPET-210.
func TestDecide_IssueBackendOutage_IsUncountedAndNeverTerminal(t *testing.T) {
	d := Decide(agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome))
	if d.Decision != RetryUncounted {
		t.Fatalf("Decision = %v, want RetryUncounted", d.Decision)
	}
	if d.Backoff != BPIssueBackendOutage {
		t.Errorf("Backoff = %v, want BPIssueBackendOutage (a fixed recheck, not an exponential ramp)", d.Backoff)
	}
	if d.OnExhaustion == Block || d.OnExhaustion == FastFail {
		t.Errorf("OnExhaustion = %v, want no exhaustion path at all", d.OnExhaustion)
	}
}
