package agenterr

import (
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// The whole point of the markers is that they beat the prose. These pin the
// classes; the policy consequences that follow from them (uncounted retry, no
// quarantine) are pinned next to the policy table in internal/agentpolicy.
func TestClassify_HarnessTerminalReasonsAreBlameless(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		class wrapper.ErrorClass
	}{
		{
			name:  "auth required",
			text:  AuthRequiredMarker + ": auth_required: harness login expired or re-authentication required",
			class: wrapper.ErrAuth,
		},
		{
			name:  "usage limited",
			text:  UsageLimitedMarker + ": usage_limit: harness usage or session limit reached",
			class: wrapper.ErrRateLimited,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ae := ClassifyFromOutput(c.text, 1, "claude")
			if ae == nil {
				t.Fatal("a terminal harness reason must classify")
			}
			if !ae.Class.IsClass(c.class) {
				t.Fatalf("Class = %v, want %v", ae.Class, c.class)
			}
		})
	}
}

// An unfamiliar reason must NOT get a marker's treatment. The marker means
// "the harness told us what this is"; anything else stays on the ordinary
// path, where a wrong guess is at least a guess the existing table owns.
func TestClassify_UnmarkedTurnErrorIsNotBlameless(t *testing.T) {
	ae := ClassifyFromOutput("claude turn errored: the model produced no output", 1, "claude")
	if ae == nil {
		t.Fatal("an errored turn must classify")
	}
	if ae.Class.IsClass(wrapper.ErrAuth) || ae.Class.IsClass(wrapper.ErrRateLimited) {
		t.Fatalf("Class = %v, want a non-blameless class for an unnamed failure", ae.Class)
	}
}

// A usage wall usually says when it lifts. Honoring it is the difference
// between backing off for the stated window and backing off for the default.
func TestClassify_UsageLimitHonorsRetryAfter(t *testing.T) {
	text := UsageLimitedMarker + ": usage_limit reached; retry-after: 1800"
	ae := ClassifyFromOutput(text, 1, "claude")
	if ae == nil {
		t.Fatal("expected a classification")
	}
	if ae.RetryAfter != 30*time.Minute {
		t.Fatalf("RetryAfter = %v, want 30m from the stated window", ae.RetryAfter)
	}
}

// The markers outrank the residual patterns, not the other way round. A quota
// message that also happens to contain an auth-shaped phrase must still be
// classified from the marker the harness emitted.
func TestClassify_MarkerBeatsResidualPatterns(t *testing.T) {
	text := UsageLimitedMarker + ": usage_limit reached (401 unauthorized mentioned in the wall text)"
	ae := ClassifyFromOutput(text, 1, "claude")
	if ae == nil || !ae.Class.IsClass(wrapper.ErrRateLimited) {
		t.Fatalf("Class = %v, want RateLimited from the marker rather than the prose", ae)
	}
}
