package agentpolicy

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// End-to-end over the seam that matters: the text the leaf emits when the
// harness declares a turn terminal, through classification, to the decision
// the supervisor acts on. The classes and the dispositions are pinned
// separately elsewhere; what this pins is that they are wired to each other,
// so a change to either side that breaks the pairing fails here.
func TestBlamelessTerminalReasons_DoNotConsumeBudget(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		wantDecision Decision
		// A blameless outcome must never push a task toward quarantine: the
		// task is fine, the account or the quota window is not.
		wantQuarantine bool
	}{
		{
			name: "an expired login stops for the operator rather than retrying",
			text: agenterr.AuthRequiredMarker + ": auth_required",
			// Retrying a turn that needs a human to log in cannot succeed, and
			// each attempt is a real spawn. Stop and say why.
			wantDecision: StopFatal,
		},
		{
			name: "an exhausted quota retries without spending the restart budget",
			text: agenterr.UsageLimitedMarker + ": usage_limit",
			// The window reopens on its own, so this must be RetryUncounted:
			// counted retries would exhaust the budget during a quota window
			// and park an agent that was never broken.
			wantDecision: RetryUncounted,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ae := agenterr.ClassifyFromOutput(c.text, 1, "claude")
			if ae == nil {
				t.Fatal("a marked terminal reason must classify")
			}
			if got := Decide(ae.Class).Decision; got != c.wantDecision {
				t.Errorf("Decide(%v).Decision = %v, want %v", ae.Class, got, c.wantDecision)
			}
			if got := QuarantineEligible(ae.Class); got != c.wantQuarantine {
				t.Errorf("QuarantineEligible(%v) = %v, want %v", ae.Class, got, c.wantQuarantine)
			}
		})
	}
}
